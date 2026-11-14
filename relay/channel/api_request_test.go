package channel

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubClientAdaptor implements just enough of Adaptor for the outbound client
// relay request helpers (DoApiRequest / DoFormRequest), mirroring the
// stubTaskAdaptor pattern in api_request_getbody_test.go.
type stubClientAdaptor struct {
	Adaptor
	baseURL string
}

func (s *stubClientAdaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return s.baseURL + "/v1/chat/completions", nil
}

func (s *stubClientAdaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	return nil
}

// blockingUpstream is an httptest upstream whose handler blocks until its
// request context is done (client abort) so a test can prove the outbound
// request was aborted upstream, not merely abandoned client-side. The 5s
// hang-breaker guarantees the suite can never deadlock on a regression that
// drops the cancellation binding.
type blockingUpstream struct {
	server    *httptest.Server
	started   chan struct{}
	sawCancel chan struct{}
}

func newBlockingUpstream(t *testing.T) *blockingUpstream {
	t.Helper()
	up := &blockingUpstream{
		started:   make(chan struct{}, 1),
		sawCancel: make(chan struct{}, 1),
	}
	up.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain the request body first like real upstreams do: net/http only
		// starts its background read (the client-disconnect detector that
		// cancels r.Context()) once the request body hits EOF.
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case up.started <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
			select {
			case up.sawCancel <- struct{}{}:
			default:
			}
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(up.server.Close)
	return up
}

type clientRequestResult struct {
	resp *http.Response
	err  error
}

func TestDoApiRequestAbortsUpstreamWhenClientContextCanceled(t *testing.T) {
	service.InitHttpClient()
	gin.SetMode(gin.TestMode)
	up := newBlockingUpstream(t)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(requestContext)

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	adaptor := &stubClientAdaptor{baseURL: up.server.URL}

	resultCh := make(chan clientRequestResult, 1)
	go func() {
		resp, err := DoApiRequest(adaptor, c, info, bytes.NewReader([]byte(`{"model":"test-model"}`)))
		resultCh <- clientRequestResult{resp: resp, err: err}
	}()

	select {
	case <-up.started:
	case <-time.After(5 * time.Second):
		require.Fail(t, "upstream never received the outbound request")
	}
	cancel()

	select {
	case got := <-resultCh:
		if got.resp != nil {
			got.resp.Body.Close()
		}
		require.ErrorIs(t, got.err, context.Canceled,
			"cancelling the client context must abort the in-flight upstream call with a context.Canceled error chain")
	case <-time.After(2 * time.Second):
		require.Fail(t, "DoApiRequest did not return within 2s after the client context was canceled",
			"the outbound upstream request must be bound to c.Request.Context()")
	}

	select {
	case <-up.sawCancel:
	case <-time.After(2 * time.Second):
		require.Fail(t, "upstream did not observe request-context cancellation",
			"the outbound request must be aborted upstream, not just abandoned client-side")
	}
}

func TestDoFormRequestAbortsUpstreamWhenClientContextCanceled(t *testing.T) {
	service.InitHttpClient()
	gin.SetMode(gin.TestMode)
	up := newBlockingUpstream(t)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil).WithContext(requestContext)

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	adaptor := &stubClientAdaptor{baseURL: up.server.URL}

	resultCh := make(chan clientRequestResult, 1)
	go func() {
		resp, err := DoFormRequest(adaptor, c, info, bytes.NewReader([]byte("payload=1")))
		resultCh <- clientRequestResult{resp: resp, err: err}
	}()

	select {
	case <-up.started:
	case <-time.After(5 * time.Second):
		require.Fail(t, "upstream never received the outbound request")
	}
	cancel()

	select {
	case got := <-resultCh:
		if got.resp != nil {
			got.resp.Body.Close()
		}
		require.ErrorIs(t, got.err, context.Canceled,
			"cancelling the client context must abort the in-flight form upstream call with a context.Canceled error chain")
	case <-time.After(2 * time.Second):
		require.Fail(t, "DoFormRequest did not return within 2s after the client context was canceled",
			"the outbound upstream request must be bound to c.Request.Context()")
	}

	select {
	case <-up.sawCancel:
	case <-time.After(2 * time.Second):
		require.Fail(t, "upstream did not observe request-context cancellation",
			"the outbound request must be aborted upstream, not just abandoned client-side")
	}
}

func TestNewTaskAPIRequestInheritsClientCancellation(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestContext, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestContext)

	upstream, err := newTaskAPIRequest(c, "https://provider.example/tasks", nil)
	require.NoError(t, err)
	cancel()

	require.ErrorIs(t, upstream.Context().Err(), context.Canceled)
}

func TestNewClientAPIRequestInheritsClientCancellation(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestContext, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(requestContext)

	upstream, err := newClientAPIRequest(c, "https://provider.example/v1/chat/completions", nil)
	require.NoError(t, err)
	cancel()

	require.ErrorIs(t, upstream.Context().Err(), context.Canceled)
}

func TestProcessHeaderOverride_ChannelTestSkipsPassthroughRules(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"*": "",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Empty(t, headers)
}

func TestProcessHeaderOverride_ChannelTestSkipsClientHeaderPlaceholder(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Upstream-Trace": "{client_header:X-Trace-Id}",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	_, ok := headers["x-upstream-trace"]
	require.False(t, ok)
}

func TestProcessHeaderOverride_NonTestKeepsClientHeaderPlaceholder(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Upstream-Trace": "{client_header:X-Trace-Id}",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "trace-123", headers["x-upstream-trace"])
}

func TestProcessHeaderOverride_RuntimeOverrideIsFinalHeaderMap(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{
		IsChannelTest:             false,
		UseRuntimeHeadersOverride: true,
		RuntimeHeadersOverride: map[string]any{
			"x-static":  "runtime-value",
			"x-runtime": "runtime-only",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Static": "legacy-value",
				"X-Legacy": "legacy-only",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "runtime-value", headers["x-static"])
	require.Equal(t, "runtime-only", headers["x-runtime"])
	_, exists := headers["x-legacy"]
	require.False(t, exists)
}

func TestProcessHeaderOverride_PassthroughSkipsAcceptEncoding(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")
	ctx.Request.Header.Set("Accept-Encoding", "gzip")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"*": "",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "trace-123", headers["x-trace-id"])

	_, hasAcceptEncoding := headers["accept-encoding"]
	require.False(t, hasAcceptEncoding)
}

func TestProcessHeaderOverride_PassHeadersTemplateSetsRuntimeHeaders(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	ctx.Request.Header.Set("Originator", "Codex CLI")
	ctx.Request.Header.Set("Session_id", "sess-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		RequestHeaders: map[string]string{
			"Originator": "Codex CLI",
			"Session_id": "sess-123",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ParamOverride: map[string]any{
				"operations": []any{
					map[string]any{
						"mode":  "pass_headers",
						"value": []any{"Originator", "Session_id", "X-Codex-Beta-Features"},
					},
				},
			},
			HeadersOverride: map[string]any{
				"X-Static": "legacy-value",
			},
		},
	}

	_, err := relaycommon.ApplyParamOverrideWithRelayInfo([]byte(`{"model":"gpt-4.1"}`), info)
	require.NoError(t, err)
	require.True(t, info.UseRuntimeHeadersOverride)
	require.Equal(t, "Codex CLI", info.RuntimeHeadersOverride["originator"])
	require.Equal(t, "sess-123", info.RuntimeHeadersOverride["session_id"])
	_, exists := info.RuntimeHeadersOverride["x-codex-beta-features"]
	require.False(t, exists)
	require.Equal(t, "legacy-value", info.RuntimeHeadersOverride["x-static"])

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "Codex CLI", headers["originator"])
	require.Equal(t, "sess-123", headers["session_id"])
	_, exists = headers["x-codex-beta-features"]
	require.False(t, exists)

	upstreamReq := httptest.NewRequest(http.MethodPost, "https://example.com/v1/responses", nil)
	applyHeaderOverrideToRequest(upstreamReq, headers)
	require.Equal(t, "Codex CLI", upstreamReq.Header.Get("Originator"))
	require.Equal(t, "sess-123", upstreamReq.Header.Get("Session_id"))
	require.Empty(t, upstreamReq.Header.Get("X-Codex-Beta-Features"))
}

func TestToWebSocketURL(t *testing.T) {
	for input, want := range map[string]string{
		"https://api.openai.com/v1/responses":             "wss://api.openai.com/v1/responses",
		"http://127.0.0.1:3000/v1/responses":              "ws://127.0.0.1:3000/v1/responses",
		"wss://chatgpt.com/backend-api/codex/responses":   "wss://chatgpt.com/backend-api/codex/responses",
		"ws://127.0.0.1:3000/backend-api/codex/responses": "ws://127.0.0.1:3000/backend-api/codex/responses",
	} {
		assert.Equal(t, want, toWebSocketURL(input), input)
	}
}
