package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResetStatusCode(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		statusCode       int
		statusCodeConfig string
		expectedCode     int
	}{
		{
			name:             "map string value",
			statusCode:       429,
			statusCodeConfig: `{"429":"503"}`,
			expectedCode:     503,
		},
		{
			name:             "map int value",
			statusCode:       429,
			statusCodeConfig: `{"429":503}`,
			expectedCode:     503,
		},
		{
			name:             "skip invalid string value",
			statusCode:       429,
			statusCodeConfig: `{"429":"bad-code"}`,
			expectedCode:     429,
		},
		{
			name:             "skip status code 200",
			statusCode:       200,
			statusCodeConfig: `{"200":503}`,
			expectedCode:     200,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			newAPIError := &types.NewAPIError{
				StatusCode: tc.statusCode,
			}
			ResetStatusCode(newAPIError, tc.statusCodeConfig)
			require.Equal(t, tc.expectedCode, newAPIError.StatusCode)
		})
	}
}

func TestRelayErrorHandlerTruncatesInvalidJSONBodyInLog(t *testing.T) {
	withDebugEnabled(t, false)

	body := strings.Repeat("b", common.LocalLogContentLimit+256)
	var logBuffer bytes.Buffer

	common.LogWriterMu.Lock()
	oldWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logBuffer
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = oldWriter
		common.LogWriterMu.Unlock()
	})

	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)

	require.NotNil(t, newAPIError)
	require.Equal(t, "bad response status code 500", newAPIError.Error())
	require.Contains(t, logBuffer.String(), "[truncated")
	require.Contains(t, logBuffer.String(), fmt.Sprintf("original_length=%d", len(body)))
	require.NotContains(t, logBuffer.String(), strings.Repeat("b", common.LocalLogContentLimit+1))
}

func TestRelayErrorHandlerKeepsStructuredErrorMessage(t *testing.T) {
	message := strings.Repeat("c", common.LocalLogContentLimit+256)
	body := `{"message":"` + message + `"}`
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)

	require.NotNil(t, newAPIError)
	require.Equal(t, message, newAPIError.Error())
}

func TestRelayErrorHandlerKeepsOpenAIErrorMessage(t *testing.T) {
	message := strings.Repeat("d", common.LocalLogContentLimit+256)
	body := `{"error":{"message":"` + message + `","type":"server_error","code":"server_error"}}`
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)

	require.NotNil(t, newAPIError)
	require.Equal(t, message, newAPIError.Error())
}

func TestRelayErrorHandlerKeepsInvalidJSONBodyInDebugLog(t *testing.T) {
	withDebugEnabled(t, true)

	body := strings.Repeat("e", common.LocalLogContentLimit+256)
	var logBuffer bytes.Buffer

	common.LogWriterMu.Lock()
	oldWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logBuffer
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = oldWriter
		common.LogWriterMu.Unlock()
	})

	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)

	require.NotNil(t, newAPIError)
	require.NotContains(t, logBuffer.String(), "[truncated")
	require.Contains(t, logBuffer.String(), body)
}

func withDebugEnabled(t *testing.T, enabled bool) {
	t.Helper()

	oldDebug := common.DebugEnabled
	common.DebugEnabled = enabled
	t.Cleanup(func() {
		common.DebugEnabled = oldDebug
	})
}

// captureTaskSysLog redirects the system log into a buffer for the duration of
// the test. common.SysLog writes to gin.DefaultWriter, not to the error writer.
func captureTaskSysLog(t *testing.T) *bytes.Buffer {
	t.Helper()

	var logBuffer bytes.Buffer
	common.LogWriterMu.Lock()
	oldWriter := gin.DefaultWriter
	gin.DefaultWriter = &logBuffer
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = oldWriter
		common.LogWriterMu.Unlock()
	})
	return &logBuffer
}

// taskErrorLogLines returns the task failure lines the system log received.
func taskErrorLogLines(t *testing.T, logBuffer *bytes.Buffer) []string {
	t.Helper()

	var lines []string
	for _, line := range strings.Split(logBuffer.String(), "\n") {
		if strings.Contains(line, "task error: code=") {
			lines = append(lines, line)
		}
	}
	return lines
}

// taskUpstreamErrorChain builds the error chain the upstream request layer
// produces: the relaykit classifier already attached the fine-grained code and
// the operator-facing reason to the *types.NewAPIError.
func taskUpstreamErrorChain(cause error) error {
	failure := types.ClassifyUpstreamError(cause)
	return fmt.Errorf("do request failed: %w", types.NewError(
		errors.New("upstream error: do request failed"),
		failure.Code,
		types.ErrOptionWithAdminReason(failure.Reason),
	))
}

func TestTaskUpstreamErrorCode(t *testing.T) {
	t.Run("fine grained upstream code is taken from the error chain", func(t *testing.T) {
		err := taskUpstreamErrorChain(context.DeadlineExceeded)

		assert.Equal(t, "upstream_timeout", TaskUpstreamErrorCode(err))
	})

	t.Run("non transport code falls back to the catch all", func(t *testing.T) {
		err := types.NewError(errors.New("bad response"), types.ErrorCodeBadResponseStatusCode)

		assert.Equal(t, "do_request_failed", TaskUpstreamErrorCode(err))
	})

	t.Run("plain error falls back to the catch all", func(t *testing.T) {
		assert.Equal(t, "do_request_failed", TaskUpstreamErrorCode(errors.New("boom")))
	})

	t.Run("nil error falls back to the catch all", func(t *testing.T) {
		assert.Equal(t, "do_request_failed", TaskUpstreamErrorCode(nil))
	})
}

func TestTaskErrorWrapperLogsUpstreamFailure(t *testing.T) {
	t.Run("timeout failure leaves a trace", func(t *testing.T) {
		logBuffer := captureTaskSysLog(t)
		failure := types.ClassifyUpstreamError(context.DeadlineExceeded)
		err := taskUpstreamErrorChain(context.DeadlineExceeded)

		taskErr := TaskErrorWrapper(err, TaskUpstreamErrorCode(err), http.StatusInternalServerError)

		require.NotNil(t, taskErr)
		assert.Equal(t, http.StatusInternalServerError, taskErr.StatusCode)
		assert.Equal(t, "do request failed: upstream error: do request failed", taskErr.Message)
		lines := taskErrorLogLines(t, logBuffer)
		require.Len(t, lines, 1)
		assert.Contains(t, lines[0], "code=upstream_timeout")
		assert.Contains(t, lines[0], "upstream_code=upstream_timeout")
		assert.Contains(t, lines[0], failure.Reason)
	})

	t.Run("upstream response body is replaced by a placeholder", func(t *testing.T) {
		logBuffer := captureTaskSysLog(t)
		body := `{"error":"upstream denied the job SECRET-BODY-MARKER"}`

		taskErr := TaskErrorWrapper(&TaskUpstreamBodyError{Body: body}, "fail_to_fetch_task", http.StatusBadGateway)

		require.NotNil(t, taskErr)
		assert.Equal(t, body, taskErr.Message)
		lines := taskErrorLogLines(t, logBuffer)
		require.Len(t, lines, 1)
		assert.NotContains(t, lines[0], "SECRET-BODY-MARKER")
		assert.Contains(t, lines[0], "upstream response body omitted")
	})

	t.Run("upstream side failure is traced through the local wrapper", func(t *testing.T) {
		logBuffer := captureTaskSysLog(t)

		taskErr := TaskErrorWrapperLocal(errors.New("upstream returned an empty response"), "fail_to_fetch_task", http.StatusBadGateway)

		require.NotNil(t, taskErr)
		assert.True(t, taskErr.LocalError)
		lines := taskErrorLogLines(t, logBuffer)
		require.Len(t, lines, 1)
		assert.Contains(t, lines[0], "code=fail_to_fetch_task")
		assert.Contains(t, lines[0], "status=502")
	})

	t.Run("long detail is truncated outside debug mode", func(t *testing.T) {
		withDebugEnabled(t, false)
		logBuffer := captureTaskSysLog(t)
		longText := strings.Repeat("x", common.LocalLogContentLimit+256)

		taskErr := TaskErrorWrapper(errors.New(longText), "build_request_failed", http.StatusInternalServerError)

		require.NotNil(t, taskErr)
		lines := taskErrorLogLines(t, logBuffer)
		require.Len(t, lines, 1)
		assert.Contains(t, lines[0], "[truncated")
		assert.Contains(t, lines[0], fmt.Sprintf("original_length=%d", len(longText)))
		assert.Less(t, len(lines[0]), common.LocalLogContentLimit+512)
	})
}

func TestTaskErrorWrapperSkipsNonUpstreamFailures(t *testing.T) {
	silentCodes := []string{
		"request_cancelled",
		"upstream_client_canceled",
		"read_request_body_failed",
		"invalid_request",
		"invalid_api_platform",
		"task_plugin_system_disabled",
		"task_plugin_disabled",
		"model_mapping_failed",
		"plugin_usage_invalid",
		"model_price_error",
		"invalid_relay_mode",
		"task_not_exist",
		"channel_not_found",
		"task_channel_disable",
		"origin_task_channel_disabled",
		"channel_no_available_key",
		"invalid_channel_id",
		"not_implemented",
		"plugin_request_invalid",
		"get_channel_failed",
		"setup_locked_channel_failed",
		"insufficient_user_quota",
	}
	tracedCodes := []string{
		"fail_to_fetch_task",
		"plugin_submit_response_invalid",
		"plugin_submit_response_failed",
		"image_generation_failed",
		"task_timeout",
		"build_request_failed",
		"read_response_body_failed",
		"task_submit_failed",
		"task_insert_failed",
		"task_billing_settlement_failed",
		"get_task_failed",
		"get_origin_task_failed",
		"copy_response_body_failed",
		"convert_to_openai_video_failed",
		"marshal_response_failed",
	}

	for _, code := range silentCodes {
		t.Run(code, func(t *testing.T) {
			logBuffer := captureTaskSysLog(t)

			taskErr := TaskErrorWrapperLocal(errors.New("rejected before the upstream request"), code, http.StatusBadRequest)

			require.NotNil(t, taskErr)
			assert.True(t, taskErr.LocalError)
			assert.Empty(t, taskErrorLogLines(t, logBuffer))
		})
	}

	for _, code := range tracedCodes {
		t.Run(code, func(t *testing.T) {
			logBuffer := captureTaskSysLog(t)

			taskErr := TaskErrorWrapperLocal(errors.New("upstream side failure"), code, http.StatusBadGateway)

			require.NotNil(t, taskErr)
			assert.Len(t, taskErrorLogLines(t, logBuffer), 1)
		})
	}
}

func TestTaskSilentFailureCodesSet(t *testing.T) {
	expected := []string{
		"request_cancelled",
		"upstream_client_canceled",
		"read_request_body_failed",
		"invalid_request",
		"invalid_api_platform",
		"task_plugin_system_disabled",
		"task_plugin_disabled",
		"model_mapping_failed",
		"plugin_usage_invalid",
		"model_price_error",
		"invalid_relay_mode",
		"task_not_exist",
		"channel_not_found",
		"task_channel_disable",
		"origin_task_channel_disabled",
		"channel_no_available_key",
		"invalid_channel_id",
		"not_implemented",
		"plugin_request_invalid",
		"get_channel_failed",
		"setup_locked_channel_failed",
		"insufficient_user_quota",
	}

	require.Len(t, taskSilentFailureCodes, len(expected))
	for _, code := range expected {
		assert.True(t, taskSilentFailureCodes[code], "expected %q to be a silent failure code", code)
	}
}
