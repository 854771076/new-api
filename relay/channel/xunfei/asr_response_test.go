package xunfei

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 讯飞转写响应构造与音频计费用的测试。
//
// 响应构造覆盖三种 response_format 的响应体与 Content-Type；计费用量覆盖时长换算、
// 输入侧落账字段与总量同步。端到端接缝用本地 HTTP 服务器假扮讯飞上游。

const (
	asrRespAppID      = "37f3f2b5"
	asrRespSecret     = "test-access-key-secret"
	asrRespAccessKey  = "a296be0df88f701ec2e4882be7727568"
	asrRespOrderID    = "DKHJQ2020042816200428163809B030D800083"
	asrRespEstimateMs = 5100
)

// asrRespFakeUpstream 假扮讯飞 IFASR 上游的本地测试服务器。
type asrRespFakeUpstream struct {
	server *httptest.Server
	// originalDurationMs 是上游回报的原始音频时长。
	originalDurationMs int64
	// orderStatus 是上游回报的订单状态。
	orderStatus int
}

func newASRRespFakeUpstream(t *testing.T) *asrRespFakeUpstream {
	t.Helper()

	fake := &asrRespFakeUpstream{originalDurationMs: 60000, orderStatus: ASROrderStatusCompleted}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.handle))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *asrRespFakeUpstream) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case ASRUploadPath:
		writeASRRespJSON(w, map[string]any{
			"code":     "000000",
			"descInfo": "success",
			"content": map[string]any{
				"orderId":          asrRespOrderID,
				"taskEstimateTime": asrRespEstimateMs,
			},
		})
	case ASRResultPath:
		writeASRRespJSON(w, map[string]any{
			"code":     "000000",
			"descInfo": "success",
			"content": map[string]any{
				"orderInfo": map[string]any{
					"orderId":          asrRespOrderID,
					"status":           f.orderStatus,
					"failType":         0,
					"originalDuration": f.originalDurationMs,
				},
				"orderResult": asrRespOrderResult(),
			},
		})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// asrRespOrderResult 构造两段不同说话人的转写结果。
func asrRespOrderResult() string {
	first := `{"st":{"bg":"0","ed":"1000","rl":"0","rt":[{"ws":[{"cw":[{"w":"你好"}]}]}]}}`
	second := `{"st":{"bg":"1000","ed":"2000","rl":"1","rt":[{"ws":[{"cw":[{"w":"世界"}]}]}]}}`
	lattice, err := common.Marshal(map[string]any{
		"lattice": []any{
			map[string]any{"json_1best": first},
			map[string]any{"json_1best": second},
		},
	})
	if err != nil {
		return ""
	}
	return string(lattice)
}

func writeASRRespJSON(w http.ResponseWriter, payload any) {
	data, err := common.Marshal(payload)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

// asrRespResult 构造一段没有说话人信息的转写结果。
func asrRespResult() *ASRResult {
	return &ASRResult{
		Text:               "你好世界",
		OriginalDurationMs: 12345,
		Segments: []ASRSegment{
			{Index: 0, StartMs: 0, EndMs: 1000, Text: "你好"},
			{Index: 1, StartMs: 1000, EndMs: 2345, Text: "世界"},
		},
	}
}

// TestWriteASRResponseJSON 默认格式只返回转写文本。
func TestWriteASRResponseJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	require.NoError(t, WriteASRResponse(c, asrRespResult(), "json"))

	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	var body map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
	assert.Equal(t, "你好世界", body["text"])
	assert.Len(t, body, 1, "默认格式不应附加其他字段")
}

// TestWriteASRResponseVerboseJSON verbose_json 附时长（秒）与分段列表。
func TestWriteASRResponseVerboseJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	require.NoError(t, WriteASRResponse(c, asrRespResult(), "verbose_json"))

	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	var body dto.TranscriptionVerboseJSONResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))

	assert.Equal(t, "你好世界", body.Text)
	assert.InDelta(t, 12.345, body.Duration, 0.0001, "时长应由上游毫秒值除以 1000")
	assert.Equal(t, "", body.Language, "transfer 结果类型下语种字段必须留空")
	require.Len(t, body.Segments, 2)
	assert.Equal(t, 0, body.Segments[0].Id)
	assert.InDelta(t, 0, body.Segments[0].Start, 0.0001)
	assert.InDelta(t, 1, body.Segments[0].End, 0.0001)
	assert.Equal(t, "你好", body.Segments[0].Text)
	assert.Equal(t, 1, body.Segments[1].Id)
	assert.InDelta(t, 1, body.Segments[1].Start, 0.0001)
	assert.InDelta(t, 2.345, body.Segments[1].End, 0.0001)
	assert.Equal(t, "世界", body.Segments[1].Text)
}

// TestWriteASRResponseText 纯文本格式走独立的写出分支。
func TestWriteASRResponseText(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	require.NoError(t, WriteASRResponse(c, asrRespResult(), "text"))

	assert.Equal(t, "text/plain", recorder.Header().Get("Content-Type"))
	assert.Equal(t, "你好世界", recorder.Body.String())
}

// TestWriteASRResponseUnknownFormatFallsBackToJSON 未知格式按默认 JSON 处理。
func TestWriteASRResponseUnknownFormatFallsBackToJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	require.NoError(t, WriteASRResponse(c, asrRespResult(), "srt"))

	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	var body map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
	assert.Equal(t, "你好世界", body["text"])
}

// TestWriteASRResponseMergesSpeakerLabels 说话人以标签形式并入文本。
func TestWriteASRResponseMergesSpeakerLabels(t *testing.T) {
	gin.SetMode(gin.TestMode)

	result := &ASRResult{
		Segments: []ASRSegment{
			{Index: 0, Speaker: "0", StartMs: 0, EndMs: 1000, Text: "你好"},
			{Index: 1, Speaker: "1", StartMs: 1000, EndMs: 2000, Text: "世界"},
			{Index: 2, Speaker: "0", StartMs: 2000, EndMs: 3000, Text: "再见"},
		},
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	require.NoError(t, WriteASRResponse(c, result, "text"))

	assert.Equal(t, "[说话人1]你好\n[说话人2]世界\n[说话人1]再见", recorder.Body.String())
}

// TestBuildASRUsage 时长换算与输入侧落账字段。
func TestBuildASRUsage(t *testing.T) {
	tests := []struct {
		name       string
		result     *ASRResult
		audio      ASRAudio
		wantTokens int
	}{
		{
			name:       "以上游原始时长为准",
			result:     &ASRResult{OriginalDurationMs: 60000},
			audio:      ASRAudio{DurationMs: 1000},
			wantTokens: 1000,
		},
		{
			name:       "上游时长为零时回落本地解析时长",
			result:     &ASRResult{},
			audio:      ASRAudio{DurationMs: 60000},
			wantTokens: 1000,
		},
		{
			name:       "负值时长钳到零",
			result:     &ASRResult{OriginalDurationMs: -1000},
			audio:      ASRAudio{DurationMs: 60000},
			wantTokens: 0,
		},
		{
			name:       "秒数向上取整后换算",
			result:     &ASRResult{OriginalDurationMs: 1500},
			audio:      ASRAudio{},
			wantTokens: 33,
		},
		{
			name:       "极大时长被饱和钳制",
			result:     &ASRResult{OriginalDurationMs: 1e18},
			audio:      ASRAudio{},
			wantTokens: common.MaxQuota,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage := BuildASRUsage(tt.result, tt.audio)
			require.NotNil(t, usage)

			assert.Equal(t, tt.wantTokens, usage.PromptTokens, "输入 token 计数应与音频 token 一致")
			assert.Equal(t, tt.wantTokens, usage.TotalTokens, "总量必须同步，否则结算层会当作上游超时")
			assert.Equal(t, tt.wantTokens, usage.PromptTokensDetails.AudioTokens, "音频 token 落在输入侧")
			assert.Equal(t, 0, usage.CompletionTokenDetails.AudioTokens, "输出侧音频 token 为零")
			assert.Equal(t, 0, usage.CompletionTokens)
		})
	}
}

// TestAdaptorASRResponseFormats 端到端：假上游 → 适配器 → 响应体与 Content-Type。
func TestAdaptorASRResponseFormats(t *testing.T) {
	gin.SetMode(gin.TestMode)

	previousInterval := asrAdaptorPollInterval
	asrAdaptorPollInterval = 1
	t.Cleanup(func() { asrAdaptorPollInterval = previousInterval })

	for _, tc := range []struct {
		format      string
		contentType string
	}{
		{format: "json", contentType: "application/json"},
		{format: "verbose_json", contentType: "application/json"},
		{format: "text", contentType: "text/plain"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			fake := newASRRespFakeUpstream(t)

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)

			adaptor := &Adaptor{
				audio: &ASRAudio{
					Data:       []byte("fake-audio-bytes"),
					FileName:   "sample.wav",
					Format:     "wav",
					DurationMs: 1000,
				},
				responseFormat: tc.format,
			}
			info := &relaycommon.RelayInfo{
				RelayMode: relayconstant.RelayModeAudioTranscription,
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl: fake.server.URL,
					ApiKey:         asrRespAppID + "|" + asrRespSecret + "|" + asrRespAccessKey,
				},
			}

			usage, apiErr := adaptor.DoResponse(c, nil, info)
			require.Nil(t, apiErr)
			require.NotNil(t, usage)

			assert.Equal(t, tc.contentType, recorder.Header().Get("Content-Type"))
			switch tc.format {
			case "json":
				var body map[string]any
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
				assert.Equal(t, "[说话人1]你好\n[说话人2]世界", body["text"])
			case "verbose_json":
				var body dto.TranscriptionVerboseJSONResponse
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
				assert.Equal(t, "[说话人1]你好\n[说话人2]世界", body.Text)
				assert.InDelta(t, 60, body.Duration, 0.0001)
				assert.Len(t, body.Segments, 2)
			case "text":
				assert.Equal(t, "[说话人1]你好\n[说话人2]世界", recorder.Body.String())
			}

			// 用量落在音频结算通道：输入侧音频 token 决定结算入口。
			usageObject, ok := usage.(*dto.Usage)
			require.True(t, ok)
			assert.Equal(t, 1000, usageObject.PromptTokensDetails.AudioTokens)
			assert.Equal(t, 1000, usageObject.TotalTokens)
		})
	}
}

// TestAdaptorASRResponseUpstreamFailure 转写失败时适配器返回错误，
// 由既有的失败退款流程退回预扣配额。
func TestAdaptorASRResponseUpstreamFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	previousInterval := asrAdaptorPollInterval
	asrAdaptorPollInterval = 1
	t.Cleanup(func() { asrAdaptorPollInterval = previousInterval })

	fake := newASRRespFakeUpstream(t)
	fake.orderStatus = ASROrderStatusFailed

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)

	adaptor := &Adaptor{
		audio: &ASRAudio{
			Data:       []byte("fake-audio-bytes"),
			FileName:   "sample.wav",
			Format:     "wav",
			DurationMs: 1000,
		},
		responseFormat: "json",
	}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeAudioTranscription,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: fake.server.URL,
			ApiKey:         asrRespAppID + "|" + asrRespSecret + "|" + asrRespAccessKey,
		},
	}

	usage, apiErr := adaptor.DoResponse(c, nil, info)
	require.NotNil(t, apiErr, "转写失败必须返回错误，交给既有流程退款")
	assert.Nil(t, usage)
	assert.Empty(t, recorder.Body.String(), "失败时不得写出响应体")
}

// TestAdaptorASRResponseRecordsQuotaClamp 极大时长触发饱和时，标记交给结算层记录。
func TestAdaptorASRResponseRecordsQuotaClamp(t *testing.T) {
	gin.SetMode(gin.TestMode)

	previousInterval := asrAdaptorPollInterval
	asrAdaptorPollInterval = 1
	t.Cleanup(func() { asrAdaptorPollInterval = previousInterval })

	fake := newASRRespFakeUpstream(t)
	fake.originalDurationMs = 1e18

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)

	adaptor := &Adaptor{
		audio: &ASRAudio{
			Data:       []byte("fake-audio-bytes"),
			FileName:   "sample.wav",
			Format:     "wav",
			DurationMs: 1000,
		},
		responseFormat: "json",
	}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeAudioTranscription,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: fake.server.URL,
			ApiKey:         asrRespAppID + "|" + asrRespSecret + "|" + asrRespAccessKey,
		},
	}

	usage, apiErr := adaptor.DoResponse(c, nil, info)
	require.Nil(t, apiErr)

	usageObject, ok := usage.(*dto.Usage)
	require.True(t, ok)
	assert.Equal(t, common.MaxQuota, usageObject.PromptTokensDetails.AudioTokens, "极大时长应被饱和钳制")

	require.NotNil(t, info.QuotaClamp, "饱和标记必须交给结算层记录")
	assert.Equal(t, common.QuotaClampOverflow, info.QuotaClamp.Kind)
	assert.Equal(t, common.MaxQuota, info.QuotaClamp.Clamped)
}
