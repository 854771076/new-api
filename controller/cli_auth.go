package controller

import (
	"errors"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// cliAuthCodeInvalidGrant is the single error surface of the exchange
// endpoint so an attacker cannot distinguish unknown, expired, or consumed
// codes (OWASP Authentication Cheat Sheet: no user/grant enumeration).
const cliAuthCodeInvalidGrant = "The authorization code is invalid, expired, or has already been used."

type cliAuthCodeCreateRequest struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

type cliAuthCodeExchangeRequest struct {
	Code string `json:"code"`
}

// CreateCLIAuthCode issues a single-use code to a signed-in browser session.
// The CLI exchanges it for a personal access token whose name and scopes are
// bound here, so the CLI can never widen its own grant. The plaintext code is
// returned once; only its hash is stored.
func CreateCLIAuthCode(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	var req cliAuthCodeCreateRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		writeAccessTokenError(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body.")
		return
	}
	name, ok := normalizeAccessTokenName(req.Name)
	if !ok {
		writeAccessTokenError(c, http.StatusBadRequest, "ACCESS_TOKEN_NAME_INVALID", "Access token name must be 1 to 64 characters.")
		return
	}
	scopes, err := service.NormalizeAccessTokenScopes(identity.UserID, c.GetInt("role"), req.Scopes)
	switch {
	case errors.Is(err, service.ErrAccessTokenScopeForbidden):
		writeAccessTokenError(c, http.StatusBadRequest, "ACCESS_TOKEN_SCOPE_FORBIDDEN", "You cannot grant one or more of these permissions.")
		return
	case err != nil:
		writeAccessTokenError(c, http.StatusBadRequest, "ACCESS_TOKEN_SCOPE_INVALID", "Select at least one valid permission.")
		return
	}
	code := model.GenerateCLIAuthCode()
	row := &model.CLIAuthCode{
		CodeHash:  model.CLIAuthCodeHash(code),
		UserID:    identity.UserID,
		Name:      name,
		ExpiresAt: common.GetTimestamp() + int64(model.CLIAuthCodeTTL/time.Second),
	}
	if err := row.SetScopes(scopes); err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if err := model.CreateCLIAuthCode(row); err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	recordUserSecurityAudit(c, identity.UserID, "cli_auth_code.issue", map[string]any{
		"name":       name,
		"scopes":     scopes,
		"expires_at": row.ExpiresAt,
	})
	common.ApiSuccess(c, gin.H{"code": code, "expires_at": row.ExpiresAt})
}

// ExchangeCLIAuthCode consumes a code and creates the personal access token
// bound to it. It takes no scope or name parameters: the grant is whatever
// the user approved in the browser. Every failure returns the same
// invalid_grant error.
func ExchangeCLIAuthCode(c *gin.Context) {
	var req cliAuthCodeExchangeRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		writeAccessTokenError(c, http.StatusBadRequest, "invalid_grant", cliAuthCodeInvalidGrant)
		return
	}
	consumed, err := model.ConsumeCLIAuthCode(req.Code)
	if err != nil {
		writeAccessTokenError(c, http.StatusBadRequest, "invalid_grant", cliAuthCodeInvalidGrant)
		return
	}
	scopes := consumed.GetScopes()
	if len(scopes) == 0 {
		writeAccessTokenError(c, http.StatusBadRequest, "invalid_grant", cliAuthCodeInvalidGrant)
		return
	}
	suffix, err := common.GenerateRandomCharsKey(43)
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	raw := model.AccessTokenPrefix + suffix
	token := &model.UserAccessToken{
		Name:      consumed.Name,
		TokenHash: model.AccessTokenFingerprint(raw),
		TokenHint: model.AccessTokenHint(raw),
		ExpiresAt: common.GetTimestamp() + int64(service.AccessTokenDefaultExpiryDays)*24*60*60,
		CreatedAt: common.GetTimestamp(),
	}
	if err := token.SetScopes(scopes); err != nil {
		writeSecurityOperationError(c, err)
		return
	}
	if err := model.CreateUserAccessToken(consumed.UserID, token, service.AccessTokenMaxPerUser); err != nil {
		if errors.Is(err, model.ErrAccessTokenLimit) {
			writeAccessTokenError(c, http.StatusBadRequest, "ACCESS_TOKEN_LIMIT", "You can create at most {{count}} access tokens", map[string]any{"count": service.AccessTokenMaxPerUser})
			return
		}
		writeSecurityOperationError(c, err)
		return
	}
	// Audit only the token fingerprint, never the plaintext code or token. The
	// endpoint is anonymous, so attribute the record to the token owner's role.
	if owner, err := model.GetUserById(consumed.UserID, false); err == nil {
		c.Set("role", owner.Role)
	}
	recordUserSecurityAudit(c, consumed.UserID, "cli_auth_code.exchange", map[string]any{
		"token_id":   token.Id,
		"name":       token.Name,
		"scopes":     scopes,
		"expires_at": token.ExpiresAt,
		"token_ref":  token.TokenHash,
	})
	common.ApiSuccess(c, gin.H{"token": raw, "expires_at": token.ExpiresAt})
}
