// Package concurrency implements per-user / per-API-key / per-channel in-flight
// concurrency limits for new-api-monster. The package is deliberately split into:
//
//   - ScopeKey + MemoryLimiter (this file): pure Go, no external dependencies.
//     Drives unit tests and the dev-mode fallback when Redis is offline.
//   - RedisLimiter (limiter_redis.go): production path backed by Redis ZSET
//     leases — one member per granted slot, released atomically by Lua.
//   - token_limits.go: CSV decoder for the Token.ConcurrencyLimitByModel column.
//
// Wire-up happens in middleware/concurrency.go (request gate) and
// controller/concurrency.go (admin snapshot).
package concurrency

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// ScopeKind enumerates the subject axes the gateway can throttle on.
type ScopeKind uint8

const (
	ScopeKindKey     ScopeKind = 1
	ScopeKindUser    ScopeKind = 2
	ScopeKindChannel ScopeKind = 3
)

// ScopeKey identifies a throttle bucket. Wire format:
//
//	"<key|user>:<id>:<model|*>"   three segments, "*" = the subject's global bucket
//	"channel:<id>"                two segments (BCD D-2): the channel axis has no
//	                              per-model bucket, so Model() naturally reads "*"
type ScopeKey string

// ChannelScope builds the channel-axis scope. It is the ONLY constructor of the
// two-segment "channel:<id>" wire format (BCD D-2): that format is part of the
// ZSET key namespace and the snapshot row contract, so it must not be assembled
// ad hoc at call sites.
func ChannelScope(channelID int) ScopeKey {
	return ScopeKey("channel:" + strconv.Itoa(channelID))
}

// ErrConcurrencyExceeded is the sentinel error returned by Acquire when the
// scope's current in-flight count already equals its limit. Callers must use
// errors.Is(err, ErrConcurrencyExceeded), not direct equality, because future
// transports may wrap the error with transport-specific context.
var ErrConcurrencyExceeded = errors.New("concurrency limit exceeded")

// Kind returns which axis the scope addresses. The channel axis has to be
// checked explicitly: without it a "channel:<id>" scope silently falls through
// to the key axis (BCD D-2).
func (s ScopeKey) Kind() ScopeKind {
	if strings.HasPrefix(string(s), "user:") {
		return ScopeKindUser
	}
	if strings.HasPrefix(string(s), "channel:") {
		return ScopeKindChannel
	}
	return ScopeKindKey
}

// SubjectID returns the user, token, or channel id encoded in the scope.
func (s ScopeKey) SubjectID() int {
	parts := strings.SplitN(string(s), ":", 3)
	if len(parts) < 2 {
		return 0
	}
	var id int
	_, _ = fmt.Sscanf(parts[1], "%d", &id)
	return id
}

// Model returns the model name or "*" for the global bucket.
func (s ScopeKey) Model() string {
	parts := strings.SplitN(string(s), ":", 3)
	if len(parts) < 3 {
		return "*"
	}
	return parts[2]
}

// IsModelScoped reports whether the scope is bound to a specific model.
func (s ScopeKey) IsModelScoped() bool {
	return s.Model() != "*"
}

// String renders the scope for hashing / Redis keys.
func (s ScopeKey) String() string { return string(s) }

// ScopeSnapshot is one row of the admin snapshot endpoint.
type ScopeSnapshot struct {
	Scope       ScopeKey `json:"scope"`
	Kind        string   `json:"kind"`
	SubjectID   int      `json:"subjectId"`
	Model       string   `json:"model"`
	Current     int      `json:"current"`
	Limit       int      `json:"limit"`
	Utilization float64  `json:"utilization"`
	LastUpdated int64    `json:"lastUpdatedAt"`
}

// ScopeLimit pairs a scope bucket with the per-request cap that governs it.
// Caps are computed by the caller on every request (ARCHITECTURE Decision A)
// and never stored in the limiter.
type ScopeLimit struct {
	Scope ScopeKey
	Limit int
}

// DegradedStatus is the observability view of the backend's fail-open
// degradation state (D-14 / RED-05): what the admin snapshot envelope
// serializes as degraded:{active,lastDegradedAt,consecutiveFailures}.
//
//   - Active: the backend is currently failing open (degraded window open).
//   - LastDegradedAt: unix milliseconds of the most recent degradation entry;
//     0 means never degraded. It is deliberately RETAINED after recovery so
//     operators keep a "last degraded at" forensic trail (D-14).
//   - ConsecutiveFailures: how many backend failures in a row since the last
//     recovery evidence; reset to 0 on recovery.
//
// It is a read-only observation — it never drives the breaker, the lease
// paths, or the fallback counters.
type DegradedStatus struct {
	Active              bool  `json:"active"`
	LastDegradedAt      int64 `json:"lastDegradedAt"`
	ConsecutiveFailures int   `json:"consecutiveFailures"`
}

// Limiter is the interface every backing store must satisfy.
type Limiter interface {
	// Acquire increments the in-flight count for scope when the caller's
	// per-request limit allows it. limit <= 0 means unlimited: Acquire always
	// succeeds but still counts the holder so snapshots keep observing
	// in-flight requests. On success the returned release func must be called
	// exactly once when the request finishes; double-release is a no-op,
	// missed release leaks a slot.
	Acquire(ctx context.Context, scope ScopeKey, limit int) (release func(), err error)
	// AcquireBatch acquires every (scope, limit) pair as one all-or-nothing
	// unit: either all buckets gain a slot and the returned release func
	// frees them together, or the call fails with ErrConcurrencyExceeded and
	// no bucket is touched — no partial grant, no rollback window (D-05).
	// A bucket with limit <= 0 still counts without enforcing, matching
	// Acquire (D-18).
	AcquireBatch(ctx context.Context, scopes []ScopeLimit) (release func(), err error)
	// Snapshot returns the current state of every known scope, ordered by
	// ScopeKey for stable rendering.
	Snapshot(ctx context.Context) ([]ScopeSnapshot, error)
	// DegradedStatus returns the backend's degradation observation state
	// (D-14 / RED-05) for the admin snapshot envelope. It is a read-only
	// accessor: it never mutates breaker, lease, or fallback state.
	DegradedStatus() DegradedStatus
}

// MemoryLimiter is a goroutine-safe in-process Limiter used as the test double
// and the dev-mode fallback when Redis is unavailable. It is intentionally
// small: a sync.RWMutex, two maps. Limits are never stored: callers compute
// them per request and pass them to Acquire (ARCHITECTURE Decision A). When
// Redis is online, callers should swap in RedisLimiter (see limiter_redis.go).
type MemoryLimiter struct {
	mu      sync.RWMutex
	holders map[string]int
	updated map[string]int64
}

// NewMemoryLimiter returns a fresh, empty MemoryLimiter.
func NewMemoryLimiter() *MemoryLimiter {
	return &MemoryLimiter{
		holders: map[string]int{},
		updated: map[string]int64{},
	}
}

// Acquire checks the in-flight count for this subject against the caller's
// per-request limit and either reserves a slot (returning a release func) or
// returns ErrConcurrencyExceeded. limit <= 0 always succeeds but still counts
// the holder so Snapshot keeps observing unlimited subjects — an intentional
// deviation from the milestone ARCHITECTURE.md "skip zero-limit" wording;
// skipping is the RedisLimiter's key-cost concern, not the gate's.
// Acquire is the single-bucket form of AcquireBatch: the limit<=0
// counting contract and the ErrConcurrencyExceeded refusal are identical
// for one-element batches.
func (m *MemoryLimiter) Acquire(ctx context.Context, scope ScopeKey, limit int) (func(), error) {
	return m.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: limit}})
}

// AcquireBatch grants every bucket of scopes in one all-or-nothing step under
// a single lock: every capped bucket is judged before any hold is written, so
// a refusal returns ErrConcurrencyExceeded with zero partial holds, and
// limit<=0 buckets are counted without enforcing (D-18). The returned release
// frees every bucket of the batch together and is idempotent.
func (m *MemoryLimiter) AcquireBatch(_ context.Context, scopes []ScopeLimit) (func(), error) {
	m.mu.Lock()
	// Judge the whole batch first — all-or-nothing, no rollback window.
	for _, sl := range scopes {
		if sl.Limit > 0 && m.holders[sl.Scope.String()] >= sl.Limit {
			m.mu.Unlock()
			return nil, ErrConcurrencyExceeded
		}
	}
	keys := make([]string, 0, len(scopes))
	for _, sl := range scopes {
		key := sl.Scope.String()
		m.holders[key]++
		m.updated[key] = nowUnix()
		keys = append(keys, key)
	}
	m.mu.Unlock()

	var done bool
	return func() {
		if done {
			return
		}
		done = true
		m.mu.Lock()
		for _, key := range keys {
			if m.holders[key] > 0 {
				m.holders[key]--
			}
			if m.holders[key] == 0 {
				delete(m.holders, key)
			}
			m.updated[key] = nowUnix()
		}
		m.mu.Unlock()
	}, nil
}

// Snapshot returns current state for every known scope in deterministic order
// (by ScopeKey string). Only in-flight (holder) rows are emitted and Limit is
// always 0: the configured caps live in the token/user configuration data and
// are back-filled at the controller layer (D-02), keeping the limiter decoupled
// from configuration push.
func (m *MemoryLimiter) Snapshot(_ context.Context) ([]ScopeSnapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	scopes := make([]ScopeSnapshot, 0, len(m.holders))
	for k, cur := range m.holders {
		scopes = append(scopes, scopeRow(ScopeKey(k), cur, 0, m.updated[k]))
	}
	sort.Slice(scopes, func(i, j int) bool { return scopes[i].Scope < scopes[j].Scope })
	return scopes, nil
}

// DegradedStatus always reports the zero-value healthy status: the
// MemoryLimiter IS the single-node mode (RED-04) — without Redis there is no
// degraded backend to observe, so the admin snapshot never shows a degraded
// badge no matter how many slots are held.
func (m *MemoryLimiter) DegradedStatus() DegradedStatus {
	return DegradedStatus{}
}

// nowUnix is overridable for tests; see limiter_clock_test.go for the seam.
var nowUnix = func() int64 { return unixMilli() }

// unixMilli uses the runtime clock; replaced by tests with a fixed value.
var unixMilli = func() int64 {
	return wallClockMillis()
}

func scopeRow(s ScopeKey, current, limit int, updated int64) ScopeSnapshot {
	util := 0.0
	if limit > 0 {
		util = float64(current) / float64(limit)
	}
	kind := "key"
	switch s.Kind() {
	case ScopeKindUser:
		kind = "user"
	case ScopeKindChannel:
		kind = "channel"
	}
	return ScopeSnapshot{
		Scope:       s,
		Kind:        kind,
		SubjectID:   s.SubjectID(),
		Model:       s.Model(),
		Current:     current,
		Limit:       limit,
		Utilization: util,
		LastUpdated: updated,
	}
}

// wallClockMillis returns wall-clock milliseconds. Defined as a var so tests
// can swap in a deterministic clock without touching time.Now directly.
var wallClockMillis = func() int64 {
	return realWallClockMillis()
}
