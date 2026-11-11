package concurrency

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
)

// lease_acquire.lua grants a batch of scope buckets all-or-nothing (D-05);
// lease_release.lua releases the batch's member and runs the registry
// cleanup chain (D-17/D-18); lease_renew.lua batch-extends this node's held
// leases with ZADD XX (D-01/D-02); lease_flush.lua batch-clears buffered
// (member, key) pairs — pending releases after an outage and shutdown bulk
// cleanup share it (D-13/D-19). All are embedded so a deployment ships
// exactly one script version with the binary.
//
//go:embed lua/lease_acquire.lua
var leaseAcquireScript string

//go:embed lua/lease_release.lua
var leaseReleaseScript string

//go:embed lua/lease_renew.lua
var leaseRenewScript string

//go:embed lua/lease_flush.lua
var leaseFlushScript string

var (
	acquireScript = redis.NewScript(leaseAcquireScript)
	releaseScript = redis.NewScript(leaseReleaseScript)
	renewScript   = redis.NewScript(leaseRenewScript)
	flushScript   = redis.NewScript(leaseFlushScript)
)

const (
	// registryKeySuffix appends to the key prefix to form the known-scope
	// registry Set; scope names always start with "key:"/"user:"/"channel:",
	// so "<prefix>registry" can never collide with a scope key.
	registryKeySuffix = "registry"

	// leaseTTLEnv bounds a lease before Redis reclaims it — the safety net
	// for a lost release (process crash, network drop). D-03/D-04: seconds,
	// default 900 (15 minutes), comfortably above any in-flight request.
	leaseTTLEnv      = "REDIS_CONCURRENCY_LEASE_TTL_SECONDS"
	defaultLeaseTTLs = 900

	// redisOpTimeout bounds every Redis round-trip (D-06). Calls run on
	// context.Background() with this deadline — never on the caller's ctx —
	// so a client disconnecting mid-request cannot cancel an already-issued
	// acquire (slot leaking to TTL) and cannot manufacture phantom transport
	// errors. Adjustable constant; the D-11 breaker (02-10) removes the
	// per-request timeout cost during outages.
	redisOpTimeout = 200 * time.Millisecond

	// degradeWarnPrefix is the stable WARN marker emitted whenever the Redis
	// backend fails and the limiter fails open (D-08/D-10), so operators can
	// grep outage windows without matching incidental log noise.
	degradeWarnPrefix = "concurrency: redis"

	// renewalIntervalDivisor derives the renewal ticker interval from the
	// lease TTL (D-03/D-04): every TTL/3, so at least two renewal ticks land
	// before any held lease can expire (default 900s TTL → 5min interval).
	renewalIntervalDivisor = 3

	// renewStopWarnPrefix is the stable WARN marker for the D-02
	// stop-renewal event: a held lease was found already reclaimed, so this
	// node drops it and never re-acquires (fewer counts beat corruption).
	renewStopWarnPrefix = "concurrency: renewal stopped"

	// breakerCooldownEnv / defaultBreakerCooldown bound the D-11 cooldown
	// window: after the first backend failure the breaker opens for this long
	// and every request degrades directly with zero Redis calls; expiry admits
	// a single probe request (Fowler half-open trial call). Seconds, default
	// 5 — an adjustable magnitude constant (D-11/D-24); the cooldown is read
	// from the env at trip time so tests can inject a short window (same
	// pattern as leaseTTLEnv).
	breakerCooldownEnv     = "REDIS_CONCURRENCY_BREAKER_COOLDOWN_SECONDS"
	defaultBreakerCooldown = 5

	// breakerTripWarnPrefix / breakerRecoverWarnPrefix are the stable D-11
	// WARN markers: trip fires once per failed backend attempt (initial trip
	// or failed probe — probe-paced, never per-request), recovery once when a
	// probe succeeds. Operators can grep the outage window either way.
	breakerTripWarnPrefix    = "concurrency: breaker opened"
	breakerRecoverWarnPrefix = "concurrency: breaker closed"
)

// RedisLimiter is the production Limiter backed by Redis ZSET leases. Every
// granted slot is a {nodeID}:{crypto/rand} member in the scope's ZSET with an
// expiry-millisecond score, so counts are shared across gateway nodes (RED-01)
// and stale slots self-reclaim after the lease TTL.
type RedisLimiter struct {
	rdb         *redis.Client
	prefix      string
	registryKey string
	// fallback is the in-process counter every non-over-limit failure fails
	// open into (D-08/D-09): transport errors, timeouts, and malformed script
	// replies count the batch here with limit=0 semantics — count, never
	// enforce — instead of surfacing to the request. Acquire's only non-nil
	// error is therefore ErrConcurrencyExceeded by construction (D-10).
	fallback *MemoryLimiter

	// held is this node's held-lease registry (D-01): every Redis-granted
	// batch's member → {scope keys, granting backend}, so one renewal tick
	// batch-extends everything this node currently holds. Guarded by heldMu
	// because the renewal goroutine reads it while request paths write it
	// (T-02-21: race-free under -race, Close joins the goroutine).
	heldMu sync.Mutex
	held   map[string]heldLease

	// pending is the local release buffer (D-13): a batch whose release hit
	// the outage (or ran inside the cooldown) leaves held but lands here —
	// logically released (never renewed again), physically still in Redis
	// until the next successful acquire or Close flushes it through
	// lease_flush.lua. Guarded by heldMu with held: a member moves between
	// the two maps, never both. No size bound by decision (grows only during
	// an outage, drains on recovery, TTL backstops; see 02-10 flagged
	// assumption) — bounded only by the lease TTL in practice.
	pending map[string]heldLease

	// D-11 cooldown breaker — global granularity (a Redis failure is a
	// global phenomenon; Fowler breaker per dependency, not per scope).
	// breakerMu guards the state only — never held across a Redis call, so
	// it cannot interlock with heldMu or the request path.
	breakerMu      sync.Mutex
	breakerOpen    bool      // cooldown window open → degrade directly
	breakerUntil   time.Time // cooldown deadline; expiry admits one probe
	breakerProbing bool      // a probe request is in flight (single trial)
	closeOnce      sync.Once

	// degradedMu guards the D-14 observation state written on the breaker
	// trip/recover seams and on snapshot read failure/success — read-only for
	// DegradedStatus() callers, never driving the breaker or the lease paths.
	degradedMu sync.Mutex
	degraded   DegradedStatus

	// Renewal ticker lifecycle (D-01): one goroutine per RedisLimiter — one
	// per node in production — ticking every renewInterval, stopped and
	// joined by Close(). renewStop/renewDone wire the stop signal and join.
	renewInterval time.Duration
	renewStop     chan struct{}
	renewDone     chan struct{}
}

// heldLease tracks one in-flight Redis grant for renewal: the scope bucket
// keys it wrote a member into (registry key excluded — renewals only touch
// ZSETs) and the backend that granted it, so renewal stays on its own
// backend even if the limiter's client is swapped mid-outage (same isolation
// rule as releaseFunc).
type heldLease struct {
	keys []string
	rdb  *redis.Client
}

// NewRedisLimiter builds a RedisLimiter over rdb. prefix namespaces every
// scope key (production uses "conc:"); the registry lives at
// "<prefix>registry". Construction also starts the node's single renewal
// ticker (D-01) at TTL/3 (D-03/D-04); Close() stops and joins it.
func NewRedisLimiter(rdb *redis.Client, prefix string) *RedisLimiter {
	interval := time.Duration(common.GetEnvOrDefault(leaseTTLEnv, defaultLeaseTTLs)) *
		time.Second / renewalIntervalDivisor
	if interval <= 0 {
		interval = time.Second
	}
	r := &RedisLimiter{
		rdb:           rdb,
		prefix:        prefix,
		registryKey:   prefix + registryKeySuffix,
		fallback:      NewMemoryLimiter(),
		held:          make(map[string]heldLease),
		pending:       make(map[string]heldLease),
		renewInterval: interval,
		renewStop:     make(chan struct{}),
		renewDone:     make(chan struct{}),
	}
	go r.renewalLoop()
	return r
}

// leaseMember mints the ownership token for one acquire batch (D-07):
// {nodeID}:{random}, nodeID from common/node_identity.go for operator
// readability, crypto/rand suffix so uniqueness IS ownership — one release
// can only ever clear its own batch.
func leaseMember() string {
	var suffix [12]byte
	_, _ = rand.Read(suffix[:]) // crypto/rand.Read never fails; it aborts instead
	return common.GetNodeIdentity().Name + ":" + hex.EncodeToString(suffix[:])
}

// AcquireBatch grants every bucket of scopes in one Lua call — one Redis
// round-trip, all-or-nothing (D-05). On refusal it returns
// ErrConcurrencyExceeded with nothing written; on success the release func
// clears exactly this batch's member from every bucket.
//
// Error taxonomy (D-08/D-10): the only non-nil error this can ever return is
// ErrConcurrencyExceeded. Connection failures, timeouts, Lua runtime errors,
// and malformed replies all fail open into the in-process fallback counter
// (D-09) with a WARN — an infrastructure fault is never a 429.
func (r *RedisLimiter) AcquireBatch(ctx context.Context, scopes []ScopeLimit) (func(), error) {
	if len(scopes) == 0 {
		return func() {}, nil
	}
	// D-11: inside the cooldown, degrade directly — zero Redis calls, zero
	// timeout wait. An expired cooldown lets exactly one caller through as
	// the probe (half-open trial call); everyone else still degrades.
	if !r.breakerAllow() {
		return r.degrade(ctx, scopes, fmt.Errorf("breaker cooldown active, degrading without a redis call (D-11)"))
	}
	member := leaseMember()
	keys := make([]string, 0, len(scopes)+1)
	keys = append(keys, r.registryKey)
	argv := make([]any, 0, len(scopes)+2)
	argv = append(argv, member, common.GetEnvOrDefault(leaseTTLEnv, defaultLeaseTTLs)*1000)
	for _, sl := range scopes {
		keys = append(keys, r.prefix+sl.Scope.String())
		argv = append(argv, sl.Limit)
	}
	rdb := r.rdb
	// Background + short timeout (D-06): the caller's ctx must not reach the
	// Redis call — an aborted request cannot cancel an issued acquire.
	opCtx, cancel := context.WithTimeout(context.Background(), redisOpTimeout)
	defer cancel()
	// Script.Run does EVALSHA with an automatic EVAL fallback on NOSCRIPT —
	// no separate NOSCRIPT path to maintain (D-10, research accepted #3).
	res, err := acquireScript.Run(opCtx, rdb, keys, argv...).Int64Slice()
	if err != nil {
		r.breakerNoteFailure()
		return r.degrade(ctx, scopes, fmt.Errorf("lease acquire failed: %w", err))
	}
	// The backend answered — that includes a genuine refusal: any valid
	// reply closes the breaker (D-11) and lets the pending buffer drain
	// before shared counting resumes (D-13: flush on recovery, retry on
	// later successes; no-op while the buffer is empty).
	r.breakerNoteSuccess()
	r.flushPending()
	if len(res) > 0 && res[0] == 0 {
		// The script's only refusal code: a bucket was at its limit.
		return nil, ErrConcurrencyExceeded
	}
	if len(res) > 0 && res[0] == 1 {
		// Enter the held-lease registry (D-01): keys[0] is the registry Set,
		// the renewal ticker only needs the scope bucket keys.
		r.heldMu.Lock()
		r.held[member] = heldLease{keys: keys[1:], rdb: rdb}
		r.heldMu.Unlock()
		return r.releaseFunc(rdb, member, keys), nil
	}
	// Any other reply shape is malformed — fail open, never guess a refusal.
	// A malformed reply is an infrastructure fault like any other: it also
	// opens the cooldown so the next requests do not wait on a broken path.
	r.breakerNoteFailure()
	return r.degrade(ctx, scopes, fmt.Errorf("malformed lease acquire reply: %v", res))
}

// degrade is the fail-open path (D-08/D-09/D-10): it WARNs the cause, counts
// the whole batch into the in-process fallback with limit=0 semantics (count,
// never enforce), and returns a usable release with a nil error so the
// request proceeds. The fallback is independent per acquire, so each release
// only ever decrements the backend that granted it.
func (r *RedisLimiter) degrade(ctx context.Context, scopes []ScopeLimit, cause error) (func(), error) {
	common.SysError(fmt.Sprintf("%s acquire degraded to in-memory counting: %v", degradeWarnPrefix, cause))
	zeroed := make([]ScopeLimit, len(scopes))
	for i, sl := range scopes {
		zeroed[i] = ScopeLimit{Scope: sl.Scope, Limit: 0}
	}
	release, err := r.fallback.AcquireBatch(ctx, zeroed)
	if err != nil {
		// Unreachable: every bucket is limit=0, so the memory backend cannot
		// refuse. Fail open anyway — an infrastructure problem must never
		// become a refusal (safe direction over strictness).
		common.SysError(fmt.Sprintf("%s fallback counter refused: %v", degradeWarnPrefix, err))
		return func() {}, nil
	}
	return release, nil
}

// releaseFunc returns the idempotent release closure for one granted batch:
// it runs lease_release.lua with the batch's member across the same keys, so
// it only ever clears slots this batch owns (D-07). A second call ZREMs an
// absent member — a no-op (Pitfall 11).
//
// The closure captures the client that granted the slot, so a release always
// stays on its own backend even if the limiter's client is swapped while an
// outage window is open. Either way the member leaves the held-lease registry
// so the renewal ticker stops extending it: on success it is already cleared
// from Redis; on failure (or while the D-11 cooldown holds — no call is made
// then) the release moves into the pending buffer instead (D-13) and is
// flushed by the next successful acquire or by Close()'s shutdown cleanup
// (D-19), with the lease TTL as the final backstop.
func (r *RedisLimiter) releaseFunc(rdb *redis.Client, member string, keys []string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			// Inside the cooldown the backend is known-down: skip the call
			// entirely (D-11 zero-cost outage) and buffer straight away.
			buffered := false
			if r.breakerCooldownActive() {
				buffered = true
			} else {
				// Background + short timeout like acquire (D-06): a release
				// is not the request's to cancel.
				opCtx, cancel := context.WithTimeout(context.Background(), redisOpTimeout)
				_, err := releaseScript.Run(opCtx, rdb, keys, member).Result()
				cancel()
				if err != nil {
					buffered = true
					r.breakerNoteFailure()
					common.SysError(fmt.Sprintf("%s release failed, buffering for flush (D-13), lease TTL remains the backstop: %v", degradeWarnPrefix, err))
				}
			}
			// Logical release completes either way: drop the held entry so no
			// tick keeps renewing a slot this node no longer owns. A buffered
			// release lands in the pending map (already logically released —
			// never renewed again); a successful one clears any pending trace
			// left by a Close-time handoff (shutdown then late release).
			r.heldMu.Lock()
			delete(r.held, member)
			if buffered {
				r.pending[member] = heldLease{keys: keys[1:], rdb: rdb}
			} else {
				delete(r.pending, member)
			}
			r.heldMu.Unlock()
		})
	}
}

// breakerAllow reports whether this acquire may reach the backend (D-11).
// Closed → always. Open and cooling down → false: degrade directly, zero
// calls, zero timeout wait. Open with an expired cooldown → exactly one
// caller wins the probe slot (half-open trial call, Fowler); the rest still
// degrade until the probe reports back.
func (r *RedisLimiter) breakerAllow() bool {
	r.breakerMu.Lock()
	defer r.breakerMu.Unlock()
	if !r.breakerOpen {
		return true
	}
	if time.Now().Before(r.breakerUntil) {
		return false
	}
	if r.breakerProbing {
		return false
	}
	r.breakerProbing = true
	return true
}

// breakerCooldownActive reports whether the cooldown window still holds —
// releases use it to skip the backend entirely (D-11 applies to every Redis
// round-trip, not just acquires).
func (r *RedisLimiter) breakerCooldownActive() bool {
	r.breakerMu.Lock()
	defer r.breakerMu.Unlock()
	return r.breakerOpen && time.Now().Before(r.breakerUntil)
}

// breakerNoteFailure opens the cooldown after a failed backend attempt —
// the first failure trips it, a failed probe (or a failed release in an
// already-open window) extends it. The trip WARN fires once per failed
// attempt, and failed attempts only happen on trips and probes, so the WARN
// is naturally rate-limited by the probe frequency (D-11/D-24).
func (r *RedisLimiter) breakerNoteFailure() {
	cooldown := time.Duration(common.GetEnvOrDefault(breakerCooldownEnv, defaultBreakerCooldown)) * time.Second
	if cooldown <= 0 {
		cooldown = time.Duration(defaultBreakerCooldown) * time.Second
	}
	r.breakerMu.Lock()
	wasOpen := r.breakerOpen
	r.breakerOpen = true
	r.breakerProbing = false // the trial call reported back — no longer in flight
	r.breakerUntil = time.Now().Add(cooldown)
	r.breakerMu.Unlock()
	r.noteDegraded() // D-14 observation: enter/refresh the degraded window
	if wasOpen {
		common.SysError(fmt.Sprintf("%s backend still failing; cooldown extended to %s (D-11)", breakerTripWarnPrefix, cooldown))
	} else {
		common.SysError(fmt.Sprintf("%s redis call failed; degrading directly for %s (D-11)", breakerTripWarnPrefix, cooldown))
	}
}

// breakerNoteSuccess closes the breaker after a valid backend reply (any
// script result — including a genuine over-limit refusal — proves the backend
// is answering) and emits the one-time recovery WARN. Pending releases drain
// via flushPending at the call site.
func (r *RedisLimiter) breakerNoteSuccess() {
	r.breakerMu.Lock()
	wasOpen := r.breakerOpen
	r.breakerOpen = false
	r.breakerProbing = false
	r.breakerUntil = time.Time{}
	r.breakerMu.Unlock()
	r.noteRecovered() // D-14 observation: a valid backend reply is recovery evidence
	if wasOpen {
		common.SysError(fmt.Sprintf("%s redis recovered; resuming shared counting (D-11)", breakerRecoverWarnPrefix))
	}
}

// DegradedStatus returns the D-14 observation state (read-only accessor —
// never drives the breaker or the lease paths). See limiter.go's type doc for
// the field semantics.
func (r *RedisLimiter) DegradedStatus() DegradedStatus {
	r.degradedMu.Lock()
	defer r.degradedMu.Unlock()
	return r.degraded
}

// noteDegraded records one entry/continuation of the degraded window (D-14):
// active=true, lastDegradedAt stamped with the unix-millisecond clock seam
// (nowUnix — no new time source), consecutive failure count incremented. It
// is called on the breaker trip seam (every failed backend attempt, probe
// paced) and on a failed snapshot read — never on per-request cooldown
// degrades, which are consequences of an already-recorded failure.
func (r *RedisLimiter) noteDegraded() {
	r.degradedMu.Lock()
	r.degraded.Active = true
	r.degraded.LastDegradedAt = nowUnix()
	r.degraded.ConsecutiveFailures++
	r.degradedMu.Unlock()
}

// noteRecovered clears the degraded window (D-14): active=false and the
// consecutive counter zeroes, while lastDegradedAt is deliberately RETAINED —
// "last degraded at" survives recovery for the forensic trail. Recovery
// evidence is a valid backend reply (breaker probe) or a successful snapshot
// read (planner discretion 02-12: the admin page clears its badge even at
// zero traffic). Observation only — the breaker keeps its own state machine.
func (r *RedisLimiter) noteRecovered() {
	r.degradedMu.Lock()
	r.degraded.Active = false
	r.degraded.ConsecutiveFailures = 0
	r.degradedMu.Unlock()
}

// leaseScriptArgs assembles the KEYS/ARGV parallel slices for one batched
// lease Lua call (lease_flush.lua and lease_renew.lua). Each row contributes a
// scope key — deduplicated so a key shared across several members appears once
// in KEYS — and an (absolute 1-based KEYS index, member) ARGV pair. Leading
// KEYS (the registry key) and leading ARGV elements (the renewal TTL) are
// supplied up front; the caller consumes keys/argv when invoking the script.
type leaseScriptArgs struct {
	keys     []string
	argv     []any
	keyIndex map[string]int
}

// newLeaseScriptArgs starts an assembly with the caller's leading KEYS and
// ARGV elements, reserving capacity for up to rows distinct keys/pairs.
func newLeaseScriptArgs(leadKeys []string, leadArgv []any, rows int) *leaseScriptArgs {
	keys := make([]string, len(leadKeys), len(leadKeys)+rows)
	copy(keys, leadKeys)
	argv := make([]any, len(leadArgv), len(leadArgv)+2*rows)
	copy(argv, leadArgv)
	return &leaseScriptArgs{keys: keys, argv: argv, keyIndex: make(map[string]int, rows)}
}

// add registers one (key, member) pair: a key seen for the first time is
// appended to KEYS, and member is appended to ARGV as (1-based KEYS index,
// member).
func (a *leaseScriptArgs) add(key, member string) {
	idx, ok := a.keyIndex[key]
	if !ok {
		a.keys = append(a.keys, key)
		idx = len(a.keys) // absolute KEYS position
		a.keyIndex[key] = idx
	}
	a.argv = append(a.argv, idx, member)
}

// flushPending drains the local pending-release buffer (D-13): one
// lease_flush.lua call per granting backend batch-ZREMs every buffered
// (member, key) pair and runs the registry cleanup chain (D-17/D-18) in the
// same script. A backend whose call fails keeps its entries for the next
// successful acquire or Close to retry — ZREM is idempotent, so retries are
// safe — and the lease TTL backstops anything never flushed.
func (r *RedisLimiter) flushPending() {
	type flushRow struct {
		member string
		key    string
	}

	r.heldMu.Lock()
	if len(r.pending) == 0 {
		r.heldMu.Unlock()
		return
	}
	rowsByBackend := make(map[*redis.Client][]flushRow, 1)
	membersByBackend := make(map[*redis.Client][]string, 1)
	for member, entry := range r.pending {
		rows := rowsByBackend[entry.rdb]
		for _, key := range entry.keys {
			rows = append(rows, flushRow{member: member, key: key})
		}
		rowsByBackend[entry.rdb] = rows
		membersByBackend[entry.rdb] = append(membersByBackend[entry.rdb], member)
	}
	r.heldMu.Unlock()

	for rdb, rows := range rowsByBackend {
		args := newLeaseScriptArgs([]string{r.registryKey}, nil, len(rows))
		for _, row := range rows {
			args.add(row.key, row.member)
		}

		opCtx, cancel := context.WithTimeout(context.Background(), redisOpTimeout)
		_, err := flushScript.Run(opCtx, rdb, args.keys, args.argv...).Int64()
		cancel()
		if err != nil {
			common.SysError(fmt.Sprintf("%s pending release flush failed, buffering for retry (D-13): %v", degradeWarnPrefix, err))
			continue // keep this backend's entries; TTL backstops
		}
		// Only the snapshotted members are cleared: entries buffered while
		// the script ran stay for the next round.
		r.heldMu.Lock()
		for _, member := range membersByBackend[rdb] {
			delete(r.pending, member)
		}
		r.heldMu.Unlock()
	}
}

// Acquire is the single-bucket form of AcquireBatch (leg 0 contract kept
// byte-for-byte: same signature, same sentinel, same release semantics),
// including the fail-open taxonomy: the only non-nil error is
// ErrConcurrencyExceeded.
func (r *RedisLimiter) Acquire(ctx context.Context, scope ScopeKey, limit int) (func(), error) {
	return r.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: limit}})
}

// renewalLoop is the node's single renewal goroutine (D-01): constructed with
// the limiter, ticking every TTL/3, fully decoupled from the relay request
// path, and joined by Close() so no renewal write can land after shutdown.
func (r *RedisLimiter) renewalLoop() {
	defer close(r.renewDone)
	ticker := time.NewTicker(r.renewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.renewStop:
			return
		case <-ticker.C:
			r.renewHeldLeases()
		}
	}
}

// renewHeldLeases runs one renewal pass: batch-renew every lease this node
// still believes it holds — grouped per granting backend, one
// lease_renew.lua call per backend (1 RTT) — then apply the D-02
// stop-renewal branch to members the script reports as gone.
//
// Transport faults and malformed replies keep every entry (retry next tick):
// only a script-reported "member gone" stops renewal, so a flaky network can
// never evict a live lease, and an evicted lease can never be re-acquired.
func (r *RedisLimiter) renewHeldLeases() {
	type renewRow struct {
		member string
		key    string
	}

	r.heldMu.Lock()
	if len(r.held) == 0 {
		r.heldMu.Unlock()
		return
	}
	rowsByBackend := make(map[*redis.Client][]renewRow, 1)
	for member, entry := range r.held {
		rows := rowsByBackend[entry.rdb]
		for _, key := range entry.keys {
			rows = append(rows, renewRow{member: member, key: key})
		}
		rowsByBackend[entry.rdb] = rows
	}
	r.heldMu.Unlock()

	ttlMs := common.GetEnvOrDefault(leaseTTLEnv, defaultLeaseTTLs) * 1000
	for rdb, rows := range rowsByBackend {
		args := newLeaseScriptArgs(nil, []any{ttlMs}, len(rows)) // ARGV[1] = TTL ms (score = server TIME + TTL)
		for _, row := range rows {
			args.add(row.key, row.member)
		}

		opCtx, cancel := context.WithTimeout(context.Background(), redisOpTimeout)
		res, err := renewScript.Run(opCtx, rdb, args.keys, args.argv...).Int64Slice()
		cancel()
		if err != nil {
			common.SysError(fmt.Sprintf("%s renewal failed, retrying next tick: %v", degradeWarnPrefix, err))
			continue
		}
		if len(res) != len(rows) {
			common.SysError(fmt.Sprintf("%s malformed renewal reply %v, retrying next tick", degradeWarnPrefix, res))
			continue
		}

		for i, row := range rows {
			if res[i] != 0 {
				continue // renewed (ZADD XX CH reported the member present)
			}
			// D-02: the lease was reclaimed while this node still believed it
			// held it (GC stall / failover window past the TTL). Drop the
			// held entry and WARN — never re-acquire: fewer counts beat
			// corruption or zombie slots. Skip the WARN when the entry is
			// already gone because a concurrent release untracked it first.
			r.heldMu.Lock()
			_, stillHeld := r.held[row.member]
			if stillHeld {
				delete(r.held, row.member)
			}
			r.heldMu.Unlock()
			if stillHeld {
				common.SysError(fmt.Sprintf("%s lease %s reclaimed externally; dropping it, not re-acquiring (D-02)",
					renewStopWarnPrefix, row.member))
			}
		}
	}
}

// Close stops the renewal ticker, joins its goroutine (so zero renewal
// writes can land afterwards), then runs the D-19 shutdown bulk cleanup: the
// remaining held-lease registry moves into the pending buffer and both are
// flushed through lease_flush.lua — one batch ZREM per granting backend that
// drains the local buckets and the registry. It is idempotent (sync.Once)
// and safe to call concurrently; main.go invokes it between srv.Shutdown and
// process exit. Direction-safe: failures only mean fewer counts — an
// unreachable backend WARNs and the lease TTL reclaims whatever is left after
// exit. MemoryLimiter has no Close.
func (r *RedisLimiter) Close() {
	r.closeOnce.Do(func() {
		close(r.renewStop)
		<-r.renewDone
		// D-19: hand every locally-known lease to the flush facility —
		// grants the server never saw released (drain timeout, force kill)
		// plus releases buffered by an outage.
		r.heldMu.Lock()
		for member, entry := range r.held {
			r.pending[member] = entry
			delete(r.held, member)
		}
		r.heldMu.Unlock()
		r.flushPending()
	})
}

// Snapshot enumerates the registry set — the single source of truth for known
// scopes (D-17; no SCAN/KEYS) — reads each bucket's ZCARD, and merges the
// local fallback buckets into the same rows (D-15): slots granted while Redis
// was unreachable live ONLY in the in-process fallback counter, each release
// decrements only its granting backend, so summing per scope can never double
// count — the merged current is ZCARD + fallback with no sudden drop when
// reads recover. Enumeration source = registry members ∪ fallback holders,
// merged per scope and sorted by ScopeKey; rows carry Limit=0 because
// configured caps live in token/user configuration and are back-filled by the
// controller (D-02). Empty buckets cannot appear: the release cleanup chain
// SREMs them the moment ZCARD hits 0 (fallback rows drain on release too).
//
// Any Redis read failure WARNs, records one degradation (D-14 observation),
// and falls back to the in-process view alone — err stays nil forever, so the
// admin page's GET /api/concurrency/snapshot polls 200 through the outage.
// A successful read is recovery evidence: it clears active and the failure
// counter while keeping lastDegradedAt (D-14), without touching the breaker.
func (r *RedisLimiter) Snapshot(ctx context.Context) ([]ScopeSnapshot, error) {
	opCtx, cancel := context.WithTimeout(context.Background(), redisOpTimeout)
	defer cancel()
	members, err := r.rdb.SMembers(opCtx, r.registryKey).Result()
	if err != nil {
		common.SysError(fmt.Sprintf("%s snapshot fell back to in-memory view: %v", degradeWarnPrefix, err))
		r.noteDegraded()
		return r.fallback.Snapshot(ctx)
	}
	merged := make(map[ScopeKey]ScopeSnapshot, len(members))
	for _, key := range members {
		count, err := r.rdb.ZCard(opCtx, key).Result()
		if err != nil {
			common.SysError(fmt.Sprintf("%s snapshot fell back to in-memory view: %v", degradeWarnPrefix, err))
			r.noteDegraded()
			return r.fallback.Snapshot(ctx)
		}
		name := ScopeKey(strings.TrimPrefix(key, r.prefix))
		merged[name] = scopeRow(name, int(count), 0, nowUnix())
	}
	// D-15 merge: add the local fallback buckets (the only slots Redis has
	// never seen) per scope — same-scope rows combine into one, otherwise the
	// local row stands alone. The local held-lease registry is deliberately
	// NOT added: it is the shadow of Redis leases already counted in ZCARD.
	fallbackRows, _ := r.fallback.Snapshot(ctx)
	for _, row := range fallbackRows {
		if existing, ok := merged[row.Scope]; ok {
			existing.Current += row.Current
			existing.LastUpdated = max(existing.LastUpdated, row.LastUpdated)
			merged[row.Scope] = existing
		} else {
			merged[row.Scope] = row
		}
	}
	scopes := make([]ScopeSnapshot, 0, len(merged))
	for _, row := range merged {
		scopes = append(scopes, row)
	}
	sort.Slice(scopes, func(i, j int) bool { return scopes[i].Scope < scopes[j].Scope })
	r.noteRecovered() // a healthy read is recovery evidence (D-14 observation)
	return scopes, nil
}

var _ Limiter = (*RedisLimiter)(nil)
