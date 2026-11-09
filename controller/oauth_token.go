package controller

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// OAuthToken handles POST /oauth/token.
func OAuthToken(c *gin.Context) {
	grantType := c.PostForm("grant_type")
	if grantType == "" {
		oauthTokenError(c, http.StatusBadRequest, oauthErrInvalidRequest, "grant_type is required")
		return
	}

	clientID, clientSecret, ok := authenticateOAuthClient(c)
	if !ok {
		oauthTokenError(c, http.StatusUnauthorized, oauthErrInvalidClient, "client authentication failed")
		return
	}
	c.Set("oauth_client_id", clientID)

	app, err := model.GetOAuthAppByClientID(clientID)
	if err != nil || !app.IsEnabled() {
		oauthTokenError(c, http.StatusUnauthorized, oauthErrInvalidClient, "client not found or disabled")
		return
	}
	if !model.VerifyOAuthAppSecret(app, clientSecret) {
		oauthTokenError(c, http.StatusUnauthorized, oauthErrInvalidClient, "client authentication failed")
		return
	}

	switch grantType {
	case "authorization_code":
		handleOAuthAuthorizationCodeGrant(c, app)
	case "refresh_token":
		handleOAuthRefreshTokenGrant(c, app)
	default:
		oauthTokenError(c, http.StatusBadRequest, "unsupported_grant_type", "grant_type is not supported")
	}
}

// authenticateOAuthClient extracts client_id and client_secret from either the
// HTTP Basic Auth header or the request body parameters.
func authenticateOAuthClient(c *gin.Context) (clientID, clientSecret string, ok bool) {
	// Try HTTP Basic Auth first.
	if auth := c.GetHeader("Authorization"); auth != "" {
		if strings.HasPrefix(auth, "Basic ") {
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(auth, "Basic "))
			if err == nil {
				parts := strings.SplitN(string(decoded), ":", 2)
				if len(parts) == 2 {
					return parts[0], parts[1], true
				}
			}
		}
	}
	// Fall back to body parameters.
	clientID = c.PostForm("client_id")
	clientSecret = c.PostForm("client_secret")
	return clientID, clientSecret, clientID != "" && clientSecret != ""
}

// handleOAuthAuthorizationCodeGrant exchanges an authorization code for tokens.
func handleOAuthAuthorizationCodeGrant(c *gin.Context, app *model.OAuthApp) {
	code := c.PostForm("code")
	redirectURI := strings.TrimSpace(c.PostForm("redirect_uri"))
	codeVerifier := strings.TrimSpace(c.PostForm("code_verifier"))

	if code == "" || redirectURI == "" || codeVerifier == "" {
		oauthTokenError(c, http.StatusBadRequest, oauthErrInvalidRequest, "code, redirect_uri, and code_verifier are required")
		return
	}

	consumed, err := model.ConsumeOAuthAuthorizationCode(code, app.Id, redirectURI)
	if err != nil {
		if errors.Is(err, model.ErrOAuthCodeNotFound) || errors.Is(err, model.ErrOAuthCodeConsumed) || errors.Is(err, model.ErrOAuthCodeExpired) {
			oauthTokenError(c, http.StatusBadRequest, oauthErrInvalidGrant, "authorization code is invalid, expired, or has been used")
			return
		}
		if errors.Is(err, model.ErrOAuthCodeMismatch) {
			oauthTokenError(c, http.StatusBadRequest, oauthErrInvalidGrant, "authorization code does not match client or redirect_uri")
			return
		}
		oauthTokenError(c, http.StatusInternalServerError, oauthErrServerError, "internal error")
		return
	}

	// Verify PKCE.
	if !VerifyPKCE(codeVerifier, consumed.CodeChallenge) {
		c.Set("oauth_user_id", consumed.UserID)
		oauthTokenError(c, http.StatusBadRequest, oauthErrInvalidGrant, "code_verifier does not match code_challenge")
		return
	}

	issueOAuthTokenResponse(c, app, consumed.UserID, consumed.SessionID, consumed.Scope, consumed.Nonce, "oauth.token")
}

// handleOAuthRefreshTokenGrant rotates a refresh token and issues new tokens.
func handleOAuthRefreshTokenGrant(c *gin.Context, app *model.OAuthApp) {
	refreshToken := strings.TrimSpace(c.PostForm("refresh_token"))
	if refreshToken == "" {
		oauthTokenError(c, http.StatusBadRequest, oauthErrInvalidRequest, "refresh_token is required")
		return
	}

	oldHash := model.OAuthRefreshTokenHash(refreshToken)
	existing, err := model.GetOAuthRefreshTokenByHash(oldHash)
	if err != nil {
		oauthTokenError(c, http.StatusBadRequest, oauthErrInvalidGrant, "refresh token is invalid, expired, or has been revoked")
		return
	}
	c.Set("oauth_user_id", existing.UserID)
	// Reject tokens that belong to another client before rotating them, so a
	// leaked token cannot be burned or its chain revoked by a different app.
	if existing.AppID != app.Id {
		oauthTokenError(c, http.StatusBadRequest, oauthErrInvalidGrant, "refresh token does not belong to this client")
		return
	}
	newRefreshPlaintext := model.GenerateOAuthRefreshToken()
	newHash := model.OAuthRefreshTokenHash(newRefreshPlaintext)
	newExpiresAt := time.Now().Add(model.OAuthRefreshTokenTTL).Unix()

	rotated, err := model.RotateOAuthRefreshToken(oldHash, newHash, newExpiresAt)
	if err != nil {
		if errors.Is(err, model.ErrOAuthRefreshTokenNotFound) || errors.Is(err, model.ErrOAuthRefreshTokenExpired) || errors.Is(err, model.ErrOAuthRefreshTokenRevoked) {
			oauthTokenError(c, http.StatusBadRequest, oauthErrInvalidGrant, "refresh token is invalid, expired, or has been revoked")
			return
		}
		oauthTokenError(c, http.StatusInternalServerError, oauthErrServerError, "internal error")
		return
	}

	issueOAuthTokenResponseWithRefresh(c, app, rotated.UserID, rotated.SessionID, rotated.Scope, newRefreshPlaintext, "oauth.token.refresh")
}

// issueOAuthTokenResponse issues access, ID, and refresh tokens for a fresh
// authorization code grant.
func issueOAuthTokenResponse(c *gin.Context, app *model.OAuthApp, userID int, sessionID, scope, nonce, auditAction string) {
	accessToken, accessExpiresAt, err := service.IssueOAuthAccessToken(userID, app.ClientID, scope)
	if err != nil {
		oauthTokenError(c, http.StatusInternalServerError, oauthErrServerError, "internal error")
		return
	}

	refreshPlaintext := model.GenerateOAuthRefreshToken()
	refresh := &model.OAuthRefreshToken{
		TokenHash: model.OAuthRefreshTokenHash(refreshPlaintext),
		AppID:     app.Id,
		UserID:    userID,
		SessionID: sessionID,
		Scope:     scope,
		ExpiresAt: time.Now().Add(model.OAuthRefreshTokenTTL).Unix(),
	}
	if err := model.CreateOAuthRefreshToken(refresh); err != nil {
		oauthTokenError(c, http.StatusInternalServerError, oauthErrServerError, "internal error")
		return
	}

	response := gin.H{
		"access_token":  accessToken,
		"token_type":    "Bearer",
		"expires_in":    int64(time.Until(time.Unix(accessExpiresAt, 0)).Seconds()),
		"refresh_token": refreshPlaintext,
		"scope":         scope,
	}
	if hasOpenIDScope(scope) {
		idToken, _, err := service.IssueOAuthIDToken(userID, app.ClientID, nonce, time.Now().Unix())
		if err != nil {
			oauthTokenError(c, http.StatusInternalServerError, oauthErrServerError, "internal error")
			return
		}
		response["id_token"] = idToken
	}
	recordOAuthAudit(c, userID, oauthAuditActorRole(userID), auditAction, true, http.StatusOK, map[string]any{
		"client_id": app.ClientID,
		"scope":     scope,
	})
	c.JSON(http.StatusOK, response)
}

// issueOAuthTokenResponseWithRefresh issues access and ID tokens using a
// pre-rotated refresh token.
func issueOAuthTokenResponseWithRefresh(c *gin.Context, app *model.OAuthApp, userID int, sessionID, scope, refreshPlaintext, auditAction string) {
	accessToken, accessExpiresAt, err := service.IssueOAuthAccessToken(userID, app.ClientID, scope)
	if err != nil {
		oauthTokenError(c, http.StatusInternalServerError, oauthErrServerError, "internal error")
		return
	}

	response := gin.H{
		"access_token":  accessToken,
		"token_type":    "Bearer",
		"expires_in":    int64(time.Until(time.Unix(accessExpiresAt, 0)).Seconds()),
		"refresh_token": refreshPlaintext,
		"scope":         scope,
	}
	if hasOpenIDScope(scope) {
		idToken, _, err := service.IssueOAuthIDToken(userID, app.ClientID, "", time.Now().Unix())
		if err != nil {
			oauthTokenError(c, http.StatusInternalServerError, oauthErrServerError, "internal error")
			return
		}
		response["id_token"] = idToken
	}
	recordOAuthAudit(c, userID, oauthAuditActorRole(userID), auditAction, true, http.StatusOK, map[string]any{
		"client_id": app.ClientID,
		"scope":     scope,
	})
	c.JSON(http.StatusOK, response)
}

// hasOpenIDScope reports whether the scope string requests OIDC identity
// claims, which is the condition for returning an ID token.
func hasOpenIDScope(scope string) bool {
	for _, s := range strings.Fields(scope) {
		if s == "openid" {
			return true
		}
	}
	return false
}

// oauthTokenError writes a standard OAuth 2.0 token error response and, when
// the grant is attributable to a user, records the failure without any code or
// token material. Client authentication failures are left to the rate limiter.
func oauthTokenError(c *gin.Context, status int, errorCode, description string) {
	if userID := c.GetInt("oauth_user_id"); userID > 0 {
		recordOAuthAudit(c, userID, oauthAuditActorRole(userID), "oauth.token.failure", false, status, map[string]any{
			"client_id":  c.GetString("oauth_client_id"),
			"grant_type": c.PostForm("grant_type"),
			"error":      errorCode,
		})
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.JSON(status, gin.H{
		"error":             errorCode,
		"error_description": description,
	})
}

// recordOAuthAudit records an OAuth provider event. Callers must pass only
// identifiers and scopes; authorization codes, tokens, and client secrets must
// never enter the audit log.
func recordOAuthAudit(c *gin.Context, userID, actorRole int, action string, success bool, status int, params map[string]any) {
	model.RecordAuditLog(c, model.AuditLog{
		UserId:    userID,
		ActorRole: actorRole,
		Category:  model.AuditCategorySecurity,
		Action:    action,
		Status:    status,
		Success:   success,
		Other:     model.AuditOther{Op: &model.AuditOperation{Action: action, Params: params}},
	})
}

// oauthAuditActorRole resolves the role of the user a token was issued for so
// the event is attributed to the correct actor in the audit trail.
func oauthAuditActorRole(userID int) int {
	user, err := model.GetUserById(userID, true)
	if err != nil {
		return 0
	}
	return user.Role
}
