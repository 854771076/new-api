// FORK-LOCAL-EXTENSION: 2026-10-19 e2e regression test for model_mapping wire body; remove-when: upstream merges the same regression test or supersedes it.
//
// Asserts the user's symptom: channel A saturated on the first hop → retry
// hop selects channel B with a DIFFERENT model_mapping → channel B's
// upstream receives ITS OWN mapped model name (not the raw client virtual
// name and not channel A's mapped name). The fix is broken if either
// assertion fails.

package controller

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestRelayWireBodyCarriesRetryChannelModelMapping is the e2e pin for the
// fix that moved helper.ModelMappedHelper into the retry loop. Two channels
// carry DIFFERENT model_mapping dictionaries; channel A is force-saturated
// (sibling holds the only slot) so the retry deterministically lands on B;
// the test inspects the raw body the upstream receives and asserts each
// attempt's request carries ITS channel's mapped name — not the raw client
// name and not the other channel's mapped name. This catches the entire
// class of bugs where the mapping is computed but never reaches the wire
// (wrong placement, DeepCopy losing the change, pass-through bypass,
// chat-via-responses losing the change, etc.).
func TestRelayWireBodyCarriesRetryChannelModelMapping(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := newChannelSlotDB(t)

	const (
		clientModel = "tyy_maas_kimi-k2.5"
		upstreamA   = "real-upstream-a"
		upstreamB   = "real-upstream-b"
		userID      = 7801
		channelAID  = 7801
		channelBID  = 7802
	)

	previousRetryTimes := common.RetryTimes
	common.RetryTimes = 1
	previousStatusCodes := operation_setting.AutomaticRetryStatusCodesToString()
	require.NoError(t, operation_setting.AutomaticRetryStatusCodesFromString("500"))
	previousAutoDisable := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = false
	t.Cleanup(func() {
		common.RetryTimes = previousRetryTimes
		require.NoError(t, operation_setting.AutomaticRetryStatusCodesFromString(previousStatusCodes))
		common.AutomaticDisableChannelEnabled = previousAutoDisable
	})

	previousRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{"%s":0}`, clientModel)))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })

	var (
		bodyMu       sync.Mutex
		bodiesFromA  []string
		bodiesFromB  []string
		upstreamHits atomic.Int32
	)
	upstreamAHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodyMu.Lock()
		bodiesFromA = append(bodiesFromA, string(body))
		bodyMu.Unlock()
		upstreamHits.Add(1)
		// Channel A's upstream is fully saturated, so it NEVER reaches here
		// in this test — sibling holds the only slot, A's attempt is refused
		// before any wire call. We declare a stale 200 just in case a future
		// test reuses this server with different wiring.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-a","object":"chat.completion","created":1,"model":"` + upstreamA +
			`","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],` +
			`"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
	})
	upstreamBHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodyMu.Lock()
		bodiesFromB = append(bodiesFromB, string(body))
		bodyMu.Unlock()
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-b","object":"chat.completion","created":1,"model":"` + upstreamB +
			`","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],` +
			`"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
	})
	upstreamAServer := httptest.NewServer(upstreamAHandler)
	t.Cleanup(upstreamAServer.Close)
	upstreamBServer := httptest.NewServer(upstreamBHandler)
	t.Cleanup(upstreamBServer.Close)
	service.InitHttpClient()

	require.NoError(t, db.Create(&model.User{
		Id:       userID,
		Username: "wire-mapping-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    100000,
	}).Error)
	require.NoError(t, db.Create(&model.Token{
		Id:             userID,
		UserId:         userID,
		Key:            "wire-mapping-token-key",
		Name:           "wire-mapping-token",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}).Error)

	mappingA := `{"` + clientModel + `":"` + upstreamA + `"}`
	mappingB := `{"` + clientModel + `":"` + upstreamB + `"}`
	limitA, limitB := 1, 1
	channelA := &model.Channel{
		Id:               channelAID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "wire-mapping-channel-a-key",
		Name:             "wire-mapping-channel-a",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           clientModel,
		BaseURL:          &upstreamAServer.URL,
		ModelMapping:     &mappingA,
		ConcurrencyLimit: &limitA,
	}
	require.NoError(t, db.Create(channelA).Error)
	channelB := &model.Channel{
		Id:               channelBID,
		Type:             constant.ChannelTypeOpenAI,
		Key:              "wire-mapping-channel-b-key",
		Name:             "wire-mapping-channel-b",
		Status:           common.ChannelStatusEnabled,
		Group:            "default",
		Models:           clientModel,
		BaseURL:          &upstreamBServer.URL,
		ModelMapping:     &mappingB,
		ConcurrencyLimit: &limitB,
	}
	require.NoError(t, db.Create(channelB).Error)
	priority := int64(100)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     clientModel,
		ChannelId: channelAID,
		Enabled:   true,
		Priority:  &priority,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     clientModel,
		ChannelId: channelBID,
		Enabled:   true,
		Priority:  &priority,
	}).Error)

	lim := newChannelSlotLimiter()
	installChannelSlotLimiter(t, lim)

	// Sibling holds A's only slot so the first hop is refused before any
	// wire call. The retry hop then has B as the only candidate left.
	siblingRelease, err := lim.inner.Acquire(t.Context(), concurrency.ChannelScope(channelAID), limitA)
	require.NoError(t, err)
	t.Cleanup(func() { siblingRelease() })

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+clientModel+`","messages":[{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setSlotRelayIdentityKeys(c, userID, "wire-mapping-user", "wire-mapping-token-key")
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, channelA, clientModel))

	Relay(c, types.RelayFormatOpenAI)

	assert.Equal(t, http.StatusOK, recorder.Code,
		"retry hop must succeed (got body=%s)", recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), "chatcmpl-b",
		"the client-visible response must come from channel B (attempt 1 was saturated)")
	bodyMu.Lock()
	aBodies := append([]string(nil), bodiesFromA...)
	bBodies := append([]string(nil), bodiesFromB...)
	bodyMu.Unlock()

	require.Empty(t, aBodies,
		"channel A is saturated — its upstream must NOT be hit (got %d body)", len(aBodies))
	require.Len(t, bBodies, 1,
		"channel B must have seen exactly one wire request (the retry hop)")

	got := bBodies[0]
	assert.Contains(t, got, `"model":"`+upstreamB+`"`,
		"channel B's wire body must carry channel B's mapped name, not the client virtual name; got=%s", got)
	assert.NotContains(t, got, clientModel,
		"channel B's wire body must NOT carry the raw client-supplied model name; got=%s", got)
	assert.NotContains(t, got, upstreamA,
		"channel B's wire body must NOT carry channel A's mapped name (the bug this test pins); got=%s", got)
}

// TestSingleHopZhipuChannelSendsConfiguredModelMapping covers the plain
// first-hop case on the exact channel type the user reported (zhipu_v4, the
// provider that answered 404 "模型不存在" with the raw virtual name
// tyy_maas_kimi-k2.5). No retry, no saturation — one channel, one request —
// so a failure here means the configured channel model_mapping simply never
// reaches the upstream wire body, independent of any retry-loop behaviour.
//
// This is the "配置映射 → 上游收到映射名" verification: the channel row
// carries ModelMapping = {virtual name -> real zhipu model name}, and the
// assertion inspects the raw bytes the fake zhipu upstream received.
func TestSingleHopZhipuChannelSendsConfiguredModelMapping(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := newChannelSlotDB(t)

	const (
		clientModel = "tyy_maas_kimi-k2.5"
		zhipuModel  = "glm-5.3-flash"
		userID      = 7901
		channelID   = 7901
	)

	previousRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{"%s":0}`, clientModel)))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })

	var (
		bodyMu     sync.Mutex
		wireBodies []string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodyMu.Lock()
		wireBodies = append(wireBodies, string(body))
		bodyMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-zhipu","object":"chat.completion","created":1,"model":"` + zhipuModel +
			`","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],` +
			`"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
	}))
	t.Cleanup(upstream.Close)
	service.InitHttpClient()

	require.NoError(t, db.Create(&model.User{
		Id:       userID,
		Username: "zhipu-mapping-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    100000,
	}).Error)
	require.NoError(t, db.Create(&model.Token{
		Id:             userID,
		UserId:         userID,
		Key:            "zhipu-mapping-token-key",
		Name:           "zhipu-mapping-token",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}).Error)

	// The channel is configured with a model_mapping that translates the
	// client's virtual name to the real zhipu model name — exactly what the
	// user configured on the failing channel.
	modelMapping := `{"` + clientModel + `":"` + zhipuModel + `"}`
	channel := &model.Channel{
		Id:           channelID,
		Type:         constant.ChannelTypeZhipu_v4,
		Key:          "zhipu-mapping-channel-key",
		Name:         "zhipu-mapping-channel",
		Status:       common.ChannelStatusEnabled,
		Group:        "default",
		Models:       clientModel,
		BaseURL:      &upstream.URL,
		ModelMapping: &modelMapping,
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     clientModel,
		ChannelId: channelID,
		Enabled:   true,
	}).Error)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+clientModel+`","messages":[{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setSlotRelayIdentityKeys(c, userID, "zhipu-mapping-user", "zhipu-mapping-token-key")
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, channel, clientModel))

	Relay(c, types.RelayFormatOpenAI)

	assert.Equal(t, http.StatusOK, recorder.Code,
		"a single-hop zhipu request must succeed (got body=%s)", recorder.Body.String())
	bodyMu.Lock()
	got := append([]string(nil), wireBodies...)
	bodyMu.Unlock()

	require.Len(t, got, 1, "the zhipu upstream must have seen exactly one wire request")
	assert.Contains(t, got[0], `"model":"`+zhipuModel+`"`,
		"the configured channel model_mapping must reach the upstream wire body; got=%s", got[0])
	assert.NotContains(t, got[0], clientModel,
		"the upstream wire body must NOT carry the raw client virtual name; got=%s", got[0])
}

// _ ensures model + gorm imports are retained when running in isolation.
var _ = gorm.ErrRecordNotFound
