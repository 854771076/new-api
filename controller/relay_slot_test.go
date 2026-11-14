package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/concurrency"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Recording limiter fixture
// ---------------------------------------------------------------------------

// slotAcquire is one recorded Acquire call: the scope the relay layer asked for
// and the per-request cap it passed, which together pin that the channel bucket
// is taken through the 02-08 Acquire contract with the published limit.
type slotAcquire struct {
	Scope concurrency.ScopeKey
	Limit int
}

// channelSlotLimiter is the recording limiter installed by the channel-slot
// tests. Every operation delegates to a real MemoryLimiter so snapshot counts
// stay truthful, while the (scope, limit) pairs handed to Acquire are recorded
// for assertions. Successful grants also drive a held/peak gauge so tests can
// assert "at most one slot in flight at any moment" (CHN-03/SC-2).
type channelSlotLimiter struct {
	inner *concurrency.MemoryLimiter

	mu       sync.Mutex
	acquires []slotAcquire
	held     atomic.Int32
	peak     atomic.Int32
	// granted/rejected tally every outer Acquire outcome so load tests can
	// assert "granted+rejected == attempts" across two counting layers (the
	// routing layer's use_channel entries vs this limiter's records).
	granted  atomic.Int32
	rejected atomic.Int32
}

func newChannelSlotLimiter() *channelSlotLimiter {
	return &channelSlotLimiter{inner: concurrency.NewMemoryLimiter()}
}

func (l *channelSlotLimiter) Acquire(ctx context.Context, scope concurrency.ScopeKey, limit int) (func(), error) {
	release, err := l.inner.Acquire(ctx, scope, limit)
	l.mu.Lock()
	l.acquires = append(l.acquires, slotAcquire{Scope: scope, Limit: limit})
	l.mu.Unlock()
	if err != nil {
		l.rejected.Add(1)
		return release, err
	}
	l.granted.Add(1)
	current := l.held.Add(1)
	for {
		previous := l.peak.Load()
		if current <= previous || l.peak.CompareAndSwap(previous, current) {
			break
		}
	}
	innerRelease := release
	return func() {
		l.held.Add(-1)
		innerRelease()
	}, nil
}

func (l *channelSlotLimiter) AcquireBatch(ctx context.Context, scopes []concurrency.ScopeLimit) (func(), error) {
	return l.inner.AcquireBatch(ctx, scopes)
}

func (l *channelSlotLimiter) Snapshot(ctx context.Context) ([]concurrency.ScopeSnapshot, error) {
	return l.inner.Snapshot(ctx)
}

func (l *channelSlotLimiter) DegradedStatus() concurrency.DegradedStatus {
	return l.inner.DegradedStatus()
}

func (l *channelSlotLimiter) records() []slotAcquire {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]slotAcquire, len(l.acquires))
	copy(out, l.acquires)
	return out
}

// peakHeld reports the highest number of slots this limiter granted
// concurrently — the "at most N in flight at any moment" gauge.
func (l *channelSlotLimiter) peakHeld() int32 {
	return l.peak.Load()
}

// installChannelSlotLimiter assigns the package-private limiter singleton the
// same way controller/concurrency_test.go does. SetConcurrencyLimiter is a
// sync.Once, so tests must swap the variable directly and restore it.
func installChannelSlotLimiter(t *testing.T, l concurrency.Limiter) {
	t.Helper()
	previous := concurrencyLimiter
	concurrencyLimiter = l
	t.Cleanup(func() { concurrencyLimiter = previous })
}

// slotContext builds the minimal gin context the attempt helper needs: one
// request and the published channel cap.
func slotContext(t *testing.T, channelConcurrencyLimit int) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"slot-model","messages":[{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyChannelConcurrencyLimit, channelConcurrencyLimit)
	return c
}

// slotSnapshotCurrent reads the current in-flight count of scope, 0 when the
// bucket has no holder row (MemoryLimiter deletes rows that reach zero).
func slotSnapshotCurrent(t *testing.T, l concurrency.Limiter, scope concurrency.ScopeKey) int {
	t.Helper()
	rows, err := l.Snapshot(context.Background())
	require.NoError(t, err)
	for _, row := range rows {
		if row.Scope == scope {
			return row.Current
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// Publication seam
// ---------------------------------------------------------------------------

// TestSetupContextForSelectedChannelPublishesChannelConcurrencyLimit pins the
// CHN-03 upper-limit publication seam: the channel axis cap must reach the
// request context so the relay layer can read it per attempt. Both hops share
// this one publisher — the first hop through middleware.Distribute and every
// retry hop through controller.getChannel — so a single publication line covers
// both. A nil ConcurrencyLimit must publish 0 (02-09's nil-safe getter; 0 means
// unlimited but the bucket still counts, D-18).
func TestSetupContextForSelectedChannelPublishesChannelConcurrencyLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	capped := 2
	cappedChannel := &model.Channel{
		Id:               4242,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "slot-limit-key",
		Name:             "slot-limit-channel",
		Models:           "slot-limit-model",
		ConcurrencyLimit: &capped,
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"slot-limit-model"}`))
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, cappedChannel, "slot-limit-model"))
	assert.Equal(t, 2, common.GetContextKeyInt(c, constant.ContextKeyChannelConcurrencyLimit),
		"a configured channel cap must be published into the request context")

	unlimitedChannel := &model.Channel{
		Id:     4243,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "slot-no-limit-key",
		Name:   "slot-no-limit-channel",
		Models: "slot-limit-model",
	}
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"slot-limit-model"}`))
	require.Nil(t, middleware.SetupContextForSelectedChannel(c2, unlimitedChannel, "slot-limit-model"))
	assert.Equal(t, 0, common.GetContextKeyInt(c2, constant.ContextKeyChannelConcurrencyLimit),
		"a nil ConcurrencyLimit must publish 0 (unlimited), never a stale value")
}

// ---------------------------------------------------------------------------
// Per-attempt lifecycle: three exits + rejection signal
// ---------------------------------------------------------------------------

// TestRunChannelSlotAttemptReleasesOnEveryExit pins the SC-3 three-exit
// contract (T-02-14-01): the slot taken for one attempt is freed on the
// success return, on the error return, and while a panic unwinds — and the
// panic must keep propagating rather than being swallowed.
func TestRunChannelSlotAttemptReleasesOnEveryExit(t *testing.T) {
	const channelID = 4242
	scope := concurrency.ChannelScope(channelID)

	t.Run("success return releases", func(t *testing.T) {
		lim := newChannelSlotLimiter()
		installChannelSlotLimiter(t, lim)
		c := slotContext(t, 2)

		var ran bool
		got := runChannelSlotAttempt(c, channelID, func() *types.NewAPIError {
			ran = true
			assert.Equal(t, 1, slotSnapshotCurrent(t, lim, scope), "the slot is held while the body runs")
			return nil
		})
		require.True(t, ran, "an unchallenged attempt must enter its body")
		require.Nil(t, got)
		assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scope), "the slot must be free after a success return")
	})

	t.Run("error return releases", func(t *testing.T) {
		lim := newChannelSlotLimiter()
		installChannelSlotLimiter(t, lim)
		c := slotContext(t, 2)

		bodyErr := types.NewError(errors.New("upstream failed"), types.ErrorCodeBadResponse)
		got := runChannelSlotAttempt(c, channelID, func() *types.NewAPIError {
			assert.Equal(t, 1, slotSnapshotCurrent(t, lim, scope), "the slot is held while the body runs")
			return bodyErr
		})
		require.Same(t, bodyErr, got, "the body's error must pass through untouched")
		assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scope), "the slot must be free after an error return")
	})

	t.Run("panic unwinds and releases", func(t *testing.T) {
		lim := newChannelSlotLimiter()
		installChannelSlotLimiter(t, lim)
		c := slotContext(t, 2)

		require.PanicsWithValue(t, "slot-boom", func() {
			_ = runChannelSlotAttempt(c, channelID, func() *types.NewAPIError {
				assert.Equal(t, 1, slotSnapshotCurrent(t, lim, scope), "the slot is held while the body runs")
				panic("slot-boom")
			})
		}, "a panic in the body must keep propagating")
		assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scope), "the slot must be free after a panic unwinds")
	})
}

// TestRunChannelSlotAttemptRejectsWhenChannelBucketIsFull pins the 02-15 seam:
// a saturated channel bucket refuses the attempt before its body runs and
// surfaces the dedicated rejection signal — code channel:concurrency_exceeded
// (so IsChannelError routes it to retry for free, BCD D-4), status 429, and a
// fixed neutral message that leaks neither the scope literal nor the channel id
// (CNT-01 hygiene, T-02-14-05).
func TestRunChannelSlotAttemptRejectsWhenChannelBucketIsFull(t *testing.T) {
	const channelID = 4242
	scope := concurrency.ChannelScope(channelID)

	lim := newChannelSlotLimiter()
	installChannelSlotLimiter(t, lim)
	c := slotContext(t, 1)

	// A sibling in-flight request already holds the only slot of this bucket.
	_, err := lim.inner.Acquire(context.Background(), scope, 1)
	require.NoError(t, err)

	var bodyRan bool
	got := runChannelSlotAttempt(c, channelID, func() *types.NewAPIError {
		bodyRan = true
		return nil
	})
	require.False(t, bodyRan, "a saturated bucket must not enter the attempt body")
	require.NotNil(t, got)

	assert.Equal(t, service.ChannelConcurrencyExceededCode, got.GetErrorCode())
	assert.Equal(t, http.StatusTooManyRequests, got.StatusCode)
	assert.True(t, service.IsChannelConcurrencyRejection(got))
	assert.True(t, types.IsChannelError(got), "the signal must hit the IsChannelError retry route (BCD D-4)")

	sanitizer := regexp.MustCompile(`channel:\d+`)
	for _, rendered := range []string{got.Error(), got.ToOpenAIError().Message} {
		assert.NotRegexp(t, sanitizer, rendered, "the rendered message must not leak the scope literal")
		assert.NotContains(t, rendered, fmt.Sprint(channelID), "the rendered message must not leak the channel id")
	}
}

// TestRunChannelSlotAttemptWithoutLimiterRunsBody pins the no-op stance shared
// with ConcurrencyMiddleware: with no limiter installed the relay must proceed
// untouched instead of failing closed.
func TestRunChannelSlotAttemptWithoutLimiterRunsBody(t *testing.T) {
	previous := concurrencyLimiter
	concurrencyLimiter = nil
	t.Cleanup(func() { concurrencyLimiter = previous })

	c := slotContext(t, 2)
	var ran bool
	got := runChannelSlotAttempt(c, 4242, func() *types.NewAPIError {
		ran = true
		return nil
	})
	require.True(t, ran)
	require.Nil(t, got)
}

// ---------------------------------------------------------------------------
// Tracer e2e: one real Relay attempt
// ---------------------------------------------------------------------------

// newChannelSlotDB builds the isolated SQLite fixture for the relay e2e. Unlike
// modelManagementDB it runs the FULL migration (IsMasterNode is true for the
// InitDB call only) so every table the billing/session path touches — notably
// user_subscriptions — exists, and it keeps the database in shared-cache memory
// so no file handle survives into TempDir teardown (the audit.db Windows lock
// documented in deferred-items.md).
func newChannelSlotDB(t *testing.T) *gorm.DB {
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

// TestRelaySingleAttemptAcquiresChannelSlotOnce is the Task 02-14-01 tracer
// slice: one successful attempt through the real controller.Relay in the
// default text format, with the channel context published by the real
// middleware.SetupContextForSelectedChannel and the dispatch hitting an
// httptest upstream. The recording limiter must observe exactly one Acquire —
// scope channel:<id>, cap = the published value — and the bucket must be back
// at 0 once Relay returns (SC-3 release, no leak).
func TestRelaySingleAttemptAcquiresChannelSlotOnce(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := newChannelSlotDB(t)

	const modelName = "slot-attempt-model"
	previousRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{"%s":0}`, modelName)))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios))
	})

	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-slot","object":"chat.completion","created":1,"model":"`+modelName+
			`","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
	}))
	t.Cleanup(upstream.Close)
	service.InitHttpClient()

	const userID = 7301
	require.NoError(t, db.Create(&model.User{
		Id:       userID,
		Username: "slot-attempt-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    100000,
	}).Error)
	require.NoError(t, db.Create(&model.Token{
		Id:             7301,
		UserId:         userID,
		Key:            "slot-attempt-token-key",
		Name:           "slot-attempt-token",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}).Error)

	const channelID = 7301
	channelLimit := 2
	channel := &model.Channel{
		Id:               channelID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "slot-attempt-channel-key",
		Name:             "slot-attempt-channel",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		ConcurrencyLimit: &channelLimit,
	}
	require.NoError(t, db.Create(channel).Error)

	lim := newChannelSlotLimiter()
	installChannelSlotLimiter(t, lim)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+modelName+`","messages":[{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")

	// Identity keys normally written by middleware.TokenAuth.
	c.Set("id", userID)
	c.Set("username", "slot-attempt-user")
	c.Set("token_id", 7301)
	c.Set("token_key", "slot-attempt-token-key")
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUserQuota, 100000)
	common.SetContextKey(c, constant.ContextKeyTokenUnlimited, true)
	common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
	common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())

	// First-hop publication through the real publisher (production reaches it
	// from middleware.Distribute; the retry hop reaches it from getChannel).
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, channel, modelName))

	Relay(c, types.RelayFormatOpenAI)

	require.Equal(t, 1, int(upstreamCalls.Load()), "a single successful attempt makes exactly one upstream call")
	records := lim.records()
	require.Len(t, records, 1, "the attempt must acquire the channel bucket exactly once")
	assert.Equal(t, concurrency.ChannelScope(channelID), records[0].Scope)
	assert.Equal(t, channelLimit, records[0].Limit, "the cap must be the value published into the context")
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, concurrency.ChannelScope(channelID)),
		"the channel bucket must be back at 0 once Relay returns")
	assert.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
}

// ---------------------------------------------------------------------------
// Retry-loop invariants: per-attempt slots across attempts (CHN-03/SC-2)
// ---------------------------------------------------------------------------

// pinRelayRetryBudget pins the retry drivers for the loop-invariant tests so
// they never depend on production defaults: attempt budget = one retry plus an
// explicitly configured retryable upstream status (500). Auto-disable stays
// off — these tests pin slot lifecycle, not ban behavior (T-02-14-04 lives in
// the service package).
func pinRelayRetryBudget(t *testing.T) {
	t.Helper()
	pinRelayRetryBudgetWithRetries(t, 1)
}

// pinRelayRetryBudgetWithRetries is pinRelayRetryBudget with an explicit
// attempt budget: the all-saturated empty-selection classification needs one
// more hop (refuse A → refuse B → empty selection) than the two-attempt
// loop-invariant tests, so it asks for its own retry count.
func pinRelayRetryBudgetWithRetries(t *testing.T, retryTimes int) {
	t.Helper()
	previousRetryTimes := common.RetryTimes
	common.RetryTimes = retryTimes
	previousStatusCodes := operation_setting.AutomaticRetryStatusCodesToString()
	require.NoError(t, operation_setting.AutomaticRetryStatusCodesFromString("500"))
	previousAutoDisable := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = false
	t.Cleanup(func() {
		common.RetryTimes = previousRetryTimes
		require.NoError(t, operation_setting.AutomaticRetryStatusCodesFromString(previousStatusCodes))
		common.AutomaticDisableChannelEnabled = previousAutoDisable
	})
}

// newSlotRetryUpstream returns an httptest upstream whose FIRST call fails
// with a retryable 500 and every later call succeeds. The atomic call counter
// pins the attempt count — no sleep-based synchronization anywhere.
func newSlotRetryUpstream(t *testing.T, modelName string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, "upstream boom")
			return
		}
		_, _ = io.WriteString(w, `{"id":"chatcmpl-slot-retry","object":"chat.completion","created":1,"model":"`+modelName+
			`","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

// setSlotRelayIdentityKeys writes the context keys middleware.TokenAuth and
// the first-hop distributor would normally provide when a test drives
// controller.Relay directly.
func setSlotRelayIdentityKeys(c *gin.Context, userID int, username, tokenKey string) {
	c.Set("id", userID)
	c.Set("username", username)
	c.Set("token_id", userID)
	c.Set("token_key", tokenKey)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUserQuota, 100000)
	common.SetContextKey(c, constant.ContextKeyTokenUnlimited, true)
	common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
	common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
}

// TestRelayRetryLoopHoldsOneChannelSlotPerAttempt pins CHN-03/SC-2 for a
// limit=1 channel: attempt 1 fails retryably, attempt 2 succeeds — the retry
// must take the SAME bucket's slot again, which only works if attempt 1
// released before attempt 2 acquired (a cross-iteration hold would deadlock
// the request against its own limit=1 bucket and the request would fail).
// Exactly two grants on scope channel:<id>, peak concurrent holds <= 1,
// bucket back at 0, and the request must end in HTTP 200 with both upstream
// calls observed.
func TestRelayRetryLoopHoldsOneChannelSlotPerAttempt(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := newChannelSlotDB(t)
	pinRelayRetryBudget(t)

	const modelName = "slot-retry-model"
	previousRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{"%s":0}`, modelName)))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })

	upstream, upstreamCalls := newSlotRetryUpstream(t, modelName)
	service.InitHttpClient()

	const userID = 7302
	const channelID = 7302
	require.NoError(t, db.Create(&model.User{
		Id:       userID,
		Username: "slot-retry-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    100000,
	}).Error)
	require.NoError(t, db.Create(&model.Token{
		Id:             userID,
		UserId:         userID,
		Key:            "slot-retry-token-key",
		Name:           "slot-retry-token",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}).Error)
	channelLimit := 1
	channel := &model.Channel{
		Id:               channelID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "slot-retry-channel-key",
		Name:             "slot-retry-channel",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		ConcurrencyLimit: &channelLimit,
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelID,
		Enabled:   true,
	}).Error)

	lim := newChannelSlotLimiter()
	installChannelSlotLimiter(t, lim)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+modelName+`","messages":[{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setSlotRelayIdentityKeys(c, userID, "slot-retry-user", "slot-retry-token-key")

	// First-hop publication through the real publisher (the retry hop
	// re-publishes from controller.getChannel via the same function).
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, channel, modelName))

	Relay(c, types.RelayFormatOpenAI)

	require.Equal(t, 2, int(upstreamCalls.Load()),
		"attempt 1 must fail retryably and attempt 2 must reach the upstream")
	records := lim.records()
	require.Len(t, records, 2, "each attempt acquires the channel bucket exactly once")
	scope := concurrency.ChannelScope(channelID)
	for i := range records {
		assert.Equal(t, scope, records[i].Scope, "attempt %d must take the same channel bucket", i+1)
		assert.Equal(t, channelLimit, records[i].Limit, "attempt %d must use the cap published for this attempt", i+1)
	}
	assert.LessOrEqual(t, int(lim.peakHeld()), 1,
		"the retry must never hold a slot across iterations (limit=1 self-deadlock guard)")
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scope),
		"the bucket must be back at 0 once Relay returns")
	assert.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), "chatcmpl-slot-retry",
		"the successful second attempt must have written the upstream response")
}

// TestRelayRetrySwitchesChannelSlotScopePerAttempt pins "switching channels
// on retry does not double-count": attempt 1 runs on context channel A (first
// hop stub), the retry hop re-selects channel B from the database — the two
// grants must target channel:A and channel:B respectively, the concurrency
// peak must stay <= 1 (slots never overlap), and each bucket must be back at
// 0 once Relay returns.
func TestRelayRetrySwitchesChannelSlotScopePerAttempt(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := newChannelSlotDB(t)
	pinRelayRetryBudget(t)

	const modelName = "slot-switch-model"
	previousRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{"%s":0}`, modelName)))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })

	upstream, upstreamCalls := newSlotRetryUpstream(t, modelName)
	service.InitHttpClient()

	const userID = 7311
	const channelAID = 7311
	const channelBID = 7312
	require.NoError(t, db.Create(&model.User{
		Id:       userID,
		Username: "slot-switch-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    100000,
	}).Error)
	require.NoError(t, db.Create(&model.Token{
		Id:             userID,
		UserId:         userID,
		Key:            "slot-switch-token-key",
		Name:           "slot-switch-token",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}).Error)
	// Channel A seeds the FIRST hop only: it carries no ability row, so the
	// retry hop's database selection can never pick it — that makes the
	// attempt-1→A / attempt-2→B split deterministic without relying on
	// priority weights or random selection.
	limitA, limitB := 1, 1
	channelA := &model.Channel{
		Id:               channelAID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "slot-switch-channel-a-key",
		Name:             "slot-switch-channel-a",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		ConcurrencyLimit: &limitA,
	}
	require.NoError(t, db.Create(channelA).Error)
	channelB := &model.Channel{
		Id:               channelBID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "slot-switch-channel-b-key",
		Name:             "slot-switch-channel-b",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		ConcurrencyLimit: &limitB,
	}
	require.NoError(t, db.Create(channelB).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelBID,
		Enabled:   true,
	}).Error)

	lim := newChannelSlotLimiter()
	installChannelSlotLimiter(t, lim)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+modelName+`","messages":[{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setSlotRelayIdentityKeys(c, userID, "slot-switch-user", "slot-switch-token-key")

	// First hop pinned to channel A through the real publisher; the retry hop
	// re-selects via getChannel and re-publishes for channel B.
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, channelA, modelName))

	Relay(c, types.RelayFormatOpenAI)

	require.Equal(t, 2, int(upstreamCalls.Load()),
		"attempt 1 on channel A must fail retryably and attempt 2 on channel B must succeed")
	records := lim.records()
	require.Len(t, records, 2, "each attempt acquires exactly one channel bucket")
	assert.Equal(t, concurrency.ChannelScope(channelAID), records[0].Scope,
		"attempt 1 must run on the first-hop channel A")
	assert.Equal(t, 1, records[0].Limit)
	assert.Equal(t, concurrency.ChannelScope(channelBID), records[1].Scope,
		"the retry hop must re-acquire on the re-selected channel B (no double-count on A)")
	assert.Equal(t, 1, records[1].Limit, "attempt 2 must read B's freshly published cap, not A's stale value")
	assert.LessOrEqual(t, int(lim.peakHeld()), 1,
		"the two attempts must never hold slots at the same time")
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, concurrency.ChannelScope(channelAID)),
		"channel A's bucket must be back at 0 once Relay returns")
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, concurrency.ChannelScope(channelBID)),
		"channel B's bucket must be back at 0 once Relay returns")
	assert.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), "chatcmpl-slot-retry",
		"the successful second attempt must have written the upstream response")
}

// ---------------------------------------------------------------------------
// CHN-02 tracer: saturation rejection switches to the next channel (02-15-01)
// ---------------------------------------------------------------------------

// TestRelaySaturationSwitchesToNextChannelWithoutClientError pins the CHN-02
// minimal loop: channel A's bucket is held full by a sibling while channel B
// stays idle, and both are same-priority candidates for one model (A weighted
// so high that any un-excluded selection would re-pick the saturated channel —
// the defect this plan's exclusion set exists for). One real Relay request must
// come back as an upstream 200 — the refusal on A is invisible to the client —
// with use_channel exactly [A, B] (A tried once, no repeat) and the recording
// limiter observing one rejection on channel:A plus one grant on channel:B,
// both buckets drained once Relay returns. A second request after A's bucket is
// released must select A again: the exclusion set lives exactly one client
// request and never sticks (T-02-15-05).
func TestRelaySaturationSwitchesToNextChannelWithoutClientError(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := newChannelSlotDB(t)
	pinRelayRetryBudget(t)

	const modelName = "slot-saturation-model"
	previousRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{"%s":0}`, modelName)))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })

	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-saturation","object":"chat.completion","created":1,"model":"`+modelName+
			`","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
	}))
	t.Cleanup(upstream.Close)
	service.InitHttpClient()

	const userID = 7341
	const channelAID = 7341
	const channelBID = 7342
	require.NoError(t, db.Create(&model.User{
		Id:       userID,
		Username: "slot-saturation-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    100000,
	}).Error)
	require.NoError(t, db.Create(&model.Token{
		Id:             userID,
		UserId:         userID,
		Key:            "slot-saturation-token-key",
		Name:           "slot-saturation-token",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}).Error)

	// Both channels are real same-priority candidates. A carries a crushing
	// weight so an un-excluded re-selection would overwhelmingly pick it again;
	// the exclusion set must make the retry land on B deterministically.
	weightA, weightB := uint(10000), uint(1)
	limitA, limitB := 1, 1
	channelA := &model.Channel{
		Id:               channelAID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "slot-saturation-channel-a-key",
		Name:             "slot-saturation-channel-a",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		Weight:           &weightA,
		ConcurrencyLimit: &limitA,
	}
	require.NoError(t, db.Create(channelA).Error)
	channelB := &model.Channel{
		Id:               channelBID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "slot-saturation-channel-b-key",
		Name:             "slot-saturation-channel-b",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		Weight:           &weightB,
		ConcurrencyLimit: &limitB,
	}
	require.NoError(t, db.Create(channelB).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelAID,
		Enabled:   true,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelBID,
		Enabled:   true,
	}).Error)

	lim := newChannelSlotLimiter()
	installChannelSlotLimiter(t, lim)
	scopeA := concurrency.ChannelScope(channelAID)
	scopeB := concurrency.ChannelScope(channelBID)

	// A's only slot is held by a sibling for the whole first request: every
	// attempt on A must be refused without a grant.
	siblingRelease, err := lim.inner.Acquire(context.Background(), scopeA, limitA)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+modelName+`","messages":[{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setSlotRelayIdentityKeys(c, userID, "slot-saturation-user", "slot-saturation-token-key")
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, channelA, modelName))

	Relay(c, types.RelayFormatOpenAI)

	assert.Equal(t, http.StatusOK, recorder.Code,
		"a saturated channel must be invisible to the client — the request succeeds on another channel")
	assert.Contains(t, recorder.Body.String(), "chatcmpl-saturation",
		"the client must receive the upstream success body")
	useChannel := c.GetStringSlice("use_channel")
	assert.Equal(t, []string{fmt.Sprint(channelAID), fmt.Sprint(channelBID)}, useChannel,
		"channel A must be attempted exactly once and the retry must switch to B (no repeat of the saturated channel)")
	records := lim.records()
	require.Len(t, records, 2, "two attempts must each acquire exactly one channel bucket")
	assert.Equal(t, scopeA, records[0].Scope, "attempt 1 must be refused on the first-hop channel A")
	assert.Equal(t, scopeB, records[1].Scope, "the retry must take channel B's slot, never re-take A")
	assert.Equal(t, 1, records[0].Limit, "attempt 1 must use A's published cap")
	assert.Equal(t, 1, records[1].Limit, "attempt 2 must use B's published cap")
	assert.LessOrEqual(t, int(lim.peakHeld()), 1, "the attempts must never hold slots at the same time")
	assert.EqualValues(t, 1, upstreamCalls.Load(), "only channel B reaches the upstream")
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scopeB),
		"channel B's bucket must be back at 0 once Relay returns")

	// Second request: A's bucket is released. The first request's exclusion
	// set must not cross the request boundary — A is selectable again.
	siblingRelease()
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scopeA),
		"channel A's bucket must be back at 0 once the sibling holder releases")

	recorder2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(recorder2)
	c2.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+modelName+`","messages":[{"role":"user","content":"hi"}]}`))
	c2.Request.Header.Set("Content-Type", "application/json")
	setSlotRelayIdentityKeys(c2, userID, "slot-saturation-user", "slot-saturation-token-key")
	require.Nil(t, middleware.SetupContextForSelectedChannel(c2, channelA, modelName))

	Relay(c2, types.RelayFormatOpenAI)

	assert.Equal(t, http.StatusOK, recorder2.Code, recorder2.Body.String())
	useChannel2 := c2.GetStringSlice("use_channel")
	assert.Equal(t, []string{fmt.Sprint(channelAID)}, useChannel2,
		"the second request must select A again — the exclusion set never sticks across requests")
	assert.EqualValues(t, 2, upstreamCalls.Load(), "request 2 must reach the upstream through channel A")
}

// ---------------------------------------------------------------------------
// Refund interplay: saturation rejection after pre-consume still refunds
// ---------------------------------------------------------------------------

// TestRelayChannelSaturationRefundsPreConsumedQuota pins the refund interplay
// asserted after reading .agents/rules/billing.md (T-02-14-03, plan Task
// 02-14-03): the channel slot is refused AFTER PrepareRequestBilling has
// pre-consumed quota, and the deferred RefundFailedRequestBilling must still
// give the reservation back — "rejected also refunds". The slot refusal never
// enters the attempt body (the pinned upstream must see zero calls), the
// rejection answers 429, the bucket drains to 0 once the sibling holder
// releases, and the token quota trace must show BOTH the pre-consume write
// (a value below the seed) and the refund write (back at the seed). Only the
// existing refund semantics are asserted — no billing formula or refund path
// is changed. Refund runs on a worker goroutine, so the test observes
// committed token writes through a gorm update callback plus a deadline loop
// (mirrors TestResponsesWebSocketInitialUpstreamRejectionRefundsReservation)
// instead of sleeping.
func TestRelayChannelSaturationRefundsPreConsumedQuota(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := newChannelSlotDB(t)
	pinRelayRetryBudget(t)

	// A paid model with token-count estimation on: pre-consume is
	// promptTokens * PreConsumeMultiplier * modelRatio (> 0), so the refusal
	// genuinely happens after a real reservation.
	previousCountToken := constant.CountToken
	constant.CountToken = true
	t.Cleanup(func() { constant.CountToken = previousCountToken })

	const modelName = "slot-refund-model"
	previousRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{"%s":2}`, modelName)))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })

	// The upstream must never be reached: a saturated bucket refuses before
	// the attempt body runs. Any call here is a leak of the refusal seam.
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(upstream.Close)
	service.InitHttpClient()

	const userID = 7321
	const channelID = 7321
	const initialTokenQuota = 50000
	require.NoError(t, db.Create(&model.User{
		Id:       userID,
		Username: "slot-refund-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    100000,
	}).Error)
	require.NoError(t, db.Create(&model.Token{
		Id:             userID, // token id == user id keeps setSlotRelayIdentityKeys aligned
		UserId:         userID,
		Key:            "slot-refund-token-key",
		Name:           "slot-refund-token",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		RemainQuota:    initialTokenQuota,
	}).Error)
	channelLimit := 1
	channel := &model.Channel{
		Id:               channelID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "slot-refund-channel-key",
		Name:             "slot-refund-channel",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		ConcurrencyLimit: &channelLimit,
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelID,
		Enabled:   true,
	}).Error)

	lim := newChannelSlotLimiter()
	installChannelSlotLimiter(t, lim)
	scope := concurrency.ChannelScope(channelID)

	// A sibling in-flight request holds the only slot of this bucket for the
	// whole test — every relay attempt must be refused without a grant.
	siblingRelease, err := lim.inner.Acquire(context.Background(), scope, channelLimit)
	require.NoError(t, err)
	t.Cleanup(siblingRelease)

	// Trace every committed write to the tokens table: the synchronous
	// pre-consume must show a value below the seed, the asynchronous refund
	// must land back on the seed.
	var traceMu sync.Mutex
	tokenTrace := make([]int, 0, 4)
	updates := make(chan struct{}, 8)
	require.NoError(t, db.Callback().Update().After("gorm:commit_or_rollback_transaction").Register("slot-refund-trace", func(tx *gorm.DB) {
		if tx.Error != nil || tx.Statement.Table != "tokens" {
			return
		}
		var tokenNow model.Token
		if err := model.DB.First(&tokenNow, userID).Error; err == nil {
			traceMu.Lock()
			tokenTrace = append(tokenTrace, tokenNow.RemainQuota)
			traceMu.Unlock()
		}
		select {
		case updates <- struct{}{}:
		default:
		}
	}))

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+modelName+`","messages":[{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setSlotRelayIdentityKeys(c, userID, "slot-refund-user", "slot-refund-token-key")
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, channel, modelName))

	Relay(c, types.RelayFormatOpenAI)

	// The refusal surfaces as the 429 saturation rejection and never reaches
	// the upstream or grants a slot.
	assert.Equal(t, http.StatusTooManyRequests, recorder.Code, recorder.Body.String())
	assert.EqualValues(t, 0, upstreamCalls.Load(),
		"a saturated bucket must refuse before the attempt body runs")
	assert.EqualValues(t, 0, lim.peakHeld(),
		"the refused request must never hold a slot of the saturated bucket")
	records := lim.records()
	require.NotEmpty(t, records, "each attempt must try to acquire the channel bucket")
	for i := range records {
		assert.Equal(t, scope, records[i].Scope, "attempt %d must target the channel bucket", i+1)
		assert.Equal(t, channelLimit, records[i].Limit, "attempt %d must use the published cap", i+1)
	}

	// The sibling holder leaves only after the request finished: the bucket
	// must be back at 0 — the refused attempts contributed no leak.
	siblingRelease()
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scope),
		"the channel bucket must drain to 0 once the last holder releases")

	// Quota before/after: wait for the async refund to commit, and require
	// that a pre-consume write (below the seed) was actually observed —
	// without it the equality assertion would be vacuous.
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		var tokenAfter model.Token
		require.NoError(t, db.First(&tokenAfter, userID).Error)
		traceMu.Lock()
		observed := append([]int(nil), tokenTrace...)
		traceMu.Unlock()
		sawPreConsume := false
		for _, quota := range observed {
			if quota < initialTokenQuota {
				sawPreConsume = true
				break
			}
		}
		if tokenAfter.RemainQuota == initialTokenQuota && sawPreConsume {
			break
		}
		select {
		case <-updates:
		case <-deadline.C:
			t.Fatalf("saturation rejection must refund the reservation: quota=%d trace=%v (want %d with a pre-consume write below it)",
				tokenAfter.RemainQuota, observed, initialTokenQuota)
		}
	}
}

// ---------------------------------------------------------------------------
// Load: bounded gauge and drain-to-zero (CHN-03/SC-3)
// ---------------------------------------------------------------------------

// TestChannelSlotHammerBoundedByLimitAndDrainsToZero hammers one channel
// bucket from many goroutines through the real runChannelSlotAttempt and
// pins the SC-3 load contract with deterministic counters (no sleep): the
// atomic gauge never exceeds L, granted+rejected equals the attempt count,
// and after the WaitGroup joins the snapshot is back at 0 (actual in-flight).
func TestChannelSlotHammerBoundedByLimitAndDrainsToZero(t *testing.T) {
	const channelID = 4242
	const limit = 3
	const workers = 8
	const rounds = 6
	scope := concurrency.ChannelScope(channelID)

	lim := newChannelSlotLimiter()
	installChannelSlotLimiter(t, lim)

	var granted, rejected atomic.Int32
	var wg sync.WaitGroup
	// One context per worker: the acquire path only reads it, and dedicated
	// contexts keep the race detector quiet about gin's Keys map.
	contexts := make([]*gin.Context, workers)
	for i := range workers {
		contexts[i] = slotContext(t, limit)
	}
	for i := range workers {
		c := contexts[i]
		wg.Go(func() {
			for range rounds {
				got := runChannelSlotAttempt(c, channelID, func() *types.NewAPIError {
					granted.Add(1)
					return nil
				})
				if got != nil {
					rejected.Add(1)
					assert.True(t, service.IsChannelConcurrencyRejection(got),
						"the only refusal of a healthy bucket is the saturation rejection")
				}
			}
		})
	}
	wg.Wait()

	attempts := int32(workers * rounds)
	assert.Equal(t, attempts, granted.Load()+rejected.Load(),
		"granted+rejected must account for every attempt")
	assert.GreaterOrEqual(t, granted.Load(), int32(1),
		"the bucket starts empty, so at least the first attempt must be granted")
	assert.LessOrEqual(t, int(lim.peakHeld()), limit,
		"the concurrent gauge must never exceed the bucket limit")
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scope),
		"after every worker joins the bucket must be back at 0 (actual in-flight)")
}

// ---------------------------------------------------------------------------
// All-saturated terminal: CNT-01 429 normalization (02-15-02)
// ---------------------------------------------------------------------------

// terminalErrorBody parses a relay terminal error response and returns the
// inner OpenAI-style error object, failing the test when the envelope shape is
// not what every non-Claude relay format writes.
func terminalErrorBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body),
		"the terminal body must be JSON: %s", recorder.Body.String())
	errObj, ok := body["error"].(map[string]any)
	require.True(t, ok, "the terminal body must be an OpenAI-style error envelope: %s", recorder.Body.String())
	return errObj
}

// TestRelayAllSaturationChannelsRenderTerminalCNT01429 pins the client-facing
// CNT-01 contract for the all-channels-saturated terminal (BCD D-4, T-02-15-01):
// with both same-model channels' buckets held full by siblings, one real Relay
// request must end in HTTP 429 whose body is the neutral CNT-01 envelope —
// type/code exactly "concurrency_exceeded", the fixed signal wording plus the
// request-id suffix — never the internal "channel:"-prefixed routing code and
// never a scope literal or channel id. The response must carry Retry-After,
// the refusal must never reach an upstream, and the request must have tried
// each channel exactly once before giving up.
func TestRelayAllSaturationChannelsRenderTerminalCNT01429(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := newChannelSlotDB(t)
	pinRelayRetryBudget(t)

	const modelName = "slot-all-sat-model"
	const requestID = "task2-all-sat-req"
	previousRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{"%s":0}`, modelName)))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })

	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(upstream.Close)
	service.InitHttpClient()

	const userID = 7361
	const channelAID = 7361
	const channelBID = 7362
	require.NoError(t, db.Create(&model.User{
		Id:       userID,
		Username: "slot-all-sat-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    100000,
	}).Error)
	require.NoError(t, db.Create(&model.Token{
		Id:             userID,
		UserId:         userID,
		Key:            "slot-all-sat-token-key",
		Name:           "slot-all-sat-token",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}).Error)

	limitA, limitB := 1, 1
	channelA := &model.Channel{
		Id:               channelAID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "slot-all-sat-channel-a-key",
		Name:             "slot-all-sat-channel-a",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		ConcurrencyLimit: &limitA,
	}
	require.NoError(t, db.Create(channelA).Error)
	channelB := &model.Channel{
		Id:               channelBID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "slot-all-sat-channel-b-key",
		Name:             "slot-all-sat-channel-b",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		ConcurrencyLimit: &limitB,
	}
	require.NoError(t, db.Create(channelB).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelAID,
		Enabled:   true,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelBID,
		Enabled:   true,
	}).Error)

	lim := newChannelSlotLimiter()
	installChannelSlotLimiter(t, lim)
	scopeA := concurrency.ChannelScope(channelAID)
	scopeB := concurrency.ChannelScope(channelBID)

	// Every candidate is saturated for the whole request: siblings hold both
	// buckets, so no attempt can ever be granted.
	siblingReleaseA, err := lim.inner.Acquire(context.Background(), scopeA, limitA)
	require.NoError(t, err)
	siblingReleaseB, err := lim.inner.Acquire(context.Background(), scopeB, limitB)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+modelName+`","messages":[{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setSlotRelayIdentityKeys(c, userID, "slot-all-sat-user", "slot-all-sat-token-key")
	c.Set(common.RequestIdKey, requestID)
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, channelA, modelName))

	Relay(c, types.RelayFormatOpenAI)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code, recorder.Body.String())
	errObj := terminalErrorBody(t, recorder)
	assert.Equal(t, "concurrency_exceeded", errObj["type"],
		"terminal type must be the CNT-01 value, not the internal error type")
	assert.Equal(t, "concurrency_exceeded", errObj["code"],
		"terminal code must be the CNT-01 value, not the internal channel:-prefixed routing code")
	wantMessage := common.MessageWithRequestId(service.NewChannelConcurrencyRejection().Error(), requestID)
	assert.Equal(t, wantMessage, errObj["message"],
		"terminal message must be the neutral signal wording plus the request-id suffix")
	assert.NotContains(t, recorder.Body.String(), "channel:",
		"the client-visible body must not leak the internal routing prefix")
	assert.NotContains(t, recorder.Body.String(), fmt.Sprint(channelAID),
		"the client-visible body must not leak a channel id")
	assert.NotContains(t, recorder.Body.String(), fmt.Sprint(channelBID),
		"the client-visible body must not leak a channel id")
	assert.NotEmpty(t, recorder.Header().Get("Retry-After"),
		"the all-saturated terminal 429 must carry a Retry-After hint")
	require.Equal(t, strconv.Itoa(middleware.ConcurrencyRetryAfterSeconds), recorder.Header().Get("Retry-After"),
		"Retry-After must be the exported middleware constant (single source), never a re-literalized value")
	assert.EqualValues(t, 0, upstreamCalls.Load(),
		"an all-saturated request must never reach an upstream")
	useChannel := c.GetStringSlice("use_channel")
	assert.Equal(t, []string{fmt.Sprint(channelAID), fmt.Sprint(channelBID)}, useChannel,
		"the request must try each channel exactly once before the terminal")
	records := lim.records()
	require.Len(t, records, 2, "each attempt must try to acquire exactly one channel bucket")
	assert.Equal(t, scopeA, records[0].Scope, "attempt 1 must refuse on the first-hop channel A")
	assert.Equal(t, scopeB, records[1].Scope, "attempt 2 must refuse on the re-selected channel B")
	siblingReleaseA()
	siblingReleaseB()
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scopeA),
		"channel A's bucket must drain once the sibling holder releases")
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scopeB),
		"channel B's bucket must drain once the sibling holder releases")
}

// TestRelayEmptySelectionAfterSaturationClassifiesAs429 pins the CHN-02
// empty-selection rule at the getChannel boundary: the first attempt dispatches
// on the context-pinned channel A (no ability row of its own) and fails
// retryably, the re-selection lands on the only able channel B whose bucket is
// held full by a sibling (recorded into the exclusion set), and the next
// selection has no candidate left while the exclusion set is non-empty — that
// combination means "every remaining candidate was refused for concurrency" and
// must terminate as the CNT-01 429 rejection, not as a misleading
// "channel does not exist" error. An empty-exclusion empty selection keeps the
// existing error (pinned by TestRelayEmptySelectionWithoutSaturationKeepsExistingError).
func TestRelayEmptySelectionAfterSaturationClassifiesAs429(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := newChannelSlotDB(t)
	// Attempt budget: dispatch on A (1) → refuse on B (2) → empty selection
	// classification (3).
	pinRelayRetryBudgetWithRetries(t, 2)

	const modelName = "slot-empty-sel-model"
	const requestID = "task2-empty-sel-req"
	previousRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{"%s":0}`, modelName)))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })

	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if upstreamCalls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-slot-empty-sel","object":"chat.completion","created":1,"model":"`+modelName+
			`","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
	}))
	t.Cleanup(upstream.Close)
	service.InitHttpClient()

	const userID = 7371
	const channelAID = 7371
	const channelBID = 7372
	require.NoError(t, db.Create(&model.User{
		Id:       userID,
		Username: "slot-empty-sel-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    100000,
	}).Error)
	require.NoError(t, db.Create(&model.Token{
		Id:             userID,
		UserId:         userID,
		Key:            "slot-empty-sel-token-key",
		Name:           "slot-empty-sel-token",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}).Error)

	// Channel A seeds the first hop only (no ability row): the database
	// selection can never pick it, so attempt 2 lands on B deterministically
	// and attempt 3's selection has no non-excluded candidate at all.
	limitA, limitB := 1, 1
	channelA := &model.Channel{
		Id:               channelAID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "slot-empty-sel-channel-a-key",
		Name:             "slot-empty-sel-channel-a",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		ConcurrencyLimit: &limitA,
	}
	require.NoError(t, db.Create(channelA).Error)
	channelB := &model.Channel{
		Id:               channelBID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "slot-empty-sel-channel-b-key",
		Name:             "slot-empty-sel-channel-b",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		ConcurrencyLimit: &limitB,
	}
	require.NoError(t, db.Create(channelB).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelBID,
		Enabled:   true,
	}).Error)

	lim := newChannelSlotLimiter()
	installChannelSlotLimiter(t, lim)
	scopeA := concurrency.ChannelScope(channelAID)
	scopeB := concurrency.ChannelScope(channelBID)

	// B's bucket is saturated for the whole request; A stays free so attempt 1
	// really dispatches (which is what distinguishes this classification path
	// from the first-hop fallback).
	siblingReleaseB, err := lim.inner.Acquire(context.Background(), scopeB, limitB)
	require.NoError(t, err)
	t.Cleanup(siblingReleaseB)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+modelName+`","messages":[{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setSlotRelayIdentityKeys(c, userID, "slot-empty-sel-user", "slot-empty-sel-token-key")
	c.Set(common.RequestIdKey, requestID)
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, channelA, modelName))

	Relay(c, types.RelayFormatOpenAI)

	assert.Equal(t, http.StatusTooManyRequests, recorder.Code, recorder.Body.String())
	assert.NotEmpty(t, recorder.Header().Get("Retry-After"),
		"the all-saturated terminal 429 must carry a Retry-After hint")
	errObj := terminalErrorBody(t, recorder)
	assert.Equal(t, "concurrency_exceeded", errObj["type"],
		"an empty selection with a non-empty exclusion set must classify as the concurrency rejection")
	assert.Equal(t, "concurrency_exceeded", errObj["code"],
		"an empty selection with a non-empty exclusion set must classify as the concurrency rejection")
	wantMessage := common.MessageWithRequestId(service.NewChannelConcurrencyRejection().Error(), requestID)
	assert.Equal(t, wantMessage, errObj["message"])
	assert.NotContains(t, recorder.Body.String(), "channel:",
		"the client-visible body must not leak the internal routing prefix")
	assert.NotContains(t, recorder.Body.String(), fmt.Sprint(channelAID))
	assert.NotContains(t, recorder.Body.String(), fmt.Sprint(channelBID))
	assert.EqualValues(t, 1, upstreamCalls.Load(),
		"only the first attempt reaches the upstream; the refused and empty-selection hops never do")
	useChannel := c.GetStringSlice("use_channel")
	assert.Equal(t, []string{fmt.Sprint(channelAID), fmt.Sprint(channelBID)}, useChannel,
		"the classification hop must not re-take any channel")
	records := lim.records()
	require.Len(t, records, 2, "exactly two attempts ever touch a bucket (dispatch on A, refusal on B)")
	assert.Equal(t, scopeA, records[0].Scope, "attempt 1 must dispatch on the first-hop channel A")
	assert.Equal(t, scopeB, records[1].Scope, "attempt 2 must refuse on the re-selected channel B")
	assert.LessOrEqual(t, int(lim.peakHeld()), 1, "at most one slot is ever held")
	siblingReleaseB()
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scopeA),
		"channel A's bucket must drain once its attempt releases")
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scopeB),
		"channel B's bucket must drain once the sibling holder releases")
}

// TestRelayEmptySelectionWithoutSaturationKeepsExistingError pins the other
// half of the empty-selection rule: when no channel was ever refused for
// saturation (empty exclusion set), an empty selection must keep today's
// "channel does not exist" classification — a request that simply has no
// candidate must never be misreported as a concurrency 429. This test passes
// on the current disk state and is a regression pin (green-on-arrival), not a
// red point.
func TestRelayEmptySelectionWithoutSaturationKeepsExistingError(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := newChannelSlotDB(t)
	pinRelayRetryBudget(t)

	const modelName = "slot-no-sat-model"
	const requestID = "task2-no-sat-req"
	previousRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{"%s":0}`, modelName)))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })

	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(upstream.Close)
	service.InitHttpClient()

	const userID = 7381
	const channelAID = 7381
	require.NoError(t, db.Create(&model.User{
		Id:       userID,
		Username: "slot-no-sat-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    100000,
	}).Error)
	require.NoError(t, db.Create(&model.Token{
		Id:             userID,
		UserId:         userID,
		Key:            "slot-no-sat-token-key",
		Name:           "slot-no-sat-token",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}).Error)

	// The context-pinned channel has no ability row and no other candidate
	// exists, so once the first hop has dispatched (ChannelMeta set), the next
	// selection is empty with a never-used exclusion set.
	limitA := 1
	channelA := &model.Channel{
		Id:               channelAID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "slot-no-sat-channel-a-key",
		Name:             "slot-no-sat-channel-a",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		ConcurrencyLimit: &limitA,
	}
	require.NoError(t, db.Create(channelA).Error)

	lim := newChannelSlotLimiter()
	installChannelSlotLimiter(t, lim)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+modelName+`","messages":[{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setSlotRelayIdentityKeys(c, userID, "slot-no-sat-user", "slot-no-sat-token-key")
	c.Set(common.RequestIdKey, requestID)
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, channelA, modelName))

	Relay(c, types.RelayFormatOpenAI)

	require.Equal(t, http.StatusInternalServerError, recorder.Code, recorder.Body.String(),
		"a non-saturation empty selection must keep the existing channel-missing classification")
	assert.Empty(t, recorder.Header().Get("Retry-After"),
		"Retry-After belongs to the concurrency terminal only")
	errObj := terminalErrorBody(t, recorder)
	assert.Equal(t, "get_channel_failed", errObj["code"],
		"the existing get-channel error code must be preserved when nothing was ever saturated")
	assert.NotContains(t, recorder.Body.String(), "concurrency_exceeded",
		"a non-saturation empty selection must never be reported as a concurrency rejection")
	assert.EqualValues(t, 1, upstreamCalls.Load(),
		"only the first attempt reaches the upstream")
	useChannel := c.GetStringSlice("use_channel")
	assert.Equal(t, []string{fmt.Sprint(channelAID)}, useChannel,
		"the classification hop must not take a channel")
}

// ---------------------------------------------------------------------------
// Terminal rendering shape: CNT-01 envelopes per relay format (02-15-02)
// ---------------------------------------------------------------------------

// TestSaturationRejectionRendersCNT01ByRelayFormat drives the terminal renderer
// directly for every relay format (BCD D-4, T-02-15-01): default and Gemini
// write the OpenAI envelope {"error":{message,type,param,code}} with type/code
// exactly "concurrency_exceeded", Claude writes the Anthropic envelope
// {"type":"error","error":{type,message}}, and Realtime carries the same
// OpenAIError inside its WS error frame (the transport is upgraded before any
// terminal write, so no HTTP header is possible there — the frame body is the
// contract surface). Every HTTP shape asserts the Retry-After value equals the
// exported middleware constant (single source), and a non-saturation
// channel:-prefixed fault keeps its existing rendering untouched.
func TestSaturationRejectionRendersCNT01ByRelayFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const requestID = "task2-envelope-req"
	wantMessage := common.MessageWithRequestId(service.NewChannelConcurrencyRejection().Error(), requestID)
	wantRetryAfter := strconv.Itoa(middleware.ConcurrencyRetryAfterSeconds)
	newSignal := func() *types.NewAPIError {
		err := service.NewChannelConcurrencyRejection()
		err.SetMessage(common.MessageWithRequestId(err.Error(), requestID))
		return err
	}
	// Four saturation renders below shed one counted 429 each; the
	// non-saturation fault at the end must not move the counter.
	rejectionsBefore := concurrency.RejectionCount()

	assertOpenAIEnvelope := func(t *testing.T, recorder *httptest.ResponseRecorder) {
		t.Helper()
		require.Equal(t, http.StatusTooManyRequests, recorder.Code, recorder.Body.String())
		require.Equal(t, wantRetryAfter, recorder.Header().Get("Retry-After"),
			"the terminal 429 must carry the single-source Retry-After hint")
		errObj := terminalErrorBody(t, recorder)
		require.Equal(t, "concurrency_exceeded", errObj["type"],
			"the CNT-01 envelope must use the neutral type, never the internal error type")
		require.Equal(t, "concurrency_exceeded", errObj["code"],
			"the CNT-01 envelope must use the neutral code, never the channel:-prefixed routing code")
		require.Equal(t, wantMessage, errObj["message"])
		require.Equal(t, "", errObj["param"])
		require.NotContains(t, recorder.Body.String(), "channel:",
			"the client-visible body must not leak the internal routing prefix")
	}

	t.Run("default OpenAI envelope", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		renderRelayTerminalError(c, types.RelayFormatOpenAI, nil, newSignal())
		assertOpenAIEnvelope(t, recorder)
	})

	t.Run("Gemini reuses the default envelope", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		renderRelayTerminalError(c, types.RelayFormatGemini, nil, newSignal())
		assertOpenAIEnvelope(t, recorder)
	})

	t.Run("Claude envelope", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		renderRelayTerminalError(c, types.RelayFormatClaude, nil, newSignal())
		require.Equal(t, http.StatusTooManyRequests, recorder.Code, recorder.Body.String())
		require.Equal(t, wantRetryAfter, recorder.Header().Get("Retry-After"),
			"the Claude terminal 429 must carry the single-source Retry-After hint")
		var body map[string]any
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body),
			"the terminal body must be JSON: %s", recorder.Body.String())
		require.Equal(t, "error", body["type"], "the Claude envelope wraps the error in type:\"error\"")
		errObj, ok := body["error"].(map[string]any)
		require.True(t, ok, "the Claude envelope must carry an error object: %s", recorder.Body.String())
		require.Equal(t, "concurrency_exceeded", errObj["type"])
		require.Equal(t, wantMessage, errObj["message"])
		require.NotContains(t, recorder.Body.String(), "channel:",
			"the client-visible body must not leak the internal routing prefix")
	})

	t.Run("Realtime WS error frame", func(t *testing.T) {
		handlerErr := make(chan error, 1)
		engine := gin.New()
		engine.GET("/render", func(c *gin.Context) {
			ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
			if err != nil {
				handlerErr <- err
				return
			}
			defer ws.Close()
			c.Set(common.RequestIdKey, requestID)
			renderRelayTerminalError(c, types.RelayFormatOpenAIRealtime, ws, newSignal())
			handlerErr <- nil
		})
		server := httptest.NewServer(engine)
		t.Cleanup(server.Close)

		client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/render", nil)
		require.NoError(t, err)
		defer client.Close()
		require.NoError(t, client.SetReadDeadline(time.Now().Add(5*time.Second)))
		_, frame, err := client.ReadMessage()
		require.NoError(t, err, "the realtime terminal must deliver a WS error frame")
		require.NoError(t, <-handlerErr)

		var event map[string]any
		require.NoError(t, common.Unmarshal(frame, &event), "the frame must be JSON: %s", frame)
		require.Equal(t, "error", event["type"], "the realtime error frame uses type:\"error\"")
		errObj, ok := event["error"].(map[string]any)
		require.True(t, ok, "the frame must carry an error object: %s", frame)
		require.Equal(t, "concurrency_exceeded", errObj["type"],
			"the realtime frame must carry the CNT-01 type")
		require.Equal(t, "concurrency_exceeded", errObj["code"],
			"the realtime frame must carry the CNT-01 code, never the channel:-prefixed routing code")
		require.Equal(t, wantMessage, errObj["message"])
		require.NotContains(t, string(frame), "channel:",
			"the frame must not leak the internal routing prefix")
	})

	t.Run("non-saturation channel fault keeps its rendering", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		fault := types.NewError(errors.New("invalid api key"), types.ErrorCodeChannelInvalidKey,
			types.ErrOptionWithStatusCode(http.StatusUnauthorized))
		renderRelayTerminalError(c, types.RelayFormatOpenAI, nil, fault)
		require.Equal(t, http.StatusUnauthorized, recorder.Code, recorder.Body.String())
		require.Empty(t, recorder.Header().Get("Retry-After"),
			"Retry-After belongs to the concurrency terminal only")
		errObj := terminalErrorBody(t, recorder)
		require.Equal(t, "channel:invalid_key", errObj["code"],
			"other channel: fault codes must keep their existing rendering (no normalization)")
		require.NotContains(t, recorder.Body.String(), "concurrency_exceeded",
			"the concurrency envelope must never swallow other error classes")
	})

	require.Equal(t, rejectionsBefore+4, concurrency.RejectionCount(),
		"each terminal concurrency 429 render must count exactly one shed request; "+
			"the non-saturation fault must not")
}

// ---------------------------------------------------------------------------
// Task 02-15-03: refund quota before/after + regression net
// (BCD D-5 / T-02-15-02 / T-02-15-04; .agents/rules/billing.md read in full
// before writing these — they assert real quota deltas only and never touch a
// billing formula, the pre-consume path, or the refund path)
// ---------------------------------------------------------------------------

// seedSlotRelayUserToken seeds the user + token pair the relay e2e fixtures in
// this file share: an enabled default-group user holding a wallet quota plus a
// matched enabled token whose id equals the user id (so setSlotRelayIdentityKeys'
// token_id lines up). remainQuota seeds the token's billable balance that the
// quota before/after assertions measure.
func seedSlotRelayUserToken(t *testing.T, db *gorm.DB, userID int, name string, userQuota, remainQuota int) {
	t.Helper()
	require.NoError(t, db.Create(&model.User{
		Id:       userID,
		Username: name + "-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    userQuota,
	}).Error)
	require.NoError(t, db.Create(&model.Token{
		Id:             userID,
		UserId:         userID,
		Key:            name + "-token-key",
		Name:           name + "-token",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		RemainQuota:    remainQuota,
	}).Error)
}

// TestRelayAllSaturationTerminal429RefundsPreConsumedQuota extends the 02-14
// single-channel refund pin to the 02-15 all-channels-saturated terminal path
// (T-02-15-02): a paid model pre-consumes quota, BOTH same-model channels
// refuse the request for concurrency, and the terminal answer is the
// normalized CNT-01 429 from the Task 02 renderer — yet the deferred
// RefundFailedRequestBilling must still give the reservation back. The token
// AND wallet balances must return exactly to their seeds (real quota deltas,
// never a billing formula), a pre-consume write below the seed must have been
// observed so the equality is not vacuous, no attempt may ever hold a slot,
// and both channel buckets must drain to 0 once the sibling holders release.
// Only existing refund semantics are asserted — no billing formula or refund
// path is changed. Refund runs on a worker goroutine, so the test observes
// committed writes through a gorm update callback plus a deadline loop
// (mirrors TestRelayChannelSaturationRefundsPreConsumedQuota) — no sleeping.
func TestRelayAllSaturationTerminal429RefundsPreConsumedQuota(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := newChannelSlotDB(t)
	pinRelayRetryBudget(t)

	previousCountToken := constant.CountToken
	constant.CountToken = true
	t.Cleanup(func() { constant.CountToken = previousCountToken })

	const modelName = "slot-all-sat-refund-model"
	const requestID = "task3-all-sat-refund-req"
	previousRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{"%s":2}`, modelName)))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })

	// A saturated bucket refuses before the attempt body runs, so the pinned
	// upstream must see zero calls — any call here is a leak of the seam.
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(upstream.Close)
	service.InitHttpClient()

	const userID = 7391
	const channelAID = 7391
	const channelBID = 7392
	const initialTokenQuota = 50000
	const initialUserQuota = 100000
	seedSlotRelayUserToken(t, db, userID, "slot-all-sat-refund", initialUserQuota, initialTokenQuota)

	limitA, limitB := 1, 1
	channelA := &model.Channel{
		Id:               channelAID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "slot-all-sat-refund-channel-a-key",
		Name:             "slot-all-sat-refund-channel-a",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		ConcurrencyLimit: &limitA,
	}
	require.NoError(t, db.Create(channelA).Error)
	channelB := &model.Channel{
		Id:               channelBID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "slot-all-sat-refund-channel-b-key",
		Name:             "slot-all-sat-refund-channel-b",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		ConcurrencyLimit: &limitB,
	}
	require.NoError(t, db.Create(channelB).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelAID,
		Enabled:   true,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelBID,
		Enabled:   true,
	}).Error)

	lim := newChannelSlotLimiter()
	installChannelSlotLimiter(t, lim)
	scopeA := concurrency.ChannelScope(channelAID)
	scopeB := concurrency.ChannelScope(channelBID)

	// Sibling holders keep BOTH buckets full for the whole request: every
	// attempt must be refused without a grant.
	siblingReleaseA, err := lim.inner.Acquire(context.Background(), scopeA, limitA)
	require.NoError(t, err)
	t.Cleanup(siblingReleaseA)
	siblingReleaseB, err := lim.inner.Acquire(context.Background(), scopeB, limitB)
	require.NoError(t, err)
	t.Cleanup(siblingReleaseB)

	// Trace every committed write to the tokens table: the synchronous
	// pre-consume must show a value below the seed, the asynchronous refund
	// must land back on the seed.
	var traceMu sync.Mutex
	tokenTrace := make([]int, 0, 4)
	updates := make(chan struct{}, 8)
	require.NoError(t, db.Callback().Update().After("gorm:commit_or_rollback_transaction").Register("task3-all-sat-refund-trace", func(tx *gorm.DB) {
		if tx.Error != nil || tx.Statement.Table != "tokens" {
			return
		}
		var tokenNow model.Token
		if err := model.DB.First(&tokenNow, userID).Error; err == nil {
			traceMu.Lock()
			tokenTrace = append(tokenTrace, tokenNow.RemainQuota)
			traceMu.Unlock()
		}
		select {
		case updates <- struct{}{}:
		default:
		}
	}))

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+modelName+`","messages":[{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setSlotRelayIdentityKeys(c, userID, "slot-all-sat-refund-user", "slot-all-sat-refund-token-key")
	c.Set(common.RequestIdKey, requestID)
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, channelA, modelName))

	Relay(c, types.RelayFormatOpenAI)

	// Terminal answer: the Task 02 normalized CNT-01 429, not a raw signal and
	// not a "channel does not exist" error.
	require.Equal(t, http.StatusTooManyRequests, recorder.Code, recorder.Body.String())
	errObj := terminalErrorBody(t, recorder)
	assert.Equal(t, "concurrency_exceeded", errObj["type"],
		"the terminal type must be the CNT-01 value")
	assert.Equal(t, "concurrency_exceeded", errObj["code"],
		"the terminal code must be the CNT-01 value, not the internal channel:-prefixed routing code")
	wantMessage := common.MessageWithRequestId(service.NewChannelConcurrencyRejection().Error(), requestID)
	assert.Equal(t, wantMessage, errObj["message"],
		"the terminal message must be the neutral wording plus the request-id suffix")
	assert.NotContains(t, recorder.Body.String(), "channel:",
		"the client-visible body must not leak the internal routing prefix")
	assert.NotContains(t, recorder.Body.String(), fmt.Sprint(channelAID),
		"the client-visible body must not leak a channel id")
	assert.NotContains(t, recorder.Body.String(), fmt.Sprint(channelBID),
		"the client-visible body must not leak a channel id")
	require.Equal(t, strconv.Itoa(middleware.ConcurrencyRetryAfterSeconds), recorder.Header().Get("Retry-After"),
		"the terminal 429 must carry the single-source Retry-After hint")

	assert.EqualValues(t, 0, upstreamCalls.Load(),
		"an all-saturated request must never reach an upstream")
	useChannel := c.GetStringSlice("use_channel")
	assert.Equal(t, []string{fmt.Sprint(channelAID), fmt.Sprint(channelBID)}, useChannel,
		"the request must try each channel exactly once before the terminal")
	records := lim.records()
	require.Len(t, records, 2, "each attempt must try to acquire exactly one channel bucket")
	assert.Equal(t, scopeA, records[0].Scope, "attempt 1 must refuse on the first-hop channel A")
	assert.Equal(t, scopeB, records[1].Scope, "attempt 2 must refuse on the re-selected channel B")
	assert.EqualValues(t, 0, lim.peakHeld(),
		"the refused request must never hold a slot of a saturated bucket")

	siblingReleaseA()
	siblingReleaseB()
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scopeA),
		"channel A's bucket must drain once the sibling holder releases")
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scopeB),
		"channel B's bucket must drain once the sibling holder releases")

	// Quota before/after: wait for the async refund to commit, and require
	// that a pre-consume write (below the seed) was actually observed —
	// without it the equality assertion would be vacuous. BillingSession.Refund
	// restores the funding source (wallet) BEFORE the token balance inside one
	// worker closure, so observing the token back at its seed proves the wallet
	// side is already back too — that ordering is what makes the wallet
	// equality below deterministic rather than racy.
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		var tokenAfter model.Token
		require.NoError(t, db.First(&tokenAfter, userID).Error)
		var userAfter model.User
		require.NoError(t, db.First(&userAfter, userID).Error)
		traceMu.Lock()
		observed := append([]int(nil), tokenTrace...)
		traceMu.Unlock()
		sawPreConsume := false
		for _, quota := range observed {
			if quota < initialTokenQuota {
				sawPreConsume = true
				break
			}
		}
		if tokenAfter.RemainQuota == initialTokenQuota && sawPreConsume {
			require.Equal(t, initialUserQuota, userAfter.Quota,
				"the wallet side of the reservation must be refunded to its seed as well")
			break
		}
		select {
		case <-updates:
		case <-deadline.C:
			t.Fatalf("all-saturated terminal 429 must refund the reservation: token=%d user=%d trace=%v (want token %d with a pre-consume write below it)",
				tokenAfter.RemainQuota, userAfter.Quota, observed, initialTokenQuota)
		}
	}
}

// TestRelaySaturationSwitchBillsExactlyOncePerRequest pins the other half of
// BCD D-5: when a saturation refusal makes the request switch channels and
// still succeed, the client is billed exactly ONCE for that request — the
// billing session is created once before the retry loop and retained across
// attempts, so the quota delta of the two-attempt switch request must equal
// the quota delta of a plain one-attempt control request on the same fixture
// (a per-attempt re-pre-consume or a missing refund would double it). Asserted
// on both real balances — the token's RemainQuota and the user's wallet quota
// — with a positive single charge, and every channel bucket drained to 0 at
// the end. Only existing billing semantics are asserted; no formula changes.
func TestRelaySaturationSwitchBillsExactlyOncePerRequest(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := newChannelSlotDB(t)
	pinRelayRetryBudget(t)

	previousCountToken := constant.CountToken
	constant.CountToken = true
	t.Cleanup(func() { constant.CountToken = previousCountToken })

	const modelName = "slot-switch-billing-model"
	previousRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{"%s":2}`, modelName)))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })

	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-slot-switch-billing","object":"chat.completion","created":1,"model":"`+modelName+
			`","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
	}))
	t.Cleanup(upstream.Close)
	service.InitHttpClient()

	const userID = 7401
	const channelAID = 7401
	const channelBID = 7402
	const initialTokenQuota = 50000
	const initialUserQuota = 100000
	seedSlotRelayUserToken(t, db, userID, "slot-switch-billing", initialUserQuota, initialTokenQuota)

	// A carries a crushing weight so an un-excluded re-selection would
	// overwhelmingly re-pick the saturated channel; the exclusion set must make
	// the retry land on B deterministically.
	weightA, weightB := uint(10000), uint(1)
	limitA, limitB := 1, 1
	channelA := &model.Channel{
		Id:               channelAID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "slot-switch-billing-channel-a-key",
		Name:             "slot-switch-billing-channel-a",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		Weight:           &weightA,
		ConcurrencyLimit: &limitA,
	}
	require.NoError(t, db.Create(channelA).Error)
	channelB := &model.Channel{
		Id:               channelBID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "slot-switch-billing-channel-b-key",
		Name:             "slot-switch-billing-channel-b",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		Weight:           &weightB,
		ConcurrencyLimit: &limitB,
	}
	require.NoError(t, db.Create(channelB).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelAID,
		Enabled:   true,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelBID,
		Enabled:   true,
	}).Error)

	lim := newChannelSlotLimiter()
	installChannelSlotLimiter(t, lim)
	scopeA := concurrency.ChannelScope(channelAID)
	scopeB := concurrency.ChannelScope(channelBID)

	readQuotas := func() (tokenQuota, userQuota int) {
		t.Helper()
		var token model.Token
		require.NoError(t, db.First(&token, userID).Error)
		var user model.User
		require.NoError(t, db.First(&user, userID).Error)
		return token.RemainQuota, user.Quota
	}
	runRequest := func(t *testing.T) (*httptest.ResponseRecorder, *gin.Context) {
		t.Helper()
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			strings.NewReader(`{"model":"`+modelName+`","messages":[{"role":"user","content":"hi"}]}`))
		c.Request.Header.Set("Content-Type", "application/json")
		setSlotRelayIdentityKeys(c, userID, "slot-switch-billing-user", "slot-switch-billing-token-key")
		require.Nil(t, middleware.SetupContextForSelectedChannel(c, channelA, modelName))
		Relay(c, types.RelayFormatOpenAI)
		return recorder, c
	}

	// --- Request 1 (control): channel A idle, one attempt, plain single
	// billing. This is the "单次计费" yardstick the switch request is measured
	// against — same fixture, same prompt, same upstream usage.
	token0, user0 := readQuotas()
	recorder1, c1 := runRequest(t)
	require.Equal(t, http.StatusOK, recorder1.Code, recorder1.Body.String())
	assert.Equal(t, []string{fmt.Sprint(channelAID)}, c1.GetStringSlice("use_channel"),
		"the control request must run on the first-hop channel A in one attempt")
	token1, user1 := readQuotas()
	chargeToken1 := token0 - token1
	chargeUser1 := user0 - user1
	assert.Greater(t, chargeToken1, 0, "the control request must bill a real single charge")
	assert.Greater(t, chargeUser1, 0, "the control request must bill a real single charge on the wallet side")

	// --- Request 2 (switch): A's only slot is held by a sibling, so the
	// request refuses on A and must succeed on B — two attempts, one client
	// request. The quota delta must equal the control's single charge: the
	// billing session spans attempts, so a per-attempt double charge would show
	// up here as a doubled delta.
	siblingRelease, err := lim.inner.Acquire(context.Background(), scopeA, limitA)
	require.NoError(t, err)
	t.Cleanup(siblingRelease)

	recorder2, c2 := runRequest(t)
	require.Equal(t, http.StatusOK, recorder2.Code, recorder2.Body.String())
	assert.Equal(t, []string{fmt.Sprint(channelAID), fmt.Sprint(channelBID)}, c2.GetStringSlice("use_channel"),
		"the switch request must refuse A exactly once and succeed on B")
	token2, user2 := readQuotas()
	chargeToken2 := token1 - token2
	chargeUser2 := user1 - user2
	assert.Greater(t, chargeToken2, 0, "the switch request must bill a real charge")
	assert.Equal(t, chargeToken1, chargeToken2,
		"the two-attempt switch request must bill exactly once — the quota delta must equal the single-charge control")
	assert.Equal(t, chargeUser1, chargeUser2,
		"the wallet-side delta must also equal the single-charge control (billing session spans attempts)")

	assert.EqualValues(t, 2, upstreamCalls.Load(),
		"the control request and the switch request's B attempt must be the only upstream calls")
	records := lim.records()
	require.Len(t, records, 3, "control: one acquire on A; switch: refusal on A + grant on B")
	assert.Equal(t, scopeA, records[0].Scope, "control attempt must take channel A's slot")
	assert.Equal(t, scopeA, records[1].Scope, "switch attempt 1 must target channel A")
	assert.Equal(t, scopeB, records[2].Scope, "switch attempt 2 must take channel B's slot")
	assert.LessOrEqual(t, int(lim.peakHeld()), 1,
		"at most one slot is ever held (sequential requests, limit=1 buckets)")

	siblingRelease()
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scopeA),
		"channel A's bucket must drain once the sibling holder releases")
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scopeB),
		"channel B's bucket must be back at 0 after the successful switch attempt")
}

// TestRelaySaturationExemptsChannelAutoDisableEndToEnd pins the T-02-15-04
// exemption at integration level: this plan ROUTES many more saturation
// rejections through the 02-14 chokepoint (every refused attempt now feeds the
// exclusion set), which amplifies the "busier a channel gets the faster it is
// auto-disabled" DoS surface if ShouldDisableChannel's saturation exclusion ever
// regresses. The test runs with common.AutomaticDisableChannelEnabled ON — an
// anti-vacuity guard first proves a REAL channel fault is still judged
// disableable under that flag — then drives a switch request (A refused) and an
// all-saturated request (A and B refused) through the real Relay and asserts
// BOTH trigger lines stayed silent: every saturation failure event carries
// Health "unchanged" (never channel/key_disable_requested — the synchronous,
// deterministic discriminator) and both channels still read Enabled through
// model.CacheGetChannel (the cache/DB unified read) after the requests. The
// service-level table-driven pins (ShouldDisableChannel chokepoint and both
// trigger lines) stay green in the service suite — together the two layers keep
// "saturation never auto-disables" regression-proof.
func TestRelaySaturationExemptsChannelAutoDisableEndToEnd(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := newChannelSlotDB(t)

	// Retry pin WITHOUT the usual auto-disable kill switch: this test needs
	// AutomaticDisableChannelEnabled ON, because the exemption chokepoint is
	// exactly what must stop the rejection from reaching DisableChannel.
	previousRetryTimes := common.RetryTimes
	common.RetryTimes = 1
	previousStatusCodes := operation_setting.AutomaticRetryStatusCodesToString()
	require.NoError(t, operation_setting.AutomaticRetryStatusCodesFromString("500"))
	previousAutoDisable := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() {
		common.RetryTimes = previousRetryTimes
		require.NoError(t, operation_setting.AutomaticRetryStatusCodesFromString(previousStatusCodes))
		common.AutomaticDisableChannelEnabled = previousAutoDisable
	})

	// Anti-vacuity: with the flag ON, a real channel fault must still be judged
	// disableable — otherwise "nothing got disabled" below would prove nothing.
	require.True(t, service.ShouldDisableChannel(
		types.NewError(errors.New("invalid api key"), types.ErrorCodeChannelInvalidKey)),
		"auto-disable must be armed in this test for the saturation exemption to mean anything")

	const modelName = "slot-autoban-exempt-model"
	previousRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{"%s":0}`, modelName)))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })

	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-autoban-exempt","object":"chat.completion","created":1,"model":"`+modelName+
			`","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
	}))
	t.Cleanup(upstream.Close)
	service.InitHttpClient()

	const userID = 7411
	const channelAID = 7411
	const channelBID = 7412
	seedSlotRelayUserToken(t, db, userID, "slot-autoban-exempt", 100000, 0)

	// AutoBan is set EXPLICITLY on both channels: GetAutoBan() returns false for
	// a nil field, which would disarm DisableChannel at the call site and turn
	// this pin vacuous. auto_ban:1 mirrors the production column default.
	autoBanOn := 1
	limitA, limitB := 1, 1
	channelA := &model.Channel{
		Id:               channelAID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "slot-autoban-exempt-channel-a-key",
		Name:             "slot-autoban-exempt-channel-a",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		ConcurrencyLimit: &limitA,
		AutoBan:          &autoBanOn,
	}
	require.NoError(t, db.Create(channelA).Error)
	channelB := &model.Channel{
		Id:               channelBID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "slot-autoban-exempt-channel-b-key",
		Name:             "slot-autoban-exempt-channel-b",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		ConcurrencyLimit: &limitB,
		AutoBan:          &autoBanOn,
	}
	require.NoError(t, db.Create(channelB).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelAID,
		Enabled:   true,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelBID,
		Enabled:   true,
	}).Error)

	lim := newChannelSlotLimiter()
	installChannelSlotLimiter(t, lim)
	scopeA := concurrency.ChannelScope(channelAID)
	scopeB := concurrency.ChannelScope(channelBID)

	// A's only slot is held for the whole test: every attempt on A refuses.
	siblingReleaseA, err := lim.inner.Acquire(context.Background(), scopeA, limitA)
	require.NoError(t, err)
	t.Cleanup(siblingReleaseA)

	runRequest := func(t *testing.T) (*httptest.ResponseRecorder, *gin.Context) {
		t.Helper()
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			strings.NewReader(`{"model":"`+modelName+`","messages":[{"role":"user","content":"hi"}]}`))
		c.Request.Header.Set("Content-Type", "application/json")
		setSlotRelayIdentityKeys(c, userID, "slot-autoban-exempt-user", "slot-autoban-exempt-token-key")
		require.Nil(t, middleware.SetupContextForSelectedChannel(c, channelA, modelName))
		Relay(c, types.RelayFormatOpenAI)
		return recorder, c
	}

	// --- Request 1: switch — A refuses for saturation, B succeeds.
	recorder1, c1 := runRequest(t)
	require.Equal(t, http.StatusOK, recorder1.Code, recorder1.Body.String())
	assert.True(t, c1.GetBool("auto_ban"),
		"the first hop must publish auto_ban=true or the trigger lines were never armed")
	events1 := service.RequestPolicy(c1).Events()
	assertNoDisableRequested := func(t *testing.T, events []service.PolicyEvent) int {
		t.Helper()
		saturationFailures := 0
		for _, event := range events {
			assert.NotEqual(t, "channel_disable_requested", event.Health,
				"a saturation rejection must never request a channel disable (trigger line: %s)", event.Decision.Reason)
			assert.NotEqual(t, "key_disable_requested", event.Health,
				"a saturation rejection must never request a key disable")
			if event.ErrorCode == string(service.ChannelConcurrencyExceededCode) && event.Health == "unchanged" {
				saturationFailures++
			}
		}
		return saturationFailures
	}
	require.GreaterOrEqual(t, len(events1), 1, "the refused attempt must be recorded")
	satFailures1 := assertNoDisableRequested(t, events1)
	assert.GreaterOrEqual(t, satFailures1, 1,
		"the switch request's saturation refusal must be recorded with Health unchanged")

	// --- Request 2: all-saturated — B's bucket is held by a sibling as well.
	siblingReleaseB, err := lim.inner.Acquire(context.Background(), scopeB, limitB)
	require.NoError(t, err)
	t.Cleanup(siblingReleaseB)
	recorder2, c2 := runRequest(t)
	require.Equal(t, http.StatusTooManyRequests, recorder2.Code, recorder2.Body.String())
	assert.True(t, c2.GetBool("auto_ban"),
		"the retry hop must republish auto_ban=true for the re-selected channel")
	events2 := service.RequestPolicy(c2).Events()
	satFailures2 := assertNoDisableRequested(t, events2)
	assert.GreaterOrEqual(t, satFailures2, 2,
		"both refusals of the all-saturated request must be recorded with Health unchanged")

	// Status layer: both channels must still read Enabled through the
	// cache/DB unified read after routing saturation rejections through both
	// trigger lines with auto-disable armed.
	for _, channelID := range []int{channelAID, channelBID} {
		channel, err := model.CacheGetChannel(channelID)
		require.NoError(t, err, "channel %d must still be readable", channelID)
		assert.Equal(t, common.ChannelStatusEnabled, channel.Status,
			"a saturation rejection must never auto-disable channel %d", channelID)
	}

	siblingReleaseA()
	siblingReleaseB()
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scopeA),
		"channel A's bucket must drain once the sibling holder releases")
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scopeB),
		"channel B's bucket must drain once the sibling holder releases")
}

// TestChannelSlotSwitchHammerDrainsToZeroWithExclusionRouting extends the
// 02-14 load pin (SC-3) over the 02-15 exclusion routing: N goroutines × M
// rounds of REAL controller.Relay traffic, every request first-hopped onto a
// channel whose bucket a sibling holds full, so each request must refuse on A
// exactly once, switch to B through the exclusion set, and succeed — under
// concurrency. Deterministic counters, no sleep anywhere: every request ends
// 200 with use_channel exactly [A, B]; the routing layer's attempt count
// (summed use_channel entries) equals the limiter layer's acquire records,
// granted+rejected accounts for every attempt (one grant on B, one refusal on
// A per request); the atomic gauge never exceeds the published bucket cap; and
// once the WaitGroup joins and the sibling releases, both snapshots read 0.
//
// The upstream answers with zero usage and consume-log recording is toggled
// off for this test: the hammer's contract is routing + slot accounting, not
// billing, and skipping the per-request consume-log / used-quota writes keeps
// the shared-cache fixture effectively read-only under load so the assertions
// measure slot behavior instead of SQLite lock contention.
func TestChannelSlotSwitchHammerDrainsToZeroWithExclusionRouting(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := newChannelSlotDB(t)
	pinRelayRetryBudget(t)

	const modelName = "slot-hammer-model"
	previousRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{"%s":0}`, modelName)))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })

	previousLogConsume := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsume })

	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-slot-hammer","object":"chat.completion","created":1,"model":"`+modelName+
			`","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`)
	}))
	t.Cleanup(upstream.Close)
	service.InitHttpClient()

	const userID = 7421
	const channelAID = 7421
	const channelBID = 7422
	seedSlotRelayUserToken(t, db, userID, "slot-hammer", 100000, 0)

	const workers = 4
	const rounds = 3
	// B's cap covers the whole worker pool, so B never refuses under this load
	// and every request resolves as the deterministic A→B switch (the gauge
	// invariant is measured against this published cap). A's cap stays 1 and
	// its only slot is held by the sibling for the whole hammer.
	limitA, limitB := 1, workers
	channelA := &model.Channel{
		Id:               channelAID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "slot-hammer-channel-a-key",
		Name:             "slot-hammer-channel-a",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		ConcurrencyLimit: &limitA,
	}
	require.NoError(t, db.Create(channelA).Error)
	channelB := &model.Channel{
		Id:               channelBID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "slot-hammer-channel-b-key",
		Name:             "slot-hammer-channel-b",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		ConcurrencyLimit: &limitB,
	}
	require.NoError(t, db.Create(channelB).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelAID,
		Enabled:   true,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelBID,
		Enabled:   true,
	}).Error)

	lim := newChannelSlotLimiter()
	installChannelSlotLimiter(t, lim)
	scopeA := concurrency.ChannelScope(channelAID)
	scopeB := concurrency.ChannelScope(channelBID)

	siblingReleaseA, err := lim.inner.Acquire(context.Background(), scopeA, limitA)
	require.NoError(t, err)
	t.Cleanup(siblingReleaseA)

	gin.SetMode(gin.TestMode)

	// One result per client request; all assertions run on the main goroutine
	// after the join (require must never FailNow from a worker goroutine).
	type hammerResult struct {
		code       int
		body       string
		useChannel []string
		setupErr   string
	}
	results := make(chan hammerResult, workers*rounds)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for range rounds {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
					strings.NewReader(`{"model":"`+modelName+`","messages":[{"role":"user","content":"hi"}]}`))
				c.Request.Header.Set("Content-Type", "application/json")
				setSlotRelayIdentityKeys(c, userID, "slot-hammer-user", "slot-hammer-token-key")
				result := hammerResult{}
				if setupErr := middleware.SetupContextForSelectedChannel(c, channelA, modelName); setupErr != nil {
					result.setupErr = setupErr.Error()
					results <- result
					continue
				}
				Relay(c, types.RelayFormatOpenAI)
				result.code = recorder.Code
				result.body = recorder.Body.String()
				result.useChannel = c.GetStringSlice("use_channel")
				results <- result
			}
		})
	}
	wg.Wait()
	close(results)

	collected := make([]hammerResult, 0, workers*rounds)
	for result := range results {
		collected = append(collected, result)
	}
	require.Len(t, collected, workers*rounds, "every hammer request must produce a result")

	attempts := 0
	for i, result := range collected {
		require.Empty(t, result.setupErr, "hammer request %d failed channel setup: %s", i, result.setupErr)
		require.Equal(t, http.StatusOK, result.code,
			"hammer request %d must succeed after switching away from the saturated channel: %s", i, result.body)
		require.Contains(t, result.body, "chatcmpl-slot-hammer",
			"hammer request %d must receive the upstream success body", i)
		require.Equal(t, []string{fmt.Sprint(channelAID), fmt.Sprint(channelBID)}, result.useChannel,
			"hammer request %d must refuse A exactly once and then switch to B (no repeat of the saturated channel)", i)
		attempts += len(result.useChannel)
	}
	totalRequests := workers * rounds

	// Two counting layers must agree: the routing layer's attempt entries and
	// the limiter's acquire records — and granted+rejected accounts for all of
	// them (one refusal on A + one grant on B per request).
	records := lim.records()
	require.Len(t, records, attempts,
		"the routing layer's attempt count must equal the limiter layer's acquire count")
	require.Equal(t, attempts, int(lim.granted.Load()+lim.rejected.Load()),
		"granted+rejected must account for every attempt")
	assert.Equal(t, totalRequests, int(lim.granted.Load()),
		"every request must be granted exactly once — on channel B")
	assert.Equal(t, totalRequests, int(lim.rejected.Load()),
		"every request must be refused exactly once — on channel A")
	countScopeA, countScopeB := 0, 0
	for _, record := range records {
		switch record.Scope {
		case scopeA:
			countScopeA++
			assert.Equal(t, limitA, record.Limit, "A's attempts must use the published cap")
		case scopeB:
			countScopeB++
			assert.Equal(t, limitB, record.Limit, "B's attempts must use the published cap")
		default:
			t.Fatalf("unexpected acquire scope %v", record.Scope)
		}
	}
	assert.Equal(t, totalRequests, countScopeA, "every request must target channel A's bucket exactly once")
	assert.Equal(t, totalRequests, countScopeB, "every request must take channel B's bucket exactly once")
	assert.EqualValues(t, totalRequests, upstreamCalls.Load(),
		"only channel B must reach the upstream, exactly once per request")
	assert.LessOrEqual(t, int(lim.peakHeld()), limitB,
		"the concurrent gauge must never exceed the published bucket cap")

	siblingReleaseA()
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scopeA),
		"after every worker joins and the sibling releases, A's bucket must be back at 0")
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scopeB),
		"after every worker joins, B's bucket must be back at 0 (actual in-flight)")
}

// ---------------------------------------------------------------------------
// Task 02-18-01 (RTE-02): an upstream 429 must not terminate the request
// after a single channel — one bounded cross-channel hop through the 02-15
// exclusion set, then whatever terminal error results renders through the
// EXISTING relay envelope (client contract byte-identical).
// ---------------------------------------------------------------------------

// pinUpstream429RetryBudget pins the RTE-02 observation conditions to the
// PRODUCTION defaults: attempt budget common.RetryTimes = 0 (the RTE-02
// guarantee must hold with zero operator configuration), auto-disable off,
// and the retryable status-code ranges set to the code defaults that already
// contain 429 (100-199/300-399/401-407/409-499/500-503/505-523/525-599,
// see setting/operation_setting/status_code_ranges.go). Unlike
// pinRelayRetryBudgetWithRetries it deliberately does NOT pin the ranges to
// "500": that pin routes 429 into status_not_retryable and would mask the
// exact production decision path (attempt_budget_exhausted reached with a
// 429 upstream status) this tracer exists to observe.
func pinUpstream429RetryBudget(t *testing.T) {
	t.Helper()
	previousRetryTimes := common.RetryTimes
	common.RetryTimes = 0
	previousStatusCodes := operation_setting.AutomaticRetryStatusCodesToString()
	require.NoError(t, operation_setting.AutomaticRetryStatusCodesFromString(
		"100-199,300-399,401-407,409-499,500-503,505-523,525-599"))
	previousAutoDisable := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = false
	t.Cleanup(func() {
		common.RetryTimes = previousRetryTimes
		require.NoError(t, operation_setting.AutomaticRetryStatusCodesFromString(previousStatusCodes))
		common.AutomaticDisableChannelEnabled = previousAutoDisable
	})
}

// TestRelayUpstream429DoesNotTerminateAfterSingleChannel is the RTE-02 tracer:
// four subtests against the real controller.Relay under the production-default
// retry pin (RetryTimes=0), each on a freshly seeded fixture with independent
// counting upstreams:
//
//	(a) switches_to_second_channel — A 429 → B 200: the client must receive
//	    the upstream success body, use_channel exactly [A, B] (one bounded
//	    cross-channel hop, A never re-picked), A and B upstream hit counts
//	    exactly 1 each, slot records [scopeA, scopeB] with peak <= 1 and both
//	    buckets drained, and the grant observable in the existing policy
//	    decision events (retry / upstream_429_cross_channel).
//	(b) both_fail_passthrough — A and B both 429: the terminal answer stays
//	    the EXISTING relay default envelope carrying the FINAL upstream 429
//	    (message fragment identifies B), type/code never
//	    "concurrency_exceeded", use_channel exactly [A, B] with hit counts 1
//	    each — exactly one grant, no third attempt.
//	(c) single_channel_falls_back_to_upstream_429 — only A exists (429): the
//	    grant's re-selection comes back empty and the fallback must render the
//	    ORIGINAL upstream 429 — not the CNT-01 concurrency rejection, not the
//	    "channel does not exist" error — and use_channel stays [A] (the failed
//	    selection happens before dispatch, so it records no second hop).
//	(d) non_429_terminal_unchanged — A 500: no cross-channel hop for other
//	    error classes; first attempt is terminal (status 500, use_channel [A],
//	    A hit count 1), pinning that every other error type keeps its current
//	    semantics.
//
// RED-batch discipline (02-07 convention): this tracer only references APIs
// that already exist on disk, so its first run fails as a pure BEHAVIOR red
// (the current loop stops after the first 429); the service-layer predicate
// matrix that references the not-yet-existing symbols compiles red separately.
func TestRelayUpstream429DoesNotTerminateAfterSingleChannel(t *testing.T) {
	require.NoError(t, i18n.Init())

	// newUpstream returns an httptest upstream answering every call with
	// `status`: >=400 renders an OpenAI-shaped error body tagged with `tag`
	// (the message/type/code fragment terminal assertions use to identify
	// WHICH upstream produced the terminal error), 200 renders the standard
	// success body. One atomic counter per upstream pins per-channel hit
	// counts without any sleeping.
	newUpstream := func(t *testing.T, modelName, tag string, status int) (*httptest.Server, *atomic.Int32) {
		t.Helper()
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			if status >= http.StatusBadRequest {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, fmt.Sprintf(
					`{"error":{"message":"upstream %s rejected the request","type":"%s_upstream","code":"%s_code"}}`,
					tag, tag, tag))
				return
			}
			_, _ = io.WriteString(w, fmt.Sprintf(
				`{"id":"chatcmpl-up429-%s","object":"chat.completion","created":1,"model":"%s","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
				tag, modelName))
		}))
		t.Cleanup(server.Close)
		return server, &calls
	}

	type upstream429Run struct {
		recorder *httptest.ResponseRecorder
		c        *gin.Context
		callsA   *atomic.Int32
		callsB   *atomic.Int32
		lim      *channelSlotLimiter
		idA      int
		idB      int
	}

	// runRequest drives one REAL controller.Relay request through a freshly
	// seeded fixture: channel A first-hop pinned with a crushing weight and a
	// limit-1 bucket, an optional same-priority channel B (weight 1, limit-1
	// bucket), independent counting upstreams, the recording slot limiter and
	// the production-default retry pin. Channel ids derive from userID so each
	// subtest stays self-contained.
	runRequest := func(t *testing.T, modelName, seedName string, userID, statusA int, withB bool, statusB int) upstream429Run {
		t.Helper()
		db := newChannelSlotDB(t)
		pinUpstream429RetryBudget(t)

		previousRatios := ratio_setting.ModelRatio2JSONString()
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{"%s":0}`, modelName)))
		t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })

		upstreamA, callsA := newUpstream(t, modelName, "chanA", statusA)
		var upstreamB *httptest.Server
		var callsB *atomic.Int32
		if withB {
			upstreamB, callsB = newUpstream(t, modelName, "chanB", statusB)
		}
		service.InitHttpClient()

		seedSlotRelayUserToken(t, db, userID, seedName, 100000, 50000)
		weightA, weightB := uint(10000), uint(1)
		limitA, limitB := 1, 1
		channelA := &model.Channel{
			Id:               userID + 1,
			Type:             constant.ChannelTypeOpenAI,
			Key:              seedName + "-channel-a-key",
			Name:             seedName + "-channel-a",
			Status:           common.ChannelStatusEnabled,
			Group:            "default",
			Models:           modelName,
			BaseURL:          &upstreamA.URL,
			Weight:           &weightA,
			ConcurrencyLimit: &limitA,
		}
		require.NoError(t, db.Create(channelA).Error)
		require.NoError(t, db.Create(&model.Ability{
			Group:     "default",
			Model:     modelName,
			ChannelId: channelA.Id,
			Enabled:   true,
		}).Error)
		if withB {
			channelB := &model.Channel{
				Id:               userID + 2,
				Type:             constant.ChannelTypeOpenAI,
				Key:              seedName + "-channel-b-key",
				Name:             seedName + "-channel-b",
				Status:           common.ChannelStatusEnabled,
				Group:            "default",
				Models:           modelName,
				BaseURL:          &upstreamB.URL,
				Weight:           &weightB,
				ConcurrencyLimit: &limitB,
			}
			require.NoError(t, db.Create(channelB).Error)
			require.NoError(t, db.Create(&model.Ability{
				Group:     "default",
				Model:     modelName,
				ChannelId: channelB.Id,
				Enabled:   true,
			}).Error)
		}

		lim := newChannelSlotLimiter()
		installChannelSlotLimiter(t, lim)

		gin.SetMode(gin.TestMode)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			strings.NewReader(`{"model":"`+modelName+`","messages":[{"role":"user","content":"hi"}]}`))
		c.Request.Header.Set("Content-Type", "application/json")
		setSlotRelayIdentityKeys(c, userID, seedName+"-user", seedName+"-token-key")
		require.Nil(t, middleware.SetupContextForSelectedChannel(c, channelA, modelName))

		Relay(c, types.RelayFormatOpenAI)

		return upstream429Run{
			recorder: recorder,
			c:        c,
			callsA:   callsA,
			callsB:   callsB,
			lim:      lim,
			idA:      channelA.Id,
			idB:      userID + 2,
		}
	}

	t.Run("switches_to_second_channel", func(t *testing.T) {
		run := runRequest(t, "up429-switch-model", "up429-switch", 7451, http.StatusTooManyRequests, true, http.StatusOK)

		require.Equal(t, http.StatusOK, run.recorder.Code, run.recorder.Body.String())
		assert.Contains(t, run.recorder.Body.String(), "up429-switch-model",
			"the client must receive the upstream success body from channel B")
		assert.Equal(t, []string{fmt.Sprint(run.idA), fmt.Sprint(run.idB)}, run.c.GetStringSlice("use_channel"),
			"the 429 must buy exactly one cross-channel hop: A then B, no repeat of A, no third attempt")
		assert.EqualValues(t, 1, run.callsA.Load(), "channel A's upstream must be hit exactly once")
		assert.EqualValues(t, 1, run.callsB.Load(), "channel B's upstream must be hit exactly once")

		scopeA := concurrency.ChannelScope(run.idA)
		scopeB := concurrency.ChannelScope(run.idB)
		records := run.lim.records()
		require.Len(t, records, 2, "each attempt must acquire exactly one channel bucket")
		assert.Equal(t, scopeA, records[0].Scope, "attempt 1 must take channel A's slot")
		assert.Equal(t, scopeB, records[1].Scope, "attempt 2 must take channel B's slot")
		assert.Equal(t, 1, records[0].Limit, "attempt 1 must use A's published cap")
		assert.Equal(t, 1, records[1].Limit, "attempt 2 must use B's published cap")
		assert.LessOrEqual(t, int(run.lim.peakHeld()), 1,
			"the cross-channel hop must never hold two slots at once")
		assert.Equal(t, 0, slotSnapshotCurrent(t, run.lim, scopeA), "A's bucket must drain to 0")
		assert.Equal(t, 0, slotSnapshotCurrent(t, run.lim, scopeB), "B's bucket must drain to 0")

		// Decision observability: the grant must ride the existing
		// RecordPolicyFailure channel as decision retry /
		// upstream_429_cross_channel — no new event fields. The literal
		// mirrors service.Upstream429CrossChannelReason; the RED batch keeps
		// this tracer on existing API only (02-07 layering), while the
		// service-layer matrix test pins the constant itself.
		granted := false
		for _, event := range service.RequestPolicy(run.c).Events() {
			if event.Decision.Action == "retry" && event.Decision.Reason == "upstream_429_cross_channel" {
				granted = true
				break
			}
		}
		assert.True(t, granted,
			"the cross-channel grant must be recorded as decision retry/upstream_429_cross_channel in the policy events")
	})

	t.Run("both_fail_passthrough", func(t *testing.T) {
		run := runRequest(t, "up429-both-model", "up429-both", 7461, http.StatusTooManyRequests, true, http.StatusTooManyRequests)

		require.Equal(t, http.StatusTooManyRequests, run.recorder.Code, run.recorder.Body.String())
		errObj := terminalErrorBody(t, run.recorder)
		message, _ := errObj["message"].(string)
		assert.Contains(t, message, "upstream chanB rejected the request",
			"the terminal must stay the EXISTING relay envelope carrying the final upstream 429 (channel B's message)")
		assert.NotEqual(t, "concurrency_exceeded", errObj["type"],
			"an upstream 429 must never be re-labeled as the gateway concurrency rejection")
		assert.NotEqual(t, "concurrency_exceeded", errObj["code"],
			"an upstream 429 must never be re-labeled as the gateway concurrency rejection")
		assert.NotContains(t, run.recorder.Body.String(), "concurrency_exceeded",
			"no gateway concurrency envelope may replace the upstream 429 passthrough")
		assert.Equal(t, []string{fmt.Sprint(run.idA), fmt.Sprint(run.idB)}, run.c.GetStringSlice("use_channel"),
			"exactly one cross-channel grant: A then B, no third attempt")
		assert.EqualValues(t, 1, run.callsA.Load(), "channel A's upstream must be hit exactly once")
		assert.EqualValues(t, 1, run.callsB.Load(), "channel B's upstream must be hit exactly once")

		scopeA := concurrency.ChannelScope(run.idA)
		scopeB := concurrency.ChannelScope(run.idB)
		records := run.lim.records()
		require.Len(t, records, 2, "each attempt must acquire exactly one channel bucket")
		assert.Equal(t, scopeA, records[0].Scope)
		assert.Equal(t, scopeB, records[1].Scope)
		assert.Equal(t, 0, slotSnapshotCurrent(t, run.lim, scopeA), "A's bucket must drain to 0")
		assert.Equal(t, 0, slotSnapshotCurrent(t, run.lim, scopeB), "B's bucket must drain to 0")
	})

	t.Run("single_channel_falls_back_to_upstream_429", func(t *testing.T) {
		run := runRequest(t, "up429-single-model", "up429-single", 7471, http.StatusTooManyRequests, false, 0)

		require.Equal(t, http.StatusTooManyRequests, run.recorder.Code, run.recorder.Body.String())
		errObj := terminalErrorBody(t, run.recorder)
		message, _ := errObj["message"].(string)
		assert.Contains(t, message, "upstream chanA rejected the request",
			"the empty re-selection must fall back to the ORIGINAL upstream 429, not a gateway classification")
		assert.NotEqual(t, "concurrency_exceeded", errObj["type"],
			"the fallback must not surface the CNT-01 concurrency rejection")
		assert.NotEqual(t, "concurrency_exceeded", errObj["code"],
			"the fallback must not surface the CNT-01 concurrency rejection")
		assert.NotContains(t, run.recorder.Body.String(), "可用渠道不存在",
			"the fallback must not surface the empty-selection channel-not-found error")
		assert.Equal(t, []string{fmt.Sprint(run.idA)}, run.c.GetStringSlice("use_channel"),
			"the failed re-selection happens before dispatch, so it records no second hop")
		assert.EqualValues(t, 1, run.callsA.Load(), "channel A's upstream must be hit exactly once")

		records := run.lim.records()
		require.Len(t, records, 1, "only the first attempt may acquire a slot")
		assert.Equal(t, concurrency.ChannelScope(run.idA), records[0].Scope)
		assert.Equal(t, 0, slotSnapshotCurrent(t, run.lim, concurrency.ChannelScope(run.idA)),
			"A's bucket must drain to 0")
	})

	t.Run("non_429_terminal_unchanged", func(t *testing.T) {
		run := runRequest(t, "up429-non429-model", "up429-non429", 7481, http.StatusInternalServerError, false, 0)

		require.Equal(t, http.StatusInternalServerError, run.recorder.Code, run.recorder.Body.String())
		errObj := terminalErrorBody(t, run.recorder)
		message, _ := errObj["message"].(string)
		assert.Contains(t, message, "upstream chanA rejected the request",
			"the non-429 terminal keeps its existing passthrough rendering")
		assert.Equal(t, "chanA_code", errObj["code"],
			"non-429 render regression (02-18-02): the existing ToOpenAIError envelope shape must be untouched by the grant logic")
		assert.NotContains(t, run.recorder.Body.String(), "concurrency_exceeded",
			"non-429 render regression (02-18-02): the concurrency envelope must never swallow other error classes")
		assert.Equal(t, []string{fmt.Sprint(run.idA)}, run.c.GetStringSlice("use_channel"),
			"non-429 errors must keep the first-hop terminal semantics — no cross-channel hop")
		assert.EqualValues(t, 1, run.callsA.Load(), "channel A's upstream must be hit exactly once")

		records := run.lim.records()
		require.Len(t, records, 1, "a single attempt must acquire a slot exactly once")
		assert.Equal(t, concurrency.ChannelScope(run.idA), records[0].Scope)
		assert.Equal(t, 0, slotSnapshotCurrent(t, run.lim, concurrency.ChannelScope(run.idA)),
			"A's bucket must drain to 0")
	})
}

// ---------------------------------------------------------------------------
// Task 02-18-02: billing invariants on the RTE-02 grant path
// (.agents/rules/billing.md read in full before writing these — both tests
// assert REAL quota before/after deltas against a control request and never
// touch a billing formula, the pre-consume path, or the refund path)
// ---------------------------------------------------------------------------

// TestRelayUpstream429CrossChannelRetryBillsExactlyOnce pins the anti-double-
// charge half of the grant path (T-02-18-02): a request whose first hop hits
// an upstream 429 and succeeds on the granted second channel must pay EXACTLY
// the same as a plain one-attempt control request on the same fixture — the
// billing session is prepared once before the retry loop and retained across
// attempts, so a per-attempt re-pre-consume would double the delta. Measured
// on both real balances (token RemainQuota and user wallet quota) with a
// positive single charge, mirroring TestRelaySaturationSwitchBillsExactlyOnce
// PerRequest's control/switch shape; the retry pin is the production default
// (pinUpstream429RetryBudget, RetryTimes=0 — NOT the RetryTimes=1/"500"-range
// pin, which would push 429 into status_not_retryable and never reach the
// grant). Slot records and upstream hit deltas pin that the switch request
// performed exactly one cross-channel hop.
func TestRelayUpstream429CrossChannelRetryBillsExactlyOnce(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := newChannelSlotDB(t)
	pinUpstream429RetryBudget(t)

	previousCountToken := constant.CountToken
	constant.CountToken = true
	t.Cleanup(func() { constant.CountToken = previousCountToken })

	const modelName = "up429-billing-model"
	previousRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{"%s":2}`, modelName)))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })

	var upstreamACalls atomic.Int32
	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamACalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"upstream chanA rejected the request","type":"chanA_upstream","code":"chanA_code"}}`)
	}))
	t.Cleanup(upstreamA.Close)
	var upstreamBCalls atomic.Int32
	upstreamB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamBCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-up429-billing","object":"chat.completion","created":1,"model":"`+modelName+
			`","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
	}))
	t.Cleanup(upstreamB.Close)
	service.InitHttpClient()

	const userID = 7491
	const channelAID = 7491
	const channelBID = 7492
	const initialTokenQuota = 50000
	const initialUserQuota = 100000
	seedSlotRelayUserToken(t, db, userID, "up429-billing", initialUserQuota, initialTokenQuota)

	weightA, weightB := uint(10000), uint(1)
	limitA, limitB := 1, 1
	channelA := &model.Channel{
		Id:               channelAID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "up429-billing-channel-a-key",
		Name:             "up429-billing-channel-a",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstreamA.URL,
		Weight:           &weightA,
		ConcurrencyLimit: &limitA,
	}
	require.NoError(t, db.Create(channelA).Error)
	channelB := &model.Channel{
		Id:               channelBID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "up429-billing-channel-b-key",
		Name:             "up429-billing-channel-b",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstreamB.URL,
		Weight:           &weightB,
		ConcurrencyLimit: &limitB,
	}
	require.NoError(t, db.Create(channelB).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelAID,
		Enabled:   true,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelBID,
		Enabled:   true,
	}).Error)

	lim := newChannelSlotLimiter()
	installChannelSlotLimiter(t, lim)
	scopeA := concurrency.ChannelScope(channelAID)
	scopeB := concurrency.ChannelScope(channelBID)

	readQuotas := func() (tokenQuota, userQuota int) {
		t.Helper()
		var token model.Token
		require.NoError(t, db.First(&token, userID).Error)
		var user model.User
		require.NoError(t, db.First(&user, userID).Error)
		return token.RemainQuota, user.Quota
	}
	runRequest := func(t *testing.T, pinned *model.Channel) (*httptest.ResponseRecorder, *gin.Context) {
		t.Helper()
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			strings.NewReader(`{"model":"`+modelName+`","messages":[{"role":"user","content":"hi"}]}`))
		c.Request.Header.Set("Content-Type", "application/json")
		setSlotRelayIdentityKeys(c, userID, "up429-billing-user", "up429-billing-token-key")
		require.Nil(t, middleware.SetupContextForSelectedChannel(c, pinned, modelName))
		Relay(c, types.RelayFormatOpenAI)
		return recorder, c
	}

	// --- Request 1 (control): first hop pinned to B (200), one attempt,
	// plain single billing — the 单次计费 yardstick, same fixture, same
	// prompt, same upstream usage as the grant request.
	token0, user0 := readQuotas()
	recorder1, c1 := runRequest(t, channelB)
	require.Equal(t, http.StatusOK, recorder1.Code, recorder1.Body.String())
	assert.Equal(t, []string{fmt.Sprint(channelBID)}, c1.GetStringSlice("use_channel"),
		"the control request must run on channel B in one attempt")
	token1, user1 := readQuotas()
	chargeToken1 := token0 - token1
	chargeUser1 := user0 - user1
	assert.Greater(t, chargeToken1, 0, "the control request must bill a real single charge")
	assert.Greater(t, chargeUser1, 0, "the control request must bill a real single charge on the wallet side")
	bCallsAfterControl := upstreamBCalls.Load()
	require.EqualValues(t, 1, bCallsAfterControl, "the control request must hit B exactly once")

	// --- Request 2 (grant): first hop pinned to A (upstream 429) → one
	// cross-channel grant → B succeeds. Two attempts, one client request: the
	// quota delta must equal the control's single charge — a per-attempt
	// double charge would show up here as a doubled delta.
	tokenBefore, userBefore := token1, user1
	recorder2, c2 := runRequest(t, channelA)
	require.Equal(t, http.StatusOK, recorder2.Code, recorder2.Body.String())
	assert.Contains(t, recorder2.Body.String(), "chatcmpl-up429-billing",
		"the client must receive the upstream success body from channel B")
	assert.Equal(t, []string{fmt.Sprint(channelAID), fmt.Sprint(channelBID)}, c2.GetStringSlice("use_channel"),
		"the grant request must attempt A exactly once and succeed on B")
	token2, user2 := readQuotas()
	chargeToken2 := tokenBefore - token2
	chargeUser2 := userBefore - user2
	assert.Greater(t, chargeToken2, 0, "the grant request must bill a real charge")
	assert.Equal(t, chargeToken1, chargeToken2,
		"the two-attempt grant request must bill exactly once — the token-side delta must equal the single-charge control")
	assert.Equal(t, chargeUser1, chargeUser2,
		"the wallet-side delta must also equal the single-charge control (billing session spans attempts)")

	assert.EqualValues(t, 1, upstreamACalls.Load(),
		"only the grant request's first attempt may hit channel A, exactly once")
	assert.EqualValues(t, 2, upstreamBCalls.Load(),
		"the control request and the grant request's second attempt must be the only B hits")
	assert.EqualValues(t, 1, upstreamBCalls.Load()-bCallsAfterControl,
		"the grant request itself must hit B exactly once")

	records := lim.records()
	require.Len(t, records, 3, "control: one acquire on B; grant: attempt on A + attempt on B")
	assert.Equal(t, scopeB, records[0].Scope, "the control attempt must take channel B's slot")
	assert.Equal(t, scopeA, records[1].Scope, "the grant request's first attempt must target channel A")
	assert.Equal(t, scopeB, records[2].Scope, "the grant request's second attempt must take channel B's slot")
	assert.LessOrEqual(t, int(lim.peakHeld()), 1,
		"at most one slot is ever held (sequential requests, limit=1 buckets)")
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scopeA),
		"channel A's bucket must be back at 0 after the failed first attempt")
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scopeB),
		"channel B's bucket must be back at 0 after both successful attempts")
}

// TestRelayUpstream429DoubleFailureRefundsPreConsumedQuota pins the anti-
// leak-refund half of the grant path (T-02-18-02): a paid fixture pre-consumes
// quota before the retry loop, BOTH channels answer with upstream 429 (the
// granted second hop fails too), and the deferred RefundFailedRequestBilling
// must return the reservation exactly once — token AND wallet balances back to
// their seeds, with a pre-consume write below the seed observed so the
// equality is not vacuous. The terminal answer stays the EXISTING relay
// envelope carrying the final upstream 429 (never the CNT-01 concurrency
// rejection, never the channel-not-found error), each channel's upstream is
// hit exactly once, and both buckets drain to 0. Only existing refund
// semantics are asserted — no billing formula or refund path is changed.
// Refund runs on a worker goroutine, so the test observes committed writes
// through a gorm update callback plus a deadline loop (mirrors
// TestRelayAllSaturationTerminal429RefundsPreConsumedQuota) — no sleeping.
func TestRelayUpstream429DoubleFailureRefundsPreConsumedQuota(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := newChannelSlotDB(t)
	pinUpstream429RetryBudget(t)

	previousCountToken := constant.CountToken
	constant.CountToken = true
	t.Cleanup(func() { constant.CountToken = previousCountToken })

	const modelName = "up429-refund-model"
	previousRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{"%s":2}`, modelName)))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })

	// Both channels share this upstream: every call answers 429 with an
	// OpenAI-shaped body whose message/type/code identify the source as the
	// upstream (never the gateway concurrency class).
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"upstream rejected the request with 429","type":"upstream_429","code":"upstream_429_code"}}`)
	}))
	t.Cleanup(upstream.Close)
	service.InitHttpClient()

	const userID = 7501
	const channelAID = 7501
	const channelBID = 7502
	const initialTokenQuota = 50000
	const initialUserQuota = 100000
	seedSlotRelayUserToken(t, db, userID, "up429-refund", initialUserQuota, initialTokenQuota)

	limitA, limitB := 1, 1
	channelA := &model.Channel{
		Id:               channelAID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "up429-refund-channel-a-key",
		Name:             "up429-refund-channel-a",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		ConcurrencyLimit: &limitA,
	}
	require.NoError(t, db.Create(channelA).Error)
	channelB := &model.Channel{
		Id:               channelBID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "up429-refund-channel-b-key",
		Name:             "up429-refund-channel-b",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           modelName,
		BaseURL:          &upstream.URL,
		ConcurrencyLimit: &limitB,
	}
	require.NoError(t, db.Create(channelB).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelAID,
		Enabled:   true,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelBID,
		Enabled:   true,
	}).Error)

	lim := newChannelSlotLimiter()
	installChannelSlotLimiter(t, lim)
	scopeA := concurrency.ChannelScope(channelAID)
	scopeB := concurrency.ChannelScope(channelBID)

	// Trace every committed write to the tokens table: the synchronous
	// pre-consume must show a value below the seed, the asynchronous refund
	// must land back on the seed.
	var traceMu sync.Mutex
	tokenTrace := make([]int, 0, 4)
	updates := make(chan struct{}, 8)
	require.NoError(t, db.Callback().Update().After("gorm:commit_or_rollback_transaction").Register("up429-double-refund-trace", func(tx *gorm.DB) {
		if tx.Error != nil || tx.Statement.Table != "tokens" {
			return
		}
		var tokenNow model.Token
		if err := model.DB.First(&tokenNow, userID).Error; err == nil {
			traceMu.Lock()
			tokenTrace = append(tokenTrace, tokenNow.RemainQuota)
			traceMu.Unlock()
		}
		select {
		case updates <- struct{}{}:
		default:
		}
	}))

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+modelName+`","messages":[{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setSlotRelayIdentityKeys(c, userID, "up429-refund-user", "up429-refund-token-key")
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, channelA, modelName))

	Relay(c, types.RelayFormatOpenAI)

	// Terminal answer: the EXISTING relay envelope carrying the final
	// upstream 429 — not the CNT-01 concurrency rejection and not a
	// channel-not-found classification.
	require.Equal(t, http.StatusTooManyRequests, recorder.Code, recorder.Body.String())
	errObj := terminalErrorBody(t, recorder)
	message, _ := errObj["message"].(string)
	assert.Contains(t, message, "upstream rejected the request with 429",
		"the terminal must passthrough the final upstream 429 message")
	assert.NotEqual(t, "concurrency_exceeded", errObj["type"],
		"the terminal must never be re-labeled as the gateway concurrency rejection")
	assert.NotEqual(t, "concurrency_exceeded", errObj["code"],
		"the terminal must never be re-labeled as the gateway concurrency rejection")
	assert.NotContains(t, recorder.Body.String(), "可用渠道不存在",
		"the terminal must not surface the empty-selection channel-not-found error")
	assert.NotContains(t, recorder.Body.String(), "channel:",
		"the client-visible body must not leak the internal routing prefix")

	useChannel := c.GetStringSlice("use_channel")
	assert.Equal(t, []string{fmt.Sprint(channelAID), fmt.Sprint(channelBID)}, useChannel,
		"the request must attempt each channel exactly once before the terminal")
	assert.EqualValues(t, 2, upstreamCalls.Load(),
		"both attempts must reach the shared upstream exactly once each")
	records := lim.records()
	require.Len(t, records, 2, "each attempt must acquire exactly one channel bucket")
	assert.Equal(t, scopeA, records[0].Scope, "attempt 1 must take channel A's slot")
	assert.Equal(t, scopeB, records[1].Scope, "the granted attempt must take channel B's slot")
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scopeA),
		"channel A's bucket must drain to 0 after the failed attempt")
	assert.Equal(t, 0, slotSnapshotCurrent(t, lim, scopeB),
		"channel B's bucket must drain to 0 after the failed granted attempt")

	// Quota before/after: wait for the async refund to commit, and require
	// that a pre-consume write (below the seed) was actually observed —
	// without it the equality assertion would be vacuous. BillingSession.Refund
	// restores the funding source (wallet) BEFORE the token balance inside one
	// worker closure, so the token equality below implies the wallet side too
	// (the same ordering the 02-15 refund pins rely on).
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		var tokenAfter model.Token
		require.NoError(t, db.First(&tokenAfter, userID).Error)
		var userAfter model.User
		require.NoError(t, db.First(&userAfter, userID).Error)
		traceMu.Lock()
		observed := append([]int(nil), tokenTrace...)
		traceMu.Unlock()
		sawPreConsume := false
		for _, quota := range observed {
			if quota < initialTokenQuota {
				sawPreConsume = true
				break
			}
		}
		if tokenAfter.RemainQuota == initialTokenQuota && sawPreConsume {
			require.Equal(t, initialUserQuota, userAfter.Quota,
				"the wallet side of the reservation must be refunded to its seed as well (exactly once — no double refund)")
			break
		}
		select {
		case <-updates:
		case <-deadline.C:
			t.Fatalf("a double upstream-429 terminal must refund the reservation exactly once: token=%d user=%d trace=%v (want token %d with a pre-consume write below it)",
				tokenAfter.RemainQuota, userAfter.Quota, observed, initialTokenQuota)
		}
	}
}
