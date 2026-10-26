package xunfei

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 讯飞 IFASR 上传与轮询编排测试。
//
// 测试用本地 HTTP 服务器假扮讯飞上游：上传接口返回订单号，查询接口按预设顺序返回订单状态，
// 由此驱动完整的上传→轮询→终态判定流程。

const (
	testASRAppID     = "37f3f2b5"
	testASRSecret    = "test-access-key-secret"
	testASRAccessKey = "a296be0df88f701ec2e4882be7727568"
	testASROrderID   = "DKHJQ2020042816200428163809B030D800083"
)

// asrRecordedRequest 记录一次假上游收到的请求。
type asrRecordedRequest struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   []byte
}

// asrFakeUpstream 假扮讯飞 IFASR 上游的本地测试服务器。
type asrFakeUpstream struct {
	server *httptest.Server

	mu                 sync.Mutex
	uploadRecords      []asrRecordedRequest
	pollRecords        []asrRecordedRequest
	pollStatuses       []int
	pollIndex          int
	pollFailTypes      []int
	taskEstimateTimeMs int64
	orderId            string
	onPollResponse     func(pollIndex int)
}

func newASRFakeUpstream(t *testing.T, pollStatuses []int) *asrFakeUpstream {
	t.Helper()

	fake := &asrFakeUpstream{
		pollStatuses:       pollStatuses,
		taskEstimateTimeMs: 5100,
		orderId:            testASROrderID,
	}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.handle))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *asrFakeUpstream) handle(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	record := asrRecordedRequest{
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  r.URL.Query(),
		Header: r.Header.Clone(),
		Body:   body,
	}

	switch r.URL.Path {
	case ASRUploadPath:
		f.mu.Lock()
		f.uploadRecords = append(f.uploadRecords, record)
		f.mu.Unlock()
		writeASRJSON(w, map[string]any{
			"code":     "000000",
			"descInfo": "success",
			"content": map[string]any{
				"orderId":          f.orderId,
				"taskEstimateTime": f.taskEstimateTimeMs,
			},
		})
	case ASRResultPath:
		f.mu.Lock()
		f.pollRecords = append(f.pollRecords, record)
		index := f.pollIndex
		f.pollIndex++
		status := f.pollStatuses[len(f.pollStatuses)-1]
		if index < len(f.pollStatuses) {
			status = f.pollStatuses[index]
		}
		failType := 0
		if len(f.pollFailTypes) > 0 {
			failType = f.pollFailTypes[len(f.pollFailTypes)-1]
			if index < len(f.pollFailTypes) {
				failType = f.pollFailTypes[index]
			}
		}
		onPoll := f.onPollResponse
		f.mu.Unlock()

		writeASRJSON(w, f.pollPayload(status, failType))
		if onPoll != nil {
			onPoll(index)
		}
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// pollPayload 构造一次查询接口的响应体。
func (f *asrFakeUpstream) pollPayload(status int, failType int) map[string]any {
	orderResult := ""
	if status == ASROrderStatusCompleted {
		orderResult = asrCompletedOrderResult()
	}
	return map[string]any{
		"code":     "000000",
		"descInfo": "success",
		"content": map[string]any{
			"orderInfo": map[string]any{
				"orderId":          f.orderId,
				"status":           status,
				"failType":         failType,
				"originalDuration": 12345,
			},
			"orderResult": orderResult,
		},
	}
}

// asrCompletedOrderResult 构造一段已转写完成的订单结果，识别文本为「你好」。
func asrCompletedOrderResult() string {
	sentence := `{"st":{"bg":"0","ed":"1000","rt":[{"ws":[{"cw":[{"w":"你好"}]}]}]}}`
	lattice, err := common.Marshal(map[string]any{
		"lattice": []any{map[string]any{"json_1best": sentence}},
	})
	if err != nil {
		return ""
	}
	return string(lattice)
}

func writeASRJSON(w http.ResponseWriter, payload any) {
	data, err := common.Marshal(payload)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func (f *asrFakeUpstream) uploads() []asrRecordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]asrRecordedRequest(nil), f.uploadRecords...)
}

func (f *asrFakeUpstream) polls() []asrRecordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]asrRecordedRequest(nil), f.pollRecords...)
}

// asrTestCredentials 返回测试用凭据。
func asrTestCredentials() ASRCredentials {
	return ASRCredentials{AppID: testASRAppID, APISecret: testASRSecret, APIKey: testASRAccessKey}
}

// asrTestAudio 返回测试用音频。
func asrTestAudio() ASRAudio {
	return ASRAudio{
		Data:       []byte("fake-audio-bytes"),
		FileName:   "sample.wav",
		Format:     "wav",
		DurationMs: 56352,
	}
}

// useASRFixedClock 用固定时刻替换 asrNow，使上传与查询各自的 dateTime 可确定地不同。
func useASRFixedClock(t *testing.T, times ...time.Time) {
	t.Helper()

	previous := asrNow
	var mu sync.Mutex
	index := 0
	asrNow = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		current := times[len(times)-1]
		if index < len(times) {
			current = times[index]
		}
		if index < len(times)-1 {
			index++
		}
		return current
	}
	t.Cleanup(func() { asrNow = previous })
}

// TestASRTranscribeUploadAndPoll 覆盖完整的上传→轮询→终态流程与协议细节。
func TestASRTranscribeUploadAndPoll(t *testing.T) {
	fake := newASRFakeUpstream(t, []int{ASROrderStatusProcessing, ASROrderStatusCompleted})
	useASRFixedClock(t,
		time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("UTC+8", 8*60*60)),
		time.Date(2026, 1, 2, 3, 4, 6, 0, time.FixedZone("UTC+8", 8*60*60)),
		time.Date(2026, 1, 2, 3, 4, 7, 0, time.FixedZone("UTC+8", 8*60*60)),
	)

	audio := asrTestAudio()
	result, err := ASRTranscribe(context.Background(), asrTestCredentials(), audio, ASROptions{
		PollInterval: time.Millisecond,
		MaxWait:      10 * time.Second,
		BaseURL:      fake.server.URL,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	// 终态判定与结果内容。
	assert.Equal(t, ASROrderStatusCompleted, result.Order.Status)
	assert.Equal(t, "你好", result.Text)
	assert.Equal(t, int64(12345), result.OriginalDurationMs)
	assert.Equal(t, ASRResultTypeTransfer, result.ResultType)

	uploads := fake.uploads()
	require.Len(t, uploads, 1)
	polls := fake.polls()
	require.Len(t, polls, 2, "订单处理中时应继续轮询一次")

	// 上传请求：路径、请求头、请求体与查询参数。
	upload := uploads[0]
	assert.Equal(t, http.MethodPost, upload.Method)
	assert.Equal(t, ASRUploadPath, upload.Path)
	assert.Equal(t, "application/octet-stream", upload.Header.Get("Content-Type"))
	assert.Equal(t, audio.Data, upload.Body, "上传请求体应是音频原始字节")
	assert.Equal(t, testASRAppID, upload.Query.Get("appId"))
	assert.Equal(t, testASRAccessKey, upload.Query.Get("accessKeyId"))
	assert.Equal(t, strconv.Itoa(len(audio.Data)), upload.Query.Get("fileSize"))
	assert.Equal(t, "sample.wav", upload.Query.Get("fileName"))
	assert.Equal(t, "56352", upload.Query.Get("duration"))
	assert.Equal(t, ASRDefaultLanguage, upload.Query.Get("language"))
	assert.Equal(t, "2026-01-02T03:04:05+0800", upload.Query.Get("dateTime"))

	// 签名走请求头，不进查询串。
	assert.NotEmpty(t, upload.Header.Get(XfyunASRSignatureHeader))
	assert.Empty(t, upload.Query.Get(XfyunASRSignatureHeader))
	assert.NotContains(t, upload.Query.Encode(), XfyunASRSignatureHeader+"=")

	// 查询请求：固定空 JSON 体、查询参数与各自的 dateTime。
	for _, poll := range polls {
		assert.Equal(t, http.MethodPost, poll.Method)
		assert.Equal(t, ASRResultPath, poll.Path)
		assert.Equal(t, "application/json", poll.Header.Get("Content-Type"))
		assert.Equal(t, "{}", string(poll.Body), "查询请求体固定为空 JSON 对象")
		assert.Equal(t, testASRAccessKey, poll.Query.Get("accessKeyId"))
		assert.Equal(t, testASRAppID, poll.Query.Get("appId"))
		assert.Equal(t, testASROrderID, poll.Query.Get("orderId"))
		assert.Equal(t, string(ASRResultTypeTransfer), poll.Query.Get("resultType"))
		assert.NotEmpty(t, poll.Header.Get(XfyunASRSignatureHeader))
		assert.Empty(t, poll.Query.Get(XfyunASRSignatureHeader))
	}

	// 两次调用共用同一 signatureRandom，dateTime 各自重新生成。
	signatureRandom := upload.Query.Get("signatureRandom")
	require.NotEmpty(t, signatureRandom)
	assert.Equal(t, "2026-01-02T03:04:06+0800", polls[0].Query.Get("dateTime"))
	assert.Equal(t, "2026-01-02T03:04:07+0800", polls[1].Query.Get("dateTime"))
	for _, poll := range polls {
		assert.Equal(t, signatureRandom, poll.Query.Get("signatureRandom"), "查询必须复用上传的 signatureRandom")
	}
}

// TestASRTranscribeOptions 语言、结果类型与文件名补全。
func TestASRTranscribeOptions(t *testing.T) {
	fake := newASRFakeUpstream(t, []int{ASROrderStatusCompleted})

	audio := asrTestAudio()
	audio.FileName = "recording"
	result, err := ASRTranscribe(context.Background(), asrTestCredentials(), audio, ASROptions{
		PollInterval: time.Millisecond,
		MaxWait:      10 * time.Second,
		Language:     "en",
		ResultType:   ASRResultTypeAnalysis,
		BaseURL:      fake.server.URL,
	})
	require.NoError(t, err)
	assert.Equal(t, ASRResultTypeAnalysis, result.ResultType)

	uploads := fake.uploads()
	require.Len(t, uploads, 1)
	assert.Equal(t, "en", uploads[0].Query.Get("language"))
	assert.Equal(t, "recording.wav", uploads[0].Query.Get("fileName"), "缺后缀的文件名按音频格式补齐")
	assert.Equal(t, string(ASRResultTypeAnalysis), fake.polls()[0].Query.Get("resultType"))
}

// TestASRTranscribeOrderFailed 订单失败时返回明确错误。
func TestASRTranscribeOrderFailed(t *testing.T) {
	fake := newASRFakeUpstream(t, []int{ASROrderStatusFailed})
	fake.pollFailTypes = []int{2}

	result, err := ASRTranscribe(context.Background(), asrTestCredentials(), asrTestAudio(), ASROptions{
		PollInterval: time.Millisecond,
		MaxWait:      10 * time.Second,
		BaseURL:      fake.server.URL,
	})
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "失败类型 2")
}

// TestASRTranscribeRespectsContextCancellation 客户端取消后立即停止上游轮询。
func TestASRTranscribeRespectsContextCancellation(t *testing.T) {
	fake := newASRFakeUpstream(t, []int{ASROrderStatusProcessing})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake.onPollResponse = func(pollIndex int) {
		if pollIndex == 0 {
			cancel()
		}
	}

	result, err := ASRTranscribe(ctx, asrTestCredentials(), asrTestAudio(), ASROptions{
		PollInterval: 50 * time.Millisecond,
		MaxWait:      10 * time.Second,
		BaseURL:      fake.server.URL,
	})
	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Len(t, fake.polls(), 1, "取消后不应继续轮询")
}

// TestASRTranscribeTimeout 超过最大等待时间后返回明确错误。
func TestASRTranscribeTimeout(t *testing.T) {
	fake := newASRFakeUpstream(t, []int{ASROrderStatusProcessing})

	result, err := ASRTranscribe(context.Background(), asrTestCredentials(), asrTestAudio(), ASROptions{
		PollInterval: 5 * time.Millisecond,
		MaxWait:      40 * time.Millisecond,
		BaseURL:      fake.server.URL,
	})
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "超时")
}

// TestASRTranscribeRejectsInvalidInput 凭据与音频为空时立即报错。
func TestASRTranscribeRejectsInvalidInput(t *testing.T) {
	_, err := ASRTranscribe(context.Background(), ASRCredentials{}, asrTestAudio(), ASROptions{})
	require.Error(t, err)

	_, err = ASRTranscribe(context.Background(), asrTestCredentials(), ASRAudio{}, ASROptions{})
	require.Error(t, err)
}

// TestASRFirstPollDelay 首次等待时长的取值规则。
func TestASRFirstPollDelay(t *testing.T) {
	const pollInterval = 5 * time.Second

	tests := []struct {
		name         string
		estimateMs   int64
		pollInterval time.Duration
		want         time.Duration
	}{
		{name: "预估时长不超过 5 秒时按轮询间隔等待", estimateMs: 5000, pollInterval: pollInterval, want: pollInterval},
		{name: "预估时长超过 5 秒时取预估秒数的三成", estimateMs: 10000, pollInterval: pollInterval, want: 3 * time.Second},
		{name: "预估秒数乘三成超过轮询间隔时取轮询间隔", estimateMs: 100000, pollInterval: pollInterval, want: pollInterval},
		{name: "轮询间隔较小时以轮询间隔为准", estimateMs: 10000, pollInterval: time.Second, want: time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, asrFirstPollDelay(tt.estimateMs, tt.pollInterval))
		})
	}
}

// TestASRUploadFileName 上传文件名的后缀补全规则。
func TestASRUploadFileName(t *testing.T) {
	tests := []struct {
		name     string
		fileName string
		format   string
		want     string
	}{
		{name: "已有后缀时保持不变", fileName: "a.wav", format: "wav", want: "a.wav"},
		{name: "缺后缀时按格式补齐", fileName: "a", format: "wav", want: "a.wav"},
		{name: "格式带点号时补齐", fileName: "a", format: ".mp3", want: "a.mp3"},
		{name: "文件名为空时使用默认名", fileName: "", format: "mp3", want: "audio.mp3"},
		{name: "没有格式信息时保持原样", fileName: "a", format: "", want: "a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, asrUploadFileName(tt.fileName, tt.format))
		})
	}
}

// TestASRDefaultBaseURL 讯飞 IFASR 主机是独立于星火对话的新增常量。
func TestASRDefaultBaseURL(t *testing.T) {
	assert.Equal(t, "https://office-api-ist-dx.iflyaisol.com", ASRDefaultBaseURL)
	assert.True(t, strings.HasPrefix(ASRDefaultBaseURL, "https://"))
	assert.Equal(t, 5*time.Second, ASRDefaultPollInterval)
}

// testWAVBytes 构造一段 1 秒的 16kHz 单声道 16bit WAV 音频。
func testWAVBytes() []byte {
	const sampleRate = 16000
	const samples = 16000
	dataSize := samples * 2

	buffer := &bytes.Buffer{}
	buffer.WriteString("RIFF")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(36+dataSize))
	buffer.WriteString("WAVE")
	buffer.WriteString("fmt ")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(16))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1)) // PCM
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1)) // 单声道
	_ = binary.Write(buffer, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(sampleRate*2))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(2))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(16))
	buffer.WriteString("data")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(dataSize))
	buffer.Write(make([]byte, dataSize))
	return buffer.Bytes()
}

// TestAdaptorConvertAudioRequest 音频转换产出待上传的音频字节与元信息。
func TestAdaptorConvertAudioRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	audio := testWAVBytes()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "sample.wav")
	require.NoError(t, err)
	_, err = part.Write(audio)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())

	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeAudioTranscription,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}

	reader, err := adaptor.ConvertAudioRequest(c, info, dto.AudioRequest{Model: "spark-asr"})
	require.NoError(t, err)
	require.NotNil(t, reader)

	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, audio, data, "音频转换应产出音频原始字节")

	require.NotNil(t, adaptor.audio)
	assert.Equal(t, "wav", adaptor.audio.Format)
	assert.Equal(t, "sample.wav", adaptor.audio.FileName)
	assert.Equal(t, int64(1000), adaptor.audio.DurationMs)
}

// TestAdaptorConvertAudioRequestReturnsBadRequest 本地音频来源问题（缺少音频、
// 不受支持的扩展名等）返回 400，而不是被上层包装成 500。
func TestAdaptorConvertAudioRequestReturnsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	webm := &bytes.Buffer{}
	writer := multipart.NewWriter(webm)
	part, err := writer.CreateFormFile("file", "sample.webm")
	require.NoError(t, err)
	_, err = part.Write([]byte("fake-webm-bytes"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	cases := []struct {
		name        string
		body        *bytes.Buffer
		contentType string
	}{
		{name: "没有提供音频", body: bytes.NewBufferString(`{"model":"spark-asr"}`), contentType: "application/json"},
		{name: "上传的音频扩展名不受支持", body: webm, contentType: writer.FormDataContentType()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", tc.body)
			c.Request.Header.Set("Content-Type", tc.contentType)

			adaptor := &Adaptor{}
			info := &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeAudioTranscription,
				ChannelMeta: &relaycommon.ChannelMeta{},
			}

			_, err := adaptor.ConvertAudioRequest(c, info, dto.AudioRequest{Model: "spark-asr"})
			require.Error(t, err)

			var apiErr *types.NewAPIError
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			assert.Equal(t, types.ErrorCodeConvertRequestFailed, apiErr.GetErrorCode())
			assert.True(t, types.IsSkipRetryError(apiErr))

			// 上层把转换失败再包一层时，errors.As 分支必须保留这个 400，而不是退回 500。
			wrapped := types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			assert.Equal(t, http.StatusBadRequest, wrapped.StatusCode)
		})
	}
}

// TestAdaptorDoResponseAudioBranch 音频路径不依赖星火对话专用的请求状态，
// 并把转写结果放入请求上下文供响应构造使用。
func TestAdaptorDoResponseAudioBranch(t *testing.T) {
	gin.SetMode(gin.TestMode)

	fake := newASRFakeUpstream(t, []int{ASROrderStatusCompleted})

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)

	adaptor := &Adaptor{
		audio: &ASRAudio{
			Data:       []byte("fake-audio-bytes"),
			FileName:   "sample.wav",
			Format:     "wav",
			DurationMs: 1000,
		},
	}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeAudioTranscription,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: fake.server.URL,
			ApiKey:         testASRAppID + "|" + testASRSecret + "|" + testASRAccessKey,
		},
	}

	usage, apiErr := adaptor.DoResponse(c, nil, info)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	result, ok := ASRResultFromContext(c)
	require.True(t, ok)
	require.NotNil(t, result)
	assert.Equal(t, "你好", result.Text)
	assert.Equal(t, result, adaptor.ASRResult())

	// 渠道 BaseURL 覆盖只作用于 IFASR 转写路径。
	uploads := fake.uploads()
	require.Len(t, uploads, 1)
	assert.Equal(t, ASRUploadPath, uploads[0].Path)
}

// TestAdaptorDoResponseAudioBranchInvalidKey 凭据不是三段时报错。
func TestAdaptorDoResponseAudioBranchInvalidKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)

	adaptor := &Adaptor{audio: &ASRAudio{Data: []byte("fake-audio-bytes"), FileName: "a.wav", Format: "wav"}}
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeAudioTranscription,
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "only-one-part"},
	}

	_, apiErr := adaptor.DoResponse(c, nil, info)
	require.NotNil(t, apiErr)
}

// TestAdaptorModelGuard 模型名守卫：转写路径只接受转写模型，对话路径拒绝转写模型。
//
// 守卫判定用的是模型映射之后的上游模型名：运营方给转写模型配了别名时，转写请求仍然
// 被接受；转写模型被映射到别的模型时，转写请求被明确拒绝。
func TestAdaptorModelGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("转写路径拒绝非转写模型", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)

		adaptor := &Adaptor{audio: &ASRAudio{Data: []byte("fake-audio-bytes"), FileName: "a.wav", Format: "wav"}}
		info := &relaycommon.RelayInfo{
			RelayMode: relayconstant.RelayModeAudioTranscription,
			ChannelMeta: &relaycommon.ChannelMeta{
				ApiKey:            testASRAppID + "|" + testASRSecret + "|" + testASRAccessKey,
				UpstreamModelName: "SparkDesk",
			},
		}

		_, apiErr := adaptor.DoResponse(c, nil, info)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
		assert.Equal(t, types.ErrorCodeInvalidRequest, apiErr.GetErrorCode())
		assert.True(t, types.IsSkipRetryError(apiErr))
		assert.Contains(t, apiErr.Error(), ASRModelName)
	})

	t.Run("转写模型被映射到别的模型时转写路径拒绝", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)

		adaptor := &Adaptor{audio: &ASRAudio{Data: []byte("fake-audio-bytes"), FileName: "a.wav", Format: "wav"}}
		info := &relaycommon.RelayInfo{
			OriginModelName: ASRModelName,
			RelayMode:       relayconstant.RelayModeAudioTranscription,
			ChannelMeta: &relaycommon.ChannelMeta{
				ApiKey:            testASRAppID + "|" + testASRSecret + "|" + testASRAccessKey,
				UpstreamModelName: "SparkDesk",
			},
		}

		_, apiErr := adaptor.DoResponse(c, nil, info)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
		assert.Equal(t, types.ErrorCodeInvalidRequest, apiErr.GetErrorCode())
	})

	t.Run("对话路径拒绝转写模型", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

		adaptor := &Adaptor{request: &dto.GeneralOpenAIRequest{Model: ASRModelName}}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ApiKey:            testASRAppID + "|" + testASRSecret + "|" + testASRAccessKey,
				UpstreamModelName: ASRModelName,
			},
		}

		_, apiErr := adaptor.DoResponse(c, nil, info)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
		assert.Equal(t, types.ErrorCodeInvalidRequest, apiErr.GetErrorCode())
		assert.True(t, types.IsSkipRetryError(apiErr))
		assert.Contains(t, apiErr.Error(), ASRModelName)
	})

	t.Run("对话路径的普通模型不受守卫影响", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

		adaptor := &Adaptor{request: &dto.GeneralOpenAIRequest{Model: "SparkDesk"}}
		// 密钥不满足三段，错误必然是凭据错误而不是模型守卫错误，说明普通模型没有被守卫拦下。
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ApiKey:            "only-one-part",
				UpstreamModelName: "SparkDesk",
			},
		}

		_, apiErr := adaptor.DoResponse(c, nil, info)
		require.NotNil(t, apiErr)
		assert.Equal(t, types.ErrorCodeChannelInvalidKey, apiErr.GetErrorCode())
		assert.NotContains(t, apiErr.Error(), ASRModelName)
	})
}

// TestAdaptorModelGuardAcceptsAliasedASRModel 转写模型配了别名时，转写请求按映射后的
// 上游模型名判定，不会被误拒。
func TestAdaptorModelGuardAcceptsAliasedASRModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	fake := newASRFakeUpstream(t, []int{ASROrderStatusCompleted})

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)

	adaptor := &Adaptor{
		audio: &ASRAudio{
			Data:       []byte("fake-audio-bytes"),
			FileName:   "sample.wav",
			Format:     "wav",
			DurationMs: 1000,
		},
	}
	info := &relaycommon.RelayInfo{
		// 客户端请求的是别名，渠道把别名映射到了转写模型。
		OriginModelName: "whisper-1",
		RelayMode:       relayconstant.RelayModeAudioTranscription,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    fake.server.URL,
			ApiKey:            testASRAppID + "|" + testASRSecret + "|" + testASRAccessKey,
			UpstreamModelName: ASRModelName,
		},
	}

	usage, apiErr := adaptor.DoResponse(c, nil, info)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Len(t, fake.uploads(), 1)
}

// TestASRUploadLanguage 语种取值的映射规则。
func TestASRUploadLanguage(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "缺失时为空", raw: "", want: ""},
		// 讯飞 IFASR 的 language 参数只接受 autodialect 与 autominor，
		// 通用语种代码一律映射到默认的 autodialect。
		{name: "JSON 字符串的中文语种映射为 autodialect", raw: `"zh"`, want: "autodialect"},
		{name: "JSON 字符串的英文语种映射为 autodialect", raw: `"en"`, want: "autodialect"},
		{name: "讯飞自有取值原样透传", raw: `"autodialect"`, want: "autodialect"},
		{name: "讯飞自有取值 autominor 原样透传", raw: `"autominor"`, want: "autominor"},
		{name: "null 视为缺失", raw: "null", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, asrUploadLanguage([]byte(tt.raw)))
		})
	}
}
