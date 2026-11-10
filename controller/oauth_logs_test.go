package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// --- OAuthLogs-specific setup (extends the shared one with seeded log rows) ---

func setupOAuthLogsTestEnv(t *testing.T) *oauthExtTestEnv {
	t.Helper()
	// Start from a fresh DB that also has the logs table migrated.
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Option{}, &model.OAuthApp{}, &model.Log{}))
	model.DB = db
	model.LOG_DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		_ = sqlDB.Close()
	})

	user := &model.User{
		Username: "oauth-logs-user", Password: "unused", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
		Quota: 5000, UsedQuota: 1234, RequestCount: 56, Email: "logs@example.com",
	}
	require.NoError(t, db.Create(user).Error)

	app := &model.OAuthApp{
		Name: "OAuth Logs Test App", ClientID: "logs-client",
		ClientSecretHash: model.OAuthAppSecretHash("test-secret"),
		Status:           model.OAuthAppStatusEnabled,
		CreatedBy:        user.Id,
	}
	require.NoError(t, app.SetRedirectURIs([]string{"https://app.example.com/callback"}))
	require.NoError(t, app.SetScopes([]string{"openid", "profile", "email", "api:read", "api:write"}))
	require.NoError(t, db.Create(app).Error)

	// Seed a few log rows for this user.
	now := common.GetTimestamp()
	logs := []*model.Log{
		{
			UserId: user.Id, CreatedAt: now - 100, Username: user.Username,
			TokenName: "prod-token", ModelName: "gpt-4o-mini",
			Quota: 10000, PromptTokens: 500, CompletionTokens: 300,
			UseTime: 1500, ChannelId: 1, ChannelName: "openai",
			Group: user.Group, RequestId: "req-001",
			Content: "PROMPT: Hello world\nRESPONSE: Hi there!", // sensitive
			Other:   `{"upstream_api_key":"sk-SECRETKEY123"}`,   // sensitive
			Ip:      "1.2.3.4",                                  // sensitive
		},
		{
			UserId: user.Id, CreatedAt: now - 50, Username: user.Username,
			TokenName: "prod-token", ModelName: "gpt-4o",
			Quota: 20000, PromptTokens: 800, CompletionTokens: 500,
			UseTime: 3000, ChannelId: 1, ChannelName: "openai",
			Group: user.Group, RequestId: "req-002",
			Content: "PROMPT: Another request\nRESPONSE: Some other answer!",
			Other:   `{"upstream_api_key":"sk-ANOTHERKEY999"}`,
			Ip:      "5.6.7.8",
		},
	}
	for _, l := range logs {
		require.NoError(t, db.Create(l).Error)
	}

	// We must ensure the log-time-range filter does not reject our seeded logs:
	// limitLogTimeRange is called by GetUserLogs only for non-admin roles, so
	// our rows at `now-100` / `now-50` are comfortably within the default range.
	_ = operation_setting.GetLogSetting()

	token, _, err := service.IssueOAuthAccessToken(user.Id, app.ClientID, "api:read")
	require.NoError(t, err)

	return &oauthExtTestEnv{db: db, user: user, app: app, token: token}
}

func TestOAuthLogsList_InvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/oauth/logs", nil)
	c.Request.Header.Set("Authorization", "Bearer not-a-real-jwt")

	OAuthLogsList(c)

	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestOAuthLogsList_ScopeForbidden(t *testing.T) {
	env := setupOAuthLogsTestEnv(t)
	// Issue a token WITHOUT api:read for the same app/user.
	token, _, err := service.IssueOAuthAccessToken(env.user.Id, env.app.ClientID, "openid profile")
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/oauth/logs", nil)
	c.Request.Header.Set("Authorization", "Bearer "+token)

	OAuthLogsList(c)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	var body map[string]any
	require.NoError(t, common.UnmarshalJsonStr(recorder.Body.String(), &body))
	assert.Equal(t, "insufficient_scope", body["error"])
}

func TestOAuthLogsList_WhitelistNoSensitive(t *testing.T) {
	env := setupOAuthLogsTestEnv(t)
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/oauth/logs", nil)
	c.Request.Header.Set("Authorization", "Bearer "+env.token)

	OAuthLogsList(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var body struct {
		Total int64   `json:"total"`
		Page  int     `json:"page"`
		Items []gin.H `json:"items"`
	}
	require.NoError(t, common.UnmarshalJsonStr(recorder.Body.String(), &body))

	assert.Equal(t, 1, body.Page)
	require.NotEmpty(t, body.Items)
	for _, item := range body.Items {
		// Whitelist fields must be present (per sanitizeOAuthLog).
		for _, field := range []string{"id", "created_at", "username", "token_name",
			"model_name", "quota", "prompt_tokens", "completion_tokens",
			"use_time", "channel_name", "group", "request_id"} {
			_, present := item[field]
			assert.True(t, present, "whitelist field %s must be present", field)
		}
		// Sensitive/internal fields must NOT be present.
		for _, field := range []string{"content", "other", "ip", "user_id", "token_id",
			"channel_id", "upstream_request_id", "type", "is_stream"} {
			_, present := item[field]
			assert.False(t, present, "sensitive/internal field %s must be excluded", field)
		}
	}
}
