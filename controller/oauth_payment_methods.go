package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// OAuthPaymentMethods handles GET /oauth/payment/methods. It includes the
// balance method unless it is globally disabled, and appends each third-party
// provider only when that provider is enabled for top-ups.
func OAuthPaymentMethods(c *gin.Context) {
	if _, _, ok := oauthAppAccess(c, model.OAuthScopeAPIRead); !ok {
		return
	}

	methods := []string{}
	if !operation_setting.IsAppBalancePayDisabled() {
		methods = append(methods, model.PaymentMethodBalance)
	}
	if isStripeTopUpEnabled() {
		methods = append(methods, model.PaymentMethodStripe)
	}
	if isCreemTopUpEnabled() {
		methods = append(methods, model.PaymentMethodCreem)
	}
	if isEpayTopUpEnabled() {
		methods = append(methods, model.PaymentProviderEpay)
	}
	if isWaffoPancakeTopUpEnabled() {
		methods = append(methods, model.PaymentMethodWaffoPancake)
	}

	c.JSON(http.StatusOK, gin.H{"payment_methods": methods})
}
