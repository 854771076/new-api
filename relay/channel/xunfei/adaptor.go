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
}

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
		return nil, err
	}

	a.audio = &ASRAudio{
		Data:       source.Data,
		FileName:   source.FileName,
		Format:     source.Format,
		DurationMs: source.DurationMs,
	}
	a.asrLanguage = asrUploadLanguage(request.Language)
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
func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	if info.RelayMode == relayconstant.RelayModeAudioTranscription || info.RelayMode == relayconstant.RelayModeAudioTranslation {
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

// doASRResponse 完成讯飞 IFASR 的上传与轮询，并把解析结果放入请求上下文供响应构造与计费使用。
func (a *Adaptor) doASRResponse(c *gin.Context, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	splits := strings.Split(info.ApiKey, "|")
	if len(splits) != 3 {
		return nil, types.NewError(errors.New("invalid auth"), types.ErrorCodeChannelInvalidKey)
	}
	if a.audio == nil {
		return nil, types.NewError(errors.New("audio source is missing"), types.ErrorCodeInvalidRequest)
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
		BaseURL:    baseURL,
		Language:   a.asrLanguage,
		ResultType: ASRResultTypeTransfer,
	})
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeDoRequestFailed)
	}

	a.asrResult = result
	c.Set(contextKeyASRResult, result)
	// 音频 token 的用量回填由音频计费环节负责，这里只交出转写结果本身。
	return &dto.Usage{}, nil
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
// 客户端给的是通用语种代码（zh / en），讯飞接口用的是 cn / en，这里做一次映射；
// 讯飞自有取值（autodialect / autominor 等）原样透传。字段缺失或不是字符串时返回空串，
// 由调用方回落到默认语种。
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
	language = strings.TrimSpace(language)
	switch strings.ToLower(language) {
	case "zh", "zh-cn", "zh_cn", "zh-hans", "zh-hant", "zh-tw", "zh_tw":
		return "cn"
	case "en", "en-us", "en_us", "en-gb", "en_gb":
		return "en"
	}
	return language
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
