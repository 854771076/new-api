package middleware

import (
	"context"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/concurrency"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
)

// ConcurrencyRetryAfterSeconds is the fixed Retry-After hint sent with 429
// concurrency_exceeded responses — both this token/user gate and the relay
// all-channels-saturated terminal 429 (controller/relay.go) reference this one
// export; the seconds value must never be re-literalized at a call site. A
// fixed 1s constant is deliberate: under an in-flight model any more precise
// estimate would be a guess, and the named constant leaves room for later
// tuning.
const ConcurrencyRetryAfterSeconds = 1

// 三种 429 message(06:仅 message 可区分,状态码恒 429、type/code 恒
// concurrency_exceeded——产品裁定不新增错误码子类型)。文案与
// service/relay_error.go 的中性模板同风格,不含队列深度、配置值或作用域
// 桶名等内部细节;客户端按短语识别来源:"Concurrency limit exceeded for
// your account"(并发超限)/"Timed out waiting"(排队等待超时)/
// "wait queue is full"(队列已满)。渠道饱和的第四种形态由 relay 出口
// (controller/relay.go renderRelayTerminalError)渲染,不经此常量。
const (
	concurrencyExceededMessage     = "Concurrency limit exceeded for your account. Please retry after the current requests finish."
	concurrencyQueueTimeoutMessage = "Timed out waiting for an available concurrency slot. Please retry after the current requests finish."
	concurrencyQueueFullMessage    = "The concurrency wait queue is full. Please retry later."
)

// 等待队列的轮询节奏：50ms 起步，×1.5 指数退避，封顶 500ms；每次唤醒再加
// 0–25ms 抖动，避免一批排队请求同时醒来互相踩踏（惊群）。
const (
	queueRetryBaseInterval  = 50 * time.Millisecond
	queueRetryMaxInterval   = 500 * time.Millisecond
	queueRetryBackoffFactor = 1.5
	queueRetryMaxJitter     = 25 * time.Millisecond
)

// concurrencyLimiterAdapter bridges the package-private batch contract used
// in concurrency_test.go (a stub) with the exported Limiter interface used by
// production code. We keep the test stub free of the production Limiter
// dependency to avoid pulling Redis into the unit tests.
type concurrencyLimiterAdapter interface {
	AcquireBatch(scopes []concurrency.ScopeLimit) (func(), error)
}

// concurrencyMiddlewareState holds the chosen limiter. Set once at startup,
// read on every request — there is no hot swap in v1.
var (
	concurrencyLimiterOnce sync.Once
	concurrencyLimiter     concurrencyLimiterAdapter
)

// SetConcurrencyLimiter installs the limiter used by ConcurrencyMiddleware.
// Intended to be called from the boot path after the limiter has been built.
// Calling more than once is a programming error and panics on the second
// invocation so misconfiguration does not silently swap implementations.
func SetConcurrencyLimiter(l concurrencyLimiterAdapter) {
	concurrencyLimiterOnce.Do(func() { concurrencyLimiter = l })
	if concurrencyLimiter == nil {
		// Subsequent calls lost the race; the Once swallowed them. We must
		// not silently let a second limiter replace the first.
		panic("concurrency limiter already set")
	}
}

// ConcurrencyMiddleware enforces per-user and per-API-key in-flight caps
// before the request reaches the relay. It must be mounted after
// middleware.Distribute() so c.GetInt("id"), c.GetInt("token_id"), and the
// original model name are populated.
//
// When no limiter has been installed (e.g. during early bring-up tests) the
// middleware is a no-op, so existing test suites remain green until the
// limiter is wired in.
func ConcurrencyMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		l := concurrencyLimiter
		if l == nil {
			c.Next()
			return
		}

		subject := readConcurrencySubject(c)
		if subject.userID == 0 && subject.tokenID == 0 {
			// No identity — token auth has not populated the context. This is
			// either a public route (in which case this middleware should not
			// have been mounted) or an internal error. Either way, fail closed.
			c.JSON(http.StatusBadRequest, gin.H{
				"error": gin.H{
					"message": "Missing authentication context for concurrency gate.",
					"type":    "concurrency_exceeded",
					"code":    "concurrency_exceeded",
				},
			})
			c.Abort()
			return
		}

		// Acquire every axis bucket with its per-request cap in ONE batch
		// call (D-05 all-or-nothing, single Redis round-trip): the token
		// model cap (key:<id>:<model>), the token global cap (key:<id>:*) and
		// the user aggregate (user:<id>:*) are granted or refused as a unit,
		// so the stricter cap governs the request — min(model cap, global
		// cap) — the model cap can never bypass the global cap, and a
		// refusal leaves no partial hold behind. The user aggregate bucket
		// carries the configured user cap and aggregates every token of that
		// user.
		scopes := buildScopes(subject)
		// 采集载体是跨层共享的分段耗时载体（relay/common.RequestTiming）：
		// 排队等待是最早的一段埋点，必须在拿到槽位之前就写进去。
		timing := relaycommon.RequestTimingOf(c)
		release, err := l.AcquireBatch(scopes)
		if err != nil {
			// 活跃槽位已满：进入网关内的有界等待队列。排队关闭、排队深度系数
			// 为 0（等效关闭）、或等待窗口为 0 时保持既有行为 —— 立即 429。
			// queuedAt 是本次请求开始排队的时刻，被拒路径同样写入已等待时长，
			// 便于判断"排队等满 N 秒"被拒的请求。
			queuedAt := time.Now()
			ratio := setting.GetConcurrencyQueueDepthRatio()
			depthCap := setting.GetConcurrencyQueueDepthCap()
			maxWait := setting.GetConcurrencyQueueMaxWait()
			if !setting.GetConcurrencyQueueEnabled() || ratio <= 0 || maxWait <= 0 {
				// 排队关闭：即时拒绝，保持 CNT-01 逐字文案（06 的①来源）。
				timing.SetQueueWait(time.Since(queuedAt))
				logConcurrencyRejection(c, concurrencyRejectReasonQueueDisabled, 0, subject, ratio, depthCap, maxWait)
				rejectConcurrency(c, concurrencyExceededMessage)
				return
			}
			// 排队位是进程内（per-node）状态：多节点部署时每个节点各自排队、
			// 各自计数，没有跨节点仲裁；跨节点的活跃上限仍由 Limiter 保证。
			leave, ok := concurrency.DefaultWaitQueue().TryEnter(scopes, ratio, depthCap)
			if !ok {
				// 队列满：429 + Retry-After，message 单独可区分（06 的③来源）。
				timing.SetQueueWait(time.Since(queuedAt))
				logConcurrencyRejection(c, concurrencyRejectReasonQueueFull, 0, subject, ratio, depthCap, maxWait)
				rejectConcurrency(c, concurrencyQueueFullMessage)
				return
			}
			// defer 是 LIFO：leave 先注册 → 最后执行，所以请求结束时先释放
			// 活跃槽位（queuedRelease），再归还排队位，不存在"槽位已空、排队
			// 位还在" 的错位窗口。
			defer leave()

			queuedRelease, outcome := waitForConcurrencySlot(c.Request.Context(), l, scopes, time.Now().Add(maxWait))
			if outcome != concurrencyWaitAdmitted {
				// 超时或客户端断开：排队位由上面的 defer leave() 归还。两种退出
				// 共用等待超时 message（06 的②来源；断开时客户端已不在，无需再分），
				// 但日志里必须分得清——"排满 10 秒被拒"和"客户端自己走了"是两种
				// 完全不同的运营结论。
				waited := time.Since(queuedAt)
				timing.SetQueueWait(waited)
				reason := concurrencyRejectReasonQueueTimeout
				if outcome == concurrencyWaitClientGone {
					reason = concurrencyRejectReasonClientGone
				}
				logConcurrencyRejection(c, reason, waited, subject, ratio, depthCap, maxWait)
				rejectConcurrency(c, concurrencyQueueTimeoutMessage)
				return
			}
			timing.SetQueueWait(time.Since(queuedAt))
			defer queuedRelease()

			c.Next()
			return
		}
		defer release()

		c.Next()
	}
}

// concurrencyWaitOutcome 区分"拿到槽位 / 等待超时 / 客户端断开"三种结局。
// 超时与客户端断开此前合并为同一个 false 返回，运营分不出"排了 10 秒最后
// 被拒"与"客户端自己走了"；这里在内部把两者拆开，对外的 429 响应逐字不变。
type concurrencyWaitOutcome int

const (
	concurrencyWaitAdmitted concurrencyWaitOutcome = iota
	concurrencyWaitTimedOut
	concurrencyWaitClientGone
)

// waitForConcurrencySlot 轮询重试批量获取，直到拿到槽位、越过 deadline 或
// 客户端断开。入队后先等再试：间隔从 50ms 起按 ×1.5 指数退避、封顶 500ms，
// 每次加 0–25ms 抖动。返回 (release, concurrencyWaitAdmitted) 表示已拿到槽位；
// 其余两种结局调用方应立即 429 —— 此时排队位仍由调用方已注册的 defer leave()
// 归还。
func waitForConcurrencySlot(ctx context.Context, l concurrencyLimiterAdapter, scopes []concurrency.ScopeLimit, deadline time.Time) (func(), concurrencyWaitOutcome) {
	backoff := queueRetryBaseInterval
	for {
		// 每次重试前先看客户端是否已断开：断开就立刻退出等待。
		select {
		case <-ctx.Done():
			return nil, concurrencyWaitClientGone
		default:
		}

		now := time.Now()
		if !now.Before(deadline) {
			return nil, concurrencyWaitTimedOut
		}
		wait := backoff + time.Duration(rand.Int63n(int64(queueRetryMaxJitter)))
		if remaining := deadline.Sub(now); wait > remaining {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, concurrencyWaitClientGone
		case <-timer.C:
		}

		if release, err := l.AcquireBatch(scopes); err == nil {
			return release, concurrencyWaitAdmitted
		}
		backoff = time.Duration(float64(backoff) * queueRetryBackoffFactor)
		if backoff > queueRetryMaxInterval {
			backoff = queueRetryMaxInterval
		}
	}
}

// buildScopes returns the scope buckets the gateway acquires for one request,
// each paired with its per-request limit. The token axis contributes
// key:<id>:<model> (LimitForModel) and key:<id>:* (Global) buckets; acquiring
// both in one batch means the stricter cap wins (min semantics). The user
// axis contributes a single aggregate bucket user:<id>:* carrying the
// configured user cap — it counts every in-flight request of that user across
// all of the user's tokens (USR-01), and there is deliberately no
// user:<id>:<model> bucket. Order is irrelevant because the batch is granted
// or refused atomically (D-05) and the single release frees it as a unit.
func buildScopes(subject concurrencySubject) []concurrency.ScopeLimit {
	scopes := make([]concurrency.ScopeLimit, 0, 3)
	if subject.tokenID > 0 {
		if subject.model != "" {
			scopes = append(scopes, concurrency.ScopeLimit{
				Scope: concurrency.ScopeKey("key:" + strconv.Itoa(subject.tokenID) + ":" + subject.model),
				Limit: subject.limits.LimitForModel(subject.model),
			})
		}
		scopes = append(scopes, concurrency.ScopeLimit{
			Scope: concurrency.ScopeKey("key:" + strconv.Itoa(subject.tokenID) + ":*"),
			Limit: subject.limits.Global,
		})
	}
	if subject.userID > 0 {
		scopes = append(scopes, concurrency.ScopeLimit{
			Scope: concurrency.ScopeKey("user:" + strconv.Itoa(subject.userID) + ":*"),
			Limit: subject.userLimit,
		})
	}
	return scopes
}

// concurrencySubject is the identity triple plus the concurrency limit
// configuration for one request.
type concurrencySubject struct {
	userID    int
	tokenID   int
	model     string
	limits    concurrency.TokenConcurrencyLimits
	userLimit int
}

// readConcurrencySubject pulls the (userID, tokenID, model) triple and the
// concurrency limits from the gin context. Production callers populate these
// via middleware.SetupContextForToken (token limits, key "token_concurrency_
// limits") and middleware.Distribute (model); tests inject identity and limits
// via X-Test-* headers. Every X-Test-* fallback read is gated behind
// gin.TestMode so the headers can never become a production limit- or
// identity-injection surface (threat T-02-04); in release/debug mode the
// fallbacks are skipped entirely.
func readConcurrencySubject(c *gin.Context) concurrencySubject {
	subject := concurrencySubject{
		userID:  c.GetInt("id"),
		tokenID: c.GetInt("token_id"),
		// The configured user cap published by UserBase.WriteContext (02-03-03)
		// under constant.ContextKeyUserConcurrencyLimit — read through the
		// typed helper so the key cannot drift from the publisher.
		userLimit: common.GetContextKeyInt(c, constant.ContextKeyUserConcurrencyLimit),
	}
	if v, ok := c.Get("token_concurrency_limits"); ok {
		if limits, ok := v.(concurrency.TokenConcurrencyLimits); ok {
			subject.limits = limits
		}
	}
	if v, ok := c.Get(string(constant.ContextKeyOriginalModel)); ok {
		subject.model, _ = v.(string)
	}
	if gin.Mode() != gin.TestMode {
		return subject
	}
	if subject.userID == 0 {
		if v := c.GetHeader("X-Test-User-Id"); v != "" {
			subject.userID, _ = strconv.Atoi(v)
		}
	}
	if subject.tokenID == 0 {
		if v := c.GetHeader("X-Test-Token-Id"); v != "" {
			subject.tokenID, _ = strconv.Atoi(v)
		}
	}
	if subject.model == "" {
		subject.model = c.GetHeader("X-Test-Model")
	}
	if v := c.GetHeader("X-Test-Token-Concurrency-Limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			subject.limits.Global = n
		}
	}
	if v := c.GetHeader("X-Test-Token-Concurrency-By-Model"); v != "" {
		subject.limits = concurrency.ParseTokenConcurrencyLimits(subject.limits.Global, v)
	}
	if v := c.GetHeader("X-Test-User-Concurrency-Limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			subject.userLimit = n
		}
	}
	return subject
}

// rejectConcurrency writes the 429 body and stops the chain. Kept as its own
// function so the test suite can pin the response shape independently. The
// message argument distinguishes the refusal source (06) while the status,
// type and code stay unified.
func rejectConcurrency(c *gin.Context, message string) {
	concurrency.AddRejection()
	c.Header("Retry-After", strconv.Itoa(ConcurrencyRetryAfterSeconds))
	c.JSON(http.StatusTooManyRequests, gin.H{
		"error": gin.H{
			"message": message,
			"type":    "concurrency_exceeded",
			"code":    "concurrency_exceeded",
		},
	})
	c.Abort()
}

// Compile-time check that the production Limiter satisfies the adapter so we
// cannot ship a stub mismatch.
var _ concurrencyLimiterAdapter = (*concurrencyAdapter)(nil)

type concurrencyAdapter struct{ inner concurrency.Limiter }

// AcquireBatch keeps the Phase-2 Background-context form (D-06): the Redis
// timeout wrapper lives inside RedisLimiter, never on the request context.
func (a *concurrencyAdapter) AcquireBatch(scopes []concurrency.ScopeLimit) (func(), error) {
	return a.inner.AcquireBatch(context.Background(), scopes)
}

// WrapConcurrencyLimiter adapts a production Limiter (which carries context) to
// the no-context shape the middleware uses. Use SetConcurrencyLimiter instead
// of calling this directly.
func WrapConcurrencyLimiter(l concurrency.Limiter) concurrencyLimiterAdapter {
	return &concurrencyAdapter{inner: l}
}
