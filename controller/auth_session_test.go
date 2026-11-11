package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAuthLogoutRejectsRefreshCookieSessionMismatch(t *testing.T) {
	previousDB := model.DB
	previousRedis := common.RedisEnabled
	previousSecret := common.SessionSecret
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}))
	model.DB = db
	common.RedisEnabled = false
	common.SessionSecret = "auth-logout-mismatch-test-secret"
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedis
		common.SessionSecret = previousSecret
	})

	user := &model.User{
		Username: "logout-mismatch-user", Password: "unused", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
	}
	require.NoError(t, db.Create(user).Error)
	sessionA, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "agent-a")
	require.NoError(t, err)
	sessionB, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "agent-b")
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/auth/logout", nil)
	c.Request.Header.Set("Authorization", "Bearer "+sessionA.AccessToken)
	c.Request.Header.Set("X-Auth-Session", sessionA.Session.SID)
	c.Request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: sessionB.RefreshToken})

	AuthLogout(c)

	assert.Equal(t, http.StatusConflict, recorder.Code)
	var response struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(t, "AUTH_SESSION_MISMATCH", response.Code)
	for _, sid := range []string{sessionA.Session.SID, sessionB.Session.SID} {
		stored, err := model.GetUserSessionBySID(sid)
		require.NoError(t, err)
		assert.Equal(t, model.UserSessionStatusActive, stored.Status)
	}
}

func TestAuthLogoutAuthenticatesCookieBeforeOAuthSideEffects(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)
	login, err := service.CreateLoginSession(env.user.Id, "password", "127.0.0.1", "logout-test")
	require.NoError(t, err)
	otherLogin, err := service.CreateLoginSession(env.user.Id, "password", "127.0.0.1", "other-login")
	require.NoError(t, err)
	for i, session := range []service.LoginSessionView{login.Session, otherLogin.Session} {
		require.NoError(t, model.CreateOAuthRefreshToken(&model.OAuthRefreshToken{
			TokenHash: model.OAuthRefreshTokenHash(session.SID), AppID: env.app.Id, UserID: env.user.Id,
			SessionID: session.SID, AuthVersion: 1, AuthTime: session.CreatedAt, Scope: "openid", ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}))
		assert.NotEmpty(t, session.SID, "session %d", i)
	}
	logout := func(cookie, bearer string) {
		c, recorder := newOAuthFormContext(t, "/api/user/auth/logout", nil)
		if cookie != "" {
			c.Request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: cookie})
		}
		if bearer != "" {
			c.Request.Header.Set("Authorization", "Bearer "+bearer)
		}
		AuthLogout(c)
		require.Equal(t, http.StatusOK, recorder.Code)
	}
	logout(login.Session.SID+".wrong-secret", "")
	var active int64
	require.NoError(t, model.DB.Model(&model.OAuthRefreshToken{}).Where("revoked_at = 0").Count(&active).Error)
	assert.Equal(t, int64(2), active, "a known SID without its secret cannot revoke any grants")
	stored, err := model.GetUserSessionBySID(login.Session.SID)
	require.NoError(t, err)
	assert.Equal(t, model.UserSessionStatusActive, stored.Status)

	logout(login.RefreshToken, "")
	stored, err = model.GetUserSessionBySID(login.Session.SID)
	require.NoError(t, err)
	assert.Equal(t, model.UserSessionStatusRevoked, stored.Status)
	require.NoError(t, model.DB.Model(&model.OAuthRefreshToken{}).Where("revoked_at = 0").Count(&active).Error)
	assert.Zero(t, active, "authenticated logout revokes user-wide OAuth refresh grants")

	// Grants from a later login cannot be mutated by retrying the old cookie or bearer.
	require.NoError(t, model.CreateOAuthRefreshToken(&model.OAuthRefreshToken{
		TokenHash: "post-logout-grant", AppID: env.app.Id, UserID: env.user.Id,
		SessionID: otherLogin.Session.SID, AuthVersion: 1, AuthTime: otherLogin.Session.CreatedAt,
		Scope: "openid", ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}))
	logout(login.RefreshToken, "")
	logout("", login.AccessToken)
	newGrant, err := model.GetOAuthRefreshTokenByHash("post-logout-grant")
	require.NoError(t, err)
	assert.Zero(t, newGrant.RevokedAt, "idempotent logout must not touch grants from later logins")
}

func TestWriteAuthSessionErrorMapsSessionGrowthLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name           string
		err            error
		expectedStatus int
		expectedCode   string
	}{
		{
			name:           "active session limit",
			err:            model.ErrUserSessionLimit,
			expectedStatus: http.StatusConflict,
			expectedCode:   "AUTH_SESSION_LIMIT",
		},
		{
			name:           "issuance limit",
			err:            model.ErrUserSessionIssuanceLimit,
			expectedStatus: http.StatusTooManyRequests,
			expectedCode:   "AUTH_SESSION_ISSUANCE_LIMIT",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			writeAuthSessionError(c, test.err)

			assert.Equal(t, test.expectedStatus, recorder.Code)
			var response struct {
				Success bool   `json:"success"`
				Code    string `json:"code"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.False(t, response.Success)
			assert.Equal(t, test.expectedCode, response.Code)
		})
	}
}

func TestSessionLimitDoesNotRecordRejectedLoginAsSuccessful(t *testing.T) {
	previousDB := model.DB
	previousRedis := common.RedisEnabled
	previousActiveLimit := common.UserSessionActiveLimit
	previousIssuanceLimit := common.UserSessionIssuanceLimit
	previousIssuanceWindow := common.UserSessionIssuanceWindowSeconds
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.TwoFA{}, &model.PasskeyCredential{}))
	model.DB = db
	common.RedisEnabled = false
	common.UserSessionActiveLimit = 1
	common.UserSessionIssuanceLimit = 100
	common.UserSessionIssuanceWindowSeconds = int64(common.DefaultUserSessionIssuanceWindowSeconds)
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedis
		common.UserSessionActiveLimit = previousActiveLimit
		common.UserSessionIssuanceLimit = previousIssuanceLimit
		common.UserSessionIssuanceWindowSeconds = previousIssuanceWindow
	})

	const previousLastLoginAt = int64(123)
	user := &model.User{
		Username: "rejected-login-audit-user", Password: "unused", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, LastLoginAt: previousLastLoginAt,
	}
	require.NoError(t, db.Create(user).Error)
	now := time.Now().Unix()
	require.NoError(t, db.Create(&model.UserSession{
		SID: "existing-active-session", UserID: user.Id, Version: 1, UserAuthVersion: user.AuthVersion,
		Status: model.UserSessionStatusActive, RefreshHash: "hash", LoginMethod: "password",
		CreatedAt: now, LastActiveAt: now, ExpiresAt: now + 3600,
	}).Error)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/login", nil)
	setupLogin(user, nil, c)

	assert.Equal(t, http.StatusConflict, recorder.Code)
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, previousLastLoginAt, stored.LastLoginAt)
}
