package controller

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// OAuthUserInfo handles GET /oauth/userinfo.
// It validates the access token and returns user claims based on the granted
// scopes.
func OAuthUserInfo(c *gin.Context) {
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

	response := gin.H{
		"sub": strconv.Itoa(user.Id),
	}
	if _, ok := scopeSet[model.OAuthScopeProfile]; ok {
		response["username"] = user.Username
		response["name"] = user.DisplayName
	}
	if _, ok := scopeSet[model.OAuthScopeEmail]; ok {
		response["email"] = user.Email
	}

	c.JSON(http.StatusOK, response)
}

// OAuthIntrospect handles POST /oauth/introspect (RFC 7662).
// Requires client authentication. Returns the active state of the token.
func OAuthIntrospect(c *gin.Context) {
	clientID, clientSecret, ok := authenticateOAuthClient(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": oauthErrInvalidClient, "error_description": "client authentication required"})
		return
	}
	app, err := model.GetOAuthAppByClientID(clientID)
	if err != nil || !app.IsEnabled() || !model.VerifyOAuthAppSecret(app, clientSecret) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": oauthErrInvalidClient, "error_description": "client authentication failed"})
		return
	}

	token := c.PostForm("token")
	tokenTypeHint := c.PostForm("token_type_hint")

	response := introspectToken(app, token, tokenTypeHint)
	c.JSON(http.StatusOK, response)
}

// introspectToken determines whether a token is active and returns its claims.
func introspectToken(app *model.OAuthApp, token, typeHint string) gin.H {
	token = strings.TrimSpace(token)
	if token == "" {
		return gin.H{"active": false}
	}

	// Try as access token (JWT) first unless the hint says refresh_token.
	if typeHint != "refresh_token" {
		userID, clientID, scope, authVersion, err := service.ParseOAuthAccessToken(token)
		if err == nil && clientID == app.ClientID {
			user, err := model.GetUserById(userID, true)
			if err == nil && user.Status == common.UserStatusEnabled && user.AuthVersion == authVersion {
				return gin.H{
					"active":     true,
					"scope":      scope,
					"client_id":  clientID,
					"username":   user.Username,
					"sub":        strconv.Itoa(userID),
					"token_type": "access_token",
					"exp":        extractJWTExp(token),
					"iat":        extractJWTIat(token),
				}
			}
		}
	}

	// Try as refresh token.
	if typeHint != "access_token" {
		hash := model.OAuthRefreshTokenHash(token)
		rt, err := model.GetOAuthRefreshTokenByHash(hash)
		if err == nil && rt.AppID == app.Id && rt.RevokedAt == 0 {
			return gin.H{
				"active":     true,
				"scope":      rt.Scope,
				"client_id":  app.ClientID,
				"sub":        strconv.Itoa(rt.UserID),
				"token_type": "refresh_token",
				"exp":        rt.ExpiresAt,
			}
		}
	}

	return gin.H{"active": false}
}

// OAuthRevoke handles POST /oauth/revoke (RFC 7009).
// Requires client authentication. Revokes a refresh token.
func OAuthRevoke(c *gin.Context) {
	clientID, clientSecret, ok := authenticateOAuthClient(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": oauthErrInvalidClient})
		return
	}
	app, err := model.GetOAuthAppByClientID(clientID)
	if err != nil || !app.IsEnabled() || !model.VerifyOAuthAppSecret(app, clientSecret) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": oauthErrInvalidClient})
		return
	}

	token := strings.TrimSpace(c.PostForm("token"))
	if token == "" {
		// RFC 7009: revoke of empty token is a no-op success.
		c.Status(http.StatusOK)
		return
	}

	// Only refresh tokens are revocable; access tokens expire naturally.
	hash := model.OAuthRefreshTokenHash(token)
	rt, err := model.GetOAuthRefreshTokenByHash(hash)
	if err == nil && rt.AppID == app.Id {
		_ = model.RevokeOAuthRefreshTokenByHash(hash)
		recordOAuthAudit(c, rt.UserID, oauthAuditActorRole(rt.UserID), "oauth.revoke", true, http.StatusOK, map[string]any{
			"client_id":  app.ClientID,
			"token_type": "refresh_token",
		})
	}

	// Always return 200 per RFC 7009, even if the token was unknown.
	c.Status(http.StatusOK)
}

// OAuthLogout handles GET/POST /oauth/logout.
// Revokes all refresh tokens for the user within the requesting app and
// redirects to the post-logout redirect URI if provided and whitelisted.
func OAuthLogout(c *gin.Context) {
	var clientID, clientSecret string
	var ok bool

	// Logout can be initiated with an id_token_hint (no client auth needed) or
	// with explicit client credentials.
	idTokenHint := strings.TrimSpace(c.Query("id_token_hint"))
	if idTokenHint == "" {
		idTokenHint = strings.TrimSpace(c.PostForm("id_token_hint"))
	}

	if idTokenHint != "" {
		// Extract client_id from the id_token claims without verification
		// (logout is advisory; revocation is scoped to the app).
		clientID = extractJWTClientID(idTokenHint)
	} else {
		clientID, clientSecret, ok = authenticateOAuthClient(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": oauthErrInvalidClient})
			return
		}
	}

	app, err := model.GetOAuthAppByClientID(clientID)
	if err != nil {
		c.String(http.StatusBadRequest, "invalid client")
		return
	}
	if clientSecret != "" && !model.VerifyOAuthAppSecret(app, clientSecret) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": oauthErrInvalidClient})
		return
	}

	// Determine the user from the access token or id_token.
	userID := extractJWTSubject(c.Query("access_token"))
	if userID <= 0 {
		userID = extractJWTSubject(c.PostForm("access_token"))
	}
	if userID <= 0 && idTokenHint != "" {
		userID = extractJWTSubject(idTokenHint)
	}

	if userID > 0 {
		_ = model.RevokeOAuthRefreshTokensByAppAndUser(app.Id, userID)
		recordOAuthAudit(c, userID, oauthAuditActorRole(userID), "oauth.logout", true, http.StatusOK, map[string]any{
			"client_id": app.ClientID,
		})
	}

	// Redirect to post_logout_redirect_uri if provided and whitelisted.
	postLogoutURI := strings.TrimSpace(c.Query("post_logout_redirect_uri"))
	if postLogoutURI == "" {
		postLogoutURI = strings.TrimSpace(c.PostForm("post_logout_redirect_uri"))
	}
	if postLogoutURI != "" && app.HasPostLogoutRedirectURI(postLogoutURI) {
		state := c.Query("state")
		sep := "?"
		if strings.Contains(postLogoutURI, "?") {
			sep = "&"
		}
		if state != "" {
			postLogoutURI += sep + "state=" + state
		}
		c.Redirect(http.StatusFound, postLogoutURI)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// extractBearerToken extracts the token from the Authorization: Bearer header.
func extractBearerToken(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
}
