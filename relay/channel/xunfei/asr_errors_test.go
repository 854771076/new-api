package xunfei

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 讯飞转写错误映射测试。
//
// 映射的正确性不只看状态码，还要看它与仓库默认规则的相互作用：渠道自动禁用的默认状态码
// 范围只有 401，默认重试范围覆盖 401–407。凭据类错误如果落到 401，渠道会被自动禁用并
// 重试，正是本票据要禁止的结果，所以这些用例直接引用仓库默认规则来断言。

// TestMapASRErrorUpstreamCodes 上游业务错误码到网关状态码与重试标记的映射。
func TestMapASRErrorUpstreamCodes(t *testing.T) {
	tests := []struct {
		name          string
		code          string
		description   string
		wantStatus    int
		wantSkipRetry bool
	}{
		{name: "参数错误或不完整", code: "000001", description: "参数缺失", wantStatus: http.StatusBadRequest, wantSkipRetry: true},
		{name: "accessKeyId 不存在", code: "000002", description: "accessKeyId not exist", wantStatus: http.StatusForbidden, wantSkipRetry: true},
		{name: "订单不存在或状态异常", code: "100001", description: "order not exist", wantStatus: http.StatusNotFound, wantSkipRetry: true},
		{name: "订单音频未上传", code: "100002", description: "audio not uploaded", wantStatus: http.StatusConflict, wantSkipRetry: true},
		{name: "参数错误", code: "100003", description: "dateTime format must be [yyyy-MM-dd'T'HH:mm:ssZ]", wantStatus: http.StatusBadRequest, wantSkipRetry: true},
		{name: "查询订单错误", code: "100004", description: "query order failed", wantStatus: http.StatusBadGateway, wantSkipRetry: false},
		{name: "上传音频异常", code: "100006", description: "upload audio failed", wantStatus: http.StatusBadGateway, wantSkipRetry: false},
		{name: "权限错误", code: "100007", description: "permission denied", wantStatus: http.StatusForbidden, wantSkipRetry: true},
		{name: "签名异常-请求时间超过限制", code: "100008", description: "request time out of limit", wantStatus: http.StatusForbidden, wantSkipRetry: true},
		{name: "签名校验不通过", code: "100009", description: "signature check failed", wantStatus: http.StatusForbidden, wantSkipRetry: true},
		{name: "请求超过频率限制", code: "100012", description: "frequency limit", wantStatus: http.StatusTooManyRequests, wantSkipRetry: false},
		{name: "订单未完成", code: "100013", description: "order not finished", wantStatus: http.StatusConflict, wantSkipRetry: true},
		{name: "语言验证失败", code: "100020", description: "language verify failed", wantStatus: http.StatusBadRequest, wantSkipRetry: true},
		{name: "时间格式必须为 yy-MM-dd", code: "100026", description: "time format error", wantStatus: http.StatusBadRequest, wantSkipRetry: true},
		{name: "未知的文件格式", code: "100030", description: "unknown file format", wantStatus: http.StatusBadRequest, wantSkipRetry: true},
		{name: "非法的订单号", code: "100037", description: "illegal order id", wantStatus: http.StatusNotFound, wantSkipRetry: true},
		{name: "订单为空", code: "100039", description: "order is empty", wantStatus: http.StatusNotFound, wantSkipRetry: true},
		{name: "外链地址无效", code: "100042", description: "invalid audio url", wantStatus: http.StatusBadRequest, wantSkipRetry: true},
		{name: "未知异常", code: "999999", description: "unknown exception", wantStatus: http.StatusBadGateway, wantSkipRetry: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &ASRUpstreamError{
				ASRErrorInfo: ASRErrorInfo{UpstreamCode: tt.code, Description: tt.description, Stage: asrStageUpload},
			}
			status := MapASRError(err)

			assert.Equal(t, tt.wantStatus, status.StatusCode)
			assert.Equal(t, tt.wantSkipRetry, status.SkipRetry)
			// 错误信息必须带上上游错误码与描述，便于排查。
			assert.Contains(t, status.Message, tt.code)
			assert.Contains(t, status.Message, tt.description)
		})
	}
}

// TestMapASRErrorOrderFailTypes 订单失败状态下的失败类型分类。
func TestMapASRErrorOrderFailTypes(t *testing.T) {
	tests := []struct {
		name          string
		failType      int
		wantStatus    int
		wantSkipRetry bool
	}{
		{name: "上传失败", failType: 1, wantStatus: http.StatusBadGateway, wantSkipRetry: false},
		{name: "转码失败", failType: 2, wantStatus: http.StatusBadGateway, wantSkipRetry: false},
		{name: "识别失败", failType: 3, wantStatus: http.StatusBadGateway, wantSkipRetry: false},
		{name: "时长超限", failType: 4, wantStatus: http.StatusBadRequest, wantSkipRetry: true},
		{name: "时长校验失败", failType: 5, wantStatus: http.StatusBadRequest, wantSkipRetry: true},
		{name: "静音文件", failType: 6, wantStatus: http.StatusBadRequest, wantSkipRetry: true},
		{name: "翻译失败", failType: 7, wantStatus: http.StatusBadGateway, wantSkipRetry: false},
		{name: "账号无翻译权限", failType: 8, wantStatus: http.StatusForbidden, wantSkipRetry: true},
		{name: "转写质检失败", failType: 9, wantStatus: http.StatusBadGateway, wantSkipRetry: false},
		{name: "转写质检未匹配出关键词", failType: 10, wantStatus: http.StatusBadRequest, wantSkipRetry: true},
		{name: "未开启质检或翻译能力", failType: 11, wantStatus: http.StatusForbidden, wantSkipRetry: true},
		{name: "音频语种分析失败", failType: 12, wantStatus: http.StatusBadGateway, wantSkipRetry: false},
		{name: "其他", failType: 99, wantStatus: http.StatusBadGateway, wantSkipRetry: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &ASRUpstreamError{
				ASRErrorInfo: ASRErrorInfo{
					UpstreamCode: asrSuccessCode,
					Description:  "订单 " + testASROrderID + " 转写失败",
					FailType:     tt.failType,
					Stage:        asrStagePoll,
				},
			}
			status := MapASRError(err)

			assert.Equal(t, tt.wantStatus, status.StatusCode)
			assert.Equal(t, tt.wantSkipRetry, status.SkipRetry)
			// 失败类型必须透传到错误信息里。
			assert.Contains(t, status.Message, "失败类型")
			assert.Contains(t, status.Message, asrFailTypeDescription(tt.failType))
		})
	}
}

// TestMapASRErrorCredentialErrorsSkipAutoDisableAndRetry 凭据类错误既不自动禁用渠道，
// 也不触发渠道重试。
func TestMapASRErrorCredentialErrorsSkipAutoDisableAndRetry(t *testing.T) {
	for _, code := range []string{"000002", "100009"} {
		t.Run(code, func(t *testing.T) {
			status := MapASRError(&ASRUpstreamError{
				ASRErrorInfo: ASRErrorInfo{UpstreamCode: code, Description: "credential rejected", Stage: asrStageUpload},
			})

			assert.False(t, operation_setting.ShouldDisableByStatusCode(status.StatusCode),
				"凭据类错误映射的状态码不能落入自动禁用范围")
			assert.True(t, operation_setting.ShouldRetryByStatusCode(status.StatusCode),
				"该状态码落在默认重试范围内，因此必须显式跳过重试")
			assert.True(t, status.SkipRetry, "凭据类错误必须显式标记跳过重试")
		})
	}
}

// TestMapASRErrorClientErrorsDoNotRetry 400 类错误不触发渠道重试。
func TestMapASRErrorClientErrorsDoNotRetry(t *testing.T) {
	for _, code := range []string{"000001", "100003", "100030"} {
		t.Run(code, func(t *testing.T) {
			status := MapASRError(&ASRUpstreamError{
				ASRErrorInfo: ASRErrorInfo{UpstreamCode: code, Description: "客户端请求有误", Stage: asrStageUpload},
			})

			assert.Equal(t, http.StatusBadRequest, status.StatusCode)
			assert.False(t, operation_setting.ShouldRetryByStatusCode(status.StatusCode),
				"400 不在默认重试范围内")
			assert.True(t, status.SkipRetry)
		})
	}
}

// TestMapASRErrorRetryableFailures 限流与上游故障计入失败次数并允许重试。
func TestMapASRErrorRetryableFailures(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "频率限制", err: &ASRUpstreamError{ASRErrorInfo: ASRErrorInfo{UpstreamCode: "100012", Description: "frequency limit"}}, wantStatus: http.StatusTooManyRequests},
		{name: "上游业务故障", err: &ASRUpstreamError{ASRErrorInfo: ASRErrorInfo{UpstreamCode: "100006", Description: "upload failed"}}, wantStatus: http.StatusBadGateway},
		{name: "上游 HTTP 故障", err: &ASRUpstreamError{HTTPStatus: 503, ASRErrorInfo: ASRErrorInfo{Description: "service unavailable"}}, wantStatus: http.StatusBadGateway},
		{name: "网络故障", err: &ASRUpstreamError{ASRErrorInfo: ASRErrorInfo{Description: "上游请求失败（网络或传输故障）"}}, wantStatus: http.StatusBadGateway},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := MapASRError(tt.err)

			assert.Equal(t, tt.wantStatus, status.StatusCode)
			assert.False(t, status.SkipRetry, "限流与上游故障不应跳过重试")
			assert.True(t, operation_setting.ShouldRetryByStatusCode(status.StatusCode),
				"5xx 与 429 都应落在默认重试范围内")
		})
	}
}

// TestMapASRErrorStageDistinction 上传失败与轮询失败可区分。
func TestMapASRErrorStageDistinction(t *testing.T) {
	upload := MapASRError(&ASRUpstreamError{
		ASRErrorInfo: ASRErrorInfo{UpstreamCode: "100009", Description: "签名校验不通过", Stage: asrStageUpload},
	})
	poll := MapASRError(&ASRUpstreamError{
		ASRErrorInfo: ASRErrorInfo{UpstreamCode: "100009", Description: "签名校验不通过", Stage: asrStagePoll},
	})

	assert.Contains(t, upload.Message, "上传")
	assert.Contains(t, poll.Message, "查询")
	assert.NotEqual(t, upload.Message, poll.Message)
}

// TestMapASRErrorUnclassified 无法分类的错误走 5xx 兜底并允许重试。
func TestMapASRErrorUnclassified(t *testing.T) {
	status := MapASRError(errors.New("未知故障"))
	assert.Equal(t, http.StatusInternalServerError, status.StatusCode)
	assert.False(t, status.SkipRetry)
	assert.True(t, operation_setting.ShouldRetryByStatusCode(status.StatusCode))
}

// TestMapASRErrorNil 空错误返回兜底结果。
func TestMapASRErrorNil(t *testing.T) {
	status := MapASRError(nil)
	assert.Equal(t, http.StatusInternalServerError, status.StatusCode)
}

// TestNewASRAPIError 网关错误携带映射后的状态码与跳过重试标记。
func TestNewASRAPIError(t *testing.T) {
	t.Run("凭据类错误", func(t *testing.T) {
		apiErr := NewASRAPIError(&ASRUpstreamError{
			ASRErrorInfo: ASRErrorInfo{UpstreamCode: "100009", Description: "签名校验不通过", Stage: asrStageUpload},
		}, types.ErrorCodeDoRequestFailed)
		require.NotNil(t, apiErr)

		assert.Equal(t, http.StatusForbidden, apiErr.StatusCode)
		assert.True(t, types.IsSkipRetryError(apiErr))
		assert.Contains(t, apiErr.Error(), "100009")
		assert.Contains(t, apiErr.Error(), "签名校验不通过")
	})

	t.Run("限流错误", func(t *testing.T) {
		apiErr := NewASRAPIError(&ASRUpstreamError{
			ASRErrorInfo: ASRErrorInfo{UpstreamCode: "100012", Description: "请求超过频率限制", Stage: asrStagePoll},
		}, types.ErrorCodeDoRequestFailed)
		require.NotNil(t, apiErr)

		assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
		assert.False(t, types.IsSkipRetryError(apiErr))
		assert.Contains(t, apiErr.Error(), "100012")
	})

	t.Run("无法分类时使用兜底错误码", func(t *testing.T) {
		apiErr := NewASRAPIError(errors.New("未知故障"), types.ErrorCodeDoRequestFailed)
		require.NotNil(t, apiErr)

		assert.Equal(t, http.StatusInternalServerError, apiErr.StatusCode)
		assert.Equal(t, types.ErrorCodeDoRequestFailed, apiErr.GetErrorCode())
		assert.False(t, types.IsSkipRetryError(apiErr))
	})

	t.Run("空错误返回 nil", func(t *testing.T) {
		assert.Nil(t, NewASRAPIError(nil, types.ErrorCodeDoRequestFailed))
	})
}

// asrErrorFakeUpstream 是按路径返回预置响应体的假上游。
type asrErrorFakeUpstream struct {
	server     *httptest.Server
	uploadBody string
	pollBody   string
	uploadCode int
	pollCode   int
	pollCount  atomic.Int64
}

// newASRErrorFakeUpstream 构造一个按路径返回预置响应体的假上游。
func newASRErrorFakeUpstream(t *testing.T, uploadBody string, pollBody string) *asrErrorFakeUpstream {
	t.Helper()

	fake := &asrErrorFakeUpstream{
		uploadBody: uploadBody,
		pollBody:   pollBody,
		uploadCode: http.StatusOK,
		pollCode:   http.StatusOK,
	}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case ASRUploadPath:
			w.WriteHeader(fake.uploadCode)
			_, _ = w.Write([]byte(fake.uploadBody))
		case ASRResultPath:
			fake.pollCount.Add(1)
			w.WriteHeader(fake.pollCode)
			_, _ = w.Write([]byte(fake.pollBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

// TestASRTranscribeUploadStageError 上传阶段的业务错误带上上游错误码与描述。
func TestASRTranscribeUploadStageError(t *testing.T) {
	fake := newASRErrorFakeUpstream(t,
		`{"code":"100009","descInfo":"signature check failed"}`,
		`{"code":"000000","descInfo":"success"}`)

	_, err := ASRTranscribe(context.Background(), asrTestCredentials(), asrTestAudio(), ASROptions{
		PollInterval: time.Millisecond,
		MaxWait:      5 * time.Second,
		BaseURL:      fake.server.URL,
	})
	require.Error(t, err)

	status := MapASRError(err)
	assert.Equal(t, http.StatusForbidden, status.StatusCode)
	assert.True(t, status.SkipRetry)
	assert.Contains(t, err.Error(), "100009")
	assert.Contains(t, err.Error(), "signature check failed")
	assert.Contains(t, err.Error(), "上传")
}

// TestASRTranscribePollStageRateLimited 轮询阶段的限流错误映射为 429 且允许重试。
func TestASRTranscribePollStageRateLimited(t *testing.T) {
	fake := newASRErrorFakeUpstream(t,
		`{"code":"000000","descInfo":"success","content":{"orderId":"`+testASROrderID+`","taskEstimateTime":100}}`,
		`{"code":"100012","descInfo":"frequency limit"}`)

	_, err := ASRTranscribe(context.Background(), asrTestCredentials(), asrTestAudio(), ASROptions{
		PollInterval: time.Millisecond,
		MaxWait:      5 * time.Second,
		BaseURL:      fake.server.URL,
	})
	require.Error(t, err)

	status := MapASRError(err)
	assert.Equal(t, http.StatusTooManyRequests, status.StatusCode)
	assert.False(t, status.SkipRetry)
	assert.Contains(t, err.Error(), "100012")
	assert.Contains(t, err.Error(), "查询")
}

// TestASRTranscribeOrderFailureClassifiesFailType 订单失败时按失败类型分类。
func TestASRTranscribeOrderFailureClassifiesFailType(t *testing.T) {
	fake := newASRErrorFakeUpstream(t,
		`{"code":"000000","descInfo":"success","content":{"orderId":"`+testASROrderID+`","taskEstimateTime":100}}`,
		`{"code":"000000","descInfo":"success","content":{"orderInfo":{"orderId":"`+testASROrderID+`","status":-1,"failType":5}}}`)

	_, err := ASRTranscribe(context.Background(), asrTestCredentials(), asrTestAudio(), ASROptions{
		PollInterval: time.Millisecond,
		MaxWait:      5 * time.Second,
		BaseURL:      fake.server.URL,
	})
	require.Error(t, err)

	status := MapASRError(err)
	assert.Equal(t, http.StatusBadRequest, status.StatusCode, "时长校验失败属于客户端请求有误")
	assert.True(t, status.SkipRetry)
	assert.Contains(t, err.Error(), "失败类型 5")
}

// TestASRTranscribeUpstreamHTTPFailure 上游返回 HTTP 5xx 时映射为 5xx 并允许重试。
func TestASRTranscribeUpstreamHTTPFailure(t *testing.T) {
	fake := newASRErrorFakeUpstream(t, `{"code":"000000","descInfo":"success"}`, `{"code":"000000","descInfo":"success"}`)
	fake.uploadCode = http.StatusBadGateway

	_, err := ASRTranscribe(context.Background(), asrTestCredentials(), asrTestAudio(), ASROptions{
		PollInterval: time.Millisecond,
		MaxWait:      5 * time.Second,
		BaseURL:      fake.server.URL,
	})
	require.Error(t, err)

	status := MapASRError(err)
	assert.GreaterOrEqual(t, status.StatusCode, 500)
	assert.False(t, status.SkipRetry)
}

// TestASRTranscribeNumericUpstreamCode 上游失败响应的业务码是数字形态时（官方失败示例
// 即如此），仍要取到错误码并完成映射，而不是让整段响应解析失败。
func TestASRTranscribeNumericUpstreamCode(t *testing.T) {
	t.Run("上传阶段的数字错误码", func(t *testing.T) {
		fake := newASRErrorFakeUpstream(t, `{"code":100009,"descInfo":"signature check failed"}`, `{"code":"000000"}`)

		_, err := ASRTranscribe(context.Background(), asrTestCredentials(), asrTestAudio(), ASROptions{
			PollInterval: time.Millisecond,
			MaxWait:      5 * time.Second,
			BaseURL:      fake.server.URL,
		})
		require.Error(t, err)

		status := MapASRError(err)
		assert.Equal(t, http.StatusForbidden, status.StatusCode)
		assert.True(t, status.SkipRetry)
		assert.Contains(t, err.Error(), "100009")
		assert.Contains(t, err.Error(), "signature check failed")
	})

	t.Run("轮询阶段的数字错误码", func(t *testing.T) {
		fake := newASRErrorFakeUpstream(t,
			`{"code":"000000","descInfo":"success","content":{"orderId":"`+testASROrderID+`","taskEstimateTime":100}}`,
			`{"code":100012,"descInfo":"frequency limit"}`)

		_, err := ASRTranscribe(context.Background(), asrTestCredentials(), asrTestAudio(), ASROptions{
			PollInterval: time.Millisecond,
			MaxWait:      5 * time.Second,
			BaseURL:      fake.server.URL,
		})
		require.Error(t, err)

		status := MapASRError(err)
		assert.Equal(t, http.StatusTooManyRequests, status.StatusCode)
		assert.False(t, status.SkipRetry)
		assert.Contains(t, err.Error(), "100012")
	})

	t.Run("数字错误码且订单未完成时继续轮询", func(t *testing.T) {
		fake := newASRErrorFakeUpstream(t,
			`{"code":"000000","descInfo":"success","content":{"orderId":"`+testASROrderID+`","taskEstimateTime":100}}`,
			`{"code":100013,"descInfo":"order not finished","content":{"orderInfo":{"orderId":"`+testASROrderID+`","status":3}}}`)

		_, err := ASRTranscribe(context.Background(), asrTestCredentials(), asrTestAudio(), ASROptions{
			PollInterval: time.Millisecond,
			MaxWait:      200 * time.Millisecond,
			BaseURL:      fake.server.URL,
		})
		// 订单始终未完成，最终以超时结束，且轮询确实发生过多次。
		require.Error(t, err)
		assert.Contains(t, err.Error(), "超时")
		assert.Greater(t, fake.pollCount.Load(), int64(1), "订单未完成时应继续轮询")
	})
}

// TestASRTranscribeRejectsInvalidAudio 音频本身不合法时返回客户端错误。
func TestASRTranscribeRejectsInvalidAudio(t *testing.T) {
	tests := []struct {
		name  string
		audio ASRAudio
	}{
		{name: "音频内容为空", audio: ASRAudio{FileName: "a.wav", Format: "wav", DurationMs: 1000}},
		{name: "音频格式不受支持", audio: ASRAudio{Data: []byte("x"), FileName: "a.xyz", Format: "xyz", DurationMs: 1000}},
		{name: "音频时长超过上限", audio: ASRAudio{Data: []byte("x"), FileName: "a.wav", Format: "wav", DurationMs: int64(6 * time.Hour / time.Millisecond)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ASRTranscribe(context.Background(), asrTestCredentials(), tt.audio, ASROptions{})
			require.Error(t, err)

			status := MapASRError(err)
			assert.Equal(t, http.StatusBadRequest, status.StatusCode)
			assert.True(t, status.SkipRetry)
			assert.False(t, operation_setting.ShouldRetryByStatusCode(status.StatusCode))
		})
	}
}

// TestASRTranscribeIncompleteCredentials 凭据不完整按凭据类错误处理。
func TestASRTranscribeIncompleteCredentials(t *testing.T) {
	_, err := ASRTranscribe(context.Background(), ASRCredentials{AppID: testASRAppID}, asrTestAudio(), ASROptions{})
	require.Error(t, err)

	status := MapASRError(err)
	assert.Equal(t, http.StatusForbidden, status.StatusCode)
	assert.True(t, status.SkipRetry)
	assert.False(t, operation_setting.ShouldDisableByStatusCode(status.StatusCode))
}
