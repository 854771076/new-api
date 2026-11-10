package controller

import (
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// OAuthTopupCheckout handles POST /oauth/topup/checkout.
//
// Requires Authorization: Bearer <oauth access token> with scope api:write.
// Body: {"amount": 10} (channel is ignored; currently only waffo_pancake is
// supported).
//
// Returns {"checkout_url": "...", "order_id": "...", "expires_at": "..."} on
// success. We intentionally do not expose the underlying TopUp table's
// database auto-increment id (order_id is the opaque business identifier).
func OAuthTopupCheckout(c *gin.Context) {
	token := extractBearerToken(c)
	if token == "" {
		c.Header("WWW-Authenticate", "Bearer")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token", "error_description": "access token is required"})
		return
	}

	user, scope, err := service.ValidateOAuthAccessToken(token)
	if err != nil {
		c.Header("WWW-Authenticate", "Bearer error=\"invalid_token\"")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token", "error_description": "access token is invalid or expired"})
		return
	}

	scopeSet := make(map[string]struct{})
	for _, s := range strings.Fields(scope) {
		scopeSet[s] = struct{}{}
	}
	if _, ok := scopeSet[model.OAuthScopeAPIWrite]; !ok {
		c.Header("WWW-Authenticate", "Bearer error=\"insufficient_scope\"")
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient_scope", "error_description": "api:write scope is required"})
		return
	}

	var body struct {
		Amount int64 `json:"amount"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "error_description": "invalid JSON body"})
		return
	}
	if body.Amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "error_description": "amount must be greater than 0"})
		return
	}

	result, err := generateWaffoPancakeCheckout(c.Request.Context(), user, body.Amount)
	if err != nil {
		var msg *common.Message
		if errors.As(err, &msg) {
			// common.Message 是 controller 层的业务提示，统一当客户端 400 处理。
			c.JSON(http.StatusBadRequest, gin.H{"error": "checkout_failed", "error_description": msg.Error()})
			return
		}
		// 其它错误来自支付网关 / 内部依赖，归为上游错误 502。
		c.JSON(http.StatusBadGateway, gin.H{"error": "checkout_failed", "error_description": "checkout service temporarily unavailable"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"checkout_url": result.CheckoutURL,
		"order_id":     result.OrderID,
		"expires_at":   result.ExpiresAt,
	})
}
