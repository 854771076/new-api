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
