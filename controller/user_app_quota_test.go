package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupUserAppQuotaTestDB creates an isolated SQLite database for the app
// wallet views. model.DB / model.LOG_DB and the database dialect are restored
// after the test.
func setupUserAppQuotaTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	require.NoError(t, i18n.Init())
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()

	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/user-app-quota.db"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	model.DB, model.LOG_DB = db, db
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.SetDatabaseTypes(previousMainType, previousLogType)
		_ = sqlDB.Close()
	})

	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Option{},
		&model.OAuthApp{},
		&model.UserAppWallet{},
		&model.Log{},
		&model.AuditLog{},
	))
	return db
}

func createUserAppQuotaUser(t *testing.T, db *gorm.DB, name string, quota, usedQuota int) *model.User {
	t.Helper()
	user := &model.User{
		Username: name, DisplayName: strings.ToUpper(name), Password: "hashed-secret",
		Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Group: "default",
		Email: name + "@example.com", AffCode: "aff-" + name, Quota: quota, UsedQuota: usedQuota, AuthVersion: 1,
	}
	require.NoError(t, db.Create(user).Error)
	return user
}

func createUserAppQuotaOAuthApp(t *testing.T, db *gorm.DB, clientID string) *model.OAuthApp {
	t.Helper()
	service.InvalidateOAuthAppCache(clientID)
	t.Cleanup(func() { service.InvalidateOAuthAppCache(clientID) })
	app := &model.OAuthApp{
		Name: "User App Quota " + clientID, ClientID: clientID,
		ClientSecretHash: model.OAuthAppSecretHash("test-secret"),
		Status:           model.OAuthAppStatusEnabled,
	}
	require.NoError(t, db.Create(app).Error)
	return app
}

func setUserAppQuotaWallet(t *testing.T, db *gorm.DB, userId, appId, quota, usedQuota int) {
	t.Helper()
	require.NoError(t, db.Create(&model.UserAppWallet{
		UserId: userId, AppId: appId, Quota: quota, UsedQuota: usedQuota,
	}).Error)
}

type userListResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Total int              `json:"total"`
		Items []map[string]any `json:"items"`
	} `json:"data"`
}

func performUserListRequest(t *testing.T, target string, handler gin.HandlerFunc) userListResponse {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	handler(c)
	require.Equal(t, http.StatusOK, recorder.Code)

	var response userListResponse
	require.NoError(t, common.UnmarshalJsonStr(recorder.Body.String(), &response))
	return response
}

func TestUsersForAppListAndSearchJoinWalletQuota(t *testing.T) {
	db := setupUserAppQuotaTestDB(t)
	alpha := createUserAppQuotaUser(t, db, "alpha", 1000, 10)
	beta := createUserAppQuotaUser(t, db, "beta", 2000, 20)
	gamma := createUserAppQuotaUser(t, db, "gamma", 3000, 30)
	const appId = 7
	setUserAppQuotaWallet(t, db, alpha.Id, appId, 50, 5)
	setUserAppQuotaWallet(t, db, gamma.Id, appId, 400, 25)

	pageOne, total, err := model.GetAllUsersForApp(appId, &common.PageInfo{Page: 1, PageSize: 2}, model.NewUserSortOptions("quota", "asc"))
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, pageOne, 2)
	assert.Equal(t, beta.Id, pageOne[0].Id)
	assert.Equal(t, 0, pageOne[0].AppQuota)
	assert.Equal(t, 0, pageOne[0].AppUsedQuota)
	assert.Equal(t, alpha.Id, pageOne[1].Id)
	assert.Equal(t, 50, pageOne[1].AppQuota)
	assert.Equal(t, 5, pageOne[1].AppUsedQuota)
	assert.Equal(t, 1000, pageOne[1].Quota)
	assert.Empty(t, pageOne[1].Password, "joined list must not expose password hashes")

	pageTwo, _, err := model.GetAllUsersForApp(appId, &common.PageInfo{Page: 2, PageSize: 2}, model.NewUserSortOptions("quota", "asc"))
	require.NoError(t, err)
	require.Len(t, pageTwo, 1)
	assert.Equal(t, gamma.Id, pageTwo[0].Id)
	assert.Equal(t, 400, pageTwo[0].AppQuota)
	assert.Equal(t, 25, pageTwo[0].AppUsedQuota)

	byUsed, _, err := model.GetAllUsersForApp(appId, &common.PageInfo{Page: 1, PageSize: 10}, model.NewUserSortOptions("used_quota", "desc"))
	require.NoError(t, err)
	require.Len(t, byUsed, 3)
	assert.Equal(t, gamma.Id, byUsed[0].Id)
	assert.Equal(t, alpha.Id, byUsed[1].Id)
	assert.Equal(t, beta.Id, byUsed[2].Id)

	search, searchTotal, err := model.SearchUsersForApp(appId, "beta", "", nil, nil, 0, 10, model.NewUserSortOptions("id", "asc"))
	require.NoError(t, err)
	assert.Equal(t, int64(1), searchTotal)
	require.Len(t, search, 1)
	assert.Equal(t, beta.Id, search[0].Id)
	assert.Equal(t, 0, search[0].AppQuota)

	response := performUserListRequest(t, fmt.Sprintf("/api/user/?p=1&page_size=10&sort_by=quota&sort_order=desc&app_id=%d", appId), GetAllUsers)
	require.True(t, response.Success)
	require.Equal(t, 3, response.Data.Total)
	require.Len(t, response.Data.Items, 3)
	assert.Equal(t, float64(gamma.Id), response.Data.Items[0]["id"])
	assert.Equal(t, float64(400), response.Data.Items[0]["app_quota"])
	assert.Equal(t, float64(25), response.Data.Items[0]["app_used_quota"])
}

func TestPlatformUserListRemainsUnchanged(t *testing.T) {
	db := setupUserAppQuotaTestDB(t)
	createUserAppQuotaUser(t, db, "delta", 300, 3)
	charlie := createUserAppQuotaUser(t, db, "charlie", 100, 1)
	createUserAppQuotaUser(t, db, "echo", 200, 2)

	users, total, err := model.GetAllUsers(&common.PageInfo{Page: 1, PageSize: 10}, model.NewUserSortOptions("quota", "asc"))
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, users, 3)
	assert.Equal(t, charlie.Id, users[0].Id)

	encoded, err := common.Marshal(users[0])
	require.NoError(t, err)
	var platformUser map[string]any
	require.NoError(t, common.Unmarshal(encoded, &platformUser))
	for _, key := range []string{"id", "username", "quota", "used_quota", "request_count", "group", "status"} {
		assert.Contains(t, platformUser, key)
	}
	assert.NotContains(t, platformUser, "app_quota")
	assert.NotContains(t, platformUser, "app_used_quota")

	response := performUserListRequest(t, "/api/user/?p=1&page_size=10&sort_by=quota&sort_order=asc", GetAllUsers)
	require.True(t, response.Success)
	require.Equal(t, 3, response.Data.Total)
	require.Len(t, response.Data.Items, 3)
	assert.Equal(t, float64(charlie.Id), response.Data.Items[0]["id"])
	assert.NotContains(t, response.Data.Items[0], "app_quota")
}

func TestAdjustUserAppQuota(t *testing.T) {
	db := setupUserAppQuotaTestDB(t)
	user := createUserAppQuotaUser(t, db, "adjust-target", 900, 0)
	app := createUserAppQuotaOAuthApp(t, db, "adjust-user-app")
	setUserAppQuotaWallet(t, db, user.Id, app.Id, 100, 0)

	adjustment, err := model.AdjustUserAppQuota(user.Id, app.Id, common.RoleRootUser, "add", 50)
	require.NoError(t, err)
	assert.Equal(t, 100, adjustment.Before)
	assert.Equal(t, 150, adjustment.After)
	quota, err := model.GetUserAppQuota(user.Id, app.Id)
	require.NoError(t, err)
	assert.Equal(t, 150, quota)
	var reloaded model.User
	require.NoError(t, db.First(&reloaded, user.Id).Error)
	assert.Equal(t, 900, reloaded.Quota, "app adjustment must not touch the platform quota")

	adjustment, err = model.AdjustUserAppQuota(user.Id, app.Id, common.RoleRootUser, "subtract", 30)
	require.NoError(t, err)
	assert.Equal(t, 150, adjustment.Before)
	assert.Equal(t, 120, adjustment.After)

	_, err = model.AdjustUserAppQuota(user.Id, app.Id, common.RoleRootUser, "subtract", 121)
	assert.ErrorIs(t, err, model.ErrUserAppQuotaInsufficient)
	quota, err = model.GetUserAppQuota(user.Id, app.Id)
	require.NoError(t, err)
	assert.Equal(t, 120, quota)

	_, err = model.AdjustUserAppQuota(user.Id, app.Id, common.RoleRootUser, "override", 0)
	require.NoError(t, err)
	quota, err = model.GetUserAppQuota(user.Id, app.Id)
	require.NoError(t, err)
	assert.Equal(t, 0, quota)

	_, err = model.AdjustUserAppQuota(user.Id, app.Id, common.RoleRootUser, "override", -1)
	assert.ErrorIs(t, err, model.ErrInvalidUserQuotaAdjustment)

	_, err = model.AdjustUserAppQuota(user.Id, app.Id, common.RoleRootUser, "override", common.MaxWalletQuota)
	require.NoError(t, err)
	_, err = model.AdjustUserAppQuota(user.Id, app.Id, common.RoleRootUser, "add", 1)
	assert.ErrorIs(t, err, model.ErrWalletQuotaLimitExceeded)
	quota, err = model.GetUserAppQuota(user.Id, app.Id)
	require.NoError(t, err)
	assert.Equal(t, common.MaxWalletQuota, quota)
	_, err = model.AdjustUserAppQuota(user.Id, app.Id, common.RoleRootUser, "override", 0)
	require.NoError(t, err)

	_, err = model.AdjustUserAppQuota(user.Id, 99999, common.RoleRootUser, "add", 1)
	assert.ErrorIs(t, err, model.ErrOAuthAppNotFound)

	_, err = model.AdjustUserAppQuota(user.Id, -1, common.RoleRootUser, "add", 1)
	assert.ErrorIs(t, err, model.ErrInvalidAppId)

	platformAdjustment, err := model.AdjustUserAppQuota(user.Id, 0, common.RoleRootUser, "add", 40)
	require.NoError(t, err)
	assert.Equal(t, 900, platformAdjustment.Before)
	assert.Equal(t, 940, platformAdjustment.After)
	quota, err = model.GetUserAppQuota(user.Id, app.Id)
	require.NoError(t, err)
	assert.Equal(t, 0, quota, "platform adjustment must not touch the app wallet")
}

func TestManageUserQuotaAcceptsAppId(t *testing.T) {
	db := setupUserAppQuotaTestDB(t)
	user := createUserAppQuotaUser(t, db, "http-adjust-target", 500, 0)
	app := createUserAppQuotaOAuthApp(t, db, "http-adjust-user-app")

	performManageQuota := func(body string) map[string]any {
		gin.SetMode(gin.TestMode)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/user/manage", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("id", 9999)
		c.Set("role", common.RoleRootUser)
		ManageUser(c)
		require.Equal(t, http.StatusOK, recorder.Code)

		var response map[string]any
		require.NoError(t, common.UnmarshalJsonStr(recorder.Body.String(), &response))
		return response
	}

	response := performManageQuota(fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"add","value":60,"app_id":%d}`, user.Id, app.Id))
	assert.Equal(t, true, response["success"])
	quota, err := model.GetUserAppQuota(user.Id, app.Id)
	require.NoError(t, err)
	assert.Equal(t, 60, quota)

	response = performManageQuota(fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"subtract","value":61,"app_id":%d}`, user.Id, app.Id))
	assert.Equal(t, false, response["success"])
	quota, err = model.GetUserAppQuota(user.Id, app.Id)
	require.NoError(t, err)
	assert.Equal(t, 60, quota)

	response = performManageQuota(fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"add","value":25}`, user.Id))
	assert.Equal(t, true, response["success"])
	var reloaded model.User
	require.NoError(t, db.First(&reloaded, user.Id).Error)
	assert.Equal(t, 525, reloaded.Quota)
}

func TestOAuthUserInfoReturnsAppQuota(t *testing.T) {
	db := setupUserAppQuotaTestDB(t)
	user := createUserAppQuotaUser(t, db, "userinfo-target", 5000, 123)
	app := createUserAppQuotaOAuthApp(t, db, "userinfo-user-app")
	setUserAppQuotaWallet(t, db, user.Id, app.Id, 777, 33)

	token, _, err := service.IssueOAuthAccessToken(user.Id, app.ClientID, "openid profile email api:read")
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/oauth/userinfo", nil)
	c.Request.Header.Set("Authorization", "Bearer "+token)
	OAuthUserInfo(c)
	require.Equal(t, http.StatusOK, recorder.Code)

	var body map[string]any
	require.NoError(t, common.UnmarshalJsonStr(recorder.Body.String(), &body))
	assert.Equal(t, float64(5000), body["quota"])
	assert.Equal(t, float64(123), body["used_quota"])
	assert.Equal(t, float64(777), body["app_quota"])
	assert.Equal(t, float64(33), body["app_used_quota"])
}
