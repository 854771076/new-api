package common

import (
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
)

// 分段耗时快照的固定键集合。键名是后端与前端之间的契约
// (web/src/features/usage-logs/types.ts 的 RequestTimingInfo)，新增键必须
// 同步前端类型；这十一个键全部是数值，不含上游 URL、请求体、响应体、渠道
// 密钥或任何用户内容。
const (
	TimingKeyQueueWaitMs            = "queue_wait_ms"
	TimingKeyChannelSelectMs        = "channel_select_ms"
	TimingKeyUpstreamHeaderMs       = "upstream_header_ms"
	TimingKeyUpstreamBodyMs         = "upstream_body_ms"
	TimingKeyUpstreamFirstByteMs    = "upstream_first_byte_ms"
	TimingKeyUpstreamStreamMs       = "upstream_stream_ms"
	TimingKeySettleMs               = "settle_ms"
	TimingKeyAttempts               = "attempts"
	TimingKeyUpstreamHeaderTimeoutS = "upstream_header_timeout_s"
	TimingKeyStreamingTimeoutS      = "streaming_timeout_s"
	TimingKeyRelayTimeoutS          = "relay_timeout_s"
)

// requestTimingCreateMu 只保护采集载体的首次创建。载体的其余字段都是原子
// 变量，创建完成后各埋点之间不再需要互斥。
var requestTimingCreateMu sync.Mutex

// RequestTiming 采集一个请求被拆开的各段耗时。载体本身放在 gin 上下文里，
// 因为并发准入(最早埋点)与流式扫描(最晚埋点)在不同 goroutine 上，context 是
// 唯一的公共通道。
//
// 所有字段都是原子变量：上游响应头到达时刻由发请求的 goroutine 写，首个上游
// 字节时刻由流式扫描 goroutine 写，快照由落盘路径读。首包耗时在快照时由两个
// 纳秒时间戳相减得出，因此跨 goroutine 读取不需要额外加锁。
type RequestTiming struct {
	// QueueWaitMs 是并发准入的排队等待时长（直放为 0）。
	QueueWaitMs atomic.Int64
	// ChannelSelectMs 是选渠道耗时，多 attempt 时累加。
	ChannelSelectMs atomic.Int64
	// UpstreamHeaderMs 是等待上游响应头的时长，最后一次 attempt 为准。
	UpstreamHeaderMs atomic.Int64
	// UpstreamBodyMs 是非流式路径读取上游响应体的时长。
	UpstreamBodyMs atomic.Int64
	// UpstreamStreamMs 是上游流的持续时长（首包到流结束）。
	UpstreamStreamMs atomic.Int64
	// SettleMs 是结算耗时（进入结算到写日志之前）。
	SettleMs atomic.Int64
	// Attempts 是选到渠道的尝试次数。
	Attempts atomic.Int64

	upstreamHeaderAtNs atomic.Int64
	firstByteAtNs      atomic.Int64
}

// SetQueueWait 记录并发准入的排队等待时长。直放路径不调用它，值保持 0。
func (t *RequestTiming) SetQueueWait(d time.Duration) {
	if t == nil {
		return
	}
	t.QueueWaitMs.Store(d.Milliseconds())
}

// AddChannelSelect 累加一次选渠道耗时。重试会重新选渠道，因此是累加而非覆盖。
func (t *RequestTiming) AddChannelSelect(d time.Duration) {
	if t == nil {
		return
	}
	t.ChannelSelectMs.Add(d.Milliseconds())
}

// AddAttempt 记一次成功选到渠道的尝试。
func (t *RequestTiming) AddAttempt() {
	if t == nil {
		return
	}
	t.Attempts.Add(1)
}

// MarkUpstreamHeaderReceived 记录上游响应头到达的时刻。首包耗时以它为起点，
// 这样"上游响应头等待"与"上游首包等待"两段互不重叠，同一段时间不会被计两次。
// 拿不到这个时刻的路径（不走 doRequest 的上游通道）就不写首包段，绝不编造。
func (t *RequestTiming) MarkUpstreamHeaderReceived(at time.Time) {
	if t == nil {
		return
	}
	t.upstreamHeaderAtNs.Store(at.UnixNano())
}

// UpstreamHeaderAt 返回上游响应头到达的时刻；没有记录时返回零值。
func (t *RequestTiming) UpstreamHeaderAt() time.Time {
	if t == nil {
		return time.Time{}
	}
	ns := t.upstreamHeaderAtNs.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// SetUpstreamHeaderWait 记录等待上游响应头的时长（覆盖写，最后一次 attempt
// 为准）。连接失败与响应头超时同样写入，否则"卡在上游响应头直到超时"这种
// 最典型的慢请求反而没有数据。
func (t *RequestTiming) SetUpstreamHeaderWait(d time.Duration) {
	if t == nil {
		return
	}
	t.UpstreamHeaderMs.Store(d.Milliseconds())
}

// MarkFirstByte 记录首个上游字节（首个 data 行）到达的时刻。只保留第一次，
// 后续 data 行不影响首包耗时。
func (t *RequestTiming) MarkFirstByte(at time.Time) {
	if t == nil {
		return
	}
	t.firstByteAtNs.CompareAndSwap(0, at.UnixNano())
}

// FirstByteAt 返回首个上游字节到达的时刻；没有记录时返回零值。
func (t *RequestTiming) FirstByteAt() time.Time {
	if t == nil {
		return time.Time{}
	}
	ns := t.firstByteAtNs.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// SetUpstreamBodyDuration 记录上游响应体读取的时长。非流式路径的整段生成
// 时间通常发生在响应体读取阶段，没有这一段就看不出"响应头已到、内容还没来"
// 的等待。读失败同样写入，因为读失败本身就说明卡在响应体阶段。
func (t *RequestTiming) SetUpstreamBodyDuration(d time.Duration) {
	if t == nil {
		return
	}
	t.UpstreamBodyMs.Store(d.Milliseconds())
}

// SetUpstreamStreamDuration 记录上游流的持续时长。
func (t *RequestTiming) SetUpstreamStreamDuration(d time.Duration) {
	if t == nil {
		return
	}
	t.UpstreamStreamMs.Store(d.Milliseconds())
}

// SetSettle 记录结算耗时。
func (t *RequestTiming) SetSettle(d time.Duration) {
	if t == nil {
		return
	}
	t.SettleMs.Store(d.Milliseconds())
}

// Snapshot 返回采集结果的固定键集合快照，供落盘到日志的 admin_info.timing。
// 首包耗时在这里由两个原子纳秒值相减得出，跨 goroutine 读取是安全的。
func (t *RequestTiming) Snapshot() map[string]any {
	if t == nil {
		return nil
	}
	firstByteMs := int64(0)
	headerAt, firstByte := t.upstreamHeaderAtNs.Load(), t.firstByteAtNs.Load()
	if headerAt > 0 && firstByte >= headerAt {
		firstByteMs = (firstByte - headerAt) / int64(time.Millisecond)
	}
	return map[string]any{
		TimingKeyQueueWaitMs:            t.QueueWaitMs.Load(),
		TimingKeyChannelSelectMs:        t.ChannelSelectMs.Load(),
		TimingKeyUpstreamHeaderMs:       t.UpstreamHeaderMs.Load(),
		TimingKeyUpstreamBodyMs:         t.UpstreamBodyMs.Load(),
		TimingKeyUpstreamFirstByteMs:    firstByteMs,
		TimingKeyUpstreamStreamMs:       t.UpstreamStreamMs.Load(),
		TimingKeySettleMs:               t.SettleMs.Load(),
		TimingKeyAttempts:               t.Attempts.Load(),
		TimingKeyUpstreamHeaderTimeoutS: int64(common.RelayResponseHeaderTimeout),
		TimingKeyStreamingTimeoutS:      int64(constant.StreamingTimeout),
		TimingKeyRelayTimeoutS:          int64(common.RelayTimeout),
	}
}

// ReadUpstreamBody 读取上游响应体，并把这段读取耗时写进采集载体的响应体分段。
// 只用于非流式路径：流式路径的读取时长已经由"上游首包 + 上游流持续"覆盖，
// 再写一次就是重复计入。
func ReadUpstreamBody(c *gin.Context, body io.Reader) ([]byte, error) {
	startedAt := time.Now()
	data, err := io.ReadAll(body)
	RequestTimingOf(c).SetUpstreamBodyDuration(time.Since(startedAt))
	return data, err
}

// RequestTimingFrom 返回上下文里已有的采集载体；没有时返回 nil（非 relay
// 路径不写 timing 键，也不该凭空造一个空载体）。
func RequestTimingFrom(c *gin.Context) *RequestTiming {
	if c == nil {
		return nil
	}
	value, ok := c.Get(string(constant.ContextKeyRequestTiming))
	if !ok {
		return nil
	}
	timing, _ := value.(*RequestTiming)
	return timing
}

// RequestTimingOf 返回上下文里的采集载体，没有就创建一个并回写上下文。
// gin 的 Context.Set 自带锁，创建路径额外用一把互斥锁保证并发调用时不会
// 造出两个实例。
func RequestTimingOf(c *gin.Context) *RequestTiming {
	if c == nil {
		return nil
	}
	if timing := RequestTimingFrom(c); timing != nil {
		return timing
	}
	requestTimingCreateMu.Lock()
	defer requestTimingCreateMu.Unlock()
	if timing := RequestTimingFrom(c); timing != nil {
		return timing
	}
	timing := &RequestTiming{}
	c.Set(string(constant.ContextKeyRequestTiming), timing)
	return timing
}
