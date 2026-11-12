package controller

import (
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// OAuthTopupCheckout handles POST /oauth/topup/checkout.
//
// Requires Authorization: Bearer <oauth access token> with scope api:write.
// Body: {"amount": 10, "payment_method": "waffo_pancake"|"balance"}.
// The credited pool is always the OAuth App wallet of the access token, never
// the platform user quota.
//
// Returns {"checkout_url": "...", "order_id": "...", "expires_at": "..."} for
// online payments and {"status": "success", "quota": ...} for balance top-ups.
// We intentionally do not expose the underlying TopUp table's database
// auto-increment id (order_id is the opaque business identifier).
func OAuthTopupCheckout(c *gin.Context) {
	user, app, ok := oauthAppAccess(c, model.OAuthScopeAPIWrite)
	if !ok {
		return
	}

	var body struct {
		Amount        int64  `json:"amount"`
		PaymentMethod string `json:"payment_method"`
		// AppId is accepted only so a mismatching client is rejected.
		AppId int `json:"app_id"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "error_description": "invalid JSON body"})
		return
	}
	if body.AppId != 0 && body.AppId != app.Id {
		c.JSON(http.StatusForbidden, gin.H{"error": "access_denied", "error_description": "app_id does not match the access token"})
		return
	}
	if body.Amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "error_description": "amount must be greater than 0"})
		return
	}

	paymentMethod := strings.TrimSpace(body.PaymentMethod)
	if paymentMethod == "" {
		paymentMethod = model.PaymentMethodWaffoPancake
	}

	switch paymentMethod {
	case model.PaymentMethodBalance:
		quota, err := creditAppTopUpWithBalance(user.Id, app.Id, body.Amount)
		if err != nil {
			oauthCheckoutError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "success", "payment_method": paymentMethod, "quota": quota})
	case model.PaymentMethodWaffoPancake:
		result, err := generateWaffoPancakeCheckout(c.Request.Context(), user, body.Amount, app.Id)
		if err != nil {
			var msg *common.Message
			if errors.As(err, &msg) {
				// Business validation messages are client errors.
				c.JSON(http.StatusBadRequest, gin.H{"error": "checkout_failed", "error_description": msg.Error()})
				return
			}
			// Remaining failures come from the payment gateway or an internal
			// dependency, so report them as an upstream error.
			c.JSON(http.StatusBadGateway, gin.H{"error": "checkout_failed", "error_description": "checkout service temporarily unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status":         "pending",
			"payment_method": paymentMethod,
			"checkout_url":   result.CheckoutURL,
			"order_id":       result.OrderID,
			"expires_at":     result.ExpiresAt,
		})
	case model.PaymentMethodStripe:
		payLink, orderID, err := generateStripeTopUpCheckout(c.Request.Context(), user, body.Amount, app.Id)
		if err != nil {
			oauthTopupCheckoutError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status":         "pending",
			"payment_method": paymentMethod,
			"pay_link":       payLink,
			"order_id":       orderID,
		})
	case model.PaymentMethodCreem:
		checkoutURL, orderID, err := generateCreemTopUpCheckout(c.Request.Context(), user, body.Amount, app.Id)
		if err != nil {
			oauthTopupCheckoutError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status":         "pending",
			"payment_method": paymentMethod,
			"checkout_url":   checkoutURL,
			"order_id":       orderID,
		})
	case model.PaymentProviderEpay:
		paymentType, err := firstConfiguredEpayMethod()
		if err != nil {
			oauthTopupCheckoutError(c, err)
			return
		}
		uri, params, err := generateEpayTopUpCheckout(c.Request.Context(), user, body.Amount, app.Id, paymentType)
		if err != nil {
			oauthTopupCheckoutError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status":         "pending",
			"payment_method": model.PaymentProviderEpay,
			"url":            uri,
			"params":         params,
		})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported_payment_method", "error_description": "this payment method is not supported for app top-up"})
	}
}

// oauthTopupCheckoutError maps top-up checkout failures to OAuth responses.
// Business validation messages become 400; upstream failures become 502.
func oauthTopupCheckoutError(c *gin.Context, err error) {
	var msg *common.Message
	if errors.As(err, &msg) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "checkout_failed", "error_description": msg.Error()})
		return
	}
	c.JSON(http.StatusBadGateway, gin.H{"error": "checkout_failed", "error_description": "checkout service temporarily unavailable"})
}
