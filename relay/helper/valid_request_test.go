package helper

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestGetAndValidAudioRequest pins the request contract of the three media
// endpoints (voice clone, voice design, music) and the MiniMax-only channel
// gate that keeps them away from other channels.
func TestGetAndValidAudioRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newContext := func(path, body string, channelType int) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		common.SetContextKey(c, constant.ContextKeyChannelType, channelType)
		return c
	}

	longPreviewText := strings.Repeat("a", 501)

	for _, tt := range []struct {
		name        string
		path        string
		relayMode   int
		body        string
		channelType int
		wantErr     string
	}{
		{
			name:        "voice clone accepts a complete request",
			path:        "/v1/audio/voice/clone",
			relayMode:   relayconstant.RelayModeAudioVoiceClone,
			body:        `{"model":"speech-01","voice":"voice-abc","input":"hello","metadata":{"file_id":123}}`,
			channelType: constant.ChannelTypeMiniMax,
		},
		{
			name:        "voice clone accepts a clone_prompt without replacing file_id",
			path:        "/v1/audio/voice/clone",
			relayMode:   relayconstant.RelayModeAudioVoiceClone,
			body:        `{"model":"speech-01","voice":"voice-abc","input":"hello","metadata":{"file_id":123,"clone_prompt":{"prompt_text":"hello"}}}`,
			channelType: constant.ChannelTypeMiniMax,
		},
		{
			name:        "voice design accepts a complete request",
			path:        "/v1/audio/voice/design",
			relayMode:   relayconstant.RelayModeAudioVoiceDesign,
			body:        `{"model":"speech-02-hd","input":"a warm voice","metadata":{"preview_text":"hello"}}`,
			channelType: constant.ChannelTypeMiniMax,
		},
		{
			name:        "music accepts a request with input",
			path:        "/v1/audio/music",
			relayMode:   relayconstant.RelayModeAudioMusic,
			body:        `{"model":"music-3.0","input":"a happy song"}`,
			channelType: constant.ChannelTypeMiniMax,
		},
		{
			name:        "music accepts a request with lyrics only",
			path:        "/v1/audio/music",
			relayMode:   relayconstant.RelayModeAudioMusic,
			body:        `{"model":"music-3.0","metadata":{"lyrics":"la la la"}}`,
			channelType: constant.ChannelTypeMiniMax,
		},
		{
			name:        "voice clone rejects a missing file_id",
			path:        "/v1/audio/voice/clone",
			relayMode:   relayconstant.RelayModeAudioVoiceClone,
			body:        `{"model":"speech-01","voice":"voice-abc","input":"hello"}`,
			channelType: constant.ChannelTypeMiniMax,
			wantErr:     "metadata.file_id is required",
		},
		{
			name:        "voice clone rejects a missing voice",
			path:        "/v1/audio/voice/clone",
			relayMode:   relayconstant.RelayModeAudioVoiceClone,
			body:        `{"model":"speech-01","input":"hello","metadata":{"file_id":123}}`,
			channelType: constant.ChannelTypeMiniMax,
			wantErr:     "voice is required",
		},
		{
			name:        "voice design rejects a missing preview_text",
			path:        "/v1/audio/voice/design",
			relayMode:   relayconstant.RelayModeAudioVoiceDesign,
			body:        `{"model":"speech-02-hd","input":"a warm voice"}`,
			channelType: constant.ChannelTypeMiniMax,
			wantErr:     "metadata.preview_text is required",
		},
		{
			name:        "voice design rejects a preview_text longer than 500 characters",
			path:        "/v1/audio/voice/design",
			relayMode:   relayconstant.RelayModeAudioVoiceDesign,
			body:        `{"model":"speech-02-hd","input":"a warm voice","metadata":{"preview_text":"` + longPreviewText + `"}}`,
			channelType: constant.ChannelTypeMiniMax,
			wantErr:     "metadata.preview_text must be at most 500 characters",
		},
		{
			name:        "voice design rejects a missing input",
			path:        "/v1/audio/voice/design",
			relayMode:   relayconstant.RelayModeAudioVoiceDesign,
			body:        `{"model":"speech-02-hd","metadata":{"preview_text":"hello"}}`,
			channelType: constant.ChannelTypeMiniMax,
			wantErr:     "input is required",
		},
		{
			name:        "music rejects a request without input and lyrics",
			path:        "/v1/audio/music",
			relayMode:   relayconstant.RelayModeAudioMusic,
			body:        `{"model":"music-3.0"}`,
			channelType: constant.ChannelTypeMiniMax,
			wantErr:     "input or metadata.lyrics is required",
		},
		{
			name:        "voice clone is rejected on a non minimax channel",
			path:        "/v1/audio/voice/clone",
			relayMode:   relayconstant.RelayModeAudioVoiceClone,
			body:        `{"model":"speech-01","voice":"voice-abc","input":"hello","metadata":{"file_id":123}}`,
			channelType: constant.ChannelTypeOpenAI,
			wantErr:     "channel does not support this endpoint",
		},
		{
			name:        "voice design is rejected on a non minimax channel",
			path:        "/v1/audio/voice/design",
			relayMode:   relayconstant.RelayModeAudioVoiceDesign,
			body:        `{"model":"speech-02-hd","input":"a warm voice","metadata":{"preview_text":"hello"}}`,
			channelType: constant.ChannelTypeOpenAI,
			wantErr:     "channel does not support this endpoint",
		},
		{
			name:        "music is rejected on a non minimax channel",
			path:        "/v1/audio/music",
			relayMode:   relayconstant.RelayModeAudioMusic,
			body:        `{"model":"music-3.0","input":"a happy song"}`,
			channelType: constant.ChannelTypeOpenAI,
			wantErr:     "channel does not support this endpoint",
		},
		{
			name:        "speech stays available on other channels",
			path:        "/v1/audio/speech",
			relayMode:   relayconstant.RelayModeAudioSpeech,
			body:        `{"model":"tts-1","input":"hello"}`,
			channelType: constant.ChannelTypeOpenAI,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newContext(tt.path, tt.body, tt.channelType)
			request, err := GetAndValidAudioRequest(c, tt.relayMode)
			if tt.wantErr != "" {
				require.Error(t, err)
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, request)
		})
	}
}
