package router

import (
	"net/http"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

// registerOAuthProviderRoutes mounts the OAuth 2.0 / OIDC provider endpoints
// directly on the engine (not under /api). These are standard OAuth endpoints
// used by external applications.
func registerOAuthProviderRoutes(router *gin.Engine) {
	rateLimit := middleware.GlobalAPIRateLimit()

	// Authorization endpoint: must attempt session authentication but not
	// require it (unauthenticated users are redirected to sign-in).
	// TryUserAuth reads the Bearer token used by the dashboard SPA; the cookie
	// fallback covers the external browser navigation that starts the flow.
	authorizeGroup := router.Group("/oauth")
	authorizeGroup.Use(rateLimit, middleware.TryUserAuth(), middleware.TryOAuthSessionAuth())
	{
		authorizeGroup.GET("/authorize", controller.OAuthAuthorize)
		authorizeGroup.POST("/authorize", controller.OAuthAuthorizeConsent)
		authorizeGroup.GET("/consent/context", controller.OAuthConsentContext)
	}

	// Token, introspect, and revoke endpoints require client authentication and
	// do not use dashboard session middleware. Logout additionally reads the
	// browser session cookie so it can revoke the caller's own tokens when no
	// verified id_token_hint is supplied.
	router.POST("/oauth/token", rateLimit, controller.OAuthToken)
	router.POST("/oauth/introspect", rateLimit, controller.OAuthIntrospect)
	router.POST("/oauth/revoke", rateLimit, controller.OAuthRevoke)
	router.POST("/oauth/logout", rateLimit, middleware.TryOAuthSessionAuth(), controller.OAuthLogout)
	router.GET("/oauth/logout", rateLimit, middleware.TryOAuthSessionAuth(), controller.OAuthLogout)

	// UserInfo requires a valid access token.
	router.GET("/oauth/userinfo", rateLimit, controller.OAuthUserInfo)

	// Top-up checkout requires a valid access token with the api:write scope.
	router.POST("/oauth/topup/checkout", rateLimit, controller.OAuthTopupCheckout)

	// App-scoped purchase endpoints. Reads require api:read; checkouts require
	// api:write, both enforced by the handlers from the access token.
	router.GET("/oauth/subscription/plans", rateLimit, controller.OAuthSubscriptionPlans)
	router.POST("/oauth/subscription/checkout", rateLimit, controller.OAuthSubscriptionCheckout)
	router.GET("/oauth/products", rateLimit, controller.OAuthProductList)
	router.POST("/oauth/products/checkout", rateLimit, controller.OAuthProductCheckout)

	// Logs list requires a valid access token with the api:read scope.
	router.GET("/oauth/logs", rateLimit, controller.OAuthLogsList)

	// Pricing requires a valid access token with the api:read scope.
	router.GET("/oauth/pricing", rateLimit, controller.OAuthPricing)

	// OIDC discovery and JWKS.
	router.GET("/.well-known/openid-configuration", controller.OAuthDiscovery)
	router.GET("/oauth/jwks", controller.OAuthJWKS)

	// Health endpoint returns 200 for load balancers.
	router.GET("/oauth/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
}
