package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupCLIAuthTest mirrors the production route wiring: the issue endpoint
// sits under UserAuth like the access token routes, the exchange endpoint is
// public, and GET /api/user/ exercises PAT scope enforcement end to end.
func setupCLIAuthTest(t *testing.T) *gin.Engine {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis := common.RedisEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, i18n.Init())
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.Log{}, &model.AuditLog{}, &model.CasbinRule{}, &model.AuthzRole{}, &model.UserAccessToken{}, &model.Option{}, &model.CLIAuthCode{}))
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, model.EnsureLegacyAccessTokenRetireAt(time.Now().Unix()))
	common.RedisEnabled = false
	previousMaster := common.IsMasterNode
	common.IsMasterNode = true
	require.NoError(t, authz.Init(db))
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.IsMasterNode = previousMaster
		common.SetDatabaseTypes(previousMain, previousLog)
		common.RedisEnabled = previousRedis
	})

	router := gin.New()
	api := router.Group("/api")
	api.POST("/cli/exchange", ExchangeCLIAuthCode)
	userRoute := api.Group("/user")
	selfRoute := userRoute.Group("/", middleware.UserAuth())
	selfRoute.POST("/self/cli/auth_codes", CreateCLIAuthCode)
	adminRoute := userRoute.Group("/", middleware.AdminAuth())
	adminRoute.GET("/", func(c *gin.Context) { common.ApiSuccess(c, gin.H{"ok": true}) })
	return router
}

func createCLIAuthTestUser(t *testing.T, username string, role int) *model.User {
	t.Helper()
	user := &model.User{Username: username, Password: "placeholder", Role: role, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, AffCode: username}
	require.NoError(t, model.DB.Create(user).Error)
	return user
}

func cliAuthRequest(router http.Handler, method, path, credential, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if credential != "" {
		request.Header.Set("Authorization", "Bearer "+credential)
	}
	request.RemoteAddr = "192.0.2.9:4567"
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func decodeCLIAuthResponse(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	return body
}

func cliAuthData(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	data, ok := body["data"].(map[string]any)
	require.True(t, ok, "response carries no data object: %v", body)
	return data
}

func issueCLIAuthCode(t *testing.T, router http.Handler, jwt, name string, scopes ...string) (string, map[string]any) {
	t.Helper()
	payload, err := common.Marshal(gin.H{"name": name, "scopes": scopes})
	require.NoError(t, err)
	response := cliAuthRequest(router, http.MethodPost, "/api/user/self/cli/auth_codes", jwt, string(payload))
	require.Equal(t, http.StatusOK, response.Code, "issue failed: %s", response.Body.String())
	data := cliAuthData(t, decodeCLIAuthResponse(t, response))
	code, _ := data["code"].(string)
	require.NotEmpty(t, code)
	return code, data
}

func exchangeCLIAuthCode(t *testing.T, router http.Handler, code string) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := common.Marshal(gin.H{"code": code})
	require.NoError(t, err)
	return cliAuthRequest(router, http.MethodPost, "/api/cli/exchange", "", string(payload))
}

func TestCLIAuthCodeIssueExchangeAndPAT(t *testing.T) {
	router := setupCLIAuthTest(t)
	admin := createCLIAuthTestUser(t, "cli-admin", common.RoleAdminUser)
	_, jwt := createAccessTokenTestSession(t, admin.Id, "cli-admin-session")

	code, data := issueCLIAuthCode(t, router, jwt, "my cli", "user:read", "usage:read")
	expiresAt := int64(data["expires_at"].(float64))
	assert.InDelta(t, time.Now().Unix()+int64(model.CLIAuthCodeTTL/time.Second), expiresAt, 30)

	// Only the hash is stored; the grant is bound to the code.
	var row model.CLIAuthCode
	require.NoError(t, model.DB.Where("code_hash = ?", model.CLIAuthCodeHash(code)).First(&row).Error)
	assert.Equal(t, admin.Id, row.UserID)
	assert.Equal(t, "my cli", row.Name)
	assert.Equal(t, []string{"usage:read", "user:read"}, row.GetScopes())
	assert.Zero(t, row.ConsumedAt)

	// Exchange succeeds once and returns a PAT carrying the bound grant.
	response := exchangeCLIAuthCode(t, router, code)
	require.Equal(t, http.StatusOK, response.Code, "exchange failed: %s", response.Body.String())
	tokenData := cliAuthData(t, decodeCLIAuthResponse(t, response))
	rawToken, _ := tokenData["token"].(string)
	require.True(t, strings.HasPrefix(rawToken, model.AccessTokenPrefix))
	assert.Positive(t, tokenData["expires_at"].(float64))

	// The PAT passes the in-scope admin route and is rejected out of scope.
	inScope := cliAuthRequest(router, http.MethodGet, "/api/user/", rawToken, "")
	assert.Equal(t, http.StatusOK, inScope.Code, "in-scope PAT call failed: %s", inScope.Body.String())

	// Replay of the same code fails with the unified error.
	replay := exchangeCLIAuthCode(t, router, code)
	assert.Equal(t, http.StatusBadRequest, replay.Code)
	assert.Equal(t, "invalid_grant", decodeCLIAuthResponse(t, replay)["code"])
}

func TestCLIAuthCodeIssueForbiddenScope(t *testing.T) {
	router := setupCLIAuthTest(t)
	user := createCLIAuthTestUser(t, "cli-common", common.RoleCommonUser)
	_, jwt := createAccessTokenTestSession(t, user.Id, "cli-common-session")

	response := cliAuthRequest(router, http.MethodPost, "/api/user/self/cli/auth_codes", jwt, `{"name":"cli","scopes":["user:read"]}`)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Equal(t, "ACCESS_TOKEN_SCOPE_FORBIDDEN", decodeCLIAuthResponse(t, response)["code"])

	var count int64
	require.NoError(t, model.DB.Model(&model.CLIAuthCode{}).Count(&count).Error)
	assert.Zero(t, count, "a rejected grant must not persist a code")
}

func TestCLIAuthCodeIssueRejectsPAT(t *testing.T) {
	router := setupCLIAuthTest(t)
	admin := createCLIAuthTestUser(t, "cli-pat-admin", common.RoleAdminUser)
	rawToken, _ := createScopedAccessToken(t, admin.Id, time.Now().Unix()+3600, "user:read")

	response := cliAuthRequest(router, http.MethodPost, "/api/user/self/cli/auth_codes", rawToken, `{"name":"cli","scopes":["user:read"]}`)
	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Equal(t, "AUTH_SESSION_REQUIRED", decodeCLIAuthResponse(t, response)["code"])
}

func TestCLIAuthCodeExchangeFailures(t *testing.T) {
	router := setupCLIAuthTest(t)
	user := createCLIAuthTestUser(t, "cli-exchange", common.RoleCommonUser)

	expired := model.GenerateCLIAuthCode()
	expiredRow := &model.CLIAuthCode{
		CodeHash:  model.CLIAuthCodeHash(expired),
		UserID:    user.Id,
		Name:      "expired",
		ExpiresAt: time.Now().Unix() - 60,
	}
	require.NoError(t, expiredRow.SetScopes([]string{"usage:read"}))
	require.NoError(t, model.CreateCLIAuthCode(expiredRow))

	cases := map[string]string{
		"unknown code": model.GenerateCLIAuthCode(),
		"expired code": expired,
		"empty code":   "",
	}
	for name, code := range cases {
		t.Run(name, func(t *testing.T) {
			response := exchangeCLIAuthCode(t, router, code)
			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.Equal(t, "invalid_grant", decodeCLIAuthResponse(t, response)["code"])
		})
	}

	t.Run("malformed body", func(t *testing.T) {
		response := cliAuthRequest(router, http.MethodPost, "/api/cli/exchange", "", `{"code":`)
		assert.Equal(t, http.StatusBadRequest, response.Code)
		assert.Equal(t, "invalid_grant", decodeCLIAuthResponse(t, response)["code"])
	})
}

func TestCLIAuthCodeExchangedPATScopeEnforced(t *testing.T) {
	router := setupCLIAuthTest(t)
	admin := createCLIAuthTestUser(t, "cli-scope-admin", common.RoleAdminUser)
	_, jwt := createAccessTokenTestSession(t, admin.Id, "cli-scope-session")

	code, _ := issueCLIAuthCode(t, router, jwt, "usage only", "usage:read")
	response := exchangeCLIAuthCode(t, router, code)
	require.Equal(t, http.StatusOK, response.Code)
	rawToken, _ := cliAuthData(t, decodeCLIAuthResponse(t, response))["token"].(string)
	require.NotEmpty(t, rawToken)

	outOfScope := cliAuthRequest(router, http.MethodGet, "/api/user/", rawToken, "")
	assert.Equal(t, http.StatusForbidden, outOfScope.Code)
}
