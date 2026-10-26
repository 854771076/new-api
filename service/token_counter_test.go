package service

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 转写预扣费估算的测试。
//
// 覆盖三种音频来源（multipart file / Base64 / URL）各自的预扣估算、
// 非 multipart 请求体不再被拒、估算与上传共用同一份来源解析结果（URL 只下载一次）、
// 时长解析失败的报错、webm 走失败路径，以及与结算侧一致的每分钟 1000 token 口径。
//
// 1 秒音频按「每分钟 1000 token」折算为 ceil(1)/60*1000 ≈ 17 token。

// newAudioRelayInfo 构造一个转写模式的 relay 信息，请求体为给定的音频请求。
func newAudioRelayInfo(request dto.Request) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeAudioTranscription,
		RelayFormat: types.RelayFormatOpenAIAudio,
		Request:     request,
	}
}

func TestCountRequestTokenAudioMultipartSource(t *testing.T) {
	wav := buildTestWav(8000, 8000)
	c := newMultipartContext(t, "speech.wav", wav)

	tokens, err := CountRequestToken(c, &types.TokenCountMeta{}, newAudioRelayInfo(&dto.AudioRequest{}))
	require.NoError(t, err)
	assert.Equal(t, 17, tokens)
}

// TestCountRequestTokenAudioBase64Source 覆盖 JSON 请求体携带 Base64 音频的场景：
// 该请求体不是 multipart，估算路径不得因此被拒。
func TestCountRequestTokenAudioBase64Source(t *testing.T) {
	wav := buildTestWav(8000, 8000)
	c := newJSONContext(t)

	tokens, err := CountRequestToken(c, &types.TokenCountMeta{}, newAudioRelayInfo(&dto.AudioRequest{
		AudioBase64: "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(wav),
	}))
	require.NoError(t, err)
	assert.Equal(t, 17, tokens)
}

// TestCountRequestTokenAudioURLSource 覆盖 JSON 请求体携带 HTTP(S) 音频地址的场景。
func TestCountRequestTokenAudioURLSource(t *testing.T) {
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
	tokens, err := CountRequestToken(c, &types.TokenCountMeta{}, newAudioRelayInfo(&dto.AudioRequest{
		AudioURL: server.URL + "/speech.wav",
	}))
	require.NoError(t, err)
	assert.Equal(t, 17, tokens)
	assert.Equal(t, 1, requests, "估算阶段下载一次")
}

// TestCountRequestTokenAudioSharesResolvedSource 验证估算与上传共用同一份来源解析结果：
// 估算之后渠道侧再次解析拿到的是同一份数据，远程音频不会被重复下载。
func TestCountRequestTokenAudioSharesResolvedSource(t *testing.T) {
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
	request := &dto.AudioRequest{AudioURL: server.URL + "/speech.wav"}
	tokens, err := CountRequestToken(c, &types.TokenCountMeta{}, newAudioRelayInfo(request))
	require.NoError(t, err)
	assert.Equal(t, 17, tokens)

	// 渠道侧上传前再次解析：命中请求级缓存，不再下载。
	source, err := ResolveAudioSource(c, request)
	require.NoError(t, err)
	require.NotNil(t, source)
	assert.Equal(t, int64(1000), source.DurationMs)
	assert.Equal(t, 1, requests, fmt.Sprintf("URL 来源只应被下载一次, 实际请求 %d 次", requests))
}

// TestCountRequestTokenAudioDurationParseFailure 验证音频时长无法解析时给出明确错误，
// 而不是静默按 0 token 预扣。
func TestCountRequestTokenAudioDurationParseFailure(t *testing.T) {
	c := newJSONContext(t)

	tokens, err := CountRequestToken(c, &types.TokenCountMeta{}, newAudioRelayInfo(&dto.AudioRequest{
		AudioBase64: "data:audio/wav;base64," + base64.StdEncoding.EncodeToString([]byte("not-a-wav-file")),
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "时长")
	assert.Zero(t, tokens)
}

// TestCountRequestTokenAudioUnsupportedFormat 验证 webm 不在受支持的格式列表内。
func TestCountRequestTokenAudioUnsupportedFormat(t *testing.T) {
	c := newJSONContext(t)

	_, err := CountRequestToken(c, &types.TokenCountMeta{}, newAudioRelayInfo(&dto.AudioRequest{
		AudioBase64: "data:audio/webm;base64," + base64.StdEncoding.EncodeToString([]byte("webm-not-supported")),
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "webm")
	assert.Contains(t, err.Error(), "不受支持")
}

// TestAudioDurationMsToTokens 验证时长到 token 的换算与结算侧口径一致：
// 每分钟 1000 token，秒数向上取整；负时长钳到 0；异常时长走配额饱和换算。
func TestAudioDurationMsToTokens(t *testing.T) {
	cases := []struct {
		name       string
		durationMs int64
		want       int
		wantClamp  bool
	}{
		{name: "一分钟", durationMs: 60000, want: 1000},
		{name: "一秒半", durationMs: 1500, want: 33},
		{name: "一秒", durationMs: 1000, want: 17},
		{name: "零时长", durationMs: 0, want: 0},
		{name: "负时长钳到零", durationMs: -1000, want: 0},
		{name: "超大时长饱和", durationMs: 1e18, want: common.MaxQuota, wantClamp: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tokens, clamp := AudioDurationMsToTokens(tc.durationMs)
			assert.Equal(t, tc.want, tokens)
			if tc.wantClamp {
				require.NotNil(t, clamp)
				assert.Equal(t, common.MaxQuota, clamp.Clamped)
			} else {
				assert.Nil(t, clamp)
			}
		})
	}
}
