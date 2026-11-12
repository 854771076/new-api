package apimart

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIMartAdaptor(t *testing.T) {
	gin.SetMode(gin.TestMode)

	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RequestURLPath: "/v1/chat/completions",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeAPIMart,
			ChannelBaseUrl: "https://api.apimart.ai",
			ApiKey:         "test-key",
		},
	}
	adaptor.Init(info)

	url, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://api.apimart.ai/v1/chat/completions", url)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set("Content-Type", "application/json")

	header := http.Header{}
	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	assert.Equal(t, "Bearer test-key", header.Get("Authorization"))

	assert.Empty(t, adaptor.GetModelList())
	assert.Equal(t, "apimart", adaptor.GetChannelName())

	_, err = adaptor.ConvertImageRequest(c, info, dto.ImageRequest{Model: "gpt-image-1"})
	require.Error(t, err)

	_, err = adaptor.ConvertRerankRequest(c, relayconstant.RelayModeRerank, dto.RerankRequest{Model: "rerank-v1"})
	require.Error(t, err)

	_, err = adaptor.ConvertAudioRequest(c, info, dto.AudioRequest{Model: "tts-1"})
	require.Error(t, err)

	_, err = adaptor.ConvertClaudeRequest(c, info, &dto.ClaudeRequest{Model: "claude-sonnet-4-5"})
	require.Error(t, err)

	_, err = adaptor.ConvertGeminiRequest(c, info, &dto.GeminiChatRequest{})
	require.Error(t, err)
}
