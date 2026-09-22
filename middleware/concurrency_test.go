package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/service/concurrency"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubLimiter implements concurrencyLimiterAdapter (no-context shape).
// Tests inject it via SetConcurrencyLimiter and reset via the teardown.
type stubLimiter struct {
	mu     sync.Mutex
	limits map[string]int
	held   map[string]int
}

func newStubLimiter() *stubLimiter {
	return &stubLimiter{limits: map[string]int{}, held: map[string]int{}}
}

func (s *stubLimiter) Acquire(scope concurrency.ScopeKey) (func(), error) {
	s.mu.Lock()
	k := scope.String()
	limit, ok := s.limits[k]
	if !ok || limit <= 0 {
		s.held[k]++
		s.mu.Unlock()
		return func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.held[k] > 0 {
				s.held[k]--
			}
		}, nil
	}
	if s.held[k] >= limit {
		s.mu.Unlock()
		return nil, concurrency.ErrConcurrencyExceeded
	}
	s.held[k]++
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.held[k] > 0 {
			s.held[k]--
		}
	}, nil
}

func (s *stubLimiter) setLimit(scope concurrency.ScopeKey, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limits[scope.String()] = n
}

func (s *stubLimiter) heldFor(scope concurrency.ScopeKey) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held[scope.String()]
}

func resetMiddlewareLimiter(t *testing.T, l concurrencyLimiterAdapter) {
	t.Helper()
	concurrencyLimiter = l
}

func newTestEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ConcurrencyMiddleware())
	r.POST("/probe", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return r
}

func TestMiddleware_RequiresAuthContext(t *testing.T) {
	resetMiddlewareLimiter(t, newStubLimiter())
	r := newTestEngine()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMiddleware_RejectsOverLimit(t *testing.T) {
	stub := newStubLimiter()
	stub.setLimit(concurrency.ScopeKey("key:7:gpt-4o"), 1)
	resetMiddlewareLimiter(t, stub)
	gin.SetMode(gin.TestMode)

	type result struct {
		code int
		body string
	}
	resCh := make(chan result, 2)
	reachedHandler := make(chan struct{}, 1) // only one request can reach handler
	relay := make(chan struct{})

	r := gin.New()
	r.Use(ConcurrencyMiddleware())
	r.POST("/probe", func(c *gin.Context) {
		select {
		case reachedHandler <- struct{}{}:
		default:
		}
		<-relay
		c.Status(http.StatusOK)
	})

	for i := 0; i < 2; i++ {
		go func() {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
			req.Header.Set("X-Test-User-Id", "7")
			req.Header.Set("X-Test-Token-Id", "7")
			req.Header.Set("X-Test-Model", "gpt-4o")
			r.ServeHTTP(w, req)
			resCh <- result{code: w.Code, body: w.Body.String()}
		}()
	}

	// Wait for the first request to reach the handler (proving the
	// middleware let it through).
	<-reachedHandler

	// Give the second request a moment to be rejected by the middleware.
	// It must have called resCh by now.
	rejected := <-resCh
	assert.Equal(t, http.StatusTooManyRequests, rejected.code)
	var body map[string]map[string]string
	require.NoError(t, json.Unmarshal([]byte(rejected.body), &body))
	require.NotNil(t, body["error"])
	assert.Equal(t, "concurrency_exceeded", body["error"]["code"])
	assert.Equal(t, "concurrency_exceeded", body["error"]["type"])

	// Now release the first request and confirm it returns 200.
	close(relay)
	passed := <-resCh
	assert.Equal(t, http.StatusOK, passed.code)
}

func TestMiddleware_ReleasesSlotOnSuccess(t *testing.T) {
	stub := newStubLimiter()
	stub.setLimit(concurrency.ScopeKey("key:9:*"), 1)
	resetMiddlewareLimiter(t, stub)
	r := newTestEngine()

	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
		req.Header.Set("X-Test-User-Id", "9")
		req.Header.Set("X-Test-Token-Id", "9")
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "iteration %d should succeed because previous request released", i)
	}
}

func TestMiddleware_BothAxesRequired(t *testing.T) {
	stub := newStubLimiter()
	stub.setLimit(concurrency.ScopeKey("key:11:gpt-4o"), 5)
	stub.setLimit(concurrency.ScopeKey("user:11:gpt-4o"), 1)
	resetMiddlewareLimiter(t, stub)
	gin.SetMode(gin.TestMode)

	// Each handler signals that it has reached the downstream c.Status line,
	// then blocks on a per-request release so the slot stays held.
	type result struct {
		code int
	}
	resCh := make(chan result, 2)
	relay := make(chan struct{})

	r := gin.New()
	r.Use(ConcurrencyMiddleware())
	r.POST("/probe", func(c *gin.Context) {
		resCh <- result{code: 200} // reached downstream = middleware passed
		<-relay
		c.Status(http.StatusOK)
	})

	for i := 0; i < 2; i++ {
		go func() {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
			req.Header.Set("X-Test-User-Id", "11")
			req.Header.Set("X-Test-Token-Id", "11")
			req.Header.Set("X-Test-Model", "gpt-4o")
			r.ServeHTTP(w, req)
			resCh <- result{code: w.Code}
		}()
	}

	// First request signals "reached downstream" = middleware passed.
	first := <-resCh
	assert.Equal(t, 200, first.code, "first request must reach downstream (middleware passed)")

	// Second request should have been rejected immediately by middleware.
	second := <-resCh
	assert.Equal(t, http.StatusTooManyRequests, second.code, "second concurrent request must be blocked by user axis")

	close(relay) // drain the first request so test cleanup doesn't hang
}
