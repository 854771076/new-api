package minimax

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// MiniMax 的三个媒体端点：音色克隆 /v1/voice_clone、音色设计 /v1/voice_design、
// 音乐生成 /v1/music_generation。客户端统一走 OpenAI 的音频请求体，
// 厂商专有字段从 metadata 透传进来。
const (
	// 上游对克隆的合成文本长度上限
	minimaxVoiceCloneMaxText = 1000
	// 上游对克隆的文本校验字段长度上限
	minimaxVoiceCloneMaxTextValidation = 200
	// 上游对音色设计试听文本的长度上限
	minimaxVoiceDesignMaxPreviewText = 500
	// 上游音乐载荷默认以 hex 返回，网关解码后回传
	minimaxMusicDefaultOutputFormat = "hex"
	// 上游音色设计的试听音频解码后按 mp3 交付
	minimaxVoiceDesignAudioFormat = "mp3"
)

// MiniMaxVoiceCloneRequest 是 POST /v1/voice_clone 的请求体。
// file_id 与 voice_id 必填，voice_id 由调用方生成并传入 —— 上游克隆响应不回传它。
type MiniMaxVoiceCloneRequest struct {
	FileID      int64               `json:"file_id"`
	VoiceID     string              `json:"voice_id"`
	ClonePrompt *MiniMaxClonePrompt `json:"clone_prompt,omitempty"`
	Text        string              `json:"text,omitempty"`
	// text_file_id 是厂商透传字段，保留客户端的原始 JSON，避免数字 id 被转成浮点丢精度。
	TextFileID              json.RawMessage `json:"text_file_id,omitempty"`
	Model                   string          `json:"model,omitempty"`
	LanguageBoost           string          `json:"language_boost,omitempty"`
	TextValidation          string          `json:"text_validation,omitempty"`
	Accuracy                *float64        `json:"accuracy,omitempty"`
	NeedNoiseReduction      *bool           `json:"need_noise_reduction,omitempty"`
	NeedVolumeNormalization *bool           `json:"need_volume_normalization,omitempty"`
	AigcWatermark           *bool           `json:"aigc_watermark,omitempty"`
}

// MiniMaxClonePrompt 是克隆的可选补充提示，不能替代 file_id。
type MiniMaxClonePrompt struct {
	// PromptAudio 是上传文件的 id，上游可能回数字也可能回字符串，按原类型转发
	PromptAudio any    `json:"prompt_audio,omitempty"`
	PromptText  string `json:"prompt_text,omitempty"`
}

// MiniMaxVoiceCloneResponse 是 POST /v1/voice_clone 的响应体。
type MiniMaxVoiceCloneResponse struct {
	VoiceID string `json:"voice_id"`
	// DemoAudio 是上游给的试听音频地址，在响应顶层
	DemoAudio      string                          `json:"demo_audio"`
	InputSensitive MiniMaxVoiceCloneInputSensitive `json:"input_sensitive"`
	ExtraInfo      MiniMaxVoiceCloneExtraInfo      `json:"extra_info"`
	BaseResp       MiniMaxBaseResp                 `json:"base_resp"`
}

// MiniMaxVoiceCloneInputSensitive 是上游的输入敏感度信息。
// Type 可能是数字（0..7）也可能是字符串，两种都得能解下来。
type MiniMaxVoiceCloneInputSensitive struct {
	Type any `json:"type"`
}

type MiniMaxVoiceCloneExtraInfo struct {
	AudioLength     int64 `json:"audio_length"`
	AudioSampleRate int64 `json:"audio_sample_rate"`
	AudioSize       int64 `json:"audio_size"`
	Bitrate         int64 `json:"bitrate"`
	WordCount       int64 `json:"word_count"`
	UsageCharacters int64 `json:"usage_characters"`
}

// MiniMaxVoiceDesignRequest 是 POST /v1/voice_design 的请求体。
// voice_id 可选，省略时由上游生成并在响应里返回。
type MiniMaxVoiceDesignRequest struct {
	Prompt      string `json:"prompt"`
	PreviewText string `json:"preview_text"`
	VoiceID     string `json:"voice_id,omitempty"`
}

// MiniMaxVoiceDesignResponse 是 POST /v1/voice_design 的响应体。
type MiniMaxVoiceDesignResponse struct {
	VoiceID    string                      `json:"voice_id"`
	TrialAudio string                      `json:"trial_audio"`
	ExtraInfo  MiniMaxVoiceDesignExtraInfo `json:"extra_info"`
	BaseResp   MiniMaxBaseResp             `json:"base_resp"`
}

type MiniMaxVoiceDesignExtraInfo struct {
	UsageCharacters int64 `json:"usage_characters"`
}

// MiniMaxVoiceDesignResult 是网关回给客户端的音色设计结果。
// 试听音频上游给的是 hex，这里解码后按 base64 交付，客户端可直接播放。
type MiniMaxVoiceDesignResult struct {
	VoiceID       string `json:"voice_id"`
	TrialAudioB64 string `json:"trial_audio_base64"`
	AudioFormat   string `json:"audio_format"`
}

// MiniMaxMusicRequest 是 POST /v1/music_generation 的请求体。
// 网关的音频链路是同步的，不透传上游的 stream 开关。
type MiniMaxMusicRequest struct {
	Model           string        `json:"model"`
	Prompt          string        `json:"prompt,omitempty"`
	Lyrics          string        `json:"lyrics,omitempty"`
	OutputFormat    string        `json:"output_format,omitempty"`
	AudioSetting    *AudioSetting `json:"audio_setting,omitempty"`
	AigcWatermark   *bool         `json:"aigc_watermark,omitempty"`
	LyricsOptimizer *bool         `json:"lyrics_optimizer,omitempty"`
	IsInstrumental  *bool         `json:"is_instrumental,omitempty"`
}

// MiniMaxMusicResponse 是 POST /v1/music_generation 的响应体。
type MiniMaxMusicResponse struct {
	Data      MiniMaxMusicData  `json:"data"`
	ExtraInfo MiniMaxMusicExtra `json:"extra_info"`
	BaseResp  MiniMaxBaseResp   `json:"base_resp"`
}

type MiniMaxMusicData struct {
	Status int    `json:"status"`
	Audio  string `json:"audio"`
}

type MiniMaxMusicExtra struct {
	MusicDuration   int64 `json:"music_duration"`
	MusicSampleRate int64 `json:"music_sample_rate"`
	MusicChannel    int64 `json:"music_channel"`
	Bitrate         int64 `json:"bitrate"`
	MusicSize       int64 `json:"music_size"`
	UsageCharacters int64 `json:"usage_characters"`
}

// miniMaxVoiceCloneMetadata 是克隆端点能从 metadata 透传的字段，
// 外加两个允许平铺在 metadata 顶层的 clone_prompt 子字段。
type miniMaxVoiceCloneMetadata struct {
	MiniMaxVoiceCloneRequest
	// PromptAudio 是上传文件的 id，数字与字符串都收，按原类型转发给上游
	PromptAudio any    `json:"prompt_audio"`
	PromptText  string `json:"prompt_text"`
}

// convertMiniMaxMediaRequest 按 relay 模式构造 MiniMax 三个媒体端点的上游请求体。
func convertMiniMaxMediaRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	// 渠道做过模型映射时，上游收到的是映射后的模型名
	model := info.GetUpstreamModelName()
	if model == "" {
		model = info.OriginModelName
	}

	var payload any
	switch info.RelayMode {
	case constant.RelayModeAudioVoiceClone:
		cloneRequest, err := miniMaxVoiceCloneRequest(model, request)
		if err != nil {
			return nil, err
		}
		// 上游克隆响应不回传 voice_id，回写调用方自己生成的那个
		c.Set("voice_id", cloneRequest.VoiceID)
		payload = cloneRequest
	case constant.RelayModeAudioVoiceDesign:
		designRequest, err := miniMaxVoiceDesignRequest(request)
		if err != nil {
			return nil, err
		}
		payload = designRequest
	case constant.RelayModeAudioMusic:
		musicRequest, err := miniMaxMusicRequest(model, request)
		if err != nil {
			return nil, err
		}
		// 响应解码与 Content-Type 由本次请求实际发给上游的 output_format 决定
		c.Set("response_format", musicRequest.OutputFormat)
		payload = musicRequest
	default:
		return nil, minimaxMediaInvalidRequest("unsupported audio relay mode")
	}

	jsonData, err := common.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("error marshalling minimax request: %w", err)
	}
	return bytes.NewReader(jsonData), nil
}

// miniMaxVoiceCloneRequest 构造上游 /v1/voice_clone 的请求体。
func miniMaxVoiceCloneRequest(model string, request dto.AudioRequest) (*MiniMaxVoiceCloneRequest, error) {
	var metadata miniMaxVoiceCloneMetadata
	if err := unmarshalMiniMaxMediaMetadata(request.Metadata, &metadata); err != nil {
		return nil, err
	}

	cloneRequest := metadata.MiniMaxVoiceCloneRequest
	// 客户端的显式字段优先于 metadata 里的同名字段，没给才沿用 metadata 的值
	cloneRequest.Model = model
	if request.Input != "" {
		cloneRequest.Text = request.Input
	}
	if request.Voice != "" {
		cloneRequest.VoiceID = request.Voice
	}
	// prompt_audio / prompt_text 也可以平铺在 metadata 顶层，这里折回 clone_prompt；
	// prompt_audio 是上传文件的 id，数字与字符串都收，按原类型转发给上游
	promptAudio := metadata.PromptAudio
	if promptAudio == "" {
		promptAudio = nil
	}
	if cloneRequest.ClonePrompt == nil && (promptAudio != nil || metadata.PromptText != "") {
		cloneRequest.ClonePrompt = &MiniMaxClonePrompt{
			PromptAudio: promptAudio,
			PromptText:  metadata.PromptText,
		}
	}

	if cloneRequest.FileID <= 0 {
		return nil, minimaxMediaInvalidRequest("metadata.file_id is required")
	}
	if cloneRequest.VoiceID == "" {
		return nil, minimaxMediaInvalidRequest("voice is required: the caller generates and passes the voice id")
	}
	// 待合成文本必须给出：客户端 input，或者 metadata 里的 text / text_file_id
	if cloneRequest.Text == "" && len(cloneRequest.TextFileID) == 0 {
		return nil, minimaxMediaInvalidRequest("input or metadata.text is required")
	}
	if utf8.RuneCountInString(cloneRequest.Text) > minimaxVoiceCloneMaxText {
		return nil, minimaxMediaInvalidRequest("input must be at most %d characters", minimaxVoiceCloneMaxText)
	}
	if utf8.RuneCountInString(cloneRequest.TextValidation) > minimaxVoiceCloneMaxTextValidation {
		return nil, minimaxMediaInvalidRequest("metadata.text_validation must be at most %d characters", minimaxVoiceCloneMaxTextValidation)
	}
	if cloneRequest.Accuracy != nil && (*cloneRequest.Accuracy < 0 || *cloneRequest.Accuracy > 1) {
		return nil, minimaxMediaInvalidRequest("metadata.accuracy must be between 0 and 1")
	}
	return &cloneRequest, nil
}

// miniMaxVoiceDesignRequest 构造上游 /v1/voice_design 的请求体。
func miniMaxVoiceDesignRequest(request dto.AudioRequest) (*MiniMaxVoiceDesignRequest, error) {
	var designRequest MiniMaxVoiceDesignRequest
	if err := unmarshalMiniMaxMediaMetadata(request.Metadata, &designRequest); err != nil {
		return nil, err
	}
	// 客户端的显式字段优先于 metadata 里的同名字段，没给才沿用 metadata 的值
	if request.Input != "" {
		designRequest.Prompt = request.Input
	}
	if request.Voice != "" {
		designRequest.VoiceID = request.Voice
	}

	if designRequest.Prompt == "" {
		return nil, minimaxMediaInvalidRequest("input is required")
	}
	if designRequest.PreviewText == "" {
		return nil, minimaxMediaInvalidRequest("metadata.preview_text is required")
	}
	if utf8.RuneCountInString(designRequest.PreviewText) > minimaxVoiceDesignMaxPreviewText {
		return nil, minimaxMediaInvalidRequest("metadata.preview_text must be at most %d characters", minimaxVoiceDesignMaxPreviewText)
	}
	return &designRequest, nil
}

// miniMaxMusicRequest 构造上游 /v1/music_generation 的请求体。
func miniMaxMusicRequest(model string, request dto.AudioRequest) (*MiniMaxMusicRequest, error) {
	var musicRequest MiniMaxMusicRequest
	if err := unmarshalMiniMaxMediaMetadata(request.Metadata, &musicRequest); err != nil {
		return nil, err
	}
	// 客户端的显式字段优先于 metadata 里的同名字段，没给才沿用 metadata 的值
	musicRequest.Model = model
	if request.Input != "" {
		musicRequest.Prompt = request.Input
	}
	// output_format 只取 metadata 里显式给出的值，没给就按上游的 hex 载荷处理
	if musicRequest.OutputFormat == "" {
		musicRequest.OutputFormat = minimaxMusicDefaultOutputFormat
	}
	switch musicRequest.OutputFormat {
	case "hex", "mp3", "url":
	default:
		return nil, minimaxMediaInvalidRequest("metadata.output_format must be one of: hex, mp3, url")
	}
	return &musicRequest, nil
}

// unmarshalMiniMaxMediaMetadata 把客户端 metadata 里的厂商专有字段原样并进上游请求体。
func unmarshalMiniMaxMediaMetadata(metadata json.RawMessage, target any) error {
	if len(metadata) == 0 {
		return nil
	}
	if err := common.Unmarshal(metadata, target); err != nil {
		return minimaxMediaInvalidRequest("invalid metadata: %v", err)
	}
	return nil
}

// handleVoiceCloneResponse 处理上游 /v1/voice_clone 的响应。
func handleVoiceCloneResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return &dto.Usage{}, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to read minimax voice clone response: %w", readErr),
			types.ErrorCodeReadResponseBodyFailed,
			http.StatusInternalServerError,
			types.ErrOptionWithSkipRetry(),
		)
	}
	service.CloseResponseBodyGracefully(resp)

	var cloneResponse MiniMaxVoiceCloneResponse
	if unmarshalErr := common.Unmarshal(body, &cloneResponse); unmarshalErr != nil {
		return &dto.Usage{}, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to unmarshal minimax voice clone response: %w", unmarshalErr),
			types.ErrorCodeBadResponseBody,
			http.StatusInternalServerError,
			types.ErrOptionWithSkipRetry(),
		)
	}
	if cloneResponse.BaseResp.StatusCode != 0 {
		return &dto.Usage{}, minimaxMediaUpstreamError("voice clone", cloneResponse.BaseResp)
	}

	// 上游克隆响应不含 voice_id，回写调用方自己生成的那个
	cloneResponse.VoiceID = c.GetString("voice_id")

	jsonResponse, err := common.Marshal(cloneResponse)
	if err != nil {
		return &dto.Usage{}, types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError, types.ErrOptionWithSkipRetry())
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(http.StatusOK)
	if _, err := c.Writer.Write(jsonResponse); err != nil {
		return &dto.Usage{}, types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError, types.ErrOptionWithSkipRetry())
	}

	return minimaxMediaUsage(info, cloneResponse.ExtraInfo.UsageCharacters), nil
}

// handleVoiceDesignResponse 处理上游 /v1/voice_design 的响应。
func handleVoiceDesignResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return &dto.Usage{}, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to read minimax voice design response: %w", readErr),
			types.ErrorCodeReadResponseBodyFailed,
			http.StatusInternalServerError,
			types.ErrOptionWithSkipRetry(),
		)
	}
	service.CloseResponseBodyGracefully(resp)

	var designResponse MiniMaxVoiceDesignResponse
	if unmarshalErr := common.Unmarshal(body, &designResponse); unmarshalErr != nil {
		return &dto.Usage{}, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to unmarshal minimax voice design response: %w", unmarshalErr),
			types.ErrorCodeBadResponseBody,
			http.StatusInternalServerError,
			types.ErrOptionWithSkipRetry(),
		)
	}
	if designResponse.BaseResp.StatusCode != 0 {
		return &dto.Usage{}, minimaxMediaUpstreamError("voice design", designResponse.BaseResp)
	}

	// 试听音频上游给的是 hex，解码后按 base64 交付
	trialAudio := ""
	if designResponse.TrialAudio != "" {
		decoded, decodeErr := hex.DecodeString(designResponse.TrialAudio)
		if decodeErr != nil {
			return &dto.Usage{}, types.NewErrorWithStatusCode(
				fmt.Errorf("failed to decode minimax voice design trial audio: %w", decodeErr),
				types.ErrorCodeBadResponse,
				http.StatusInternalServerError,
				types.ErrOptionWithSkipRetry(),
			)
		}
		trialAudio = base64.StdEncoding.EncodeToString(decoded)
	}

	jsonResponse, err := common.Marshal(MiniMaxVoiceDesignResult{
		VoiceID:       designResponse.VoiceID,
		TrialAudioB64: trialAudio,
		AudioFormat:   minimaxVoiceDesignAudioFormat,
	})
	if err != nil {
		return &dto.Usage{}, types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError, types.ErrOptionWithSkipRetry())
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(http.StatusOK)
	if _, err := c.Writer.Write(jsonResponse); err != nil {
		return &dto.Usage{}, types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError, types.ErrOptionWithSkipRetry())
	}

	return minimaxMediaUsage(info, designResponse.ExtraInfo.UsageCharacters), nil
}

// handleMusicResponse 处理上游 /v1/music_generation 的响应：音频解码后按 output_format 给 Content-Type。
func handleMusicResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return &dto.Usage{}, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to read minimax music response: %w", readErr),
			types.ErrorCodeReadResponseBodyFailed,
			http.StatusInternalServerError,
			types.ErrOptionWithSkipRetry(),
		)
	}
	service.CloseResponseBodyGracefully(resp)

	var musicResponse MiniMaxMusicResponse
	if unmarshalErr := common.Unmarshal(body, &musicResponse); unmarshalErr != nil {
		return &dto.Usage{}, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to unmarshal minimax music response: %w", unmarshalErr),
			types.ErrorCodeBadResponseBody,
			http.StatusInternalServerError,
			types.ErrOptionWithSkipRetry(),
		)
	}
	if musicResponse.BaseResp.StatusCode != 0 {
		return &dto.Usage{}, minimaxMediaUpstreamError("music generation", musicResponse.BaseResp)
	}

	audio := musicResponse.Data.Audio
	if audio == "" {
		return &dto.Usage{}, types.NewErrorWithStatusCode(
			errors.New("no audio data in minimax music response"),
			types.ErrorCodeBadResponse,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

	// 声明本次请求发给上游的 output_format，载荷认不出来时用它兜底
	outputFormat := c.GetString("response_format")
	if outputFormat == "" {
		outputFormat = minimaxMusicDefaultOutputFormat
	}

	if strings.HasPrefix(audio, "http") {
		c.Redirect(http.StatusFound, audio)
	} else {
		audioData, decodeErr := decodeMiniMaxMusicAudio(audio)
		if decodeErr != nil {
			// 上游载荷既不是 hex 也不是 base64，不能把原始文本当音频字节回传
			return &dto.Usage{}, types.NewErrorWithStatusCode(
				fmt.Errorf("failed to decode minimax music audio: %w", decodeErr),
				types.ErrorCodeBadResponse,
				http.StatusBadGateway,
				types.ErrOptionWithStatusCode(http.StatusBadGateway),
				types.ErrOptionWithSkipRetry(),
			)
		}
		c.Data(http.StatusOK, minimaxMusicContentType(audioData, outputFormat), audioData)
	}

	return minimaxMediaUsage(info, musicResponse.ExtraInfo.UsageCharacters), nil
}

// decodeMiniMaxMusicAudio 解码上游音乐载荷：上游契约是 hex，
// 客户端显式要了别的编码时再按 base64 试一次，两者都不成就是非法载荷。
func decodeMiniMaxMusicAudio(audio string) ([]byte, error) {
	decoded, err := hex.DecodeString(audio)
	if err == nil {
		return decoded, nil
	}
	decoded, err = base64.StdEncoding.DecodeString(audio)
	if err == nil {
		return decoded, nil
	}
	return base64.RawStdEncoding.DecodeString(audio)
}

// minimaxMusicContentType 按解码后音频的实际编码决定 Content-Type，
// 认不出来的载荷退回本次请求声明的格式。
func minimaxMusicContentType(audio []byte, outputFormat string) string {
	switch {
	case bytes.HasPrefix(audio, []byte("RIFF")):
		return getContentTypeByFormat("wav")
	case bytes.HasPrefix(audio, []byte("fLaC")):
		return getContentTypeByFormat("flac")
	case bytes.HasPrefix(audio, []byte("ID3")):
		return getContentTypeByFormat("mp3")
	case len(audio) > 1 && audio[0] == 0xFF && (audio[1] == 0xFB || audio[1] == 0xF3 || audio[1] == 0xF2):
		return getContentTypeByFormat("mp3")
	case len(audio) > 1 && audio[0] == 0xFF && (audio[1] == 0xF1 || audio[1] == 0xF9):
		return getContentTypeByFormat("aac")
	}
	return getContentTypeByFormat(outputFormat)
}

// minimaxMediaUsageCharacters 把上游给的字符数换算成 token 数：
// 负数没有意义先归零，再走 common 的饱和换算，不做裸 int 转换。
func minimaxMediaUsageCharacters(characters int64) int {
	if characters <= 0 {
		return 0
	}
	return common.QuotaFromFloat(float64(characters))
}

// minimaxMediaUsage 构造媒体端点的 usage：上游的字符数是音频量，
// 必须同时写进 CompletionTokens 与 AudioTokens ——
// 文本结算路径只算 PromptTokens + CompletionTokens，只放 TotalTokens 字符数就不计费。
func minimaxMediaUsage(info *relaycommon.RelayInfo, characters int64) *dto.Usage {
	audioTokens := minimaxMediaUsageCharacters(characters)
	usage := &dto.Usage{
		PromptTokens:     info.GetEstimatePromptTokens(),
		CompletionTokens: audioTokens,
	}
	usage.PromptTokensDetails.TextTokens = usage.PromptTokens
	usage.CompletionTokenDetails.AudioTokens = audioTokens
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage
}

// minimaxMediaUpstreamError 把上游 base_resp 里的错误翻成渠道错误。
func minimaxMediaUpstreamError(endpoint string, baseResp MiniMaxBaseResp) *types.NewAPIError {
	return types.NewErrorWithStatusCode(
		fmt.Errorf("minimax %s error: %d - %s", endpoint, baseResp.StatusCode, baseResp.StatusMsg),
		types.ErrorCodeBadResponse,
		http.StatusBadRequest,
		types.ErrOptionWithSkipRetry(),
	)
}

// minimaxMediaInvalidRequest 造一个渠道侧的 400：请求本身不合法，重投到下一个渠道也不会变好。
func minimaxMediaInvalidRequest(format string, args ...any) error {
	return types.NewErrorWithStatusCode(
		fmt.Errorf(format, args...),
		types.ErrorCodeInvalidRequest,
		http.StatusBadRequest,
		types.ErrOptionWithStatusCode(http.StatusBadRequest),
		types.ErrOptionWithSkipRetry(),
	)
}
