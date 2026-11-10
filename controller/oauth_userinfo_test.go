package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// --- shared setup for OAuth extension tests ---

type oauthExtTestEnv struct {
	db    *gorm.DB
	user  *model.User
	app   *model.OAuthApp
	token string
}

// setupOAuthExtTestEnv creates an in-memory SQLite DB with a seeded user and
// issues an OAuth access token for that user with the given scope.
func setupOAuthExtTestEnv(t *testing.T, scope string) *oauthExtTestEnv {
	t.Helper()
	previousDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Option{}, &model.OAuthApp{}))
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		_ = sqlDB.Close()
	})

	user := &model.User{
		Username: "oauth-ext-user", Password: "unused", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
		Quota: 5000, UsedQuota: 1234, RequestCount: 56, Email: "ext@example.com",
		DisplayName: "Ext User",
	}
	require.NoError(t, db.Create(user).Error)

	app := &model.OAuthApp{
		Name: "OAuth Ext Test App", ClientID: "test-client",
		ClientSecretHash: model.OAuthAppSecretHash("test-secret"),
		Status:           model.OAuthAppStatusEnabled,
		CreatedBy:        user.Id,
	}
	require.NoError(t, app.SetRedirectURIs([]string{"https://app.example.com/callback"}))
	require.NoError(t, app.SetScopes([]string{"openid", "profile", "email", "api:read", "api:write"}))
	require.NoError(t, db.Create(app).Error)

	token, _, err := service.IssueOAuthAccessToken(user.Id, app.ClientID, scope)
	require.NoError(t, err)

	return &oauthExtTestEnv{db: db, user: user, app: app, token: token}
}

func newOAuthTestRecorder(method, target, token string, body ...string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	reqBody := ""
	if len(body) > 0 {
		reqBody = body[0]
	}
	c.Request = httptest.NewRequest(method, target, nil)
	if reqBody != "" {
		c.Request = httptest.NewRequest(method, target, nil)
		c.Request.Body = httptest.NewRequest(method, target, nil).Body // no-op
	}
	c.Request.Header.Set("Authorization", "Bearer "+token)
	c.Request.Header.Set("Content-Type", "application/json")
	return recorder
}

// --- OAuthUserInfo tests ---

func TestOAuthUserInfo_WithApiReadScope(t *testing.T) {
	env := setupOAuthExtTestEnv(t, "openid profile email api:read")
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/oauth/userinfo", nil)
	c.Request.Header.Set("Authorization", "Bearer "+env.token)

	OAuthUserInfo(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var body map[string]any
	require.NoError(t, common.UnmarshalJsonStr(recorder.Body.String(), &body))

	// api:read scope → quota fields must be present.
	assert.Equal(t, float64(env.user.Quota), body["quota"])
	assert.Equal(t, float64(env.user.UsedQuota), body["used_quota"])
	assert.Equal(t, float64(env.user.RequestCount), body["request_count"])
	assert.Equal(t, env.user.Group, body["group"])

	// Other scopes still work.
	assert.Equal(t, "oauth-ext-user", body["username"])
	assert.Equal(t, "Ext User", body["name"])
	assert.Equal(t, "ext@example.com", body["email"])
	assert.Equal(t, "1", body["sub"])
}

func TestOAuthUserInfo_WithoutApiReadScope(t *testing.T) {
	env := setupOAuthExtTestEnv(t, "openid profile")
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/oauth/userinfo", nil)
	c.Request.Header.Set("Authorization", "Bearer "+env.token)

	OAuthUserInfo(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var body map[string]any
	require.NoError(t, common.UnmarshalJsonStr(recorder.Body.String(), &body))

	// Without api:read, the four quota fields must NOT be present.
	_, hasQuota := body["quota"]
	_, hasUsedQuota := body["used_quota"]
	_, hasRequestCount := body["request_count"]
	_, hasGroup := body["group"]
	assert.False(t, hasQuota, "quota should not be present without api:read")
	assert.False(t, hasUsedQuota, "used_quota should not be present without api:read")
	assert.False(t, hasRequestCount, "request_count should not be present without api:read")
	assert.False(t, hasGroup, "group should not be present without api:read")
}

func TestOAuthUserInfo_NoToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/oauth/userinfo", nil)

	OAuthUserInfo(c)

	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestOAuthUserInfo_InvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/oauth/userinfo", nil)
	c.Request.Header.Set("Authorization", "Bearer not-a-real-jwt")

	OAuthUserInfo(c)

	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
}
