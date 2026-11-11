// FORK-LOCAL-EXTENSION: 2026-10-20 pass-through body + model_mapping regression test; remove-when: upstream merges the same behaviour or supersedes it.
//
// Pins that a channel with pass-through body enabled still forwards the
// CHANNEL-MAPPED model name upstream. Pass-through means "skip protocol
// conversion, forward the client's body shape" — it is not a licence to
// skip model ADDRESSING. A channel whose model_mapping translates
// virtual-name -> upstream-name and whose upstream only knows the upstream
// name must still receive the mapped name, otherwise the request 404s with
// "model not found" exactly like the non-pass-through bug this file's
// sibling (relay_model_mapping_retry_test.go) pins.
//
// The test is deliberately the same shape as the zhipu single-hop case so a
// future regression reads as "pass-through lost the mapping".

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

// TestPassThroughChannelStillSendsConfiguredModelMapping pins that a channel
// with BOTH model_mapping and pass_through_body_enabled forwards the mapped
// upstream model name, not the client's virtual name.
func TestPassThroughChannelStillSendsConfiguredModelMapping(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := newChannelSlotDB(t)

	const (
		clientModel = "tyy_maas_kimi-k2.5"
		zhipuModel  = "glm-5.3-flash"
		userID      = 7911
		channelID   = 7911
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
		_, _ = w.Write([]byte(`{"id":"chatcmpl-passthrough","object":"chat.completion","created":1,"model":"` + zhipuModel +
			`","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],` +
			`"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
	}))
	t.Cleanup(upstream.Close)
	service.InitHttpClient()

	require.NoError(t, db.Create(&model.User{
		Id:       userID,
		Username: "passthrough-mapping-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    100000,
	}).Error)
	require.NoError(t, db.Create(&model.Token{
		Id:             userID,
		UserId:         userID,
		Key:            "passthrough-mapping-token-key",
		Name:           "passthrough-mapping-token",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}).Error)

	modelMapping := `{"` + clientModel + `":"` + zhipuModel + `"}`
	// pass_through_body_enabled lives inside the channel `setting` JSON blob.
	channelSetting := `{"pass_through_body_enabled":true}`
	channel := &model.Channel{
		Id:           channelID,
		Type:         constant.ChannelTypeZhipu_v4,
		Key:          "passthrough-mapping-channel-key",
		Name:         "passthrough-mapping-channel",
		Status:       common.ChannelStatusEnabled,
		Group:        "default",
		Models:       clientModel,
		BaseURL:      &upstream.URL,
		ModelMapping: &modelMapping,
		Setting:      &channelSetting,
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
	setSlotRelayIdentityKeys(c, userID, "passthrough-mapping-user", "passthrough-mapping-token-key")
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, channel, clientModel))

	Relay(c, types.RelayFormatOpenAI)

	assert.Equal(t, http.StatusOK, recorder.Code,
		"the pass-through request must still succeed (got body=%s)", recorder.Body.String())
	bodyMu.Lock()
	got := append([]string(nil), wireBodies...)
	bodyMu.Unlock()

	require.Len(t, got, 1, "the upstream must have seen exactly one wire request")
	assert.Contains(t, got[0], `"model":"`+zhipuModel+`"`,
		"a pass-through channel with model_mapping must still forward the MAPPED model name; got=%s", got[0])
	assert.NotContains(t, got[0], clientModel,
		"a pass-through channel must NOT forward the raw client virtual name when a mapping exists; got=%s", got[0])
}
