package service

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestShouldRetryRelayErrorHonorsChannelPinOnChannelError(t *testing.T) {
	err := types.NewError(errors.New("channel failed"), types.ErrorCodeChannelNoAvailableKey)
	for _, test := range []struct {
		name      string
		pin       *dto.ChannelPin
		wantRetry bool
	}{
		{name: "unrestricted channel error", wantRetry: true},
		{
			name: "single attempt pin suppresses channel error retry",
			pin: &dto.ChannelPin{
				ChannelId: 1, Source: dto.PinSourceToken, Rank: dto.PinRankToken, RetryMode: dto.PinRetrySingleAttempt,
			},
			wantRetry: false,
		},
		{
			name: "origin task pin permits retry on the same channel",
			pin: &dto.ChannelPin{
				ChannelId: 1, Source: dto.PinSourceOriginTask, Rank: dto.PinRankOriginTask, RetryMode: dto.PinRetrySameChannel,
			},
			wantRetry: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			if test.pin != nil {
				GetChannelConstraints(c).AddPin(*test.pin)
			}
			assert.Equal(t, test.wantRetry, ShouldRetryRelayError(c, err, 1))
		})
	}
}

func TestProcessChannelErrorMasksDisableReasonAndNotification(t *testing.T) {
	previousDB, previousType := model.DB, common.MainDatabaseType()
	previousCache, previousRedis := common.MemoryCacheEnabled, common.RedisEnabled
	previousAutoDisable, previousErrorLog := common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled
	previousNotifyLimit := constant.NotifyLimitCount
	previousClient, previousWorker := httpClient, system_setting.WorkerUrl
	fetch := system_setting.GetFetchSetting()
	previousFetch := *fetch
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.MemoryCacheEnabled, common.RedisEnabled = previousCache, previousRedis
		common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled = previousAutoDisable, previousErrorLog
		constant.NotifyLimitCount = previousNotifyLimit
		httpClient, system_setting.WorkerUrl = previousClient, previousWorker
		*fetch = previousFetch
	})
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.User{}))
	model.DB = database
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.MemoryCacheEnabled, common.RedisEnabled = false, false
	common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled = true, false
	constant.NotifyLimitCount = 10
	channel := &model.Channel{Name: "relay-review", Key: "fixture-key", Type: 1, Status: common.ChannelStatusEnabled, Group: "default", Models: "test-model"}
	require.NoError(t, channel.Insert())
	notifications := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		notifications <- body
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	httpClient, system_setting.WorkerUrl = server.Client(), ""
	fetch.EnableSSRFProtection = false
	settings, err := common.Marshal(kitdto.UserSetting{NotifyType: kitdto.NotifyTypeWebhook, WebhookUrl: server.URL})
	require.NoError(t, err)
	root := &model.User{Username: "notification-test-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Setting: string(settings)}
	require.NoError(t, database.Create(root).Error)
	notifyKey := fmt.Sprintf("%d:%s:%s", root.Id, formatNotifyType(channel.Id, common.ChannelStatusAutoDisabled), time.Now().Format("2006010215"))
	notifyLimitStore.Delete(notifyKey)
	t.Cleanup(func() { notifyLimitStore.Delete(notifyKey) })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://private.example.com")
	apiErr := types.NewErrorWithStatusCode(errors.New("upstream https://private.example.com/path?token=review-token api_key:review-secret"), types.ErrorCodeChannelNoAvailableKey, http.StatusUnauthorized)
	ProcessChannelError(c, types.ChannelError{ChannelId: channel.Id, ChannelName: channel.Name, AutoBan: true}, apiErr, nil)
	var notification WebhookPayload
	select {
	case payload := <-notifications:
		require.NoError(t, common.Unmarshal(payload, &notification))
	case <-time.After(5 * time.Second):
		t.Fatal("automatic channel-disable notification was not delivered")
	}
	loaded, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusAutoDisabled, loaded.Status)
	wantReason := "status_code=401, error_code=channel:no_available_key, upstream https://***.com/***?token=*** api_key:***"
	assert.Equal(t, wantReason, loaded.GetOtherInfo()["status_reason"])
	assert.Contains(t, notification.Content, wantReason)
	assert.NotContains(t, notification.Content, "review-token")
	assert.NotContains(t, notification.Content, "review-secret")
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
}

// Only addresses the admin configured are masked: a channel on the base URL
// new-api presets reaches a public official endpoint, and so do adaptors that
// build official endpoints themselves.
func TestUpstreamAddressesMaskOnlyConfiguredAddresses(t *testing.T) {
	for _, tc := range []struct {
		name        string
		channelType int
		baseURL     string
		origin      string
		proxyEnv    string
		message     string
		want        string
	}{
		{
			name:        "preset base URL stays readable",
			channelType: constant.ChannelTypeAnthropic,
			baseURL:     "https://api.anthropic.com",
			origin:      "https://api.anthropic.com",
			message:     "request to https://api.anthropic.com/v1/messages failed, see https://platform.claude.com/docs/en/build-with-claude/task-budgets",
			want:        "request to https://api.anthropic.com/v1/messages failed, see https://platform.claude.com/docs/en/build-with-claude/task-budgets",
		},
		{
			name:        "official endpoint built by the adaptor stays readable",
			channelType: constant.ChannelTypeVertexAi,
			origin:      "https://us-central1-aiplatform.googleapis.com",
			message:     "request to https://us-central1-aiplatform.googleapis.com/v1/projects failed",
			want:        "request to https://us-central1-aiplatform.googleapis.com/v1/projects failed",
		},
		{
			name:        "configured base URL is masked",
			channelType: constant.ChannelTypeOpenAI,
			baseURL:     "https://relay.private.example",
			origin:      "https://relay.private.example",
			message:     "request to https://relay.private.example/v1/chat/completions failed",
			want:        "request to https://***.example/***/***/*** failed",
		},
		{
			name:        "absolute Advanced Custom route is masked",
			channelType: constant.ChannelTypeAdvancedCustom,
			origin:      "https://route.private.example",
			message:     "dial tcp: lookup route.private.example: no such host",
			want:        "dial tcp: lookup ***.***.example: no such host",
		},
		{
			name:        "environment proxy is masked on a preset channel",
			channelType: constant.ChannelTypeAnthropic,
			baseURL:     "https://api.anthropic.com",
			proxyEnv:    "http://egress.private.invalid:8080",
			message:     "proxyconnect tcp: dial tcp: lookup egress.private.invalid: no such host",
			want:        "proxyconnect tcp: dial tcp: lookup ***.***.invalid: no such host",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HTTPS_PROXY", tc.proxyEnv)
			t.Setenv("HTTP_PROXY", "")
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			common.SetContextKey(c, constant.ContextKeyChannelType, tc.channelType)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, tc.baseURL)
			common.SetContextKey(c, constant.ContextKeyUpstreamRequestOrigin, tc.origin)
			apiErr := types.NewErrorWithStatusCode(errors.New(tc.message), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway)

			assert.Equal(t, tc.want, apiErr.ToOpenAIError(common.UpstreamAddresses(c)...).Message)
		})
	}
}

func TestDecideRelayRetryReasons(t *testing.T) {
	upstream := func(status int) *types.NewAPIError {
		return types.NewOpenAIError(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, status)
	}
	for _, tc := range []struct {
		name    string
		err     *types.NewAPIError
		retries int
		setup   func(*gin.Context)
		want    PolicyDecision
	}{
		{name: "retry status matched", err: upstream(http.StatusTooManyRequests), retries: 1, want: PolicyDecision{Action: "retry", Reason: "retry_status_matched", Source: "global"}},
		{name: "status outside retry rules", err: upstream(http.StatusBadRequest), retries: 1, want: PolicyDecision{Action: "stop", Reason: "status_not_retryable", Source: "global"}},
		{name: "attempt budget exhausted", err: upstream(http.StatusTooManyRequests), retries: 0, want: PolicyDecision{Action: "stop", Reason: "attempt_budget_exhausted", Source: "global"}},
		{name: "always skipped status", err: upstream(http.StatusGatewayTimeout), retries: 1, want: PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}},
		{name: "success status never retries", err: upstream(http.StatusOK), retries: 1, want: PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}},
		{name: "skip retry error", err: types.NewErrorWithStatusCode(errors.New("local"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry()), retries: 1, want: PolicyDecision{Action: "stop", Reason: "non_retryable_error", Source: "system"}},
		{name: "channel error retries without budget", err: types.NewError(errors.New("no key"), types.ErrorCodeChannelNoAvailableKey), retries: 0, want: PolicyDecision{Action: "retry", Reason: "channel_error", Source: "system"}},
		{name: "single attempt pin", err: upstream(http.StatusTooManyRequests), retries: 1, setup: func(c *gin.Context) {
			GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: 1, Source: dto.PinSourceToken, Rank: dto.PinRankToken, RetryMode: dto.PinRetrySingleAttempt})
		}, want: PolicyDecision{Action: "stop", Reason: "pinned_channel", Source: "channel_constraint"}},
		{name: "strict session", err: upstream(http.StatusTooManyRequests), retries: 1, setup: func(c *gin.Context) {
			c.Set(ginKeyChannelAffinitySkipRetry, true)
			RequestPolicy(c).SessionModeSource = "global"
		}, want: PolicyDecision{Action: "stop", Reason: "strict_session", Source: "global"}},
		{name: "nil error", retries: 1, want: PolicyDecision{Action: "stop", Reason: "request_completed", Source: "system"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			if tc.setup != nil {
				tc.setup(c)
			}
			decision := DecideRelayRetry(c, tc.err, tc.retries)
			assert.Equal(t, tc.want, decision)
			assert.Equal(t, tc.want.Action == "retry", ShouldRetryRelayError(c, tc.err, tc.retries))
		})
	}
}

func TestRequestPolicyEventsReachLogAdminInfo(t *testing.T) {
	previousAutoDisable := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = previousAutoDisable })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("auto_ban", true)
	c.Set("channel_id", 7)
	state := RequestPolicy(c)
	state.BeginAttempt(&model.Channel{Id: 7}, "default")
	apiErr := types.NewOpenAIError(errors.New("invalid credential"), types.ErrorCodeBadResponseStatusCode, http.StatusUnauthorized)
	RecordPolicyFailure(c, 7, apiErr, DecideRelayRetry(c, apiErr, 0))

	failed := model.NewLogOther()
	AppendRelayLogAdminInfo(c, nil, failed)
	events, ok := failed.Snapshot()["admin_info"].(map[string]any)["request_policy"].([]PolicyEvent)
	require.True(t, ok, "a failed relay exposes its decision events to administrators")
	require.Len(t, events, 3)
	assert.Equal(t, PolicyDecision{Action: "attempt", Reason: "channel_selected", Source: "routing"}, events[0].Decision)
	assert.Equal(t, "default", events[0].Group)
	assert.Equal(t, PolicyDecision{Action: "failure", Reason: "upstream_failure", Source: "upstream"}, events[1].Decision)
	assert.Equal(t, http.StatusUnauthorized, events[1].Status)
	assert.Equal(t, PolicyDecision{Action: "stop", Reason: "attempt_budget_exhausted", Source: "global"}, events[2].Decision)
	assert.Equal(t, "channel_disable_requested", events[2].Health, "the health entry follows the automatic disable rules")
	common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, true)
	RecordPolicyFailure(c, 7, apiErr, DecideRelayRetry(c, apiErr, 0))
	assert.Equal(t, "key_disable_requested", state.Events()[4].Health)

	state.BeginAttempt(&model.Channel{Id: 8}, "default")
	c.Set("channel_id", 8)
	MarkRequestPolicySuccess(c, nil)
	MarkRequestPolicySuccess(c, nil)
	succeeded := model.NewLogOther()
	AppendRelayLogAdminInfo(c, nil, succeeded)
	events, ok = succeeded.Snapshot()["admin_info"].(map[string]any)["request_policy"].([]PolicyEvent)
	require.True(t, ok, "a successful relay exposes its decision events to administrators")
	require.Len(t, events, 7, "the outcome is recorded once")
	assert.Equal(t, PolicyDecision{Action: "success", Reason: "request_completed", Source: "upstream"}, events[6].Decision)
	assert.Equal(t, 8, events[6].ChannelID)
	assert.Equal(t, 2, events[6].Attempt)
	assert.True(t, state.Successful)

	untouched, _ := gin.CreateTestContext(httptest.NewRecorder())
	other := model.NewLogOther()
	AppendRelayLogAdminInfo(untouched, nil, other)
	assert.NotContains(t, other.Snapshot()["admin_info"], "request_policy", "requests without decisions do not carry an empty record")
}

// 分段耗时落盘：admin_info.timing 带八段耗时与三个当时生效的超时上限；
// 上下文里没有采集载体时不写 timing 键（不写空 map）。
func TestRequestTimingReachesLogAdminInfo(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	withoutCarrier := model.NewLogOther()
	AppendRelayLogAdminInfo(c, nil, withoutCarrier)
	adminInfo, ok := withoutCarrier.Snapshot()["admin_info"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, adminInfo, "timing",
		"a request without a timing collector carries no timing record")

	timing := relaycommon.RequestTimingOf(c)
	timing.SetQueueWait(120 * time.Millisecond)
	timing.AddChannelSelect(4 * time.Millisecond)
	timing.AddAttempt()
	timing.SetUpstreamHeaderWait(1020 * time.Millisecond)

	withCarrier := model.NewLogOther()
	AppendRelayLogAdminInfo(c, nil, withCarrier)
	adminInfo, ok = withCarrier.Snapshot()["admin_info"].(map[string]any)
	require.True(t, ok)
	record, ok := adminInfo["timing"].(map[string]any)
	require.True(t, ok, "a relay request exposes its latency breakdown to administrators")

	for _, key := range []string{
		"queue_wait_ms",
		"channel_select_ms",
		"upstream_header_ms",
		"upstream_body_ms",
		"upstream_first_byte_ms",
		"upstream_stream_ms",
		"settle_ms",
		"attempts",
		"upstream_header_timeout_s",
		"streaming_timeout_s",
		"relay_timeout_s",
	} {
		assert.Contains(t, record, key)
	}
	assert.EqualValues(t, 120, record["queue_wait_ms"])
	assert.EqualValues(t, 4, record["channel_select_ms"])
	assert.EqualValues(t, 1020, record["upstream_header_ms"])
	assert.EqualValues(t, 1, record["attempts"])
}

// ---------------------------------------------------------------------------
// Saturation rejection vs channel auto-ban (threat T-02-14-04)
// ---------------------------------------------------------------------------
// TestShouldDisableChannelExcludesSaturationRejection pins the auto-ban
// chokepoint: the channel saturation rejection is a scheduling refusal, not
// evidence the channel is broken, so it must never be judged disableable —
// otherwise the busier a channel gets the faster it is auto-disabled
// (secondary DoS). Real channel fault codes must keep returning true (no
// regression), and a nil error must stay false.
func TestShouldDisableChannelExcludesSaturationRejection(t *testing.T) {
	previous := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = previous })

	for _, tc := range []struct {
		name string
		err  *types.NewAPIError
		want bool
	}{
		{name: "saturation rejection never disables", err: NewChannelConcurrencyRejection(), want: false},
		{name: "invalid key still disables", err: types.NewError(errors.New("invalid key"), types.ErrorCodeChannelInvalidKey), want: true},
		{name: "no available key still disables", err: types.NewError(errors.New("no key"), types.ErrorCodeChannelNoAvailableKey), want: true},
		{name: "nil error never disables", err: nil, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ShouldDisableChannel(tc.err))
		})
	}
}

// TestShouldDisableChannelCoversBothAutoBanTriggerLines pins BOTH trigger
// lines named by T-02-14-04 — service/relay_error.go's ProcessChannelError
// (performs the actual disable) and service/request_policy.go's
// RecordPolicyFailure (marks health). Both consult the ShouldDisableChannel
// chokepoint, so the saturation exclusion must hold at each call site.
func TestShouldDisableChannelCoversBothAutoBanTriggerLines(t *testing.T) {
	t.Run("RecordPolicyFailure health stays unchanged for saturation", func(t *testing.T) {
		previous := common.AutomaticDisableChannelEnabled
		common.AutomaticDisableChannelEnabled = true
		t.Cleanup(func() { common.AutomaticDisableChannelEnabled = previous })

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		c.Set("auto_ban", true)

		rejection := NewChannelConcurrencyRejection()
		decision := DecideRelayRetry(c, rejection, 1)
		assert.Equal(t, "retry", decision.Action, "the rejection must ride the IsChannelError retry route (BCD D-4)")
		RecordPolicyFailure(c, 42, rejection, decision)
		events := RequestPolicy(c).Events()
		require.NotEmpty(t, events)
		assert.Equal(t, "unchanged", events[len(events)-1].Health,
			"a saturation rejection must not mark the channel for disable")

		fault := types.NewError(errors.New("invalid key"), types.ErrorCodeChannelInvalidKey)
		RecordPolicyFailure(c, 42, fault, DecideRelayRetry(c, fault, 1))
		events = RequestPolicy(c).Events()
		require.NotEmpty(t, events)
		assert.Equal(t, "channel_disable_requested", events[len(events)-1].Health,
			"real channel faults must still mark the disable (no regression)")
	})

	t.Run("ProcessChannelError never disables on saturation", func(t *testing.T) {
		previousDB, previousType := model.DB, common.MainDatabaseType()
		previousCache, previousRedis := common.MemoryCacheEnabled, common.RedisEnabled
		previousAutoDisable, previousErrorLog := common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled
		previousNotifyLimit := constant.NotifyLimitCount
		t.Cleanup(func() {
			model.DB = previousDB
			common.SetMainDatabaseType(previousType)
			common.MemoryCacheEnabled, common.RedisEnabled = previousCache, previousRedis
			common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled = previousAutoDisable, previousErrorLog
			constant.NotifyLimitCount = previousNotifyLimit
		})
		database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := database.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
		require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.User{}))
		model.DB = database
		common.SetMainDatabaseType(common.DatabaseTypeSQLite)
		common.MemoryCacheEnabled, common.RedisEnabled = false, false
		common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled = true, false
		constant.NotifyLimitCount = 10
		// DisableChannel notifies the root user; a seeded root keeps that path
		// panic-free (NotifyRootUser dereferences the lookup result).
		require.NoError(t, database.Create(&model.User{Username: "slot-ban-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}).Error)

		const saturatedChannelID = 8401
		const faultedChannelID = 8402
		require.NoError(t, database.Create(&model.Channel{
			Id: saturatedChannelID, Name: "slot-saturated", Key: "slot-saturated-key", Type: 1,
			Status: common.ChannelStatusEnabled, Group: "default", Models: "slot-model",
		}).Error)
		require.NoError(t, database.Create(&model.Channel{
			Id: faultedChannelID, Name: "slot-faulted", Key: "slot-faulted-key", Type: 1,
			Status: common.ChannelStatusEnabled, Group: "default", Models: "slot-model",
		}).Error)

		// DisableChannel runs on a gopool goroutine, so observation is async:
		// poll for the status within a bounded window instead of sleeping once
		// and hoping (positive control below proves the fixture can disable).
		waitForDisabled := func(channelID int, window time.Duration) bool {
			deadline := time.Now().Add(window)
			for time.Now().Before(deadline) {
				var ch model.Channel
				if err := database.First(&ch, channelID).Error; err == nil && ch.Status == common.ChannelStatusAutoDisabled {
					return true
				}
				time.Sleep(10 * time.Millisecond)
			}
			return false
		}

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		ProcessChannelError(c, types.ChannelError{ChannelId: saturatedChannelID, ChannelName: "slot-saturated", AutoBan: true},
			NewChannelConcurrencyRejection(), nil)
		assert.False(t, waitForDisabled(saturatedChannelID, 1500*time.Millisecond),
			"a saturation rejection must never auto-disable the channel (T-02-14-04)")

		// Control: the same fixture DOES disable for a real channel fault, so
		// the guard above cannot pass vacuously.
		ProcessChannelError(c, types.ChannelError{ChannelId: faultedChannelID, ChannelName: "slot-faulted", AutoBan: true},
			types.NewErrorWithStatusCode(errors.New("invalid key"), types.ErrorCodeChannelInvalidKey, http.StatusUnauthorized), nil)
		require.True(t, waitForDisabled(faultedChannelID, 5*time.Second),
			"control: a real channel fault must still auto-disable through the same trigger line")
	})
}

// TestChannelConcurrencyRejectionSignalHygiene re-pins the rejection signal
// contract at the service layer (the seam 02-15 consumes): dedicated code,
// status 429, and rendered text that leaks neither the channel:<id> scope
// literal nor a channel id (CNT-01 hygiene, T-02-14-05).
func TestChannelConcurrencyRejectionSignalHygiene(t *testing.T) {
	err := NewChannelConcurrencyRejection()
	require.True(t, IsChannelConcurrencyRejection(err))
	assert.Equal(t, ChannelConcurrencyExceededCode, err.GetErrorCode())
	assert.Equal(t, http.StatusTooManyRequests, err.StatusCode)
	sanitizer := regexp.MustCompile(`channel:\d+`)
	for _, rendered := range []string{err.Error(), err.ToOpenAIError().Message} {
		assert.NotRegexp(t, sanitizer, rendered, "the rejection must not leak the channel scope literal")
	}
}

// ---------------------------------------------------------------------------
// RTE-02 (02-18): the "upstream 429 must not terminate after a single channel"
// grant predicate
// ---------------------------------------------------------------------------

// TestSingleChannelUpstream429CrossChannelRetryPredicateMatrix pins the four
// conditions of ShouldGrantUpstream429CrossChannelRetry as a deterministic
// table: exactly ONE configuration grants (upstream 429 + budget-exhausted
// stop + exactly one distinct channel tried + no grant used yet), and every
// near-miss is rejected — two channels already tried, grant already used,
// each of the request-level/explicit stop reasons the grant must never cover
// (pinned_channel, strict_session, non_retryable_error — plus the
// channel_error / retry actions that are not stops at all), the
// status_not_retryable stop, non-429 statuses, and a nil error. The
// status_not_retryable row pins the predicate's unit semantics only: that
// reason is reachable in production only when RetryTimes >= 1 (the budget
// check at service/relay_error.go DecideRelayRetry runs BEFORE the status
// check, so under the default RetryTimes=0 a non-channel terminal always
// carries attempt_budget_exhausted) — it must not be read as "an operator
// config suppresses the grant on the default path".
func TestSingleChannelUpstream429CrossChannelRetryPredicateMatrix(t *testing.T) {
	upstream429 := func(status int) *types.NewAPIError {
		return types.NewOpenAIError(errors.New("upstream rate limited"), types.ErrorCodeBadResponseStatusCode, status)
	}
	budgetStop := &PolicyDecision{Action: "stop", Reason: "attempt_budget_exhausted", Source: "global"}

	for _, tc := range []struct {
		name          string
		err           *types.NewAPIError
		decision      *PolicyDecision
		distinctTried int
		priorGrants   int
		want          bool
	}{
		// --- grant group: exactly one configuration passes ---
		{name: "grant: budget-exhausted stop + upstream 429 + one channel tried + no prior grant",
			err: upstream429(http.StatusTooManyRequests), decision: budgetStop, distinctTried: 1, priorGrants: 0, want: true},

		// --- reject group ---
		{name: "reject: two distinct channels already tried",
			err: upstream429(http.StatusTooManyRequests), decision: budgetStop, distinctTried: 2, priorGrants: 0, want: false},
		{name: "reject: grant already used (exactly once)",
			err: upstream429(http.StatusTooManyRequests), decision: budgetStop, distinctTried: 1, priorGrants: 1, want: false},
		{name: "reject: channel_error decision",
			err:           upstream429(http.StatusTooManyRequests),
			decision:      &PolicyDecision{Action: "retry", Reason: "channel_error", Source: "system"},
			distinctTried: 1, priorGrants: 0, want: false},
		{name: "reject: pinned_channel stop is never overridden",
			err:           upstream429(http.StatusTooManyRequests),
			decision:      &PolicyDecision{Action: "stop", Reason: "pinned_channel", Source: "channel_constraint"},
			distinctTried: 1, priorGrants: 0, want: false},
		{name: "reject: strict_session stop is never overridden",
			err:           upstream429(http.StatusTooManyRequests),
			decision:      &PolicyDecision{Action: "stop", Reason: "strict_session", Source: "session_rule"},
			distinctTried: 1, priorGrants: 0, want: false},
		{name: "reject: non_retryable_error stop is never overridden",
			err:           upstream429(http.StatusTooManyRequests),
			decision:      &PolicyDecision{Action: "stop", Reason: "non_retryable_error", Source: "system"},
			distinctTried: 1, priorGrants: 0, want: false},
		{name: "reject: status_not_retryable stop (reachable only with RetryTimes >= 1)",
			err:           upstream429(http.StatusTooManyRequests),
			decision:      &PolicyDecision{Action: "stop", Reason: "status_not_retryable", Source: "global"},
			distinctTried: 1, priorGrants: 0, want: false},
		{name: "reject: upstream 500 is not an upstream 429",
			err: upstream429(http.StatusInternalServerError), decision: budgetStop, distinctTried: 1, priorGrants: 0, want: false},
		{name: "reject: upstream 400 is not an upstream 429",
			err: upstream429(http.StatusBadRequest), decision: budgetStop, distinctTried: 1, priorGrants: 0, want: false},
		{name: "reject: nil error",
			err: nil, decision: budgetStop, distinctTried: 1, priorGrants: 0, want: false},
		{name: "reject: non-stop decision (retry status matched)",
			err:           upstream429(http.StatusTooManyRequests),
			decision:      &PolicyDecision{Action: "retry", Reason: "retry_status_matched", Source: "global"},
			distinctTried: 1, priorGrants: 0, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want,
				ShouldGrantUpstream429CrossChannelRetry(tc.err, tc.decision, tc.distinctTried, tc.priorGrants))
		})
	}

	assert.False(t, ShouldGrantUpstream429CrossChannelRetry(nil, nil, 1, 0),
		"a nil decision must be safe and must never grant")
}
