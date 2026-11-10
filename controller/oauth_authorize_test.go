package controller

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type oauthAuthorizeTestEnv struct {
	app       *model.OAuthApp
	user      *model.User
	sessionID string
}

func setupOAuthAuthorizeTestEnv(t *testing.T) *oauthAuthorizeTestEnv {
	t.Helper()
	previousDB, previousRedis, previousSecret := model.DB, common.RedisEnabled, common.SessionSecret
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.UserSession{},
		&model.OAuthApp{},
		&model.OAuthAuthorizationCode{},
		&model.OAuthRefreshToken{},
		&model.OAuthConsent{},
		&model.Option{},
	))
	model.DB = db
	common.RedisEnabled = false
	common.SessionSecret = "oauth-authorize-test-secret"
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedis
		common.SessionSecret = previousSecret
		_ = sqlDB.Close()
	})

	user := &model.User{
		Username: "oauth-consent-user", Password: "unused", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
	}
	require.NoError(t, db.Create(user).Error)

	app := &model.OAuthApp{
		Name: "Consent Test App", ClientID: model.GenerateOAuthClientID(),
		ClientSecretHash: model.OAuthAppSecretHash("test-secret"), Status: model.OAuthAppStatusEnabled,
		CreatedBy: user.Id,
	}
	require.NoError(t, app.SetRedirectURIs([]string{"https://app.example.com/callback"}))
	require.NoError(t, app.SetPostLogoutRedirectURIs([]string{"https://app.example.com/logout"}))
	require.NoError(t, app.SetScopes([]string{"openid", "profile", "email", "api:read"}))
	require.NoError(t, db.Create(app).Error)

	return &oauthAuthorizeTestEnv{app: app, user: user, sessionID: "session-1"}
}

func (env *oauthAuthorizeTestEnv) consentRequest(t *testing.T, scope, csrfToken string) *http.Request {
	t.Helper()
	form := url.Values{
		"response_type":         {"code"},
		"client_id":             {env.app.ClientID},
		"redirect_uri":          {"https://app.example.com/callback"},
		"scope":                 {scope},
		"state":                 {"state-123"},
		"code_challenge":        {"challenge-123"},
		"code_challenge_method": {"S256"},
		"csrf_token":            {csrfToken},
		"consent":               {"allow"},
	}
	request := httptest.NewRequest(http.MethodPost, "http://gateway.example/oauth/authorize", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}

func (env *oauthAuthorizeTestEnv) newConsentContext(request *http.Request) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = request
	c.Set("id", env.user.Id)
	c.Set("session_id", env.sessionID)
	c.Set("auth_version", int64(1))
	c.Set("session_version", int64(1))
	c.Set("role", common.RoleCommonUser)
	return c, recorder
}

func assertNoOAuthGrant(t *testing.T) {
	t.Helper()
	var consents, codes int64
	require.NoError(t, model.DB.Model(&model.OAuthConsent{}).Count(&consents).Error)
	require.NoError(t, model.DB.Model(&model.OAuthAuthorizationCode{}).Count(&codes).Error)
	assert.Zero(t, consents)
	assert.Zero(t, codes)
}

func TestOAuthAuthorizeConsentRejectsEscalatedScope(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)
	csrfToken := service.IssueOAuthConsentCSRFToken(env.sessionID, env.app.Id)

	tests := []struct {
		name  string
		scope string
	}{
		{name: "unknown scope", scope: "openid admin"},
		{name: "scope not allowed for client", scope: "openid api:write"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, recorder := env.newConsentContext(env.consentRequest(t, test.scope, csrfToken))

			OAuthAuthorizeConsent(c)
			c.Writer.WriteHeaderNow()

			require.Equal(t, http.StatusFound, recorder.Code)
			location, err := url.Parse(recorder.Header().Get("Location"))
			require.NoError(t, err)
			assert.Equal(t, "invalid_scope", location.Query().Get("error"))
			assert.Equal(t, "state-123", location.Query().Get("state"))
			assert.Empty(t, location.Query().Get("code"))
			assertNoOAuthGrant(t)
		})
	}
}

func TestOAuthAuthorizeConsentRejectsInvalidCSRFToken(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)

	tests := []struct {
		name     string
		csrf     string
		wantCode int
	}{
		{name: "missing token", csrf: ""},
		{name: "forged token", csrf: "forged-token"},
		{name: "token for another app", csrf: service.IssueOAuthConsentCSRFToken(env.sessionID, env.app.Id+1)},
		{name: "token for another session", csrf: service.IssueOAuthConsentCSRFToken("session-2", env.app.Id)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, recorder := env.newConsentContext(env.consentRequest(t, "openid", test.csrf))

			OAuthAuthorizeConsent(c)

			assert.Equal(t, http.StatusForbidden, recorder.Code)
			assertNoOAuthGrant(t)
		})
	}
}

// Cross-site consent submissions are no longer blocked by Origin/Sec-Fetch-Site
// checks. The real defences are (1) the SameSite=Lax dashboard session cookie
// that browsers do not attach to cross-site form posts, and (2) the session-bound
// CSRF token that an attacker cannot obtain. This test verifies that a cross-site
// POST without a session bounces to the sign-in page rather than granting a code.
func TestOAuthAuthorizeConsentRejectsCrossSiteRequestWithoutSession(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)
	csrfToken := service.IssueOAuthConsentCSRFToken(env.sessionID, env.app.Id)

	tests := []struct {
		name      string
		origin    string
		fetchSite string
	}{
		{name: "cross-site origin", origin: "https://evil.example"},
		{name: "cross-site fetch metadata", fetchSite: "cross-site"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := env.consentRequest(t, "openid", csrfToken)
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.fetchSite != "" {
				request.Header.Set("Sec-Fetch-Site", test.fetchSite)
			}
			// Intentionally do not inject a session identity: a real cross-site
			// form post carries no Lax session cookie.
			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = request

			OAuthAuthorizeConsent(c)
			c.Writer.WriteHeaderNow()

			require.Equal(t, http.StatusFound, recorder.Code)
			assert.Contains(t, recorder.Header().Get("Location"), "/sign-in")
			assertNoOAuthGrant(t)
		})
	}
}

func TestOAuthAuthorizeConsentGrantsCodeForSameOriginRequest(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)
	csrfToken := service.IssueOAuthConsentCSRFToken(env.sessionID, env.app.Id)
	request := env.consentRequest(t, "profile openid", csrfToken)
	request.Header.Set("Origin", "http://gateway.example")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	c, recorder := env.newConsentContext(request)

	OAuthAuthorizeConsent(c)
	c.Writer.WriteHeaderNow()

	require.Equal(t, http.StatusFound, recorder.Code)
	location, err := url.Parse(recorder.Header().Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, "https://app.example.com/callback", location.Scheme+"://"+location.Host+location.Path)
	assert.NotEmpty(t, location.Query().Get("code"))
	assert.Equal(t, "state-123", location.Query().Get("state"))

	consented, err := model.HasOAuthConsent(env.app.Id, env.user.Id, "openid profile")
	require.NoError(t, err)
	assert.True(t, consented)
	var code model.OAuthAuthorizationCode
	require.NoError(t, model.DB.Where("app_id = ? AND user_id = ?", env.app.Id, env.user.Id).First(&code).Error)
	assert.Equal(t, "openid profile", code.Scope)
	assert.Equal(t, env.sessionID, code.SessionID)
}

func TestOAuthLogoutDoesNotTrustUnverifiedIDTokenHint(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)
	rawRefreshToken := "refresh-token-forged-hint"
	require.NoError(t, model.CreateOAuthRefreshToken(&model.OAuthRefreshToken{
		TokenHash: model.OAuthRefreshTokenHash(rawRefreshToken),
		AppID:     env.app.Id,
		UserID:    env.user.Id,
		SessionID: env.sessionID,
		Scope:     "openid",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}))

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "http://gateway.example/oauth/logout?id_token_hint=forged-id-token", nil)

	OAuthLogout(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	stored, err := model.GetOAuthRefreshTokenByHash(model.OAuthRefreshTokenHash(rawRefreshToken))
	require.NoError(t, err)
	assert.Zero(t, stored.RevokedAt)
}

func TestOAuthLogoutRevokesWithVerifiedIDTokenHint(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)
	rawRefreshToken := "refresh-token-verified-hint"
	require.NoError(t, model.CreateOAuthRefreshToken(&model.OAuthRefreshToken{
		TokenHash: model.OAuthRefreshTokenHash(rawRefreshToken),
		AppID:     env.app.Id,
		UserID:    env.user.Id,
		SessionID: env.sessionID,
		Scope:     "openid",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}))
	idToken, _, err := service.IssueOAuthIDToken(env.user.Id, env.app.ClientID, "", time.Now().Unix())
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "http://gateway.example/oauth/logout", nil)
	c.Request.URL.RawQuery = url.Values{"id_token_hint": {idToken}}.Encode()

	OAuthLogout(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	stored, err := model.GetOAuthRefreshTokenByHash(model.OAuthRefreshTokenHash(rawRefreshToken))
	require.NoError(t, err)
	assert.NotZero(t, stored.RevokedAt)
}

func TestOAuthLogoutClientAuthRequiresSessionIdentity(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)
	rawRefreshToken := "refresh-token-client-auth"
	require.NoError(t, model.CreateOAuthRefreshToken(&model.OAuthRefreshToken{
		TokenHash: model.OAuthRefreshTokenHash(rawRefreshToken),
		AppID:     env.app.Id,
		UserID:    env.user.Id,
		SessionID: env.sessionID,
		Scope:     "openid",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}))

	tests := []struct {
		name        string
		sessionUser bool
	}{
		{name: "without browser session", sessionUser: false},
		{name: "with browser session", sessionUser: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "http://gateway.example/oauth/logout", nil)
			c.Request.SetBasicAuth(env.app.ClientID, "test-secret")
			if test.sessionUser {
				c.Set("id", env.user.Id)
				c.Set("session_id", env.sessionID)
				c.Set("auth_version", int64(1))
				c.Set("session_version", int64(1))
			}

			OAuthLogout(c)

			assert.Equal(t, http.StatusOK, recorder.Code)
			stored, err := model.GetOAuthRefreshTokenByHash(model.OAuthRefreshTokenHash(rawRefreshToken))
			require.NoError(t, err)
			if test.sessionUser {
				assert.NotZero(t, stored.RevokedAt)
			} else {
				assert.Zero(t, stored.RevokedAt)
			}
		})
	}
}

func newOAuthPolicyContext(c *gin.Context, env *oauthAuthorizeTestEnv, modelName string) {
	common.SetContextKey(c, constant.ContextKeyOAuthAppClientID, env.app.ClientID)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, modelName)
	common.SetContextKey(c, constant.ContextKeyUserId, env.user.Id)
	c.Set("id", env.user.Id)
}

func TestRelayTaskRejectsModelOutsideOAuthAppWhitelist(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)
	require.NoError(t, env.app.SetAIModelWhitelist([]string{"allowed-video-model"}))
	require.NoError(t, model.DB.Save(env.app).Error)
	service.InvalidateOAuthAppCache(env.app.ClientID)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", strings.NewReader("{}"))
	c.Request.Header.Set("Content-Type", "application/json")
	newOAuthPolicyContext(c, env, "restricted-video-model")

	RelayTask(c)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "not in the OAuth app whitelist")

	allowedRecorder := httptest.NewRecorder()
	allowed, _ := gin.CreateTestContext(allowedRecorder)
	allowed.Request = c.Request
	newOAuthPolicyContext(allowed, env, "allowed-video-model")
	assert.Nil(t, checkOAuthAppModelWhitelist(allowed, "allowed-video-model"))
}

func TestOAuthAppWhitelistFailsClosedAndSkipsNonOAuthRequests(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	const missingClientID = "client-missing-policy"
	service.InvalidateOAuthAppCache(missingClientID)
	common.SetContextKey(c, constant.ContextKeyOAuthAppClientID, missingClientID)

	apiErr := checkOAuthAppModelWhitelist(c, "any-model")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusServiceUnavailable, apiErr.StatusCode)

	// Requests authenticated with a dashboard API key, personal access token, or
	// browser session do not carry the OAuth app context key and stay unaffected.
	plain, _ := gin.CreateTestContext(httptest.NewRecorder())
	assert.Nil(t, checkOAuthAppModelWhitelist(plain, "restricted-model"))

	// An app without a whitelist still allows every model.
	service.InvalidateOAuthAppCache(env.app.ClientID)
	unrestricted, _ := gin.CreateTestContext(httptest.NewRecorder())
	newOAuthPolicyContext(unrestricted, env, "restricted-model")
	assert.Nil(t, checkOAuthAppModelWhitelist(unrestricted, "restricted-model"))
}

func TestDeleteOAuthAppEvictsCacheAndRejectsAccessToken(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)
	service.InvalidateOAuthAppCache(env.app.ClientID)

	rawToken, _, err := service.IssueOAuthAccessToken(env.user.Id, env.app.ClientID, "openid")
	require.NoError(t, err)
	loaded, err := service.LoadOAuthAppByClientID(env.app.ClientID)
	require.NoError(t, err)
	require.True(t, loaded.IsEnabled())

	rawRefreshToken := "refresh-token-deleted-app"
	require.NoError(t, model.CreateOAuthRefreshToken(&model.OAuthRefreshToken{
		TokenHash: model.OAuthRefreshTokenHash(rawRefreshToken),
		AppID:     env.app.Id,
		UserID:    env.user.Id,
		SessionID: env.sessionID,
		Scope:     "openid",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}))

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	id := strconv.Itoa(env.app.Id)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/oauth/apps/"+id, nil)
	c.Params = gin.Params{{Key: "id", Value: id}}

	DeleteOAuthApp(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	_, _, err = service.ValidateOAuthAccessToken(rawToken)
	require.ErrorIs(t, err, service.ErrOAuthTokenInvalid)

	// The deleted app must not be served from the memory cache.
	_, err = service.LoadOAuthAppByClientID(env.app.ClientID)
	require.Error(t, err)

	var refreshCount int64
	require.NoError(t, model.DB.Model(&model.OAuthRefreshToken{}).Where("app_id = ?", env.app.Id).Count(&refreshCount).Error)
	assert.Zero(t, refreshCount)
}

func TestUpdateOAuthAppDisableEvictsCacheAndRevokesRefreshTokens(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)
	service.InvalidateOAuthAppCache(env.app.ClientID)

	rawToken, _, err := service.IssueOAuthAccessToken(env.user.Id, env.app.ClientID, "openid")
	require.NoError(t, err)
	loaded, err := service.LoadOAuthAppByClientID(env.app.ClientID)
	require.NoError(t, err)
	require.True(t, loaded.IsEnabled())

	rawRefreshToken := "refresh-token-disabled-app"
	require.NoError(t, model.CreateOAuthRefreshToken(&model.OAuthRefreshToken{
		TokenHash: model.OAuthRefreshTokenHash(rawRefreshToken),
		AppID:     env.app.Id,
		UserID:    env.user.Id,
		SessionID: env.sessionID,
		Scope:     "openid",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}))

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	id := strconv.Itoa(env.app.Id)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/oauth/apps/"+id, strings.NewReader(`{"scopes":["openid"],"status":2}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: id}}

	UpdateOAuthApp(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	_, _, err = service.ValidateOAuthAccessToken(rawToken)
	require.ErrorIs(t, err, service.ErrOAuthTokenInvalid)

	// The cache must reflect the disabled state immediately instead of serving
	// the previously cached enabled record.
	loaded, err = service.LoadOAuthAppByClientID(env.app.ClientID)
	require.NoError(t, err)
	assert.False(t, loaded.IsEnabled())

	stored, err := model.GetOAuthRefreshTokenByHash(model.OAuthRefreshTokenHash(rawRefreshToken))
	require.NoError(t, err)
	assert.NotZero(t, stored.RevokedAt)
}

func createOAuthTestApp(t *testing.T, env *oauthAuthorizeTestEnv, name string, publicClient bool) *model.OAuthApp {
	t.Helper()
	app := &model.OAuthApp{
		Name:             name,
		ClientID:         model.GenerateOAuthClientID(),
		ClientSecretHash: model.OAuthAppSecretHash("secret-" + name),
		Status:           model.OAuthAppStatusEnabled,
		PublicClient:     publicClient,
		CreatedBy:        env.user.Id,
	}
	require.NoError(t, app.SetRedirectURIs([]string{"https://app.example.com/callback"}))
	require.NoError(t, app.SetScopes([]string{"openid", "profile"}))
	require.NoError(t, model.DB.Create(app).Error)
	return app
}

func newOAuthFormContext(t *testing.T, path string, form url.Values) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "http://gateway.example"+path, strings.NewReader(form.Encode()))
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c, recorder
}

func TestCreateOAuthAppPersistsPublicClient(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", env.user.Id)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/oauth/apps", strings.NewReader(`{"name":"Public App","redirect_uris":["https://app.example.com/callback"],"post_logout_redirect_uris":[],"scopes":["openid"],"public_client":true}`))
	c.Request.Header.Set("Content-Type", "application/json")

	CreateOAuthApp(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Data struct {
			Id           int  `json:"id"`
			PublicClient bool `json:"public_client"`
		} `json:"data"`
		ClientSecret *string `json:"client_secret"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Data.PublicClient)
	assert.Nil(t, response.ClientSecret, "public clients must not receive a client secret")
	stored, err := model.GetOAuthAppByID(response.Data.Id)
	require.NoError(t, err)
	assert.True(t, stored.PublicClient)
	assert.Empty(t, stored.ClientSecretHash)

	_, err = model.RotateOAuthAppSecret(stored.Id)
	assert.ErrorIs(t, err, model.ErrOAuthAppPublicClient)
}

func TestUpdateOAuthAppToPublicClientClearsSecret(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)
	app := createOAuthTestApp(t, env, "Switch To Public", false)
	require.NotEmpty(t, app.ClientSecretHash)

	publicClient := true
	updated, err := model.UpdateOAuthApp(app.Id, "", nil, nil, nil, 0, &publicClient)
	require.NoError(t, err)
	assert.True(t, updated.PublicClient)
	assert.Empty(t, updated.ClientSecretHash)
}

func TestOAuthTokenPublicClientRefreshRotatesWithoutSecret(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)
	app := createOAuthTestApp(t, env, "Public SPA", true)

	rawRefreshToken := "public-refresh-token"
	require.NoError(t, model.CreateOAuthRefreshToken(&model.OAuthRefreshToken{
		TokenHash: model.OAuthRefreshTokenHash(rawRefreshToken),
		AppID:     app.Id,
		UserID:    env.user.Id,
		SessionID: env.sessionID,
		Scope:     "openid",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}))

	c, recorder := newOAuthFormContext(t, "/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {app.ClientID},
		"refresh_token": {rawRefreshToken},
	})

	OAuthToken(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.NotEmpty(t, response.AccessToken)
	require.NotEmpty(t, response.RefreshToken)
	assert.NotEqual(t, rawRefreshToken, response.RefreshToken)

	oldToken, err := model.GetOAuthRefreshTokenByHash(model.OAuthRefreshTokenHash(rawRefreshToken))
	require.NoError(t, err)
	assert.NotZero(t, oldToken.RevokedAt)

	rotated, err := model.GetOAuthRefreshTokenByHash(model.OAuthRefreshTokenHash(response.RefreshToken))
	require.NoError(t, err)
	assert.Equal(t, app.Id, rotated.AppID)
	assert.Equal(t, env.user.Id, rotated.UserID)
	assert.Zero(t, rotated.RevokedAt)
}

func TestOAuthTokenConfidentialClientRequiresSecret(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)

	tests := []struct {
		name   string
		secret string
	}{
		{name: "missing secret"},
		{name: "wrong secret", secret: "wrong-secret"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			form := url.Values{
				"grant_type":    {"refresh_token"},
				"client_id":     {env.app.ClientID},
				"refresh_token": {"unused"},
			}
			if test.secret != "" {
				form.Set("client_secret", test.secret)
			}
			c, recorder := newOAuthFormContext(t, "/oauth/token", form)

			OAuthToken(c)

			require.Equal(t, http.StatusUnauthorized, recorder.Code)
			var response struct {
				Error string `json:"error"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, oauthErrInvalidClient, response.Error)
		})
	}
}

func TestOAuthTokenPublicClientAuthorizationCodeStillRequiresPKCE(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)
	app := createOAuthTestApp(t, env, "Public PKCE App", true)

	c, recorder := newOAuthFormContext(t, "/oauth/token", url.Values{
		"grant_type":   {"authorization_code"},
		"client_id":    {app.ClientID},
		"code":         {"unused-code"},
		"redirect_uri": {"https://app.example.com/callback"},
	})

	OAuthToken(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	var response struct {
		Error string `json:"error"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, oauthErrInvalidRequest, response.Error)
}

func TestOAuthRevokePublicClientWithoutSecret(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)
	app := createOAuthTestApp(t, env, "Public Revoke App", true)

	rawRefreshToken := "public-revoke-token"
	require.NoError(t, model.CreateOAuthRefreshToken(&model.OAuthRefreshToken{
		TokenHash: model.OAuthRefreshTokenHash(rawRefreshToken),
		AppID:     app.Id,
		UserID:    env.user.Id,
		SessionID: env.sessionID,
		Scope:     "openid",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}))

	c, recorder := newOAuthFormContext(t, "/oauth/revoke", url.Values{
		"token":     {rawRefreshToken},
		"client_id": {app.ClientID},
	})

	OAuthRevoke(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	stored, err := model.GetOAuthRefreshTokenByHash(model.OAuthRefreshTokenHash(rawRefreshToken))
	require.NoError(t, err)
	assert.NotZero(t, stored.RevokedAt)
}

func TestOAuthConsentContextReturnsDisplayMetadataAndFreshCSRFToken(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)
	env.app.LogoURL = "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("logo"))
	require.NoError(t, model.DB.Save(env.app).Error)

	request := httptest.NewRequest(http.MethodGet,
		"http://gateway.example/oauth/consent/context?client_id="+url.QueryEscape(env.app.ClientID), nil)
	c, recorder := env.newConsentContext(request)

	OAuthConsentContext(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Name      string `json:"name"`
			LogoURL   string `json:"logo_url"`
			CSRFToken string `json:"csrf_token"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
	assert.True(t, body.Success)
	assert.Equal(t, env.app.Name, body.Data.Name)
	assert.Equal(t, env.app.LogoURL, body.Data.LogoURL)
	require.NoError(t, service.ValidateOAuthConsentCSRFToken(body.Data.CSRFToken, env.sessionID, env.app.Id))
}

func TestOAuthConsentContextRejectsAnonymousCrossSiteAndUnknownClient(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)
	gin.SetMode(gin.TestMode)

	t.Run("anonymous", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet,
			"http://gateway.example/oauth/consent/context?client_id="+url.QueryEscape(env.app.ClientID), nil)

		OAuthConsentContext(c)

		assert.Equal(t, http.StatusUnauthorized, recorder.Code)
	})

	t.Run("cross-site with session is allowed", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet,
			"http://gateway.example/oauth/consent/context?client_id="+url.QueryEscape(env.app.ClientID), nil)
		request.Header.Set("Sec-Fetch-Site", "cross-site")
		// When a real browser cross-site-fetches this endpoint the Lax session
		// cookie is not sent and the handler returns 401. Here the session
		// identity is injected by the test harness so we only verify the
		// endpoint's business logic.
		c, recorder := env.newConsentContext(request)

		OAuthConsentContext(c)

		assert.Equal(t, http.StatusOK, recorder.Code)
	})

	t.Run("unknown client", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet,
			"http://gateway.example/oauth/consent/context?client_id=unknown", nil)
		c, recorder := env.newConsentContext(request)

		OAuthConsentContext(c)

		var body struct {
			Success bool `json:"success"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
		assert.False(t, body.Success)
	})
}

func TestValidateOAuthAppLogo(t *testing.T) {
	validPNG := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("png"))
	tests := []struct {
		name    string
		logo    string
		wantErr bool
	}{
		{name: "empty means no logo", logo: ""},
		{name: "https URL", logo: "https://cdn.example.com/logo.png"},
		{name: "inline PNG", logo: validPNG},
		{name: "inline WebP", logo: "data:image/webp;base64," + base64.StdEncoding.EncodeToString([]byte("webp"))},
		{name: "plain http is rejected", logo: "http://cdn.example.com/logo.png", wantErr: true},
		{name: "javascript scheme is rejected", logo: "javascript:alert(1)", wantErr: true},
		{name: "SVG data URI is rejected", logo: "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte("<svg/>")), wantErr: true},
		{name: "invalid base64 is rejected", logo: "data:image/png;base64,***", wantErr: true},
		{name: "oversized value is rejected", logo: "https://cdn.example.com/" + strings.Repeat("a", model.OAuthAppLogoMaxLength), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateOAuthAppLogo(test.logo)
			if test.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestOAuthDiscoveryAdvertisesNoneAuthMethod(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "http://gateway.example/.well-known/openid-configuration", nil)

	OAuthDiscovery(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var metadata struct {
		TokenEndpointAuthMethods []string `json:"token_endpoint_auth_methods_supported"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &metadata))
	assert.Contains(t, metadata.TokenEndpointAuthMethods, "none")
}
