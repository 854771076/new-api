// FORK-LOCAL-EXTENSION: 2026-10-20 billing-vs-addressing identity regression suite; remove-when: upstream ships an equivalent model-identity contract test.
//
// There are two model names in every mapped relay request and confusing them is
// SILENT, which makes it worse than the 404 this fork was forked over:
//
//	OriginModelName    the name the client sent   -> pricing, logs, health keys
//	UpstreamModelName  the name after model_mapping -> upstream addressing only
//
// Swapping them charges the customer the upstream provider's price under a name
// they never asked for, or logs a model that does not exist in the catalogue.
// The addressing half (UpstreamModelName reaching the wire) is already pinned by
// relay_model_addressing_matrix_test.go and relay_model_mapping_retry_test.go.
// This file pins the billing half over the SAME real relay path: one request,
// one upstream, and then assertions on the consume log and the quota actually
// deducted.
//
// The discrimination comes from the price tables, not from mocks: the virtual
// name and the mapped name are registered with deliberately different model and
// completion ratios, so every assertion states which table was consulted.

package controller

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	billingVirtualModel = "virtual-billing-model"
	billingMappedModel  = "real-billing-model"

	// Usage the fake upstream reports. Fixed so the expected charge is an exact
	// integer and the test never depends on a tokenizer.
	billingPromptTokens     = 3
	billingCompletionTokens = 1
)

// billingWireRecorder captures what the upstream actually received, so the
// addressing assertion and the billing assertions describe the same request.
type billingWireRecorder struct {
	mu   sync.Mutex
	Body string
	Hits int
}

func (r *billingWireRecorder) record(req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Body = string(body)
	r.Hits++
}

func (r *billingWireRecorder) snapshot() (string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Body, r.Hits
}

// consumeLogForToken returns the consume log the relay wrote for this token.
// Consume logs are written synchronously inside the relay, so one row must
// exist by the time Relay returns.
func consumeLogForToken(t *testing.T, tokenID int) model.Log {
	t.Helper()
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Where("type = ? AND token_id = ?", model.LogTypeConsume, tokenID).
		Order("id desc").Find(&logs).Error)
	require.NotEmpty(t, logs, "a successful relay must write exactly one consume log for the token")
	return logs[0]
}

// logOtherPublic parses the persisted `other` blob into its flat public scope.
func logOtherPublic(t *testing.T, raw string) map[string]any {
	t.Helper()
	var other map[string]any
	require.NoError(t, common.UnmarshalJsonStr(raw, &other))
	return other
}

func logOtherAdmin(t *testing.T, public map[string]any) map[string]any {
	t.Helper()
	admin, _ := public["admin_info"].(map[string]any)
	return admin
}

// TestRelayBillsAndLogsVirtualNameWhileAddressingMappedName is the core
// identity pin. One request, one channel, one mapping:
//
//	virtual-billing-model -> real-billing-model
//
// and the two names carry different prices. The test then asserts all four
// halves of the boundary at once:
//
//	upstream body          carries the MAPPED name   (addressing)
//	consume log model_name carries the VIRTUAL name  (catalogue / health keys)
//	consume log model_ratio + deducted quota follow the VIRTUAL price table
//	OriginModelName stays the VIRTUAL name            (proven by the three above)
func TestRelayBillsAndLogsVirtualNameWhileAddressingMappedName(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := newChannelSlotDB(t)

	const (
		userID          = 9201
		channelID       = 9202
		virtualRatio    = 1.0
		virtualComplete = 1.0
		mappedRatio     = 100.0
		mappedComplete  = 3.0
	)

	previousModelRatios := ratio_setting.ModelRatio2JSONString()
	previousCompletionRatios := ratio_setting.CompletionRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousModelRatios))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(previousCompletionRatios))
	})
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(
		`{%q:%g,%q:%g}`, billingVirtualModel, virtualRatio, billingMappedModel, mappedRatio)))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(fmt.Sprintf(
		`{%q:%g,%q:%g}`, billingVirtualModel, virtualComplete, billingMappedModel, mappedComplete)))

	recorder := &billingWireRecorder{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-billing","object":"chat.completion","created":1,"model":"`+
			billingMappedModel+`","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},`+
			`"finish_reason":"stop"}],"usage":{"prompt_tokens":`+
			fmt.Sprint(billingPromptTokens)+`,"completion_tokens":`+fmt.Sprint(billingCompletionTokens)+
			`,"total_tokens":`+fmt.Sprint(billingPromptTokens+billingCompletionTokens)+`}}`)
	}))
	t.Cleanup(upstream.Close)
	service.InitHttpClient()

	const initialTokenQuota = 100000
	require.NoError(t, db.Create(&model.User{
		Id: userID, Username: "billing-identity-user", Password: "password",
		Status: common.UserStatusEnabled, Group: "default", Quota: 100000,
	}).Error)
	require.NoError(t, db.Create(&model.Token{
		Id: userID, UserId: userID, Key: "billing-identity-token-key", Name: "billing-identity-token",
		Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: initialTokenQuota,
	}).Error)

	mapping := fmt.Sprintf(`{%q:%q}`, billingVirtualModel, billingMappedModel)
	baseURL := upstream.URL
	channel := &model.Channel{
		Id:           channelID,
		Type:         constant.ChannelTypeOpenAI,
		Key:          "billing-identity-channel-key",
		Name:         "billing-identity-channel",
		Status:       common.ChannelStatusEnabled,
		Group:        "default",
		Models:       billingVirtualModel,
		BaseURL:      &baseURL,
		ModelMapping: &mapping,
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "default", Model: billingVirtualModel, ChannelId: channelID, Enabled: true,
	}).Error)

	gin.SetMode(gin.TestMode)
	httpRecorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(httpRecorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+billingVirtualModel+`","messages":[{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setSlotRelayIdentityKeys(c, userID, "billing-identity-user", "billing-identity-token-key")
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, channel, billingVirtualModel))

	Relay(c, types.RelayFormatOpenAI)

	require.Equal(t, http.StatusOK, httpRecorder.Code, "got body=%s", httpRecorder.Body.String())

	// --- addressing half: the upstream must be given the MAPPED name ---------
	body, hits := recorder.snapshot()
	require.Equal(t, 1, hits, "the upstream must have seen exactly one wire request")
	assert.Contains(t, body, fmt.Sprintf(`"model":"%s"`, billingMappedModel),
		"upstream addressing must use the mapped name; got body=%s", body)
	assert.NotContains(t, body, billingVirtualModel,
		"the raw client virtual name must never reach a mapped channel; got body=%s", body)

	// --- catalogue half: the consume log must be filed under the VIRTUAL name
	log := consumeLogForToken(t, userID)
	assert.Equal(t, billingVirtualModel, log.ModelName,
		"the consume log is the catalogue/health identity and must keep the client's virtual name")
	assert.Equal(t, channelID, log.ChannelId)
	assert.Equal(t, billingPromptTokens, log.PromptTokens)
	assert.Equal(t, billingCompletionTokens, log.CompletionTokens)

	other := logOtherPublic(t, log.Other)
	assert.Equal(t, billingMappedModel, other["upstream_model_name"],
		"the mapped name belongs in the log's upstream_model_name field, not in model_name")
	assert.Equal(t, true, other["is_model_mapped"])
	assert.NotContains(t, logOtherAdmin(t, other), "billing_model",
		"a plain virtual name resolves to itself, so the log must not carry a separate admin billing_model override")

	// --- pricing half: which table was consulted, and how much was charged --
	groupRatio := ratio_setting.GetGroupRatio("default")
	wantQuota := int(float64(billingPromptTokens+int(billingCompletionTokens*virtualComplete)) *
		virtualRatio * groupRatio)
	mappedQuota := int(float64(billingPromptTokens+int(billingCompletionTokens*mappedComplete)) *
		mappedRatio * groupRatio)
	require.NotEqual(t, wantQuota, mappedQuota,
		"the fixture must price the two names differently, otherwise the assertion below is vacuous")

	assert.Equal(t, virtualRatio, other["model_ratio"],
		"the logged model_ratio must come from the VIRTUAL name's price entry")
	assert.Equal(t, virtualComplete, other["completion_ratio"])
	assert.Equal(t, wantQuota, log.Quota,
		"the settled charge must follow the VIRTUAL name's price, not the mapped name's (mapped would be %d)", mappedQuota)

	var token model.Token
	require.NoError(t, db.First(&token, userID).Error)
	assert.Equal(t, initialTokenQuota-wantQuota, token.RemainQuota,
		"the token must be debited the virtual-name charge, never the mapped-name one")
}

// TestRelayRefusesToBillAnUnpricedVirtualNameWhenOnlyTheMappedNameHasAPrice
// pins the other half of the same boundary as a client-visible outcome: the
// mapping is an ADDRESSING rule, not a pricing alias. When the client virtual
// name has no price configured but the mapped upstream name does, the request
// must still be priced against the virtual name and therefore be refused —
// instead of silently falling through to the upstream model's price.
//
// This is the failure an operator hits after "fixing" a 404 by adding a mapping:
// the 404 is gone and the request now fails on pricing instead, which reads
// like a billing misconfiguration rather than a missing catalogue entry.
func TestRelayRefusesToBillAnUnpricedVirtualNameWhenOnlyTheMappedNameHasAPrice(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := newChannelSlotDB(t)

	const (
		unpricedVirtual = "unpriced-virtual-billing-model"
		pricedMapped    = "priced-mapped-billing-model"
		userID          = 9211
		channelID       = 9212
	)

	previousModelRatios := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousModelRatios)) })
	// Only the mapped upstream name carries a price.
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{%q:%g}`, pricedMapped, 1.0)))

	var upstreamHits int
	var hitMu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitMu.Lock()
		upstreamHits++
		hitMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-unpriced","object":"chat.completion","created":1,"model":"`+
			pricedMapped+`","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
	}))
	t.Cleanup(upstream.Close)
	service.InitHttpClient()

	require.NoError(t, db.Create(&model.User{
		Id: userID, Username: "unpriced-billing-user", Password: "password",
		Status: common.UserStatusEnabled, Group: "default", Quota: 100000,
	}).Error)
	require.NoError(t, db.Create(&model.Token{
		Id: userID, UserId: userID, Key: "unpriced-billing-token-key", Name: "unpriced-billing-token",
		Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true,
	}).Error)

	mapping := fmt.Sprintf(`{%q:%q}`, unpricedVirtual, pricedMapped)
	baseURL := upstream.URL
	channel := &model.Channel{
		Id: channelID, Type: constant.ChannelTypeOpenAI, Key: "unpriced-billing-channel-key",
		Name: "unpriced-billing-channel", Status: common.ChannelStatusEnabled, Group: "default",
		Models: unpricedVirtual, BaseURL: &baseURL, ModelMapping: &mapping,
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "default", Model: unpricedVirtual, ChannelId: channelID, Enabled: true,
	}).Error)

	gin.SetMode(gin.TestMode)
	httpRecorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(httpRecorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+unpricedVirtual+`","messages":[{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setSlotRelayIdentityKeys(c, userID, "unpriced-billing-user", "unpriced-billing-token-key")
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, channel, unpricedVirtual))

	Relay(c, types.RelayFormatOpenAI)

	assert.NotEqual(t, http.StatusOK, httpRecorder.Code,
		"a virtual name with no price entry must not be billed through the mapped name's price; body=%s", httpRecorder.Body.String())
	hitMu.Lock()
	defer hitMu.Unlock()
	assert.Equal(t, 0, upstreamHits,
		"the request must be refused at pricing time, before it is ever sent upstream")
}
