// FORK-LOCAL-EXTENSION: 2026-10-20 model-addressing contract matrix; remove-when: upstream gains an equivalent adaptor-addressing regression suite.
//
// The zhipu incident was not a zhipu bug. It was a contract break between two
// layers that normally agree: the channel's model_mapping translates a client
// virtual name into the name the upstream actually serves, and every relay
// format must carry that translated name to the wire — in the request BODY for
// OpenAI-style channels, or in the URL PATH for zhipu v3, gemini, cloudflare
// and AWS-style channels that address the model through the route.
//
// When the translation is lost the failure is loud but misleading: a
// path-addressed channel answers 404 "no such route", which reads as a missing
// model rather than a dropped mapping. This file pins the addressing contract
// for BOTH carriers so a future refactor of the mapping path fails here instead
// of reaching production as a 404 from one provider.
//
// The sibling relay_model_mapping_retry_test.go pins the multi-channel retry
// case; this file pins the per-carrier matrix for a single attempt.

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

// upstreamCapture records what a fake upstream actually received. Path is kept
// separate from Body because the two addressing carriers put the model in
// different places and the whole point of the matrix is to assert both.
type upstreamCapture struct {
	mu   sync.Mutex
	Path string
	Body string
	Hits int
}

func (c *upstreamCapture) record(r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Path = r.URL.Path
	c.Body = string(body)
	c.Hits++
}

func (c *upstreamCapture) snapshot() (string, string, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Path, c.Body, c.Hits
}

// TestChannelModelAddressingReachesUpstreamCarrier drives one request per
// (channel type × pass-through) pair and asserts the mapped model name reaches
// the carrier the channel type actually uses.
//
// addressingCarrier names where the model must land:
//   - "body" for OpenAI-compatible channels (POST {base}/chat/completions)
//   - "path" for zhipu v3 (POST {base}/model-api/{model}/invoke)
func TestChannelModelAddressingReachesUpstreamCarrier(t *testing.T) {
	require.NoError(t, i18n.Init())

	cases := []struct {
		name              string
		channelType       int
		addressingCarrier string
		passThrough       bool
	}{
		{name: "openai_body_conversion", channelType: constant.ChannelTypeOpenAI, addressingCarrier: "body", passThrough: false},
		{name: "openai_body_passthrough", channelType: constant.ChannelTypeOpenAI, addressingCarrier: "body", passThrough: true},
		{name: "zhipu_v4_body_conversion", channelType: constant.ChannelTypeZhipu_v4, addressingCarrier: "body", passThrough: false},
		{name: "zhipu_v4_body_passthrough", channelType: constant.ChannelTypeZhipu_v4, addressingCarrier: "body", passThrough: true},
		{name: "zhipu_v3_path_conversion", channelType: constant.ChannelTypeZhipu, addressingCarrier: "path", passThrough: false},
		{name: "zhipu_v3_path_passthrough", channelType: constant.ChannelTypeZhipu, addressingCarrier: "path", passThrough: true},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newChannelSlotDB(t)

			const (
				clientModel = "virtual-model-alias"
				upstreamMod = "real-upstream-model"
			)
			// Distinct ids per case: newChannelSlotDB keys its shared
			// in-memory database on the test name, but the ratio cache and
			// ability rows are process-global, so ids must not collide.
			baseUserID := 8100 + i*2
			baseChannelID := 8200 + i*2

			previousRatios := ratio_setting.ModelRatio2JSONString()
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{"%s":0}`, clientModel)))
			t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })

			capture := &upstreamCapture{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capture.record(r)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"chatcmpl-matrix","object":"chat.completion","created":1,"model":"` + upstreamMod +
					`","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],` +
					`"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
			}))
			t.Cleanup(upstream.Close)
			service.InitHttpClient()

			require.NoError(t, db.Create(&model.User{
				Id:       baseUserID,
				Username: fmt.Sprintf("addressing-user-%d", baseUserID),
				Password: "password",
				Status:   common.UserStatusEnabled,
				Group:    "default",
				Quota:    100000,
			}).Error)
			require.NoError(t, db.Create(&model.Token{
				Id:             baseUserID,
				UserId:         baseUserID,
				Key:            fmt.Sprintf("addressing-token-key-%d", baseUserID),
				Name:           "addressing-token",
				Status:         common.TokenStatusEnabled,
				ExpiredTime:    -1,
				UnlimitedQuota: true,
			}).Error)

			mapping := fmt.Sprintf(`{"%s":"%s"}`, clientModel, upstreamMod)
			channelSetting := `{}`
			if tc.passThrough {
				channelSetting = `{"pass_through_body_enabled":true}`
			}
			baseURL := upstream.URL
			channel := &model.Channel{
				Id:           baseChannelID,
				Type:         tc.channelType,
				Key:          fmt.Sprintf("addressing-channel-key-%d", baseChannelID),
				Name:         fmt.Sprintf("addressing-channel-%d", baseChannelID),
				Status:       common.ChannelStatusEnabled,
				Group:        "default",
				Models:       clientModel,
				BaseURL:      &baseURL,
				ModelMapping: &mapping,
				Setting:      &channelSetting,
			}
			require.NoError(t, db.Create(channel).Error)
			require.NoError(t, db.Create(&model.Ability{
				Group:     "default",
				Model:     clientModel,
				ChannelId: baseChannelID,
				Enabled:   true,
			}).Error)

			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
				strings.NewReader(`{"model":"`+clientModel+`","messages":[{"role":"user","content":"hi"}]}`))
			c.Request.Header.Set("Content-Type", "application/json")
			setSlotRelayIdentityKeys(c, baseUserID, channel.Name, fmt.Sprintf("addressing-token-key-%d", baseUserID))
			require.Nil(t, middleware.SetupContextForSelectedChannel(c, channel, clientModel))

			Relay(c, types.RelayFormatOpenAI)

			assert.Equal(t, http.StatusOK, recorder.Code,
				"the request must succeed (got body=%s)", recorder.Body.String())
			path, body, hits := capture.snapshot()
			require.Equal(t, 1, hits, "the upstream must have seen exactly one wire request")

			switch tc.addressingCarrier {
			case "path":
				assert.Contains(t, path, upstreamMod,
					"a path-addressed channel must route to the MAPPED model; got path=%s", path)
				assert.NotContains(t, path, clientModel,
					"the upstream route must NOT carry the raw client virtual name; got path=%s", path)
			case "body":
				assert.Contains(t, body, upstreamMod,
					"a body-addressed channel must send the MAPPED model; got body=%s", body)
				assert.NotContains(t, body, clientModel,
					"the wire body must NOT carry the raw client virtual name; got body=%s", body)
			default:
				t.Fatalf("unknown addressing carrier %q", tc.addressingCarrier)
			}
		})
	}
}

// TestChannelModelMappingIsNotAppliedTwiceForUnmappedChannels is the negative
// half of the contract: a channel WITHOUT model_mapping must forward the
// client's model name untouched. This guards against an over-eager fix that
// strips or rewrites the model name on every request regardless of whether the
// administrator configured a mapping at all.
func TestChannelModelMappingIsNotAppliedTwiceForUnmappedChannels(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := newChannelSlotDB(t)

	const (
		clientModel = "unmapped-plain-model"
		userID      = 8500
		channelID   = 8600
	)

	previousRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{"%s":0}`, clientModel)))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })

	capture := &upstreamCapture{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-unmapped","object":"chat.completion","created":1,"model":"` + clientModel +
			`","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],` +
			`"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
	}))
	t.Cleanup(upstream.Close)
	service.InitHttpClient()

	require.NoError(t, db.Create(&model.User{
		Id: userID, Username: "unmapped-user", Password: "password",
		Status: common.UserStatusEnabled, Group: "default", Quota: 100000,
	}).Error)
	require.NoError(t, db.Create(&model.Token{
		Id: userID, UserId: userID, Key: "unmapped-token-key", Name: "unmapped-token",
		Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true,
	}).Error)

	baseURL := upstream.URL
	emptyMapping := `{}`
	channel := &model.Channel{
		Id: channelID, Type: constant.ChannelTypeOpenAI, Key: "unmapped-channel-key",
		Name: "unmapped-channel", Status: common.ChannelStatusEnabled, Group: "default",
		Models: clientModel, BaseURL: &baseURL, ModelMapping: &emptyMapping,
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "default", Model: clientModel, ChannelId: channelID, Enabled: true,
	}).Error)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+clientModel+`","messages":[{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setSlotRelayIdentityKeys(c, userID, "unmapped-user", "unmapped-token-key")
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, channel, clientModel))

	Relay(c, types.RelayFormatOpenAI)

	assert.Equal(t, http.StatusOK, recorder.Code, "got body=%s", recorder.Body.String())
	_, body, hits := capture.snapshot()
	require.Equal(t, 1, hits, "the upstream must have seen exactly one wire request")
	assert.Contains(t, body, clientModel,
		"a channel without model_mapping must forward the client model name untouched; got body=%s", body)
}
