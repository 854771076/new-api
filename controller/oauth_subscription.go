package controller

// OAuth App subscription endpoints.
//
// NOTE: GET /oauth/subscription/plans is registered by the dedicated OAuth
// router task. The handler lives here so the provider-facing route can be
// mounted alongside the other /oauth endpoints without touching this file.

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// OAuthSubscriptionPlans handles GET /oauth/subscription/plans.
// It returns the enabled plans that belong to the OAuth App owning the access
// token. Plans of other apps and platform (app_id = 0) plans are never exposed.
func OAuthSubscriptionPlans(c *gin.Context) {
	token := extractBearerToken(c)
	if token == "" {
		c.Header("WWW-Authenticate", "Bearer")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token", "error_description": "access token is required"})
		return
	}

	_, scope, err := service.ValidateOAuthAccessToken(token)
	if err != nil {
		c.Header("WWW-Authenticate", "Bearer error=\"invalid_token\"")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token", "error_description": "access token is invalid or expired"})
		return
	}

	scopeSet := make(map[string]struct{})
	for _, s := range strings.Fields(scope) {
		scopeSet[s] = struct{}{}
	}
	if _, ok := scopeSet[model.OAuthScopeAPIRead]; !ok {
		c.Header("WWW-Authenticate", "Bearer error=\"insufficient_scope\"")
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient_scope", "error_description": "api:read scope is required"})
		return
	}

	_, clientID, _, _, err := service.ParseOAuthAccessToken(token)
	if err != nil || clientID == "" {
		c.Header("WWW-Authenticate", "Bearer error=\"invalid_token\"")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token", "error_description": "access token is invalid or expired"})
		return
	}
	app, err := service.LoadOAuthAppByClientID(clientID)
	if err != nil || !app.IsEnabled() {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid_client", "error_description": "oauth app is disabled or unavailable"})
		return
	}

	plans, err := model.ListEnabledSubscriptionPlans(app.Id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error", "error_description": "failed to load subscription plans"})
		return
	}
	result := make([]SubscriptionPlanDTO, 0, len(plans))
	for _, p := range plans {
		result = append(result, SubscriptionPlanDTO{Plan: p})
	}
	c.JSON(http.StatusOK, gin.H{"plans": result})
}
