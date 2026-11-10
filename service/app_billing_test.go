package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func prepareAppBillingTest(t *testing.T) {
	t.Helper()
	require.NoError(t, model.DB.AutoMigrate(
		&model.UserAppWallet{},
		&model.SubscriptionPlan{},
		&model.SubscriptionOrder{},
		&model.UserSubscription{},
		&model.TopUp{},
	))
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM user_app_wallets")
		model.DB.Exec("DELETE FROM user_subscriptions")
		model.DB.Exec("DELETE FROM subscription_orders")
		model.DB.Exec("DELETE FROM subscription_plans")
		model.DB.Exec("DELETE FROM top_ups")
		model.DB.Exec("DELETE FROM users")
	})
}

func requireAppWalletQuota(t *testing.T, userId, appId int) int {
	t.Helper()
	quota, err := model.GetUserAppQuota(userId, appId)
	require.NoError(t, err)
	return quota
}

func TestWalletFundingAppQuotaReserveSettleAndRefund(t *testing.T) {
	prepareAppBillingTest(t)
	const userId, appId = 901, 501
	require.NoError(t, model.IncreaseUserAppQuota(userId, appId, 1000))

	funding := &WalletFunding{userId: userId, appId: appId}
	require.NoError(t, funding.PreConsume(600))
	assert.Equal(t, 400, requireAppWalletQuota(t, userId, appId))

	// Insufficient pre-consume fails atomically and never drives the pool negative.
	require.ErrorIs(t, funding.PreConsume(600), ErrInsufficientWalletQuota)
	assert.Equal(t, 400, requireAppWalletQuota(t, userId, appId))

	// Settle the extra 100 charged by the actual usage (600 reserved + 100 delta).
	require.NoError(t, funding.Settle(100))
	assert.Equal(t, 300, requireAppWalletQuota(t, userId, appId))

	// A failed request refunds the whole pre-consumed amount.
	require.NoError(t, funding.Refund())
	assert.Equal(t, 900, requireAppWalletQuota(t, userId, appId))

	// Negative deltas return quota to the app pool.
	second := &WalletFunding{userId: userId, appId: appId}
	require.NoError(t, second.PreConsume(200))
	assert.Equal(t, 700, requireAppWalletQuota(t, userId, appId))
	require.NoError(t, second.Settle(-50))
	assert.Equal(t, 750, requireAppWalletQuota(t, userId, appId))
}

func TestWalletFundingPlatformStillUsesUserQuota(t *testing.T) {
	prepareAppBillingTest(t)
	const userId = 902
	require.NoError(t, model.DB.Create(&model.User{
		Id:       userId,
		Username: "app_billing_platform",
		Quota:    1000,
		Status:   common.UserStatusEnabled,
	}).Error)

	funding := &WalletFunding{userId: userId}
	require.NoError(t, funding.PreConsume(400))
	quota, err := model.GetUserQuota(userId, true)
	require.NoError(t, err)
	assert.Equal(t, 600, quota)

	require.NoError(t, funding.Settle(100))
	quota, err = model.GetUserQuota(userId, true)
	require.NoError(t, err)
	assert.Equal(t, 500, quota)

	// Platform settlement must not create or touch any app wallet.
	var count int64
	require.NoError(t, model.DB.Model(&model.UserAppWallet{}).Where("user_id = ?", userId).Count(&count).Error)
	assert.Zero(t, count)
}

func TestBillingSessionAppWalletSettlement(t *testing.T) {
	prepareAppBillingTest(t)
	const userId, appId = 903, 502
	require.NoError(t, model.IncreaseUserAppQuota(userId, appId, 1000))

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		AppId:           appId,
		IsPlayground:    true,
		RequestId:       "app-billing-session-1",
		UserSetting:     dto.UserSetting{BillingPreference: "wallet_only"},
		OriginModelName: "test-model",
	}
	session, apiErr := NewBillingSession(c, relayInfo, 100)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	assert.Equal(t, 900, requireAppWalletQuota(t, userId, appId))
	assert.Equal(t, 1000, relayInfo.UserQuota)

	// Settling the exact reservation records the app's used quota once.
	require.NoError(t, session.Settle(100))
	require.NoError(t, session.Settle(100))
	assert.Equal(t, 900, requireAppWalletQuota(t, userId, appId))
	assert.Equal(t, 100, appWalletUsedQuota(t, userId, appId))

	// A delta settlement charges the difference and accumulates used quota once.
	secondInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		AppId:           appId,
		IsPlayground:    true,
		RequestId:       "app-billing-session-2",
		UserSetting:     dto.UserSetting{BillingPreference: "wallet_only"},
		OriginModelName: "test-model",
	}
	second, apiErr := NewBillingSession(c, secondInfo, 200)
	require.Nil(t, apiErr)
	require.NoError(t, second.Settle(250))
	assert.Equal(t, 650, requireAppWalletQuota(t, userId, appId))
	assert.Equal(t, 350, appWalletUsedQuota(t, userId, appId))
}

func appWalletUsedQuota(t *testing.T, userId, appId int) int {
	t.Helper()
	var wallet model.UserAppWallet
	require.NoError(t, model.DB.Where("user_id = ? AND app_id = ?", userId, appId).First(&wallet).Error)
	return wallet.UsedQuota
}
