package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenCreateRouteDisablesCaching(t *testing.T) {
	setupRelayRouterTestDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.UserSession{}))
	require.NoError(t, model.MigrateAuditLogs())

	previousSecret := common.SessionSecret
	common.SessionSecret = "token-route-cache-test-secret"
	t.Cleanup(func() { common.SessionSecret = previousSecret })

	user := &model.User{
		Username:    "token-cache-user",
		Password:    "placeholder",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		AffCode:     "token-cache-user",
	}
	require.NoError(t, model.DB.Create(user).Error)
	session := &model.UserSession{
		SID:             "token-cache-session",
		UserID:          user.Id,
		Version:         1,
		UserAuthVersion: 1,
		Status:          model.UserSessionStatusActive,
		RefreshHash:     "placeholder",
		LoginMethod:     "password",
		LastActiveAt:    time.Now().Unix(),
		ExpiresAt:       time.Now().Add(time.Hour).Unix(),
	}
	require.NoError(t, model.CreateUserSession(session))
	accessToken, _, err := service.IssueAccessToken(service.AuthIdentity{
		UserID:          user.Id,
		SessionID:       session.SID,
		UserAuthVersion: 1,
		SessionVersion:  1,
	})
	require.NoError(t, err)

	engine := gin.New()
	SetApiRouter(engine)

	request := httptest.NewRequest(http.MethodPost, "/api/token/", strings.NewReader(`{"name":"cache-test","unlimited_quota":true}`))
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Header().Get("Cache-Control"), "no-store")
}
