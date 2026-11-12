package minimax

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

type MiniMaxImageRequest struct {
	Model           string `json:"model"`
	Prompt          string `json:"prompt"`
	AspectRatio     string `json:"aspect_ratio,omitempty"`
	ResponseFormat  string `json:"response_format,omitempty"`
	N               int    `json:"n,omitempty"`
	PromptOptimizer *bool  `json:"prompt_optimizer,omitempty"`
	AigcWatermark   *bool  `json:"aigc_watermark,omitempty"`
}

type MiniMaxImageResponse struct {
	ID   string `json:"id"`
	Data struct {
		ImageURLs   []string `json:"image_urls"`
		ImageBase64 []string `json:"image_base64"`
	} `json:"data"`
	Metadata map[string]any `json:"metadata"`
	BaseResp struct {
		StatusCode int    `json:"status_code"`
		StatusMsg  string `json:"status_msg"`
	} `json:"base_resp"`
}

// MiniMax 有两个图像端点，形状不同：旧端点 /v1/image_generation 的响应是
// data.image_urls[] 与 data.image_base64[] 两个数组，新端点 /v1/images/generations
// 的响应是 data[] 数组，每项带 url 或 b64_json。
const (
	// 新端点单次最多生成 10 张图
	minimaxH3ImageMaxN = 10
	// 新端点的默认分辨率档位
	minimaxH3ImageArea1K = "1K"
)

// MiniMaxH3ImageRequest 是新端点 POST /v1/images/generations 的请求体。
type MiniMaxH3ImageRequest struct {
	Model                 string `json:"model"`
	Prompt                string `json:"prompt"`
	N                     int    `json:"n"`
	Size                  string `json:"size,omitempty"`
	AspectRatio           string `json:"aspect_ratio,omitempty"`
	Area                  string `json:"area,omitempty"`
	OutputFormat          string `json:"output_format,omitempty"`
	ResponseFormat        string `json:"response_format,omitempty"`
	DisablePromptEnhanced *bool  `json:"disable_prompt_enhanced,omitempty"`
}

// MiniMaxH3ImageResponse 是新端点的响应体。
type MiniMaxH3ImageResponse struct {
	Data     []MiniMaxH3ImageData `json:"data"`
	Error    *MiniMaxH3ImageError `json:"error"`
	BaseResp struct {
		StatusCode int    `json:"status_code"`
		StatusMsg  string `json:"status_msg"`
	} `json:"base_resp"`
}

type MiniMaxH3ImageData struct {
	URL           string               `json:"url"`
	B64Json       string               `json:"b64_json"`
	Size          string               `json:"size"`
	RevisedPrompt string               `json:"revised_prompt"`
	Error         *MiniMaxH3ImageError `json:"error"`
}

type MiniMaxH3ImageError struct {
	Code    json.RawMessage `json:"code"`
	Message string          `json:"message"`
}

func oaiImage2MiniMaxImageRequest(request dto.ImageRequest) MiniMaxImageRequest {
	responseFormat := normalizeMiniMaxResponseFormat(request.ResponseFormat)
	minimaxRequest := MiniMaxImageRequest{
		Model:          request.Model,
		Prompt:         request.Prompt,
		ResponseFormat: responseFormat,
		N:              1,
		AigcWatermark:  request.Watermark,
	}

	if request.Model == "" {
		minimaxRequest.Model = "image-01"
	}
	if request.N != nil && *request.N > 0 {
		minimaxRequest.N = int(*request.N)
	}
	if aspectRatio := aspectRatioFromImageRequest(request); aspectRatio != "" {
		minimaxRequest.AspectRatio = aspectRatio
	}
	if raw, ok := request.Extra["prompt_optimizer"]; ok {
		var promptOptimizer bool
		if err := common.Unmarshal(raw, &promptOptimizer); err == nil {
			minimaxRequest.PromptOptimizer = &promptOptimizer
		}
	}

	return minimaxRequest
}

// isMiniMaxH3ImageModel 判断上游模型是否走新端点 /v1/images/generations。
func isMiniMaxH3ImageModel(model string) bool {
	switch model {
	case "MiniMax-H3-Image", "MiniMax-H3-Image-Highspeed":
		return true
	default:
		return false
	}
}

// oaiImage2MiniMaxH3ImageRequest 把 OpenAI 形状的图像请求翻译成新端点的请求体。
// 新端点只接受 n 1-10，越界以 400 拒绝，不静默截断：截断会让计费乘数与真实出图数不一致。
func oaiImage2MiniMaxH3ImageRequest(request dto.ImageRequest, model string) (MiniMaxH3ImageRequest, error) {
	n := 1
	if request.N != nil && *request.N > 0 {
		n = int(*request.N)
	}
	if n > minimaxH3ImageMaxN {
		return MiniMaxH3ImageRequest{}, minimaxImageInvalidRequest(
			"minimax image generation supports at most %d images per request, got %d", minimaxH3ImageMaxN, n)
	}

	minimaxRequest := MiniMaxH3ImageRequest{
		Model:  model,
		Prompt: request.Prompt,
		N:      n,
		Size:   request.Size,
	}
	if aspectRatio := aspectRatioFromImageRequest(request); aspectRatio != "" {
		minimaxRequest.AspectRatio = aspectRatio
	}

	area, err := minimaxH3ImageArea(request)
	if err != nil {
		return MiniMaxH3ImageRequest{}, err
	}
	minimaxRequest.Area = area

	if len(request.OutputFormat) > 0 {
		var outputFormat string
		if err := common.Unmarshal(request.OutputFormat, &outputFormat); err != nil {
			return MiniMaxH3ImageRequest{}, minimaxImageInvalidRequest("minimax image output_format must be a string")
		}
		minimaxRequest.OutputFormat = outputFormat
	}

	// 新端点的 response_format 词汇是 url / b64_json，与旧端点的 base64 不同。
	// 白名单外的值（例如沿用旧词汇的 base64）直接以 400 拒绝，不静默发往上游再失败
	switch request.ResponseFormat {
	case "":
		// 客户端没指定就不下发该字段，由上游按自己的默认出图
	case "url", "b64_json":
		minimaxRequest.ResponseFormat = request.ResponseFormat
	default:
		return MiniMaxH3ImageRequest{}, minimaxImageInvalidRequest(
			"minimax image response_format must be one of url, b64_json, got %q", request.ResponseFormat)
	}

	// disable_prompt_enhanced 与旧端点的 prompt_optimizer 语义相反，不做取反推导
	if raw, ok := request.Extra["disable_prompt_enhanced"]; ok {
		var disablePromptEnhanced bool
		if err := common.Unmarshal(raw, &disablePromptEnhanced); err != nil {
			return MiniMaxH3ImageRequest{}, minimaxImageInvalidRequest("minimax image disable_prompt_enhanced must be a boolean")
		}
		minimaxRequest.DisablePromptEnhanced = &disablePromptEnhanced
	}

	return minimaxRequest, nil
}

// minimaxH3ImageArea 取新端点的分辨率档位：只收 1K / 2K / 4K，缺省 1K。
func minimaxH3ImageArea(request dto.ImageRequest) (string, error) {
	raw, ok := request.Extra["area"]
	if !ok {
		return minimaxH3ImageArea1K, nil
	}
	var area string
	if err := common.Unmarshal(raw, &area); err != nil {
		return "", minimaxImageInvalidRequest("minimax image area must be a string")
	}
	switch strings.ToUpper(strings.TrimSpace(area)) {
	case "1K":
		return "1K", nil
	case "2K":
		return "2K", nil
	case "4K":
		return "4K", nil
	default:
		return "", minimaxImageInvalidRequest("minimax image area must be one of 1K, 2K, 4K, got %q", area)
	}
}

// minimaxImageInvalidRequest 造一个渠道侧的 400：请求本身越界，重投到下一个渠道也不会变好。
func minimaxImageInvalidRequest(format string, args ...any) error {
	return types.NewErrorWithStatusCode(
		fmt.Errorf(format, args...),
		types.ErrorCodeInvalidRequest,
		http.StatusBadRequest,
		types.ErrOptionWithStatusCode(http.StatusBadRequest),
		types.ErrOptionWithSkipRetry(),
	)
}

func aspectRatioFromImageRequest(request dto.ImageRequest) string {
	if raw, ok := request.Extra["aspect_ratio"]; ok {
		var aspectRatio string
		if err := common.Unmarshal(raw, &aspectRatio); err == nil && aspectRatio != "" {
			return aspectRatio
		}
	}

	switch request.Size {
	case "1024x1024":
		return "1:1"
	case "1792x1024":
		return "16:9"
	case "1024x1792":
		return "9:16"
	case "1536x1024", "1248x832":
		return "3:2"
	case "1024x1536", "832x1248":
		return "2:3"
	case "1152x864":
		return "4:3"
	case "864x1152":
		return "3:4"
	case "1344x576":
		return "21:9"
	}

	width, height, ok := parseImageSize(request.Size)
	if !ok {
		return ""
	}
	ratio := reduceAspectRatio(width, height)
	switch ratio {
	case "1:1", "16:9", "4:3", "3:2", "2:3", "3:4", "9:16", "21:9":
		return ratio
	default:
		return ""
	}
}

func parseImageSize(size string) (int, int, bool) {
	parts := strings.Split(size, "x")
	if len(parts) != 2 {
		return 0, 0, false
	}
	width, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	height, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	if width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func reduceAspectRatio(width, height int) string {
	divisor := gcd(width, height)
	return fmt.Sprintf("%d:%d", width/divisor, height/divisor)
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	if a == 0 {
		return 1
	}
	return a
}

func normalizeMiniMaxResponseFormat(responseFormat string) string {
	switch strings.ToLower(responseFormat) {
	case "", "url":
		return "url"
	case "b64_json", "base64":
		return "base64"
	default:
		return responseFormat
	}
}

func responseMiniMax2OpenAIImage(response *MiniMaxImageResponse, info *relaycommon.RelayInfo) (*dto.ImageResponse, error) {
	imageResponse := &dto.ImageResponse{
		Created: info.StartTime.Unix(),
	}

	for _, imageURL := range response.Data.ImageURLs {
		imageResponse.Data = append(imageResponse.Data, dto.ImageData{Url: imageURL})
	}
	for _, imageBase64 := range response.Data.ImageBase64 {
		imageResponse.Data = append(imageResponse.Data, dto.ImageData{B64Json: imageBase64})
	}
	if len(response.Metadata) > 0 {
		metadata, err := common.Marshal(response.Metadata)
		if err != nil {
			return nil, err
		}
		imageResponse.Metadata = metadata
	}

	return imageResponse, nil
}

// responseMiniMaxH3Image2OpenAIImage 把新端点的响应翻译成 OpenAI 形状的图像产物。
func responseMiniMaxH3Image2OpenAIImage(responseBody []byte, info *relaycommon.RelayInfo) (*dto.ImageResponse, *types.NewAPIError) {
	var minimaxResponse MiniMaxH3ImageResponse
	if err := common.Unmarshal(responseBody, &minimaxResponse); err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	if minimaxResponse.Error != nil && minimaxResponse.Error.Message != "" {
		return nil, minimaxH3ImageUpstreamError(minimaxResponse.Error.Message, minimaxResponse.Error.Code)
	}
	if minimaxResponse.BaseResp.StatusCode != 0 {
		return nil, minimaxH3ImageUpstreamError(minimaxResponse.BaseResp.StatusMsg, nil)
	}

	imageResponse := &dto.ImageResponse{
		Created: info.StartTime.Unix(),
	}
	for _, item := range minimaxResponse.Data {
		if item.Error != nil && item.Error.Message != "" {
			return nil, minimaxH3ImageUpstreamError(item.Error.Message, item.Error.Code)
		}
		imageResponse.Data = append(imageResponse.Data, dto.ImageData{
			Url:           item.URL,
			B64Json:       item.B64Json,
			RevisedPrompt: item.RevisedPrompt,
		})
	}

	return imageResponse, nil
}

// minimaxH3ImageUpstreamError 造新端点的上游错误。
// 新端点非幂等（上游可等待 600 秒），网关在通道错误时会把同一 POST 重投到下一个渠道，
// 会造成上游重复生成与重复计费，所以带 skip-retry；
// 错误码不能用 channel: 前缀 —— DecideRelayRetry 先判 channel: 判成 retry，再看 skip-retry。
func minimaxH3ImageUpstreamError(message string, code json.RawMessage) *types.NewAPIError {
	if message == "" {
		message = "minimax image generation failed"
	}
	errorCode := strings.Trim(strings.TrimSpace(string(code)), `"`)
	if errorCode == "" || errorCode == "null" {
		errorCode = "minimax_image_error"
	}
	return types.WithOpenAIError(types.OpenAIError{
		Message: message,
		Type:    "minimax_image_error",
		Code:    errorCode,
	}, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
}

// miniMaxImageCount counts billable images from the translated OpenAI-shaped
// payload: the larger of the url-bearing and b64_json-bearing entry counts, so
// one image split across both representations bills once and payload-less
// entries bill nothing. Same rule as openaiImageResponseCount.
func miniMaxImageCount(data []dto.ImageData) int64 {
	var urls, b64s int64
	for _, item := range data {
		if item.Url != "" {
			urls++
		}
		if item.B64Json != "" {
			b64s++
		}
	}
	return max(urls, b64s)
}

// minimaxUpstreamModelName resolves the upstream model name: the channel mapped
// name first, the origin model name as fallback. The caller that owns the
// resolved name writes it back to info.UpstreamModelName so the request URL and
// the response parser read the same value.
func minimaxUpstreamModelName(info *relaycommon.RelayInfo) string {
	if info == nil {
		return ""
	}
	if model := info.GetUpstreamModelName(); model != "" {
		return model
	}
	return info.GetOriginModelName()
}

func miniMaxImageHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	// 渠道做过模型映射时，上游请求体里的 model 是映射后的上游名
	if isMiniMaxH3ImageModel(minimaxUpstreamModelName(info)) {
		return miniMaxH3ImageHandler(c, resp, info)
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	service.CloseResponseBodyGracefully(resp)

	var minimaxResponse MiniMaxImageResponse
	if err := common.Unmarshal(responseBody, &minimaxResponse); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if minimaxResponse.BaseResp.StatusCode != 0 {
		return nil, types.WithOpenAIError(types.OpenAIError{
			Message: minimaxResponse.BaseResp.StatusMsg,
			Type:    "minimax_image_error",
			Code:    fmt.Sprintf("%d", minimaxResponse.BaseResp.StatusCode),
		}, resp.StatusCode)
	}

	openAIResponse, err := responseMiniMax2OpenAIImage(&minimaxResponse, info)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	// 计费张数按翻译后的 OpenAI 形状载荷推导：旧端点的 url 与 base64 两个数组并进同一个数组，
	// 同一张图可能在两个数组里各出现一次，取两者的较大值只算一张
	info.UpdateImageCount(miniMaxImageCount(openAIResponse.Data))
	jsonResponse, err := common.Marshal(openAIResponse)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}

	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	if _, err := c.Writer.Write(jsonResponse); err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}

	return &dto.Usage{}, nil
}

// miniMaxH3ImageHandler 处理新端点 /v1/images/generations 的响应。
func miniMaxH3ImageHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	service.CloseResponseBodyGracefully(resp)

	openAIResponse, apiErr := responseMiniMaxH3Image2OpenAIImage(responseBody, info)
	if apiErr != nil {
		return nil, apiErr
	}
	// 计费张数按翻译后的 OpenAI 形状载荷推导：上游 data[] 一项一张图，
	// 同一项同时给 url 与 b64_json 是同一张图的两种表示，只算一张
	info.UpdateImageCount(miniMaxImageCount(openAIResponse.Data))
	jsonResponse, err := common.Marshal(openAIResponse)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}

	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	if _, err := c.Writer.Write(jsonResponse); err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}

	return &dto.Usage{}, nil
}
