package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOAuthTopupCheckout_ScopeForbidden(t *testing.T) {
	env := setupOAuthExtTestEnv(t, "openid profile") // no api:write
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/oauth/topup/checkout", strings.NewReader(`{"amount":100}`))
	c.Request.Header.Set("Authorization", "Bearer "+env.token)

	OAuthTopupCheckout(c)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	var body map[string]any
	require.NoError(t, common.UnmarshalJsonStr(recorder.Body.String(), &body))
	assert.Equal(t, "insufficient_scope", body["error"])
}

func TestOAuthTopupCheckout_InvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/oauth/topup/checkout", strings.NewReader(`{"amount":100}`))
	c.Request.Header.Set("Authorization", "Bearer not-a-real-jwt")

	OAuthTopupCheckout(c)

	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestOAuthTopupCheckout_NoToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/oauth/topup/checkout", strings.NewReader(`{"amount":100}`))

	OAuthTopupCheckout(c)

	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestOAuthTopupCheckout_AmountZero(t *testing.T) {
	env := setupOAuthExtTestEnv(t, "api:write")
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/oauth/topup/checkout", strings.NewReader(`{"amount":0}`))
	c.Request.Header.Set("Authorization", "Bearer "+env.token)

	OAuthTopupCheckout(c)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestOAuthTopupCheckout_AmountNegative(t *testing.T) {
	env := setupOAuthExtTestEnv(t, "api:write")
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/oauth/topup/checkout", strings.NewReader(`{"amount":-5}`))
	c.Request.Header.Set("Authorization", "Bearer "+env.token)

	OAuthTopupCheckout(c)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestOAuthTopupCheckout_InvalidJSON(t *testing.T) {
	env := setupOAuthExtTestEnv(t, "api:write")
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/oauth/topup/checkout", strings.NewReader(`not-json`))
	c.Request.Header.Set("Authorization", "Bearer "+env.token)

	OAuthTopupCheckout(c)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
}
