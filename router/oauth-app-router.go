package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

// registerOAuthAppRoutes mounts the OAuth application management API under
// /oauth-apps. These endpoints are admin-only and let platform admins create,
// update, rotate secrets for, and delete OAuth 2.0 client applications.
func registerOAuthAppRoutes(apiRouter *gin.RouterGroup) {
	proxy := apiRouter.Group("/app-proxy", middleware.AdminAuth(), middleware.DisableCache())
	proxy.POST("/users", controller.ProvisionAppProxyUser)
	proxy.POST("/users/:user_id/topup-preview", controller.ScopedAppTopupPreview)
	proxy.POST("/users/:user_id/checkout", controller.ScopedAppCheckout)
	proxy.GET("/users/:user_id/orders", controller.ScopedAppOrderList)
	proxy.GET("/users/:user_id/orders/:order_id", controller.ScopedAppOrder)
	proxy.GET("/users/:user_id/pricing", controller.AdminAppPricing)
	proxy.GET("/users/:user_id/catalog", controller.AdminAppProxyCatalog)
	proxy.GET("/users/:user_id/logs", controller.AdminAppProxyLogs)
	proxy.GET("/users/:user_id/wallet", controller.AdminAppWallet)
	proxy.POST("/users/:user_id/keys", controller.CreateAppProxyKey)
	proxy.GET("/users/:user_id/keys", controller.ListAppProxyKeys)
	proxy.PUT("/users/:user_id/keys/:token_id", controller.UpdateAppProxyKey)
	proxy.POST("/users/:user_id/keys/:token_id/commit-rotation", controller.CommitAppProxyKeyRotation)
	proxy.POST("/users/:user_id/keys/:token_id/cancel-rotation", controller.CancelAppProxyKeyRotation)
	proxy.GET("/users/:user_id/quota-rules", controller.ListTokenQuotaRules)
	proxy.POST("/users/:user_id/quota-rules", controller.SaveTokenQuotaRule)
	proxy.PUT("/users/:user_id/quota-rules/:id", controller.SaveTokenQuotaRule)
	proxy.DELETE("/users/:user_id/quota-rules/:id", controller.DeleteTokenQuotaRule)
	proxy.PUT("/users/:user_id/keys/:token_id/quota-rule", controller.BindTokenQuotaRule)
	oauthAppRoute := apiRouter.Group("/oauth-apps")
	oauthAppRoute.Use(middleware.AdminAuth())
	{
		oauthAppRoute.GET("/", controller.GetOAuthApps)
		oauthAppRoute.POST("/", controller.CreateOAuthApp)
		oauthAppRoute.GET("/:id", controller.GetOAuthApp)
		oauthAppRoute.PUT("/:id", controller.UpdateOAuthApp)
		oauthAppRoute.POST("/:id/rotate-secret", controller.RotateOAuthAppSecret)
		oauthAppRoute.DELETE("/:id", controller.DeleteOAuthApp)
	}
}
