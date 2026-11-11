package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOAuthMidjourneyWhitelistUsesOwnedTaskModels(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Midjourney{}, &model.Task{}))
	require.NoError(t, env.app.SetAIModelWhitelist([]string{"mj_imagine"}))
	require.NoError(t, model.DB.Save(env.app).Error)
	service.InvalidateOAuthAppCache(env.app.ClientID)
	t.Cleanup(func() { service.InvalidateOAuthAppCache(env.app.ClientID) })
	for _, task := range []model.Midjourney{
		{UserId: env.user.Id, AppId: env.app.Id, MjId: "allowed", Action: "IMAGINE"},
		{UserId: env.user.Id, AppId: env.app.Id, MjId: "restricted", Action: "UPSCALE"},
		{UserId: env.user.Id, AppId: env.app.Id + 1, MjId: "foreign-app", Action: "IMAGINE"},
		{UserId: env.user.Id + 1, AppId: env.app.Id, MjId: "foreign-user", Action: "IMAGINE"},
	} {
		require.NoError(t, model.DB.Create(&task).Error)
	}

	for _, tc := range []struct {
		name    string
		mode    int
		id      string
		body    string
		allowed bool
	}{
		{name: "fetch permitted model", mode: relayconstant.RelayModeMidjourneyTaskFetch, id: "allowed", allowed: true},
		{name: "image seed permitted model", mode: relayconstant.RelayModeMidjourneyTaskImageSeed, id: "allowed", allowed: true},
		{name: "fetch restricted model", mode: relayconstant.RelayModeMidjourneyTaskFetch, id: "restricted"},
		{name: "foreign app cannot supply a policy model", mode: relayconstant.RelayModeMidjourneyTaskFetch, id: "foreign-app"},
		{name: "foreign user cannot supply a policy model", mode: relayconstant.RelayModeMidjourneyTaskFetch, id: "foreign-user"},
		{name: "batch permits owned model and omits foreign task", mode: relayconstant.RelayModeMidjourneyTaskFetchByCondition, body: `{"ids":["allowed","foreign-app"]}`, allowed: true},
		{name: "batch checks every owned model", mode: relayconstant.RelayModeMidjourneyTaskFetchByCondition, body: `{"ids":["allowed","restricted"]}`},
		{name: "empty batch remains empty", mode: relayconstant.RelayModeMidjourneyTaskFetchByCondition, body: `{"ids":[]}`, allowed: true},
		{name: "malformed batch rejected", mode: relayconstant.RelayModeMidjourneyTaskFetchByCondition, body: `{`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/mj/task/list-by-condition", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Params = gin.Params{{Key: "id", Value: tc.id}}
			newOAuthPolicyContext(c, env, "")
			common.SetContextKey(c, constant.ContextKeyOAuthAppID, env.app.Id)
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
			apiErr := checkOAuthMidjourneyModelWhitelist(c, "", tc.mode)
			if !tc.allowed {
				require.NotNil(t, apiErr)
				return
			}
			require.Nil(t, apiErr)
			if tc.mode == relayconstant.RelayModeMidjourneyTaskFetchByCondition {
				require.Nil(t, relay.RelayMidjourneyTask(c, tc.mode))
				assert.NotContains(t, recorder.Body.String(), "foreign-app")
				assert.NotContains(t, recorder.Body.String(), "restricted")
			}
		})
	}
}

func TestOAuthPolicyModelDoesNotResolveForeignAppTask(t *testing.T) {
	env := setupOAuthAuthorizeTestEnv(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Task{}))
	task := &model.Task{TaskID: "foreign", UserId: env.user.Id, AppId: env.app.Id + 1,
		Properties: model.Properties{OriginModelName: "foreign-model"}}
	require.NoError(t, model.DB.Create(task).Error)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	newOAuthPolicyContext(c, env, "")
	common.SetContextKey(c, constant.ContextKeyOAuthAppID, env.app.Id)
	c.Params = gin.Params{{Key: "task_id", Value: task.TaskID}}
	assert.Empty(t, oauthPolicyModelName(c, ""))
}
