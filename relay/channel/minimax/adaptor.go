package minimax

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

type Adaptor struct {
}

// contextKeyAudioCodec 保存本次请求实际发给上游的音频编码，响应侧据此决定 Content-Type。
// 不复用 "response_format"：那个键在 minimax 包里表示上游 output_format（载荷编码），
// 与客户端要的音频编码不是一回事。
const contextKeyAudioCodec = "audio_codec"

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, req *dto.ClaudeRequest) (any, error) {
	adaptor := claude.Adaptor{}
	return adaptor.ConvertClaudeRequest(c, info, req)
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	// 音色克隆 / 音色设计 / 音乐生成三个端点各有自己的上游契约
	switch info.RelayMode {
	case constant.RelayModeAudioVoiceClone, constant.RelayModeAudioVoiceDesign, constant.RelayModeAudioMusic:
		return convertMiniMaxMediaRequest(c, info, request)
	}
	if info.RelayMode != constant.RelayModeAudioSpeech {
		return nil, types.NewErrorWithStatusCode(
			errors.New("unsupported audio relay mode"),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithStatusCode(http.StatusBadRequest),
			types.ErrOptionWithSkipRetry(),
		)
	}

	// 客户端 response_format 的语义是「要什么音频编码」，不是载荷怎么传。
	// hex/url 是载荷语义，一律按 mp3 处理；空串是 OpenAI 客户端的默认值。
	audioCodec := "mp3"
	switch request.ResponseFormat {
	case "mp3", "wav", "flac", "aac", "pcm":
		audioCodec = request.ResponseFormat
	case "", "hex", "url":
	default:
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("minimax speech only supports mp3, wav, flac, aac and pcm audio format, got %q", request.ResponseFormat),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithStatusCode(http.StatusBadRequest),
			types.ErrOptionWithSkipRetry(),
		)
	}

	speed := lo.FromPtrOr(request.Speed, 0.0)

	// 渠道做过模型映射时，上游收到的是映射后的模型名
	model := info.GetUpstreamModelName()
	if model == "" {
		model = info.OriginModelName
	}

	minimaxRequest := MiniMaxTTSRequest{
		Model: model,
		Text:  request.Input,
		VoiceSetting: VoiceSetting{
			VoiceID: request.Voice,
			Speed:   speed,
		},
		// audio_setting.format 是音频编码；顶层 output_format 是载荷编码，上游只接受 hex 与 mp3，
		// 载荷恒为 hex 就永远解得出来，不必猜 mp3 载荷的真实编码
		AudioSetting: &AudioSetting{
			Format: audioCodec,
		},
		OutputFormat: "hex",
	}

	// 同步扩展字段的厂商自定义metadata
	if len(request.Metadata) > 0 {
		if err := common.Unmarshal(request.Metadata, &minimaxRequest); err != nil {
			return nil, fmt.Errorf("error unmarshalling metadata to minimax request: %w", err)
		}
	}

	// metadata 可以补齐 voice_setting，但不能改写输出格式与音频编码
	minimaxRequest.OutputFormat = "hex"
	if minimaxRequest.AudioSetting == nil {
		minimaxRequest.AudioSetting = &AudioSetting{}
	}
	minimaxRequest.AudioSetting.Format = audioCodec

	// metadata 只能补字段，不能改写模型 / 文本 / 音色 / 流式开关：
	// 模型必须走渠道映射后的名字，文本与音色以客户端显式给的为准，
	// 上游流式这条链路解不了 chunked 载荷，所以流式恒为关
	minimaxRequest.Model = model
	minimaxRequest.Stream = false
	minimaxRequest.StreamOptions = nil
	if request.Input != "" {
		minimaxRequest.Text = request.Input
	}
	if request.Voice != "" {
		minimaxRequest.VoiceSetting.VoiceID = request.Voice
	}

	if minimaxRequest.VoiceSetting.VoiceID == "" {
		return nil, types.NewErrorWithStatusCode(
			errors.New("minimax speech requires a voice id: voice_setting.voice_id must be provided by the caller"),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithStatusCode(http.StatusBadRequest),
			types.ErrOptionWithSkipRetry(),
		)
	}

	jsonData, err := common.Marshal(minimaxRequest)
	if err != nil {
		return nil, fmt.Errorf("error marshalling minimax request: %w", err)
	}

	// 响应的 Content-Type 由本次请求实际发给上游的音频编码决定
	c.Set(contextKeyAudioCodec, audioCodec)

	// Debug: log the request structure
	// fmt.Printf("MiniMax TTS Request: %s\n", string(jsonData))

	return bytes.NewReader(jsonData), nil
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	if info.RelayMode != constant.RelayModeImagesGenerations {
		return nil, minimaxImageInvalidRequest("unsupported image relay mode: %d", info.RelayMode)
	}
	// 渠道做过模型映射时，上游请求体里的 model 是映射后的上游名；
	// 原始模型名缺失时退回请求里的 model，并把结果写回 info，
	// 让请求 URL 与响应解析读到同一个模型名
	upstreamModel := minimaxUpstreamModelName(info)
	if upstreamModel == "" {
		upstreamModel = request.Model
	}
	if upstreamModel != "" && info.ChannelMeta != nil {
		info.UpstreamModelName = upstreamModel
	}
	if isMiniMaxH3ImageModel(upstreamModel) {
		h3Request, err := oaiImage2MiniMaxH3ImageRequest(request, upstreamModel)
		if err != nil {
			return nil, err
		}
		return h3Request, nil
	}
	return oaiImage2MiniMaxImageRequest(request), nil
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return GetRequestURL(info)
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	req.Set("Authorization", "Bearer "+info.ApiKey)
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	return request, nil
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, nil
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	if info.RelayMode == constant.RelayModeAudioSpeech {
		return handleTTSResponse(c, resp, info)
	}
	if info.RelayMode == constant.RelayModeAudioVoiceClone {
		return handleVoiceCloneResponse(c, resp, info)
	}
	if info.RelayMode == constant.RelayModeAudioVoiceDesign {
		return handleVoiceDesignResponse(c, resp, info)
	}
	if info.RelayMode == constant.RelayModeAudioMusic {
		return handleMusicResponse(c, resp, info)
	}
	if info.RelayMode == constant.RelayModeImagesGenerations {
		return miniMaxImageHandler(c, resp, info)
	}

	switch info.RelayFormat {
	case types.RelayFormatClaude:
		adaptor := claude.Adaptor{}
		return adaptor.DoResponse(c, resp, info)
	default:
		adaptor := openai.Adaptor{}
		return adaptor.DoResponse(c, resp, info)
	}
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
