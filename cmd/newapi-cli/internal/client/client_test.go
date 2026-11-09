package client

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T, server, token string) *Client {
	t.Helper()
	c, err := New(server, token)
	require.NoError(t, err)
	return c
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}

func TestNewRejectsInsecureRemoteServer(t *testing.T) {
	_, err := New("http://api.example.com", "tok")
	require.Error(t, err)
	var cfgErr *ConfigError
	require.ErrorAs(t, err, &cfgErr)
	assert.Contains(t, cfgErr.Message, "https")
}

func TestNewAllowsLoopbackOverHTTP(t *testing.T) {
	for _, server := range []string{"http://localhost:3000", "http://127.0.0.1:3000", "http://[::1]:3000"} {
		c, err := New(server+"/", "tok")
		require.NoError(t, err, server)
		assert.Equal(t, server, c.BaseURL)
	}
}

func TestNewValidatesSchemeAndHost(t *testing.T) {
	for _, server := range []string{"", "api.example.com", "ftp://api.example.com", "https://"} {
		_, err := New(server, "tok")
		require.Error(t, err, server)
		var cfgErr *ConfigError
		require.ErrorAs(t, err, &cfgErr)
	}
}

func TestCheckRedirectRejectsHTTPSDowngrade(t *testing.T) {
	via := []*http.Request{{URL: mustParseURL(t, "https://api.example.com/api/user/")}}
	req := &http.Request{URL: mustParseURL(t, "http://api.example.com/api/user/")}
	err := checkRedirect(req, via)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "https to http")
}

func TestCheckRedirectRejectsCrossOrigin(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		target string
	}{
		{name: "different host", origin: "https://api.example.com/a", target: "https://evil.example.com/a"},
		{name: "different port", origin: "https://api.example.com/a", target: "https://api.example.com:8443/a"},
		{name: "different scheme", origin: "http://localhost:3000/a", target: "https://localhost:3000/a"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			via := []*http.Request{{URL: mustParseURL(t, tc.origin)}}
			req := &http.Request{URL: mustParseURL(t, tc.target)}
			require.Error(t, checkRedirect(req, via))
		})
	}
}

func TestCheckRedirectAllowsSameOrigin(t *testing.T) {
	via := []*http.Request{{URL: mustParseURL(t, "https://api.example.com/a")}}
	req := &http.Request{URL: mustParseURL(t, "https://api.example.com:443/b")}
	assert.NoError(t, checkRedirect(req, via))
}

func TestCheckRedirectLimitsHops(t *testing.T) {
	via := make([]*http.Request, maxRedirects)
	for i := range via {
		via[i] = &http.Request{URL: mustParseURL(t, "https://api.example.com/a")}
	}
	req := &http.Request{URL: mustParseURL(t, "https://api.example.com/b")}
	err := checkRedirect(req, via)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redirects")
}

func TestDoRejectsCrossOriginRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("cross-origin redirect target must not be requested")
		_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
	}))
	defer target.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/user/", http.StatusFound)
	}))
	defer origin.Close()

	_, err := newTestClient(t, origin.URL, "tok").Do(http.MethodGet, "/api/user/", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cross-origin")
}

func TestDoSuccessUnwrapsData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/user/", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "Bearer nap_test", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"message":"","data":{"total":1}}`))
	}))
	defer srv.Close()

	data, err := newTestClient(t, srv.URL, "nap_test").Do(http.MethodGet, "/api/user/", nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"total":1}`, string(data))
}

func TestDoSuccessWithoutData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"message":""}`))
	}))
	defer srv.Close()

	data, err := newTestClient(t, srv.URL, "tok").Do(http.MethodPut, "/api/option/", map[string]string{"key": "k"})
	require.NoError(t, err)
	assert.JSONEq(t, `null`, string(data))
}

func TestDoSuccessFalseMapsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"message":"code expired","code":"invalid_grant"}`))
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv.URL, "").Do(http.MethodPost, "/api/cli/exchange", map[string]string{"code": "x"})
	require.Error(t, err)
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "invalid_grant", apiErr.Code)
	assert.Equal(t, "code expired", apiErr.Message)
	assert.True(t, apiErr.IsAuth())
}

func TestDoUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"success":false,"message":"unauthorized"}`))
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv.URL, "bad").Do(http.MethodGet, "/api/user/", nil)
	require.Error(t, err)
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
	assert.Equal(t, "unauthorized", apiErr.Message)
	assert.True(t, apiErr.IsAuth())
}

func TestDoForbiddenIsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"message":"no permission"}`))
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv.URL, "tok").Do(http.MethodGet, "/api/user/", nil)
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.True(t, apiErr.IsAuth())
}

func TestDoSendsJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(body, &payload))
		assert.Equal(t, "my-token", payload["name"])
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":7}}`))
	}))
	defer srv.Close()

	data, err := newTestClient(t, srv.URL, "tok").Do(http.MethodPost, "/api/token/", json.RawMessage(`{"name":"my-token"}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":7}`, string(data))
}

func TestDoNoTokenOmitsAuthorizationHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv.URL, "").Do(http.MethodGet, "/api/status", nil)
	require.NoError(t, err)
}

func TestDoRejectsNonEnvelopeSuccess(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "html error page", body: "<html><body>login required</body></html>"},
		{name: "plain json array", body: `[1,2,3]`},
		{name: "empty body", body: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			_, err := newTestClient(t, srv.URL, "tok").Do(http.MethodGet, "/api/raw", nil)
			require.Error(t, err)
			var apiErr *Error
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, http.StatusOK, apiErr.StatusCode)
			assert.Equal(t, "http_error", apiErr.Code)
			assert.Contains(t, apiErr.Message, "200")
			assert.Contains(t, apiErr.Message, "envelope")
			if tc.body == "" {
				assert.Contains(t, apiErr.Message, "empty body")
			} else {
				assert.Contains(t, apiErr.Message, strings.TrimSpace(tc.body))
			}
		})
	}
}

func TestDoPassthroughReturnsNonEnvelopeBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[1,2,3]`))
	}))
	defer srv.Close()

	data, err := newTestClient(t, srv.URL, "tok").DoPassthrough(http.MethodGet, "/api/raw", nil)
	require.NoError(t, err)
	assert.JSONEq(t, `[1,2,3]`, string(data))
}

func TestDoPassthroughStillUnwrapsEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"total":2}}`))
	}))
	defer srv.Close()

	data, err := newTestClient(t, srv.URL, "tok").DoPassthrough(http.MethodGet, "/api/user/", nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"total":2}`, string(data))
}

func TestDoRejectsOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), maxResponseBytes+1))
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv.URL, "tok").Do(http.MethodGet, "/api/user/", nil)
	require.Error(t, err)
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "response_too_large", apiErr.Code)
	assert.Contains(t, apiErr.Message, "exceeds")
}

func TestDoNonEnvelopeHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`<html>bad gateway</html>`))
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv.URL, "tok").Do(http.MethodGet, "/api/raw", nil)
	require.Error(t, err)
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
	assert.False(t, apiErr.IsAuth())
	assert.Contains(t, apiErr.Message, "bad gateway")
}
