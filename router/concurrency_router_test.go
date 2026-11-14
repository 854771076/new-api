package router

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/concurrency"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// End-to-end concurrency gate tests. The engine replicates the real chain from
// router/relay-router.go (TokenAuth → ModelRequestRateLimit → Distribute →
// ConcurrencyMiddleware) with a blocking terminal handler replacing
// controller.Relay (Approach A: the 429 contract and limit injection all live
// inside the middleware chain, so no upstream is needed and the test stays
// deterministic).

var (
	concurrencyRouterLimiterOnce sync.Once
	concurrencyRouterLimiter     *concurrency.MemoryLimiter
)

// installConcurrencyRouterLimiter wires the process-wide limiter exactly once.
// SetConcurrencyLimiter is a sync.Once (research F22): later calls are swallowed
// and never swap the implementation, so every test shares this one MemoryLimiter
// and isolates itself via distinct token/user IDs (distinct scope keys).
func installConcurrencyRouterLimiter(t *testing.T) *concurrency.MemoryLimiter {
	t.Helper()
	// 这些端到端用例钉的是 "并发满 → 立即 429 + Retry-After: 1" 的即时拒绝
	// 契约，而网关等待队列出厂默认开启（ratio=4 / maxWait=10s）会让被拒请求
	// 先排队等满 10 秒才 429。这里把排队基线关掉，排队语义由
	// middleware/concurrency_test.go 的专用用例覆盖。
	require.NoError(t, setting.UpdateConcurrencyQueueOption(setting.ConcurrencyQueueEnabledKey, "false"))
	concurrencyRouterLimiterOnce.Do(func() {
		concurrencyRouterLimiter = concurrency.NewMemoryLimiter()
		middleware.SetConcurrencyLimiter(middleware.WrapConcurrencyLimiter(concurrencyRouterLimiter))
	})
	require.NotNil(t, concurrencyRouterLimiter)
	return concurrencyRouterLimiter
}

// seedConcurrencyRelayFixtures creates the user + token (with a global
// concurrency cap) + channel + ability rows the replicated chain needs:
// TokenAuth resolves the token, Distribute selects the channel via the
// ability table (group "default"), and ConcurrencyMiddleware reads the cap
// from the token row through the request context.
func seedConcurrencyRelayFixtures(t *testing.T, userID, tokenID, channelID int, tokenKey string, concurrencyLimit int, models ...string) {
	t.Helper()
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}))
	require.NoError(t, model.DB.Create(&model.User{
		Id:       userID,
		Username: fmt.Sprintf("conc-e2e-user-%d", userID),
		// users.aff_code carries a unique index, so every seeded user needs
		// its own value — otherwise a second seeded user in the same test
		// fails on the empty-string UNIQUE constraint.
		AffCode:  fmt.Sprintf("conc%d", userID),
		Password: "password",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    1000,
	}).Error)
	require.NoError(t, model.DB.Create(&model.Token{
		Id:               tokenID,
		UserId:           userID,
		Key:              tokenKey,
		Name:             fmt.Sprintf("conc-e2e-token-%d", tokenID),
		Status:           common.TokenStatusEnabled,
		ExpiredTime:      -1,
		UnlimitedQuota:   true,
		ConcurrencyLimit: concurrencyLimit,
	}).Error)
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:     channelID,
		Type:   constant.ChannelTypeOpenAI,
		Key:    fmt.Sprintf("conc-e2e-channel-key-%d", channelID),
		Name:   fmt.Sprintf("conc-e2e-channel-%d", channelID),
		Status: common.ChannelStatusEnabled,
		Group:  "default",
		Models: strings.Join(models, ","),
	}).Error)
	for _, m := range models {
		require.NoError(t, model.DB.Create(&model.Ability{
			Group:     "default",
			Model:     m,
			ChannelId: channelID,
			Enabled:   true,
		}).Error)
	}
}

// newConcurrencyRelayEngine replicates the relay chain from
// router/relay-router.go (see SetRelayRouter's relayV1Router/httpRouter
// composition) with the terminal handler supplied by the caller.
func newConcurrencyRelayEngine(t *testing.T, handler gin.HandlerFunc) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.TokenAuth())
	engine.Use(middleware.ModelRequestRateLimit())
	engine.Use(middleware.Distribute())
	engine.Use(middleware.ConcurrencyMiddleware())
	engine.POST("/v1/chat/completions", handler)
	return engine
}

type concurrencyRelayResult struct {
	code   int
	body   string
	header http.Header
}

func postConcurrencyRelay(engine *gin.Engine, path, tokenKey, body string) concurrencyRelayResult {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokenKey)
	engine.ServeHTTP(w, req)
	return concurrencyRelayResult{code: w.Code, body: w.Body.String(), header: w.Header()}
}

// TestConcurrency_TokenGlobalLimitEnforced pins the ENF-01/CNT-01 contract
// end-to-end through the real middleware chain: a token with
// concurrency_limit=1 blocks its second in-flight request with 429 +
// Retry-After: 1 + the verbatim concurrency_exceeded body, the held request
// completes untouched, and the slot is released afterwards.
func TestConcurrency_TokenGlobalLimitEnforced(t *testing.T) {
	setupRelayRouterTestDB(t)
	installConcurrencyRouterLimiter(t)

	const (
		userID    = 9101
		tokenID   = 9101
		channelID = 9101
		tokenKey  = "conce2ekey9101"
		modelName = "gpt-4o"
	)
	seedConcurrencyRelayFixtures(t, userID, tokenID, channelID, tokenKey, 1, modelName)

	entered := make(chan struct{}, 4)
	relay := make(chan struct{})
	engine := newConcurrencyRelayEngine(t, func(c *gin.Context) {
		entered <- struct{}{}
		<-relay
		c.Status(http.StatusOK)
	})

	resCh := make(chan concurrencyRelayResult, 4)
	body := `{"model":"` + modelName + `"}`
	send := func() {
		resCh <- postConcurrencyRelay(engine, "/v1/chat/completions", tokenKey, body)
	}

	// 1) First request passes the gate and blocks in the terminal handler
	// while holding its slots.
	go send()
	<-entered

	// 2) Second request on the same token must be rejected at the gate.
	go send()
	var rejected concurrencyRelayResult
	select {
	case rejected = <-resCh:
		// Rejected before the terminal handler — the enforced path.
	case <-entered:
		// The second request reached the terminal handler: no gate fired.
		close(relay)
		<-resCh
		<-resCh
		require.Fail(t, "second request was not rejected by the concurrency gate",
			"expected HTTP 429 concurrency_exceeded while the first request was in flight")
		return
	}
	require.Equal(t, http.StatusTooManyRequests, rejected.code)
	require.Equal(t, "1", rejected.header.Get("Retry-After"))
	var payload map[string]map[string]string
	require.NoError(t, common.Unmarshal([]byte(rejected.body), &payload))
	require.NotNil(t, payload["error"])
	assert.Equal(t, "concurrency_exceeded", payload["error"]["type"])
	assert.Equal(t, "concurrency_exceeded", payload["error"]["code"])
	assert.Equal(t, "Concurrency limit exceeded for your account. Please retry after the current requests finish.",
		payload["error"]["message"])

	// 3) Release the blocked first request: it must complete 200 (held slot
	// unaffected by the rejected sibling).
	close(relay)
	firstResult := <-resCh
	assert.Equal(t, http.StatusOK, firstResult.code)

	// 4) A third request passes: the slot was released by the completed first
	// request (the relay channel is already closed, so the handler returns
	// immediately).
	third := postConcurrencyRelay(engine, "/v1/chat/completions", tokenKey, body)
	assert.Equal(t, http.StatusOK, third.code)
}

// TestConcurrency_UserAggregateAcrossTokens pins the USR-01 aggregate user cap
// end-to-end through the real chain: the same user owns TWO tokens that are
// each individually unlimited, and the user row carries concurrency_limit=1.
// While the first token's request is in flight, the second token's request
// must be rejected with 429 + Retry-After: 1 — proof that the cap binds to the
// user:<id>:* bucket, not to a single token. A second user with its own cap of
// 1 must be unaffected (no cross-tenant drift).
func TestConcurrency_UserAggregateAcrossTokens(t *testing.T) {
	setupRelayRouterTestDB(t)
	installConcurrencyRouterLimiter(t)

	const (
		userID    = 9301
		tokenA    = 9301
		tokenB    = 9302
		channelID = 9301
		keyA      = "conce2ekey9301a"
		keyB      = "conce2ekey9301b"
		modelName = "gpt-4o"
	)
	seedConcurrencyRelayFixtures(t, userID, tokenA, channelID, keyA, 0, modelName)
	// Second token of the SAME user — also individually unlimited (0 = no cap),
	// so only the aggregate user bucket can reject it.
	require.NoError(t, model.DB.Create(&model.Token{
		Id:               tokenB,
		UserId:           userID,
		Key:              keyB,
		Name:             "conc-e2e-token-9302",
		Status:           common.TokenStatusEnabled,
		ExpiredTime:      -1,
		UnlimitedQuota:   true,
		ConcurrencyLimit: 0,
	}).Error)
	// The user axis cap itself: one in-flight request for user 9301 across all
	// of its tokens (seeded as User.ConcurrencyLimit=1, decoded from the DB
	// because Redis is off in this harness).
	require.NoError(t, model.DB.Model(&model.User{}).
		Where("id = ?", userID).Update("concurrency_limit", 1).Error)

	// Unrelated user with its own cap of 1 and an empty bucket — must not be
	// throttled by user 9301's cap.
	const (
		otherUser  = 9401
		otherToken = 9401
		otherChan  = 9401
		otherKey   = "conce2ekey9401"
	)
	seedConcurrencyRelayFixtures(t, otherUser, otherToken, otherChan, otherKey, 0, modelName)
	require.NoError(t, model.DB.Model(&model.User{}).
		Where("id = ?", otherUser).Update("concurrency_limit", 1).Error)

	entered := make(chan struct{}, 4)
	relay := make(chan struct{})
	engine := newConcurrencyRelayEngine(t, func(c *gin.Context) {
		entered <- struct{}{}
		<-relay
		c.Status(http.StatusOK)
	})

	resCh := make(chan concurrencyRelayResult, 8)
	body := `{"model":"` + modelName + `"}`
	send := func(key string) {
		resCh <- postConcurrencyRelay(engine, "/v1/chat/completions", key, body)
	}

	// 1) First token's request passes the gate and holds the aggregate slot.
	go send(keyA)
	<-entered

	// 2) The SAME user's second token must be rejected by the aggregate cap.
	go send(keyB)
	select {
	case rejected := <-resCh:
		require.Equal(t, http.StatusTooManyRequests, rejected.code)
		require.Equal(t, "1", rejected.header.Get("Retry-After"),
			"429 must carry Retry-After: 1")
		var payload map[string]map[string]string
		require.NoError(t, common.Unmarshal([]byte(rejected.body), &payload))
		require.NotNil(t, payload["error"])
		assert.Equal(t, "concurrency_exceeded", payload["error"]["type"])
		assert.Equal(t, "concurrency_exceeded", payload["error"]["code"])
	case <-entered:
		close(relay)
		<-resCh
		<-resCh
		require.Fail(t, "second token of the same user was not rejected by the user aggregate cap",
			"expected HTTP 429 concurrency_exceeded while the first token's request was in flight")
		return
	}

	// 3) The unrelated user must pass the gate while user 9301 still holds its
	// cap — a user cap never throttles another user's requests.
	go send(otherKey)
	select {
	case <-entered:
		// Expected: user 9401 is counted in its own empty bucket.
	case rejected := <-resCh:
		close(relay)
		<-resCh
		require.Fail(t, "user 9301's cap leaked into user 9401's request",
			"expected HTTP 200 for the unrelated user, got %d", rejected.code)
		return
	}

	// 4) Release everything: both held requests complete 200 and the aggregate
	// slot is freed for the next request.
	close(relay)
	assert.Equal(t, http.StatusOK, (<-resCh).code, "held request of user 9301 must be unaffected")
	assert.Equal(t, http.StatusOK, (<-resCh).code, "held request of user 9401 must complete")

	next := postConcurrencyRelay(engine, "/v1/chat/completions", keyB, body)
	assert.Equal(t, http.StatusOK, next.code, "slot must be free once the held request finished")
}

// TestConcurrency_GeminiPathEnforced pins the Gemini relay group wiring
// (orchestrator discretion 1): /v1beta/models/*path must be gated the same way
// as httpRouter. The wiring itself is asserted at the source level because the
// behavioral half replicates the chain (Approach A — the real terminal handler
// is controller.Relay, which cannot block deterministically without an
// upstream); the behavior half then proves the gated chain enforces the 429
// contract for the Gemini path shape.
func TestConcurrency_GeminiPathEnforced(t *testing.T) {
	setupRelayRouterTestDB(t)
	installConcurrencyRouterLimiter(t)

	// 1) Production wiring: the relayGeminiRouter group must mount
	// ConcurrencyMiddleware, and it must come after Distribute().
	src, err := os.ReadFile("relay-router.go")
	require.NoError(t, err, "router/relay-router.go must be readable from the router package dir")
	text := string(src)
	start := strings.Index(text, "relayGeminiRouter := router.Group")
	require.GreaterOrEqual(t, start, 0, "relayGeminiRouter group must exist in relay-router.go")
	end := strings.Index(text[start:], "func registerMjRouterGroup")
	require.Greater(t, end, 0, "relayGeminiRouter block must be inside SetRelayRouter")
	block := text[start : start+end]
	distIdx := strings.Index(block, "relayGeminiRouter.Use(middleware.Distribute())")
	require.GreaterOrEqual(t, distIdx, 0, "relayGeminiRouter must keep Distribute()")
	gateIdx := strings.Index(block, "relayGeminiRouter.Use(middleware.ConcurrencyMiddleware())")
	require.GreaterOrEqual(t, gateIdx, 0,
		"relayGeminiRouter must mount ConcurrencyMiddleware (same shape as httpRouter)")
	require.Greater(t, gateIdx, distIdx, "ConcurrencyMiddleware must be mounted after Distribute()")

	// 2) Behavior through the replicated chain: a token capped at 1 blocks
	// its second in-flight Gemini request with 429 + Retry-After: 1.
	const (
		userID    = 9201
		tokenID   = 9201
		channelID = 9201
		tokenKey  = "conce2ekey9201"
		modelName = "gemini-2.0-flash"
	)
	seedConcurrencyRelayFixtures(t, userID, tokenID, channelID, tokenKey, 1, modelName)

	entered := make(chan struct{}, 4)
	relay := make(chan struct{})
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.TokenAuth())
	engine.Use(middleware.ModelRequestRateLimit())
	engine.Use(middleware.Distribute())
	engine.Use(middleware.ConcurrencyMiddleware())
	engine.POST("/v1beta/models/*path", func(c *gin.Context) {
		entered <- struct{}{}
		<-relay
		c.Status(http.StatusOK)
	})

	path := "/v1beta/models/" + modelName + ":generateContent"
	body := `{"contents":[]}`
	resCh := make(chan concurrencyRelayResult, 4)
	send := func() {
		resCh <- postConcurrencyRelay(engine, path, tokenKey, body)
	}

	go send()
	<-entered // first Gemini request holds its slots

	go send()
	var rejected concurrencyRelayResult
	select {
	case rejected = <-resCh:
	case <-entered:
		close(relay)
		<-resCh
		<-resCh
		require.Fail(t, "second gemini-path request was not rejected by the concurrency gate")
		return
	}
	require.Equal(t, http.StatusTooManyRequests, rejected.code)
	require.Equal(t, "1", rejected.header.Get("Retry-After"))
	var payload map[string]map[string]string
	require.NoError(t, common.Unmarshal([]byte(rejected.body), &payload))
	require.NotNil(t, payload["error"])
	assert.Equal(t, "concurrency_exceeded", payload["error"]["type"])
	assert.Equal(t, "concurrency_exceeded", payload["error"]["code"])

	close(relay)
	assert.Equal(t, http.StatusOK, (<-resCh).code, "held Gemini request must complete after release")
}

// streamReleaseAdaptor implements just enough of channel.Adaptor to point
// DoApiRequest at the slow upstream, mirroring the embedded-interface stub
// pattern used in relay/channel tests.
type streamReleaseAdaptor struct {
	channel.Adaptor
	baseURL string
}

func (s *streamReleaseAdaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return s.baseURL + "/v1/chat/completions", nil
}

func (s *streamReleaseAdaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	return nil
}

// snapshotCurrent returns the in-flight count for scope, 0 when the scope has
// no holder row (MemoryLimiter deletes rows that reach zero).
func snapshotCurrent(rows []concurrency.ScopeSnapshot, scope concurrency.ScopeKey) int {
	for _, row := range rows {
		if row.Scope == scope {
			return row.Current
		}
	}
	return 0
}

// TestConcurrency_StreamClientDisconnectReleasesSlotPromptly pins the SC-3 /
// UAT G-02-3 streaming semantics: when the client disconnects mid-stream, the
// outbound upstream request is aborted through relay/channel.DoApiRequest's
// client-context binding, the terminal handler returns within ~2s instead of
// riding out the slow upstream (~10s), and the concurrency slots are released
// immediately (limiter snapshot shows the scopes back at 0). The terminal
// handler deliberately has no ctx watcher or timeout of its own: the return
// must be driven by the cancellation propagating through the outbound request.
func TestConcurrency_StreamClientDisconnectReleasesSlotPromptly(t *testing.T) {
	setupRelayRouterTestDB(t)
	limiter := installConcurrencyRouterLimiter(t)
	service.InitHttpClient()

	const (
		userID    = 9501
		tokenID   = 9501
		channelID = 9501
		tokenKey  = "conce2ekey9501"
		modelName = "gpt-4o"
	)
	seedConcurrencyRelayFixtures(t, userID, tokenID, channelID, tokenKey, 1, modelName)

	// Slow chunked upstream: one chunk every 500ms for up to ~10s unless the
	// outbound request context is canceled first. Draining the request body
	// lets net/http start its background read so the server observes the
	// client-side abort.
	upstreamSawCancel := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		flusher, _ := w.(http.Flusher)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for range 20 {
			select {
			case <-r.Context().Done():
				select {
				case upstreamSawCancel <- struct{}{}:
				default:
				}
				return
			case <-ticker.C:
				if _, err := w.Write([]byte("chunk\n")); err != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	}))
	defer server.Close()

	firstChunk := make(chan struct{})
	var firstChunkOnce sync.Once
	adaptor := &streamReleaseAdaptor{baseURL: server.URL}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	engine := newConcurrencyRelayEngine(t, func(c *gin.Context) {
		resp, err := channel.DoApiRequest(adaptor, c, info, bytes.NewReader([]byte(`{"model":"`+modelName+`"}`)))
		if err != nil {
			// The client disconnect aborted the outbound request: return so the
			// middleware defers release the concurrency slots right away.
			c.Status(http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		buf := make([]byte, 64)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				c.Writer.Write(buf[:n])
				c.Writer.Flush()
				firstChunkOnce.Do(func() { close(firstChunk) })
			}
			if readErr != nil {
				return
			}
		}
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"`+modelName+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokenKey)
	clientCtx, cancel := context.WithCancel(req.Context())
	defer cancel()
	req = req.WithContext(clientCtx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		engine.ServeHTTP(w, req)
	}()

	select {
	case <-firstChunk:
	case <-time.After(10 * time.Second):
		require.Fail(t, "upstream never delivered the first stream chunk")
	}

	// While the relay is in flight the token slot must be held, so the
	// post-disconnect snapshot claim below is meaningful.
	holdRows, err := limiter.Snapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, snapshotCurrent(holdRows, concurrency.ScopeKey(fmt.Sprintf("key:%d:*", tokenID))),
		"the in-flight relay must hold its token concurrency slot")

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		require.Fail(t, "handler did not return within 2s after the client disconnected",
			"the streaming relay must abort the upstream call on disconnect instead of riding out the slow upstream (~10s)")
	}

	rows, err := limiter.Snapshot(context.Background())
	require.NoError(t, err)
	for _, scope := range []concurrency.ScopeKey{
		concurrency.ScopeKey(fmt.Sprintf("key:%d:%s", tokenID, modelName)),
		concurrency.ScopeKey(fmt.Sprintf("key:%d:*", tokenID)),
		concurrency.ScopeKey(fmt.Sprintf("user:%d:*", userID)),
	} {
		assert.Zerof(t, snapshotCurrent(rows, scope),
			"scope %s must be released immediately after the client disconnected", scope)
	}

	select {
	case <-upstreamSawCancel:
	case <-time.After(2 * time.Second):
		require.Fail(t, "upstream did not observe the outbound request cancellation",
			"the abort must propagate to the upstream, not just abandon the client side")
	}
}

// ---------------------------------------------------------------------------
// RTE-01 tracer: dynamic effective-weight routing across the real relay chain
// ---------------------------------------------------------------------------

// newEffectiveWeightRouterDB builds the isolated SQLite fixture for the EWMA
// distribution tracer. Unlike setupRelayRouterTestDB it runs the FULL migration
// (IsMasterNode is true for the InitDB call only) because the terminal handler
// of this tracer is the REAL controller.Relay, whose billing/session path
// touches every migrated table — notably user_subscriptions. It keeps the
// database in shared-cache memory so no file handle survives into teardown
// (the audit.db Windows lock documented in deferred-items.md).
func newEffectiveWeightRouterDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	previousMaster := common.IsMasterNode
	previousRedis := common.RedisEnabled
	previousSQLite := common.SQLitePath
	previousMain := common.MainDatabaseType()
	previousLog := common.LogDatabaseType()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousSQLDSN, hadSQLDSN := os.LookupEnv("SQL_DSN")

	common.IsMasterNode = true
	common.RedisEnabled = false
	common.SQLitePath = fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, os.Setenv("SQL_DSN", "local"))
	require.NoError(t, model.InitDB())
	model.LOG_DB = model.DB
	common.IsMasterNode = false

	t.Cleanup(func() {
		if sqlDB, err := model.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
		common.IsMasterNode = previousMaster
		common.RedisEnabled = previousRedis
		common.SQLitePath = previousSQLite
		common.SetDatabaseTypes(previousMain, previousLog)
		if hadSQLDSN {
			require.NoError(t, os.Setenv("SQL_DSN", previousSQLDSN))
		} else {
			require.NoError(t, os.Unsetenv("SQL_DSN"))
		}
		model.DB, model.LOG_DB = previousDB, previousLogDB
	})
	return model.DB
}

// seedEffectiveWeightFixtures creates the user + token + two same-priority
// weighted channels (+ ability rows carrying the SAME weights so the DB path's
// (Ability.Weight+10) base and the cache path's Channel.Weight base stay
// consistent) the distribution tracer needs. channelLimit > 0 arms the D-R1
// slot gate so the two channels are eligible for a non-1.0 multiplier.
func seedEffectiveWeightFixtures(
	t *testing.T,
	db *gorm.DB,
	userID, tokenID int,
	tokenKey string,
	channelAID, channelBID int,
	baseURLA, baseURLB string,
	modelName string,
	weightA, weightB uint,
	channelLimit int,
) {
	t.Helper()
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	require.NoError(t, db.Create(&model.User{
		Id:       userID,
		Username: fmt.Sprintf("ewma-tracer-user-%d", userID),
		// users.aff_code carries a unique index — every seeded user needs its
		// own value (same constraint as seedConcurrencyRelayFixtures).
		AffCode:  fmt.Sprintf("ewma%d", userID),
		Password: "password",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    100000,
	}).Error)
	require.NoError(t, db.Create(&model.Token{
		Id:             tokenID,
		UserId:         userID,
		Key:            tokenKey,
		Name:           fmt.Sprintf("ewma-tracer-token-%d", tokenID),
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}).Error)
	channels := []struct {
		id      int
		name    string
		baseURL string
		weight  uint
	}{
		{channelAID, fmt.Sprintf("ewma-tracer-a-%d", channelAID), baseURLA, weightA},
		{channelBID, fmt.Sprintf("ewma-tracer-b-%d", channelBID), baseURLB, weightB},
	}
	for _, spec := range channels {
		weight := spec.weight
		limit := channelLimit
		require.NoError(t, db.Create(&model.Channel{
			Id:               spec.id,
			Type:             constant.ChannelTypeOpenAI,
			Key:              fmt.Sprintf("ewma-tracer-key-%d", spec.id),
			Name:             spec.name,
			Status:           common.ChannelStatusEnabled,
			Group:            "default",
			Models:           modelName,
			BaseURL:          &spec.baseURL,
			Weight:           &weight,
			ConcurrencyLimit: &limit,
		}).Error)
		require.NoError(t, db.Create(&model.Ability{
			Group:     "default",
			Model:     modelName,
			ChannelId: spec.id,
			Enabled:   true,
			Weight:    spec.weight,
		}).Error)
	}
}

// newCountingUpstream returns an httptest upstream that answers every request
// with a valid non-streaming OpenAI chat completion and counts its hits.
func newCountingUpstream(t *testing.T, modelName string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-ewma","object":"chat.completion","created":1,"model":"`+modelName+
			`","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

// effectiveWeightChannelLimit is the per-channel in-flight cap seeded by the
// tracer. 1 is enough: the middleware chain never holds the channel axis
// (buildScopes covers token/user scopes only), so the relay slot is the sole
// holder and sequential requests never self-saturate — while one sibling hold
// makes the saturation-rejection case deterministic.
const effectiveWeightChannelLimit = 1

// TestRelayChannelEffectiveWeightRoutingShiftsDistribution is the RTE-01
// tracer: two same-priority channels with static weights 90/10 serve one model
// through the REAL middleware chain (TokenAuth → ModelRequestRateLimit →
// Distribute → ConcurrencyMiddleware → controller.Relay) with two independent
// httptest upstreams. Without EWMA the distribution is the static weighted
// random baseline; after (channel, model) health warm-up the layer weights are
// scaled by the recomputed multipliers and the distribution must shift
// predictably toward the healthy channel while the unhealthy one keeps a
// non-starving floor.
//
// Static baseline over 200 draws (weights 90/10):
//   - cache path picks B with 10/(90+10)=10% → ≈20 hits
//   - DB path base is (Ability.Weight+10): B gets (10+10)/((90+10)+(10+10))
//     = 16.7% → ≈33 hits
//
// Warmed multipliers (20 failure/5000ms samples on A, 20 success/100ms on B,
// same-model warm mean latency 2550ms):
//   - A = clamp(0.2+0.8×0, 0.2, 5.0) × clamp(2550/5000, 0.5, 2.0) = 0.102 → 0.2
//     (the 0.2 floor is what guarantees A never starves)
//   - B = clamp(0.2+0.8×1, 0.2, 5.0) × clamp(2550/100, 0.5, 2.0) = 2.0
//
// EWMA-shifted share of B: cache path 10×2/(90×0.2+10×2) = 20/38 ≈ 52.6%
// (expected ≈105 hits, σ≈7.2); DB path (10+10)×2/((90+10)×0.2+(10+10)×2) =
// 40/60 ≈ 66.7% (expected ≈133 hits, σ≈6.7). The B≥70 threshold sits ≥4.9σ
// below the mean and the A≥25 floor ≥9σ below its mean on both paths, so a
// static-baseline run (20–33 hits for B) can only fail one way: the EWMA path
// did not execute.
func TestRelayChannelEffectiveWeightRoutingShiftsDistribution(t *testing.T) {
	require.NoError(t, i18n.Init())
	newEffectiveWeightRouterDB(t)

	// The attempt layer of the real controller.Relay acquires the channel slot
	// through the controller-side limiter singleton. Install a dedicated
	// MemoryLimiter so the slot gate is live end-to-end and the saturation case
	// can hold buckets. SetConcurrencyLimiter is a sync.Once and this test
	// process never calls it anywhere else, so the installation is ours alone;
	// it cannot be uninstalled afterwards, but no other router test drives
	// controller.Relay, so the singleton stays inert for them.
	relaySlotLimiter := concurrency.NewMemoryLimiter()
	controller.SetConcurrencyLimiter(relaySlotLimiter)

	// Zero-ratio models: the tracer pins routing distribution, not billing —
	// the pre-consume stays 0 exactly like the controller slot tracers.
	previousRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(
		`{"ewma-trace-db":0,"ewma-trace-cache":0}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios))
	})

	// Affinity/pin isolation (plan key_links): the default affinity rules
	// (^gpt-.*$ on /v1/responses, ^claude-.*$ on /v1/messages) must never match
	// this tracer's model/path, otherwise GetPreferredChannelByAffinity would
	// pin the first hop and bypass the weighted layer entirely. Pinned
	// explicitly so a future default-rule change breaks HERE, not silently.
	for _, rule := range operation_setting.GetChannelAffinitySetting().Rules {
		for _, pattern := range rule.ModelRegex {
			re, err := regexp.Compile(pattern)
			require.NoError(t, err)
			assert.False(t, re.MatchString("ewma-trace-db"),
				"affinity rule %q must not match the tracer model (pin bypasses weighted selection)", rule.Name)
			assert.False(t, re.MatchString("ewma-trace-cache"),
				"affinity rule %q must not match the tracer model (pin bypasses weighted selection)", rule.Name)
		}
	}

	cases := []struct {
		name                   string
		memoryCache            bool
		modelName              string
		userID, tokenID        int
		channelAID, channelBID int
		tokenKey               string
	}{
		{name: "db-path", memoryCache: false, modelName: "ewma-trace-db", userID: 9601, tokenID: 9601, channelAID: 9601, channelBID: 9602, tokenKey: "ewmatracedbkey01"},
		{name: "cache-path", memoryCache: true, modelName: "ewma-trace-cache", userID: 9611, tokenID: 9611, channelAID: 9611, channelBID: 9612, tokenKey: "ewmatracecachekey01"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			originalMemoryCache := common.MemoryCacheEnabled
			common.MemoryCacheEnabled = tc.memoryCache
			t.Cleanup(func() {
				common.MemoryCacheEnabled = originalMemoryCache
				model.InitChannelCache()
			})

			upstreamA, hitsA := newCountingUpstream(t, tc.modelName)
			upstreamB, hitsB := newCountingUpstream(t, tc.modelName)
			seedEffectiveWeightFixtures(t, model.DB, tc.userID, tc.tokenID, tc.tokenKey,
				tc.channelAID, tc.channelBID, upstreamA.URL, upstreamB.URL, tc.modelName, 90, 10,
				effectiveWeightChannelLimit)
			if tc.memoryCache {
				model.InitChannelCache()
			}

			installConcurrencyRouterLimiter(t)
			service.InitHttpClient()
			engine := newConcurrencyRelayEngine(t, func(c *gin.Context) {
				controller.Relay(c, types.RelayFormatOpenAI)
			})

			body := `{"model":"` + tc.modelName + `","messages":[{"role":"user","content":"hi"}]}`
			post := func() concurrencyRelayResult {
				return postConcurrencyRelay(engine, "/v1/chat/completions", tc.tokenKey, body)
			}

			// ------------------------------------------------------------------
			// Distribution phase: warm the (channel, model) health records, then
			// 200 sequential real-chain requests; the upstream hit counters
			// observe which channel each request was dispatched to.
			// ------------------------------------------------------------------
			model.ResetChannelRoutingHealth()
			for range 20 {
				model.RecordChannelAttempt(tc.channelAID, tc.modelName, 5000, false)
				model.RecordChannelAttempt(tc.channelBID, tc.modelName, 100, true)
			}

			before := [2]int32{hitsA.Load(), hitsB.Load()}
			for i := range 200 {
				res := post()
				require.Equalf(t, http.StatusOK, res.code, "distribution request %d must succeed: %s", i, res.body)
			}
			hitsAAfter := hitsA.Load() - before[0]
			hitsBAfter := hitsB.Load() - before[1]
			require.EqualValues(t, 200, hitsAAfter+hitsBAfter,
				"every dispatched request must land on exactly one of the two upstreams")
			assert.GreaterOrEqualf(t, hitsBAfter, int32(70),
				"channel B (healthy, static weight 10) must absorb ≥70 of 200 requests after EWMA warm-up — static baselines are 10%% (cache path) / 16.7%% (DB path), got %d", hitsBAfter)
			assert.GreaterOrEqualf(t, hitsAAfter, int32(25),
				"channel A (static weight 90, multiplier floor 0.2) must keep absorbing ≥25 of 200 requests — the clamp floor guarantees no starvation, got %d", hitsAAfter)

			// State truth: the recomputed multipliers carry the exact plan
			// formula values. A's raw product 0.2×0.51=0.102 is floored to the
			// 0.2 clamp (and its success EWMA can only creep up by ~1e-3 under
			// the 300s half-life, which the floor absorbs), B's latency factor
			// 2550/100 is capped at the 2.0 clamp — both stay exact under any
			// number of mid-phase recomputes.
			rows := model.SnapshotChannelRoutingHealth()
			assert.InDeltaf(t, 0.2, effectiveWeightRoutingMultiplier(t, rows, tc.channelAID, tc.modelName), 1e-6,
				"channel A multiplier must sit exactly on the 0.2 clamp floor")
			assert.InDeltaf(t, 2.0, effectiveWeightRoutingMultiplier(t, rows, tc.channelBID, tc.modelName), 1e-6,
				"channel B multiplier must sit exactly on the 2.0 latency-factor clamp")

			// ------------------------------------------------------------------
			// Observation closure: with a clean health store, 20 requests through
			// the real chain must produce exactly 20 samples — one per dispatched
			// attempt — and the per-channel sample counts must equal the
			// upstream hit counts one-to-one (the record point in
			// controller.Relay is wired to the actual dispatch).
			// ------------------------------------------------------------------
			model.ResetChannelRoutingHealth()
			before = [2]int32{hitsA.Load(), hitsB.Load()}
			for i := range 20 {
				res := post()
				require.Equalf(t, http.StatusOK, res.code, "observation request %d must succeed: %s", i, res.body)
			}
			rows = model.SnapshotChannelRoutingHealth()
			totalSamples := 0
			for _, row := range rows {
				totalSamples += row.Samples
			}
			assert.Equal(t, 20, totalSamples,
				"20 dispatched requests must record exactly 20 routing-health samples (one per attempt)")
			// An absent row means zero samples for that channel — the equality
			// must hold either way (0 hits ↔ 0 samples).
			assert.Equalf(t, int(hitsA.Load()-before[0]), effectiveWeightRoutingSamplesOrZero(rows, tc.channelAID, tc.modelName),
				"channel A samples must equal its upstream hit count (record point = dispatched attempt)")
			assert.Equalf(t, int(hitsB.Load()-before[1]), effectiveWeightRoutingSamplesOrZero(rows, tc.channelBID, tc.modelName),
				"channel B samples must equal its upstream hit count (record point = dispatched attempt)")

			// ------------------------------------------------------------------
			// Saturation rejection produces no samples: with BOTH channel buckets
			// held full by siblings, the relay attempt layer refuses before the
			// dispatch (the middleware chain never holds the channel axis, so the
			// 429 must come from the relay-level saturation rejection), no
			// upstream is reached, and the health store stays empty — a gateway
			// side 429 must never pollute channel health.
			// ------------------------------------------------------------------
			model.ResetChannelRoutingHealth()
			siblingReleaseA, err := relaySlotLimiter.Acquire(context.Background(), concurrency.ChannelScope(tc.channelAID), effectiveWeightChannelLimit)
			require.NoError(t, err)
			siblingReleaseB, err := relaySlotLimiter.Acquire(context.Background(), concurrency.ChannelScope(tc.channelBID), effectiveWeightChannelLimit)
			require.NoError(t, err)
			before = [2]int32{hitsA.Load(), hitsB.Load()}
			rejected := post()
			require.Equal(t, http.StatusTooManyRequests, rejected.code, rejected.body)
			var payload map[string]map[string]string
			require.NoError(t, common.Unmarshal([]byte(rejected.body), &payload))
			require.NotNil(t, payload["error"])
			assert.Equal(t, "concurrency_exceeded", payload["error"]["type"],
				"the terminal must be the relay-level saturation rejection")
			assert.Empty(t, model.SnapshotChannelRoutingHealth(),
				"a saturation rejection never enters the attempt body and must record no health sample")
			assert.EqualValues(t, 0, hitsA.Load()-before[0], "a rejected attempt must not reach channel A's upstream")
			assert.EqualValues(t, 0, hitsB.Load()-before[1], "a rejected attempt must not reach channel B's upstream")
			siblingReleaseA()
			siblingReleaseB()

			// ------------------------------------------------------------------
			// Cold static pin: with no records at all the effective weights must
			// collapse to the static baseline — every multiplier stays exactly
			// 1.0 (the record default; rows created by these very requests are
			// still below the warm threshold) and both upstreams keep getting
			// hits, i.e. the degraded path is the status-quo weighted random.
			// 100 draws (deviation from the plan's 20, recorded): at the static
			// share of B a 20-draw both-hit assertion fails with ~12% (cache) /
			// ~2.6% (DB) probability, while 100 draws put the miss probability at
			// ≈3e-5 / ≈1e-8 — the plan's own rule allows distribution assertions
			// only with a real σ margin.
			// ------------------------------------------------------------------
			model.ResetChannelRoutingHealth()
			assert.Empty(t, model.SnapshotChannelRoutingHealth(),
				"the cold phase starts from a pristine routing-health store")
			for i := range 5 {
				res := post()
				require.Equalf(t, http.StatusOK, res.code, "cold request %d must succeed: %s", i, res.body)
			}
			rows = model.SnapshotChannelRoutingHealth()
			require.NotEmpty(t, rows, "the dispatches themselves must have recorded samples")
			for _, row := range rows {
				assert.Equalf(t, 1.0, row.Multiplier,
					"channel %d stays at multiplier 1.0 while below the warm threshold (%d samples)", row.ChannelID, row.Samples)
			}
			before = [2]int32{hitsA.Load(), hitsB.Load()}
			for i := range 95 {
				res := post()
				require.Equalf(t, http.StatusOK, res.code, "cold request %d must succeed: %s", i+5, res.body)
			}
			assert.Greaterf(t, hitsA.Load()-before[0], int32(0),
				"the cold static baseline must keep dispatching to channel A")
			assert.Greaterf(t, hitsB.Load()-before[1], int32(0),
				"the cold static baseline must keep dispatching to channel B")
		})
	}
}

// effectiveWeightRoutingMultiplier reads one channel's recomputed multiplier
// out of a routing-health snapshot, failing the test when the row is missing.
func effectiveWeightRoutingMultiplier(t *testing.T, rows []model.ChannelRoutingHealth, channelID int, modelName string) float64 {
	t.Helper()
	for _, row := range rows {
		if row.ChannelID == channelID && row.Model == modelName {
			return row.Multiplier
		}
	}
	require.FailNowf(t, "routing health row missing", "channel=%d model=%s", channelID, modelName)
	return 0
}

// effectiveWeightRoutingSamplesOrZero reads one channel's sample count out of
// a routing-health snapshot; an absent row is exactly zero samples.
func effectiveWeightRoutingSamplesOrZero(rows []model.ChannelRoutingHealth, channelID int, modelName string) int {
	for _, row := range rows {
		if row.ChannelID == channelID && row.Model == modelName {
			return row.Samples
		}
	}
	return 0
}
