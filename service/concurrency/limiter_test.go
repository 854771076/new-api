package concurrency

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScopeKey verifies ScopeKey encodes (kind, id, model) in a stable order.
func TestScopeKey(t *testing.T) {
	a := ScopeKey("key:42:gpt-4o")
	b := ScopeKey("key:42:gpt-4o")
	require.Equal(t, a, b)
	require.Equal(t, ScopeKind(1), a.Kind())
	require.Equal(t, 42, a.SubjectID())
	require.Equal(t, "gpt-4o", a.Model())
	// global sentinel
	g := ScopeKey("user:7:*")
	require.Equal(t, "*", g.Model())
	require.False(t, g.IsModelScoped())
	require.True(t, ScopeKey("user:7:gpt-4o").IsModelScoped())
}

// TestLimiter_AllowsBelowLimit: limit=3, 5 serial acquires all succeed.
func TestLimiter_AllowsBelowLimit(t *testing.T) {
	l := NewMemoryLimiter()
	ctx := context.Background()
	scope := ScopeKey("key:1:gpt-4o")
	limit := 3

	for i := 0; i < 5; i++ {
		release, err := l.Acquire(ctx, scope, limit)
		require.NoError(t, err, "iteration %d should pass below limit", i)
		release()
	}
}

// TestLimiter_RejectsAboveLimit: limit=3, 4 concurrent — the 4th returns ErrConcurrencyExceeded.
func TestLimiter_RejectsAboveLimit(t *testing.T) {
	l := NewMemoryLimiter()
	ctx := context.Background()
	scope := ScopeKey("key:1:gpt-4o")
	limit := 3

	var wg sync.WaitGroup
	var inflight atomic.Int32
	var rejected atomic.Int32
	start := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			release, err := l.Acquire(ctx, scope, limit)
			if err != nil {
				require.ErrorIs(t, err, ErrConcurrencyExceeded)
				rejected.Add(1)
				return
			}
			inflight.Add(1)
			time.Sleep(150 * time.Millisecond)
			release()
		}()
	}
	close(start)
	wg.Wait()
	assert.Equal(t, int32(3), inflight.Load(), "exactly 3 should be in-flight")
	assert.Equal(t, int32(1), rejected.Load(), "exactly 1 should be rejected")
}

// TestLimiter_ReleaseFreesSlot: a release must immediately allow another acquire.
func TestLimiter_ReleaseFreesSlot(t *testing.T) {
	l := NewMemoryLimiter()
	ctx := context.Background()
	scope := ScopeKey("key:1:*")

	release, err := l.Acquire(ctx, scope, 1)
	require.NoError(t, err)
	_, err = l.Acquire(ctx, scope, 1)
	require.ErrorIs(t, err, ErrConcurrencyExceeded)
	release()
	release2, err := l.Acquire(ctx, scope, 1)
	require.NoError(t, err, "slot must free immediately after release")
	release2()
}

// TestLimiter_PerScopeIsolation: key:1 and key:2 do not interfere.
func TestLimiter_PerScopeIsolation(t *testing.T) {
	l := NewMemoryLimiter()
	ctx := context.Background()
	release1, err := l.Acquire(ctx, ScopeKey("key:1:*"), 1)
	require.NoError(t, err)
	defer release1()
	release2, err := l.Acquire(ctx, ScopeKey("key:2:*"), 1)
	require.NoError(t, err, "different scope must not be blocked by key:1")
	release2()
}

// TestLimiter_ZeroLimitMeansUnlimited: setting limit to 0 must never reject.
func TestLimiter_ZeroLimitMeansUnlimited(t *testing.T) {
	l := NewMemoryLimiter()
	ctx := context.Background()
	scope := ScopeKey("user:9:*")
	// limit 0 = unlimited: every acquire succeeds
	for i := 0; i < 50; i++ {
		release, err := l.Acquire(ctx, scope, 0)
		require.NoError(t, err, "iteration %d", i)
		release()
	}
}

// TestLimiter_SnapshotReturnsCurrentState: the snapshot carries in-flight
// rows only; Limit is always 0 because configured caps are back-filled by the
// controller layer from token/user configuration data (D-02), not stored in
// the limiter.
func TestLimiter_SnapshotReturnsCurrentState(t *testing.T) {
	l := NewMemoryLimiter()
	ctx := context.Background()
	scope := ScopeKey("key:5:gpt-4o")

	release, err := l.Acquire(ctx, scope, 2)
	require.NoError(t, err)
	defer release()

	snap, err := l.Snapshot(ctx)
	require.NoError(t, err)
	var found *ScopeSnapshot
	for i := range snap {
		if snap[i].Scope == scope {
			found = &snap[i]
		}
	}
	require.NotNil(t, found)
	assert.Equal(t, 1, found.Current)
	assert.Equal(t, 0, found.Limit)
	assert.Equal(t, 0.0, found.Utilization)
}

// TestLimiter_SnapshotStableOrder: same snapshot called twice must return scopes in same order.
func TestLimiter_SnapshotStableOrder(t *testing.T) {
	l := NewMemoryLimiter()
	ctx := context.Background()
	r1, _ := l.Acquire(ctx, ScopeKey("key:1:*"), 0)
	defer r1()
	r2, _ := l.Acquire(ctx, ScopeKey("key:2:*"), 0)
	defer r2()
	r3, _ := l.Acquire(ctx, ScopeKey("key:3:*"), 0)
	defer r3()

	snap1, _ := l.Snapshot(ctx)
	snap2, _ := l.Snapshot(ctx)
	require.Equal(t, len(snap1), len(snap2))
	for i := range snap1 {
		assert.Equal(t, snap1[i].Scope, snap2[i].Scope, "scope order at index %d", i)
	}
}

// TestLimiter_ZeroLimitCountsHolders: a limit<=0 acquire always succeeds but
// still counts the holder so the snapshot keeps observing the in-flight
// request (orchestrator discretion 2: deliberate deviation from the milestone
// ARCHITECTURE.md "skip zero-limit" wording, which belongs to RedisLimiter).
func TestLimiter_ZeroLimitCountsHolders(t *testing.T) {
	l := NewMemoryLimiter()
	ctx := context.Background()
	scope := ScopeKey("key:77:*")

	release, err := l.Acquire(ctx, scope, 0)
	require.NoError(t, err)

	snap, err := l.Snapshot(ctx)
	require.NoError(t, err)
	require.Len(t, snap, 1)
	assert.Equal(t, 1, snap[0].Current)
	assert.Equal(t, 0, snap[0].Limit)

	release()
	snap, err = l.Snapshot(ctx)
	require.NoError(t, err)
	assert.Empty(t, snap, "release must drop the holder row")
}

// TestLimiter_RejectsAtCap: serial acquires hit the cap exactly — the first
// `limit` succeed, the next returns ErrConcurrencyExceeded, and freeing a
// slot immediately allows the next caller.
func TestLimiter_RejectsAtCap(t *testing.T) {
	l := NewMemoryLimiter()
	ctx := context.Background()
	scope := ScopeKey("key:88:*")

	first, err := l.Acquire(ctx, scope, 2)
	require.NoError(t, err)
	second, err := l.Acquire(ctx, scope, 2)
	require.NoError(t, err)

	_, err = l.Acquire(ctx, scope, 2)
	require.ErrorIs(t, err, ErrConcurrencyExceeded, "third acquire at limit=2 must be rejected")

	first()
	third, err := l.Acquire(ctx, scope, 2)
	require.NoError(t, err, "a released slot must be acquirable immediately")
	second()
	third()
}

// TestLimiter_ReleaseIsIdempotent: a double release decrements the holder
// count exactly once, so the slot is not leaked and no underflow lock occurs.
func TestLimiter_ReleaseIsIdempotent(t *testing.T) {
	l := NewMemoryLimiter()
	ctx := context.Background()
	scope := ScopeKey("key:99:*")

	release, err := l.Acquire(ctx, scope, 1)
	require.NoError(t, err)
	release()
	release() // second call is a no-op

	snap, err := l.Snapshot(ctx)
	require.NoError(t, err)
	assert.Empty(t, snap, "double release must not keep a stale holder row")

	release2, err := l.Acquire(ctx, scope, 1)
	require.NoError(t, err, "slot must be fully available after double release")
	release2()
}

// errIs is the contract that ErrConcurrencyExceeded is a sentinel and usable with errors.Is.
func TestErrConcurrencyExceededIsSentinel(t *testing.T) {
	require.True(t, errors.Is(ErrConcurrencyExceeded, ErrConcurrencyExceeded))
}

// TestMemoryLimiter_AcquireBatchAllOrNothing pins the batch contract on the
// in-process backend: a batch whose later bucket is already at its cap is
// refused as one unit — ErrConcurrencyExceeded with zero holds granted (no
// partial acquire to roll back).
func TestMemoryLimiter_AcquireBatchAllOrNothing(t *testing.T) {
	l := NewMemoryLimiter()
	ctx := context.Background()

	hold, err := l.Acquire(ctx, ScopeKey("key:1:*"), 1)
	require.NoError(t, err)

	_, err = l.AcquireBatch(ctx, []ScopeLimit{
		{Scope: ScopeKey("key:3:*"), Limit: 5},
		{Scope: ScopeKey("key:1:*"), Limit: 1},
	})
	require.ErrorIs(t, err, ErrConcurrencyExceeded, "batch containing a full bucket must be refused")

	snap, err := l.Snapshot(ctx)
	require.NoError(t, err)
	require.Len(t, snap, 1, "a refused batch must leave no partial hold behind")
	assert.Equal(t, ScopeKey("key:1:*"), snap[0].Scope)
	assert.Equal(t, 1, snap[0].Current, "only the pre-existing holder remains")

	hold()
}

// TestMemoryLimiter_DegradedStatusAlwaysHealthy pins the RED-04 boundary on
// the observation accessor: without Redis there is no degradation to observe —
// a fresh, a holding, and a drained MemoryLimiter all report the zero-value
// healthy status (active=false, lastDegradedAt=0, consecutiveFailures=0), so
// the admin snapshot page never shows a degraded badge in single-node mode.
// References DegradedStatus / Limiter.DegradedStatus, which do not exist on
// the 02-08/02-10 skeleton — compile red, recorded separately from the
// behavior reds (02-07 convention).
func TestMemoryLimiter_DegradedStatusAlwaysHealthy(t *testing.T) {
	l := NewMemoryLimiter()
	ctx := context.Background()

	require.Equal(t, DegradedStatus{}, l.DegradedStatus(),
		"a fresh MemoryLimiter must report the zero-value healthy status")

	release, err := l.Acquire(ctx, ScopeKey("key:6:*"), 0)
	require.NoError(t, err)
	st := l.DegradedStatus()
	require.False(t, st.Active, "holding slots in single-node mode is not degradation (RED-04)")
	require.Zero(t, st.LastDegradedAt, "single-node mode must never stamp lastDegradedAt")
	require.Zero(t, st.ConsecutiveFailures, "single-node mode must never count failures")

	release()
	require.Equal(t, DegradedStatus{}, l.DegradedStatus(),
		"release must leave the healthy status untouched")
}

// TestMemoryLimiter_AcquireBatchGrantsAndReleasesTogether pins the granting
// side of the batch contract: all buckets gain a slot in one call (a limit<=0
// bucket counts without enforcing), the whole batch is refused once any bucket
// is full, and the release func frees every bucket of the batch at once and
// stays idempotent.
func TestMemoryLimiter_AcquireBatchGrantsAndReleasesTogether(t *testing.T) {
	l := NewMemoryLimiter()
	ctx := context.Background()
	batch := []ScopeLimit{
		{Scope: ScopeKey("key:5:gpt-4o"), Limit: 1},
		{Scope: ScopeKey("key:5:*"), Limit: 1},
		{Scope: ScopeKey("user:5:*"), Limit: 0},
	}

	release, err := l.AcquireBatch(ctx, batch)
	require.NoError(t, err)

	snap, err := l.Snapshot(ctx)
	require.NoError(t, err)
	require.Len(t, snap, 3, "every bucket of the batch must be counted")
	for i := range snap {
		assert.Equal(t, 1, snap[i].Current, "bucket %s counts one holder", snap[i].Scope)
		assert.Equal(t, 0, snap[i].Limit, "Limit column stays 0 (back-filled by the controller, D-02)")
	}

	_, err = l.AcquireBatch(ctx, batch)
	require.ErrorIs(t, err, ErrConcurrencyExceeded, "second batch at cap must be refused")

	release()
	snap, err = l.Snapshot(ctx)
	require.NoError(t, err)
	assert.Empty(t, snap, "the release must free every bucket of the batch")

	release() // idempotent second call
	snap, err = l.Snapshot(ctx)
	require.NoError(t, err)
	assert.Empty(t, snap, "a second release call must be a no-op")
}

// TestChannelScopeKindAndSnapshotRow pins the BCD D-2 channel axis: a
// two-segment "channel:<id>" scope must classify as its OWN kind instead of
// falling through to the key axis (the pre-02-14-01 defect), and the admin
// snapshot row must render kind="channel" so the observability surface can tell
// the axis apart. SubjectID/Model already read the two-segment shape correctly
// ("channel:42" -> 42 / "*"), which is exactly why the axis is two segments.
func TestChannelScopeKindAndSnapshotRow(t *testing.T) {
	require.Equal(t, ScopeKey("channel:42"), ChannelScope(42),
		"ChannelScope is the single constructor of the two-segment channel wire format")
	channelScope := ChannelScope(42)
	assert.NotEqual(t, ScopeKindKey, channelScope.Kind(),
		"channel:42 must not be classified as the key axis")
	assert.Equal(t, ScopeKindChannel, channelScope.Kind())
	assert.Equal(t, 42, channelScope.SubjectID())
	assert.Equal(t, "*", channelScope.Model(), "the channel axis has no model bucket")
	assert.False(t, channelScope.IsModelScoped())

	lim := NewMemoryLimiter()
	release, err := lim.Acquire(context.Background(), channelScope, 2)
	require.NoError(t, err)
	rows, err := lim.Snapshot(context.Background())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "channel", rows[0].Kind)
	assert.Equal(t, channelScope, rows[0].Scope)
	assert.Equal(t, 1, rows[0].Current)
	release()
}
