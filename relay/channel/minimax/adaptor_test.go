package minimax

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRequestURLForImageGeneration(t *testing.T) {
	t.Parallel()

	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesGenerations,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://api.minimax.chat",
		},
	}

	got, err := GetRequestURL(info)
	if err != nil {
		t.Fatalf("GetRequestURL returned error: %v", err)
	}

	want := "https://api.minimax.chat/v1/image_generation"
	if got != want {
		t.Fatalf("GetRequestURL() = %q, want %q", got, want)
	}
}

func TestConvertImageRequest(t *testing.T) {
	t.Parallel()

	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		OriginModelName: "image-01",
	}
	request := dto.ImageRequest{
		Model:          "image-01",
		Prompt:         "a red fox in snowfall",
		Size:           "1536x1024",
		ResponseFormat: "url",
		N:              uintPtr(2),
	}

	got, err := adaptor.ConvertImageRequest(gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New()), info, request)
	if err != nil {
		t.Fatalf("ConvertImageRequest returned error: %v", err)
	}

	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	if payload["model"] != "image-01" {
		t.Fatalf("model = %#v, want %q", payload["model"], "image-01")
	}
	if payload["prompt"] != request.Prompt {
		t.Fatalf("prompt = %#v, want %q", payload["prompt"], request.Prompt)
	}
	if payload["n"] != float64(2) {
		t.Fatalf("n = %#v, want 2", payload["n"])
	}
	if payload["aspect_ratio"] != "3:2" {
		t.Fatalf("aspect_ratio = %#v, want %q", payload["aspect_ratio"], "3:2")
	}
	if payload["response_format"] != "url" {
		t.Fatalf("response_format = %#v, want %q", payload["response_format"], "url")
	}
}

func TestDoResponseForImageGeneration(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesGenerations,
		StartTime: time.Unix(1700000000, 0),
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       httptest.NewRecorder().Result().Body,
	}
	resp.Body = ioNopCloser(`{"data":{"image_urls":["https://example.com/minimax.png"]}}`)

	adaptor := &Adaptor{}
	usage, err := adaptor.DoResponse(c, resp, info)
	if err != nil {
		t.Fatalf("DoResponse returned error: %v", err)
	}
	if usage == nil {
		t.Fatalf("DoResponse returned nil usage")
	}

	body := recorder.Body.String()
	if !strings.Contains(body, `"url":"https://example.com/minimax.png"`) {
		t.Fatalf("response body = %s, want OpenAI image response with image URL", body)
	}
	if strings.Contains(body, `"image_urls"`) {
		t.Fatalf("response body = %s, should not expose raw MiniMax image_urls payload", body)
	}
}

func TestMiniMaxModelListIncludesOfficialFamilies(t *testing.T) {
	t.Parallel()

	for _, model := range []string{
		"MiniMax-M3",
		"MiniMax-M3.1-Flash",
		"MiniMax-H3-Image",
		"MiniMax-H3-Image-Highspeed",
		"music-3.0",
	} {
		assert.Contains(t, ModelList, model, "official model %q should be registered", model)
	}

	// 停服的免费音乐模型与 hailuo 插件认领的语音模型不进渠道清单
	for _, model := range []string{
		"Music-3.0-free",
		"Music-2.6-free",
		"music-cover-free",
		"speech-2.8-hd",
		"speech-2.8-turbo",
		"speech-2.6-hd",
		"speech-2.6-turbo",
	} {
		assert.NotContains(t, ModelList, model, "model %q should not be registered in the channel model list", model)
	}

	// 既有语音条目不能重复登记
	for _, model := range []string{
		"speech-02-hd",
		"speech-02-turbo",
		"speech-01-hd",
		"speech-01-turbo",
	} {
		count := 0
		for _, registered := range ModelList {
			if registered == model {
				count++
			}
		}
		assert.Equal(t, 1, count, "model %q should be registered exactly once", model)
	}
}

func TestMiniMaxSpeechOutputFormat(t *testing.T) {
	t.Parallel()

	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeAudioSpeech,
		OriginModelName: "speech-02-hd",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "speech-02-hd",
		},
	}

	tests := []struct {
		name           string
		responseFormat string
		wantOutput     string
		wantRejected   bool
	}{
		{name: "missing format defaults to mp3", wantOutput: "mp3"},
		{name: "mp3 accepted", responseFormat: "mp3", wantOutput: "mp3"},
		{name: "hex accepted", responseFormat: "hex", wantOutput: "hex"},
		{name: "opus rejected", responseFormat: "opus", wantRejected: true},
		{name: "aac rejected", responseFormat: "aac", wantRejected: true},
		{name: "flac rejected", responseFormat: "flac", wantRejected: true},
		{name: "wav rejected", responseFormat: "wav", wantRejected: true},
		{name: "pcm rejected", responseFormat: "pcm", wantRejected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c := gin.CreateTestContextOnly(recorder, gin.New())

			body, err := adaptor.ConvertAudioRequest(c, info, dto.AudioRequest{
				Model:          "speech-02-hd",
				Input:          "hello",
				Voice:          "male-qn-qingse",
				ResponseFormat: tt.responseFormat,
			})
			if tt.wantRejected {
				require.Error(t, err)
				var apiErr *types.NewAPIError
				require.ErrorAs(t, err, &apiErr)
				assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
				assert.Contains(t, apiErr.Error(), "mp3")
				assert.Contains(t, apiErr.Error(), "hex")
				return
			}
			require.NoError(t, err)

			payload := decodeMiniMaxTTSRequest(t, body)
			assert.Equal(t, tt.wantOutput, payload["output_format"])
			audioSetting, ok := payload["audio_setting"].(map[string]any)
			require.True(t, ok, "audio_setting should be present in the upstream request")
			assert.Equal(t, "mp3", audioSetting["format"])
			assert.Equal(t, tt.wantOutput, c.GetString("response_format"))

			// 响应解码按本次请求实际发给上游的 output_format 走
			wantAudio := "mp3-audio"
			upstreamAudio := wantAudio
			if tt.wantOutput == "hex" {
				upstreamAudio = hex.EncodeToString([]byte(wantAudio))
			}
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"data":{"audio":"` + upstreamAudio + `","status":2}}`)),
			}
			_, apiErr := handleTTSResponse(c, resp, info)
			require.Nil(t, apiErr)
			assert.Equal(t, "audio/mpeg", recorder.Header().Get("Content-Type"))
			assert.Equal(t, wantAudio, recorder.Body.String())
		})
	}
}

func TestMiniMaxSpeechVoiceIdRequired(t *testing.T) {
	t.Parallel()

	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeAudioSpeech,
		OriginModelName: "speech-02-hd",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "speech-02-hd",
		},
	}

	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
	_, err := adaptor.ConvertAudioRequest(c, info, dto.AudioRequest{
		Model: "speech-02-hd",
		Input: "hello",
		Voice: "",
	})
	require.Error(t, err)
	var apiErr *types.NewAPIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Contains(t, apiErr.Error(), "voice_id")

	// 给了音色 id 时不应被拒
	c2 := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
	body, err := adaptor.ConvertAudioRequest(c2, info, dto.AudioRequest{
		Model: "speech-02-hd",
		Input: "hello",
		Voice: "male-qn-qingse",
	})
	require.NoError(t, err)
	payload := decodeMiniMaxTTSRequest(t, body)
	voiceSetting, ok := payload["voice_setting"].(map[string]any)
	require.True(t, ok, "voice_setting should be present in the upstream request")
	assert.Equal(t, "male-qn-qingse", voiceSetting["voice_id"])
}

func decodeMiniMaxTTSRequest(t *testing.T, body io.Reader) map[string]any {
	t.Helper()

	raw, err := io.ReadAll(body)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	return payload
}

type nopReadCloser struct {
	*strings.Reader
}

func (n nopReadCloser) Close() error {
	return nil
}

func ioNopCloser(body string) nopReadCloser {
	return nopReadCloser{Reader: strings.NewReader(body)}
}

func uintPtr(v uint) *uint {
	return &v
}
