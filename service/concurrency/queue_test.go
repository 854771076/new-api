package concurrency

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func scope(s string, limit int) ScopeLimit {
	return ScopeLimit{Scope: ScopeKey(s), Limit: limit}
}

// 深度公式 depth = min(ratio*limit, cap)：按 scope 自身 limit 放大。
func TestWaitQueue_DepthUsesRatioTimesLimit(t *testing.T) {
	q := NewWaitQueue()

	for i := 0; i < 8; i++ { // ratio=4, limit=2 -> depth 8
		leave, ok := q.TryEnter([]ScopeLimit{scope("key:1:gpt", 2)}, 4, 128)
		require.True(t, ok, "enter #%d must fit inside depth 8", i)
		require.NotNil(t, leave)
	}
	_, ok := q.TryEnter([]ScopeLimit{scope("key:1:gpt", 2)}, 4, 128)
	assert.False(t, ok, "the 9th waiter exceeds depth min(4*2,128)=8")
	assert.Equal(t, 8, q.Total())
}

// cap 截断：ratio*limit 超过 cap 时深度取 cap。
func TestWaitQueue_DepthTruncatedByCap(t *testing.T) {
	q := NewWaitQueue()

	for i := 0; i < 10; i++ { // ratio=4, limit=100 -> 400, cap=10 -> depth 10
		_, ok := q.TryEnter([]ScopeLimit{scope("key:2:*", 100)}, 4, 10)
		require.True(t, ok, "enter #%d must fit inside capped depth 10", i)
	}
	_, ok := q.TryEnter([]ScopeLimit{scope("key:2:*", 100)}, 4, 10)
	assert.False(t, ok, "depth must be min(4*100,10)=10, not 400")
	assert.Equal(t, 10, q.Total())
}

// ratio=0（等效关闭排队）→ 任何 scope 都进不去，且不留下任何占位。
func TestWaitQueue_ZeroRatioRefusesEntry(t *testing.T) {
	q := NewWaitQueue()

	leave, ok := q.TryEnter([]ScopeLimit{scope("user:3:*", 1)}, 0, 128)
	assert.False(t, ok, "ratio 0 means queue depth 0")
	assert.Nil(t, leave)
	assert.Equal(t, 0, q.Total())
}

// all-or-nothing：任一 scope 满了，整批都不入队，别的 scope 不留部分占位。
func TestWaitQueue_AllOrNothingAcrossScopes(t *testing.T) {
	q := NewWaitQueue()

	// Fill key:4:* to its depth (ratio=1, limit=1 -> depth 1).
	_, ok := q.TryEnter([]ScopeLimit{scope("key:4:*", 1)}, 1, 128)
	require.True(t, ok)
	require.Equal(t, 1, q.Total())

	// The batch also needs user:4:* (which has room) — must still refuse.
	leave, ok := q.TryEnter([]ScopeLimit{
		scope("user:4:*", 5),
		scope("key:4:*", 1),
	}, 1, 128)
	assert.False(t, ok, "a full scope must refuse the whole batch")
	assert.Nil(t, leave)
	assert.Equal(t, 1, q.Total(), "no partial reservation may be written on the roomy scope")

	// Proof there was no partial hold: the roomy scope alone still admits.
	leave2, ok2 := q.TryEnter([]ScopeLimit{scope("user:4:*", 5)}, 1, 128)
	require.True(t, ok2)
	require.NotNil(t, leave2)
	leave2()
	assert.Equal(t, 1, q.Total())
}

// limit<=0 的 unlimited scope 不参与队列深度判断（永不拒队）；但排队中的
// 请求本身仍然计入 Total —— Total 统计的是"排队请求数"，不是 scope 占位数。
func TestWaitQueue_UnlimitedScopeDoesNotConsumeQueueDepth(t *testing.T) {
	q := NewWaitQueue()

	leaves := make([]func(), 0, 5)
	for i := 0; i < 5; i++ {
		leave, ok := q.TryEnter([]ScopeLimit{scope("user:5:*", 0)}, 1, 128)
		require.True(t, ok, "an unlimited scope must never fill the queue")
		require.NotNil(t, leave)
		leaves = append(leaves, leave)
	}
	assert.Equal(t, 5, q.Total(), "each waiter is one queued request")
	for _, leave := range leaves {
		leave()
	}
	assert.Equal(t, 0, q.Total())
}

// leave 幂等：重复调用不把计数减到负数，且释放后可再次入队。
func TestWaitQueue_LeaveIsIdempotent(t *testing.T) {
	q := NewWaitQueue()

	leave, ok := q.TryEnter([]ScopeLimit{scope("key:6:*", 1), scope("user:6:*", 1)}, 1, 128)
	require.True(t, ok)
	assert.Equal(t, 1, q.Total(), "one request holding two scopes is one queued request")

	leave()
	assert.Equal(t, 0, q.Total())
	leave()
	leave()
	assert.Equal(t, 0, q.Total(), "double leave must not drive the counter negative")

	_, ok = q.TryEnter([]ScopeLimit{scope("key:6:*", 1)}, 1, 128)
	assert.True(t, ok, "a released scope must be immediately re-enterable")
}

// Total 汇总当前排队中的请求数。
func TestWaitQueue_TotalCountsEveryScope(t *testing.T) {
	q := NewWaitQueue()

	leaveA, ok := q.TryEnter([]ScopeLimit{scope("key:7:a", 10), scope("user:7:*", 10)}, 1, 128)
	require.True(t, ok)
	leaveB, ok := q.TryEnter([]ScopeLimit{scope("key:7:a", 10)}, 1, 128)
	require.True(t, ok)
	assert.Equal(t, 2, q.Total(), "two waiters, whatever scopes they hold")

	leaveB()
	leaveA()
	assert.Equal(t, 0, q.Total())
}

// 并发安全：go test -race 下多 goroutine 同时入队/出队，计数不得漂移。
func TestWaitQueue_ConcurrentEnterLeave(t *testing.T) {
	q := NewWaitQueue()

	const (
		goroutines = 32
		rounds     = 50
	)
	var wg sync.WaitGroup
	var admitted int64
	var mu sync.Mutex

	for range goroutines {
		wg.Go(func() {
			for range rounds {
				leave, ok := q.TryEnter([]ScopeLimit{scope("key:8:*", 1)}, 64, 1024)
				if !ok {
					continue
				}
				mu.Lock()
				admitted++
				mu.Unlock()
				leave()
			}
		})
	}
	wg.Wait()

	assert.Equal(t, int64(goroutines*rounds), admitted, "depth 1024 must admit every attempt")
	assert.Equal(t, 0, q.Total(), "every admitted waiter must have released")
}

// 包级单例：middleware 与 controller 必须拿到同一个实例。
func TestDefaultWaitQueueIsSharedSingleton(t *testing.T) {
	require.Same(t, DefaultWaitQueue(), DefaultWaitQueue())
}
