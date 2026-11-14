package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
)

func MidjourneyErrorWrapper(code int, desc string) *taskdto.MidjourneyResponse {
	return &taskdto.MidjourneyResponse{
		Code:        code,
		Description: desc,
	}
}

func MidjourneyErrorWithStatusCodeWrapper(code int, desc string, statusCode int) *taskdto.MidjourneyResponseWithStatusCode {
	return &taskdto.MidjourneyResponseWithStatusCode{
		StatusCode: statusCode,
		Response:   *MidjourneyErrorWrapper(code, desc),
	}
}

//// OpenAIErrorWrapper wraps an error into an OpenAIErrorWithStatusCode
//func OpenAIErrorWrapper(err error, code string, statusCode int) *dto.OpenAIErrorWithStatusCode {
//	text := err.Error()
//	lowerText := strings.ToLower(text)
//	if !strings.HasPrefix(lowerText, "get file base64 from url") && !strings.HasPrefix(lowerText, "mime type is not supported") {
//		if strings.Contains(lowerText, "post") || strings.Contains(lowerText, "dial") || strings.Contains(lowerText, "http") {
//			common.SysLog(fmt.Sprintf("error: %s", text))
//			text = "请求上游地址失败"
//		}
//	}
//	openAIError := dto.OpenAIError{
//		Message: text,
//		Type:    "new_api_error",
//		Code:    code,
//	}
//	return &dto.OpenAIErrorWithStatusCode{
//		Error:      openAIError,
//		StatusCode: statusCode,
//	}
//}
//
//func OpenAIErrorWrapperLocal(err error, code string, statusCode int) *dto.OpenAIErrorWithStatusCode {
//	openaiErr := OpenAIErrorWrapper(err, code, statusCode)
//	openaiErr.LocalError = true
//	return openaiErr
//}

func ClaudeErrorWrapper(err error, code string, statusCode int) *dto.ClaudeErrorWithStatusCode {
	text := err.Error()
	lowerText := strings.ToLower(text)
	if !strings.HasPrefix(lowerText, "get file base64 from url") {
		if strings.Contains(lowerText, "post") || strings.Contains(lowerText, "dial") || strings.Contains(lowerText, "http") {
			common.SysLog(common.LogText("error: %s", text))
			text = "request to the upstream address failed"
		}
	}
	claudeError := types.ClaudeError{
		Message: text,
		Type:    "new_api_error",
	}
	return &dto.ClaudeErrorWithStatusCode{
		Error:      claudeError,
		StatusCode: statusCode,
	}
}

func ClaudeErrorWrapperLocal(err error, code string, statusCode int) *dto.ClaudeErrorWithStatusCode {
	claudeErr := ClaudeErrorWrapper(err, code, statusCode)
	claudeErr.LocalError = true
	return claudeErr
}

func RelayErrorHandler(ctx context.Context, resp *http.Response, showBodyWhenFail bool) (newApiErr *types.NewAPIError) {
	newApiErr = types.InitOpenAIError(types.ErrorCodeBadResponseStatusCode, resp.StatusCode)

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	CloseResponseBodyGracefully(resp)
	var errResponse dto.GeneralErrorResponse
	responseBodyText := string(responseBody)
	responseBodyPreview := common.LocalLogPreview(responseBodyText)
	buildErrWithBody := func(message string) error {
		if message == "" {
			return fmt.Errorf("bad response status code %d, body: %s", resp.StatusCode, responseBodyText)
		}
		return fmt.Errorf("bad response status code %d, message: %s, body: %s", resp.StatusCode, message, responseBodyText)
	}

	err = common.Unmarshal(responseBody, &errResponse)
	if err != nil {
		if showBodyWhenFail {
			newApiErr.Err = buildErrWithBody("")
		} else {
			logger.LogError(ctx, common.LogText("bad response status code %d, body: %s", resp.StatusCode, responseBodyPreview))
			newApiErr.Err = fmt.Errorf("bad response status code %d", resp.StatusCode)
		}
		return
	}

	if common.GetJsonType(errResponse.Error) == "object" {
		// General format error (OpenAI, Anthropic, Gemini, etc.)
		oaiError := errResponse.TryToOpenAIError()
		if oaiError != nil {
			newApiErr = types.WithOpenAIError(*oaiError, resp.StatusCode)
			if showBodyWhenFail {
				newApiErr.Err = buildErrWithBody(newApiErr.Error())
			}
			return
		}
	}
	message := errResponse.ToMessage()
	if message == "" {
		// The body parsed as JSON but carried no usable error message; log the
		// raw body so the upstream failure remains diagnosable.
		logger.LogError(ctx, common.LogText("bad response status code %d with empty error message, body: %s", resp.StatusCode, responseBodyPreview))
	}
	newApiErr = types.NewOpenAIError(errors.New(message), types.ErrorCodeBadResponseStatusCode, resp.StatusCode)
	if showBodyWhenFail {
		newApiErr.Err = buildErrWithBody(newApiErr.Error())
	}
	return
}

func ResetStatusCode(newApiErr *types.NewAPIError, statusCodeMappingStr string) {
	if newApiErr == nil {
		return
	}
	if statusCodeMappingStr == "" || statusCodeMappingStr == "{}" {
		return
	}
	statusCodeMapping := make(map[string]any)
	err := common.Unmarshal([]byte(statusCodeMappingStr), &statusCodeMapping)
	if err != nil {
		return
	}
	if newApiErr.StatusCode == http.StatusOK {
		return
	}
	codeStr := strconv.Itoa(newApiErr.StatusCode)
	if value, ok := statusCodeMapping[codeStr]; ok {
		intCode, ok := parseStatusCodeMappingValue(value)
		if !ok {
			return
		}
		newApiErr.StatusCode = intCode
	}
}

func parseStatusCodeMappingValue(value any) (int, bool) {
	switch v := value.(type) {
	case string:
		if v == "" {
			return 0, false
		}
		statusCode, err := strconv.Atoi(v)
		if err != nil {
			return 0, false
		}
		return statusCode, true
	case float64:
		if v != math.Trunc(v) {
			return 0, false
		}
		return int(v), true
	case int:
		return v, true
	case json.Number:
		statusCode, err := strconv.Atoi(v.String())
		if err != nil {
			return 0, false
		}
		return statusCode, true
	default:
		return 0, false
	}
}

func TaskErrorWrapperLocal(err error, code string, statusCode int) *taskdto.TaskError {
	openaiErr := TaskErrorWrapper(err, code, statusCode)
	openaiErr.LocalError = true
	return openaiErr
}

// upstreamErrorCodePrefix marks the error codes that name a failure of the
// upstream transport layer. It mirrors the prefix relaykit uses for the same
// family (relaykit/types/upstream_error.go). The family grows with every new
// transport failure, so the code is matched by prefix and never by comparing
// single constants.
const upstreamErrorCodePrefix = "upstream_"

// taskUpstreamBodyOmitted replaces the upstream response body in the system
// log. The body is the only part of a task error that may carry an upstream
// payload, so it never reaches the log.
const taskUpstreamBodyOmitted = "upstream response body omitted"

// TaskUpstreamErrorCode returns the fine-grained upstream transport code of
// err when the error chain carries one, and the catch-all "do_request_failed"
// otherwise.
//
// The relaykit classifier already computed the code and the operator-facing
// reason and stored them in the *types.NewAPIError of the chain; this only
// reads that result, it never classifies again.
func TaskUpstreamErrorCode(err error) string {
	var apiErr *types.NewAPIError
	if err != nil && errors.As(err, &apiErr) {
		code := string(apiErr.GetErrorCode())
		if strings.HasPrefix(code, upstreamErrorCodePrefix) {
			return code
		}
	}
	return string(types.ErrorCodeDoRequestFailed)
}

// TaskUpstreamBodyError carries an upstream response body inside an error.
// The body stays in the client-facing message (Error returns it verbatim) but
// the system log replaces it with a placeholder.
type TaskUpstreamBodyError struct {
	Body string
}

func (e *TaskUpstreamBodyError) Error() string {
	if e == nil {
		return ""
	}
	return e.Body
}

// OmittedDetail is the placeholder the system log uses instead of the body.
func (e *TaskUpstreamBodyError) OmittedDetail() string {
	return taskUpstreamBodyOmitted
}

// taskSilentFailureCodes holds the task codes that are not upstream failures:
// the request was rejected before it reached the upstream, or the client
// canceled it. Those failures stay out of the system log so the log keeps
// pointing at the upstream faults; every other task failure is traced.
//
// The members and the call site that produces them:
//   - request_cancelled: controller/relay.go:755,770,812,856,875;
//     controller/plugin_protocol_image.go:184 (client canceled the request)
//   - upstream_client_canceled: relay/relay_task.go:355 (the client went away
//     while the upstream request was in flight; the code comes from the error
//     chain via TaskUpstreamErrorCode, and relaykit classifies it as a
//     cancellation, so the same client action stays as quiet as the
//     controller-side request_cancelled)
//   - read_request_body_failed: controller/relay.go:800,802
//   - invalid_request: relay/relay_task.go:57
//   - invalid_api_platform / task_plugin_system_disabled / task_plugin_disabled:
//     relay/relay_task.go:210 (relay/relay_adaptor.go:172-186)
//   - model_mapping_failed: relay/relay_task.go:228,245
//   - plugin_usage_invalid: relay/relay_task.go:280,313;
//     relay/channel/task/jsplugin/adaptor.go:129,140
//   - model_price_error: relay/relay_task.go:264,267,273,290,300
//   - invalid_relay_mode: relay/relay_task.go:455
//   - task_not_exist: relay/relay_task.go:72,488
//   - channel_not_found: relay/relay_task.go:93
//   - task_channel_disable: relay/relay_task.go:96
//   - origin_task_channel_disabled: relay/relay_task.go:180,183
//   - channel_no_available_key: relay/relay_task.go:103
//   - invalid_channel_id: relay/relay_task.go:504
//   - not_implemented: relay/relay_task.go:516
//   - plugin_request_invalid: relay/channel/task/jsplugin/adaptor.go:104,107,133
//   - get_channel_failed: controller/relay.go:789
//   - setup_locked_channel_failed: controller/relay.go:780
//   - insufficient_user_quota: controller/relay.go:867
var taskSilentFailureCodes = map[string]bool{
	"request_cancelled":            true,
	"upstream_client_canceled":     true,
	"read_request_body_failed":     true,
	"invalid_request":              true,
	"invalid_api_platform":         true,
	"task_plugin_system_disabled":  true,
	"task_plugin_disabled":         true,
	"model_mapping_failed":         true,
	"plugin_usage_invalid":         true,
	"model_price_error":            true,
	"invalid_relay_mode":           true,
	"task_not_exist":               true,
	"channel_not_found":            true,
	"task_channel_disable":         true,
	"origin_task_channel_disabled": true,
	"channel_no_available_key":     true,
	"invalid_channel_id":           true,
	"not_implemented":              true,
	"plugin_request_invalid":       true,
	"get_channel_failed":           true,
	"setup_locked_channel_failed":  true,
	"insufficient_user_quota":      true,
}

// TaskErrorWrapper masks the upstream addresses of the task channel in network
// errors; see common.MaskSensitiveInfo.
func TaskErrorWrapper(err error, code string, statusCode int, upstreamURLs ...string) *taskdto.TaskError {
	text := err.Error()
	lowerText := strings.ToLower(text)
	if strings.Contains(lowerText, "post") || strings.Contains(lowerText, "dial") || strings.Contains(lowerText, "http") {
		text = common.MaskSensitiveInfo(text, upstreamURLs...)
	}
	// The wrapper only decides the LocalError marker; whether the failure is
	// traced is decided by the task code, not by the wrapper that carried it.
	// An upstream failure or a server-side failure is always traced, so a
	// failure that never reached the upstream no longer disappears silently.
	if !taskSilentFailureCodes[code] {
		common.SysLog(taskFailureLogLine(err, code, statusCode))
	}
	//避免暴露内部错误
	taskError := &taskdto.TaskError{
		Code:       code,
		Message:    text,
		StatusCode: statusCode,
		Error:      err,
	}

	return taskError
}

// taskFailureLogLine renders the single system log line of a task failure.
//
// The line carries classification facts only: the task code, the status code,
// the fine-grained upstream code, the operator-facing reason and a bounded
// detail. The upstream response body never reaches it: the only task path
// that puts an upstream body into an error text wraps it in
// TaskUpstreamBodyError, and the body is replaced by a placeholder here. The
// remaining text is masked (URL, bare domain, IPv4 and api_key: shapes) and
// truncated by common.LocalLogPreview outside debug mode. Other credential
// shapes (sk-…, Bearer …, key:…) are not masked: they come from in-process
// plugins and already reach the client in the task error message.
func taskFailureLogLine(err error, code string, statusCode int) string {
	upstreamCode := "-"
	reason := "-"
	var apiErr *types.NewAPIError
	if errors.As(err, &apiErr) {
		apiCode := apiErr.GetErrorCode()
		if types.IsUpstreamTransportError(apiCode) {
			upstreamCode = string(apiCode)
		}
		if apiReason := apiErr.AdminReason(); apiReason != "" {
			reason = apiReason
		}
	}

	detail := ""
	var bodyErr *TaskUpstreamBodyError
	if errors.As(err, &bodyErr) {
		detail = bodyErr.OmittedDetail()
	} else {
		detail = common.LocalLogPreview(common.MaskSensitiveInfo(err.Error()))
	}

	return fmt.Sprintf("task error: code=%s, status=%d, upstream_code=%s, reason=%s, detail=%s", code, statusCode, upstreamCode, reason, detail)
}

// TaskErrorFromAPIError 将 PreConsumeBilling 返回的 NewAPIError 转换为 TaskError。
func TaskErrorFromAPIError(apiErr *types.NewAPIError) *taskdto.TaskError {
	if apiErr == nil {
		return nil
	}
	return &taskdto.TaskError{
		Code:       string(apiErr.GetErrorCode()),
		Message:    apiErr.Err.Error(),
		StatusCode: apiErr.StatusCode,
		Error:      apiErr.Err,
	}
}
