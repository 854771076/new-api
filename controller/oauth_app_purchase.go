package controller

import (
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// oauthAppAccess authenticates an OAuth App request and enforces the scope that
// the endpoint needs. App identity always comes from the access token.
func oauthAppAccess(c *gin.Context, requiredScope string) (*model.User, *model.OAuthApp, bool) {
	token := extractBearerToken(c)
	if token == "" {
		c.Header("WWW-Authenticate", "Bearer")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token", "error_description": "access token is required"})
		return nil, nil, false
	}

	user, scope, err := service.ValidateOAuthAccessToken(token)
	if err != nil {
		c.Header("WWW-Authenticate", "Bearer error=\"invalid_token\"")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token", "error_description": "access token is invalid or expired"})
		return nil, nil, false
	}

	scopeSet := make(map[string]struct{})
	for _, s := range strings.Fields(scope) {
		scopeSet[s] = struct{}{}
	}
	if _, ok := scopeSet[requiredScope]; !ok {
		c.Header("WWW-Authenticate", "Bearer error=\"insufficient_scope\"")
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient_scope", "error_description": requiredScope + " scope is required"})
		return nil, nil, false
	}

	_, clientID, _, _, err := service.ParseOAuthAccessToken(token)
	if err != nil || clientID == "" {
		c.Header("WWW-Authenticate", "Bearer error=\"invalid_token\"")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token", "error_description": "access token is invalid or expired"})
		return nil, nil, false
	}
	app, err := service.LoadOAuthAppByClientID(clientID)
	if err != nil || !app.IsEnabled() {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid_client", "error_description": "oauth app is disabled or unavailable"})
		return nil, nil, false
	}
	return user, app, true
}

// paymentComplianceError is response-free so shared checkout helpers cannot
// bypass the gate or write a dashboard response into an OAuth response.
func paymentComplianceError() error {
	if !operation_setting.IsPaymentComplianceConfirmed() {
		return common.NewMessage("Payment, redemption, subscription, and invitation reward features are disabled. The administrator must confirm compliance terms before enabling them.")
	}
	return nil
}

// appBalancePayDisabledError reports when OAuth app balance payment is
// globally disabled.
func appBalancePayDisabledError() error {
	if operation_setting.IsAppBalancePayDisabled() {
		return common.NewMessage("Balance payment is disabled. Please use another payment method.")
	}
	return nil
}

// oauthCheckoutError maps purchase failures to OAuth error responses. Business
// messages become 400; domain ownership/disabled errors keep their meaning; any
// other failure is reported as a server error.
func oauthCheckoutError(c *gin.Context, err error) {
	var msg *common.Message
	switch {
	case errors.Is(err, model.ErrAppProductNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "error_description": "product does not exist"})
	case errors.Is(err, model.ErrAppProductCrossApp):
		c.JSON(http.StatusForbidden, gin.H{"error": "access_denied", "error_description": "product does not belong to this app"})
	case errors.Is(err, model.ErrAppProductDisabled):
		c.JSON(http.StatusBadRequest, gin.H{"error": "checkout_failed", "error_description": "product is not enabled"})
	case errors.Is(err, model.ErrAppProductPurchaseLimitExceeded):
		c.JSON(http.StatusBadRequest, gin.H{"error": "checkout_failed", "error_description": "purchase limit for this product has been reached"})
	case errors.As(err, &msg):
		c.JSON(http.StatusBadRequest, gin.H{"error": "checkout_failed", "error_description": msg.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error", "error_description": "checkout service temporarily unavailable"})
	}
}

type oauthSubscriptionCheckoutRequest struct {
	PlanId        int    `json:"plan_id"`
	PaymentMethod string `json:"payment_method"`
	// AppId is accepted only so a mismatching client is rejected. The effective
	// app scope always comes from the access token.
	AppId int `json:"app_id"`
}

// OAuthSubscriptionCheckout handles POST /oauth/subscription/checkout.
func OAuthSubscriptionCheckout(c *gin.Context) {
	user, app, ok := oauthAppAccess(c, model.OAuthScopeAPIWrite)
	if !ok {
		return
	}

	var body oauthSubscriptionCheckoutRequest
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "error_description": "invalid JSON body"})
		return
	}
	if body.AppId != 0 && body.AppId != app.Id {
		c.JSON(http.StatusForbidden, gin.H{"error": "access_denied", "error_description": "app_id does not match the access token"})
		return
	}
	if body.PlanId <= 0 || strings.TrimSpace(body.PaymentMethod) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "error_description": "plan_id and payment_method are required"})
		return
	}

	plan, err := model.GetSubscriptionPlanById(body.PlanId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "error_description": "subscription plan does not exist"})
		return
	}
	if plan.AppId != app.Id {
		c.JSON(http.StatusForbidden, gin.H{"error": "access_denied", "error_description": "subscription plan does not belong to this app"})
		return
	}
	if !plan.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "checkout_failed", "error_description": "subscription plan is not enabled"})
		return
	}

	paymentMethod := strings.TrimSpace(body.PaymentMethod)
	switch paymentMethod {
	case model.PaymentMethodBalance:
		if err := paymentComplianceError(); err != nil {
			oauthCheckoutError(c, err)
			return
		}
		if err := appBalancePayDisabledError(); err != nil {
			oauthCheckoutError(c, err)
			return
		}
		if err := model.PurchaseSubscriptionWithBalance(user.Id, plan.Id, app.Id); err != nil {
			oauthCheckoutError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "success", "payment_method": model.PaymentMethodBalance})
	case model.PaymentProviderEpay:
		paymentType, err := firstConfiguredEpayMethod()
		if err != nil {
			oauthCheckoutError(c, err)
			return
		}
		uri, params, err := createEpaySubscriptionOrder(user.Id, plan, paymentType)
		if err != nil {
			oauthCheckoutError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "pending", "payment_method": model.PaymentProviderEpay, "url": uri, "params": params})
	case model.PaymentMethodStripe:
		payLink, err := createStripeSubscriptionOrder(c.Request.Context(), user, plan)
		if err != nil {
			oauthCheckoutError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "pending", "payment_method": paymentMethod, "pay_link": payLink})
	case model.PaymentMethodCreem:
		checkoutURL, orderID, err := createCreemSubscriptionOrder(c.Request.Context(), user, plan)
		if err != nil {
			oauthCheckoutError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "pending", "payment_method": paymentMethod, "checkout_url": checkoutURL, "order_id": orderID})
	case model.PaymentMethodWaffoPancake:
		result, err := createWaffoPancakeSubscriptionOrder(c.Request.Context(), user, plan)
		if err != nil {
			oauthCheckoutError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status":         "pending",
			"payment_method": paymentMethod,
			"checkout_url":   result.CheckoutURL,
			"session_id":     result.SessionID,
			"expires_at":     result.ExpiresAt,
			"order_id":       result.OrderID,
		})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported_payment_method", "error_description": "this payment method is not supported for app subscription plans"})
	}
}

type oauthAppProductDTO struct {
	Id                 int     `json:"id"`
	Name               string  `json:"name"`
	Description        string  `json:"description"`
	PriceAmount        float64 `json:"price_amount"`
	Currency           string  `json:"currency"`
	QuotaAmount        int64   `json:"quota_amount"`
	MaxPurchasePerUser int     `json:"max_purchase_per_user"`
}

// OAuthProductList handles GET /oauth/products.
func OAuthProductList(c *gin.Context) {
	_, app, ok := oauthAppAccess(c, model.OAuthScopeAPIRead)
	if !ok {
		return
	}

	products, err := model.ListEnabledAppProducts(app.Id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error", "error_description": "failed to load products"})
		return
	}
	result := make([]oauthAppProductDTO, 0, len(products))
	for _, p := range products {
		result = append(result, oauthAppProductDTO{
			Id:                 p.Id,
			Name:               p.Name,
			Description:        p.Description,
			PriceAmount:        p.PriceAmount,
			Currency:           p.Currency,
			QuotaAmount:        p.QuotaAmount,
			MaxPurchasePerUser: p.MaxPurchasePerUser,
		})
	}
	c.JSON(http.StatusOK, gin.H{"products": result})
}

type oauthProductCheckoutRequest struct {
	ProductId     int    `json:"product_id"`
	PaymentMethod string `json:"payment_method"`
	// AppId is accepted only so a mismatching client is rejected.
	AppId int `json:"app_id"`
}

// OAuthProductCheckout handles POST /oauth/products/checkout.
func OAuthProductCheckout(c *gin.Context) {
	user, app, ok := oauthAppAccess(c, model.OAuthScopeAPIWrite)
	if !ok {
		return
	}

	var body oauthProductCheckoutRequest
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "error_description": "invalid JSON body"})
		return
	}
	if body.AppId != 0 && body.AppId != app.Id {
		c.JSON(http.StatusForbidden, gin.H{"error": "access_denied", "error_description": "app_id does not match the access token"})
		return
	}
	if body.ProductId <= 0 || strings.TrimSpace(body.PaymentMethod) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "error_description": "product_id and payment_method are required"})
		return
	}

	product, err := model.GetAppProductById(body.ProductId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "error_description": "product does not exist"})
		return
	}
	if product.AppId != app.Id {
		c.JSON(http.StatusForbidden, gin.H{"error": "access_denied", "error_description": "product does not belong to this app"})
		return
	}
	if !product.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "checkout_failed", "error_description": "product is not enabled"})
		return
	}

	paymentMethod := strings.TrimSpace(body.PaymentMethod)
	switch paymentMethod {
	case model.PaymentMethodBalance:
		if err := paymentComplianceError(); err != nil {
			oauthCheckoutError(c, err)
			return
		}
		if err := appBalancePayDisabledError(); err != nil {
			oauthCheckoutError(c, err)
			return
		}
		order, err := model.PurchaseAppProductWithBalance(user.Id, app.Id, product.Id)
		if err != nil {
			oauthCheckoutError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status":         "success",
			"payment_method": paymentMethod,
			"order_id":       order.TradeNo,
			"quota_amount":   order.QuotaAmount,
		})
	case model.PaymentMethodWaffoPancake:
		result, err := CreateAppProductWaffoPancakeCheckout(c.Request.Context(), user, product)
		if err != nil {
			oauthCheckoutError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status":         "pending",
			"payment_method": paymentMethod,
			"checkout_url":   result.CheckoutURL,
			"session_id":     result.SessionID,
			"expires_at":     result.ExpiresAt,
			"order_id":       result.TradeNo,
		})
	case model.PaymentMethodStripe:
		result, err := CreateAppProductStripeCheckout(c.Request.Context(), user, product)
		if err != nil {
			oauthCheckoutError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status":         "pending",
			"payment_method": paymentMethod,
			"pay_link":       result.CheckoutURL,
			"order_id":       result.TradeNo,
		})
	case model.PaymentMethodCreem:
		result, err := CreateAppProductCreemCheckout(c.Request.Context(), user, product)
		if err != nil {
			oauthCheckoutError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status":         "pending",
			"payment_method": paymentMethod,
			"checkout_url":   result.CheckoutURL,
			"order_id":       result.TradeNo,
		})
	case model.PaymentProviderEpay:
		paymentType, err := firstConfiguredEpayMethod()
		if err != nil {
			oauthCheckoutError(c, err)
			return
		}
		result, err := CreateAppProductEpayCheckout(c.Request.Context(), user, product, paymentType)
		if err != nil {
			oauthCheckoutError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status":         "pending",
			"payment_method": model.PaymentProviderEpay,
			"url":            result.URL,
			"params":         result.Params,
			"order_id":       result.TradeNo,
		})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported_payment_method", "error_description": "this payment method is not supported for app products"})
	}
}

// creditAppTopUpWithBalance validates the amount before the model atomically
// transfers platform balance and records the completed app top-up.
func creditAppTopUpWithBalance(userId, appId int, amount int64) (int, error) {
	if err := paymentComplianceError(); err != nil {
		return 0, err
	}
	if amount < getMinTopup() {
		return 0, common.NewMessage("Top-up amount cannot be less than {{min}}", map[string]any{"min": getMinTopup()})
	}
	quota, err := validateTopUpQuota(amount)
	if err != nil {
		return 0, err
	}
	if err := model.TransferUserQuotaToApp(userId, appId, amount, quota); err != nil {
		return 0, err
	}
	return quota, nil
}
