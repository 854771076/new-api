package xunfei

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 讯飞转写的真实上游端到端验证。
//
// 离线假上游掩盖不了的协议差异集中在三处：参数编码（签名与查询串必须同一套 URL 编码）、
// 真实返回结构（双重嵌套 JSON 字符串 + 过度转义）、时间戳语义（毫秒与秒的换算）。
// 这些只能用真实凭据与真实音频验证，因此本文件按仓库既有的「凭据缺失则跳过」模式组织：
// 凭据与音频路径只从环境变量读取，未配置时跳过而不是失败，凭据一个字面量都不落进仓库。

const (
	// liveEnvAppID 讯飞应用 ID 的环境变量名。
	liveEnvAppID = "XFYUN_ASR_APP_ID"
	// liveEnvAPISecret 讯飞访问密钥密钥（参与签名计算）的环境变量名。
	liveEnvAPISecret = "XFYUN_ASR_API_SECRET"
	// liveEnvAccessKeyID 讯飞访问密钥 ID（accessKeyId）的环境变量名。
	liveEnvAccessKeyID = "XFYUN_ASR_ACCESS_KEY_ID"
	// liveEnvAudioPath 本地音频文件路径的环境变量名。
	liveEnvAudioPath = "XFYUN_ASR_AUDIO_PATH"
	// liveEnvLanguage 上传语种的环境变量名，可选，缺省为 autodialect。
	liveEnvLanguage = "XFYUN_ASR_LANGUAGE"
)

// liveCredentialErrorCodes 是讯飞侧表示「签名或凭据有问题」的上游业务错误码。
//
// 出现这些码说明请求根本没走到转写环节，必须与「转写失败」区分开报给使用者。
var liveCredentialErrorCodes = []string{"100009", "100008", "000002"}

// TestXfyunASRLive 用真实讯飞凭据与真实音频跑一次端到端转写。
//
// 环境变量：
//
//   - XFYUN_ASR_APP_ID：讯飞应用 ID（三段密钥的第一段）
//   - XFYUN_ASR_API_SECRET：讯飞访问密钥密钥（三段密钥的第二段，参与签名计算）
//   - XFYUN_ASR_ACCESS_KEY_ID：讯飞访问密钥 ID（三段密钥的第三段，即 accessKeyId）
//   - XFYUN_ASR_AUDIO_PATH：本地音频文件路径
//   - XFYUN_ASR_LANGUAGE：可选，上传语种，缺省 autodialect
//
// 三段凭据的拼装顺序与渠道密钥的既有切分一致：appId|apiSecret|apiKey。
func TestXfyunASRLive(t *testing.T) {
	appID := strings.TrimSpace(os.Getenv(liveEnvAppID))
	apiSecret := strings.TrimSpace(os.Getenv(liveEnvAPISecret))
	accessKeyID := strings.TrimSpace(os.Getenv(liveEnvAccessKeyID))
	audioPath := strings.TrimSpace(os.Getenv(liveEnvAudioPath))
	if appID == "" || apiSecret == "" || accessKeyID == "" || audioPath == "" {
		t.Skipf("未配置讯飞转写真实上游凭据或音频路径（%s / %s / %s / %s），跳过真实上游验证",
			liveEnvAppID, liveEnvAPISecret, liveEnvAccessKeyID, liveEnvAudioPath)
	}

	data, err := os.ReadFile(audioPath)
	require.NoError(t, err, "读取音频文件失败：%s", audioPath)
	require.NotEmpty(t, data, "音频文件为空：%s", audioPath)

	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(audioPath)), ".")
	if !slices.Contains(asrSupportedAudioFormats, format) {
		t.Skipf("音频格式 %q 不在讯飞转写支持范围内（%s），请另备一份受支持的音频后重跑",
			format, strings.Join(asrSupportedAudioFormats, "、"))
	}

	durationMs := liveAudioDurationMs(t, audioPath, format)
	liveRequireWAVProperties(t, format, data)

	language := strings.TrimSpace(os.Getenv(liveEnvLanguage))
	if language == "" {
		language = ASRDefaultLanguage
	}

	creds := ASRCredentials{AppID: appID, APISecret: apiSecret, APIKey: accessKeyID}
	audio := ASRAudio{
		Data:       data,
		FileName:   filepath.Base(audioPath),
		Format:     format,
		DurationMs: durationMs,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	result, err := ASRTranscribe(ctx, creds, audio, ASROptions{
		Language:   language,
		ResultType: ASRResultTypeTransfer,
		MaxWait:    9 * time.Minute,
	})
	if err != nil {
		var upstream *ASRUpstreamError
		if errors.As(err, &upstream) && slices.Contains(liveCredentialErrorCodes, upstream.UpstreamCode) {
			t.Fatalf("讯飞上游拒绝了签名或凭据（上游错误码 %s）：%v", upstream.UpstreamCode, err)
		}
		t.Fatalf("真实上游转写失败：%v", err)
	}

	// 签名被上游接受、订单走到完成态，真实返回结构才能被正确解析出来。
	require.Equal(t, ASROrderStatusCompleted, result.Order.Status, "上游订单应处于完成态")
	require.NotEmpty(t, result.Segments, "真实返回结构应解析出分段")
	assert.NotEmpty(t, result.Text, "真实返回结构应解析出转写文本")
	assert.Positive(t, result.OriginalDurationMs, "上游应回报原始音频时长（毫秒）")

	// 时间戳语义：分段起止是毫秒，单调递增且不超出上游回报的原始时长。
	for i, segment := range result.Segments {
		assert.GreaterOrEqual(t, segment.StartMs, int64(0), "分段起始时间不应为负")
		assert.LessOrEqual(t, segment.StartMs, segment.EndMs, "分段起始时间不应晚于结束时间")
		assert.LessOrEqual(t, segment.EndMs, result.OriginalDurationMs, "分段结束时间不应超出原始音频时长")
		if i > 0 {
			previous := result.Segments[i-1]
			assert.GreaterOrEqual(t, segment.StartMs, previous.StartMs, "分段起始时间应单调不减")
			assert.GreaterOrEqual(t, segment.EndMs, previous.EndMs, "分段结束时间应单调不减")
		}
	}

	// 用量 token 与转写文本一并核对，确认计费口径拿到的是上游的真实时长。
	usage := BuildASRUsage(result, audio)
	require.NotNil(t, usage)
	assert.Positive(t, usage.PromptTokensDetails.AudioTokens, "音频 token 应落在输入侧")
	assert.Equal(t, usage.PromptTokens, usage.TotalTokens, "总量必须同步，否则结算层会判为超时")

	// verbose_json：分段起止是秒，duration 与上游的毫秒值一致。
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)
	require.NoError(t, WriteASRResponse(c, result, asrResponseFormatVerboseJSON))

	var verbose dto.TranscriptionVerboseJSONResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &verbose))
	assert.InDelta(t, float64(result.OriginalDurationMs)/1000, verbose.Duration, 1e-9, "duration 应由上游毫秒值换算成秒")
	require.Len(t, verbose.Segments, len(result.Segments))
	for i, segment := range verbose.Segments {
		assert.InDelta(t, float64(result.Segments[i].StartMs)/1000, segment.Start, 1e-9, "分段起始时间应为秒")
		assert.InDelta(t, float64(result.Segments[i].EndMs)/1000, segment.End, 1e-9, "分段结束时间应为秒")
		assert.Equal(t, result.Segments[i].Text, segment.Text)
	}
}

// liveAudioDurationMs 解析本地音频时长并换算成毫秒。
//
// 上传时长的校验是开启的（不使用 durationCheckDisable），时长必须与真实音频一致，
// 因此解析不出时长时只能跳过，不能拿 0 时长去试上游。
func liveAudioDurationMs(t *testing.T, path string, format string) int64 {
	t.Helper()

	file, err := os.Open(path)
	require.NoError(t, err, "打开音频文件失败：%s", path)
	defer file.Close()

	seconds, err := common.GetAudioDuration(context.Background(), file, "."+format)
	if err != nil {
		t.Skipf("本地无法解析 %q 的音频时长（%v）：请改用 wav 或 mp3 音频后重跑", format, err)
	}
	return int64(math.Round(seconds * 1000))
}

// liveRequireWAVProperties 校验 WAV 音频是否满足讯飞的音频属性要求。
//
// 讯飞要求 16kHz 或 8kHz、16bit、单声道。不满足时上游要么拒绝、要么转写出错内容，
// 因此这里给出明确的跳过说明，而不是改写使用者的音频文件。
func liveRequireWAVProperties(t *testing.T, format string, data []byte) {
	t.Helper()

	if format != "wav" {
		return
	}
	sampleRate, channels, bitsPerSample, ok := liveWAVFormat(data)
	if !ok {
		t.Skipf("无法解析 WAV 头部的音频属性，无法确认其满足讯飞要求（16kHz 或 8kHz、16bit、单声道）")
	}
	if channels != 1 || bitsPerSample != 16 || (sampleRate != 8000 && sampleRate != 16000) {
		t.Skipf("WAV 音频属性不满足讯飞要求（需 16kHz 或 8kHz、16bit、单声道，当前 %dHz、%dbit、%d 声道），请另备一份符合要求的音频",
			sampleRate, bitsPerSample, channels)
	}
}

// liveWAVFormat 解析 WAV 头部的 fmt 块，取出采样率、声道数与位长。
//
// 按 RIFF 的块结构逐块前进；块结构走不通（个别文件缺少块填充字节）时回退到按标记搜索
// fmt 块，避免解析错位。解析不出时返回 ok=false，调用方据此放弃属性判定。
func liveWAVFormat(data []byte) (sampleRate uint32, channels uint16, bitsPerSample uint16, ok bool) {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return 0, 0, 0, false
	}
	for offset := 12; offset+8 <= len(data); {
		if string(data[offset:offset+4]) == "fmt " && offset+24 <= len(data) {
			sampleRate, channels, bitsPerSample = liveWAVFormatFields(data[offset+8:])
			return sampleRate, channels, bitsPerSample, true
		}
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		if chunkSize <= 0 {
			break
		}
		next := offset + 8 + chunkSize
		if chunkSize%2 == 1 {
			next++ // RIFF 块的填充字节
		}
		if next <= offset || next > len(data) {
			break
		}
		offset = next
	}

	// 回退：按标记搜索 fmt 块。
	index := bytes.Index(data[12:], []byte("fmt "))
	if index < 0 {
		return 0, 0, 0, false
	}
	body := 12 + index + 4
	if body+16 > len(data) {
		return 0, 0, 0, false
	}
	sampleRate, channels, bitsPerSample = liveWAVFormatFields(data[body:])
	// 回退解析可能命中数据块里的巧合字节，取值明显不合法时按解析失败处理。
	if sampleRate == 0 || channels == 0 || (bitsPerSample != 8 && bitsPerSample != 16 && bitsPerSample != 24 && bitsPerSample != 32) {
		return 0, 0, 0, false
	}
	return sampleRate, channels, bitsPerSample, true
}

// liveWAVFormatFields 从 fmt 块的载荷里取出采样率、声道数与位长。
func liveWAVFormatFields(fmt []byte) (sampleRate uint32, channels uint16, bitsPerSample uint16) {
	return binary.LittleEndian.Uint32(fmt[4:8]),
		binary.LittleEndian.Uint16(fmt[2:4]),
		binary.LittleEndian.Uint16(fmt[14:16])
}
