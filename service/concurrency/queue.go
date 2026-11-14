package concurrency

import "sync"

// WaitQueue 是网关内的有界等待队列占位表：并发准入（AcquireBatch）失败后，
// 请求先在这里登记一个"排队位"，再轮询重试真正的限流获取。
//
// 关键不变量：
//   - 队列只记录"在等的请求数"，绝不记录活跃请求数——活跃数仍由 Limiter 按
//     各 scope 的并发上限 C 约束，排队位不占用 C。
//   - 每个 scope 的排队深度 Q = min(ratio × C, cap)，按 scope 自身的 limit
//     计算；limit <= 0（unlimited）的 scope 永远不阻塞，不参与深度判断。
//   - 入队是 all-or-nothing：任一 scope 超深度则整批拒绝（与 D-05 批量语义
//     一致），不留下部分占位。
//   - 队列状态是进程内的（per-node）：多节点部署时各节点各自排队、各自计数，
//     没有跨节点仲裁；跨节点的活跃上限仍由 Limiter（Redis ZSET）保证。
type WaitQueue struct {
	mu sync.Mutex
	// queued 按 scope 记录排队深度，用于 min(ratio*limit, maxDepth) 的准入判断。
	queued map[ScopeKey]int
	// waiting 是当前排队中的请求数：一次成功的 TryEnter = 一个请求 = +1，
	// 与该请求占了几个 scope 无关。快照接口的 queued 字段读的就是它。
	waiting int
}

// NewWaitQueue 返回一个空的等待队列。
func NewWaitQueue() *WaitQueue {
	return &WaitQueue{queued: map[ScopeKey]int{}}
}

// defaultWaitQueue 是 middleware 与 controller 共享的包级单例。
var defaultWaitQueue = NewWaitQueue()

// DefaultWaitQueue 返回包级单例队列。排队状态是进程内的（per-node），
// 所以请求入口和快照出口必须读同一个实例。
func DefaultWaitQueue() *WaitQueue {
	return defaultWaitQueue
}

// TryEnter 以 all-or-nothing 语义为一批 scope 争取排队位。
//
// 每个 limit > 0 的 scope 的深度为 min(ratio*limit, maxDepth)：全部未超才整体
// +1 并返回幂等的 leave；任一超深则整体不入队（ok=false，leave=nil）。
// limit <= 0 的 scope 不参与深度判断，也不被计数。
func (q *WaitQueue) TryEnter(scopes []ScopeLimit, ratio, maxDepth int) (leave func(), ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	// 第一遍：整批判断（all-or-nothing），任何写入之前先判完。
	tracked := make([]ScopeKey, 0, len(scopes))
	for _, sl := range scopes {
		if sl.Limit <= 0 {
			continue // unlimited scope 永不排队
		}
		depth := min(ratio*sl.Limit, maxDepth)
		if q.queued[sl.Scope] >= depth {
			// 某个 scope 深度已满：整批拒绝，未写入任何占位。
			return nil, false
		}
		tracked = append(tracked, sl.Scope)
	}

	// 第二遍：整批占位。
	keys := tracked
	for _, key := range keys {
		q.queued[key]++
	}
	q.waiting++

	var once sync.Once
	return func() {
		once.Do(func() {
			q.mu.Lock()
			defer q.mu.Unlock()
			q.waiting--
			for _, key := range keys {
				if q.queued[key] > 0 {
					q.queued[key]--
				}
				if q.queued[key] == 0 {
					delete(q.queued, key)
				}
			}
		})
	}, true
}

// Total 返回当前全网关排队中的请求数（快照接口的 queued 字段）。
func (q *WaitQueue) Total() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.waiting
}

// Reset 清空所有排队占位，回到初始状态。仅用于测试的 Setup/Cleanup，
// 生产路径不会调用。
func (q *WaitQueue) Reset() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.queued = map[ScopeKey]int{}
	q.waiting = 0
}
