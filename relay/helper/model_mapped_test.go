package helper

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestModelMappedHelperAppliesMappingFromContext pins that ModelMappedHelper
// reads the channel's model_mapping from the gin context (the channel key
// set by middleware.SetupContextForSelectedChannel) and writes the resolved
// upstream name into both info.UpstreamModelName and the request struct. The
// controller relay loop must call this helper EVERY iteration, after
// getChannel populates the per-channel model_mapping for the attempt.
func TestModelMappedHelperAppliesMappingFromContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	// This is the exact value middleware.SetupContextForSelectedChannel puts
	// into the gin context for channel A: {"client-model":"upstream-a"}.
	c.Set(string(constant.ContextKeyChannelModelMapping),
		`{"client-model":"upstream-a"}`)

	info := &relaycommon.RelayInfo{
		OriginModelName: "client-model",
	}
	request := &dto.GeneralOpenAIRequest{Model: "client-model"}

	require.NoError(t, ModelMappedHelper(c, info, request))

	assert.Equal(t, "upstream-a", info.UpstreamModelName,
		"info.UpstreamModelName must be the mapped name from context")
	assert.Equal(t, "upstream-a", request.Model,
		"request.Model must be updated so the converted JSON carries the mapped name")
	assert.True(t, info.IsModelMapped,
		"info.IsModelMapped must be true when the origin name resolves via the channel mapping")
}

// TestModelMappedHelperObservesPerAttemptMapping pins the controller-retry fix:
// the same helper called AGAIN with a NEW channel's mapping in context MUST
// re-translate the request — otherwise a retry that selects a channel with a
// different model_mapping would forward the FIRST iteration's mapped name
// instead of the SECOND iteration's. Mirrors the getChannel → ModelMappedHelper
// ordering the controller now uses; pre-fix the call ran once before the
// loop, freezing the mapping for every retry.
func TestModelMappedHelperObservesPerAttemptMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	// Iteration 1: channel A's mapping.
	c.Set(string(constant.ContextKeyChannelModelMapping),
		`{"client-model":"upstream-a"}`)

	info := &relaycommon.RelayInfo{OriginModelName: "client-model"}
	request := &dto.GeneralOpenAIRequest{Model: "client-model"}

	require.NoError(t, ModelMappedHelper(c, info, request))
	require.Equal(t, "upstream-a", info.UpstreamModelName)
	require.Equal(t, "upstream-a", request.Model)

	// Iteration 2: same call, but the context now carries channel B's mapping.
	// The retry path replaces model_mapping in the gin context for every
	// attempt; the helper must observe the NEW mapping, not the cached one.
	c.Set(string(constant.ContextKeyChannelModelMapping),
		`{"client-model":"upstream-b"}`)
	// info and request carry over from iteration 1.
	require.NoError(t, ModelMappedHelper(c, info, request))

	assert.Equal(t, "upstream-b", info.UpstreamModelName,
		"info.UpstreamModelName must be re-translated per retry attempt")
	assert.Equal(t, "upstream-b", request.Model,
		"request.Model must be re-translated per retry attempt so the upstream receives the new channel's mapped name")
}

// TestModelMappedHelperEmptyMappingLeavesRequestUntouched pins the safety net:
// a channel with no model_mapping (empty or "{}") must not modify the
// request. This is the path the no-mapping-selected-channel retry hop takes
// and it must remain a no-op rather than erasing the client-supplied model
// name.
func TestModelMappedHelperEmptyMappingLeavesRequestUntouched(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name    string
		mapping string
	}{
		{name: "empty string", mapping: ""},
		{name: "empty json object", mapping: "{}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set(string(constant.ContextKeyChannelModelMapping), tc.mapping)

			info := &relaycommon.RelayInfo{OriginModelName: "client-model"}
			request := &dto.GeneralOpenAIRequest{Model: "client-model"}

			require.NoError(t, ModelMappedHelper(c, info, request))

			assert.Equal(t, "", info.UpstreamModelName,
				"empty mapping must not populate UpstreamModelName (it stays zero)")
			assert.Equal(t, "client-model", request.Model,
				"empty mapping must leave request.Model untouched")
			assert.False(t, info.IsModelMapped)
		})
	}
}
