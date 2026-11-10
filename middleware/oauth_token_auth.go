package middleware

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// setupContextForOAuthToken validates an OAuth access token and sets up the
// relay request context as if a regular API token had been presented. The
// user's quota and group are used directly; no token-specific limits apply.
// Returns true on success (the caller should invoke c.Next()).
func setupContextForOAuthToken(c *gin.Context, rawToken string) bool {
	user, scope, err := service.ValidateOAuthAccessToken(rawToken)
	if err != nil {
		abortWithOpenAiMessage(c, http.StatusUnauthorized, common.TranslateMessage(c, i18n.MsgTokenInvalid))
		return false
	}

	// Scope-based read/write enforcement.
	if !oauthScopeAllowsMethod(c, scope) {
		abortWithOpenAiMessage(c, http.StatusForbidden, "insufficient scope: api:write required for this operation")
		return false
	}

	userCache, err := model.GetUserCache(user.Id)
	if err != nil {
		common.SysLog(common.LogText("OAuth TokenAuth GetUserCache error for user %d: %v", user.Id, err))
		abortWithOpenAiMessage(c, http.StatusInternalServerError, common.TranslateMessage(c, i18n.MsgDatabaseError))
		return false
	}
	if userCache.Status != common.UserStatusEnabled {
		abortWithOpenAiMessage(c, http.StatusForbidden, common.TranslateMessage(c, i18n.MsgAuthUserBanned))
		return false
	}

	// Resolve the OAuth App that owns this token. An OAuth relay request is
	// billed against that app's own quota pool, never the platform user quota.
	_, clientID, _, _, perr := service.ParseOAuthAccessToken(rawToken)
	if perr != nil || clientID == "" {
		abortWithOpenAiMessage(c, http.StatusUnauthorized, common.TranslateMessage(c, i18n.MsgTokenInvalid))
		return false
	}
	app, err := service.LoadOAuthAppByClientID(clientID)
	if err != nil || !app.IsEnabled() {
		abortWithOpenAiMessage(c, http.StatusForbidden, "oauth app is disabled or unavailable")
		return false
	}
	if _, err := model.EnsureUserAppWallet(user.Id, app.Id); err != nil {
		common.SysLog(common.LogText("OAuth TokenAuth EnsureUserAppWallet error for user %d app %d: %v", user.Id, app.Id, err))
		abortWithOpenAiMessage(c, http.StatusInternalServerError, common.TranslateMessage(c, i18n.MsgDatabaseError))
		return false
	}
	appQuota, err := model.GetUserAppQuota(user.Id, app.Id)
	if err != nil {
		common.SysLog(common.LogText("OAuth TokenAuth GetUserAppQuota error for user %d app %d: %v", user.Id, app.Id, err))
		abortWithOpenAiMessage(c, http.StatusInternalServerError, common.TranslateMessage(c, i18n.MsgDatabaseError))
		return false
	}

	// Set up user context.
	userCache.WriteContext(c)
	c.Set("id", user.Id)
	// Override the platform quota written by WriteContext with the app pool
	// balance so the relay pipeline sees the app-scoped funds.
	common.SetContextKey(c, constant.ContextKeyUserQuota, appQuota)
	common.SetContextKey(c, constant.ContextKeyOAuthAppID, app.Id)
	common.SetContextKey(c, constant.ContextKeyOAuthAppClientID, clientID)

	// Set up token-like context for the relay pipeline. OAuth tokens have no
	// token-level limits; the app quota and user group apply directly.
	c.Set("token_id", 0)
	c.Set("token_key", "")
	c.Set("token_name", "oauth")
	c.Set("token_unlimited_quota", false)
	c.Set("token_quota", appQuota)
	c.Set("token_model_limit_enabled", false)
	common.SetContextKey(c, constant.ContextKeyTokenGroup, user.Group)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, user.Group)
	common.SetContextKey(c, constant.ContextKeyTokenCrossGroupRetry, false)
	return true
}

// oauthScopeAllowsMethod reports whether the granted scope permits the HTTP
// method of the current request. GET/HEAD/OPTIONS require api:read; anything
// else requires api:write.
func oauthScopeAllowsMethod(c *gin.Context, scope string) bool {
	scopeSet := make(map[string]struct{})
	for _, s := range strings.Fields(scope) {
		scopeSet[s] = struct{}{}
	}
	switch c.Request.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		_, ok := scopeSet[model.OAuthScopeAPIRead]
		return ok
	default:
		_, ok := scopeSet[model.OAuthScopeAPIWrite]
		return ok
	}
}
