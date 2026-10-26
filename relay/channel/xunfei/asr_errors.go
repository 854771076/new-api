package xunfei

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/types"
)

// 讯飞 IFASR 转写的错误映射。
//
// 映射必须落到具体的网关状态码与「跳过重试」标记上：仓库默认的渠道自动禁用状态码范围
// 只有 401，默认重试范围覆盖 401–407。若把凭据类错误映射到 401，渠道会被自动禁用并重试，
// 与「凭据错误不该让渠道被误禁用、限流与上游故障才计入失败次数」的要求正好相反。
// 因此凭据类错误统一映射到 403（不落入自动禁用范围），并显式标记跳过重试；
// 400 类错误既不在默认重试范围内，也显式跳过重试。

// 错误发生阶段。
const (
	// asrStageUpload 错误发生在音频上传阶段。
	asrStageUpload = "upload"
	// asrStagePoll 错误发生在转写结果轮询阶段。
	asrStagePoll = "poll"
)

// 客户端可见的讯飞转写错误码。
const (
	// ASRErrorCodeInvalidRequest 请求参数或音频本身不合法。
	ASRErrorCodeInvalidRequest types.ErrorCode = "xfyun_asr_invalid_request"
	// ASRErrorCodeCredential 凭据或签名不合法。
	ASRErrorCodeCredential types.ErrorCode = "xfyun_asr_credential_error"
	// ASRErrorCodePermission 账号缺少相应权限。
	ASRErrorCodePermission types.ErrorCode = "xfyun_asr_permission_error"
	// ASRErrorCodeOrderNotFound 订单不存在。
	ASRErrorCodeOrderNotFound types.ErrorCode = "xfyun_asr_order_not_found"
	// ASRErrorCodeOrderConflict 订单状态与当前请求冲突（例如订单未完成）。
	ASRErrorCodeOrderConflict types.ErrorCode = "xfyun_asr_order_conflict"
	// ASRErrorCodeRateLimited 上游频率限制。
	ASRErrorCodeRateLimited types.ErrorCode = "xfyun_asr_rate_limited"
	// ASRErrorCodeUpstream 上游故障或网络故障。
	ASRErrorCodeUpstream types.ErrorCode = "xfyun_asr_upstream_error"
)

// asrErrorCategory 是讯飞转写错误的分类，决定网关状态码、重试标记与客户端可见的错误码。
type asrErrorCategory int

const (
	// asrCategoryUnknown 无法分类的错误，走 5xx 兜底并允许重试。
	asrCategoryUnknown asrErrorCategory = iota
	// asrCategoryClient 客户端请求有误（参数、音频格式/大小/时长等）。
	asrCategoryClient
	// asrCategoryCredential 凭据或签名有误。
	asrCategoryCredential
	// asrCategoryPermission 账号缺少相应权限。
	asrCategoryPermission
	// asrCategoryOrderNotFound 订单不存在。
	asrCategoryOrderNotFound
	// asrCategoryOrderConflict 订单状态与当前请求冲突。
	asrCategoryOrderConflict
	// asrCategoryRateLimited 上游频率限制。
	asrCategoryRateLimited
	// asrCategoryUpstream 上游故障或网络故障。
	asrCategoryUpstream
)

// asrCategoryBehavior 返回一个错误分类对应的网关状态码、跳过重试标记与错误码。
//
// 状态码的选取受仓库默认规则约束：自动禁用范围只有 401，自动重试范围覆盖 401–407 与
// 409–499。凭据类映射到 403 并跳过重试，因此既不会被自动禁用，也不会被重试；
// 409（订单状态冲突）与 429（频率限制）都落在默认重试范围内，前者靠显式跳过重试避免
// 重跑一次转写，后者则按既有规则重试。
func asrCategoryBehavior(category asrErrorCategory) (int, bool, types.ErrorCode) {
	switch category {
	case asrCategoryClient:
		return http.StatusBadRequest, true, ASRErrorCodeInvalidRequest
	case asrCategoryCredential:
		return http.StatusForbidden, true, ASRErrorCodeCredential
	case asrCategoryPermission:
		return http.StatusForbidden, true, ASRErrorCodePermission
	case asrCategoryOrderNotFound:
		return http.StatusNotFound, true, ASRErrorCodeOrderNotFound
	case asrCategoryOrderConflict:
		return http.StatusConflict, true, ASRErrorCodeOrderConflict
	case asrCategoryRateLimited:
		return http.StatusTooManyRequests, false, ASRErrorCodeRateLimited
	case asrCategoryUpstream:
		return http.StatusBadGateway, false, ASRErrorCodeUpstream
	default:
		return http.StatusInternalServerError, false, ""
	}
}

// asrCodeCategories 是上游业务错误码到错误分类的映射。
//
// 码值取自讯飞 IFASR 官方接口文档的错误码对照表。
var asrCodeCategories = map[string]asrErrorCategory{
	"000001":  asrCategoryClient,        // 参数错误或不完整
	"000002":  asrCategoryCredential,    // accessKeyId 不存在
	"1000000": asrCategoryClient,        // 不支持的操作
	"100001":  asrCategoryOrderNotFound, // 订单不存在或状态异常
	"100002":  asrCategoryOrderConflict, // 订单音频未上传
	"100003":  asrCategoryClient,        // 参数错误
	"100004":  asrCategoryUpstream,      // 查询订单错误
	"100005":  asrCategoryOrderNotFound, // 查询音频为空
	"100006":  asrCategoryUpstream,      // 上传音频异常
	"100007":  asrCategoryPermission,    // 权限错误
	"100008":  asrCategoryCredential,    // 签名异常-请求时间超过限制
	"100009":  asrCategoryCredential,    // 签名校验不通过
	"100012":  asrCategoryRateLimited,   // 请求超过频率限制
	"100013":  asrCategoryOrderConflict, // 订单未完成
	"100015":  asrCategoryClient,        // 热词必须是中文
	"100016":  asrCategoryClient,        // 热词超出长度限制
	"100017":  asrCategoryClient,        // 热词超出数量限制
	"100018":  asrCategoryClient,        // 热词分隔符不能连续出现
	"100019":  asrCategoryClient,        // 热词验证失败
	"100020":  asrCategoryClient,        // 语言验证失败
	"100021":  asrCategoryUpstream,      // 热词上传失败
	"100022":  asrCategoryClient,        // 热词不断重复
	"100023":  asrCategoryUpstream,      // 热词保存失败
	"100024":  asrCategoryClient,        // 热词为空
	"100025":  asrCategoryClient,        // 热词 ID 未知
	"100026":  asrCategoryClient,        // 时间格式必须为 yy-MM-dd
	"100027":  asrCategoryClient,        // patch ID 未知
	"100028":  asrCategoryClient,        // patch 验证失败
	"100029":  asrCategoryClient,        // 文件已存在
	"100030":  asrCategoryClient,        // 未知的文件格式
	"100031":  asrCategoryClient,        // 多候选 ID 未知
	"100032":  asrCategoryClient,        // 多候选验证失败
	"100033":  asrCategoryClient,        // 无效的角色分离个数
	"100034":  asrCategoryCredential,    // 更改 AccessKeySecret 失败
	"100037":  asrCategoryOrderNotFound, // 非法的订单号
	"100038":  asrCategoryClient,        // 删除订单验证失败
	"100039":  asrCategoryOrderNotFound, // 订单为空
	"100040":  asrCategoryOrderConflict, // 订单个数超出限制
	"100041":  asrCategoryUpstream,      // 切换通道失败
	"100042":  asrCategoryClient,        // 外链地址无效
	"100043":  asrCategoryClient,        // 通道类型验证失败
	"100044":  asrCategoryClient,        // 通道类型不存在
	"999999":  asrCategoryUpstream,      // 未知异常
}

// asrFailTypeCategories 是订单失败类型到错误分类的映射。
//
// 失败类型取自上游 orderInfo.failType：时长超限、时长校验失败与静音文件都是音频本身
// 不合法，属于客户端请求有误；无翻译权限与未开启能力属于账号权限问题。
var asrFailTypeCategories = map[int]asrErrorCategory{
	1:  asrCategoryUpstream,   // 音频上传失败
	2:  asrCategoryUpstream,   // 音频转码失败
	3:  asrCategoryUpstream,   // 音频识别失败
	4:  asrCategoryClient,     // 音频时长超限
	5:  asrCategoryClient,     // 音频校验失败（时长与实际不符）
	6:  asrCategoryClient,     // 静音文件
	7:  asrCategoryUpstream,   // 翻译失败
	8:  asrCategoryPermission, // 账号无翻译权限
	9:  asrCategoryUpstream,   // 转写质检失败
	10: asrCategoryClient,     // 转写质检未匹配出关键词
	11: asrCategoryPermission, // 未开启质检或翻译能力
	12: asrCategoryUpstream,   // 音频语种分析失败
	99: asrCategoryUpstream,   // 其他
}

// asrFailTypeDescriptions 是订单失败类型的中文说明。
var asrFailTypeDescriptions = map[int]string{
	1:  "音频上传失败",
	2:  "音频转码失败",
	3:  "音频识别失败",
	4:  "音频时长超限",
	5:  "音频时长校验失败",
	6:  "静音文件",
	7:  "翻译失败",
	8:  "账号无翻译权限",
	9:  "转写质检失败",
	10: "转写质检未匹配出关键词",
	11: "未开启质检或翻译能力",
	12: "音频语种分析失败",
	99: "其他失败",
}

// asrFailTypeDescription 返回订单失败类型的中文说明，未知类型按「其他失败」处理。
func asrFailTypeDescription(failType int) string {
	if description, ok := asrFailTypeDescriptions[failType]; ok {
		return description
	}
	return asrFailTypeDescriptions[99]
}

// ASRErrorInfo 描述讯飞 IFASR 上游返回的一个错误。
type ASRErrorInfo struct {
	// UpstreamCode 是上游业务错误码，例如 "100009"（签名校验失败）。
	UpstreamCode string
	// Description 是上游给出的错误描述。
	Description string
	// FailType 是订单失败类型，取值见讯飞接口文档。
	FailType int
	// Stage 区分错误发生在哪个阶段："upload" 或 "poll"。
	Stage string
}

// ASRErrorStatus 描述一个上游错误映射到网关后的行为。
type ASRErrorStatus struct {
	// StatusCode 是网关返回给客户端的 HTTP 状态码。
	StatusCode int
	// SkipRetry 表示该错误不应触发渠道重试。
	SkipRetry bool
	// Message 是面向客户端的错误信息。
	Message string
}

// ASRUpstreamError 是讯飞 IFASR 上游侧的一次失败。
//
// 它承载上游的业务错误码、描述、订单失败类型与失败阶段；网络故障与上游 HTTP 状态错误
// 也用它承载，此时 UpstreamCode 为空、HTTPStatus 记录上游状态码。
type ASRUpstreamError struct {
	// ASRErrorInfo 承载上游错误码、描述、订单失败类型与失败阶段。
	ASRErrorInfo
	// HTTPStatus 是上游返回的 HTTP 状态码；上游返回 200 或没有响应时为 0。
	HTTPStatus int
	// Cause 是引发本次失败的底层错误（网络故障、上下文取消等），可以为空。
	Cause error
}

// Unwrap 暴露底层错误，使 errors.Is / errors.As 能沿错误链判定。
func (e *ASRUpstreamError) Unwrap() error {
	return e.Cause
}

// Error 输出面向客户端的错误信息：失败阶段、上游错误码、上游描述与失败类型。
func (e *ASRUpstreamError) Error() string {
	stage := "转写"
	switch e.Stage {
	case asrStageUpload:
		stage = "上传"
	case asrStagePoll:
		stage = "查询"
	}

	parts := []string{"讯飞转写" + stage + "失败"}
	if e.UpstreamCode != "" && e.UpstreamCode != asrSuccessCode {
		parts = append(parts, "上游错误码 "+e.UpstreamCode)
	}
	if e.Description != "" {
		parts = append(parts, "描述："+e.Description)
	}
	if e.FailType != 0 {
		parts = append(parts, "失败类型 "+strconv.Itoa(e.FailType)+"（"+asrFailTypeDescription(e.FailType)+"）")
	}
	if e.HTTPStatus != 0 {
		parts = append(parts, "上游 HTTP 状态码 "+strconv.Itoa(e.HTTPStatus))
	}
	return strings.Join(parts, "，")
}

// category 判定该上游错误所属的分类。
//
// 订单失败优先按失败类型分类（时长超限、静音文件等属于客户端请求有误）；否则按上游业务
// 错误码分类；两者都没有时是上游 HTTP 状态错误或网络故障，一律按上游故障处理。
func (e *ASRUpstreamError) category() asrErrorCategory {
	if e.FailType != 0 {
		if category, ok := asrFailTypeCategories[e.FailType]; ok {
			return category
		}
		return asrCategoryUpstream
	}
	if e.UpstreamCode != "" && e.UpstreamCode != asrSuccessCode {
		if category, ok := asrCodeCategories[e.UpstreamCode]; ok {
			return category
		}
		return asrCategoryUpstream
	}
	return asrCategoryUpstream
}

// ASRLocalError 是讯飞转写在调用上游之前检测到的请求侧错误。
//
// 它区分「客户端请求有误」与「凭据有误」两类：前者返回 400，后者返回 403，两类都跳过
// 重试，凭据类也不会触发渠道自动禁用。
type ASRLocalError struct {
	// Category 错误分类：客户端请求有误或凭据有误。
	Category asrErrorCategory
	// Reason 错误原因。
	Reason string
}

// Error 返回错误原因。
func (e *ASRLocalError) Error() string {
	return e.Reason
}

// MapASRError 把讯飞 IFASR 的上游错误映射为网关错误行为。
func MapASRError(err error) ASRErrorStatus {
	if err == nil {
		return ASRErrorStatus{StatusCode: http.StatusInternalServerError, Message: "讯飞转写未知错误"}
	}
	status, skipRetry, _ := asrCategoryBehavior(classifyASRError(err))
	return ASRErrorStatus{StatusCode: status, SkipRetry: skipRetry, Message: err.Error()}
}

// NewASRAPIError 把上游错误转换为网关错误，fallbackCode 是无法分类时的兜底错误码。
func NewASRAPIError(err error, fallbackCode types.ErrorCode) *types.NewAPIError {
	if err == nil {
		return nil
	}
	status, skipRetry, errorCode := asrCategoryBehavior(classifyASRError(err))
	if errorCode == "" {
		errorCode = fallbackCode
	}
	options := []types.NewAPIErrorOptions{types.ErrOptionWithStatusCode(status)}
	if skipRetry {
		options = append(options, types.ErrOptionWithSkipRetry())
	}
	return types.NewError(err, errorCode, options...)
}

// classifyASRError 判定一个错误所属的分类。
//
// 判定沿错误链进行：先看本地请求错误，再看上游错误，其余归为无法分类。
func classifyASRError(err error) asrErrorCategory {
	var local *ASRLocalError
	if errors.As(err, &local) {
		return local.Category
	}
	var upstream *ASRUpstreamError
	if errors.As(err, &upstream) {
		return upstream.category()
	}
	return asrCategoryUnknown
}
