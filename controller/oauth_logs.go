package controller

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// OAuthLogsList handles GET /oauth/logs.
//
// Requires Authorization: Bearer <oauth access token> with scope api:read.
// Query params: page (default 1), page_size (default 20, max 100).
//
// Returns only a sanitized subset of Log fields. Prompt/response content,
// upstream API keys, internal token_id, user_id, raw "other" JSON, and the
// caller's IP are intentionally excluded from every response.
func OAuthLogsList(c *gin.Context) {
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
	if _, ok := scopeSet[model.OAuthScopeAPIRead]; !ok {
		c.Header("WWW-Authenticate", "Bearer error=\"insufficient_scope\"")
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient_scope", "error_description": "api:read scope is required"})
		return
	}

	const maxPage = 10000
	page := 1
	if p, err := strconv.Atoi(c.Query("page")); err == nil && p > 0 {
		if p > maxPage {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "error_description": "page exceeds maximum"})
			return
		}
		page = p
	}
	pageSize := 20
	if ps, err := strconv.Atoi(c.Query("page_size")); err == nil && ps > 0 {
		pageSize = min(ps, 100)
	}

	startIdx := (page - 1) * pageSize
	logs, total, err := model.GetUserLogs(user.Id, model.LogFilter{}, startIdx, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query_failed", "error_description": "failed to query logs"})
		return
	}

	items := make([]gin.H, 0, len(logs))
	for _, log := range logs {
		items = append(items, sanitizeOAuthLog(log))
	}

	c.JSON(http.StatusOK, gin.H{
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"items":     items,
	})
}

// sanitizeOAuthLog projects a *model.Log into the whitelist of fields we are
// willing to expose to OAuth clients. Fields excluded (security-sensitive or
// implementation-internal):
//   - content         may contain the caller's prompt text
//   - other           raw internal metadata (possibly upstream API keys, etc.)
//   - ip              caller's IP address
//   - token_id        internal token row id
//   - user_id         already established via the token
func sanitizeOAuthLog(log *model.Log) gin.H {
	return gin.H{
		"id":                log.Id,
		"created_at":        log.CreatedAt,
		"username":          log.Username,
		"token_name":        log.TokenName,
		"model_name":        log.ModelName,
		"quota":             log.Quota,
		"prompt_tokens":     log.PromptTokens,
		"completion_tokens": log.CompletionTokens,
		"use_time":          log.UseTime,
		"channel_name":      log.ChannelName,
		"group":             log.Group,
		"request_id":        log.RequestId,
	}
}
