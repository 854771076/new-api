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
	// 上游音乐默认以 hex 返回音频，网关解码后按 mp3 交付
	minimaxDefaultOutputFormat = "mp3"
)

// MiniMaxVoiceCloneRequest 是 POST /v1/voice_clone 的请求体。
// file_id 与 voice_id 必填，voice_id 由调用方生成并传入 —— 上游克隆响应不回传它。
type MiniMaxVoiceCloneRequest struct {
	FileID                  int64               `json:"file_id"`
	VoiceID                 string              `json:"voice_id"`
	ClonePrompt             *MiniMaxClonePrompt `json:"clone_prompt,omitempty"`
	Text                    string              `json:"text,omitempty"`
	Model                   string              `json:"model,omitempty"`
	LanguageBoost           string              `json:"language_boost,omitempty"`
	TextValidation          string              `json:"text_validation,omitempty"`
	Accuracy                *float64            `json:"accuracy,omitempty"`
	NeedNoiseReduction      *bool               `json:"need_noise_reduction,omitempty"`
	NeedVolumeNormalization *bool               `json:"need_volume_normalization,omitempty"`
	AigcWatermark           *bool               `json:"aigc_watermark,omitempty"`
}

// MiniMaxClonePrompt 是克隆的可选补充提示，不能替代 file_id。
type MiniMaxClonePrompt struct {
	PromptAudio string `json:"prompt_audio,omitempty"`
	PromptText  string `json:"prompt_text,omitempty"`
}

// MiniMaxVoiceCloneResponse 是 POST /v1/voice_clone 的响应体。
type MiniMaxVoiceCloneResponse struct {
	VoiceID        string                          `json:"voice_id"`
	InputSensitive MiniMaxVoiceCloneInputSensitive `json:"input_sensitive"`
	ExtraInfo      MiniMaxVoiceCloneExtraInfo      `json:"extra_info"`
	BaseResp       MiniMaxBaseResp                 `json:"base_resp"`
}

type MiniMaxVoiceCloneInputSensitive struct {
	Type      string `json:"type"`
	DemoAudio string `json:"demo_audio"`
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
	PromptAudio string `json:"prompt_audio"`
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
		return nil, errors.New("unsupported audio relay mode")
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
	// 客户端的显式字段优先于 metadata 里的同名字段
	cloneRequest.Model = model
	cloneRequest.Text = request.Input
	cloneRequest.VoiceID = request.Voice
	// prompt_audio / prompt_text 也可以平铺在 metadata 顶层，这里折回 clone_prompt
	if cloneRequest.ClonePrompt == nil && (metadata.PromptAudio != "" || metadata.PromptText != "") {
		cloneRequest.ClonePrompt = &MiniMaxClonePrompt{
			PromptAudio: metadata.PromptAudio,
			PromptText:  metadata.PromptText,
		}
	}

	if cloneRequest.FileID <= 0 {
		return nil, minimaxMediaInvalidRequest("metadata.file_id is required")
	}
	if cloneRequest.VoiceID == "" {
		return nil, minimaxMediaInvalidRequest("voice is required: the caller generates and passes the voice id")
	}
	if len(cloneRequest.Text) > minimaxVoiceCloneMaxText {
		return nil, minimaxMediaInvalidRequest("input must be at most %d characters", minimaxVoiceCloneMaxText)
	}
	if len(cloneRequest.TextValidation) > minimaxVoiceCloneMaxTextValidation {
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
	// 客户端的显式字段优先于 metadata 里的同名字段
	designRequest.Prompt = request.Input
	designRequest.VoiceID = request.Voice

	if designRequest.Prompt == "" {
		return nil, minimaxMediaInvalidRequest("input is required")
	}
	if designRequest.PreviewText == "" {
		return nil, minimaxMediaInvalidRequest("metadata.preview_text is required")
	}
	if len(designRequest.PreviewText) > minimaxVoiceDesignMaxPreviewText {
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
	// 客户端的显式字段优先于 metadata 里的同名字段
	musicRequest.Model = model
	musicRequest.Prompt = request.Input
	// output_format 只取 metadata 里显式给出的值，没给就按 mp3 处理
	if musicRequest.OutputFormat == "" {
		musicRequest.OutputFormat = minimaxDefaultOutputFormat
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
		)
	}
	service.CloseResponseBodyGracefully(resp)

	var cloneResponse MiniMaxVoiceCloneResponse
	if unmarshalErr := common.Unmarshal(body, &cloneResponse); unmarshalErr != nil {
		return &dto.Usage{}, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to unmarshal minimax voice clone response: %w", unmarshalErr),
			types.ErrorCodeBadResponseBody,
			http.StatusInternalServerError,
		)
	}
	if cloneResponse.BaseResp.StatusCode != 0 {
		return &dto.Usage{}, minimaxMediaUpstreamError("voice clone", cloneResponse.BaseResp)
	}

	// 上游克隆响应不含 voice_id，回写调用方自己生成的那个
	cloneResponse.VoiceID = c.GetString("voice_id")

	jsonResponse, err := common.Marshal(cloneResponse)
	if err != nil {
		return &dto.Usage{}, types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(http.StatusOK)
	if _, err := c.Writer.Write(jsonResponse); err != nil {
		return &dto.Usage{}, types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	return &dto.Usage{
		PromptTokens:     info.GetEstimatePromptTokens(),
		CompletionTokens: 0,
		TotalTokens:      minimaxMediaUsageCharacters(cloneResponse.ExtraInfo.UsageCharacters),
	}, nil
}

// handleVoiceDesignResponse 处理上游 /v1/voice_design 的响应。
func handleVoiceDesignResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return &dto.Usage{}, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to read minimax voice design response: %w", readErr),
			types.ErrorCodeReadResponseBodyFailed,
			http.StatusInternalServerError,
		)
	}
	service.CloseResponseBodyGracefully(resp)

	var designResponse MiniMaxVoiceDesignResponse
	if unmarshalErr := common.Unmarshal(body, &designResponse); unmarshalErr != nil {
		return &dto.Usage{}, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to unmarshal minimax voice design response: %w", unmarshalErr),
			types.ErrorCodeBadResponseBody,
			http.StatusInternalServerError,
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
			)
		}
		trialAudio = base64.StdEncoding.EncodeToString(decoded)
	}

	jsonResponse, err := common.Marshal(MiniMaxVoiceDesignResult{
		VoiceID:       designResponse.VoiceID,
		TrialAudioB64: trialAudio,
		AudioFormat:   minimaxDefaultOutputFormat,
	})
	if err != nil {
		return &dto.Usage{}, types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(http.StatusOK)
	if _, err := c.Writer.Write(jsonResponse); err != nil {
		return &dto.Usage{}, types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	return &dto.Usage{
		PromptTokens:     info.GetEstimatePromptTokens(),
		CompletionTokens: 0,
		TotalTokens:      minimaxMediaUsageCharacters(designResponse.ExtraInfo.UsageCharacters),
	}, nil
}

// handleMusicResponse 处理上游 /v1/music_generation 的响应：音频解码后按 output_format 给 Content-Type。
func handleMusicResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return &dto.Usage{}, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to read minimax music response: %w", readErr),
			types.ErrorCodeReadResponseBodyFailed,
			http.StatusInternalServerError,
		)
	}
	service.CloseResponseBodyGracefully(resp)

	var musicResponse MiniMaxMusicResponse
	if unmarshalErr := common.Unmarshal(body, &musicResponse); unmarshalErr != nil {
		return &dto.Usage{}, types.NewErrorWithStatusCode(
			fmt.Errorf("failed to unmarshal minimax music response: %w", unmarshalErr),
			types.ErrorCodeBadResponseBody,
			http.StatusInternalServerError,
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
		)
	}

	// Content-Type 按本次请求实际发给上游的 output_format 走
	outputFormat := c.GetString("response_format")
	if outputFormat == "" {
		outputFormat = minimaxDefaultOutputFormat
	}

	if strings.HasPrefix(audio, "http") {
		c.Redirect(http.StatusFound, audio)
	} else {
		audioData, decodeErr := hex.DecodeString(audio)
		if decodeErr != nil {
			// 上游契约是 hex；客户端显式要了非 hex 载荷时上游直接给音频字节
			audioData = []byte(audio)
		}
		c.Data(http.StatusOK, getContentTypeByFormat(outputFormat), audioData)
	}

	return &dto.Usage{
		PromptTokens:     info.GetEstimatePromptTokens(),
		CompletionTokens: 0,
		TotalTokens:      minimaxMediaUsageCharacters(musicResponse.ExtraInfo.UsageCharacters),
	}, nil
}

// minimaxMediaUsageCharacters 把上游给的字符数换算成 token 数：
// 负数没有意义先归零，再走 common 的饱和换算，不做裸 int 转换。
func minimaxMediaUsageCharacters(characters int64) int {
	if characters <= 0 {
		return 0
	}
	return common.QuotaFromFloat(float64(characters))
}

// minimaxMediaUpstreamError 把上游 base_resp 里的错误翻成渠道错误。
func minimaxMediaUpstreamError(endpoint string, baseResp MiniMaxBaseResp) *types.NewAPIError {
	return types.NewErrorWithStatusCode(
		fmt.Errorf("minimax %s error: %d - %s", endpoint, baseResp.StatusCode, baseResp.StatusMsg),
		types.ErrorCodeBadResponse,
		http.StatusBadRequest,
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
