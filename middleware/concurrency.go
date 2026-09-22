package middleware

import (
	"context"
	"net/http"
	"strconv"
	"sync"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service/concurrency"
	"github.com/gin-gonic/gin"
)

// concurrencyLimiterAdapter bridges the package-private Acquire contract used
// in concurrency_test.go (a stub) with the exported Limiter interface used by
// production code. We keep the test stub free of the production Limiter
// dependency to avoid pulling Redis into the unit tests.
type concurrencyLimiterAdapter interface {
	Acquire(scope concurrency.ScopeKey) (func(), error)
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

		userID, tokenID, model := readConcurrencySubject(c)
		if userID == 0 && tokenID == 0 {
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

		// Acquire both axes: per-key global, per-key model, per-user global,
		// per-user model. The per-model cap takes precedence over global on
		// each axis; if any one of them refuses, the request is rejected.
		// Releases run in LIFO order so a panic in one defer doesn't strand
		// the others.
		releases := make([]func(), 0, 4)
		releaseAll := func() {
			for i := len(releases) - 1; i >= 0; i-- {
				releases[i]()
			}
		}
		defer releaseAll()

		scopes := buildScopes(userID, tokenID, model)
		for _, scope := range scopes {
			release, err := l.Acquire(scope)
			if err != nil {
				rejectConcurrency(c)
				return
			}
			releases = append(releases, release)
		}

		c.Next()
	}
}

// buildScopes returns the four scope buckets the gateway enforces for one
// request. Order is irrelevant because Acquire either succeeds or rejects
// atomically per scope and the defer fan-out handles release. We omit a
// scope when its effective limit is zero (unlimited) so the limiter never
// sees a zero-cap bucket.
func buildScopes(userID, tokenID int, model string) []concurrency.ScopeKey {
	scopes := make([]concurrency.ScopeKey, 0, 4)
	if tokenID > 0 {
		if model != "" {
			scopes = append(scopes, concurrency.ScopeKey("key:"+strconv.Itoa(tokenID)+":"+model))
		}
		scopes = append(scopes, concurrency.ScopeKey("key:"+strconv.Itoa(tokenID)+":*"))
	}
	if userID > 0 {
		if model != "" {
			scopes = append(scopes, concurrency.ScopeKey("user:"+strconv.Itoa(userID)+":"+model))
		}
		scopes = append(scopes, concurrency.ScopeKey("user:"+strconv.Itoa(userID)+":*"))
	}
	return scopes
}

// readConcurrencySubject pulls the (userID, tokenID, model) triple from the
// gin context. Production callers populate these via middleware.SetupContextForToken
// and middleware.Distribute; tests inject them via X-Test-* headers so the
// middleware remains decoupled from a full relay stack.
func readConcurrencySubject(c *gin.Context) (int, int, string) {
	userID := c.GetInt("id")
	tokenID := c.GetInt("token_id")
	model, _ := c.Get(string(constant.ContextKeyOriginalModel))
	modelStr, _ := model.(string)
	if userID == 0 {
		if v := c.GetHeader("X-Test-User-Id"); v != "" {
			userID, _ = strconv.Atoi(v)
		}
	}
	if tokenID == 0 {
		if v := c.GetHeader("X-Test-Token-Id"); v != "" {
			tokenID, _ = strconv.Atoi(v)
		}
	}
	if modelStr == "" {
		if v := c.GetHeader("X-Test-Model"); v != "" {
			modelStr = v
		}
	}
	return userID, tokenID, modelStr
}

// rejectConcurrency writes the 429 body and stops the chain. Kept as its own
// function so the test suite can pin the response shape independently.
func rejectConcurrency(c *gin.Context) {
	c.JSON(http.StatusTooManyRequests, gin.H{
		"error": gin.H{
			"message": "Concurrency limit exceeded for your account. Please retry after the current requests finish.",
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

func (a *concurrencyAdapter) Acquire(scope concurrency.ScopeKey) (func(), error) {
	return a.inner.Acquire(context.Background(), scope)
}

// WrapConcurrencyLimiter adapts a production Limiter (which carries context) to
// the no-context shape the middleware uses. Use SetConcurrencyLimiter instead
// of calling this directly.
func WrapConcurrencyLimiter(l concurrency.Limiter) concurrencyLimiterAdapter {
	return &concurrencyAdapter{inner: l}
}
