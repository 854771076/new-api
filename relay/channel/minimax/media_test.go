package minimax

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMiniMaxMediaVoiceCloneRequest(t *testing.T) {
	t.Parallel()

	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeAudioVoiceClone,
		OriginModelName: "speech-02-hd",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "speech-02-hd",
		},
	}

	body, err := adaptor.ConvertAudioRequest(gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New()), info, dto.AudioRequest{
		Model:    "speech-02-hd",
		Input:    "hello clone",
		Voice:    "caller-voice-id",
		Metadata: json.RawMessage(`{"file_id":123456,"clone_prompt":{"prompt_audio":"https://example.com/a.mp3","prompt_text":"prompt text"},"accuracy":0.8}`),
	})
	require.NoError(t, err)

	payload := decodeMiniMaxMediaBody(t, body)
	assert.Equal(t, float64(123456), payload["file_id"])
	assert.Equal(t, "caller-voice-id", payload["voice_id"])
	assert.Equal(t, "hello clone", payload["text"])
	assert.Equal(t, "speech-02-hd", payload["model"])
	assert.Equal(t, float64(0.8), payload["accuracy"])
	clonePrompt, ok := payload["clone_prompt"].(map[string]any)
	require.True(t, ok, "clone_prompt should be present in the upstream request")
	assert.Equal(t, "https://example.com/a.mp3", clonePrompt["prompt_audio"])
	assert.Equal(t, "prompt text", clonePrompt["prompt_text"])

	// 缺 file_id 时在网关就被拒
	_, err = adaptor.ConvertAudioRequest(gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New()), info, dto.AudioRequest{
		Model:    "speech-02-hd",
		Voice:    "caller-voice-id",
		Metadata: json.RawMessage(`{"clone_prompt":{"prompt_audio":"https://example.com/a.mp3"}}`),
	})
	require.Error(t, err)
	var apiErr *types.NewAPIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Contains(t, apiErr.Error(), "file_id")

	// voice_id 由调用方生成并传入，缺了同样被拒
	_, err = adaptor.ConvertAudioRequest(gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New()), info, dto.AudioRequest{
		Model:    "speech-02-hd",
		Metadata: json.RawMessage(`{"file_id":123456}`),
	})
	require.Error(t, err)
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Contains(t, apiErr.Error(), "voice")
}

func TestMiniMaxMediaVoiceDesignRequest(t *testing.T) {
	t.Parallel()

	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeAudioVoiceDesign,
		OriginModelName: "speech-02-hd",
	}

	body, err := adaptor.ConvertAudioRequest(gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New()), info, dto.AudioRequest{
		Model:    "speech-02-hd",
		Input:    "a warm female voice",
		Voice:    "designer-voice-id",
		Metadata: json.RawMessage(`{"preview_text":"hello"}`),
	})
	require.NoError(t, err)

	payload := decodeMiniMaxMediaBody(t, body)
	assert.Equal(t, "a warm female voice", payload["prompt"])
	assert.Equal(t, "hello", payload["preview_text"])
	assert.Equal(t, "designer-voice-id", payload["voice_id"])

	// 响应里的 voice_id 由上游给出
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	trialAudio := []byte("trial-audio")
	resp := newMiniMaxMediaResponse(`{"voice_id":"upstream-voice-id","trial_audio":"` + hex.EncodeToString(trialAudio) + `","base_resp":{"status_code":0,"status_msg":"success"}}`)

	usage, apiErr := handleVoiceDesignResponse(c, resp, info)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	var result MiniMaxVoiceDesignResult
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	assert.Equal(t, "upstream-voice-id", result.VoiceID)
	assert.Equal(t, base64.StdEncoding.EncodeToString(trialAudio), result.TrialAudioB64)
}

func TestMiniMaxMediaMusicRequest(t *testing.T) {
	t.Parallel()

	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeAudioMusic,
		OriginModelName: "music-3.0",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "music-3.0",
		},
	}

	body, err := adaptor.ConvertAudioRequest(gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New()), info, dto.AudioRequest{
		Model:    "music-3.0",
		Input:    "a calm piano piece",
		Metadata: json.RawMessage(`{"lyrics":"la la la","output_format":"wav","audio_setting":{"sample_rate":44100,"bitrate":256000,"format":"mp3"},"is_instrumental":true}`),
	})
	require.NoError(t, err)

	payload := decodeMiniMaxMediaBody(t, body)
	assert.Equal(t, "music-3.0", payload["model"])
	assert.Equal(t, "a calm piano piece", payload["prompt"])
	assert.Equal(t, "la la la", payload["lyrics"])
	assert.Equal(t, "wav", payload["output_format"])
	assert.Equal(t, true, payload["is_instrumental"])
	audioSetting, ok := payload["audio_setting"].(map[string]any)
	require.True(t, ok, "audio_setting should be present in the upstream request")
	assert.Equal(t, float64(44100), audioSetting["sample_rate"])

	// output_format 没显式给出时缺省 mp3
	body, err = adaptor.ConvertAudioRequest(gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New()), info, dto.AudioRequest{
		Model:    "music-3.0",
		Input:    "a calm piano piece",
		Metadata: json.RawMessage(`{"lyrics":"la la la"}`),
	})
	require.NoError(t, err)

	payload = decodeMiniMaxMediaBody(t, body)
	assert.Equal(t, "mp3", payload["output_format"])
}

func TestMiniMaxMediaRequestURL(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		mode int
		want string
	}{
		{name: "voice clone", mode: relayconstant.RelayModeAudioVoiceClone, want: "https://api.minimax.chat/v1/voice_clone"},
		{name: "voice design", mode: relayconstant.RelayModeAudioVoiceDesign, want: "https://api.minimax.chat/v1/voice_design"},
		{name: "music", mode: relayconstant.RelayModeAudioMusic, want: "https://api.minimax.chat/v1/music_generation"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				RelayMode: tt.mode,
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl: "https://api.minimax.chat",
				},
			}

			got, err := GetRequestURL(info)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMiniMaxMediaMusicHexAudio(t *testing.T) {
	t.Parallel()

	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeAudioMusic,
		OriginModelName: "music-3.0",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "music-3.0",
		},
	}

	for _, tt := range []struct {
		name         string
		outputFormat string
		wantType     string
	}{
		{name: "default output format is mp3", wantType: "audio/mpeg"},
		{name: "explicit output format drives the content type", outputFormat: "wav", wantType: "audio/wav"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)

			metadata := json.RawMessage(`{}`)
			if tt.outputFormat != "" {
				metadata = json.RawMessage(`{"output_format":"` + tt.outputFormat + `"}`)
			}
			_, err := adaptor.ConvertAudioRequest(c, info, dto.AudioRequest{
				Model:    "music-3.0",
				Input:    "a calm piano piece",
				Metadata: metadata,
			})
			require.NoError(t, err)

			audio := []byte("music-audio-bytes")
			resp := newMiniMaxMediaResponse(`{"data":{"status":2,"audio":"` + hex.EncodeToString(audio) + `"},"extra_info":{"music_duration":12},"base_resp":{"status_code":0,"status_msg":"success"}}`)

			usage, apiErr := handleMusicResponse(c, resp, info)
			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			assert.Equal(t, tt.wantType, recorder.Header().Get("Content-Type"))
			assert.Equal(t, audio, recorder.Body.Bytes())
		})
	}
}

func TestMiniMaxMediaBaseRespError(t *testing.T) {
	t.Parallel()

	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeAudioMusic,
		OriginModelName: "music-3.0",
	}

	cloneRecorder := httptest.NewRecorder()
	cloneContext, _ := gin.CreateTestContext(cloneRecorder)
	_, apiErr := handleVoiceCloneResponse(cloneContext, newMiniMaxMediaResponse(`{"base_resp":{"status_code":1004,"status_msg":"invalid file id"}}`), info)
	require.NotNil(t, apiErr)
	assert.Contains(t, apiErr.Error(), "invalid file id")

	designRecorder := httptest.NewRecorder()
	designContext, _ := gin.CreateTestContext(designRecorder)
	_, apiErr = handleVoiceDesignResponse(designContext, newMiniMaxMediaResponse(`{"voice_id":"x","base_resp":{"status_code":1002,"status_msg":"prompt too long"}}`), info)
	require.NotNil(t, apiErr)
	assert.Contains(t, apiErr.Error(), "prompt too long")

	musicRecorder := httptest.NewRecorder()
	musicContext, _ := gin.CreateTestContext(musicRecorder)
	_, apiErr = handleMusicResponse(musicContext, newMiniMaxMediaResponse(`{"data":{"status":2,"audio":"616263"},"base_resp":{"status_code":1008,"status_msg":"insufficient balance"}}`), info)
	require.NotNil(t, apiErr)
	assert.Contains(t, apiErr.Error(), "insufficient balance")
}

func TestMiniMaxMediaUsageNotNil(t *testing.T) {
	t.Parallel()

	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeAudioMusic,
		OriginModelName: "music-3.0",
	}

	cloneRecorder := httptest.NewRecorder()
	cloneContext, _ := gin.CreateTestContext(cloneRecorder)
	cloneUsage, apiErr := handleVoiceCloneResponse(cloneContext, newMiniMaxMediaResponse(`{"extra_info":{"usage_characters":42},"base_resp":{"status_code":0,"status_msg":"success"}}`), info)
	require.Nil(t, apiErr)
	require.NotNil(t, cloneUsage)
	assert.Equal(t, 42, cloneUsage.TotalTokens)

	designRecorder := httptest.NewRecorder()
	designContext, _ := gin.CreateTestContext(designRecorder)
	designUsage, apiErr := handleVoiceDesignResponse(designContext, newMiniMaxMediaResponse(`{"voice_id":"x","trial_audio":"616263","base_resp":{"status_code":0,"status_msg":"success"}}`), info)
	require.Nil(t, apiErr)
	require.NotNil(t, designUsage)

	musicRecorder := httptest.NewRecorder()
	musicContext, _ := gin.CreateTestContext(musicRecorder)
	musicUsage, apiErr := handleMusicResponse(musicContext, newMiniMaxMediaResponse(`{"data":{"status":2,"audio":"616263"},"base_resp":{"status_code":0,"status_msg":"success"}}`), info)
	require.Nil(t, apiErr)
	require.NotNil(t, musicUsage)
}

func decodeMiniMaxMediaBody(t *testing.T, body io.Reader) map[string]any {
	t.Helper()

	raw, err := io.ReadAll(body)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	return payload
}

func newMiniMaxMediaResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
