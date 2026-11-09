package client

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoSuccessUnwrapsData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/user/", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "Bearer nap_test", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"message":"","data":{"total":1}}`))
	}))
	defer srv.Close()

	data, err := New(srv.URL, "nap_test").Do(http.MethodGet, "/api/user/", nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"total":1}`, string(data))
}

func TestDoSuccessWithoutData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"message":""}`))
	}))
	defer srv.Close()

	data, err := New(srv.URL, "tok").Do(http.MethodPut, "/api/option/", map[string]string{"key": "k"})
	require.NoError(t, err)
	assert.JSONEq(t, `null`, string(data))
}

func TestDoSuccessFalseMapsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"message":"code expired","code":"invalid_grant"}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, "").Do(http.MethodPost, "/api/cli/exchange", map[string]string{"code": "x"})
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

	_, err := New(srv.URL, "bad").Do(http.MethodGet, "/api/user/", nil)
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

	_, err := New(srv.URL, "tok").Do(http.MethodGet, "/api/user/", nil)
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

	data, err := New(srv.URL, "tok").Do(http.MethodPost, "/api/token/", json.RawMessage(`{"name":"my-token"}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":7}`, string(data))
}

func TestDoNoTokenOmitsAuthorizationHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, "").Do(http.MethodGet, "/api/status", nil)
	require.NoError(t, err)
}

func TestDoNonEnvelopePassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[1,2,3]`))
	}))
	defer srv.Close()

	data, err := New(srv.URL, "tok").Do(http.MethodGet, "/api/raw", nil)
	require.NoError(t, err)
	assert.JSONEq(t, `[1,2,3]`, string(data))
}

func TestDoNonEnvelopeHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`<html>bad gateway</html>`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, "tok").Do(http.MethodGet, "/api/raw", nil)
	require.Error(t, err)
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
	assert.False(t, apiErr.IsAuth())
	assert.Contains(t, apiErr.Message, "bad gateway")
}
