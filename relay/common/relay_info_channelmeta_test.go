// FORK-LOCAL-EXTENSION: 2026-10-20 InitChannelMeta model-mapping preservation pin; remove-when: upstream gains an equivalent regression test.
//
// This file guards the single line-pair that broke the zhipu channel in
// production: InitChannelMeta rebuilds ChannelMeta on every relay attempt, and
// it used to reset both UpstreamModelName (to ContextKeyOriginalModel) and
// request.Model (to OriginModelName). The controller relay loop applies the
// selected channel's model_mapping BEFORE this method runs, so the reset
// silently discarded the mapping and the upstream received the raw client
// virtual name — a 404 "model not found" with no error anywhere in the logs.
//
// e2e/channel_model_addressing_test.go proves the mapping reaches the wire for
// the formats that exist today. This file pins the invariant at the unit level
// so a future refactor of InitChannelMeta fails here first, with a precise
// message, instead of surfacing weeks later as a 404 from one provider.

package common

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newChannelMetaTestContext builds the gin context shape that
// middleware.SetupContextForSelectedChannel publishes for one attempt, plus the
// original_model key every relay path relies on.
func newChannelMetaTestContext(originalModel string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, originalModel)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeZhipu_v4)
	common.SetContextKey(c, constant.ContextKeyChannelId, 42)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://example.invalid")
	common.SetContextKey(c, constant.ContextKeyChannelKey, "sk-test")
	return c
}

// TestInitChannelMetaPreservesUpstreamModelNameAcrossRetries is the regression
// pin for the 404: the controller loop maps the virtual name, then InitChannelMeta
// runs on the attempt. The mapped name must survive both the first call (where
// ChannelMeta is nil and the guard must not panic) and every later call (where
// ChannelMeta is replaced wholesale by a cross-channel retry).
func TestInitChannelMetaPreservesUpstreamModelNameAcrossRetries(t *testing.T) {
	const (
		originModel  = "tyy_maas_kimi-k2.5"
		upstreamName = "glm-5.3-flash"
	)

	c := newChannelMetaTestContext(originModel)
	request := &dto.GeneralOpenAIRequest{Model: upstreamName}
	info := &RelayInfo{OriginModelName: originModel, Request: request}

	// Simulate what helper.ModelMappedHelper does inside the controller loop:
	// it allocates ChannelMeta when absent, then writes the mapped name.
	info.ChannelMeta = &ChannelMeta{}
	info.UpstreamModelName = upstreamName
	info.IsModelMapped = true

	// Attempt 1: same channel. Attempt 2: the retry hop re-enters with a
	// different channel id, which is exactly when the old reset fired.
	for attempt, channelID := range []int{42, 77} {
		common.SetContextKey(c, constant.ContextKeyChannelId, channelID)
		info.InitChannelMeta(c)

		assert.Equal(t, upstreamName, info.UpstreamModelName,
			"attempt %d: InitChannelMeta must not reset the mapped upstream model name back to the client virtual name", attempt+1)
		assert.True(t, info.IsModelMapped,
			"attempt %d: InitChannelMeta must not clear the model-mapped flag", attempt+1)
		assert.Equal(t, channelID, info.ChannelId,
			"attempt %d: InitChannelMeta must still adopt the newly selected channel identity", attempt+1)
		assert.Equal(t, upstreamName, request.Model,
			"attempt %d: InitChannelMeta must not rewrite the outgoing request model back to OriginModelName", attempt+1)
		assert.Equal(t, originModel, info.OriginModelName,
			"attempt %d: OriginModelName stays the client-supplied name for pricing and logging", attempt+1)
	}
}

// TestInitChannelMetaFallsBackToOriginalModelOnFirstHop covers the other side of
// the guard: a path that bypasses the controller mapping loop (the channel test
// endpoint, for example) has no ChannelMeta yet, so InitChannelMeta must seed
// UpstreamModelName from the request instead of leaving it empty.
func TestInitChannelMetaFallsBackToOriginalModelOnFirstHop(t *testing.T) {
	const originModel = "tyy_maas_kimi-k2.5"

	c := newChannelMetaTestContext(originModel)
	info := &RelayInfo{OriginModelName: originModel}

	require.Nil(t, info.ChannelMeta, "precondition: no channel has been selected yet")

	info.InitChannelMeta(c)

	assert.Equal(t, originModel, info.UpstreamModelName,
		"the first hop must seed the upstream model name from the original request model")
	assert.False(t, info.IsModelMapped, "the first hop cannot claim a mapping that was never applied")
}
