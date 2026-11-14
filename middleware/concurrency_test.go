package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/concurrency"
	"github.com/QuantumNous/new-api/setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubAcquire records one Acquire call's (scope, limit) pair so tests can
// assert the caps the middleware computed for each bucket.
type stubAcquire struct {
	scope concurrency.ScopeKey
	limit int
}

// stubLimiter implements concurrencyLimiterAdapter (no-context batch shape).
// Tests inject it via SetConcurrencyLimiter and reset via the teardown.
// It judges each batch all-or-nothing by the limits the middleware passes in —
// per-request caps are computed by buildScopes, never pushed into the limiter —
// and records the pairs for assertions.
type stubLimiter struct {
	mu       sync.Mutex
	held     map[string]int
	acquired []stubAcquire
}

func newStubLimiter() *stubLimiter {
	return &stubLimiter{held: map[string]int{}}
}

func (s *stubLimiter) AcquireBatch(scopes []concurrency.ScopeLimit) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Record every (scope, limit) pair the middleware computed, including
	// pairs of a refused batch — assertions ask which caps were evaluated.
	for _, sl := range scopes {
		s.acquired = append(s.acquired, stubAcquire{scope: sl.Scope, limit: sl.Limit})
	}
	// All-or-nothing: judge every bucket before writing any hold.
	for _, sl := range scopes {
		k := sl.Scope.String()
		if sl.Limit > 0 && s.held[k] >= sl.Limit {
			return nil, concurrency.ErrConcurrencyExceeded
		}
	}
	keys := make([]string, 0, len(scopes))
	for _, sl := range scopes {
		k := sl.Scope.String()
		s.held[k]++
		keys = append(keys, k)
	}
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, k := range keys {
			if s.held[k] > 0 {
				s.held[k]--
			}
		}
	}, nil
}

func (s *stubLimiter) heldFor(scope concurrency.ScopeKey) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held[scope.String()]
}

// calls returns a snapshot of every recorded (scope, limit) pair.
func (s *stubLimiter) calls() []stubAcquire {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]stubAcquire, len(s.acquired))
	copy(out, s.acquired)
	return out
}

// limitFor returns the most recently passed limit for scope, if any.
func (s *stubLimiter) limitFor(scope concurrency.ScopeKey) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.acquired) - 1; i >= 0; i-- {
		if s.acquired[i].scope == scope {
			return s.acquired[i].limit, true
		}
	}
	return 0, false
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
	resetMiddlewareLimiter(t, stub)
	gin.SetMode(gin.TestMode)

	type result struct {
		code   int
		body   string
		header http.Header
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

	// Only one of the two racing requests can be shed by the gate, so the
	// rejection counter delta for this test is exactly one.
	rejectionsBefore := concurrency.RejectionCount()

	for i := 0; i < 2; i++ {
		go func() {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
			req.Header.Set("X-Test-User-Id", "7")
			req.Header.Set("X-Test-Token-Id", "7")
			req.Header.Set("X-Test-Model", "gpt-4o")
			req.Header.Set("X-Test-Token-Concurrency-By-Model", "gpt-4o=1")
			r.ServeHTTP(w, req)
			resCh <- result{code: w.Code, body: w.Body.String(), header: w.Header()}
		}()
	}

	// Wait for the first request to reach the handler (proving the
	// middleware let it through).
	<-reachedHandler

	// Give the second request a moment to be rejected by the middleware.
	// It must have called resCh by now.
	rejected := <-resCh
	assert.Equal(t, http.StatusTooManyRequests, rejected.code)
	assert.Equal(t, "1", rejected.header.Get("Retry-After"), "429 must carry Retry-After: 1")
	var body map[string]map[string]string
	require.NoError(t, json.Unmarshal([]byte(rejected.body), &body))
	require.NotNil(t, body["error"])
	assert.Equal(t, "concurrency_exceeded", body["error"]["code"])
	assert.Equal(t, "concurrency_exceeded", body["error"]["type"])
	assert.Equal(t, "Concurrency limit exceeded for your account. Please retry after the current requests finish.",
		body["error"]["message"], "429 message must stay verbatim (CNT-01)")
	assert.Equal(t, rejectionsBefore+1, concurrency.RejectionCount(),
		"a client-visible middleware 429 must increment the shed-load counter")

	// Now release the first request and confirm it returns 200.
	close(relay)
	passed := <-resCh
	assert.Equal(t, http.StatusOK, passed.code)
}

func TestMiddleware_ReleasesSlotOnSuccess(t *testing.T) {
	stub := newStubLimiter()
	resetMiddlewareLimiter(t, stub)
	r := newTestEngine()

	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
		req.Header.Set("X-Test-User-Id", "9")
		req.Header.Set("X-Test-Token-Id", "9")
		req.Header.Set("X-Test-Token-Concurrency-Limit", "1")
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "iteration %d should succeed because previous request released", i)
	}
}

// TestMiddleware_BothAxesRequired pins the ENF-02 min semantics on the token
// axis: the model cap (5) and the global cap (1) are acquired as separate
// buckets, so the stricter cap governs — the looser model cap can never bypass
// the global cap. It also asserts the (scope, limit) pairs the middleware
// computes, including the user axis: the request pairs key:<id>:<model>,
// key:<id>:* and user:<id>:* — three buckets, no user:<id>:<model> — with the
// user bucket carrying the configured user cap.
func TestMiddleware_BothAxesRequired(t *testing.T) {
	stub := newStubLimiter()
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
			req.Header.Set("X-Test-Token-Concurrency-By-Model", "gpt-4o=5")
			req.Header.Set("X-Test-Token-Concurrency-Limit", "1")
			req.Header.Set("X-Test-User-Concurrency-Limit", "1")
			r.ServeHTTP(w, req)
			resCh <- result{code: w.Code}
		}()
	}

	// First request signals "reached downstream" = middleware passed.
	first := <-resCh
	assert.Equal(t, 200, first.code, "first request must reach downstream (middleware passed)")

	// Second request must be rejected by the stricter (global) cap even though
	// the looser model cap would still have room.
	second := <-resCh
	assert.Equal(t, http.StatusTooManyRequests, second.code, "second concurrent request must be blocked by the stricter global cap")

	close(relay) // drain the first request so test cleanup doesn't hang

	// The middleware must have paired each bucket with its per-request cap.
	limit, ok := stub.limitFor(concurrency.ScopeKey("key:11:gpt-4o"))
	require.True(t, ok, "model bucket must be acquired")
	assert.Equal(t, 5, limit, "model bucket carries the per-model cap")
	limit, ok = stub.limitFor(concurrency.ScopeKey("key:11:*"))
	require.True(t, ok, "global bucket must be acquired")
	assert.Equal(t, 1, limit, "global bucket carries the token global cap")
	limit, ok = stub.limitFor(concurrency.ScopeKey("user:11:*"))
	require.True(t, ok, "user aggregate bucket must be acquired")
	assert.Equal(t, 1, limit, "user aggregate bucket carries the configured user cap")
	_, ok = stub.limitFor(concurrency.ScopeKey("user:11:gpt-4o"))
	assert.False(t, ok, "no per-model user bucket — buildScopes emits exactly three buckets")
}

// TestMiddleware_BuildScopesEmitsThreeBuckets pins the three-bucket terminal
// state of buildScopes (USR-01/USP-01): for a request with a model the scope
// set is exactly key:<id>:<model> (its per-model cap), key:<id>:* (token
// global) and user:<id>:* (the configured user cap). The former
// user:<id>:<model> bucket must not be produced.
func TestMiddleware_BuildScopesEmitsThreeBuckets(t *testing.T) {
	subject := concurrencySubject{
		userID:    11,
		tokenID:   11,
		model:     "gpt-4o",
		limits:    concurrency.ParseTokenConcurrencyLimits(1, "gpt-4o=5"),
		userLimit: 1,
	}

	scopes := buildScopes(subject)
	require.Len(t, scopes, 3, "buildScopes must produce exactly three buckets when a model is present")

	got := make([]string, 0, len(scopes))
	for _, sl := range scopes {
		got = append(got, sl.Scope.String()+"="+strconv.Itoa(sl.Limit))
	}
	assert.Equal(t, []string{
		"key:11:gpt-4o=5",
		"key:11:*=1",
		"user:11:*=1",
	}, got, "scope+limit pairs must be exactly the three-bucket set")
}

// TestMiddleware_UserAggregateAcrossTokens pins the USR-01 aggregate
// semantics: two DIFFERENT tokens of the SAME user, both individually
// unlimited, are counted into the single user:<id>:* bucket, so a user cap of
// 1 rejects the second token's concurrent request with 429 while the first
// request is untouched.
func TestMiddleware_UserAggregateAcrossTokens(t *testing.T) {
	stub := newStubLimiter()
	resetMiddlewareLimiter(t, stub)
	gin.SetMode(gin.TestMode)

	const (
		userID      = 31
		firstToken  = 41
		secondToken = 42
	)

	passed := make(chan struct{}, 4) // one slot per request that cleared the gate
	relay := make(chan struct{})
	resCh := make(chan int, 4)

	r := gin.New()
	r.Use(ConcurrencyMiddleware())
	r.POST("/probe", func(c *gin.Context) {
		passed <- struct{}{} // reached downstream = middleware passed
		<-relay
		c.Status(http.StatusOK)
	})

	send := func(userID, tokenID, userLimit int) int {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
		req.Header.Set("X-Test-User-Id", strconv.Itoa(userID))
		req.Header.Set("X-Test-Token-Id", strconv.Itoa(tokenID))
		req.Header.Set("X-Test-Model", "gpt-4o")
		req.Header.Set("X-Test-User-Concurrency-Limit", strconv.Itoa(userLimit))
		r.ServeHTTP(w, req)
		return w.Code
	}

	// 1) First token's request clears the gate and holds its slots.
	go func() { resCh <- send(userID, firstToken, 1) }()
	<-passed
	require.Equal(t, 1, stub.heldFor(concurrency.ScopeKey("user:31:*")),
		"the held request must occupy the user aggregate bucket")

	// 2) A different token of the SAME user must be rejected by the aggregate
	// user cap — each token alone is unlimited.
	go func() { resCh <- send(userID, secondToken, 1) }()
	select {
	case code := <-resCh:
		require.Equal(t, http.StatusTooManyRequests, code,
			"second token of the same user must be rejected by the user:31:* aggregate cap")
	case <-passed:
		close(relay)
		<-resCh
		<-resCh
		require.Fail(t, "second token of the same user was not rejected by the aggregate user cap")
		return
	}

	// The rejected request must not have leaked a slot into the bucket.
	assert.Equal(t, 1, stub.heldFor(concurrency.ScopeKey("user:31:*")),
		"the rejected request must roll back its aggregate slot")

	// 3) Release the first request: it completes 200 and the bucket drains.
	close(relay)
	require.Equal(t, http.StatusOK, <-resCh, "first request must be unaffected by the rejected sibling")
	require.Equal(t, 0, stub.heldFor(concurrency.ScopeKey("user:31:*")),
		"aggregate bucket must drain once the held request finishes")

	// 4) A later request on the second token passes: the slot was freed.
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
	req.Header.Set("X-Test-User-Id", strconv.Itoa(userID))
	req.Header.Set("X-Test-Token-Id", strconv.Itoa(secondToken))
	req.Header.Set("X-Test-Model", "gpt-4o")
	req.Header.Set("X-Test-User-Concurrency-Limit", "1")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "next request must pass once the aggregate slot is free")
}

// TestMiddleware_UserCapScopedToSingleUser pins the prohibition on binding the
// wrong subject (threat T-02-06): a user cap of 1 constrains ONLY that user's
// own bucket. While user A sits at its cap, a request from user B — which has
// its own cap of 1 but an empty bucket — must still pass.
func TestMiddleware_UserCapScopedToSingleUser(t *testing.T) {
	stub := newStubLimiter()
	resetMiddlewareLimiter(t, stub)
	gin.SetMode(gin.TestMode)

	const (
		userA   = 33
		userB   = 34
		tokenA  = 44
		tokenB  = 45
		tokenA2 = 46
	)

	passed := make(chan struct{}, 4)
	relay := make(chan struct{})
	resCh := make(chan int, 4)

	r := gin.New()
	r.Use(ConcurrencyMiddleware())
	r.POST("/probe", func(c *gin.Context) {
		passed <- struct{}{}
		<-relay
		c.Status(http.StatusOK)
	})

	send := func(userID, tokenID int) int {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
		req.Header.Set("X-Test-User-Id", strconv.Itoa(userID))
		req.Header.Set("X-Test-Token-Id", strconv.Itoa(tokenID))
		req.Header.Set("X-Test-Model", "gpt-4o")
		req.Header.Set("X-Test-User-Concurrency-Limit", "1")
		r.ServeHTTP(w, req)
		return w.Code
	}

	// 1) User A holds its cap of 1.
	go func() { resCh <- send(userA, tokenA) }()
	<-passed
	require.Equal(t, 1, stub.heldFor(concurrency.ScopeKey("user:33:*")))

	// 2) A second token of user A is rejected — the cap is real.
	go func() { resCh <- send(userA, tokenA2) }()
	select {
	case code := <-resCh:
		require.Equal(t, http.StatusTooManyRequests, code, "user A's own cap must reject its second token")
	case <-passed:
		close(relay)
		<-resCh
		<-resCh
		require.Fail(t, "user A's cap did not reject its own second token")
		return
	}

	// 3) User B, independently capped at 1 with an empty bucket, must pass
	// while user A is still holding its slot — no cross-tenant drift.
	go func() { resCh <- send(userB, tokenB) }()
	select {
	case <-passed:
		// Expected: user B is throttled only by its own bucket.
	case code := <-resCh:
		close(relay)
		<-resCh
		require.Fail(t, "user A's cap leaked into user B's requests",
			"expected HTTP 200 for user B, got %d", code)
		return
	}
	assert.Equal(t, 1, stub.heldFor(concurrency.ScopeKey("user:33:*")),
		"user A's bucket still holds exactly its own slot")
	assert.Equal(t, 1, stub.heldFor(concurrency.ScopeKey("user:34:*")),
		"user B's bucket is counted separately")

	close(relay)
	require.Equal(t, http.StatusOK, <-resCh, "user A's first request must complete")
	require.Equal(t, http.StatusOK, <-resCh, "user B's request must complete")
}

// TestMiddleware_RedisLimiterEndToEnd drives the real RedisLimiter (miniredis)
// through WrapConcurrencyLimiter into ConcurrencyMiddleware: a request under
// the cap passes with HTTP 200 and the limiter's snapshot returns to zero once
// the request finishes; a concurrent request over the cap is rejected with the
// pinned 429 contract (type/code=concurrency_exceeded + Retry-After header).
func TestMiddleware_RedisLimiterEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	require.NoError(t, redisClient.Ping(context.Background()).Err())
	t.Cleanup(func() { _ = redisClient.Close() })

	lim := concurrency.NewRedisLimiter(redisClient, "conc:")
	t.Cleanup(lim.Close) // D-01: join the renewal ticker before the client goes away
	resetMiddlewareLimiter(t, WrapConcurrencyLimiter(lim))

	type result struct {
		code   int
		body   string
		header http.Header
	}
	resCh := make(chan result, 2)
	reachedHandler := make(chan struct{}, 1) // only one request can hold slots
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

	send := func() {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
		req.Header.Set("X-Test-User-Id", "17")
		req.Header.Set("X-Test-Token-Id", "17")
		req.Header.Set("X-Test-Model", "gpt-4o")
		req.Header.Set("X-Test-Token-Concurrency-Limit", "1")
		r.ServeHTTP(w, req)
		resCh <- result{code: w.Code, body: w.Body.String(), header: w.Header()}
	}

	// First request clears the gate and holds its slots in Redis.
	go send()
	select {
	case <-reachedHandler:
		snap, err := lim.Snapshot(context.Background())
		require.NoError(t, err)
		require.NotEmpty(t, snap, "held request must be visible in the limiter snapshot")
	case got := <-resCh:
		require.Equal(t, http.StatusOK, got.code, "first request under the cap must pass")
		t.Fatal("first request finished without holding — slots were not retained")
	}

	// A concurrent second request over the cap must get the pinned 429.
	go send()
	rejected := <-resCh
	assert.Equal(t, http.StatusTooManyRequests, rejected.code)
	assert.Equal(t, "1", rejected.header.Get("Retry-After"), "429 must carry Retry-After: 1")
	var body map[string]map[string]string
	require.NoError(t, json.Unmarshal([]byte(rejected.body), &body))
	require.NotNil(t, body["error"])
	assert.Equal(t, "concurrency_exceeded", body["error"]["code"])
	assert.Equal(t, "concurrency_exceeded", body["error"]["type"])

	// Drain the holder; the limiter snapshot must be back to zero.
	close(relay)
	passed := <-resCh
	assert.Equal(t, http.StatusOK, passed.code)

	snap, err := lim.Snapshot(context.Background())
	require.NoError(t, err)
	assert.Empty(t, snap, "snapshot must return to zero once every request finished")
}

// TestMiddleware_IgnoresTestHeadersOutsideTestMode pins threat T-02-04: the
// X-Test-* fallback headers (identity and limit injection) are honored only in
// gin test mode. In release mode a request carrying them has no identity and
// must fail closed with 400 — proof the headers are not a production limit- or
// identity-injection surface (OWASP ASVS-relevant request-injection control).
func TestMiddleware_IgnoresTestHeadersOutsideTestMode(t *testing.T) {
	resetMiddlewareLimiter(t, newStubLimiter())
	gin.SetMode(gin.ReleaseMode)
	defer gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(ConcurrencyMiddleware())
	r.POST("/probe", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
	req.Header.Set("X-Test-User-Id", "7")
	req.Header.Set("X-Test-Token-Id", "7")
	req.Header.Set("X-Test-Model", "gpt-4o")
	req.Header.Set("X-Test-Token-Concurrency-Limit", "1")
	req.Header.Set("X-Test-Token-Concurrency-By-Model", "gpt-4o=1")
	req.Header.Set("X-Test-User-Concurrency-Limit", "1")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"X-Test-* fallback headers must be ignored outside test mode (T-02-04)")
}

// TestMiddleware_ModelAndGlobalMostRestrictiveWins pins the ENF-02 min(model
// cap, global cap) matrix: whichever cap is stricter governs, no matter which
// axis it came from, and a model the CSV does not list only counts (limit 0)
// while the global cap keeps working. It also asserts the (scope, limit)
// pairs the middleware computes for each bucket.
func TestMiddleware_ModelAndGlobalMostRestrictiveWins(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name       string
		global     int
		byModel    string
		wantReject bool
	}{
		{name: "model cap 2 tighter than global cap 5 rejects third", global: 5, byModel: "gpt-4o=2", wantReject: true},
		{name: "global cap 2 tighter than model cap 5 rejects third", global: 2, byModel: "gpt-4o=5", wantReject: true},
		{name: "only model cap 2 rejects third", global: 0, byModel: "gpt-4o=2", wantReject: true},
		{name: "only global cap 2 rejects third", global: 2, byModel: "", wantReject: true},
		{name: "unlisted model bucket only counts, global cap governs", global: 2, byModel: "other-model=2", wantReject: true},
		{name: "no caps all requests pass", global: 0, byModel: "", wantReject: false},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := newStubLimiter()
			resetMiddlewareLimiter(t, stub)
			gin.SetMode(gin.TestMode)
			tokenID := 1200 + i
			modelScope := concurrency.ScopeKey("key:" + strconv.Itoa(tokenID) + ":gpt-4o")
			globalScope := concurrency.ScopeKey("key:" + strconv.Itoa(tokenID) + ":*")

			entered := make(chan struct{}, 8)
			relay := make(chan struct{})
			r := gin.New()
			r.Use(ConcurrencyMiddleware())
			r.POST("/probe", func(c *gin.Context) {
				entered <- struct{}{}
				<-relay
				c.Status(http.StatusOK)
			})

			send := func() int {
				w := httptest.NewRecorder()
				req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
				req.Header.Set("X-Test-User-Id", strconv.Itoa(tokenID))
				req.Header.Set("X-Test-Token-Id", strconv.Itoa(tokenID))
				req.Header.Set("X-Test-Model", "gpt-4o")
				req.Header.Set("X-Test-Token-Concurrency-Limit", strconv.Itoa(tc.global))
				req.Header.Set("X-Test-Token-Concurrency-By-Model", tc.byModel)
				r.ServeHTTP(w, req)
				return w.Code
			}

			resCh := make(chan int, 4)
			go func() { resCh <- send() }()
			<-entered // first request holds its slots
			go func() { resCh <- send() }()
			<-entered // second request holds its slots

			// Every reject case has an effective cap of 2, so the third
			// concurrent request is the one that decides the matrix row.
			go func() { resCh <- send() }()
			var third int
			select {
			case third = <-resCh:
			case <-entered:
				third = http.StatusOK
				close(relay)
				for k := 0; k < 3; k++ {
					require.Equal(t, http.StatusOK, <-resCh, "uncapped case must pass every request")
				}
			}

			if tc.wantReject {
				require.Equal(t, http.StatusTooManyRequests, third,
					"third concurrent request must be rejected by the stricter cap")
			} else {
				require.Equal(t, http.StatusOK, third, "no caps means no rejection")
			}

			// Pair assertions: each bucket must carry its configured cap.
			limits := concurrency.ParseTokenConcurrencyLimits(tc.global, tc.byModel)
			got, ok := stub.limitFor(modelScope)
			require.True(t, ok, "model bucket must be acquired")
			assert.Equal(t, limits.LimitForModel("gpt-4o"), got, "model bucket carries LimitForModel(model)")
			got, ok = stub.limitFor(globalScope)
			require.True(t, ok, "global bucket must be acquired")
			assert.Equal(t, tc.global, got, "global bucket carries the token global cap")

			if tc.wantReject {
				close(relay)
				require.Equal(t, http.StatusOK, <-resCh, "first held request must complete")
				require.Equal(t, http.StatusOK, <-resCh, "second held request must complete")
			}
		})
	}
}

// TestMiddleware_ReleasesSlotsOnAllExits pins ENF-03: slots are returned on
// every exit path — handler panic (bare gin engine, no Recovery), repeated
// serial successes at cap 1 (no accumulation), and a partial-acquire rollback
// when a later bucket refuses.
func TestMiddleware_ReleasesSlotsOnAllExits(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("handler panic releases held slots", func(t *testing.T) {
		stub := newStubLimiter()
		resetMiddlewareLimiter(t, stub)
		r := gin.New() // no Recovery: the panic unwinds through ConcurrencyMiddleware
		panicked := false
		r.Use(ConcurrencyMiddleware())
		r.POST("/probe", func(c *gin.Context) {
			if !panicked {
				panicked = true
				panic("relay handler crashed")
			}
			c.Status(http.StatusOK)
		})

		newReq := func() *http.Request {
			req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
			req.Header.Set("X-Test-User-Id", "13")
			req.Header.Set("X-Test-Token-Id", "13")
			req.Header.Set("X-Test-Model", "gpt-4o")
			req.Header.Set("X-Test-Token-Concurrency-Limit", "1")
			return req
		}

		func() {
			defer func() {
				require.NotNil(t, recover(), "the handler panic must propagate to the caller")
			}()
			r.ServeHTTP(httptest.NewRecorder(), newReq())
		}()
		assert.Equal(t, 0, stub.heldFor(concurrency.ScopeKey("key:13:gpt-4o")), "model slot released after panic")
		assert.Equal(t, 0, stub.heldFor(concurrency.ScopeKey("key:13:*")), "global slot released after panic")

		w := httptest.NewRecorder()
		r.ServeHTTP(w, newReq())
		assert.Equal(t, http.StatusOK, w.Code, "next request must pass once the panicked slots are released")
	})

	t.Run("serial successes do not accumulate slots at cap 1", func(t *testing.T) {
		stub := newStubLimiter()
		resetMiddlewareLimiter(t, stub)
		r := gin.New()
		r.Use(ConcurrencyMiddleware())
		r.POST("/probe", func(c *gin.Context) { c.Status(http.StatusOK) })

		for i := 0; i < 3; i++ {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
			req.Header.Set("X-Test-User-Id", "14")
			req.Header.Set("X-Test-Token-Id", "14")
			req.Header.Set("X-Test-Model", "gpt-4o")
			req.Header.Set("X-Test-Token-Concurrency-Limit", "1")
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code, "iteration %d must pass because each prior request released", i)
		}
		assert.Equal(t, 0, stub.heldFor(concurrency.ScopeKey("key:14:gpt-4o")), "model bucket must not accumulate")
		assert.Equal(t, 0, stub.heldFor(concurrency.ScopeKey("key:14:*")), "global bucket must not accumulate")
	})

	t.Run("second bucket rejection rolls back the first bucket slots", func(t *testing.T) {
		stub := newStubLimiter()
		resetMiddlewareLimiter(t, stub)
		gin.SetMode(gin.TestMode)

		entered := make(chan struct{}, 4)
		relay := make(chan struct{})
		r := gin.New()
		r.Use(ConcurrencyMiddleware())
		r.POST("/probe", func(c *gin.Context) {
			entered <- struct{}{}
			<-relay
			c.Status(http.StatusOK)
		})

		send := func() int {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
			req.Header.Set("X-Test-User-Id", "15")
			req.Header.Set("X-Test-Token-Id", "15")
			req.Header.Set("X-Test-Model", "gpt-4o")
			req.Header.Set("X-Test-Token-Concurrency-Limit", "1")
			r.ServeHTTP(w, req)
			return w.Code
		}

		resCh := make(chan int, 2)
		go func() { resCh <- send() }()
		<-entered // blocked holder takes the model + global slots (cap 1)

		// The second request acquires the model bucket, then the global
		// bucket refuses — its model slot must be rolled back.
		go func() { resCh <- send() }()
		var second int
		select {
		case second = <-resCh:
		case <-entered:
			close(relay)
			<-resCh
			<-resCh
			require.Fail(t, "second request should have been rejected at the global bucket")
			return
		}
		require.Equal(t, http.StatusTooManyRequests, second, "global cap 1 must reject the second request")
		assert.Equal(t, 1, stub.heldFor(concurrency.ScopeKey("key:15:gpt-4o")),
			"only the blocked request's model slot remains — the rejected request rolled its slot back")

		close(relay)
		require.Equal(t, http.StatusOK, <-resCh, "held request must complete")

		assert.Equal(t, 0, stub.heldFor(concurrency.ScopeKey("key:15:gpt-4o")), "all model slots released")
		assert.Equal(t, 0, stub.heldFor(concurrency.ScopeKey("key:15:*")), "all global slots released")
	})
}

// ---------------------------------------------------------------------------
// 02-10 Task 03 — RED-03 end-to-end acceptance: the fail-open outage is driven
// through the REAL middleware chain (ConcurrencyMiddleware → RedisLimiter) and
// must never surface as a client-visible 429, and D-13's recovery nail proves
// a stale lease left by the outage cannot cap capacity afterwards. The outage
// is a transport-level miniredis Close()/Restart() — the same shape a real
// Redis death takes (plan: "miniredis Close/死端口"). The ③ sentinel failover
// soak (live gateway) is TestMiddleware_SoakGatewayTraffic below.
// ---------------------------------------------------------------------------

// Test-local copies of the breaker contract literals: the middleware package
// cannot reach service/concurrency's unexported markers, and hard-coding them
// here keeps the assertions stable (same convention as the service suite).
const (
	mwBreakerCooldownEnvKey = "REDIS_CONCURRENCY_BREAKER_COOLDOWN_SECONDS"
	mwDegradeWarn           = "concurrency: redis"
	mwBreakerTripWarn       = "concurrency: breaker opened"
	mwBreakerRecoverWarn    = "concurrency: breaker closed"

	// mwRedisOpTimeout mirrors service/concurrency's redisOpTimeout (200ms).
	// The D-11 guard below proves a cooldown request never waits that out —
	// a loose upper bound, not a performance assertion.
	mwRedisOpTimeout = 200 * time.Millisecond
)

// syncBuffer is a concurrency-safe log sink: SysError may be emitted from the
// limiter's renewal goroutine while the test goroutine reads the same buffer,
// which races a plain bytes.Buffer under -race.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// captureSysErrorWriter swaps gin.DefaultErrorWriter for an in-memory buffer
// (under LogWriterMu, per common/sys_log.go's writer-swap contract) so the
// outage tests can assert the WARN the fail-open path must emit. Mirrors the
// service package's fixture — unexported test helpers cannot cross packages.
func captureSysErrorWriter(t *testing.T) *syncBuffer {
	t.Helper()
	logs := &syncBuffer{}
	common.LogWriterMu.Lock()
	previous := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = logs
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = previous
		common.LogWriterMu.Unlock()
	})
	return logs
}

// newOutageFixture boots miniredis + a RedisLimiter wired into the middleware
// chain, and returns the pieces the outage tests need: the server (Close to
// break it, Restart to heal it), the raw client (shared-count assertions) and
// the captured SysError log.
func newOutageFixture(t *testing.T) (*miniredis.Miniredis, *redis.Client, *concurrency.RedisLimiter, *syncBuffer) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	require.NoError(t, client.Ping(context.Background()).Err())
	t.Cleanup(func() { _ = client.Close() })
	limiter := concurrency.NewRedisLimiter(client, "conc:")
	t.Cleanup(limiter.Close) // D-01: join the renewal ticker before the client goes away
	resetMiddlewareLimiter(t, WrapConcurrencyLimiter(limiter))
	return server, client, limiter, captureSysErrorWriter(t)
}

// TestMiddleware_RedisOutageNeverReturns429 pins RED-03/SC-3 end to end: with
// a limit=1 identity genuinely over the cap, killing Redis mid-flight must
// serve every request (fail-open, zero 429, zero concurrency_exceeded in the
// body) with the D-08 WARN trail and no per-request timeout wait inside the
// cooldown; once the backend answers again the 429 contract must come back
// solely from real over-limit — same identity, same cap, Retry-After intact.
func TestMiddleware_RedisOutageNeverReturns429(t *testing.T) {
	server, client, _, logs := newOutageFixture(t)
	t.Setenv(mwBreakerCooldownEnvKey, "2") // 2s window: outlasts the burst, short enough to poll
	ctx := context.Background()
	const key = "conc:key:21:*"

	type result struct {
		code    int
		body    string
		header  http.Header
		elapsed time.Duration
	}
	reachedHandler := make(chan struct{}, 8)
	relay := make(chan struct{})
	holderDone := make(chan result, 1)

	r := gin.New()
	r.Use(ConcurrencyMiddleware())
	r.POST("/probe", func(c *gin.Context) {
		if c.GetHeader("X-Test-Hold") == "1" {
			reachedHandler <- struct{}{}
			<-relay
		}
		c.Status(http.StatusOK)
	})

	// send drives one request through the full middleware chain. Caps come
	// from the X-Test-* headers (gin test mode): token axis limit=1 makes the
	// held request an over-limit identity for every sibling request.
	send := func(hold bool) result {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
		req.Header.Set("X-Test-User-Id", "21")
		req.Header.Set("X-Test-Token-Id", "21")
		req.Header.Set("X-Test-Model", "gpt-4o")
		req.Header.Set("X-Test-Token-Concurrency-Limit", "1")
		if hold {
			req.Header.Set("X-Test-Hold", "1")
		}
		start := time.Now()
		r.ServeHTTP(w, req)
		return result{code: w.Code, body: w.Body.String(), header: w.Header(), elapsed: time.Since(start)}
	}

	// 1) Over-limit identity: the holder owns the only limit=1 slot.
	go func() { holderDone <- send(true) }()
	<-reachedHandler

	// 2) Sanity — while Redis is healthy the same identity refuses with the
	// pinned 429 contract, so the outage assertions are a real contrast.
	healthy := send(false)
	require.Equal(t, http.StatusTooManyRequests, healthy.code,
		"sanity: limit=1 must refuse a second concurrent request while healthy")
	require.Equal(t, "1", healthy.header.Get("Retry-After"), "healthy 429 must carry Retry-After: 1")
	require.Contains(t, healthy.body, "concurrency_exceeded", "healthy 429 must carry the pinned error code")

	// 3) Outage: transport failure, the shape a real Redis death takes.
	server.Close()

	// The first outage request pays the failing backend attempt and trips the
	// breaker — it must still fail open (D-08: an infrastructure fault is
	// never a refusal, so never a 429).
	trip := send(false)
	require.Equal(t, http.StatusOK, trip.code, "the tripping request must fail open (RED-03 / D-08 taxonomy)")
	require.Less(t, trip.elapsed, 5*time.Second, "anti-hang guard only, not a performance assertion")
	require.Contains(t, logs.String(), mwDegradeWarn, "the degraded acquire must WARN with the stable prefix")
	require.Contains(t, logs.String(), mwBreakerTripWarn, "the first failure must open the breaker (trip WARN)")

	// Inside the cooldown every request degrades directly: zero backend calls
	// and, per the loose guard, none waiting out the 200ms operation timeout.
	for range 5 {
		got := send(false)
		require.Equal(t, http.StatusOK, got.code,
			"an outage request must be served — RED-03: fail-open, never 429")
		require.NotContains(t, got.body, "concurrency_exceeded",
			"an outage response must never surface the 429 error code (RED-03)")
		require.Less(t, got.elapsed, mwRedisOpTimeout,
			"cooldown requests must degrade without paying the backend operation timeout (D-11 loose guard)")
	}

	// 4) Recovery: the backend answers again and the cooldown expires. The
	// single probe is refused ONLY because the holder genuinely owns the slot —
	// 429 returns exclusively from real over-limit, header contract intact.
	require.NoError(t, server.Restart(), "miniredis must come back for the recovery leg")
	var probe result
	require.Eventually(t, func() bool {
		probe = send(false)
		return probe.code == http.StatusTooManyRequests
	}, 10*time.Second, 100*time.Millisecond,
		"after recovery the genuine over-limit must refuse again — 429 only from real over-limit (RED-03)")
	require.Equal(t, "1", probe.header.Get("Retry-After"), "the recovered 429 must keep Retry-After: 1")
	require.Contains(t, probe.body, "concurrency_exceeded")
	require.Contains(t, logs.String(), mwBreakerRecoverWarn,
		"the successful probe must close the breaker with a recovery WARN")

	// 5) Capacity: releasing the holder frees the shared slot immediately and
	// the count drains — nothing from the outage window lingers behind.
	close(relay)
	holder := <-holderDone
	require.Equal(t, http.StatusOK, holder.code, "the holder must complete normally after recovery")
	require.Equal(t, http.StatusOK, send(false).code, "capacity must be restored once the holder releases")
	require.Eventually(t, func() bool {
		return client.ZCard(ctx, key).Val() == 0 &&
			len(client.SMembers(ctx, "conc:registry").Val()) == 0
	}, 5*time.Second, 100*time.Millisecond,
		"every release must drain the shared count and the registry (D-17/D-18)")
}

// TestMiddleware_RecoveryFlushesStaleLeaseWithoutSecondary429 pins D-13 at the
// endpoint: a release that cannot reach Redis leaves its member behind (the
// outage's stale lease). After recovery the pending flush must clear it, so
// two genuinely in-flight requests can take BOTH limit-2 slots — if the stale
// lease lingered, the second request would 429, which is exactly the
// "stale lease fills the bucket → secondary 429 after recovery" failure D-13
// exists to eliminate. The transition itself (degrade → probe → recovered)
// must contain zero refusals, and the only 429 in the test is the genuine
// third concurrent request.
func TestMiddleware_RecoveryFlushesStaleLeaseWithoutSecondary429(t *testing.T) {
	server, client, _, logs := newOutageFixture(t)
	t.Setenv(mwBreakerCooldownEnvKey, "2")
	ctx := context.Background()
	const key = "conc:key:22:*"

	type result struct {
		code   int
		body   string
		header http.Header
	}
	reachedHandler := make(chan struct{}, 8)
	relayA := make(chan struct{})
	relayB := make(chan struct{})
	holderA := make(chan result, 1)
	holdRes := make(chan result, 4)

	r := gin.New()
	r.Use(ConcurrencyMiddleware())
	r.POST("/probe", func(c *gin.Context) {
		switch c.GetHeader("X-Test-Hold") {
		case "A":
			reachedHandler <- struct{}{}
			<-relayA
		case "B":
			reachedHandler <- struct{}{}
			<-relayB
		}
		c.Status(http.StatusOK)
	})

	send := func(hold string) result {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
		req.Header.Set("X-Test-User-Id", "22")
		req.Header.Set("X-Test-Token-Id", "22")
		req.Header.Set("X-Test-Model", "gpt-4o")
		req.Header.Set("X-Test-Token-Concurrency-Limit", "2") // two slots, one held → one free
		if hold != "" {
			req.Header.Set("X-Test-Hold", hold)
		}
		r.ServeHTTP(w, req)
		return result{code: w.Code, body: w.Body.String(), header: w.Header()}
	}

	// 1) One of the two slots is genuinely held.
	go func() { holderA <- send("A") }()
	<-reachedHandler

	// 2) Outage, then the holder completes: its release cannot reach Redis, so
	//    its member stays in the ZSET and lands in the pending buffer (D-13).
	server.Close()
	close(relayA)
	a := <-holderA
	require.Equal(t, http.StatusOK, a.code, "the holder must complete normally during the outage")
	require.Contains(t, logs.String(), mwDegradeWarn, "the failed release must WARN (D-13 path)")
	_, zErr := client.ZCard(ctx, key).Result()
	require.Error(t, zErr, "sanity: the outage must be a real transport failure")
	require.Equal(t, http.StatusOK, send("").code, "an outage request must still be served (RED-03)")

	// 3) Recovery: cooldown expires → probe succeeds → recovery WARN. No
	//    request may be refused on the way there (degrade and probe both serve).
	require.NoError(t, server.Restart(), "miniredis must come back for the recovery leg")
	var transitionRefusals []int
	require.Eventually(t, func() bool {
		got := send("")
		if got.code != http.StatusOK {
			transitionRefusals = append(transitionRefusals, got.code)
		}
		return strings.Contains(logs.String(), mwBreakerRecoverWarn)
	}, 10*time.Second, 100*time.Millisecond, "the probe must recover once the cooldown expires (D-11)")
	require.Empty(t, transitionRefusals,
		"the recovery transition must contain zero refusals — RED-03 holds across failover and recovery")

	// 4) The D-13 nail: both limit-2 slots must be usable by two genuinely
	//    in-flight requests. A lingering stale lease would make the second one
	//    429 — the secondary 429 D-13 exists to eliminate.
	go func() { holdRes <- send("B") }()
	go func() { holdRes <- send("B") }()
	for range 2 {
		select {
		case <-reachedHandler:
		case got := <-holdRes:
			close(relayB)
			require.Fail(t, "secondary 429 after recovery",
				"a request completed with status %d before both slots were admitted — the stale lease was not flushed (D-13)", got.code)
		case <-time.After(5 * time.Second):
			close(relayB)
			t.Fatal("both limit-2 requests must be admitted within 5s")
		}
	}

	// 5) The only legitimate 429: a third concurrent request at limit=2.
	thirdRes := make(chan result, 1)
	go func() { thirdRes <- send("B") }()
	select {
	case got := <-thirdRes:
		require.Equal(t, http.StatusTooManyRequests, got.code,
			"the third concurrent request must be genuinely over the limit (limit=2)")
		require.Equal(t, "1", got.header.Get("Retry-After"), "the 429 must keep Retry-After: 1")
		require.Contains(t, got.body, "concurrency_exceeded", "the 429 must carry the pinned error code")
	case <-reachedHandler:
		close(relayB)
		require.Fail(t, "over-issue after recovery",
			"a third request was admitted while two slots were already held — stale lease or over-issue")
	case <-time.After(5 * time.Second):
		close(relayB)
		t.Fatal("the third request must answer within 5s")
	}

	close(relayB)
	for range 2 {
		got := <-holdRes
		require.Equal(t, http.StatusOK, got.code, "both in-flight requests must complete with 200")
	}
	require.Eventually(t, func() bool {
		return client.ZCard(ctx, key).Val() == 0 &&
			len(client.SMembers(ctx, "conc:registry").Val()) == 0
	}, 5*time.Second, 100*time.Millisecond,
		"the shared count and registry must drain once every request finishes (D-17/D-18)")
}

// ---------------------------------------------------------------------------
// ③ sentinel failover soak (D-21/SC-5, manual layer): this drives the LIVE
// gateway (a separate process — not this test binary's handlers) and records
// the RED-03 evidence the automated layers cannot produce: a real failover
// window injected by the operator. Operator runbook, evidence paths and
// failure criteria: .planning/phases/02-token/02-10-soak-checklist.md
// ---------------------------------------------------------------------------

// TestMiddleware_SoakGatewayTraffic runs concurrent traffic against
// SOAK_BASE_URL for SOAK_DURATION_S and records every response (status,
// error code, latency, timestamp, whether the request fell inside the marked
// outage window) as JSONL evidence. It enforces exactly one RED-03 contract:
// no request completed while the outage flag was up may be a 429 — the window
// is delimited by flag files the operator creates around the failover
// (start right after `docker stop redis-master` returns, end once sentinel has
// promoted a new master), so in-window always means "Redis confirmed down",
// where fail-open must be serving every request.
//
//	SOAK_BASE_URL            e.g. http://127.0.0.1:30000   (required)
//	SOAK_API_KEY             sk-... token carrying a concurrency cap (required)
//	SOAK_MODEL               default gpt-4o
//	SOAK_DURATION_S          default 45
//	SOAK_CONCURRENCY         parallel workers, default 4
//	SOAK_INTERVAL_MS         pacing per worker, default 100
//	SOAK_STREAM              1 → stream:true long-flow requests, default 0
//	SOAK_OUTAGE_START_FLAG   default ./soak-outage.start
//	SOAK_OUTAGE_END_FLAG     default ./soak-outage.end
//	SOAK_EVIDENCE_FILE       JSONL evidence output path (optional)
func TestMiddleware_SoakGatewayTraffic(t *testing.T) {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("SOAK_BASE_URL")), "/")
	apiKey := strings.TrimSpace(os.Getenv("SOAK_API_KEY"))
	if baseURL == "" || apiKey == "" {
		t.Skip("SOAK_BASE_URL/SOAK_API_KEY not set — the ③ sentinel failover soak is a manual layer (D-21)")
	}
	envOr := func(key, fallback string) string {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
		return fallback
	}
	soakSeconds, err := strconv.Atoi(envOr("SOAK_DURATION_S", "45"))
	require.NoError(t, err, "SOAK_DURATION_S must be an integer")
	workers, err := strconv.Atoi(envOr("SOAK_CONCURRENCY", "4"))
	require.NoError(t, err, "SOAK_CONCURRENCY must be an integer")
	intervalMs, err := strconv.Atoi(envOr("SOAK_INTERVAL_MS", "100"))
	require.NoError(t, err, "SOAK_INTERVAL_MS must be an integer")
	model := envOr("SOAK_MODEL", "gpt-4o")
	stream := envOr("SOAK_STREAM", "0") == "1"
	startFlag := envOr("SOAK_OUTAGE_START_FLAG", "soak-outage.start")
	endFlag := envOr("SOAK_OUTAGE_END_FLAG", "soak-outage.end")
	evidencePath := strings.TrimSpace(os.Getenv("SOAK_EVIDENCE_FILE"))

	// Clean flag state from a previous run — the operator creates the start
	// flag only after this harness is already recording.
	_ = os.Remove(startFlag)
	_ = os.Remove(endFlag)

	payload, err := common.Marshal(map[string]any{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": "soak ping"}},
		"max_tokens": 16,
		"stream":     stream,
	})
	require.NoError(t, err, "the soak request body must marshal")

	type soakSample struct {
		At        string `json:"at"`
		Worker    int    `json:"worker"`
		Status    int    `json:"status"`
		ErrorCode string `json:"error_code,omitempty"`
		InWindow  bool   `json:"in_window"`
		ElapsedMs int64  `json:"elapsed_ms"`
	}
	var (
		mu        sync.Mutex
		samples   []soakSample
		startSeen bool
	)
	flagUp := func(path string) bool {
		_, statErr := os.Stat(path)
		return statErr == nil
	}
	httpClient := &http.Client{Timeout: 60 * time.Second}

	deadline := time.Now().Add(time.Duration(soakSeconds) * time.Second)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for time.Now().Before(deadline) {
				mu.Lock()
				inWindow := flagUp(startFlag) && !flagUp(endFlag)
				if flagUp(startFlag) {
					startSeen = true
				}
				mu.Unlock()

				sample := soakSample{
					At:       time.Now().Format(time.RFC3339Nano),
					Worker:   worker,
					InWindow: inWindow,
				}
				req, reqErr := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(payload))
				if reqErr != nil {
					sample.ErrorCode = "request_build_error"
				} else {
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Authorization", "Bearer "+apiKey)
					reqStart := time.Now()
					resp, doErr := httpClient.Do(req)
					if doErr != nil {
						sample.ErrorCode = "transport_error"
					} else {
						respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
						_ = resp.Body.Close()
						sample.Status = resp.StatusCode
						sample.ElapsedMs = time.Since(reqStart).Milliseconds()
						var parsed struct {
							Error struct {
								Code string `json:"code"`
								Type string `json:"type"`
							} `json:"error"`
						}
						if common.Unmarshal(respBody, &parsed) == nil {
							sample.ErrorCode = parsed.Error.Code
							if sample.ErrorCode == "" {
								sample.ErrorCode = parsed.Error.Type
							}
						}
					}
				}

				mu.Lock()
				samples = append(samples, sample)
				mu.Unlock()
				time.Sleep(time.Duration(intervalMs) * time.Millisecond)
			}
		}(w)
	}
	wg.Wait()

	require.NotEmpty(t, samples, "the soak must record at least one response")
	statusHist := map[int]int{}
	inWindow, inWindow429, transportErrs := 0, 0, 0
	for _, s := range samples {
		statusHist[s.Status]++
		if s.Status == 0 {
			transportErrs++
		}
		if s.InWindow {
			inWindow++
			if s.Status == http.StatusTooManyRequests {
				inWindow429++
			}
		}
	}
	t.Logf("soak summary: total=%d statuses=%v transport_errors=%d in_window=%d in_window_429=%d",
		len(samples), statusHist, transportErrs, inWindow, inWindow429)
	for _, s := range samples {
		if s.Status == http.StatusTooManyRequests {
			t.Logf("429 at %s (in_window=%v error_code=%q worker=%d)",
				s.At, s.InWindow, s.ErrorCode, s.Worker)
		}
	}

	if startSeen {
		require.Greater(t, inWindow, 0,
			"the outage flag went up but no request completed inside the window — coverage is missing")
		require.Zero(t, inWindow429,
			"RED-03: an outage-window request returned 429 (%d of %d in-window responses)",
			inWindow429, inWindow)
	} else {
		t.Log("no outage window was marked — record-only run; touch SOAK_OUTAGE_START_FLAG around the failover")
	}

	if evidencePath != "" {
		var sb strings.Builder
		for _, s := range samples {
			line, marshalErr := common.Marshal(s)
			require.NoError(t, marshalErr)
			sb.Write(line)
			sb.WriteByte('\n')
		}
		require.NoError(t, os.WriteFile(evidencePath, []byte(sb.String()), 0o644),
			"the soak evidence must be written for the acceptance record")
		t.Logf("evidence written to %s", evidencePath)
	}
}

// ---------------------------------------------------------------------------
// 网关内有界等待队列（bounded wait queue）
// ---------------------------------------------------------------------------

// TestMain 把整个 middleware 包的排队基线设为关闭。网关队列的出厂默认是开启
// （ratio=4 / cap=128 / maxWait=10s，产品已拍板），但本文件既有的用例钉的是
// "并发满 → 立即 429 + Retry-After: 1" 的即时拒绝契约；开着队列它们会先等
// 10 秒再 429。需要排队语义的用例用 configureConcurrencyQueue 显式打开，
// 并在 Cleanup 里恢复这条基线。
func TestMain(m *testing.M) {
	_ = setting.UpdateConcurrencyQueueOption(setting.ConcurrencyQueueEnabledKey, "false")
	os.Exit(m.Run())
}

// configureConcurrencyQueue 显式设置 4 个队列 option、清空排队占位，并在
// Cleanup 时恢复出厂默认 + "排队关闭" 的包基线。
func configureConcurrencyQueue(t *testing.T, enabled bool, ratio, depthCap, waitSeconds int) {
	t.Helper()
	apply := func(key, value string) {
		require.NoError(t, setting.UpdateConcurrencyQueueOption(key, value))
	}
	apply(setting.ConcurrencyQueueEnabledKey, strconv.FormatBool(enabled))
	apply(setting.ConcurrencyQueueDepthRatioKey, strconv.Itoa(ratio))
	apply(setting.ConcurrencyQueueDepthCapKey, strconv.Itoa(depthCap))
	apply(setting.ConcurrencyQueueMaxWaitSecondsKey, strconv.Itoa(waitSeconds))
	concurrency.DefaultWaitQueue().Reset()
	t.Cleanup(func() {
		for key, value := range setting.ConcurrencyQueueOptionDefaults() {
			apply(key, value)
		}
		apply(setting.ConcurrencyQueueEnabledKey, "false")
		concurrency.DefaultWaitQueue().Reset()
	})
}

// scriptedLimiter 先按脚本拒绝前 N 次 AcquireBatch，再转交内层 stub。
// 这样 "槽位先忙后空" 的时序是确定的，不依赖 goroutine 竞速。
type scriptedLimiter struct {
	inner    concurrencyLimiterAdapter
	mu       sync.Mutex
	failures int
	attempts int
}

func (s *scriptedLimiter) AcquireBatch(scopes []concurrency.ScopeLimit) (func(), error) {
	s.mu.Lock()
	s.attempts++
	if s.failures > 0 {
		s.failures--
		s.mu.Unlock()
		return nil, concurrency.ErrConcurrencyExceeded
	}
	s.mu.Unlock()
	return s.inner.AcquireBatch(scopes)
}

func (s *scriptedLimiter) attemptCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

// 队列等待后放行：第一次 AcquireBatch 被拒 → 入队 → 轮询重试成功 → 200，
// 且队列占位在请求结束后归零。
func TestMiddleware_QueueWaitsThenAdmits(t *testing.T) {
	configureConcurrencyQueue(t, true, 4, 128, 5)
	scripted := &scriptedLimiter{inner: newStubLimiter(), failures: 1}
	resetMiddlewareLimiter(t, scripted)
	r := newTestEngine()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
	req.Header.Set("X-Test-User-Id", "11")
	req.Header.Set("X-Test-Token-Id", "11")
	req.Header.Set("X-Test-Model", "gpt-4o")
	req.Header.Set("X-Test-Token-Concurrency-By-Model", "gpt-4o=1")

	start := time.Now()
	r.ServeHTTP(w, req)
	elapsed := time.Since(start)

	assert.Equal(t, http.StatusOK, w.Code, "a queued request must be admitted once a slot frees")
	assert.GreaterOrEqual(t, scripted.attemptCount(), 2, "the gate must have retried the batch acquire")
	assert.GreaterOrEqual(t, elapsed, 50*time.Millisecond,
		"the first retry waits out the 50ms base interval")
	assert.Equal(t, 0, concurrency.DefaultWaitQueue().Total(),
		"the queue slot must be released when the request completes")
}

// 排队等待埋点：排队命中时采集载体里的排队等待大于 0，直放时等于 0。
// 这是"网关自己造成、且可配置的那段等待"第一次被记录下来。
func TestMiddleware_QueueWaitIsRecordedInRequestTiming(t *testing.T) {
	configureConcurrencyQueue(t, true, 4, 128, 5)

	run := func(t *testing.T, limiter concurrencyLimiterAdapter) *relaycommon.RequestTiming {
		t.Helper()
		resetMiddlewareLimiter(t, limiter)
		var captured *relaycommon.RequestTiming
		r := gin.New()
		r.Use(ConcurrencyMiddleware())
		r.POST("/probe", func(c *gin.Context) {
			captured = relaycommon.RequestTimingOf(c)
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
		req.Header.Set("X-Test-User-Id", "21")
		req.Header.Set("X-Test-Token-Id", "21")
		req.Header.Set("X-Test-Model", "gpt-4o")
		req.Header.Set("X-Test-Token-Concurrency-By-Model", "gpt-4o=1")
		r.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		require.NotNil(t, captured, "the request must carry a timing collector")
		return captured
	}

	queued := run(t, &scriptedLimiter{inner: newStubLimiter(), failures: 1})
	assert.Greater(t, queued.QueueWaitMs.Load(), int64(0),
		"a queued request must record the milliseconds it waited for a slot")

	direct := run(t, newStubLimiter())
	assert.EqualValues(t, 0, direct.QueueWaitMs.Load(),
		"a directly admitted request records no queue wait")
}

// 队列满：深度 min(ratio*limit, cap) 已被占满时直接 429，不进入等待循环。
func TestMiddleware_QueueFullRejectsImmediately(t *testing.T) {
	configureConcurrencyQueue(t, true, 1, 128, 5)
	scripted := &scriptedLimiter{inner: newStubLimiter(), failures: 1}
	resetMiddlewareLimiter(t, scripted)

	// 预占这条请求将要占用的 scope：ratio=1、两个 capped scope 的 limit 都是 1，
	// 所以队列深度是 1，预占这一次即满。
	scopes := buildScopes(concurrencySubject{
		userID:    13,
		tokenID:   13,
		model:     "gpt-4o",
		limits:    concurrency.ParseTokenConcurrencyLimits(1, "gpt-4o=1"),
		userLimit: 0,
	})
	leave, ok := concurrency.DefaultWaitQueue().TryEnter(scopes, 1, 128)
	require.True(t, ok, "the pre-fill must fit the depth it is about to exhaust")
	t.Cleanup(leave)

	rejectionsBefore := concurrency.RejectionCount()
	r := newTestEngine()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
	req.Header.Set("X-Test-User-Id", "13")
	req.Header.Set("X-Test-Token-Id", "13")
	req.Header.Set("X-Test-Model", "gpt-4o")
	req.Header.Set("X-Test-Token-Concurrency-By-Model", "gpt-4o=1")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code, "a full queue must shed immediately")
	assert.Equal(t, 1, scripted.attemptCount(), "a full queue must reject without retrying")
	assert.Equal(t, "1", w.Header().Get("Retry-After"), "429 must carry Retry-After: 1")
	assert.Equal(t, rejectionsBefore+1, concurrency.RejectionCount())

	var body map[string]map[string]string
	require.NoError(t, json.Unmarshal([]byte(w.Body.String()), &body))
	require.NotNil(t, body["error"])
	assert.Equal(t, "concurrency_exceeded", body["error"]["type"])
	assert.Equal(t, "concurrency_exceeded", body["error"]["code"])
	assert.Equal(t, "The concurrency wait queue is full. Please retry later.",
		body["error"]["message"],
		"queue-full 429 must carry its own distinguishable message (06)")
}

// 等待超时：max_wait_seconds=1 且槽位一直不空 → 满 1 秒后 429，占位归零。
func TestMiddleware_QueueWaitTimeoutRejects(t *testing.T) {
	configureConcurrencyQueue(t, true, 4, 128, 1)
	scripted := &scriptedLimiter{inner: newStubLimiter(), failures: 1 << 30}
	resetMiddlewareLimiter(t, scripted)
	r := newTestEngine()

	rejectionsBefore := concurrency.RejectionCount()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
	req.Header.Set("X-Test-User-Id", "14")
	req.Header.Set("X-Test-Token-Id", "14")
	req.Header.Set("X-Test-Model", "gpt-4o")
	req.Header.Set("X-Test-Token-Concurrency-By-Model", "gpt-4o=1")

	start := time.Now()
	r.ServeHTTP(w, req)
	elapsed := time.Since(start)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.GreaterOrEqual(t, elapsed, time.Second,
		"the request must wait up to queue_max_wait_seconds before shedding")
	assert.GreaterOrEqual(t, scripted.attemptCount(), 2, "the gate must have retried while waiting")
	assert.LessOrEqual(t, scripted.attemptCount(), 30,
		"the wait loop must back off between retries instead of busy-spinning")
	assert.Equal(t, 0, concurrency.DefaultWaitQueue().Total(), "a timed-out waiter must free its slot")
	assert.Equal(t, rejectionsBefore+1, concurrency.RejectionCount())
	assert.Equal(t, "1", w.Header().Get("Retry-After"))

	var body map[string]map[string]string
	require.NoError(t, json.Unmarshal([]byte(w.Body.String()), &body))
	require.NotNil(t, body["error"])
	assert.Equal(t, "concurrency_exceeded", body["error"]["type"])
	assert.Equal(t, "concurrency_exceeded", body["error"]["code"])
	assert.Equal(t, "Timed out waiting for an available concurrency slot. Please retry after the current requests finish.",
		body["error"]["message"],
		"queue-wait-timeout 429 must carry its own distinguishable message (06)")
}

// 配置关闭：走既有路径，第一次 AcquireBatch 失败就 429，不入队、不重试。
func TestMiddleware_QueueDisabledRejectsImmediately(t *testing.T) {
	configureConcurrencyQueue(t, false, 4, 128, 5)
	scripted := &scriptedLimiter{inner: newStubLimiter(), failures: 1}
	resetMiddlewareLimiter(t, scripted)
	r := newTestEngine()

	rejectionsBefore := concurrency.RejectionCount()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
	req.Header.Set("X-Test-User-Id", "15")
	req.Header.Set("X-Test-Token-Id", "15")
	req.Header.Set("X-Test-Model", "gpt-4o")
	req.Header.Set("X-Test-Token-Concurrency-By-Model", "gpt-4o=1")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, 1, scripted.attemptCount(), "a disabled queue must not retry")
	assert.Equal(t, 0, concurrency.DefaultWaitQueue().Total(), "a disabled queue must never occupy a slot")
	assert.Equal(t, rejectionsBefore+1, concurrency.RejectionCount())
	assert.Equal(t, "1", w.Header().Get("Retry-After"))

	var body map[string]map[string]string
	require.NoError(t, json.Unmarshal([]byte(w.Body.String()), &body))
	require.NotNil(t, body["error"])
	assert.Equal(t, "concurrency_exceeded", body["error"]["type"])
	assert.Equal(t, "concurrency_exceeded", body["error"]["code"])
	assert.Equal(t, "Concurrency limit exceeded for your account. Please retry after the current requests finish.",
		body["error"]["message"],
		"the immediate over-limit reject keeps the verbatim CNT-01 message (06)")
}

// 客户端断开：等待中的请求立刻退出等待、释放排队位并写 429。
func TestMiddleware_ClientDisconnectFreesQueueSlot(t *testing.T) {
	configureConcurrencyQueue(t, true, 4, 128, 5)
	scripted := &scriptedLimiter{inner: newStubLimiter(), failures: 1 << 30}
	resetMiddlewareLimiter(t, scripted)
	r := newTestEngine()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
	req = req.WithContext(ctx)
	req.Header.Set("X-Test-User-Id", "16")
	req.Header.Set("X-Test-Token-Id", "16")
	req.Header.Set("X-Test-Model", "gpt-4o")
	req.Header.Set("X-Test-Token-Concurrency-By-Model", "gpt-4o=1")

	rejectionsBefore := concurrency.RejectionCount()
	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		done <- w.Code
	}()

	require.Eventually(t, func() bool {
		return concurrency.DefaultWaitQueue().Total() > 0
	}, 2*time.Second, 5*time.Millisecond, "the request must hold a queue slot while waiting")

	cancel()

	select {
	case code := <-done:
		assert.Equal(t, http.StatusTooManyRequests, code)
	case <-time.After(3 * time.Second):
		t.Fatal("the middleware must return once the client context is cancelled")
	}
	assert.Equal(t, 0, concurrency.DefaultWaitQueue().Total(),
		"a cancelled waiter must free its queue slot")
	assert.Equal(t, rejectionsBefore+1, concurrency.RejectionCount())
}

// ---------------------------------------------------------------------------
// 三条 429 拒绝路径的异常原因日志
//
// 三条拒绝路径此前一行日志都不写，运营既看不到"某个请求排了 N 秒最后被拒"，
// 也分不清是排队没开、队列满了、等待超时还是客户端自己断开。这里钉住契约：
// 每条路径各写一行含异常原因的系统日志，带已等待时长、当时队列配置与拒绝
// 对象标识（user id / token id / 模型名，绝不含任何凭据）。
// ---------------------------------------------------------------------------

// resetConcurrencyRejectionLogs 清空拒绝日志的节流状态，让每个用例的日志行
// 断言不受同包其它用例写入量的影响。
func resetConcurrencyRejectionLogs(t *testing.T) {
	t.Helper()
	concurrencyRejectionLogs.reset()
	t.Cleanup(func() { concurrencyRejectionLogs.reset() })
}

// rejectionLogLine 从系统日志缓冲里取出"并发拒绝"那一行（同一缓冲里可能还
// 有其它来源的日志行）。
func rejectionLogLine(t *testing.T, logs *syncBuffer) string {
	t.Helper()
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, "concurrency: request rejected") {
			return line
		}
	}
	t.Fatalf("no concurrency rejection log line found in:\n%s", logs.String())
	return ""
}

// waitedMsFromLog 取出日志行里的 waited_ms 数值。
func waitedMsFromLog(t *testing.T, line string) int64 {
	t.Helper()
	match := regexp.MustCompile(`waited_ms=(\d+)`).FindStringSubmatch(line)
	require.Len(t, match, 2, "the rejection log must carry waited_ms: %s", line)
	value, err := strconv.ParseInt(match[1], 10, 64)
	require.NoError(t, err)
	return value
}

func TestMiddleware_RejectionPathsWriteReasonLog(t *testing.T) {
	t.Run("queue disabled rejects immediately and logs the reason", func(t *testing.T) {
		configureConcurrencyQueue(t, false, 4, 128, 5)
		resetConcurrencyRejectionLogs(t)
		logs := captureSysErrorWriter(t)

		scripted := &scriptedLimiter{inner: newStubLimiter(), failures: 1}
		resetMiddlewareLimiter(t, scripted)
		r := newTestEngine()

		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
		req.Header.Set("X-Test-User-Id", "15")
		req.Header.Set("X-Test-Token-Id", "15")
		req.Header.Set("X-Test-Model", "gpt-4o")
		req.Header.Set("X-Test-Token-Concurrency-By-Model", "gpt-4o=1")
		r.ServeHTTP(w, req)

		require.Equal(t, http.StatusTooManyRequests, w.Code)
		line := rejectionLogLine(t, logs)
		assert.Contains(t, line, "reason=queue_disabled",
			"the immediate reject must say the queue was effectively off")
		assert.EqualValues(t, 0, waitedMsFromLog(t, line),
			"an immediate reject waited for nothing")
		assert.Contains(t, line, "queue_enabled=false")
		assert.Contains(t, line, "queue_ratio=4")
		assert.Contains(t, line, "queue_cap=128")
		assert.Contains(t, line, "queue_max_wait_ms=5000")
		assert.Contains(t, line, "user_id=15")
		assert.Contains(t, line, "token_id=15")
		assert.Contains(t, line, "model=gpt-4o")
	})

	t.Run("queue full rejects immediately and logs the reason", func(t *testing.T) {
		configureConcurrencyQueue(t, true, 1, 128, 5)
		resetConcurrencyRejectionLogs(t)
		logs := captureSysErrorWriter(t)

		scripted := &scriptedLimiter{inner: newStubLimiter(), failures: 1}
		resetMiddlewareLimiter(t, scripted)

		// 预占这条请求将要占用的 scope：ratio=1、capped scope 的 limit 都是 1，
		// 队列深度是 1，预占这一次即满。
		scopes := buildScopes(concurrencySubject{
			userID:    13,
			tokenID:   13,
			model:     "gpt-4o",
			limits:    concurrency.ParseTokenConcurrencyLimits(1, "gpt-4o=1"),
			userLimit: 0,
		})
		leave, ok := concurrency.DefaultWaitQueue().TryEnter(scopes, 1, 128)
		require.True(t, ok, "the pre-fill must fit the depth it is about to exhaust")
		t.Cleanup(leave)

		r := newTestEngine()
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
		req.Header.Set("X-Test-User-Id", "13")
		req.Header.Set("X-Test-Token-Id", "13")
		req.Header.Set("X-Test-Model", "gpt-4o")
		req.Header.Set("X-Test-Token-Concurrency-By-Model", "gpt-4o=1")
		r.ServeHTTP(w, req)

		require.Equal(t, http.StatusTooManyRequests, w.Code)
		line := rejectionLogLine(t, logs)
		assert.Contains(t, line, "reason=queue_full",
			"a full queue must say so in the log")
		assert.EqualValues(t, 0, waitedMsFromLog(t, line),
			"a queue-full reject waited for nothing")
		assert.Contains(t, line, "queue_enabled=true")
		assert.Contains(t, line, "queue_ratio=1")
		assert.Contains(t, line, "queue_cap=128")
		assert.Contains(t, line, "queue_max_wait_ms=5000")
		assert.Contains(t, line, "user_id=13")
		assert.Contains(t, line, "token_id=13")
		assert.Contains(t, line, "model=gpt-4o")
	})

	t.Run("queue wait timeout logs the reason and the waited duration", func(t *testing.T) {
		configureConcurrencyQueue(t, true, 4, 128, 1)
		resetConcurrencyRejectionLogs(t)
		logs := captureSysErrorWriter(t)

		scripted := &scriptedLimiter{inner: newStubLimiter(), failures: 1 << 30}
		resetMiddlewareLimiter(t, scripted)
		r := newTestEngine()

		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
		req.Header.Set("X-Test-User-Id", "14")
		req.Header.Set("X-Test-Token-Id", "14")
		req.Header.Set("X-Test-Model", "gpt-4o")
		req.Header.Set("X-Test-Token-Concurrency-By-Model", "gpt-4o=1")
		r.ServeHTTP(w, req)

		require.Equal(t, http.StatusTooManyRequests, w.Code)
		line := rejectionLogLine(t, logs)
		assert.Contains(t, line, "reason=queue_timeout",
			"a waited-out request must be distinguishable from a client disconnect")
		assert.GreaterOrEqual(t, waitedMsFromLog(t, line), int64(1000),
			"the log must carry how long the request actually waited")
		assert.Contains(t, line, "queue_max_wait_ms=1000")
		assert.Contains(t, line, "user_id=14")
		assert.Contains(t, line, "token_id=14")
		assert.Contains(t, line, "model=gpt-4o")
	})

	t.Run("client disconnect logs its own reason", func(t *testing.T) {
		configureConcurrencyQueue(t, true, 4, 128, 5)
		resetConcurrencyRejectionLogs(t)
		logs := captureSysErrorWriter(t)

		scripted := &scriptedLimiter{inner: newStubLimiter(), failures: 1 << 30}
		resetMiddlewareLimiter(t, scripted)
		r := newTestEngine()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
		req = req.WithContext(ctx)
		req.Header.Set("X-Test-User-Id", "16")
		req.Header.Set("X-Test-Token-Id", "16")
		req.Header.Set("X-Test-Model", "gpt-4o")
		req.Header.Set("X-Test-Token-Concurrency-By-Model", "gpt-4o=1")

		done := make(chan int, 1)
		go func() {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			done <- w.Code
		}()
		require.Eventually(t, func() bool {
			return concurrency.DefaultWaitQueue().Total() > 0
		}, 2*time.Second, 5*time.Millisecond, "the request must hold a queue slot while waiting")
		cancel()
		require.Equal(t, http.StatusTooManyRequests, <-done)

		line := rejectionLogLine(t, logs)
		assert.Contains(t, line, "reason=client_disconnected",
			"a client that walked away must not be logged as a wait timeout")
		assert.Contains(t, line, "user_id=16")
		assert.Contains(t, line, "token_id=16")
		assert.Contains(t, line, "model=gpt-4o")
	})
}

// 拒绝日志的节流：同一个原因在同一个时间窗内写满上限后，超出的拒绝只计数
// 不写行；下一行必须带上被抑制的条数（拒绝事件不丢，日志量有上界）。
func TestMiddleware_RejectionLogThrottleSuppressesFlood(t *testing.T) {
	configureConcurrencyQueue(t, false, 4, 128, 5)
	resetConcurrencyRejectionLogs(t)
	logs := captureSysErrorWriter(t)

	// 每次 AcquireBatch 都被拒 → 每个请求都走"排队等效关闭即拒"这条路径。
	scripted := &scriptedLimiter{inner: newStubLimiter(), failures: 1 << 30}
	resetMiddlewareLimiter(t, scripted)
	r := newTestEngine()

	send := func() {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/probe", nil)
		req.Header.Set("X-Test-User-Id", "19")
		req.Header.Set("X-Test-Token-Id", "19")
		req.Header.Set("X-Test-Model", "gpt-4o")
		req.Header.Set("X-Test-Token-Concurrency-By-Model", "gpt-4o=1")
		r.ServeHTTP(w, req)
	}

	// 超过一个时间窗的写入量：前 N 行照写，之后被抑制。
	for range concurrencyRejectionLogMaxPerWindow + 5 {
		send()
	}
	lines := strings.Count(logs.String(), "concurrency: request rejected")
	assert.LessOrEqual(t, lines, concurrencyRejectionLogMaxPerWindow,
		"the rejection log must stay bounded inside one window")

	// 下一个时间窗的第一行必须带上被抑制的条数。
	time.Sleep(concurrencyRejectionLogWindow + 50*time.Millisecond)
	send()
	assert.Contains(t, logs.String(), "suppressed=5",
		"the next line must report how many rejections were suppressed")
}
