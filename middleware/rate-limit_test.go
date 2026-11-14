package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dispatchRequest runs one request through handler and returns the recorder
// plus whether the terminal handler behind the limiter executed.
func dispatchRequest(handler gin.HandlerFunc) (*httptest.ResponseRecorder, bool) {
	gin.SetMode(gin.TestMode)
	executed := false
	r := gin.New()
	r.Use(handler)
	r.GET("/", func(c *gin.Context) {
		executed = true
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:12345"
	r.ServeHTTP(w, req)
	return w, executed
}

// TestGlobalAPIRateLimitFailsOpenToMemoryWhenRedisDown pins the D-08/D-12
// fail-open contract on the rate-limit surface: a Redis outage during a
// request must never turn the call into a 500 (observed for real on
// 2026-10-13 — snapshot endpoint 500'd for the whole outage window, so the
// admin page could never read degraded status). The quota stays enforced via
// the process-local window instead of being dropped or escalated to a server
// error.
func TestGlobalAPIRateLimitFailsOpenToMemoryWhenRedisDown(t *testing.T) {
	prevEnabled := common.RedisEnabled
	prevRDB := common.RDB
	prevNum := common.GlobalApiRateLimitNum
	prevDur := common.GlobalApiRateLimitDuration
	prevEnable := common.GlobalApiRateLimitEnable
	t.Cleanup(func() {
		common.RedisEnabled = prevEnabled
		common.RDB = prevRDB
		common.GlobalApiRateLimitNum = prevNum
		common.GlobalApiRateLimitDuration = prevDur
		common.GlobalApiRateLimitEnable = prevEnable
	})

	// Redis "enabled" but unusable — redisFixedWindowTake errors out.
	common.RedisEnabled = true
	common.RDB = nil
	common.GlobalApiRateLimitEnable = true
	common.GlobalApiRateLimitNum = 3
	common.GlobalApiRateLimitDuration = 60

	handler := GlobalAPIRateLimit()

	// First requests must pass (fail-open to the memory window, not 500).
	for i := 0; i < 3; i++ {
		w, executed := dispatchRequest(handler)
		require.Truef(t, executed, "request %d: terminal handler must run during Redis outage", i+1)
		require.Equalf(t, http.StatusOK, w.Code, "request %d: must not escalate a Redis outage to 500", i+1)
	}

	// The fallback window still enforces the quota.
	w, executed := dispatchRequest(handler)
	assert.False(t, executed, "4th request: in-memory fallback must still reject over-quota calls")
	assert.Equal(t, http.StatusTooManyRequests, w.Code, "4th request: expect 429 from memory fallback")
	assert.NotEmpty(t, w.Header().Get("Retry-After"), "4th request: 429 keeps the Retry-After hint")
}

// TestUserRateLimitFailsOpenToMemoryWhenRedisDown is the user-scoped twin:
// pre-built user keys must fall back to the same process-local window instead
// of 500-ing authenticated endpoints during a Redis outage.
func TestUserRateLimitFailsOpenToMemoryWhenRedisDown(t *testing.T) {
	prevEnabled := common.RedisEnabled
	prevRDB := common.RDB
	t.Cleanup(func() {
		common.RedisEnabled = prevEnabled
		common.RDB = prevRDB
	})

	common.RedisEnabled = true
	common.RDB = nil

	handler := userRateLimitFactory(2, 60, "UC:test")

	for i := 0; i < 2; i++ {
		gin.SetMode(gin.TestMode)
		executed := false
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Set("id", 42)
		})
		r.Use(handler)
		r.GET("/", func(c *gin.Context) {
			executed = true
			c.Status(http.StatusOK)
		})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "203.0.113.9:12345"
		r.ServeHTTP(w, req)
		require.Truef(t, executed, "request %d: terminal handler must run during Redis outage", i+1)
		require.Equalf(t, http.StatusOK, w.Code, "request %d: must not escalate a Redis outage to 500", i+1)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("id", 42)
	})
	r.Use(handler)
	executed := false
	r.GET("/", func(c *gin.Context) {
		executed = true
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.9:12345"
	r.ServeHTTP(w, req)
	assert.False(t, executed, "3rd request: in-memory fallback must still reject over-quota calls")
	assert.Equal(t, http.StatusTooManyRequests, w.Code, "3rd request: expect 429 from memory fallback")
}
