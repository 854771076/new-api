package model

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustCreateAppProduct(t *testing.T, appId int, maxPurchasePerUser int) *AppProduct {
	t.Helper()
	product := &AppProduct{
		AppId:              appId,
		Name:               "test product",
		PriceAmount:        10,
		Currency:           "USD",
		QuotaAmount:        1000,
		Enabled:            true,
		MaxPurchasePerUser: maxPurchasePerUser,
	}
	require.NoError(t, DB.Create(product).Error)
	return product
}

func TestCreateAppProductOrderEnforcesPurchaseLimit(t *testing.T) {
	setupAppModelTestDB(t)
	product := mustCreateAppProduct(t, 1, 1)

	order, err := CreateAppProductOrder(7, 1, product.Id, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.NoError(t, err)
	require.NotEmpty(t, order.TradeNo)
	assert.True(t, strings.HasPrefix(order.TradeNo, AppProductOrderTradeNoPrefix))
	assert.Equal(t, AppProductOrderStatusPending, order.Status)
	assert.Equal(t, product.PriceAmount, order.Money)
	assert.Equal(t, product.PriceAmount, order.PriceAmount)
	assert.Equal(t, product.Currency, order.Currency)
	assert.Equal(t, product.QuotaAmount, order.QuotaAmount)
	assert.NotZero(t, order.CreateTime)

	// A second order is rejected while the first one is still pending.
	_, err = CreateAppProductOrder(7, 1, product.Id, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.ErrorIs(t, err, ErrAppProductPurchaseLimitExceeded)

	// A successful purchase keeps the slot consumed.
	require.NoError(t, CompleteAppProductOrder(order.TradeNo, "", PaymentProviderWaffoPancake, product.PriceAmount))
	_, err = CreateAppProductOrder(7, 1, product.Id, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.ErrorIs(t, err, ErrAppProductPurchaseLimitExceeded)

	// Generated trade numbers are unique across users.
	otherOrder, err := CreateAppProductOrder(8, 1, product.Id, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.NoError(t, err)
	assert.NotEqual(t, order.TradeNo, otherOrder.TradeNo)
}

func TestExpiredAppProductOrderReleasesPurchaseSlot(t *testing.T) {
	setupAppModelTestDB(t)
	product := mustCreateAppProduct(t, 2, 1)

	order, err := CreateAppProductOrder(9, 2, product.Id, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.NoError(t, err)

	require.NoError(t, UpdateAppProductOrderStatus(order.TradeNo, PaymentProviderWaffoPancake, AppProductOrderStatusExpired))
	_, err = CreateAppProductOrder(9, 2, product.Id, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.NoError(t, err)

	// An expired order can no longer be completed.
	require.ErrorIs(t,
		CompleteAppProductOrder(order.TradeNo, "", PaymentProviderWaffoPancake, product.PriceAmount),
		ErrAppProductOrderStatusInvalid,
	)
}

func TestCreateAppProductOrderRejectsDisabledAndCrossAppProducts(t *testing.T) {
	setupAppModelTestDB(t)

	disabled := mustCreateAppProduct(t, 3, 0)
	require.NoError(t, DB.Model(&AppProduct{}).Where("id = ?", disabled.Id).Update("enabled", false).Error)
	_, err := CreateAppProductOrder(10, 3, disabled.Id, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.ErrorIs(t, err, ErrAppProductDisabled)

	otherAppProduct := mustCreateAppProduct(t, 4, 0)
	_, err = CreateAppProductOrder(10, 3, otherAppProduct.Id, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.ErrorIs(t, err, ErrAppProductCrossApp)

	_, err = CreateAppProductOrder(10, 3, 999999, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.ErrorIs(t, err, ErrAppProductNotFound)
}

func TestCompleteAppProductOrderCreditsExactlyOnce(t *testing.T) {
	setupAppModelTestDB(t)
	product := mustCreateAppProduct(t, 5, 0)

	order, err := CreateAppProductOrder(11, 5, product.Id, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.NoError(t, err)

	require.NoError(t, CompleteAppProductOrder(order.TradeNo, "payload-1", PaymentProviderWaffoPancake, product.PriceAmount))
	assert.EqualValues(t, product.QuotaAmount, getUserAppWalletQuota(t, 11, 5))

	completed := GetAppProductOrderByTradeNo(order.TradeNo)
	require.NotNil(t, completed)
	assert.Equal(t, AppProductOrderStatusSuccess, completed.Status)
	assert.NotZero(t, completed.CompleteTime)
	assert.Equal(t, "payload-1", completed.ProviderPayload)

	// A duplicate callback must not credit again.
	require.NoError(t, CompleteAppProductOrder(order.TradeNo, "payload-2", PaymentProviderWaffoPancake, product.PriceAmount))
	assert.EqualValues(t, product.QuotaAmount, getUserAppWalletQuota(t, 11, 5))
}

func TestCompleteAppProductOrderRejectsAmountAndProviderMismatch(t *testing.T) {
	setupAppModelTestDB(t)
	product := mustCreateAppProduct(t, 6, 0)

	order, err := CreateAppProductOrder(12, 6, product.Id, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake)
	require.NoError(t, err)

	require.ErrorIs(t,
		CompleteAppProductOrder(order.TradeNo, "", PaymentProviderWaffoPancake, product.PriceAmount-0.01),
		ErrAppProductOrderAmountMismatch,
	)
	assert.Zero(t, getUserAppWalletQuota(t, 12, 6))

	require.ErrorIs(t,
		CompleteAppProductOrder(order.TradeNo, "", PaymentProviderStripe, product.PriceAmount),
		ErrPaymentMethodMismatch,
	)
	assert.Zero(t, getUserAppWalletQuota(t, 12, 6))

	require.NoError(t, CompleteAppProductOrder(order.TradeNo, "", PaymentProviderWaffoPancake, product.PriceAmount))
	assert.EqualValues(t, product.QuotaAmount, getUserAppWalletQuota(t, 12, 6))
}
