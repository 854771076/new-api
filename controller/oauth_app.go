package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// OAuthAppResponse is the public view of an OAuth app. It never includes the
// client secret or the AI model whitelist (the latter is an internal policy
// field that is not meaningful to admin users without context).
type OAuthAppResponse struct {
	Id                     int      `json:"id"`
	Name                   string   `json:"name"`
	ClientID               string   `json:"client_id"`
	RedirectURIs           []string `json:"redirect_uris"`
	PostLogoutRedirectURIs []string `json:"post_logout_redirect_uris"`
	Scopes                 []string `json:"scopes"`
	Status                 int      `json:"status"`
	PublicClient           bool     `json:"public_client"`
	CreatedBy              int      `json:"created_by"`
	CreatedAt              int64    `json:"created_at"`
	UpdatedAt              int64    `json:"updated_at"`
	SupabaseProjectRef     string   `json:"supabase_project_ref,omitempty"`
}

func toOAuthAppResponse(app *model.OAuthApp) *OAuthAppResponse {
	return &OAuthAppResponse{
		Id:                     app.Id,
		Name:                   app.Name,
		ClientID:               app.ClientID,
		RedirectURIs:           app.GetRedirectURIs(),
		PostLogoutRedirectURIs: app.GetPostLogoutRedirectURIs(),
		Scopes:                 app.GetScopes(),
		Status:                 app.Status,
		PublicClient:           app.PublicClient,
		CreatedBy:              app.CreatedBy,
		CreatedAt:              app.CreatedAt,
		UpdatedAt:              app.UpdatedAt,
		SupabaseProjectRef:     app.SupabaseProjectRef,
	}
}

// CreateOAuthAppRequest is the request body for creating an OAuth app.
type CreateOAuthAppRequest struct {
	Name                   string   `json:"name" binding:"required"`
	RedirectURIs           []string `json:"redirect_uris" binding:"required"`
	PostLogoutRedirectURIs []string `json:"post_logout_redirect_uris"`
	Scopes                 []string `json:"scopes" binding:"required"`
	SupabaseProjectRef     string   `json:"supabase_project_ref"`
	AIModelWhitelist       []string `json:"ai_model_whitelist"`
	PublicClient           bool     `json:"public_client"`
}

// CreateOAuthApp creates a new OAuth application. The client secret is
// returned in the response exactly once; only its hash is persisted.
func CreateOAuthApp(c *gin.Context) {
	var req CreateOAuthAppRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorT(c, "Invalid request parameters: {{error}}", map[string]any{"error": err.Error()})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.SupabaseProjectRef = strings.TrimSpace(req.SupabaseProjectRef)
	if req.Name == "" {
		common.ApiErrorT(c, "App name is required")
		return
	}
	if len(req.SupabaseProjectRef) > 64 {
		common.ApiErrorT(c, "supabase_project_ref exceeds 64 characters")
		return
	}

	if err := validateOAuthRedirectURIs(req.RedirectURIs); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := validateOAuthRedirectURIs(req.PostLogoutRedirectURIs); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := validateOAuthScopes(req.Scopes); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.ValidateAIModelWhitelist(req.AIModelWhitelist); err != nil {
		common.ApiError(c, err)
		return
	}

	clientID := model.GenerateOAuthClientID()
	// Public clients authenticate with PKCE only and must not hold a secret.
	clientSecret := ""
	secretHash := ""
	if !req.PublicClient {
		clientSecret = model.GenerateOAuthClientSecret()
		secretHash = model.OAuthAppSecretHash(clientSecret)
	}

	app := &model.OAuthApp{
		Name:               req.Name,
		ClientID:           clientID,
		ClientSecretHash:   secretHash,
		Status:             model.OAuthAppStatusEnabled,
		CreatedBy:          c.GetInt("id"),
		SupabaseProjectRef: req.SupabaseProjectRef,
		PublicClient:       req.PublicClient,
	}
	if err := app.SetRedirectURIs(req.RedirectURIs); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := app.SetPostLogoutRedirectURIs(req.PostLogoutRedirectURIs); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := app.SetScopes(req.Scopes); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := app.SetAIModelWhitelist(req.AIModelWhitelist); err != nil {
		common.ApiError(c, err)
		return
	}

	if err := model.CreateOAuthApp(app); err != nil {
		common.ApiError(c, err)
		return
	}
	service.InvalidateOAuthAppCache(app.ClientID)

	response := gin.H{
		"success": true,
		"message": "",
		"data":    toOAuthAppResponse(app),
	}
	if clientSecret != "" {
		response["client_secret"] = clientSecret
	}
	c.JSON(http.StatusOK, response)
}

// GetOAuthApps returns all OAuth apps.
func GetOAuthApps(c *gin.Context) {
	apps, err := model.ListOAuthApps()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	response := make([]*OAuthAppResponse, len(apps))
	for i := range apps {
		response[i] = toOAuthAppResponse(&apps[i])
	}
	common.ApiSuccess(c, response)
}

// GetOAuthApp returns a single OAuth app by ID.
func GetOAuthApp(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid ID")
		return
	}
	app, err := model.GetOAuthAppByID(id)
	if err != nil {
		common.ApiErrorT(c, "OAuth app not found")
		return
	}
	common.ApiSuccess(c, toOAuthAppResponse(app))
}

// UpdateOAuthAppRequest is the request body for updating an OAuth app.
type UpdateOAuthAppRequest struct {
	Name                   *string  `json:"name"`
	RedirectURIs           []string `json:"redirect_uris"`
	PostLogoutRedirectURIs []string `json:"post_logout_redirect_uris"`
	Scopes                 []string `json:"scopes"`
	Status                 *int     `json:"status"`
	SupabaseProjectRef     *string  `json:"supabase_project_ref"`
	AIModelWhitelist       []string `json:"ai_model_whitelist"`
	PublicClient           *bool    `json:"public_client"`
}

// UpdateOAuthApp updates mutable fields of an OAuth app.
func UpdateOAuthApp(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid ID")
		return
	}

	var req UpdateOAuthAppRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorT(c, "Invalid request parameters: {{error}}", map[string]any{"error": err.Error()})
		return
	}

	var name string
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	var status int
	if req.Status != nil {
		status = *req.Status
	}

	if err := validateOAuthRedirectURIs(req.RedirectURIs); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := validateOAuthRedirectURIs(req.PostLogoutRedirectURIs); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := validateOAuthScopes(req.Scopes); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.ValidateAIModelWhitelist(req.AIModelWhitelist); err != nil {
		common.ApiError(c, err)
		return
	}

	app, err := model.UpdateOAuthApp(id, name, req.RedirectURIs, req.PostLogoutRedirectURIs, req.Scopes, status, req.PublicClient)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	changed := false
	if req.SupabaseProjectRef != nil {
		ref := strings.TrimSpace(*req.SupabaseProjectRef)
		if len(ref) > 64 {
			common.ApiErrorT(c, "supabase_project_ref exceeds 64 characters")
			return
		}
		if app.SupabaseProjectRef != ref {
			app.SupabaseProjectRef = ref
			changed = true
		}
	}
	if req.AIModelWhitelist != nil {
		if app.SetAIModelWhitelist(req.AIModelWhitelist) == nil {
			changed = true
		}
	}

	if changed {
		app.UpdatedAt = common.GetTimestamp()
		if err := model.DB.Save(app).Error; err != nil {
			common.ApiError(c, err)
			return
		}
	}

	// The database has committed the new state. Evict the cache so the whitelist
	// and enablement checks observe it immediately; a disabled app also loses
	// all refresh tokens, while its unexpired access tokens are rejected by
	// service.ValidateOAuthAccessToken's enabled-app check.
	service.InvalidateOAuthAppCache(app.ClientID)
	if !app.IsEnabled() {
		if err := model.RevokeOAuthRefreshTokensByApp(app.Id); err != nil {
			common.SysError(common.LogText("failed to revoke oauth refresh tokens for app %d: %s", app.Id, err.Error()))
		}
	}

	common.ApiSuccess(c, toOAuthAppResponse(app))
}

// RotateOAuthAppSecret rotates the client secret. The new secret is returned
// exactly once in the response.
func RotateOAuthAppSecret(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid ID")
		return
	}
	secret, err := model.RotateOAuthAppSecret(id)
	if err != nil {
		if errors.Is(err, model.ErrOAuthAppPublicClient) {
			common.ApiErrorT(c, "Public client does not need a client secret")
			return
		}
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"message":       "",
		"client_secret": secret,
	})
}

// DeleteOAuthApp deletes an OAuth app and all its dependent records.
func DeleteOAuthApp(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid ID")
		return
	}
	app, err := model.GetOAuthAppByID(id)
	if err != nil {
		common.ApiErrorT(c, "OAuth app not found")
		return
	}
	if err := model.DeleteOAuthApp(id); err != nil {
		common.ApiError(c, err)
		return
	}
	// DeleteOAuthApp removes the app's refresh tokens and authorization codes in
	// the same transaction. Evicting the cache makes the access-token app check
	// fail closed on the next request instead of serving a deleted app.
	service.InvalidateOAuthAppCache(app.ClientID)
	common.ApiSuccessT(c, "OAuth app deleted successfully", nil)
}

// checkOAuthAppModelWhitelist enforces the OAuthApp model whitelist for relay
// requests authenticated by an OAuth access token. Requests from dashboard API
// keys, personal access tokens, or browser sessions do not carry the
// ContextKeyOAuthAppClientID context key and are never checked.
//
// The check is fail-closed: when the app record cannot be loaded, the request
// is rejected with 503 instead of being allowed; a model outside a configured
// whitelist is rejected with 403. An app without a whitelist still allows every
// model, preserving the existing policy semantics.
func checkOAuthAppModelWhitelist(c *gin.Context, modelName string) *types.NewAPIError {
	clientID := common.GetContextKeyString(c, constant.ContextKeyOAuthAppClientID)
	if clientID == "" {
		return nil
	}
	allowed, err := service.CheckOAuthAppModelWhitelist(clientID, modelName)
	if err != nil {
		common.SysLog(common.LogText("oauth_app_whitelist check failed for client_id=%s model=%s: %v (failing closed)", clientID, modelName, err))
		return types.NewErrorWithStatusCode(
			fmt.Errorf("model authorization is temporarily unavailable"),
			types.ErrorCodeAccessDenied,
			http.StatusServiceUnavailable,
			types.ErrOptionWithSkipRetry(),
		)
	}
	if allowed {
		return nil
	}
	common.SysLog(common.LogText("oauth_app_whitelist DENY client_id=%s model=%s user_id=%d", clientID, modelName, c.GetInt("id")))
	return types.NewErrorWithStatusCode(
		fmt.Errorf("insufficient model permission: %s is not in the OAuth app whitelist", modelName),
		types.ErrorCodeAccessDenied,
		http.StatusForbidden,
		types.ErrOptionWithSkipRetry(),
	)
}

// oauthPolicyModelName returns the model name used for the OAuth app whitelist
// check. Fetch endpoints do not select a channel, so they have no origin model
// on the context even though the stored task still belongs to a model; resolve
// it from the task row when an OAuth app is actually present on the request.
func oauthPolicyModelName(c *gin.Context, modelName string) string {
	if modelName != "" || common.GetContextKeyString(c, constant.ContextKeyOAuthAppClientID) == "" {
		return modelName
	}
	taskID := strings.TrimSpace(c.Param("task_id"))
	if taskID == "" {
		taskID = strings.TrimSpace(c.Param("id"))
	}
	if taskID == "" {
		return ""
	}
	if task, exists, err := model.GetByTaskId(c.GetInt("id"), taskID); err == nil && exists && task != nil {
		return task.Properties.OriginModelName
	}
	return ""
}

// taskErrorFromOAuthPolicy adapts an OAuth whitelist rejection to the task
// error envelope used by task and plugin-protocol entry points. The rejection
// is local, so it must not be retried on another channel.
func taskErrorFromOAuthPolicy(apiErr *types.NewAPIError) *taskdto.TaskError {
	taskErr := service.TaskErrorFromAPIError(apiErr)
	taskErr.LocalError = true
	return taskErr
}

// validateOAuthRedirectURIs checks that each URI is an absolute HTTPS/HTTP URL
// and that the list does not exceed the configured maximum.
func validateOAuthRedirectURIs(uris []string) error {
	if len(uris) > model.OAuthAppRedirectURIMax {
		return common.NewMessage("Too many redirect URIs")
	}
	for _, u := range uris {
		u = strings.TrimSpace(u)
		if u == "" {
			return common.NewMessage("Redirect URI cannot be empty")
		}
		if len(u) > model.OAuthAppRedirectURIMaxLength {
			return common.NewMessage("Redirect URI is too long")
		}
		parsed, err := url.Parse(u)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return common.NewMessage("Invalid redirect URI: {{uri}}", map[string]any{"uri": u})
		}
		if parsed.Scheme != "https" && parsed.Scheme != "http" {
			return common.NewMessage("Redirect URI must use http or https scheme")
		}
		if parsed.Fragment != "" {
			return common.NewMessage("Redirect URI must not contain a fragment")
		}
	}
	return nil
}

// validateOAuthScopes checks that scopes are non-empty, use allowed characters,
// and do not exceed the configured limits.
func validateOAuthScopes(scopes []string) error {
	if len(scopes) == 0 {
		return common.NewMessage("At least one scope is required")
	}
	seen := make(map[string]struct{}, len(scopes))
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if s == "" {
			return common.NewMessage("Scope cannot be empty")
		}
		if _, ok := seen[s]; ok {
			return common.NewMessage("Duplicate scope: {{scope}}", map[string]any{"scope": s})
		}
		seen[s] = struct{}{}
	}
	return nil
}
