package channel

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
)

func TestDoRequestReturnsUpstreamRedirectWithoutFollowing(t *testing.T) {
	service.InitHttpClient()
	gin.SetMode(gin.TestMode)
	sharedClient := service.GetHttpClient()
	require.NotNil(t, sharedClient)
	require.NotNil(t, sharedClient.CheckRedirect)
	originalRedirectPolicy := reflect.ValueOf(sharedClient.CheckRedirect).Pointer()

	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetRequests.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer target.Close()

	const responseBody = "redirect response"
	tests := []int{
		http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	}

	for _, statusCode := range tests {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			targetRequests.Store(0)
			var sourceRequests atomic.Int32
			type sourceResult struct {
				body []byte
				err  error
			}
			sourceResultCh := make(chan sourceResult, 1)
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sourceRequests.Add(1)
				body, err := io.ReadAll(r.Body)
				sourceResultCh <- sourceResult{body: body, err: err}
				w.Header().Set("Location", target.URL+"/redirect-target")
				w.WriteHeader(statusCode)
				_, _ = io.WriteString(w, responseBody)
			}))
			defer source.Close()

			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)

			req, err := http.NewRequest(http.MethodPost, source.URL, bytes.NewReader([]byte("request body")))
			require.NoError(t, err)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

			resp, err := doRequest(ctx, req, info)
			require.NoError(t, err)
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			gotSource := <-sourceResultCh
			require.NoError(t, gotSource.err)

			assert.Equal(t, statusCode, resp.StatusCode)
			assert.Equal(t, target.URL+"/redirect-target", resp.Header.Get("Location"))
			assert.Equal(t, responseBody, string(body))
			assert.Equal(t, []byte("request body"), gotSource.body)
			assert.EqualValues(t, 1, sourceRequests.Load())
			assert.Zero(t, targetRequests.Load())
		})
	}

	assert.Equal(t, originalRedirectPolicy, reflect.ValueOf(sharedClient.CheckRedirect).Pointer(), "the cached client must not be mutated")
}

// doRequest 的上游响应头等待埋点：请求发出到收到响应头的毫秒数写入采集载体。
// 上游故意延迟 30ms 才回响应头，这段延迟正是"卡在上游响应头"的典型形态。
func TestDoRequestRecordsUpstreamHeaderWait(t *testing.T) {
	service.InitHttpClient()
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(30 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)

	req, err := http.NewRequest(http.MethodPost, upstream.URL, bytes.NewReader([]byte("request body")))
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	resp, err := doRequest(ctx, req, info)
	require.NoError(t, err)
	defer resp.Body.Close()

	timing := relaycommon.RequestTimingFrom(ctx)
	require.NotNil(t, timing, "the relay request must carry a timing collector")
	assert.GreaterOrEqual(t, timing.UpstreamHeaderMs.Load(), int64(30),
		"the upstream header wait must cover the upstream delay")

	snapshot := timing.Snapshot()
	assert.EqualValues(t, 0, snapshot[relaycommon.TimingKeyUpstreamFirstByteMs],
		"a non-streaming upstream response has no first upstream byte")
}

// doRequest 的传输层失败分类：上游直接断开连接时，错误码要细到
// upstream_unexpected_eof；对外文案仍然保持脱敏，真实原因只挂在管理员可见的原因摘要上。
func TestDoRequestClassifiesUpstreamTransportFailure(t *testing.T) {
	service.InitHttpClient()
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			return
		}
		_ = conn.Close()
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)

	req, err := http.NewRequest(http.MethodPost, upstream.URL, bytes.NewReader([]byte("request body")))
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	resp, err := doRequest(ctx, req, info)
	require.Error(t, err)
	require.Nil(t, resp)

	apiErr := &types.NewAPIError{}
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, types.ErrorCodeUpstreamUnexpectedEOF, apiErr.GetErrorCode(),
		"the transport failure carries a fine-grained code")
	assert.Equal(t, http.StatusInternalServerError, apiErr.StatusCode)
	assert.Equal(t, "upstream error: do request failed", apiErr.Error(),
		"the client-facing message stays masked")
	assert.Equal(t, "upstream error: do request failed", apiErr.ToOpenAIError().Message,
		"the client-facing envelope keeps the masked message")
	assert.Equal(t, types.ErrorCodeUpstreamUnexpectedEOF, apiErr.ToOpenAIError().Code,
		"the client sees the fine-grained error code")
	assert.Equal(t, "new_api_error", apiErr.ToOpenAIError().Type,
		"the client-facing error type stays unchanged")
	assert.Contains(t, apiErr.AdminReason(), "upstream closed the connection unexpectedly",
		"the operator-facing reason explains the real cause")
}

// doRequest 的 HTTP/2 分类:上游用 GOAWAY 关闭连接、或用 RST_STREAM 重置单条流时,
// 错误码必须细到 upstream_http2_goaway / upstream_http2_stream_error。这两种形态既不是
// socket 错误也不是超时,通用分类器看不到它们,只能落进兜底码。
func TestClassifyHTTP2UpstreamFailureRecognizesGoAwayAndStreamReset(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		wantCode types.ErrorCode
	}{
		{
			name:     "the upstream closes the connection with a GOAWAY frame",
			err:      fmt.Errorf("Post %q: %w", "https://upstream.example.com/v1/chat/completions", http2.GoAwayError{LastStreamID: 3, ErrCode: http2.ErrCodeNo}),
			wantCode: types.ErrorCodeUpstreamHTTP2GoAway,
		},
		{
			name:     "the upstream resets the stream",
			err:      fmt.Errorf("Post %q: %w", "https://upstream.example.com/v1/chat/completions", http2.StreamError{StreamID: 5, Code: http2.ErrCodeRefusedStream}),
			wantCode: types.ErrorCodeUpstreamHTTP2StreamError,
		},
		{
			name:     "a GOAWAY error behind a url error is still a GOAWAY failure",
			err:      &url.Error{Op: "Post", URL: "https://upstream.example.com", Err: http2.GoAwayError{LastStreamID: 1}},
			wantCode: types.ErrorCodeUpstreamHTTP2GoAway,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure, ok := classifyHTTP2UpstreamFailure(tc.err)
			require.True(t, ok, "the HTTP/2 shape has to be recognized")
			assert.Equal(t, tc.wantCode, failure.Code)
			assert.NotEmpty(t, failure.Reason, "the failure carries an operator-facing reason")
			assert.Equal(t, types.ErrorCodeDoRequestFailed, types.ClassifyUpstreamError(tc.err).Code,
				"control: the generic classifier cannot see this shape, so the HTTP/2 check is what names it")
		})
	}

	_, ok := classifyHTTP2UpstreamFailure(io.EOF)
	assert.False(t, ok, "a failure that is not an HTTP/2 shape stays unclassified here")
}

// doRequest 同时记录上游响应头到达的时刻:首包段以它为起点,于是"响应头等待"
// 与"首包等待"两段互不重叠,相加才等于上游耗时。
func TestDoRequestRecordsUpstreamHeaderMoment(t *testing.T) {
	service.InitHttpClient()
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)

	req, err := http.NewRequest(http.MethodPost, upstream.URL, bytes.NewReader([]byte("request body")))
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	resp, err := doRequest(ctx, req, info)
	require.NoError(t, err)
	defer resp.Body.Close()

	timing := relaycommon.RequestTimingFrom(ctx)
	require.NotNil(t, timing)
	headerAt := timing.UpstreamHeaderAt()
	assert.False(t, headerAt.IsZero(), "the upstream header moment must be recorded")

	timing.MarkFirstByte(headerAt.Add(250 * time.Millisecond))
	snapshot := timing.Snapshot()
	assert.EqualValues(t, 250, snapshot[relaycommon.TimingKeyUpstreamFirstByteMs],
		"the first byte segment starts at the upstream header moment, not at the request send")
}
