package concurrency

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

// newRedisLimiterFixture boots a per-test miniredis and wires a RedisLimiter
// with the production "conc:" prefix to it. Returns the server (for raw key
// inspection), the limiter under test, and the client for ZCARD/SMEMBERS/
// DBSIZE assertions.
func newRedisLimiterFixture(t *testing.T) (*miniredis.Miniredis, *RedisLimiter, *redis.Client) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	require.NoError(t, client.Ping(context.Background()).Err())
	t.Cleanup(func() { _ = client.Close() })
	limiter := NewRedisLimiter(client, "conc:")
	// Registered last, so it runs first (LIFO): stop the renewal ticker
	// before the server/client go away (D-01 lifecycle hygiene).
	t.Cleanup(limiter.Close)
	return server, limiter, client
}

// TestRedisLimiter_GrantRejectReleaseCycle pins the basic acquire contract on
// the Redis backend: at limit=2 the first two batches are granted (ZCARD=2),
// the third returns ErrConcurrencyExceeded, and after a full release ZCARD
// drains to 0 so slots are acquirable again.
func TestRedisLimiter_GrantRejectReleaseCycle(t *testing.T) {
	_, lim, client := newRedisLimiterFixture(t)
	ctx := context.Background()
	scope := ScopeKey("key:1:gpt-4o")
	key := "conc:key:1:gpt-4o"

	first, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 2}})
	require.NoError(t, err, "first acquire at limit=2 must be granted")
	second, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 2}})
	require.NoError(t, err, "second acquire at limit=2 must be granted")
	require.EqualValues(t, 2, client.ZCard(ctx, key).Val(), "two granted batches must hold two ZSET members")

	_, err = lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 2}})
	require.ErrorIs(t, err, ErrConcurrencyExceeded, "third acquire at limit=2 must be rejected")

	first()
	second()
	require.EqualValues(t, 0, client.ZCard(ctx, key).Val(), "releasing every batch must drain the scope ZSET")

	third, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 2}})
	require.NoError(t, err, "slot must be acquirable immediately after full release")
	third()
}

// TestRedisLimiter_BatchAllOrNothingRejectsWithoutWrites pins D-05: when a
// batch contains a full bucket (B at limit 1) and an empty one (A at limit 5),
// the whole batch is refused with ErrConcurrencyExceeded and bucket A gets
// zero writes — no ZSET key, no registry entry, no partial grant, no rollback
// window. The pre-existing holder of B must be untouched.
func TestRedisLimiter_BatchAllOrNothingRejectsWithoutWrites(t *testing.T) {
	_, lim, client := newRedisLimiterFixture(t)
	ctx := context.Background()
	scopeA := ScopeKey("key:10:*")
	scopeB := ScopeKey("key:11:*")
	keyA := "conc:key:10:*"
	keyB := "conc:key:11:*"

	// Fill bucket B (limit 1) with a single grant.
	holdB, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scopeB, Limit: 1}})
	require.NoError(t, err)

	// The batch pairs empty bucket A with full bucket B: it must be refused
	// as one unit.
	_, err = lim.AcquireBatch(ctx, []ScopeLimit{
		{Scope: scopeA, Limit: 5},
		{Scope: scopeB, Limit: 1},
	})
	require.ErrorIs(t, err, ErrConcurrencyExceeded, "a batch containing a full bucket must be refused")

	require.EqualValues(t, 0, client.Exists(ctx, keyA).Val(),
		"bucket A must have zero writes after a refused batch (no ZSET key at all)")
	require.EqualValues(t, 0, client.ZCard(ctx, keyA).Val(),
		"bucket A must hold no members after a refused batch")
	require.NotContains(t, client.SMembers(ctx, "conc:registry").Val(), keyA,
		"registry must not list bucket A after a refused batch")

	require.EqualValues(t, 1, client.ZCard(ctx, keyB).Val(),
		"the pre-existing holder of bucket B must be untouched by the refused batch")
	holdB()
}

// TestRedisLimiter_MemberUniqueOwnershipAndIdempotentRelease pins D-07: every
// batch mints a fresh {nodeID}:{crypto/rand} member, uniqueness is ownership —
// one release clears only its own batch's member (the other holder's count is
// untouched) — and a second release call is a zero-change no-op (Pitfall 11).
func TestRedisLimiter_MemberUniqueOwnershipAndIdempotentRelease(t *testing.T) {
	_, lim, client := newRedisLimiterFixture(t)
	ctx := context.Background()
	scope := ScopeKey("key:20:*")
	key := "conc:key:20:*"

	firstRelease, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err)
	members1 := client.ZRange(ctx, key, 0, -1).Val()
	require.Len(t, members1, 1, "first batch must hold exactly one member")
	myMember := members1[0]

	secondRelease, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err)
	all := client.ZRange(ctx, key, 0, -1).Val()
	require.Len(t, all, 2, "two batches on the same scope must hold two members")
	require.NotEqual(t, all[0], all[1], "each batch must mint a distinct member")

	nodeID := common.GetNodeIdentity().Name
	for _, m := range all {
		require.True(t, strings.HasPrefix(m, nodeID+":"),
			"member %q must be {nodeID}:{random} with nodeID=%q", m, nodeID)
		require.Greater(t, len(m), len(nodeID)+1, "member %q must carry a random suffix", m)
	}

	// Releasing batch 1 must clear only its own member.
	firstRelease()
	require.EqualValues(t, 1, client.ZCard(ctx, key).Val(),
		"one release must clear only its own batch's member")
	remaining := client.ZRange(ctx, key, 0, -1).Val()
	require.Len(t, remaining, 1, "the other holder must keep its slot")
	require.NotEqual(t, myMember, remaining[0],
		"release must not free the other batch's member")

	// A second call of the same release is a no-op.
	firstRelease()
	require.EqualValues(t, 1, client.ZCard(ctx, key).Val(), "idempotent release must not change counts")
	require.Equal(t, remaining, client.ZRange(ctx, key, 0, -1).Val(),
		"idempotent release must not touch the remaining member")

	secondRelease()
	require.EqualValues(t, 0, client.ZCard(ctx, key).Val(), "last release must drain the scope")
	require.Empty(t, client.SMembers(ctx, "conc:registry").Val(),
		"registry must drain once every holder releases")
}

// TestRedisLimiter_ZeroLimitCountsWithCleanupChain pins D-18/D-17: a limit=0
// scope still counts in Redis (never refused, ZCARD grows), and the release
// cleanup chain — ZREM -> ZCARD=0 -> registry SREM -> empty key auto-delete —
// keeps key storage equal to the currently in-flight scope set: registry and
// ZSET keys vanish after release and DBSIZE returns to its baseline across
// rounds of different scope combinations.
func TestRedisLimiter_ZeroLimitCountsWithCleanupChain(t *testing.T) {
	_, lim, client := newRedisLimiterFixture(t)
	ctx := context.Background()
	baseline := client.DBSize(ctx).Val()

	// limit=0 counts without enforcing: every acquire succeeds.
	rel1, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: ScopeKey("key:30:*"), Limit: 0}})
	require.NoError(t, err, "zero-limit scope must be granted")
	require.EqualValues(t, 1, client.ZCard(ctx, "conc:key:30:*").Val(),
		"zero-limit scope must still count one holder")
	rel2, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: ScopeKey("key:30:*"), Limit: 0}})
	require.NoError(t, err, "zero-limit scope must never refuse")
	require.EqualValues(t, 2, client.ZCard(ctx, "conc:key:30:*").Val(),
		"zero-limit scope keeps counting without enforcing")

	rel1()
	rel2()
	require.Empty(t, client.SMembers(ctx, "conc:registry").Val(),
		"registry must be empty after every holder releases")
	require.EqualValues(t, 0, client.Exists(ctx, "conc:key:30:*").Val(),
		"the emptied scope ZSET key must auto-delete")
	require.EqualValues(t, 0, client.Exists(ctx, "conc:registry").Val(),
		"the emptied registry key must self-delete")

	// Rounds over different scope combinations must not grow key storage.
	for _, scope := range []ScopeKey{"key:31:*", "key:32:*", "user:33:*", "key:31:gpt-4o"} {
		release, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 0}})
		require.NoError(t, err)
		release()
	}
	require.EqualValues(t, baseline, client.DBSize(ctx).Val(),
		"DBSIZE must return to baseline — keys must not accumulate historical scope combinations")
}

// deadRedisClient returns a client aimed at an address nothing listens on:
// open a listener, take its address, close it, so dials are refused fast.
func deadRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// hangingRedisClient returns a client aimed at a listener that accepts
// connections but never replies, so every command stalls until the limiter's
// operation timeout fires — the timeout class of the error taxonomy.
func hangingRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			mu.Lock()
			held = append(held, conn)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		for _, conn := range held {
			_ = conn.Close()
		}
		mu.Unlock()
	})
	client := redis.NewClient(&redis.Options{Addr: ln.Addr().String()})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// syncBuffer is a concurrency-safe log sink: SysError may be emitted from
// background goroutines (the renewal ticker) while the test goroutine reads
// the same buffer, which races a plain bytes.Buffer under -race.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// captureSysErrorWriter swaps gin.DefaultErrorWriter for an in-memory buffer
// (under LogWriterMu, per common/sys_log.go's writer-swap contract) so tests
// can assert the WARN the fail-open path must emit.
func captureSysErrorWriter(t *testing.T) *syncBuffer {
	t.Helper()
	logs := &syncBuffer{}
	common.LogWriterMu.Lock()
	previous := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = logs
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = previous
		common.LogWriterMu.Unlock()
	})
	return logs
}

// swapAcquireScript replaces the embedded acquire script for one test — the
// fault-injection seam for malformed Lua replies (unknown code / wrong type).
func swapAcquireScript(t *testing.T, src string) {
	t.Helper()
	original := acquireScript
	acquireScript = redis.NewScript(src)
	t.Cleanup(func() { acquireScript = original })
}

// TestRedisLimiter_ErrorTaxonomyFailOpen pins D-08/D-10: across the four
// error classes (connection failure / timeout / malformed Lua reply / genuine
// over-limit), a non-nil error may only ever be ErrConcurrencyExceeded.
// Every infrastructure fault must fail open — nil error plus a usable
// release — and emit a WARN with the stable prefix; a genuine refusal
// returns the sentinel and must not WARN.
func TestRedisLimiter_ErrorTaxonomyFailOpen(t *testing.T) {
	ctx := context.Background()
	const warnMarker = "concurrency: redis"
	scope := ScopeKey("key:77:taxonomy")

	tests := []struct {
		name    string
		setup   func(t *testing.T) *RedisLimiter
		prehold bool
		wantErr error // nil => fail open: degraded grant with release
	}{
		{
			name: "class_connection_failure_degrades",
			setup: func(t *testing.T) *RedisLimiter {
				lim := NewRedisLimiter(deadRedisClient(t), "conc:")
				t.Cleanup(lim.Close) // D-01: join the renewal ticker with this limiter (no goroutine leak)
				return lim
			},
			wantErr: nil,
		},
		{
			name: "class_timeout_degrades",
			setup: func(t *testing.T) *RedisLimiter {
				lim := NewRedisLimiter(hangingRedisClient(t), "conc:")
				t.Cleanup(lim.Close) // D-01: join the renewal ticker with this limiter (no goroutine leak)
				return lim
			},
			wantErr: nil,
		},
		{
			name: "class_lua_string_reply_degrades",
			setup: func(t *testing.T) *RedisLimiter {
				swapAcquireScript(t, `return "boom"`)
				_, lim, _ := newRedisLimiterFixture(t)
				return lim
			},
			wantErr: nil,
		},
		{
			name: "class_lua_unknown_code_degrades",
			setup: func(t *testing.T) *RedisLimiter {
				swapAcquireScript(t, `return {7}`)
				_, lim, _ := newRedisLimiterFixture(t)
				return lim
			},
			wantErr: nil,
		},
		{
			name: "class_genuine_over_limit_returns_sentinel",
			setup: func(t *testing.T) *RedisLimiter {
				_, lim, _ := newRedisLimiterFixture(t)
				return lim
			},
			prehold: true,
			wantErr: ErrConcurrencyExceeded,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lim := tc.setup(t)
			if tc.prehold {
				hold, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 1}})
				require.NoError(t, err, "setup must fill the bucket")
				t.Cleanup(hold)
			}
			logs := captureSysErrorWriter(t)

			release, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 1}})

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr, "only a genuine over-limit refusal may return the sentinel")
				require.Nil(t, release, "a refused batch must not hand out a release")
				require.Empty(t, logs.String(), "a genuine refusal is not an infrastructure fault and must not WARN")
				return
			}
			require.NoError(t, err, "infrastructure faults must fail open instead of surfacing an error")
			require.NotNil(t, release, "degraded acquire must still hand out a usable release")
			release()
			require.Contains(t, logs.String(), "[SYS]", "degraded acquire must WARN")
			require.Contains(t, logs.String(), warnMarker, "WARN must carry the stable prefix")
		})
	}
}

// TestRedisLimiter_FailOpenCountsWithoutEnforcing pins D-09 plus the D-06
// anti-hang guard: against a dead backend, Acquire degrades inside a generous
// bound, the degraded window counts every hold with limit=0 semantics (a
// second acquire at limit=1 is never refused), the counts are visible through
// Snapshot's fallback view with Limit=0, and releases drain the view.
func TestRedisLimiter_FailOpenCountsWithoutEnforcing(t *testing.T) {
	ctx := context.Background()
	lim := NewRedisLimiter(deadRedisClient(t), "conc:")
	t.Cleanup(lim.Close) // D-01: join the renewal ticker with this limiter (no goroutine leak)
	scope := ScopeKey("key:78:degraded")
	logs := captureSysErrorWriter(t)

	start := time.Now()
	first, err := lim.Acquire(ctx, scope, 1)
	require.NoError(t, err, "dead backend must fail open, never surface an error")
	require.NotNil(t, first, "degraded acquire must hand out a release")
	require.Less(t, time.Since(start), 5*time.Second,
		"dead backend must return within the anti-hang guard (guard only, not a perf assertion)")

	// D-09: the degraded window counts but never enforces — limit=1 cannot
	// refuse a second hold while Redis is down.
	second, err := lim.Acquire(ctx, scope, 1)
	require.NoError(t, err, "degraded window counts without enforcing (limit=0 semantics)")
	require.NotNil(t, second)

	rows, err := lim.Snapshot(ctx)
	require.NoError(t, err, "snapshot during an outage must fall back to the in-memory view")
	require.Len(t, rows, 1, "the degraded bucket must be visible")
	require.Equal(t, scope, rows[0].Scope)
	require.Equal(t, 2, rows[0].Current, "both degraded holds must be counted")
	require.Zero(t, rows[0].Limit, "Limit column is always 0 (D-02 back-fill)")

	first()
	second()
	rows, err = lim.Snapshot(ctx)
	require.NoError(t, err)
	require.Empty(t, rows, "releases must drain the degraded view")

	require.Contains(t, logs.String(), "[SYS]", "degraded acquires must WARN")
	require.Contains(t, logs.String(), "concurrency: redis", "WARN must carry the stable prefix")
}

// TestRedisLimiter_CancelledRequestContextStillAcquires pins D-06: the caller's
// (cancellable) context must not reach the Redis call — a request that
// disconnects mid-flight cannot cancel an already-issued acquire. The acquire
// still lands in shared Redis.
func TestRedisLimiter_CancelledRequestContextStillAcquires(t *testing.T) {
	_, lim, client := newRedisLimiterFixture(t)
	scope := ScopeKey("key:79:cancelled")
	key := "conc:key:79:cancelled"

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	release, err := lim.AcquireBatch(cancelled, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err, "request cancellation must not cancel the acquire (D-06)")
	require.NotNil(t, release)
	require.EqualValues(t, 1, client.ZCard(context.Background(), key).Val(),
		"the acquire must land in shared Redis despite the cancelled ctx")
	release()
	require.EqualValues(t, 0, client.ZCard(context.Background(), key).Val())
}

// TestRedisLimiter_SnapshotRegistryView pins D-17: the registry set is the
// only enumeration source — exactly the registered scopes appear (sorted by
// ScopeKey, Current=ZCARD, Limit=0), and a scope ZSET written behind the
// limiter's back never enters the snapshot.
func TestRedisLimiter_SnapshotRegistryView(t *testing.T) {
	_, lim, client := newRedisLimiterFixture(t)
	ctx := context.Background()

	relA, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: ScopeKey("user:5:*"), Limit: 3}})
	require.NoError(t, err)
	relB, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: ScopeKey("key:80:gpt-4o"), Limit: 2}})
	require.NoError(t, err)

	// A ZSET that never entered the registry must not be enumerated.
	require.NoError(t, client.ZAdd(ctx, "conc:ghost:*", &redis.Z{Score: 1, Member: "m"}).Err())

	rows, err := lim.Snapshot(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 2, "exactly the two registered scopes, no ghost key")
	got := make([]ScopeKey, 0, len(rows))
	for _, row := range rows {
		got = append(got, row.Scope)
		require.Equal(t, 1, row.Current, "Current mirrors ZCARD")
		require.Zero(t, row.Limit, "Limit column is always 0 (D-02 back-fill)")
	}
	require.Equal(t, []ScopeKey{ScopeKey("key:80:gpt-4o"), ScopeKey("user:5:*")}, got,
		"rows must be sorted by ScopeKey and exclude the non-registry key")

	relA()
	relB()
}

// TestRedisLimiter_SnapshotMergesDegradedFallbackIntoRedisView pins D-15: an
// outage window grants slots into the in-process fallback counter while
// earlier leases stay in shared Redis; once reads work again the snapshot must
// merge both views per scope (ZCARD + local fallback, the two backends' slots
// never overlap so summing is dedup) instead of showing only the Redis half —
// no error, no sudden drop to the Redis-only count. Enumeration stays
// registry ∪ fallback holders (D-17: no SCAN/KEYS).
func TestRedisLimiter_SnapshotMergesDegradedFallbackIntoRedisView(t *testing.T) {
	t.Setenv(breakerCooldownEnvKey, "1") // bounded cooldown so the drain phase can probe
	_, lim, client := newRedisLimiterFixture(t)
	ctx := context.Background()
	scope := ScopeKey("key:90:merged")
	key := "conc:key:90:merged"

	// Two leases granted through shared Redis before the outage.
	relR1, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 10}})
	require.NoError(t, err)
	relR2, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 10}})
	require.NoError(t, err)
	require.EqualValues(t, 2, client.ZCard(ctx, key).Val(),
		"setup: two Redis-backed leases hold the bucket")

	// Outage window: point the limiter at a dead endpoint — two more slots
	// are granted fail-open into the local fallback counter (D-09).
	original := lim.rdb
	lim.rdb = deadRedisClient(t)
	t.Cleanup(func() { lim.rdb = original })
	relF1, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 10}})
	require.NoError(t, err, "outage must fail open")
	relF2, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 10}})
	require.NoError(t, err, "outage must fail open")

	// Reads are reachable again: the merged view must sum both backends.
	lim.rdb = original
	rows, err := lim.Snapshot(ctx)
	require.NoError(t, err, "a readable backend must never error the snapshot")
	require.Len(t, rows, 1, "the same scope must merge into a single row")
	require.Equal(t, scope, rows[0].Scope)
	require.Equal(t, 4, rows[0].Current,
		"merged current = Redis ZCARD (2) + local fallback slots (2); showing only 2 would be the D-15 sudden drop")
	require.Zero(t, rows[0].Limit, "Limit column stays 0 — the controller back-fills it (D-02)")

	// Drain: wait out the D-11 cooldown so the probe heals the breaker (a
	// Redis release inside the cooldown would buffer instead of ZREM), then
	// every backend releases its own slots and the merged view empties.
	time.Sleep(1200 * time.Millisecond)
	probe, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 10}})
	require.NoError(t, err, "the probe must land back in shared Redis")
	require.NotNil(t, probe)
	probe()
	relF1()
	relF2()
	relR1()
	relR2()
	rows, err = lim.Snapshot(ctx)
	require.NoError(t, err)
	require.Empty(t, rows, "every released slot must leave the merged view")
}

// TestRedisLimiter_DegradedStatusLifecycle pins the D-14 observation state
// machine on the Redis backend: fresh = zero value (healthy); a transport
// failure records active + lastDegradedAt (unix millis) + the first
// consecutive failure; a snapshot read failure counts as one more consecutive
// failure (stable-prefix WARN, err=nil); recovery — via a successful probe
// acquire or a successful snapshot read — clears active and zeroes the
// counter while lastDegradedAt is RETAINED for the forensic trail; a later
// re-entry starts a fresh count. The accessor is read-only: it never drives
// the breaker or the lease paths. References DegradedStatus /
// Limiter.DegradedStatus, which do not exist on the 02-08/02-10 skeleton —
// compile red, recorded separately from the behavior reds (02-07 convention).
func TestRedisLimiter_DegradedStatusLifecycle(t *testing.T) {
	t.Setenv(breakerCooldownEnvKey, "1") // keep the probe window bounded
	_, lim, _ := newRedisLimiterFixture(t)
	ctx := context.Background()
	scope := ScopeKey("key:91:observed")
	logs := captureSysErrorWriter(t)

	// Fresh limiter: zero value — healthy baseline.
	require.Equal(t, DegradedStatus{}, lim.DegradedStatus(),
		"a fresh RedisLimiter must report the zero-value healthy status")

	// 1) Enter degradation: a transport failure trips the breaker (D-11) and
	//    records the observation (D-14).
	before := time.Now().UnixMilli()
	originalAcquire := acquireScript
	acquireScript = redis.NewScript(`return error('outage')`)
	t.Cleanup(func() { acquireScript = originalAcquire })
	rel, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err, "infrastructure fault must fail open (D-08 taxonomy)")
	rel()
	st := lim.DegradedStatus()
	require.True(t, st.Active, "a fail-open degrade must set active")
	require.GreaterOrEqual(t, st.LastDegradedAt, before,
		"entering degradation must stamp lastDegradedAt with unix milliseconds")
	require.Equal(t, 1, st.ConsecutiveFailures, "first failure starts the consecutive count")

	// 2) A snapshot read failure against a dead endpoint counts as one more
	//    consecutive failure and must WARN with the stable prefix (err stays
	//    nil — the admin poll never breaks).
	originalRDB := lim.rdb
	lim.rdb = deadRedisClient(t)
	t.Cleanup(func() { lim.rdb = originalRDB })
	rows, snapErr := lim.Snapshot(ctx)
	require.NoError(t, snapErr, "snapshot read failure must fall back, never error (D-15)")
	require.Empty(t, rows, "no local slots are held — the fallback view is empty")
	st = lim.DegradedStatus()
	require.True(t, st.Active, "the snapshot read failure keeps the status active")
	require.Equal(t, 2, st.ConsecutiveFailures,
		"a snapshot read failure must count as one more consecutive failure")
	require.Contains(t, logs.String(), degradeWarnPrefix,
		"the snapshot fallback must WARN with the stable prefix")
	lim.rdb = originalRDB

	// 3) Recovery via a successful probe acquire: heal the backend, active
	//    clears, the counter zeroes, lastDegradedAt is retained (D-14 trail).
	acquireScript = originalAcquire     // backend answers again
	time.Sleep(1200 * time.Millisecond) // outlive the 1s cooldown → the next acquire is the probe
	probe, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err, "the probe must reach the healed backend")
	require.NotNil(t, probe)
	probe()
	st = lim.DegradedStatus()
	require.False(t, st.Active, "a successful probe must clear active")
	require.Zero(t, st.ConsecutiveFailures, "recovery must zero the consecutive failure count")
	retained := st.LastDegradedAt
	require.Greater(t, retained, int64(0),
		"lastDegradedAt must survive recovery — 'last degraded at' stays for forensics")

	// 4) Re-entry after recovery: a fresh failure turns active again with a
	//    new count of 1 and a timestamp at or after the retained one.
	acquireScript = redis.NewScript(`return error('outage again')`)
	rel2, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err)
	rel2()
	st = lim.DegradedStatus()
	require.True(t, st.Active, "re-entry after recovery must set active again")
	require.Equal(t, 1, st.ConsecutiveFailures, "the count restarts at re-entry")
	require.GreaterOrEqual(t, st.LastDegradedAt, retained,
		"re-entry must refresh lastDegradedAt")
	acquireScript = originalAcquire

	// 5) Recovery via a successful snapshot read (planner discretion: a
	//    healthy read is recovery evidence even at zero traffic) — the same
	//    clearing semantics, lastDegradedAt still retained.
	rows, snapErr = lim.Snapshot(ctx)
	require.NoError(t, snapErr)
	require.Empty(t, rows, "fallback drained; Redis holds nothing either after probe release")
	st = lim.DegradedStatus()
	require.False(t, st.Active, "a successful snapshot read must clear active")
	require.Zero(t, st.ConsecutiveFailures)
	require.GreaterOrEqual(t, st.LastDegradedAt, retained,
		"lastDegradedAt must never be cleared by recovery")
}

// TestRedisLimiter_RecoveryIsolatesBackends pins the recovery path: after an
// outage window (client pointed at a dead endpoint) new grants return to the
// shared Redis count, and every release stays on the backend that granted it —
// a degraded release must never clear a Redis slot, and a Redis release must
// never touch the degraded counter.
func TestRedisLimiter_RecoveryIsolatesBackends(t *testing.T) {
	t.Setenv(breakerCooldownEnvKey, "1") // D-11: recovery goes through the cooldown probe
	_, lim, client := newRedisLimiterFixture(t)
	ctx := context.Background()
	scope := ScopeKey("key:81:recovery")
	key := "conc:key:81:recovery"

	// Healthy grant lands in shared Redis.
	beforeOutage, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err)

	// Outage: point the limiter at a dead endpoint; grants fail open.
	original := lim.rdb
	lim.rdb = deadRedisClient(t)
	t.Cleanup(func() { lim.rdb = original })

	degraded, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err, "outage must fail open")
	require.NotNil(t, degraded)

	// Recovery: point back at the live client. The failed acquire opened the
	// D-11 cooldown, so recovery is no longer instant — poll (bounded, no
	// fixed sleep) until the cooldown expires and the probe lands the grant
	// in shared Redis; degraded attempts inside the window release locally.
	lim.rdb = original
	var recovered func()
	require.Eventually(t, func() bool {
		rel, acquireErr := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
		if acquireErr != nil || rel == nil {
			return false
		}
		if client.ZCard(ctx, key).Val() == 2 {
			recovered = rel
			return true
		}
		rel() // still cooling down (degraded) — drain the fallback counter
		return false
	}, 5*time.Second, 100*time.Millisecond,
		"after the D-11 cooldown the probe must land the recovered grant in shared Redis")
	require.NotNil(t, recovered)
	require.EqualValues(t, 2, client.ZCard(ctx, key).Val(),
		"only Redis-backed grants (before outage + after recovery) count in the ZSET")

	// Backend isolation: each release only clears its own counter.
	degraded()
	require.EqualValues(t, 2, client.ZCard(ctx, key).Val(),
		"the degraded release must not clear any shared Redis slot")
	beforeOutage()
	require.EqualValues(t, 1, client.ZCard(ctx, key).Val())
	recovered()
	require.EqualValues(t, 0, client.ZCard(ctx, key).Val())
	require.Empty(t, client.SMembers(ctx, "conc:registry").Val(),
		"the registry must drain once every Redis-backed release runs")
}

// TestInitRedisClient_UnreachableStillStarts pins D-12 (RED-04 self-heal):
// REDIS_CONN_STRING pointing at a dead address must NOT abort startup. The
// pre-D-12 form is FatalLog → os.Exit(1), which kills this test process
// outright (process-exit red); the softened form returns nil, keeps
// RedisEnabled=true (so main.go still installs RedisLimiter and fails open
// per request until Redis recovers), leaves RDB constructed, and WARNs.
func TestInitRedisClient_UnreachableStillStarts(t *testing.T) {
	// Orphan port: listen, capture the address, close — dials get refused.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	deadAddr := ln.Addr().String()
	require.NoError(t, ln.Close())

	prevRDB, prevEnabled := common.RDB, common.RedisEnabled
	t.Cleanup(func() {
		common.RDB = prevRDB
		common.RedisEnabled = prevEnabled
	})
	// Force the plain-client path even if the ambient env enables Sentinel.
	t.Setenv("REDIS_SENTINEL_MODE", "")
	t.Setenv("REDIS_CONN_STRING", "redis://"+deadAddr+"/0")
	logs := captureSysErrorWriter(t)

	initErr := common.InitRedisClient()
	require.NoError(t, initErr,
		"an unreachable Redis must not fail startup (D-12: ping failure is reachability, not config error)")
	require.True(t, common.RedisEnabled,
		"RedisEnabled must stay true so main.go installs the RedisLimiter and self-heals (RED-04/D-12)")
	require.NotNil(t, common.RDB,
		"RDB must stay constructed so the limiter can fail open until Redis recovers")
	require.Contains(t, logs.String(), "[SYS]",
		"the softened ping failure must WARN (SysError), not exit")
	require.Contains(t, logs.String(), "Redis ping test failed",
		"the WARN must name the ping failure for operators")
}

// dockerRedisAddr gates the ②-layer suite on TEST_REDIS_ADDR (D-22/02-03
// TEST_MYSQL_DSN precedent): unset → explicit t.Skip; set but unreachable →
// the caller's ping require fails the test (never a silent skip).
func dockerRedisAddr(t *testing.T) string {
	t.Helper()
	addr := strings.TrimSpace(os.Getenv("TEST_REDIS_ADDR"))
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR is not configured")
	}
	return addr
}

// resetDockerConcurrencyState wipes the conc: namespace (registry members
// first, then the registry itself, then the caller's known keys) so each
// docker test starts from a clean baseline and can assert DBSIZE returns to
// it. Returns the post-cleanup DBSIZE.
func resetDockerConcurrencyState(t *testing.T, client *redis.Client, keys ...string) int64 {
	t.Helper()
	ctx := context.Background()
	for _, k := range client.SMembers(ctx, "conc:registry").Val() {
		require.NoError(t, client.Del(ctx, k).Err())
	}
	require.NoError(t, client.Del(ctx, "conc:registry").Err())
	if len(keys) > 0 {
		require.NoError(t, client.Del(ctx, keys...).Err())
	}
	return client.DBSize(ctx).Val()
}

// TestRedisLimiterDocker_NodesHammerSharedScopeNeverExceedsLimit is the ②
// layer of D-21: 3 RedisLimiter instances (one client each = simulated
// nodes) hammer one shared scope against real Redis (redis.call('TIME') and
// all). Phase 1 is a deterministic barrier — every goroutine attempts before
// any release — so exactly `limit` grants and `goroutines-limit` refusals
// must land and the concurrent gauge must reach exactly `limit` (proves the
// cap is actually hit, non-vacuous). Phase 2 is a free-running N-goroutine ×
// M-round hammer: the atomic gauge must never exceed the limit (over-issue
// would fail it), granted+rejected must equal attempts, and the run must
// finish with ZCARD=0, an empty registry, DBSIZE back at baseline, and no
// fail-open WARN (real Redis must never degrade).
func TestRedisLimiterDocker_NodesHammerSharedScopeNeverExceedsLimit(t *testing.T) {
	addr := dockerRedisAddr(t)
	ctx := context.Background()
	logs := captureSysErrorWriter(t)

	const (
		limit      = 6
		nodes      = 3
		goroutines = 24
		rounds     = 10
	)
	scope := ScopeKey("key:docker-hammer:*")
	key := "conc:key:docker-hammer:*"

	limiters := make([]*RedisLimiter, 0, nodes)
	var control *redis.Client
	for i := 0; i < nodes; i++ {
		c := redis.NewClient(&redis.Options{Addr: addr})
		require.NoError(t, c.Ping(ctx).Err(),
			"TEST_REDIS_ADDR is set but not reachable — docker ② layer must run, not skip (D-22)")
		t.Cleanup(func() { _ = c.Close() })
		if control == nil {
			control = c
		}
		limiter := NewRedisLimiter(c, "conc:")
		t.Cleanup(limiter.Close) // D-01: join each simulated node's renewal ticker
		limiters = append(limiters, limiter)
	}
	baseline := resetDockerConcurrencyState(t, control, key)

	var attempts, granted, rejected, violations, unexpected atomic.Int64
	var gauge, maxGauge atomic.Int64
	var mu sync.Mutex
	var firstUnexpected string
	noteUnexpected := func(err error) {
		unexpected.Add(1)
		mu.Lock()
		if firstUnexpected == "" {
			firstUnexpected = err.Error()
		}
		mu.Unlock()
	}
	trackMax := func(cur int64) {
		for {
			old := maxGauge.Load()
			if cur <= old || maxGauge.CompareAndSwap(old, cur) {
				return
			}
		}
	}
	attempt := func(lim *RedisLimiter, sl ScopeLimit) func() {
		attempts.Add(1)
		release, err := lim.AcquireBatch(ctx, []ScopeLimit{sl})
		if err != nil {
			if errors.Is(err, ErrConcurrencyExceeded) {
				rejected.Add(1)
			} else {
				noteUnexpected(err)
			}
			return nil
		}
		granted.Add(1)
		cur := gauge.Add(1)
		trackMax(cur)
		if cur > limit {
			violations.Add(1)
		}
		return release
	}

	// Phase 1 — deterministic barrier: all goroutines attempt before any
	// release, so exactly `limit` must be granted on a clean bucket.
	var attempted sync.WaitGroup
	attempted.Add(goroutines)
	releaseSignal := make(chan struct{})
	// If a barrier assertion fails (require → Goexit), the holders parked on
	// releaseSignal must still be woken or the test would hang to timeout.
	var releaseOnce sync.Once
	releaseHolders := func() { releaseOnce.Do(func() { close(releaseSignal) }) }
	t.Cleanup(releaseHolders)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(node int) {
			defer wg.Done()
			<-start
			release := attempt(limiters[node%nodes], ScopeLimit{Scope: scope, Limit: limit})
			attempted.Done()
			if release != nil {
				<-releaseSignal // hold the slot so every grant overlaps
				gauge.Add(-1)
				release()
			}
		}(i)
	}
	close(start)
	attempted.Wait()
	require.EqualValues(t, limit, granted.Load(),
		"barrier: with no releases until every attempt lands, exactly `limit` batches must be granted")
	require.EqualValues(t, goroutines-limit, rejected.Load(),
		"barrier: every attempt past the cap must be refused")
	require.EqualValues(t, limit, gauge.Load(),
		"all granted slots must be held concurrently at the barrier")
	releaseHolders()
	wg.Wait()

	// Phase 2 — free-running hammer across all simulated nodes.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(node int) {
			defer wg.Done()
			lim := limiters[node%nodes]
			for r := 0; r < rounds; r++ {
				release := attempt(lim, ScopeLimit{Scope: scope, Limit: limit})
				if release == nil {
					continue
				}
				for j := 0; j < 8; j++ {
					runtime.Gosched()
				}
				gauge.Add(-1)
				release()
			}
		}(i)
	}
	wg.Wait()

	require.Zero(t, violations.Load(),
		"the shared count must never exceed the limit — over-issue on real Redis (RED-01)")
	require.Zero(t, unexpected.Load(),
		"non-nil error may only ever be ErrConcurrencyExceeded (D-08 taxonomy), first=%s", firstUnexpected)
	require.Equal(t, attempts.Load(), granted.Load()+rejected.Load(),
		"granted + rejected must equal attempts")
	require.GreaterOrEqual(t, maxGauge.Load(), int64(limit),
		"the cap must actually have been reached (non-vacuous contention)")
	require.EqualValues(t, 0, control.ZCard(ctx, key).Val(),
		"every granted batch must have released (ZCARD drains to 0)")
	require.Empty(t, control.SMembers(ctx, "conc:registry").Val(),
		"the release cleanup chain must drain the registry on the real engine (D-17/D-18)")
	require.EqualValues(t, baseline, control.DBSize(ctx).Val(),
		"DBSIZE must return to baseline — no key accumulation")
	require.NotContains(t, logs.String(), degradeWarnPrefix,
		"a healthy docker Redis must never trigger fail-open degradation")
	t.Logf("attempts=%d granted=%d rejected=%d maxGauge=%d limit=%d",
		attempts.Load(), granted.Load(), rejected.Load(), maxGauge.Load(), limit)
}

// TestRedisLimiterDocker_BatchHammerAllOrNothing pins D-05 on the real
// engine: batches of {A limit 3, B limit 2} hammered from many goroutines
// across 3 instances — the barrier phase must grant exactly min(A,B)=2
// batches (B fills first), the per-bucket gauges must never exceed their
// limits, a partial grant on a refused batch would leave an orphan member
// that fails the end-state ZCARD=0 / DBSIZE==baseline checks, and the
// registry must drain.
func TestRedisLimiterDocker_BatchHammerAllOrNothing(t *testing.T) {
	addr := dockerRedisAddr(t)
	ctx := context.Background()
	logs := captureSysErrorWriter(t)

	const (
		limitA     = 3
		limitB     = 2
		nodes      = 3
		goroutines = 18
		rounds     = 10
	)
	scopeA := ScopeKey("key:docker-batch-a:*")
	scopeB := ScopeKey("key:docker-batch-b:*")
	keyA := "conc:key:docker-batch-a:*"
	keyB := "conc:key:docker-batch-b:*"
	batch := []ScopeLimit{{Scope: scopeA, Limit: limitA}, {Scope: scopeB, Limit: limitB}}

	limiters := make([]*RedisLimiter, 0, nodes)
	var control *redis.Client
	for i := 0; i < nodes; i++ {
		c := redis.NewClient(&redis.Options{Addr: addr})
		require.NoError(t, c.Ping(ctx).Err(),
			"TEST_REDIS_ADDR is set but not reachable — docker ② layer must run, not skip (D-22)")
		t.Cleanup(func() { _ = c.Close() })
		if control == nil {
			control = c
		}
		limiter := NewRedisLimiter(c, "conc:")
		t.Cleanup(limiter.Close) // D-01: join each simulated node's renewal ticker
		limiters = append(limiters, limiter)
	}
	baseline := resetDockerConcurrencyState(t, control, keyA, keyB)

	var attempts, granted, rejected, violations, unexpected atomic.Int64
	var gaugeA, gaugeB, maxA, maxB atomic.Int64
	var mu sync.Mutex
	var firstUnexpected string
	track := func(m *atomic.Int64, cur int64) {
		for {
			old := m.Load()
			if cur <= old || m.CompareAndSwap(old, cur) {
				return
			}
		}
	}
	attempt := func(lim *RedisLimiter) func() {
		attempts.Add(1)
		release, err := lim.AcquireBatch(ctx, batch)
		if err != nil {
			if errors.Is(err, ErrConcurrencyExceeded) {
				rejected.Add(1)
			} else {
				unexpected.Add(1)
				mu.Lock()
				if firstUnexpected == "" {
					firstUnexpected = err.Error()
				}
				mu.Unlock()
			}
			return nil
		}
		granted.Add(1)
		curA := gaugeA.Add(1)
		curB := gaugeB.Add(1)
		track(&maxA, curA)
		track(&maxB, curB)
		if curA > limitA || curB > limitB {
			violations.Add(1)
		}
		return release
	}

	// Barrier phase: all attempts land before any release — bucket B (limit
	// 2) fills first, so exactly 2 batches are granted and both buckets hold
	// exactly 2 members.
	var attempted sync.WaitGroup
	attempted.Add(goroutines)
	releaseSignal := make(chan struct{})
	// If a barrier assertion fails (require → Goexit), the holders parked on
	// releaseSignal must still be woken or the test would hang to timeout.
	var releaseOnce sync.Once
	releaseHolders := func() { releaseOnce.Do(func() { close(releaseSignal) }) }
	t.Cleanup(releaseHolders)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(node int) {
			defer wg.Done()
			<-start
			release := attempt(limiters[node%nodes])
			attempted.Done()
			if release != nil {
				<-releaseSignal
				gaugeA.Add(-1)
				gaugeB.Add(-1)
				release()
			}
		}(i)
	}
	close(start)
	attempted.Wait()
	require.EqualValues(t, limitB, granted.Load(),
		"barrier: the tighter bucket (B, limit 2) must cap the batch grants at exactly 2")
	require.EqualValues(t, goroutines-limitB, rejected.Load(),
		"barrier: every batch past the cap must be refused as one unit")
	require.EqualValues(t, 2, control.ZCard(ctx, keyA).Val(),
		"granted batches must each have written one member to A")
	require.EqualValues(t, 2, control.ZCard(ctx, keyB).Val(),
		"granted batches must each have written one member to B")
	releaseHolders()
	wg.Wait()

	// Free-running batch hammer.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(node int) {
			defer wg.Done()
			lim := limiters[node%nodes]
			for r := 0; r < rounds; r++ {
				release := attempt(lim)
				if release == nil {
					continue
				}
				for j := 0; j < 8; j++ {
					runtime.Gosched()
				}
				gaugeA.Add(-1)
				gaugeB.Add(-1)
				release()
			}
		}(i)
	}
	wg.Wait()

	require.Zero(t, violations.Load(),
		"per-bucket gauges must never exceed their limits (RED-01, no over-issue)")
	require.Zero(t, unexpected.Load(),
		"non-nil error may only ever be ErrConcurrencyExceeded (D-08 taxonomy), first=%s", firstUnexpected)
	require.Equal(t, attempts.Load(), granted.Load()+rejected.Load(),
		"granted + rejected must equal attempts")
	require.LessOrEqual(t, maxA.Load(), int64(limitA))
	require.LessOrEqual(t, maxB.Load(), int64(limitB))
	require.GreaterOrEqual(t, maxB.Load(), int64(limitB),
		"the tighter bucket must actually have been reached (non-vacuous)")
	require.EqualValues(t, 0, control.ZCard(ctx, keyA).Val(),
		"a refused batch must leave no orphan member in A (no partial grant)")
	require.EqualValues(t, 0, control.ZCard(ctx, keyB).Val(),
		"a refused batch must leave no orphan member in B (no partial grant)")
	require.Empty(t, control.SMembers(ctx, "conc:registry").Val(),
		"the registry must drain on the real engine (D-17/D-18)")
	require.EqualValues(t, baseline, control.DBSize(ctx).Val(),
		"DBSIZE must return to baseline — no key accumulation")
	require.NotContains(t, logs.String(), degradeWarnPrefix,
		"a healthy docker Redis must never trigger fail-open degradation")
	t.Logf("attempts=%d granted=%d rejected=%d maxA=%d maxB=%d",
		attempts.Load(), granted.Load(), rejected.Load(), maxA.Load(), maxB.Load())
}

// renewalWarnPrefix is the stable D-02 stop-renewal WARN marker (02-10). The
// tests assert on this literal so the renewal behavior suite compiles and runs
// as a behavior red against the 02-08 skeleton, which has no renewal pass yet.
const renewalWarnPrefix = "concurrency: renewal stopped"

// TestRedisLimiterRenewalExtendsScoreMonotonically pins D-01/D-03 renewal:
// with a short TTL injected (3s → TTL/3 = 1s ticker interval), the node's
// renewal ticker must batch-extend the held lease's ZSET score monotonically
// beyond the initial grant, and the score must stay in the future while the
// lease is held. Against the 02-08 skeleton (no renewal pass) the score never
// moves — behavior red, not a compile error.
func TestRedisLimiterRenewalExtendsScoreMonotonically(t *testing.T) {
	t.Setenv(leaseTTLEnv, "3") // TTL 3s → renewal interval 1s (D-03/D-04)
	_, lim, client := newRedisLimiterFixture(t)
	ctx := context.Background()
	scope := ScopeKey("key:90:renewal")
	key := "conc:key:90:renewal"

	release, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err)
	t.Cleanup(release)
	members := client.ZRange(ctx, key, 0, -1).Val()
	require.Len(t, members, 1, "the granted batch must hold exactly one member")
	member := members[0]
	score0 := client.ZScore(ctx, key, member).Val()
	require.Greater(t, score0, float64(0), "the grant must write an expiry score")

	// First renewal tick must move the score strictly forward.
	require.Eventually(t, func() bool {
		return client.ZScore(ctx, key, member).Val() > score0
	}, 10*time.Second, 100*time.Millisecond,
		"renewal ticker must extend the held lease's score beyond the initial grant")

	// A second observation must move it again — monotonic extrapolation.
	firstRenewed := client.ZScore(ctx, key, member).Val()
	require.Eventually(t, func() bool {
		return client.ZScore(ctx, key, member).Val() > firstRenewed
	}, 10*time.Second, 100*time.Millisecond,
		"renewal score must keep extrapolating monotonically on every tick")
	require.Greater(t, client.ZScore(ctx, key, member).Val(), float64(time.Now().UnixMilli()),
		"while held, the renewed score must stay in the future (lease not expired)")
}

// TestRedisLimiterRenewalKeepsLongHoldAcrossTTLs pins the Pitfall-2 scenario:
// a holder kept alive across more than 2× the lease TTL must NOT be reclaimed,
// because the node ticker renews it every TTL/3 (D-01). The sleep is not a
// condition wait — crossing the TTL horizon is the behavior under test; the
// assertion afterwards is the state check. Against the 02-08 skeleton the
// lease expires with the TTL and the probe acquire is granted — behavior red.
func TestRedisLimiterRenewalKeepsLongHoldAcrossTTLs(t *testing.T) {
	const ttl = 3
	t.Setenv(leaseTTLEnv, strconv.Itoa(ttl)) // TTL 3s → renewal interval 1s
	_, lim, client := newRedisLimiterFixture(t)
	ctx := context.Background()
	scope := ScopeKey("key:91:longhold")
	key := "conc:key:91:longhold"

	hold, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 1}})
	require.NoError(t, err)
	t.Cleanup(hold)

	// Fresh state sanity: limit=1 refuses a second holder immediately.
	_, err = lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 1}})
	require.ErrorIs(t, err, ErrConcurrencyExceeded, "sanity: limit=1 must refuse while fresh")

	// Hold past 2× the initial TTL (6s+); renewal ticks keep the score ahead.
	time.Sleep(2*ttl*time.Second + time.Second)

	_, err = lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 1}})
	require.ErrorIs(t, err, ErrConcurrencyExceeded,
		"a long hold must survive beyond 2× TTL via renewal — reclamation means renewal is missing (Pitfall 2)")

	// Release drains the slot, proving the refusal above was the live lease.
	hold()
	after, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 1}})
	require.NoError(t, err, "release must still drain the renewed slot")
	after()
	require.EqualValues(t, 0, client.ZCard(ctx, key).Val(),
		"full release must drain the scope ZSET after renewal has run")
}

// TestRedisLimiterRenewalStopsWhenLeaseReclaimed pins the D-02 stop-renewal
// branch: when a member vanishes from the ZSET while this node still believes
// it holds it (GC stall / failover window past the TTL), the renewal pass must
// drop the held entry, emit exactly one stable-prefix WARN, and never
// re-acquire — the scope stays empty across further ticks (fewer counts beats
// corruption). Against the 02-08 skeleton no renewal pass exists, so no WARN
// ever fires — behavior red, not a compile error.
func TestRedisLimiterRenewalStopsWhenLeaseReclaimed(t *testing.T) {
	t.Setenv(leaseTTLEnv, "3") // TTL 3s → renewal interval 1s
	_, lim, client := newRedisLimiterFixture(t)
	ctx := context.Background()
	scope := ScopeKey("key:92:reclaimed")
	key := "conc:key:92:reclaimed"
	logs := captureSysErrorWriter(t)

	release, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err)
	t.Cleanup(release)
	members := client.ZRange(ctx, key, 0, -1).Val()
	require.Len(t, members, 1)
	member := members[0]

	// Simulate an external reclaim: the member disappears from the ZSET while
	// the limiter still tracks it as held.
	require.NoError(t, client.ZRem(ctx, key, member).Err())

	require.Eventually(t, func() bool {
		return strings.Contains(logs.String(), renewalWarnPrefix)
	}, 10*time.Second, 100*time.Millisecond,
		"D-02: a reclaimed lease must trigger the stop-renewal WARN on the next tick")

	// Zero re-acquire writes: further ticks must leave the scope empty and the
	// stop-renewal WARN must fire exactly once (the entry was dropped).
	time.Sleep(2500 * time.Millisecond)
	require.EqualValues(t, 0, client.ZCard(ctx, key).Val(),
		"stop-renewal must never re-acquire the reclaimed member")
	require.NotContains(t, client.ZRange(ctx, key, 0, -1).Val(), member,
		"the reclaimed member must not reappear")
	require.Equal(t, 1, strings.Count(logs.String(), renewalWarnPrefix),
		"the stop-renewal WARN must fire exactly once — the held entry is dropped after discovery")
}

// TestRedisLimiterRenewalCloseStopsRenewalWrites pins the D-01 ticker
// lifecycle: Close() stops the renewal goroutine and joins it, after which no
// renewal write may land (score frozen); Close is idempotent. D-19 makes
// Close() also bulk-clean held leases through flushScript, so the shutdown
// flush is deliberately blocked here (fault-injected script error) — this
// test observes the ticker contract on a member that survives Close; the
// successful bulk cleanup is TestRedisLimiterShutdownBulkClearsHeldAndPending.
func TestRedisLimiterRenewalCloseStopsRenewalWrites(t *testing.T) {
	t.Setenv(leaseTTLEnv, "2") // TTL 2s → renewal interval ~667ms
	_, lim, client := newRedisLimiterFixture(t)
	ctx := context.Background()
	scope := ScopeKey("key:93:close")
	key := "conc:key:93:close"

	release, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err)
	t.Cleanup(release)
	members := client.ZRange(ctx, key, 0, -1).Val()
	require.Len(t, members, 1)
	member := members[0]

	originalFlush := flushScript
	flushScript = redis.NewScript(`return error('shutdown flush blocked for this test')`)
	t.Cleanup(func() { flushScript = originalFlush })
	lim.Close()
	lim.Close() // idempotent: second call must not panic or block
	scoreAtClose := client.ZScore(ctx, key, member).Val()
	require.Greater(t, scoreAtClose, float64(0),
		"blocked shutdown flush must leave the member in place (D-19 TTL backstop)")

	// Well past one ticker interval: with the ticker joined, the score must
	// be byte-for-byte unchanged — zero renewal writes after Close.
	time.Sleep(2*lim.renewInterval + 300*time.Millisecond)
	require.Equal(t, scoreAtClose, client.ZScore(ctx, key, member).Val(),
		"no renewal write may land after Close() stopped and joined the ticker (D-01 lifecycle)")

	release()
	require.EqualValues(t, 0, client.ZCard(ctx, key).Val(),
		"release must still drain the slot after Close")
}

// TestRedisLimiterRenewalZAddXXDoesNotReviveReleasedMember pins the ZADD XX
// renewal contract (research accepted #1) at the script level: renewing a
// member that was already released reports 0 (gone) and never re-adds it — no
// zombie slot revival — while an existing member in the same call reports 1
// and gets its score extended. Also drives the limiter's renewal pass directly
// to prove an empty held registry performs zero writes. References renewScript,
// which does not exist on the 02-08 skeleton — compile red, recorded
// separately.
func TestRedisLimiterRenewalZAddXXDoesNotReviveReleasedMember(t *testing.T) {
	_, lim, client := newRedisLimiterFixture(t)
	ctx := context.Background()
	scope := ScopeKey("key:94:zaddxx")
	key := "conc:key:94:zaddxx"

	relGone, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err)
	goneMember := client.ZRange(ctx, key, 0, -1).Val()[0]
	relGone() // released: member drained from the ZSET

	relLive, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err)
	t.Cleanup(relLive)
	liveMember := client.ZRange(ctx, key, 0, -1).Val()[0]
	liveScore := client.ZScore(ctx, key, liveMember).Val()

	// One renewal call over both members: released → 0 (never revived),
	// live → 1 (score extended). The 10ms wait is clock granularity only —
	// scores are millisecond-resolution, so the renewed score must differ.
	time.Sleep(10 * time.Millisecond)
	ttlMs := common.GetEnvOrDefault(leaseTTLEnv, defaultLeaseTTLs) * 1000
	res, err := renewScript.Run(ctx, client, []string{key},
		ttlMs, 1, goneMember, 1, liveMember).Int64Slice()
	require.NoError(t, err)
	require.Equal(t, []int64{0, 1}, res,
		"ZADD XX CH must report the released member as gone (0) and the live member as renewed (1)")
	require.EqualValues(t, 1, client.ZCard(ctx, key).Val(),
		"renewal must never revive a released member — no zombie slot (T-02-18)")
	require.NotContains(t, client.ZRange(ctx, key, 0, -1).Val(), goneMember,
		"the released member must stay absent after renewal")
	require.Greater(t, client.ZScore(ctx, key, liveMember).Val(), liveScore,
		"the live member's score must be extended by the same call")

	// The limiter-level pass with an empty held registry is a strict no-op.
	relLive()
	lim.renewHeldLeases()
	require.EqualValues(t, 0, client.ZCard(ctx, key).Val(),
		"renewal pass with nothing held must write nothing")
	require.Empty(t, client.SMembers(ctx, "conc:registry").Val(),
		"registry must stay drained")
}

// ---------------------------------------------------------------------------
// 02-10 Task 02 behavior suite: cooldown breaker (D-11), pending flush (D-13),
// shutdown bulk cleanup (D-19). The cooldown env name and the WARN markers are
// declared test-local with their literal values so the suite compiles and runs
// as a behavior red against the pre-breaker code (Task 01 shape) — the same
// convention renewalWarnPrefix established above.
// ---------------------------------------------------------------------------

const breakerCooldownEnvKey = "REDIS_CONCURRENCY_BREAKER_COOLDOWN_SECONDS"

// The stable D-11 WARN markers the breaker must emit (trip / recovery).
const (
	breakerTripWarn    = "concurrency: breaker opened"
	breakerRecoverWarn = "concurrency: breaker closed"
)

// countingHook counts every command that reaches the Redis backend through a
// go-redis v8 Hook — the D-11 acceptance "zero backend calls during the
// cooldown, exactly one probe after it" is asserted against this counter.
type countingHook struct{ calls atomic.Int64 }

func (h *countingHook) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	h.calls.Add(1)
	return ctx, nil
}

func (h *countingHook) AfterProcess(context.Context, redis.Cmder) error { return nil }

func (h *countingHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	h.calls.Add(1)
	return ctx, nil
}

func (h *countingHook) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

// newBreakerFixture is newRedisLimiterFixture plus the backend call counter.
func newBreakerFixture(t *testing.T) (*RedisLimiter, *redis.Client, *countingHook) {
	t.Helper()
	server := miniredis.RunT(t)
	hook := &countingHook{}
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	client.AddHook(hook)
	require.NoError(t, client.Ping(context.Background()).Err())
	t.Cleanup(func() { _ = client.Close() })
	limiter := NewRedisLimiter(client, "conc:")
	// Registered after the client cleanup, so it runs first (LIFO): the
	// shutdown bulk cleanup (D-19) still has a live client to talk to.
	t.Cleanup(limiter.Close)
	return limiter, client, hook
}

// TestRedisLimiterBreakerCooldownSkipsBackendDuringOutage pins D-11: the first
// backend failure opens a global cooldown with a trip WARN; every request
// inside the cooldown degrades straight into the fail-open path with ZERO
// Redis calls and no timeout wait; when the cooldown expires the next request
// is the single probe — exactly one backend call — and its success closes the
// breaker with a recovery WARN. Against the pre-breaker code every cooldown
// request still pays a backend attempt — behavior red, not a compile error.
func TestRedisLimiterBreakerCooldownSkipsBackendDuringOutage(t *testing.T) {
	t.Setenv(breakerCooldownEnvKey, "1") // 1s cooldown keeps the test bounded
	lim, client, hook := newBreakerFixture(t)
	ctx := context.Background()
	scope := ScopeKey("key:95:breaker")
	key := "conc:key:95:breaker"
	logs := captureSysErrorWriter(t)

	// Warm the good acquire script into miniredis so the post-cooldown probe
	// EVALSHA is a cache hit — exactly one command (deterministic counting).
	warm, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err)
	warm()

	// Trip: the first backend failure opens the cooldown (D-11).
	originalAcquire := acquireScript
	acquireScript = redis.NewScript(`return error('injected outage')`)
	t.Cleanup(func() { acquireScript = originalAcquire })
	faulted, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err, "an infrastructure fault must fail open (D-08 taxonomy)")
	require.NotNil(t, faulted, "degraded acquire must still hand out a release")
	faulted()
	acquireScript = originalAcquire // heal the backend; the cooldown still holds

	// Inside the cooldown: degrade directly — zero backend calls, no timeout wait.
	countAtCooldown := hook.calls.Load()
	start := time.Now()
	first, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err)
	require.NotNil(t, first)
	second, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err)
	require.NotNil(t, second)
	require.Equal(t, countAtCooldown, hook.calls.Load(),
		"cooldown requests must degrade without any Redis call (D-11 zero backend calls)")
	require.Less(t, time.Since(start), redisOpTimeout,
		"cooldown degrade must not wait out the operation timeout (D-11 zero latency cost)")
	first()
	second()
	require.Contains(t, logs.String(), degradeWarnPrefix,
		"cooldown degradation must keep the D-08 stable WARN prefix")

	// Cooldown expired: the next request is the single probe (half-open trial).
	time.Sleep(1200 * time.Millisecond)
	countBeforeProbe := hook.calls.Load()
	rel, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err, "a healthy backend must be back to shared counting")
	require.NotNil(t, rel)
	require.Equal(t, countBeforeProbe+1, hook.calls.Load(),
		"exactly one probe call may reach the backend when the cooldown expires")
	rel()
	require.EqualValues(t, 0, client.ZCard(ctx, key).Val(), "probe grant must release normally")
	require.Empty(t, client.SMembers(ctx, "conc:registry").Val(),
		"the release cleanup chain must drain the registry after recovery")

	// WARN semantics: trip on first failure, recovery on successful probe.
	require.Contains(t, logs.String(), breakerTripWarn,
		"the first failure must open the breaker with a trip WARN")
	require.Contains(t, logs.String(), breakerRecoverWarn,
		"a successful probe must close the breaker with a recovery WARN")
	require.Equal(t, 1, strings.Count(logs.String(), breakerTripWarn),
		"exactly one trip WARN — the healed backend must not re-trip")
}

// TestRedisLimiterBreakerProbeFailureReopensCooldown pins the half-open
// failure arm of D-11: an expired cooldown admits exactly one probe, a failing
// probe re-opens the cooldown with a fresh trip WARN (probe frequency is the
// natural WARN rate limit — D-24), and requests inside the renewed cooldown
// again reach zero backend calls. Against the pre-breaker code every request
// still hits Redis — behavior red.
func TestRedisLimiterBreakerProbeFailureReopensCooldown(t *testing.T) {
	t.Setenv(breakerCooldownEnvKey, "2") // 2s: the renewed cooldown must outlive the follow-up assertions
	lim, _, hook := newBreakerFixture(t)
	ctx := context.Background()
	scope := ScopeKey("key:96:probefail")
	logs := captureSysErrorWriter(t)

	// Backend down for good: the acquire fails and trips the breaker.
	originalAcquire := acquireScript
	acquireScript = redis.NewScript(`return error('still down')`)
	t.Cleanup(func() { acquireScript = originalAcquire })
	trip, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err, "must fail open (taxonomy)")
	require.NotNil(t, trip)
	trip()

	// Wait out the cooldown: the next request becomes the probe and fails.
	time.Sleep(2200 * time.Millisecond)
	countBeforeProbe := hook.calls.Load()
	probe, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err)
	require.NotNil(t, probe)
	require.Greater(t, hook.calls.Load(), countBeforeProbe,
		"the expired cooldown must admit the probe request to the backend")
	probe()

	// The failed probe re-opened the cooldown: further requests degrade with
	// zero backend calls (no per-request timeout attempts, D-11).
	countAfterProbe := hook.calls.Load()
	for range 3 {
		rel, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
		require.NoError(t, err, "degraded requests keep failing open (taxonomy)")
		require.NotNil(t, rel)
		rel()
	}
	require.Equal(t, countAfterProbe, hook.calls.Load(),
		"a failed probe must restart the cooldown — zero backend calls until it expires again")

	// WARN rate limit (D-24): one trip for the initial failure, one for the
	// failed probe — nothing per degraded request.
	require.Equal(t, 2, strings.Count(logs.String(), breakerTripWarn),
		"exactly one trip WARN per failed backend attempt (probe-paced, not per-request)")
	require.NotContains(t, logs.String(), breakerRecoverWarn,
		"no recovery WARN may fire while probes keep failing")
}

// TestRedisLimiterBreakerProbeRecoversAndFlushesPending pins D-13 end to end:
// a release that hits the outage moves (member, keys) into the local pending
// buffer (instead of WARN-and-forget), and the first successful probe after
// the cooldown flushes it through lease_flush.lua — ZCARD returns to the
// actual in-flight count, so a stale lease can never fill the bucket and
// cause a secondary 429 after recovery. Genuine over-limit refusals keep
// working afterwards (D-08 taxonomy). Against the pre-breaker code the stale
// member is never flushed — behavior red.
func TestRedisLimiterBreakerProbeRecoversAndFlushesPending(t *testing.T) {
	t.Setenv(breakerCooldownEnvKey, "1")
	lim, client, _ := newBreakerFixture(t)
	ctx := context.Background()
	scope := ScopeKey("key:97:pendingflush")
	key := "conc:key:97:pendingflush"
	logs := captureSysErrorWriter(t)

	// A Redis-granted hold whose release hits the outage (limit 2 leaves room
	// for the recovery probe to be granted alongside the stale member).
	hold, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 2}})
	require.NoError(t, err)
	members := client.ZRange(ctx, key, 0, -1).Val()
	require.Len(t, members, 1, "the hold must land one member in the bucket")
	stale := members[0]

	originalRelease := releaseScript
	releaseScript = redis.NewScript(`return error('release outage')`)
	t.Cleanup(func() { releaseScript = originalRelease })
	hold() // fails → buffered for flush (D-13); also trips the breaker
	releaseScript = originalRelease
	require.EqualValues(t, 1, client.ZCard(ctx, key).Val(),
		"until flush, the stale lease still occupies the bucket")

	// Cooldown elapses; the next successful acquire is the probe and must
	// flush the pending release before shared counting resumes.
	time.Sleep(1200 * time.Millisecond)
	rel, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 2}})
	require.NoError(t, err, "recovery must return to shared counting")
	require.NotContains(t, client.ZRange(ctx, key, 0, -1).Val(), stale,
		"recovery must flush the buffered release — the stale member is gone (D-13)")
	require.EqualValues(t, 1, client.ZCard(ctx, key).Val(),
		"after flush, ZCARD must equal the actual in-flight grants — no stale lease (no secondary 429)")

	// Taxonomy regression: only a genuine over-limit may refuse after recovery.
	_, err = lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 1}})
	require.ErrorIs(t, err, ErrConcurrencyExceeded,
		"only a genuine over-limit may refuse once the backend is back")

	rel()
	require.EqualValues(t, 0, client.ZCard(ctx, key).Val(),
		"full release must drain the bucket after the flush")
	require.Empty(t, client.SMembers(ctx, "conc:registry").Val(),
		"the registry cleanup chain must drain after recovery (D-18 re-check)")

	// WARN semantics: the release failure trips, the successful probe recovers.
	require.Contains(t, logs.String(), breakerTripWarn,
		"a release failure must open the breaker (trip WARN)")
	require.Contains(t, logs.String(), breakerRecoverWarn,
		"the successful probe must emit the recovery WARN")
}

// TestRedisLimiterShutdownBulkClearsHeldAndPending pins D-19: Close() — the
// hook main.go calls between srv.Shutdown and process exit — bulk-cleans the
// remaining held-lease registry AND flushes the pending buffer through the
// same lease_flush.lua facility, so the bucket and the registry drain to zero
// (direction-safe: a failed cleanup means fewer counts, TTL backstops the
// rest). Against Task 01's Close (ticker stop only) the members linger —
// behavior red.
func TestRedisLimiterShutdownBulkClearsHeldAndPending(t *testing.T) {
	lim, client, _ := newBreakerFixture(t)
	ctx := context.Background()
	scope := ScopeKey("key:98:shutdown")
	key := "conc:key:98:shutdown"

	// Two grants: one released normally before shutdown, one whose release
	// hits an outage and lands in the pending buffer.
	keep, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err)
	drop, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err)
	require.EqualValues(t, 2, client.ZCard(ctx, key).Val())

	originalRelease := releaseScript
	releaseScript = redis.NewScript(`return error('release outage')`)
	t.Cleanup(func() { releaseScript = originalRelease })
	drop()
	releaseScript = originalRelease

	// D-19: shutdown bulk cleanup drains held + pending in one pass.
	lim.Close()
	require.EqualValues(t, 0, client.ZCard(ctx, key).Val(),
		"shutdown bulk cleanup must ZREM every locally-known member (D-19)")
	require.Empty(t, client.SMembers(ctx, "conc:registry").Val(),
		"shutdown bulk cleanup must run the registry cleanup chain (D-18 re-check)")
	require.Empty(t, client.Keys(ctx, "conc:*").Val(),
		"no conc:* key may survive an orderly shutdown (cleanup chain + auto-delete)")

	// Close stays idempotent (the fixture cleanup calls it again).
	lim.Close()
	keep() // releasing after cleanup is an idempotent no-op (Pitfall 11)
}

// TestRedisLimiterFlushScriptBatchZREMIsIdempotent pins the lease_flush.lua
// script contract (D-13/D-19) at the script level, mirroring the lease_renew
// script test: one call batch-ZREMs every (key, member) pair, runs the
// ZCARD=0 → registry SREM cleanup chain for the touched keys, and a repeated
// flush of the same members is a zero-change no-op (ZREM ignores absent
// members — research accepted #4 — so flush retries are safe by construction).
// References flushScript, which does not exist before Task 02's implementation
// — compile red, recorded separately from the behavior reds above.
func TestRedisLimiterFlushScriptBatchZREMIsIdempotent(t *testing.T) {
	_, _, client := newRedisLimiterFixture(t)
	ctx := context.Background()
	key := "conc:key:99:flushscript"

	require.NoError(t, client.SAdd(ctx, "conc:registry", key).Err())
	require.NoError(t, client.ZAdd(ctx, key,
		&redis.Z{Score: 1000, Member: "node:a"},
		&redis.Z{Score: 2000, Member: "node:b"}).Err())

	// KEYS[1]=registry, KEYS[2]=bucket; ARGV pairs are (absolute KEYS index,
	// member) — the same packing shape as lease_renew.lua.
	keys := []string{"conc:registry", key}
	removed, err := flushScript.Run(ctx, client, keys, 2, "node:a", 2, "node:b").Int64()
	require.NoError(t, err)
	require.EqualValues(t, 2, removed, "batch flush must ZREM both members in one call")
	require.EqualValues(t, 0, client.ZCard(ctx, key).Val(),
		"the emptied bucket must drain to ZCARD=0")
	require.NotContains(t, client.SMembers(ctx, "conc:registry").Val(), key,
		"flush must run the ZCARD=0 → SREM registry cleanup chain (D-17/D-18, same as lease_release)")
	require.EqualValues(t, 0, client.Exists(ctx, key).Val(),
		"the emptied ZSET key must auto-delete")

	// Idempotent: a repeated flush of the same members changes nothing.
	removed, err = flushScript.Run(ctx, client, keys, 2, "node:a", 2, "node:b").Int64()
	require.NoError(t, err)
	require.Zero(t, removed,
		"ZREM of absent members must be ignored — repeated flush is a safe no-op (research accepted #4)")
	require.Empty(t, client.SMembers(ctx, "conc:registry").Val(),
		"a repeated flush must leave the registry empty")
}

// ---------------------------------------------------------------------------
// 02-10 Task 03 acceptance layer: D-23 crash reclaim on the ① miniredis
// fixture, plus the ② docker-gated real-engine family (D-21/D-22: real
// redis.call('TIME') renewal and reclaim). The ③ sentinel failover soak stays
// manual — its operator runbook is
// .planning/phases/02-token/02-10-soak-checklist.md.
// ---------------------------------------------------------------------------

// deadNodeMember mints the leftover lease a crashed gateway node abandons:
// the same {nodeID}:{random} shape leaseMember() writes, under a dead node's
// ID, so the reclaim path meets a realistic occupant (D-23 / T-02-20).
func deadNodeMember() string {
	var suffix [12]byte
	_, _ = rand.Read(suffix[:]) // crypto/rand.Read never fails; it aborts instead
	return "dead-node-crash:" + hex.EncodeToString(suffix[:])
}

// newDockerLimiter returns a real-Redis client plus its RedisLimiter for the ②
// layer. The ping is a hard require, never a skip: TEST_REDIS_ADDR set but
// unreachable must fail the run (D-22 — silent skipping would hide the real
// TIME/lease path). The limiter's renewal ticker is joined on cleanup (D-01).
func newDockerLimiter(t *testing.T, addr string) (*RedisLimiter, *redis.Client) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: addr})
	require.NoError(t, client.Ping(context.Background()).Err(),
		"TEST_REDIS_ADDR is set but not reachable — docker ② layer must run, not skip (D-22)")
	t.Cleanup(func() { _ = client.Close() })
	limiter := NewRedisLimiter(client, "conc:")
	t.Cleanup(limiter.Close)
	return limiter, client
}

// TestRedisLimiter_ReclaimsDeadNodeLeaseWithinTTL pins D-23 (RED-02/SC-2) at
// the ① layer: a dead node's leftover member seals a limit=1 bucket, and the
// lease TTL is the safety net that frees it — both realistic score shapes
// (already expired, and expiring exactly one TTL out) are covered with a
// bounded poll instead of a fixed sleep (deterministic in CI).
//
// Reclaim is lazy by design: lease_acquire.lua runs ZREMRANGEBYSCORE before
// judging a bucket, so the assertion is "the next acquire finds the slot free",
// which is the capacity-recovery contract a gateway actually depends on. The
// real redis.call('TIME') variant of the same contract runs in the ② docker
// family below; true process-kill evidence comes from the ③ soak runbook.
func TestRedisLimiter_ReclaimsDeadNodeLeaseWithinTTL(t *testing.T) {
	t.Setenv(leaseTTLEnv, "3") // D-23: short TTL injected → reclaim inside 3s (D-03/D-04)
	_, lim, client := newRedisLimiterFixture(t)
	ctx := context.Background()
	scope := ScopeKey("key:950:deadnode")
	key := "conc:key:950:deadnode"
	dead := deadNodeMember()
	ttlSeconds := common.GetEnvOrDefault(leaseTTLEnv, defaultLeaseTTLs)
	reclaimDeadline := time.Duration(ttlSeconds)*time.Second + 7*time.Second

	tests := []struct {
		name string
		// score is evaluated inside the subtest so the near-expiry shape is
		// always written a full TTL ahead of the moment it lands.
		score func() float64
		// sealed: the leftover must hold the limit=1 slot until the TTL passes.
		sealed bool
	}{
		{
			name:   "already_expired_leftover",
			score:  func() float64 { return float64(time.Now().UnixMilli() - 1000) },
			sealed: false,
		},
		{
			name:   "near_expiry_leftover",
			score:  func() float64 { return float64(time.Now().UnixMilli()) + float64(ttlSeconds)*1000 },
			sealed: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// A crashed node leaves exactly this behind: registry membership
			// plus a ZSET member carrying its expiry instant as the score.
			require.NoError(t, client.SAdd(ctx, "conc:registry", key).Err())
			require.NoError(t, client.ZAdd(ctx, key, &redis.Z{Score: tc.score(), Member: dead}).Err())
			require.EqualValues(t, 1, client.ZCard(ctx, key).Val(),
				"the dead node's leftover must occupy the slot before reclaim")

			if tc.sealed {
				_, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 1}})
				require.ErrorIs(t, err, ErrConcurrencyExceeded,
					"sanity: an unexpired leftover must still seal limit=1 (no premature reclaim)")
			}

			// Capacity recovery: bounded polling up to TTL+margin — never a
			// fixed sleep, and never a blind wait past the deadline.
			var reclaimed func()
			require.Eventually(t, func() bool {
				rel, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 1}})
				if err != nil {
					return false // still sealed (unexpired leftover)
				}
				reclaimed = rel
				return true
			}, reclaimDeadline, 200*time.Millisecond,
				"the dead node's slot must be reclaimed within the lease TTL (D-23)")

			members := client.ZRange(ctx, key, 0, -1).Val()
			require.Len(t, members, 1, "only the new grant may remain after reclaim")
			require.NotEqual(t, dead, members[0], "the dead node's member must be gone from the ZSET")
			require.EqualValues(t, 1, client.ZCard(ctx, key).Val(),
				"ZCARD must reflect exactly the single live grant")

			reclaimed()
			require.EqualValues(t, 0, client.ZCard(ctx, key).Val(), "release must drain the reclaimed slot")
			require.Empty(t, client.SMembers(ctx, "conc:registry").Val(),
				"the cleanup chain must drain the registry the dead node left behind (D-17/D-18)")
			require.EqualValues(t, 0, client.Exists(ctx, key).Val(), "the emptied ZSET key must auto-delete")
		})
	}
}

// TestRedisLimiterDocker_ReclaimsDeadNodeLeaseWithinTTL is the ② layer of
// D-23: the same contract against real redis:7-alpine, with the leftover's
// score derived from the SERVER's clock (redis TIME) — the exact source
// lease_acquire.lua compares against — so the reclaim is proven on the real
// engine's time path instead of the in-process miniredis clock.
func TestRedisLimiterDocker_ReclaimsDeadNodeLeaseWithinTTL(t *testing.T) {
	addr := dockerRedisAddr(t)
	t.Setenv(leaseTTLEnv, "3") // short TTL → reclaim inside 3s on the real engine
	ctx := context.Background()
	lim, client := newDockerLimiter(t, addr)
	scope := ScopeKey("key:docker-reclaim:*")
	key := "conc:key:docker-reclaim:*"
	baseline := resetDockerConcurrencyState(t, client, key)

	serverTime, err := client.Time(ctx).Result()
	require.NoError(t, err, "real Redis must answer TIME (the ② layer exists for this path)")
	dead := deadNodeMember()
	require.NoError(t, client.SAdd(ctx, "conc:registry", key).Err())
	require.NoError(t, client.ZAdd(ctx, key, &redis.Z{
		Score:  float64(serverTime.UnixMilli()) + 3000, // expires exactly one TTL out
		Member: dead,
	}).Err())
	require.EqualValues(t, 1, client.ZCard(ctx, key).Val(),
		"the dead node's leftover must occupy the slot before reclaim")

	_, err = lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 1}})
	require.ErrorIs(t, err, ErrConcurrencyExceeded,
		"sanity: an unexpired leftover must still seal limit=1 on the real engine")

	var reclaimed func()
	require.Eventually(t, func() bool {
		rel, acquireErr := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 1}})
		if acquireErr != nil {
			return false
		}
		reclaimed = rel
		return true
	}, 10*time.Second, 250*time.Millisecond,
		"the dead node's slot must be reclaimed within the lease TTL on real Redis (D-23 ②)")

	members := client.ZRange(ctx, key, 0, -1).Val()
	require.Len(t, members, 1, "only the new grant may remain after reclaim")
	require.NotEqual(t, dead, members[0], "the dead node's member must be gone from the ZSET")

	reclaimed()
	require.EqualValues(t, 0, client.ZCard(ctx, key).Val())
	require.Empty(t, client.SMembers(ctx, "conc:registry").Val(),
		"the cleanup chain must drain the registry on the real engine (D-17/D-18)")
	require.EqualValues(t, baseline, client.DBSize(ctx).Val(),
		"DBSIZE must return to baseline — no key accumulation")
}

// TestRedisLimiterDocker_RenewalExtendsScoreMonotonically is the ② layer of
// D-01/D-03: with a short TTL injected (3s → 1s ticker), the node ticker must
// move the held lease's score strictly forward on the real redis.call('TIME')
// path — twice in a row (monotonic extrapolation) and staying in the future
// while the lease is held. miniredis shares this process's clock by
// construction; this test is the cross-process proof of the same time source.
func TestRedisLimiterDocker_RenewalExtendsScoreMonotonically(t *testing.T) {
	addr := dockerRedisAddr(t)
	t.Setenv(leaseTTLEnv, "3") // TTL 3s → renewal interval 1s (D-03/D-04)
	ctx := context.Background()
	lim, client := newDockerLimiter(t, addr)
	scope := ScopeKey("key:docker-renew-monotonic:*")
	key := "conc:key:docker-renew-monotonic:*"
	baseline := resetDockerConcurrencyState(t, client, key)

	release, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 5}})
	require.NoError(t, err)
	t.Cleanup(release)
	members := client.ZRange(ctx, key, 0, -1).Val()
	require.Len(t, members, 1, "the granted batch must hold exactly one member")
	member := members[0]
	score0 := client.ZScore(ctx, key, member).Val()
	require.Greater(t, score0, float64(0), "the grant must write an expiry score")

	require.Eventually(t, func() bool {
		return client.ZScore(ctx, key, member).Val() > score0
	}, 15*time.Second, 250*time.Millisecond,
		"the ticker must extend the held lease's score beyond the initial grant (real TIME path)")

	firstRenewed := client.ZScore(ctx, key, member).Val()
	require.Eventually(t, func() bool {
		return client.ZScore(ctx, key, member).Val() > firstRenewed
	}, 15*time.Second, 250*time.Millisecond,
		"renewal score must keep extrapolating monotonically on every tick")
	require.Greater(t, client.ZScore(ctx, key, member).Val(), float64(time.Now().UnixMilli()),
		"while held, the renewed score must stay in the future (lease not expired)")

	release()
	require.EqualValues(t, 0, client.ZCard(ctx, key).Val(), "release must drain the renewed slot")
	require.Empty(t, client.SMembers(ctx, "conc:registry").Val(),
		"the release cleanup chain must drain the registry (D-17/D-18)")
	require.EqualValues(t, baseline, client.DBSize(ctx).Val(),
		"DBSIZE must return to baseline — no key accumulation")
}

// TestRedisLimiterDocker_RenewalKeepsLongHoldAcrossTTL is the ② layer of the
// Pitfall-2 scenario: a holder kept alive past 2× the lease TTL must NOT be
// reclaimed, because the node ticker renews it every TTL/3 on the real engine.
// Crossing the TTL horizon is the behavior under test (the sleep is not a
// condition wait); the assertions afterwards are the state checks.
func TestRedisLimiterDocker_RenewalKeepsLongHoldAcrossTTL(t *testing.T) {
	const ttl = 3
	addr := dockerRedisAddr(t)
	t.Setenv(leaseTTLEnv, strconv.Itoa(ttl)) // TTL 3s → renewal interval 1s
	ctx := context.Background()
	lim, client := newDockerLimiter(t, addr)
	scope := ScopeKey("key:docker-longhold:*")
	key := "conc:key:docker-longhold:*"
	baseline := resetDockerConcurrencyState(t, client, key)

	hold, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 1}})
	require.NoError(t, err)
	t.Cleanup(hold)

	_, err = lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 1}})
	require.ErrorIs(t, err, ErrConcurrencyExceeded, "sanity: limit=1 must refuse while fresh")

	// Hold past 2× the initial TTL; renewal ticks keep the score ahead.
	time.Sleep(2*ttl*time.Second + time.Second)

	_, err = lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 1}})
	require.ErrorIs(t, err, ErrConcurrencyExceeded,
		"a long hold must survive beyond 2× TTL via renewal — reclamation means renewal is missing (Pitfall 2)")
	require.EqualValues(t, 1, client.ZCard(ctx, key).Val(),
		"the renewed lease must still occupy exactly one slot")

	hold()
	after, err := lim.AcquireBatch(ctx, []ScopeLimit{{Scope: scope, Limit: 1}})
	require.NoError(t, err, "release must still drain the renewed slot")
	after()
	require.EqualValues(t, 0, client.ZCard(ctx, key).Val())
	require.Empty(t, client.SMembers(ctx, "conc:registry").Val())
	require.EqualValues(t, baseline, client.DBSize(ctx).Val(),
		"DBSIZE must return to baseline — no key accumulation")
}
