package middleware

import (
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
)

// 并发准入的三条 429 拒绝路径此前一行日志都不写：运营既看不到"某个请求排了
// 10 秒最后被拒"，也分不清是排队没开、队列满了、等待超时，还是客户端自己
// 走了。这里为每次拒绝写一行含异常原因的系统日志，带上已等待时长、当时的
// 队列配置和被拒对象的标识。
//
// 边界（有意为之）：
//   - 不写消费日志，也不写 Log 表。被拒的请求没有产生任何计费语义，进 Log
//     表会把"消费记录"这一语义搅浑（产品裁定：只记异常原因）。
//   - 不受 ERROR_LOG_ENABLED 控制。那个开关只门控"失败请求写 Log 表错误行"
//     （service/relay_error.go 的 ProcessChannelError），与本处的系统日志是
//     两套互不相干的通道；异常路径不该因为一个落库开关而重新变成黑洞。
//   - 不记录任何凭据：只有 user id / token id（数据库主键）/ 模型名，没有
//     token key、渠道密钥、请求体或响应体。
const (
	// 排队等效关闭即拒：排队未开启、深度系数 ≤ 0 或等待窗口 ≤ 0。
	concurrencyRejectReasonQueueDisabled = "queue_disabled"
	// 队列已满：深度 min(ratio×limit, cap) 已被占满。
	concurrencyRejectReasonQueueFull = "queue_full"
	// 排队等待超时：等满 max_wait 仍未拿到槽位。
	concurrencyRejectReasonQueueTimeout = "queue_timeout"
	// 客户端断开：等待期间客户端自己走了（此前与等待超时合并为同一个结局）。
	concurrencyRejectReasonClientGone = "client_disconnected"
)

// 并发拒绝日志的节流：同一个原因在同一个时间窗内最多写
// concurrencyRejectionLogMaxPerWindow 行，超出的只计数、不写行；下一行带上
// suppressed=N。被抑制的拒绝事件不会消失——既有计数器 concurrency.AddRejection
// 仍然逐次累加，聚合视图（并发监控页 + 10s 采样历史）不受影响，日志量则有了
// 明确上界，不会被拒绝风暴打爆。
const (
	concurrencyRejectionLogWindow       = time.Second
	concurrencyRejectionLogMaxPerWindow = 20
)

// concurrencyRejectionLogWindow 记录某个原因在当前时间窗内的写入情况。
type concurrencyRejectionLogWindowState struct {
	start      time.Time
	emitted    int
	suppressed int
}

// concurrencyRejectionLogLimiter 按原因分别做窗口节流。
type concurrencyRejectionLogLimiter struct {
	mu      sync.Mutex
	windows map[string]*concurrencyRejectionLogWindowState
}

var concurrencyRejectionLogs = &concurrencyRejectionLogLimiter{
	windows: map[string]*concurrencyRejectionLogWindowState{},
}

// admit 报告本次拒绝是否要写日志行；若上一个窗口有被抑制的行，返回它们的
// 数量以便并入下一行（计数不丢）。
func (l *concurrencyRejectionLogLimiter) admit(reason string, now time.Time) (emit bool, suppressed int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	window, ok := l.windows[reason]
	if !ok {
		window = &concurrencyRejectionLogWindowState{}
		l.windows[reason] = window
	}
	if window.start.IsZero() || now.Sub(window.start) >= concurrencyRejectionLogWindow {
		window.start = now
		window.emitted = 0
		suppressed = window.suppressed
		window.suppressed = 0
	}
	if window.emitted >= concurrencyRejectionLogMaxPerWindow {
		window.suppressed++
		return false, 0
	}
	window.emitted++
	return true, suppressed
}

// reset 清空节流状态（测试夹具用：让每个用例的日志行断言不受同包其它用例
// 的写入量影响）。
func (l *concurrencyRejectionLogLimiter) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.windows = map[string]*concurrencyRejectionLogWindowState{}
}

// logConcurrencyRejection 写一行"并发拒绝"的异常原因日志。复用仓库既有的
// 系统日志通道（logger.LogError → 一体化日志文件 + stderr），因此这条记录
// 会带上请求 ID，便于与客户端拿到的 X-Oneapi-Request-Id 对齐。
func logConcurrencyRejection(c *gin.Context, reason string, waited time.Duration, subject concurrencySubject, ratio, depthCap int, maxWait time.Duration) {
	emit, suppressed := concurrencyRejectionLogs.admit(reason, time.Now())
	if !emit {
		return
	}
	msg := fmt.Sprintf(
		"concurrency: request rejected reason=%s waited_ms=%d queue_enabled=%t queue_ratio=%d queue_cap=%d queue_max_wait_ms=%d user_id=%d token_id=%d model=%s",
		reason,
		waited.Milliseconds(),
		setting.GetConcurrencyQueueEnabled(),
		ratio,
		depthCap,
		maxWait.Milliseconds(),
		subject.userID,
		subject.tokenID,
		common.LocalLogPreview(subject.model),
	)
	if suppressed > 0 {
		msg += fmt.Sprintf(" suppressed=%d", suppressed)
	}
	logger.LogError(c, msg)
}
