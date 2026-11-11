package controller

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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

func setupOAuthPurchaseTestEnv(t *testing.T, scope string) *oauthExtTestEnv {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis := common.RedisEnabled
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	payment := operation_setting.GetPaymentSetting()
	previousPayment := *payment
	payment.ComplianceConfirmed = true
	payment.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	t.Cleanup(func() { *payment = previousPayment })

	// File-backed SQLite keeps multiple connections pointing at the same data,
	// which the subscription transaction needs because it reads database time.
	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "oauth-purchase.db")) + "?_pragma=busy_timeout(5000)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	model.DB, model.LOG_DB = db, db
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		common.RedisEnabled = previousRedis
		_ = sqlDB.Close()
	})

	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Option{},
		&model.OAuthApp{},
		&model.UserAppWallet{},
		&model.SubscriptionPlan{},
		&model.SubscriptionOrder{},
		&model.UserSubscription{},
		&model.AppProduct{},
		&model.AppProductOrder{},
		&model.TopUp{},
		&model.Log{},
	))

	user := &model.User{
		Username: "oauth-purchase-user", Password: "unused", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
		Quota: 5000, UsedQuota: 0, Email: "purchase@example.com", DisplayName: "Purchase User",
	}
	require.NoError(t, db.Create(user).Error)

	app := &model.OAuthApp{
		Name: "OAuth Purchase Test App", ClientID: "purchase-client",
		ClientSecretHash: model.OAuthAppSecretHash("purchase-secret"),
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

func newOAuthPurchaseContext(t *testing.T, method, target, token, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	return c, recorder
}

func createOAuthTestPlan(t *testing.T, env *oauthExtTestEnv, appId int, priceAmount float64) *model.SubscriptionPlan {
	t.Helper()
	plan := &model.SubscriptionPlan{
		AppId:            appId,
		Title:            "App Plan",
		PriceAmount:      priceAmount,
		Currency:         "USD",
		DurationUnit:     model.SubscriptionDurationMonth,
		DurationValue:    1,
		Enabled:          true,
		TotalAmount:      1000,
		QuotaResetPeriod: model.SubscriptionResetNever,
	}
	require.NoError(t, env.db.Create(plan).Error)
	model.InvalidateSubscriptionPlanCache(plan.Id)
	return plan
}

func createOAuthTestProduct(t *testing.T, env *oauthExtTestEnv, appId int) *model.AppProduct {
	t.Helper()
	product := &model.AppProduct{
		AppId:              appId,
		Name:               "App Product",
		Description:        "Quota package",
		PriceAmount:        0.5,
		Currency:           "USD",
		QuotaAmount:        1000,
		Enabled:            true,
		MaxPurchasePerUser: 1,
	}
	require.NoError(t, env.db.Create(product).Error)
	return product
}

func TestOAuthAppPurchase_ScopeForbidden(t *testing.T) {
	env := setupOAuthPurchaseTestEnv(t, "openid profile")

	cases := []struct {
		name   string
		body   string
		handle gin.HandlerFunc
	}{
		{"subscription checkout", `{"plan_id":1,"payment_method":"balance"}`, OAuthSubscriptionCheckout},
		{"product list", ``, OAuthProductList},
		{"product checkout", `{"product_id":1,"payment_method":"balance"}`, OAuthProductCheckout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, recorder := newOAuthPurchaseContext(t, http.MethodPost, "/oauth/purchase", env.token, tc.body)
			tc.handle(c)
			require.Equal(t, http.StatusForbidden, recorder.Code)

			var body map[string]any
			require.NoError(t, common.UnmarshalJsonStr(recorder.Body.String(), &body))
			assert.Equal(t, "insufficient_scope", body["error"])
		})
	}

	var orderCount int64
	require.NoError(t, env.db.Model(&model.SubscriptionOrder{}).Count(&orderCount).Error)
	assert.Zero(t, orderCount)
	var productOrderCount int64
	require.NoError(t, env.db.Model(&model.AppProductOrder{}).Count(&productOrderCount).Error)
	assert.Zero(t, productOrderCount)
}

func TestOAuthSubscriptionCheckout_CrossAppForbidden(t *testing.T) {
	env := setupOAuthPurchaseTestEnv(t, "api:write")
	otherPlan := createOAuthTestPlan(t, env, env.app.Id+1, 1)

	c, recorder := newOAuthPurchaseContext(t, http.MethodPost, "/oauth/subscription/checkout", env.token,
		`{"plan_id":`+strconv.Itoa(otherPlan.Id)+`,"payment_method":"balance"}`)
	OAuthSubscriptionCheckout(c)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	var body map[string]any
	require.NoError(t, common.UnmarshalJsonStr(recorder.Body.String(), &body))
	assert.Equal(t, "access_denied", body["error"])

	var orderCount int64
	require.NoError(t, env.db.Model(&model.SubscriptionOrder{}).Count(&orderCount).Error)
	assert.Zero(t, orderCount)
}

func TestOAuthProductCheckout_CrossAppForbidden(t *testing.T) {
	env := setupOAuthPurchaseTestEnv(t, "api:write")
	otherProduct := createOAuthTestProduct(t, env, env.app.Id+1)

	c, recorder := newOAuthPurchaseContext(t, http.MethodPost, "/oauth/products/checkout", env.token,
		`{"product_id":`+strconv.Itoa(otherProduct.Id)+`,"payment_method":"balance"}`)
	OAuthProductCheckout(c)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	var body map[string]any
	require.NoError(t, common.UnmarshalJsonStr(recorder.Body.String(), &body))
	assert.Equal(t, "access_denied", body["error"])

	var orderCount int64
	require.NoError(t, env.db.Model(&model.AppProductOrder{}).Count(&orderCount).Error)
	assert.Zero(t, orderCount)
}

func TestOAuthSubscriptionCheckout_BalanceDeductsAppWallet(t *testing.T) {
	env := setupOAuthPurchaseTestEnv(t, "api:write")
	plan := createOAuthTestPlan(t, env, env.app.Id, 1)
	require.NoError(t, model.IncreaseUserAppQuota(env.user.Id, env.app.Id, 600000))

	c, recorder := newOAuthPurchaseContext(t, http.MethodPost, "/oauth/subscription/checkout", env.token,
		`{"plan_id":`+strconv.Itoa(plan.Id)+`,"payment_method":"balance"}`)
	OAuthSubscriptionCheckout(c)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	walletQuota, err := model.GetUserAppQuota(env.user.Id, env.app.Id)
	require.NoError(t, err)
	assert.Equal(t, 100000, walletQuota)

	var subscription model.UserSubscription
	require.NoError(t, env.db.Where("user_id = ? AND app_id = ?", env.user.Id, env.app.Id).First(&subscription).Error)
	assert.Equal(t, plan.Id, subscription.PlanId)

	var order model.SubscriptionOrder
	require.NoError(t, env.db.Where("user_id = ? AND app_id = ?", env.user.Id, env.app.Id).First(&order).Error)
	assert.Equal(t, model.PaymentProviderBalance, order.PaymentProvider)
}

func TestOAuthProductCheckout_BalanceCreditsExactlyOnce(t *testing.T) {
	env := setupOAuthPurchaseTestEnv(t, "api:write")
	product := createOAuthTestProduct(t, env, env.app.Id)
	require.NoError(t, model.IncreaseUserAppQuota(env.user.Id, env.app.Id, 300000))

	c, recorder := newOAuthPurchaseContext(t, http.MethodPost, "/oauth/products/checkout", env.token,
		`{"product_id":`+strconv.Itoa(product.Id)+`,"payment_method":"balance"}`)
	OAuthProductCheckout(c)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var body map[string]any
	require.NoError(t, common.UnmarshalJsonStr(recorder.Body.String(), &body))
	tradeNo, _ := body["order_id"].(string)
	require.NotEmpty(t, tradeNo)

	// Price 0.5 USD -> 250000 quota deducted, snapshot quota 1000 credited.
	walletQuota, err := model.GetUserAppQuota(env.user.Id, env.app.Id)
	require.NoError(t, err)
	assert.Equal(t, 300000-250000+1000, walletQuota)

	// Completing the same successful order again must not credit a second time.
	require.NoError(t, model.CompleteAppProductOrder(tradeNo, "", model.PaymentProviderBalance, 0))
	walletQuota, err = model.GetUserAppQuota(env.user.Id, env.app.Id)
	require.NoError(t, err)
	assert.Equal(t, 51000, walletQuota)
}

func TestOAuthTopupCheckout_BalanceConvertsPlatformQuota(t *testing.T) {
	env := setupOAuthPurchaseTestEnv(t, "api:write")
	require.NoError(t, env.db.Model(&model.User{}).Where("id = ?", env.user.Id).Update("quota", 2000000).Error)

	c, recorder := newOAuthPurchaseContext(t, http.MethodPost, "/oauth/topup/checkout", env.token,
		`{"amount":2,"payment_method":"balance"}`)
	OAuthTopupCheckout(c)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	walletQuota, err := model.GetUserAppQuota(env.user.Id, env.app.Id)
	require.NoError(t, err)
	assert.Equal(t, 1000000, walletQuota)

	// The app credit must be funded by the platform balance, never minted.
	var platformUser model.User
	require.NoError(t, env.db.Where("id = ?", env.user.Id).First(&platformUser).Error)
	assert.Equal(t, 1000000, platformUser.Quota)

	var topUp model.TopUp
	require.NoError(t, env.db.Where("user_id = ? AND app_id = ?", env.user.Id, env.app.Id).First(&topUp).Error)
	assert.Equal(t, common.TopUpStatusSuccess, topUp.Status)
	assert.Equal(t, model.PaymentProviderBalance, topUp.PaymentProvider)
}

func TestOAuthTopupCheckout_BalanceRejectsInsufficientPlatformQuota(t *testing.T) {
	env := setupOAuthPurchaseTestEnv(t, "api:write")

	// The fixture user only holds 5000 platform quota, far below the 1000000
	// quota requested by a 2 USD top-up.
	c, recorder := newOAuthPurchaseContext(t, http.MethodPost, "/oauth/topup/checkout", env.token,
		`{"amount":2,"payment_method":"balance"}`)
	OAuthTopupCheckout(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())

	walletQuota, err := model.GetUserAppQuota(env.user.Id, env.app.Id)
	require.NoError(t, err)
	assert.Zero(t, walletQuota)

	var platformUser model.User
	require.NoError(t, env.db.Where("id = ?", env.user.Id).First(&platformUser).Error)
	assert.Equal(t, 5000, platformUser.Quota)

	var topUpCount int64
	require.NoError(t, env.db.Model(&model.TopUp{}).Where("user_id = ?", env.user.Id).Count(&topUpCount).Error)
	assert.Zero(t, topUpCount)
}

func TestOAuthPurchasesRejectUnconfirmedCompliance(t *testing.T) {
	env := setupOAuthPurchaseTestEnv(t, "api:write")
	plan := createOAuthTestPlan(t, env, env.app.Id, 0)
	product := createOAuthTestProduct(t, env, env.app.Id)
	for _, state := range []struct {
		name      string
		confirmed bool
		version   string
	}{
		{"unconfirmed", false, operation_setting.CurrentComplianceTermsVersion},
		{"outdated terms", true, "outdated"},
	} {
		t.Run(state.name, func(t *testing.T) {
			payment := operation_setting.GetPaymentSetting()
			payment.ComplianceConfirmed, payment.ComplianceTermsVersion = state.confirmed, state.version
			for _, tc := range []struct {
				name   string
				body   string
				handle gin.HandlerFunc
			}{
				{"subscription", `{"plan_id":` + strconv.Itoa(plan.Id) + `,"payment_method":"balance"}`, OAuthSubscriptionCheckout},
				{"product", `{"product_id":` + strconv.Itoa(product.Id) + `,"payment_method":"balance"}`, OAuthProductCheckout},
				{"topup", `{"amount":2,"payment_method":"balance"}`, OAuthTopupCheckout},
			} {
				t.Run(tc.name, func(t *testing.T) {
					c, recorder := newOAuthPurchaseContext(t, http.MethodPost, "/oauth/checkout", env.token, tc.body)
					tc.handle(c)
					assert.Equal(t, http.StatusBadRequest, recorder.Code)
					var response map[string]any
					require.NoError(t, common.UnmarshalJsonStr(recorder.Body.String(), &response), "exactly one JSON response")
					assert.Equal(t, "checkout_failed", response["error"])
					assert.Contains(t, response["error_description"], "administrator must confirm compliance terms")
				})
			}
			_, _, err := createEpaySubscriptionOrder(env.user.Id, plan, "alipay")
			assert.ErrorContains(t, err, "administrator must confirm compliance terms")
			_, err = createStripeSubscriptionOrder(t.Context(), env.user, plan)
			assert.ErrorContains(t, err, "administrator must confirm compliance terms")
			_, _, err = createCreemSubscriptionOrder(t.Context(), env.user, plan)
			assert.ErrorContains(t, err, "administrator must confirm compliance terms")
			_, err = createWaffoPancakeSubscriptionOrder(t.Context(), env.user, plan)
			assert.ErrorContains(t, err, "administrator must confirm compliance terms")
			_, err = CreateAppProductWaffoPancakeCheckout(t.Context(), env.user, product)
			assert.ErrorContains(t, err, "administrator must confirm compliance terms")
		})
	}
	for _, table := range []any{&model.TopUp{}, &model.SubscriptionOrder{}, &model.AppProductOrder{}, &model.UserSubscription{}, &model.UserAppWallet{}} {
		var count int64
		require.NoError(t, env.db.Model(table).Count(&count).Error)
		assert.Zero(t, count)
	}
}

func TestAdminCreateAppProductPreservesEnabledChoice(t *testing.T) {
	env := setupOAuthPurchaseTestEnv(t, "api:write")
	for _, tc := range []struct {
		name, enabled string
		want          bool
	}{
		{"omitted", "", true}, {"disabled", `,"enabled":false`, false}, {"enabled", `,"enabled":true`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, recorder := newOAuthPurchaseContext(t, http.MethodPost, "/api/app-products", "",
				`{"app_id":`+strconv.Itoa(env.app.Id)+`,"name":"`+tc.name+`","quota_amount":100`+tc.enabled+`}`)
			AdminCreateAppProduct(c)
			var response struct {
				Success bool             `json:"success"`
				Data    model.AppProduct `json:"data"`
			}
			require.NoError(t, common.UnmarshalJsonStr(recorder.Body.String(), &response))
			require.True(t, response.Success, recorder.Body.String())
			assert.Equal(t, tc.want, response.Data.Enabled)
			stored, err := model.GetAppProductById(response.Data.Id)
			require.NoError(t, err)
			assert.Equal(t, tc.want, stored.Enabled)
		})
	}
}

func TestGetSubscriptionSelf_ExcludesAppSubscriptions(t *testing.T) {
	env := setupOAuthPurchaseTestEnv(t, "openid")
	now := time.Now().Unix()
	platformSub := &model.UserSubscription{
		AppId: 0, UserId: env.user.Id, PlanId: 101,
		AmountTotal: 100, StartTime: now - 60, EndTime: now + 3600, Status: "active",
	}
	appSub := &model.UserSubscription{
		AppId: env.app.Id, UserId: env.user.Id, PlanId: 102,
		AmountTotal: 100, StartTime: now - 60, EndTime: now + 3600, Status: "active",
	}
	require.NoError(t, env.db.Create(platformSub).Error)
	require.NoError(t, env.db.Create(appSub).Error)

	c, recorder := newOAuthPurchaseContext(t, http.MethodGet, "/api/subscription/self", env.token, "")
	c.Set("id", env.user.Id)
	GetSubscriptionSelf(c)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Subscriptions    []model.SubscriptionSummary `json:"subscriptions"`
			AllSubscriptions []model.SubscriptionSummary `json:"all_subscriptions"`
		} `json:"data"`
	}
	require.NoError(t, common.UnmarshalJsonStr(recorder.Body.String(), &body))

	require.Len(t, body.Data.Subscriptions, 1)
	require.NotNil(t, body.Data.Subscriptions[0].Subscription)
	assert.Zero(t, body.Data.Subscriptions[0].Subscription.AppId)
	require.Len(t, body.Data.AllSubscriptions, 1)
	require.NotNil(t, body.Data.AllSubscriptions[0].Subscription)
	assert.Zero(t, body.Data.AllSubscriptions[0].Subscription.AppId)
}
