package service

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildTestWav 构造一个单声道 16bit WAV，采样率 sampleRate、采样点个数 sampleCount。
func buildTestWav(sampleRate int, sampleCount int) []byte {
	dataLen := sampleCount * 2
	buf := &bytes.Buffer{}
	buf.WriteString("RIFF")
	_ = binary.Write(buf, binary.LittleEndian, uint32(36+dataLen))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	_ = binary.Write(buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1)) // PCM
	_ = binary.Write(buf, binary.LittleEndian, uint16(1)) // 单声道
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate*2))
	_ = binary.Write(buf, binary.LittleEndian, uint16(2)) // blockAlign
	_ = binary.Write(buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	_ = binary.Write(buf, binary.LittleEndian, uint32(dataLen))
	buf.Write(make([]byte, dataLen))
	return buf.Bytes()
}

// newJSONContext 构造一个 JSON 请求体的 gin 上下文。
func newJSONContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", strings.NewReader("{}"))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

// newMultipartContext 构造一个携带 file 文件字段的 multipart 请求上下文。
func newMultipartContext(t *testing.T, fileName string, content []byte) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", fileName)
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	return c
}

// initHTTPClientOnce 保证测试只初始化一次全局 HTTP 客户端（生产环境由启动流程调用）。
var initHTTPClientOnce sync.Once

// prepareLocalDownload 初始化全局 HTTP 客户端并关闭 SSRF 防护，让测试可以访问 httptest 的本地回环地址。
func prepareLocalDownload(t *testing.T) {
	t.Helper()
	initHTTPClientOnce.Do(func() { InitHttpClient() })
	fetchSetting := system_setting.GetFetchSetting()
	previous := fetchSetting.EnableSSRFProtection
	fetchSetting.EnableSSRFProtection = false
	t.Cleanup(func() { fetchSetting.EnableSSRFProtection = previous })
}

func TestResolveAudioSourceMultipartFile(t *testing.T) {
	wav := buildTestWav(8000, 8000)
	c := newMultipartContext(t, "speech.wav", wav)

	source, err := ResolveAudioSource(c, &dto.AudioRequest{})
	require.NoError(t, err)
	require.NotNil(t, source)
	assert.Equal(t, wav, source.Data)
	assert.Equal(t, "wav", source.Format)
	assert.Equal(t, "speech.wav", source.FileName)
	// 8000 采样点 / 8000 采样率 = 1 秒 = 1000 毫秒
	assert.Equal(t, int64(1000), source.DurationMs)
}

func TestResolveAudioSourceMultipartTakesPriority(t *testing.T) {
	wav := buildTestWav(8000, 8000)
	c := newMultipartContext(t, "speech.wav", wav)

	source, err := ResolveAudioSource(c, &dto.AudioRequest{
		AudioBase64: "data:audio/mp3;base64," + base64.StdEncoding.EncodeToString([]byte("not-a-real-mp3")),
		AudioURL:    "http://127.0.0.1:1/should-not-be-requested.wav",
	})
	require.NoError(t, err)
	require.NotNil(t, source)
	assert.Equal(t, wav, source.Data)
	assert.Equal(t, "wav", source.Format)
	assert.Equal(t, "speech.wav", source.FileName)
}

func TestResolveAudioSourceMultipartWithoutExtension(t *testing.T) {
	c := newMultipartContext(t, "speech", buildTestWav(8000, 8000))

	_, err := ResolveAudioSource(c, &dto.AudioRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "扩展名")
}

func TestResolveAudioSourceMultipartWithoutFileFallsBackToBase64(t *testing.T) {
	wav := buildTestWav(8000, 8000)
	gin.SetMode(gin.TestMode)
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	require.NoError(t, writer.WriteField("model", "xunfei-asr"))
	require.NoError(t, writer.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())

	source, err := ResolveAudioSource(c, &dto.AudioRequest{
		AudioBase64: "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(wav),
	})
	require.NoError(t, err)
	require.NotNil(t, source)
	assert.Equal(t, wav, source.Data)
	assert.Equal(t, "wav", source.Format)
}

func TestResolveAudioSourceBase64DataURI(t *testing.T) {
	wav := buildTestWav(8000, 16000)
	c := newJSONContext(t)

	source, err := ResolveAudioSource(c, &dto.AudioRequest{
		AudioBase64: "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(wav),
	})
	require.NoError(t, err)
	require.NotNil(t, source)
	assert.Equal(t, wav, source.Data)
	assert.Equal(t, "wav", source.Format)
	assert.Equal(t, "audio.wav", source.FileName)
	// 16000 采样点 / 8000 采样率 = 2 秒 = 2000 毫秒
	assert.Equal(t, int64(2000), source.DurationMs)
}

func TestResolveAudioSourceBase64DataURIMimeMapping(t *testing.T) {
	wav := buildTestWav(8000, 8000)
	c := newJSONContext(t)

	source, err := ResolveAudioSource(c, &dto.AudioRequest{
		AudioBase64: "data:audio/x-wav;base64," + base64.StdEncoding.EncodeToString(wav),
	})
	require.NoError(t, err)
	require.NotNil(t, source)
	assert.Equal(t, "wav", source.Format)
	assert.Equal(t, "audio.wav", source.FileName)
}

func TestResolveAudioSourceBase64BareRequiresFormat(t *testing.T) {
	wav := buildTestWav(8000, 8000)
	c := newJSONContext(t)

	_, err := ResolveAudioSource(c, &dto.AudioRequest{
		AudioBase64: base64.StdEncoding.EncodeToString(wav),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "音频格式")
}

func TestResolveAudioSourceBase64BareWithFormat(t *testing.T) {
	wav := buildTestWav(8000, 8000)
	c := newJSONContext(t)

	source, err := ResolveAudioSource(c, &dto.AudioRequest{
		AudioBase64: base64.StdEncoding.EncodeToString(wav),
		AudioFormat: "wav",
	})
	require.NoError(t, err)
	require.NotNil(t, source)
	assert.Equal(t, wav, source.Data)
	assert.Equal(t, "wav", source.Format)
	assert.Equal(t, "audio.wav", source.FileName)
	assert.Equal(t, int64(1000), source.DurationMs)
}

func TestResolveAudioSourceBase64URLSafeWithoutPadding(t *testing.T) {
	wav := buildTestWav(8000, 8000)
	// URL-safe 变体且去掉 padding
	urlSafe := base64.RawURLEncoding.EncodeToString(wav)
	c := newJSONContext(t)

	source, err := ResolveAudioSource(c, &dto.AudioRequest{
		AudioBase64: urlSafe,
		AudioFormat: "wav",
	})
	require.NoError(t, err)
	require.NotNil(t, source)
	assert.Equal(t, wav, source.Data)
	assert.Equal(t, "wav", source.Format)
}

func TestResolveAudioSourceBase64InvalidPayload(t *testing.T) {
	c := newJSONContext(t)

	_, err := ResolveAudioSource(c, &dto.AudioRequest{
		AudioBase64: "data:audio/wav;base64,%%%not-base64%%%",
		AudioFormat: "wav",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Base64")
}

func TestResolveAudioSourceBase64UnsupportedFormat(t *testing.T) {
	c := newJSONContext(t)

	_, err := ResolveAudioSource(c, &dto.AudioRequest{
		AudioBase64: "data:audio/webm;base64," + base64.StdEncoding.EncodeToString([]byte("webm-not-supported")),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "不受支持")
	assert.Contains(t, err.Error(), "webm")
}

func TestResolveAudioSourceURLFormatFromPath(t *testing.T) {
	prepareLocalDownload(t)
	wav := buildTestWav(8000, 8000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(wav)
	}))
	defer server.Close()

	c := newJSONContext(t)
	source, err := ResolveAudioSource(c, &dto.AudioRequest{AudioURL: server.URL + "/speech.wav"})
	require.NoError(t, err)
	require.NotNil(t, source)
	assert.Equal(t, wav, source.Data)
	assert.Equal(t, "wav", source.Format)
	assert.Equal(t, "audio.wav", source.FileName)
	assert.Equal(t, int64(1000), source.DurationMs)
}

func TestResolveAudioSourceURLFormatFromContentType(t *testing.T) {
	prepareLocalDownload(t)
	wav := buildTestWav(8000, 8000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/x-wav")
		_, _ = w.Write(wav)
	}))
	defer server.Close()

	c := newJSONContext(t)
	source, err := ResolveAudioSource(c, &dto.AudioRequest{AudioURL: server.URL + "/stream"})
	require.NoError(t, err)
	require.NotNil(t, source)
	assert.Equal(t, "wav", source.Format)
	assert.Equal(t, int64(1000), source.DurationMs)
}

func TestResolveAudioSourceURLWithoutFormat(t *testing.T) {
	prepareLocalDownload(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(buildTestWav(8000, 8000))
	}))
	defer server.Close()

	c := newJSONContext(t)
	_, err := ResolveAudioSource(c, &dto.AudioRequest{AudioURL: server.URL + "/stream"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "格式")
}

func TestResolveAudioSourceURLSizeLimitExceeded(t *testing.T) {
	prepareLocalDownload(t)
	previous := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 1
	t.Cleanup(func() { constant.MaxFileDownloadMB = previous })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(make([]byte, 2*1024*1024))
	}))
	defer server.Close()

	c := newJSONContext(t)
	_, err := ResolveAudioSource(c, &dto.AudioRequest{AudioURL: server.URL + "/big.wav"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "上限")
}

func TestResolveAudioSourceURLNonHTTPScheme(t *testing.T) {
	c := newJSONContext(t)

	_, err := ResolveAudioSource(c, &dto.AudioRequest{AudioURL: "file:///etc/passwd"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http")
}

func TestResolveAudioSourceMissingAudio(t *testing.T) {
	c := newJSONContext(t)

	_, err := ResolveAudioSource(c, &dto.AudioRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "未提供音频")
}

func TestResolveAudioSourceResolvesOnlyOnce(t *testing.T) {
	prepareLocalDownload(t)
	wav := buildTestWav(8000, 8000)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wav)
	}))
	defer server.Close()

	c := newJSONContext(t)
	req := &dto.AudioRequest{AudioURL: server.URL + "/speech.wav"}

	first, err := ResolveAudioSource(c, req)
	require.NoError(t, err)
	second, err := ResolveAudioSource(c, req)
	require.NoError(t, err)

	assert.Same(t, first, second)
	assert.Equal(t, 1, requests, fmt.Sprintf("URL 来源只应被下载一次, 实际请求 %d 次", requests))
}
