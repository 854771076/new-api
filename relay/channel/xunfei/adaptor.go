package xunfei

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// contextKeyASRResult 是讯飞转写结果在 gin 上下文中的键。
const contextKeyASRResult = "xunfei_asr_result"

type Adaptor struct {
	// request 是星火对话路径专用的请求状态，音频路径不会写入它。
	request *dto.GeneralOpenAIRequest
	// audio 是音频路径解析出的待上传音频。
	audio *ASRAudio
	// asrLanguage 是上传时使用的语种。
	asrLanguage string
	// asrResult 是音频路径的转写结果，供响应构造与计费使用。
	asrResult *ASRResult
	// responseFormat 是客户端请求的转写响应格式，缺省为 json。
	responseFormat string
}

// asrAdaptorPollInterval 是 IFASR 的轮询间隔，测试会替换该变量以缩短等待时间。
var asrAdaptorPollInterval = ASRDefaultPollInterval

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertClaudeRequest(*gin.Context, *relaycommon.RelayInfo, *dto.ClaudeRequest) (any, error) {
	//TODO implement me
	panic("implement me")
}

// ConvertAudioRequest 解析音频来源，产出待上传的音频原始字节。
//
// 音频来源由 service.ResolveAudioSource 归一（multipart file / Base64 / URL 三种来源），
// 归一结果同时给出音频格式、文件名与时长（毫秒），供讯飞上传接口的查询参数使用。
func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	if info.RelayMode != relayconstant.RelayModeAudioTranscription && info.RelayMode != relayconstant.RelayModeAudioTranslation {
		return nil, errors.New("unsupported audio relay mode")
	}

	source, err := service.ResolveAudioSource(c, &request)
	if err != nil {
		return nil, newASRAudioSourceError(err)
	}

	a.audio = &ASRAudio{
		Data:       source.Data,
		FileName:   source.FileName,
		Format:     source.Format,
		DurationMs: source.DurationMs,
	}
	a.asrLanguage = asrUploadLanguage(request.Language)
	a.responseFormat = request.ResponseFormat
	if a.responseFormat == "" {
		a.responseFormat = asrResponseFormatJSON
	}
	return bytes.NewReader(source.Data), nil
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return "", nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	a.request = request
	return request, nil
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, nil
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	// TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	// xunfei's request is not http request, so we don't need to do anything here
	dummyResp := &http.Response{}
	dummyResp.StatusCode = http.StatusOK
	return dummyResp, nil
}

// DoResponse 按 relay mode 分流：音频转写与音频翻译走讯飞 IFASR 的两段式流程，
// 其余走既有的星火对话路径。音频路径不持有星火对话专用的请求状态。
//
// 分流先于星火路径的版本解析（getAPIVersion 只在星火路径被调用），因此转写模型名不会
// 被当成 API 版本。分流的同时做模型名守卫：转写模型不能用于对话接口，对话模型也不能
// 用于转写接口，误用时给出明确的客户端错误而不是含糊的上游报错。
func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	// 守卫判定用模型映射之后的上游模型名：运营方给转写模型配了别名（model_mapping）时，
	// 客户端请求的是别名，判定仍要落在转写模型上；反过来，转写模型被映射到别的模型时，
	// 转写请求必须被拒绝。上游模型名缺失时回落到客户端请求的原始模型名。
	model := info.GetUpstreamModelName()
	if model == "" {
		model = info.OriginModelName
	}
	isTranscription := info.RelayMode == relayconstant.RelayModeAudioTranscription || info.RelayMode == relayconstant.RelayModeAudioTranslation
	// 请求没有携带模型名时不做判定：生产链路上模型名必填（GetAndValidAudioRequest 已校验），
	// 这里只是让守卫不误伤没有模型信息的调用方。
	if model != "" {
		switch {
		case isTranscription && !IsASRModel(model):
			return nil, newASRClientError("模型 " + model + " 不是讯飞转写模型，转写请求必须使用 " + ASRModelName)
		case !isTranscription && IsASRModel(model):
			return nil, newASRClientError("模型 " + ASRModelName + " 是讯飞转写模型，只能用于 /v1/audio/transcriptions，不能用于对话接口")
		}
	}

	if isTranscription {
		return a.doASRResponse(c, info)
	}

	splits := strings.Split(info.ApiKey, "|")
	if len(splits) != 3 {
		return nil, types.NewError(errors.New("invalid auth"), types.ErrorCodeChannelInvalidKey)
	}
	if a.request == nil {
		return nil, types.NewError(errors.New("request is nil"), types.ErrorCodeInvalidRequest)
	}
	if info.IsStream {
		usage, err = xunfeiStreamHandler(c, *a.request, splits[0], splits[1], splits[2])
	} else {
		usage, err = xunfeiHandler(c, *a.request, splits[0], splits[1], splits[2])
	}
	return
}

// newASRClientError 构造讯飞转写路径上的客户端错误。
//
// 这类错误是客户端请求有误（模型名用错、缺少音频来源等）：状态码取 400 并显式跳过重试，
// 既不落在仓库默认的重试范围里，也不会因为重试而反复触发上游调用。
func newASRClientError(message string) *types.NewAPIError {
	return types.NewError(errors.New(message), types.ErrorCodeInvalidRequest,
		types.ErrOptionWithStatusCode(http.StatusBadRequest), types.ErrOptionWithSkipRetry())
}

// doASRResponse 完成讯飞 IFASR 的上传与轮询，写出转写响应并产出音频结算用量。
func (a *Adaptor) doASRResponse(c *gin.Context, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	splits := strings.Split(info.ApiKey, "|")
	if len(splits) != 3 {
		return nil, types.NewError(errors.New("invalid auth"), types.ErrorCodeChannelInvalidKey)
	}
	if a.audio == nil {
		return nil, newASRClientError("audio source is missing")
	}

	// 渠道 BaseURL 覆盖只作用于 IFASR 转写路径，星火对话继续使用其既有地址。
	baseURL := ""
	if info.HasChannelMeta() {
		baseURL = info.ChannelBaseUrl
	}

	result, err := ASRTranscribe(c.Request.Context(), ASRCredentials{
		AppID:     splits[0],
		APISecret: splits[1],
		APIKey:    splits[2],
	}, *a.audio, ASROptions{
		BaseURL:      baseURL,
		Language:     a.asrLanguage,
		ResultType:   ASRResultTypeTransfer,
		PollInterval: asrAdaptorPollInterval,
	})
	if err != nil {
		return nil, NewASRAPIError(err, types.ErrorCodeDoRequestFailed)
	}

	// 上游未回报原始时长时回落到本地解析出的音频时长，保证响应里的时长与计费时长一致。
	if result.OriginalDurationMs == 0 && a.audio.DurationMs > 0 {
		result.OriginalDurationMs = a.audio.DurationMs
	}

	a.asrResult = result
	c.Set(contextKeyASRResult, result)

	if err := WriteASRResponse(c, result, a.responseFormat); err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}

	usage, clamp := buildASRUsage(result, *a.audio)
	if clamp != nil {
		// 饱和标记交给结算层记录：记录函数、「首次钳制生效」的守卫与日志挂载都在 service 包内。
		service.NoteQuotaClamp(info, clamp)
	}
	return usage, nil
}

// ASRResult 返回本次音频转写的结果，音频路径之外返回 nil。
func (a *Adaptor) ASRResult() *ASRResult {
	return a.asrResult
}

// ASRResultFromContext 从请求上下文取出讯飞转写结果。
func ASRResultFromContext(c *gin.Context) (*ASRResult, bool) {
	if c == nil {
		return nil, false
	}
	value, ok := c.Get(contextKeyASRResult)
	if !ok {
		return nil, false
	}
	result, ok := value.(*ASRResult)
	return result, ok
}

// asrUploadLanguage 从请求的 language 字段取出上传语种。
//
// 讯飞 IFASR 的 language 参数只接受 autodialect（中英 + 202 种方言免切识别）与
// autominor（37 个语种免切识别，需单独申请权限）两个取值，因此客户端给的通用语种代码
// （zh / en 等）与其它未知取值一律回落到默认语种；讯飞自有取值原样透传（大小写归一）。
// 字段缺失或不是字符串时返回空串，由调用方回落到默认语种。
func asrUploadLanguage(raw []byte) string {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return ""
	}
	var language string
	if err := common.Unmarshal(raw, &language); err != nil {
		// 不是 JSON 字符串形态时按原始文本处理，非字符串内容视为未提供语种。
		if !strings.HasPrefix(value, `"`) {
			return ""
		}
		language = strings.Trim(value, `"`)
	}
	return normalizeASRLanguage(language)
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
