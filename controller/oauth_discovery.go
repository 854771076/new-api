package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// OAuthDiscovery handles GET /.well-known/openid-configuration.
// Returns the OIDC provider metadata document.
func OAuthDiscovery(c *gin.Context) {
	issuer := service.OAuthIssuer()
	c.JSON(http.StatusOK, gin.H{
		"issuer":                                issuer,
		"authorization_endpoint":                issuer + "/oauth/authorize",
		"token_endpoint":                        issuer + "/oauth/token",
		"userinfo_endpoint":                     issuer + "/oauth/userinfo",
		"jwks_uri":                              issuer + "/oauth/jwks",
		"end_session_endpoint":                  issuer + "/oauth/logout",
		"introspection_endpoint":                issuer + "/oauth/introspect",
		"revocation_endpoint":                   issuer + "/oauth/revoke",
		"scopes_supported":                      model.AllOAuthScopes,
		"response_types_supported":              []string{"code"},
		"response_modes_supported":              []string{"query"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post", "none"},
		"code_challenge_methods_supported":      []string{"S256"},
		"claims_supported": []string{
			"sub", "iss", "aud", "exp", "iat", "auth_time", "nonce",
			"name", "preferred_username", "email", "email_verified",
		},
	})
}

// OAuthJWKS handles GET /oauth/jwks.
// Returns the RSA public key used to sign access and ID tokens so that
// standard OIDC clients can verify them locally.
func OAuthJWKS(c *gin.Context) {
	jwk, err := service.OAuthPublicJWK()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error"})
		return
	}
	c.Header("Cache-Control", "public, max-age=3600")
	c.JSON(http.StatusOK, gin.H{"keys": []any{jwk}})
}
