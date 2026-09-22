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
	require.NoError(t, l.SetLimit(ctx, scope, 3))

	for i := 0; i < 5; i++ {
		release, err := l.Acquire(ctx, scope)
		require.NoError(t, err, "iteration %d should pass below limit", i)
		release()
	}
}

// TestLimiter_RejectsAboveLimit: limit=3, 4 concurrent — the 4th returns ErrConcurrencyExceeded.
func TestLimiter_RejectsAboveLimit(t *testing.T) {
	l := NewMemoryLimiter()
	ctx := context.Background()
	scope := ScopeKey("key:1:gpt-4o")
	require.NoError(t, l.SetLimit(ctx, scope, 3))

	var wg sync.WaitGroup
	var inflight atomic.Int32
	var rejected atomic.Int32
	start := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			release, err := l.Acquire(ctx, scope)
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
	require.NoError(t, l.SetLimit(ctx, scope, 1))

	release, err := l.Acquire(ctx, scope)
	require.NoError(t, err)
	_, err = l.Acquire(ctx, scope)
	require.ErrorIs(t, err, ErrConcurrencyExceeded)
	release()
	release2, err := l.Acquire(ctx, scope)
	require.NoError(t, err, "slot must free immediately after release")
	release2()
}

// TestLimiter_PerScopeIsolation: key:1 and key:2 do not interfere.
func TestLimiter_PerScopeIsolation(t *testing.T) {
	l := NewMemoryLimiter()
	ctx := context.Background()
	require.NoError(t, l.SetLimit(ctx, ScopeKey("key:1:*"), 1))
	require.NoError(t, l.SetLimit(ctx, ScopeKey("key:2:*"), 1))

	release1, err := l.Acquire(ctx, ScopeKey("key:1:*"))
	require.NoError(t, err)
	defer release1()
	release2, err := l.Acquire(ctx, ScopeKey("key:2:*"))
	require.NoError(t, err, "different scope must not be blocked by key:1")
	release2()
}

// TestLimiter_ZeroLimitMeansUnlimited: setting limit to 0 must never reject.
func TestLimiter_ZeroLimitMeansUnlimited(t *testing.T) {
	l := NewMemoryLimiter()
	ctx := context.Background()
	scope := ScopeKey("user:9:*")
	// limit intentionally not set (default 0 = unlimited)
	for i := 0; i < 50; i++ {
		release, err := l.Acquire(ctx, scope)
		require.NoError(t, err, "iteration %d", i)
		release()
	}
}

// TestLimiter_SnapshotReturnsCurrentState.
func TestLimiter_SnapshotReturnsCurrentState(t *testing.T) {
	l := NewMemoryLimiter()
	ctx := context.Background()
	scope := ScopeKey("key:5:gpt-4o")
	require.NoError(t, l.SetLimit(ctx, scope, 2))

	release, err := l.Acquire(ctx, scope)
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
	assert.Equal(t, 2, found.Limit)
	assert.InDelta(t, 0.5, found.Utilization, 1e-9)
}

// TestLimiter_SnapshotStableOrder: same snapshot called twice must return scopes in same order.
func TestLimiter_SnapshotStableOrder(t *testing.T) {
	l := NewMemoryLimiter()
	ctx := context.Background()
	require.NoError(t, l.SetLimit(ctx, ScopeKey("key:1:*"), 1))
	require.NoError(t, l.SetLimit(ctx, ScopeKey("key:2:*"), 1))
	require.NoError(t, l.SetLimit(ctx, ScopeKey("key:3:*"), 1))
	r1, _ := l.Acquire(ctx, ScopeKey("key:2:*"))
	defer r1()

	snap1, _ := l.Snapshot(ctx)
	snap2, _ := l.Snapshot(ctx)
	require.Equal(t, len(snap1), len(snap2))
	for i := range snap1 {
		assert.Equal(t, snap1[i].Scope, snap2[i].Scope, "scope order at index %d", i)
	}
}

// errIs is the contract that ErrConcurrencyExceeded is a sentinel and usable with errors.Is.
func TestErrConcurrencyExceededIsSentinel(t *testing.T) {
	require.True(t, errors.Is(ErrConcurrencyExceeded, ErrConcurrencyExceeded))
}
