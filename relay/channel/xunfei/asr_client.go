package xunfei

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
)

// 讯飞 IFASR（录音文件转写大模型）上传与轮询编排。
//
// 讯飞的两段式交互：先上传音频原始字节换取订单号，再按固定间隔查询订单直到转写完成或失败。
// 客户端只感知一次请求的最终结果，轮询在服务端内联完成。
//
// 官方接口文档：https://www.xfyun.cn/doc/spark/asr_llm/Ifasr_llm.html
//
// 注意：讯飞 IFASR 的主机与签名算法都不同于星火对话（星火用 wss://spark-api.xf-yun.com 与
// HMAC-SHA256），此处不得复用星火路径的地址与鉴权实现。

const (
	// ASRDefaultBaseURL 讯飞 IFASR 官方主机地址。
	//
	// 该地址是新增常量：仓库内讯飞渠道的 BaseURL 配置并未被其上游路径读取（星火对话的主机
	// 硬编码在星火路径上），BaseURL 覆盖只作用于 IFASR 转写路径，不得破坏星火路由。
	ASRDefaultBaseURL = "https://office-api-ist-dx.iflyaisol.com"

	// ASRUploadPath 讯飞 IFASR 音频上传路径。
	ASRUploadPath = "/v2/upload"

	// ASRResultPath 讯飞 IFASR 转写结果查询路径。
	ASRResultPath = "/v2/getResult"

	// ASRDefaultPollInterval 默认轮询间隔。
	ASRDefaultPollInterval = 5 * time.Second

	// ASRDefaultMaxWait 默认最大等待时长，约束轮询阶段占用的连接时间。
	ASRDefaultMaxWait = time.Hour

	// ASRDefaultLanguage 默认上传语种。
	ASRDefaultLanguage = "cn"

	// asrSuccessCode 讯飞业务成功码。
	asrSuccessCode = "000000"

	// asrMaxResponseBytes 上游响应体读取上限，防止异常响应打爆内存。
	asrMaxResponseBytes = 32 << 20

	// asrMaxAudioBytes 上游单次上传的音频大小上限（500MB）。
	asrMaxAudioBytes = 500 << 20

	// asrMaxAudioDuration 上游单次转写的音频时长上限（5 小时）。
	asrMaxAudioDuration = 5 * time.Hour
)

// asrSupportedAudioFormats 是上游支持的音频格式，取值见讯飞 IFASR 接口文档。
var asrSupportedAudioFormats = []string{"mp3", "wav", "pcm", "opus", "flac", "ogg", "speex"}

// ASRCredentials 是讯飞 IFASR 所需的三项凭据。
//
// APIKey 即讯飞的 accessKeyId，APISecret 即 accessKeySecret；三者来自渠道密钥的三段切分
// （分隔符 |，顺序 appId / apiSecret / apiKey）。
type ASRCredentials struct {
	// AppID 讯飞平台分配的应用 ID。
	AppID string
	// APISecret 讯飞平台分配的访问密钥密钥，参与签名计算。
	APISecret string
	// APIKey 讯飞平台分配的访问密钥 ID（accessKeyId），同时作为签名身份标识。
	APIKey string
}

// ASRAudio 是待上传的音频。
type ASRAudio struct {
	// Data 音频原始字节。
	Data []byte
	// FileName 带扩展名的文件名，上游据此识别音频格式。
	FileName string
	// Format 音频格式，小写且不含点号，如 wav / mp3。
	Format string
	// DurationMs 音频时长，单位毫秒。
	DurationMs int64
}

// ASROptions 是可调参数。
type ASROptions struct {
	// PollInterval 轮询间隔，零值或负值时取默认 5 秒。
	PollInterval time.Duration
	// MaxWait 轮询阶段的最大等待时长，超时返回明确错误；零值或负值时取默认值。
	MaxWait time.Duration
	// Language 上传时的语种参数，空值时取默认 cn。
	Language string
	// ResultType 查询时的结果类型，零值时取默认 transfer。
	ResultType ASRResultType
	// BaseURL 上游主机地址，空值时取讯飞官方默认主机；覆盖只作用于 IFASR 转写路径。
	BaseURL string
}

// asrNow 取当前时间，用于生成请求的 dateTime。测试会替换该变量，
// 以确定性地验证「上传与查询各自重新生成 dateTime」。
var asrNow = time.Now

// ASRTranscribe 完成上传→轮询→解析的全流程，返回解析后的转写结果。
//
// 上传与查询共用同一个 signatureRandom，但各自的 dateTime 重新生成；查询接口的 signature
// 走请求头，不进查询串。轮询响应客户端请求上下文的取消，超过最大等待时长返回明确错误。
func ASRTranscribe(ctx context.Context, creds ASRCredentials, audio ASRAudio, opts ASROptions) (*ASRResult, error) {
	if strings.TrimSpace(creds.AppID) == "" || strings.TrimSpace(creds.APISecret) == "" || strings.TrimSpace(creds.APIKey) == "" {
		return nil, &ASRLocalError{
			Category: asrCategoryCredential,
			Reason:   "讯飞转写凭据不完整：需要 appId、apiSecret、apiKey",
		}
	}
	if len(audio.Data) == 0 {
		return nil, &ASRLocalError{
			Category: asrCategoryClient,
			Reason:   "讯飞转写音频内容为空",
		}
	}
	if err := validateASRAudio(audio); err != nil {
		return nil, err
	}

	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = ASRDefaultBaseURL
	}
	pollInterval := opts.PollInterval
	if pollInterval <= 0 {
		pollInterval = ASRDefaultPollInterval
	}
	maxWait := opts.MaxWait
	if maxWait <= 0 {
		maxWait = ASRDefaultMaxWait
	}
	resultType := opts.ResultType
	if resultType == "" {
		resultType = ASRResultTypeTransfer
	}
	language := opts.Language
	if language == "" {
		language = ASRDefaultLanguage
	}

	// 上传与查询必须复用同一个 signatureRandom，dateTime 则各自重新生成。
	signatureRandom := XfyunASRSignatureRandom()

	upload, err := asrUploadAudio(ctx, baseURL, creds, audio, language, signatureRandom)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(maxWait)
	wait := asrFirstPollDelay(upload.TaskEstimateTimeMs, pollInterval)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, &ASRUpstreamError{
				ASRErrorInfo: ASRErrorInfo{
					Description: "等待转写结果超时：订单 " + upload.OrderId + " 在 " + maxWait.String() + " 内未完成",
					Stage:       asrStagePoll,
				},
			}
		}
		if wait > remaining {
			wait = remaining
		}
		if err := asrWait(ctx, wait); err != nil {
			return nil, &ASRUpstreamError{
				ASRErrorInfo: ASRErrorInfo{Description: "等待转写结果被中断", Stage: asrStagePoll},
				Cause:        err,
			}
		}

		result, err := asrQueryResult(ctx, baseURL, creds, upload.OrderId, resultType, signatureRandom)
		if err != nil {
			return nil, err
		}

		switch result.Order.Status {
		case ASROrderStatusCompleted:
			return result, nil
		case ASROrderStatusFailed:
			return nil, &ASRUpstreamError{
				ASRErrorInfo: ASRErrorInfo{
					Description: "订单 " + result.Order.OrderId + " 转写失败",
					FailType:    result.Order.FailType,
					Stage:       asrStagePoll,
				},
			}
		}
		// 0（已创建）与 3（处理中）不是终态，继续按固定间隔轮询。
		wait = pollInterval
	}
}

// asrUploadResult 是上传接口的响应要点。
type asrUploadResult struct {
	// OrderId 订单号，用于后续查询转写结果。
	OrderId string
	// TaskEstimateTimeMs 预估转写完成时长，单位毫秒。
	TaskEstimateTimeMs int64
}

// asrUploadEnvelope 讯飞 IFASR 上传接口的响应结构。
type asrUploadEnvelope struct {
	// Code 是上游业务码：成功是字符串 "000000"，官方失败示例里是数字，两种形态都兼容。
	Code     asrUpstreamValue `json:"code"`
	DescInfo string           `json:"descInfo"`
	Content  struct {
		OrderId          string `json:"orderId"`
		TaskEstimateTime int64  `json:"taskEstimateTime"`
	} `json:"content"`
}

// asrPollFailure 讯飞 IFASR 轮询响应的宽松解析：只取业务错误码、描述与订单状态。
//
// 上游成功响应的业务码是字符串 "000000"，官方失败示例里的业务码却是数字；完整解析只
// 接受字符串码，因此这里先用兼容两种形态的结构取出业务错误码。
type asrPollFailure struct {
	Code     asrUpstreamValue `json:"code"`
	DescInfo string           `json:"descInfo"`
	Content  struct {
		OrderInfo asrOrderInfoPayload `json:"orderInfo"`
	} `json:"content"`
}

// asrUploadAudio 上传音频原始字节，换取订单号与预估转写时长。
func asrUploadAudio(ctx context.Context, baseURL string, creds ASRCredentials, audio ASRAudio, language string, signatureRandom string) (*asrUploadResult, error) {
	params := url.Values{}
	params.Set("appId", creds.AppID)
	params.Set("accessKeyId", creds.APIKey)
	params.Set("dateTime", XfyunASRDateTime(asrNow()))
	params.Set("signatureRandom", signatureRandom)
	params.Set("fileSize", strconv.Itoa(len(audio.Data)))
	params.Set("fileName", asrUploadFileName(audio.FileName, audio.Format))
	params.Set("language", language)
	// 时长校验保持开启（不使用 durationCheckDisable 逃生开关），因此 duration 必传且单位为毫秒。
	params.Set("duration", strconv.FormatInt(audio.DurationMs, 10))

	requestURL, signature := XfyunASRBuildRequest(baseURL+ASRUploadPath, params, creds.APISecret)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(audio.Data))
	if err != nil {
		return nil, &ASRUpstreamError{
			ASRErrorInfo: ASRErrorInfo{Description: "上传请求构造失败", Stage: asrStageUpload},
			Cause:        err,
		}
	}
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set(XfyunASRSignatureHeader, signature)

	response, err := asrHTTPClient().Do(request)
	if err != nil {
		return nil, &ASRUpstreamError{
			ASRErrorInfo: ASRErrorInfo{Description: "上游请求失败（网络或传输故障）", Stage: asrStageUpload},
			Cause:        err,
		}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, asrMaxResponseBytes))
	if err != nil {
		return nil, &ASRUpstreamError{
			ASRErrorInfo: ASRErrorInfo{Description: "上游响应读取失败", Stage: asrStageUpload},
			Cause:        err,
		}
	}
	if response.StatusCode != http.StatusOK {
		code, description := asrUpstreamErrorBody(body)
		return nil, &ASRUpstreamError{
			ASRErrorInfo: ASRErrorInfo{UpstreamCode: code, Description: description, Stage: asrStageUpload},
			HTTPStatus:   response.StatusCode,
		}
	}

	var envelope asrUploadEnvelope
	if err := common.Unmarshal(body, &envelope); err != nil {
		return nil, &ASRUpstreamError{
			ASRErrorInfo: ASRErrorInfo{Description: "上游响应不是合法 JSON", Stage: asrStageUpload},
			Cause:        err,
		}
	}
	if code := string(envelope.Code); code != "" && code != asrSuccessCode {
		return nil, &ASRUpstreamError{
			ASRErrorInfo: ASRErrorInfo{UpstreamCode: code, Description: envelope.DescInfo, Stage: asrStageUpload},
		}
	}
	if envelope.Content.OrderId == "" {
		description := "上游未返回订单号"
		if envelope.DescInfo != "" {
			description += "：" + envelope.DescInfo
		}
		return nil, &ASRUpstreamError{
			ASRErrorInfo: ASRErrorInfo{Description: description, Stage: asrStageUpload},
		}
	}
	return &asrUploadResult{
		OrderId:            envelope.Content.OrderId,
		TaskEstimateTimeMs: envelope.Content.TaskEstimateTime,
	}, nil
}

// asrQueryResult 查询订单状态并解析上游响应。
func asrQueryResult(ctx context.Context, baseURL string, creds ASRCredentials, orderId string, resultType ASRResultType, signatureRandom string) (*ASRResult, error) {
	params := url.Values{}
	params.Set("accessKeyId", creds.APIKey)
	params.Set("appId", creds.AppID)
	params.Set("dateTime", XfyunASRDateTime(asrNow()))
	params.Set("signatureRandom", signatureRandom)
	params.Set("orderId", orderId)
	params.Set("resultType", string(resultType))

	requestURL, signature := XfyunASRBuildRequest(baseURL+ASRResultPath, params, creds.APISecret)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, strings.NewReader("{}"))
	if err != nil {
		return nil, &ASRUpstreamError{
			ASRErrorInfo: ASRErrorInfo{Description: "查询请求构造失败", Stage: asrStagePoll},
			Cause:        err,
		}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(XfyunASRSignatureHeader, signature)

	response, err := asrHTTPClient().Do(request)
	if err != nil {
		return nil, &ASRUpstreamError{
			ASRErrorInfo: ASRErrorInfo{Description: "上游请求失败（网络或传输故障）", Stage: asrStagePoll},
			Cause:        err,
		}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, asrMaxResponseBytes))
	if err != nil {
		return nil, &ASRUpstreamError{
			ASRErrorInfo: ASRErrorInfo{Description: "上游响应读取失败", Stage: asrStagePoll},
			Cause:        err,
		}
	}
	if response.StatusCode != http.StatusOK {
		code, description := asrUpstreamErrorBody(body)
		return nil, &ASRUpstreamError{
			ASRErrorInfo: ASRErrorInfo{UpstreamCode: code, Description: description, Stage: asrStagePoll},
			HTTPStatus:   response.StatusCode,
		}
	}

	// 上游以非成功码表示「订单未完成」时继续轮询，其余情况按失败处理。
	// 业务码先按宽松结构取出：官方失败示例里的业务码是数字，完整解析只接受字符串码。
	var failure asrPollFailure
	if err := common.Unmarshal(body, &failure); err != nil {
		return nil, &ASRUpstreamError{
			ASRErrorInfo: ASRErrorInfo{Description: "上游响应不是合法 JSON", Stage: asrStagePoll},
			Cause:        err,
		}
	}
	if code := string(failure.Code); code != "" && code != asrSuccessCode {
		order := ASROrderInfo{
			OrderId:  failure.Content.OrderInfo.OrderId,
			Status:   failure.Content.OrderInfo.Status,
			FailType: failure.Content.OrderInfo.FailType,
		}
		if order.OrderId != "" && (order.Status == ASROrderStatusCreated || order.Status == ASROrderStatusProcessing) {
			return &ASRResult{ResultType: resultType, Code: code, DescInfo: failure.DescInfo, Order: order}, nil
		}
		return nil, &ASRUpstreamError{
			ASRErrorInfo: ASRErrorInfo{UpstreamCode: code, Description: failure.DescInfo, Stage: asrStagePoll},
		}
	}

	result, err := ParseASRResult(body, resultType)
	if err != nil {
		return nil, &ASRUpstreamError{
			ASRErrorInfo: ASRErrorInfo{Description: "上游响应解析失败", Stage: asrStagePoll},
			Cause:        err,
		}
	}
	return result, nil
}

// asrUpstreamErrorBody 从上游失败响应体中提取业务错误码与描述。
//
// 上游通常以 HTTP 200 携带业务错误码，但网关或代理也可能直接返回 HTTP 错误；此时若
// 响应体仍是上游的业务结构就取出错误码，否则把响应体片段当作描述。
func asrUpstreamErrorBody(body []byte) (string, string) {
	var envelope asrUploadEnvelope
	if err := common.Unmarshal(body, &envelope); err == nil && envelope.Code != "" {
		return string(envelope.Code), envelope.DescInfo
	}

	const bodyLimit = 200
	text := strings.TrimSpace(string(body))
	if len(text) > bodyLimit {
		text = text[:bodyLimit] + "…"
	}
	return "", text
}

// validateASRAudio 校验音频是否满足上游的格式、大小与时长限制。
//
// 上游限制为音频大小不超过 500MB、时长不超过 5 小时、格式限定为
// mp3/wav/pcm/opus/flac/ogg/speex。违反限制的音频属于客户端请求有误，网关直接返回
// 4xx，不必等上游拒绝。格式缺失时交由上游按文件名判断。
func validateASRAudio(audio ASRAudio) error {
	format := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(audio.Format), "."))
	if format != "" && !slices.Contains(asrSupportedAudioFormats, format) {
		return &ASRLocalError{
			Category: asrCategoryClient,
			Reason:   "讯飞转写不支持该音频格式：" + format,
		}
	}
	if len(audio.Data) > asrMaxAudioBytes {
		return &ASRLocalError{
			Category: asrCategoryClient,
			Reason:   "讯飞转写音频大小超过上游限制（上限 500MB）",
		}
	}
	if audio.DurationMs > int64(asrMaxAudioDuration/time.Millisecond) {
		return &ASRLocalError{
			Category: asrCategoryClient,
			Reason:   "讯飞转写音频时长超过上游限制（上限 5 小时）",
		}
	}
	return nil
}

// asrFirstPollDelay 计算上传成功后的首次等待时长。
//
// 上游给出预估转写时长 taskEstimateTime（毫秒）。当预估超过 5 秒时，首次等待取
// min(预估秒数 × 0.3, 轮询间隔)——目的是不等满预估时长就尽早开始轮询，因此该值恒不超过轮询间隔；
// 其余情况按轮询间隔等待，随后按固定间隔继续。
func asrFirstPollDelay(estimateMs int64, pollInterval time.Duration) time.Duration {
	const estimateThresholdMs = 5000

	if pollInterval <= 0 {
		pollInterval = ASRDefaultPollInterval
	}
	if estimateMs <= estimateThresholdMs {
		return pollInterval
	}
	delay := time.Duration(float64(estimateMs) / 1000 * 0.3 * float64(time.Second))
	if delay > pollInterval {
		return pollInterval
	}
	if delay <= 0 {
		return pollInterval
	}
	return delay
}

// asrWait 等待指定时长；客户端上下文取消时立刻返回。
func asrWait(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// asrUploadFileName 补全上传文件名：上游按扩展名识别音频格式，缺后缀时按音频格式补齐。
func asrUploadFileName(fileName string, format string) string {
	name := strings.TrimSpace(fileName)
	if name == "" {
		name = "audio"
	}
	suffix := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(format)), ".")
	if suffix == "" {
		return name
	}
	if path.Ext(name) != "" {
		return name
	}
	return name + "." + suffix
}

// asrHTTPClient 返回访问讯飞上游的 HTTP 客户端。
func asrHTTPClient() *http.Client {
	if client := service.GetHttpClient(); client != nil {
		return client
	}
	return http.DefaultClient
}
