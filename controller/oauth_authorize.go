package controller

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// OAuth authorize endpoint error codes per RFC 6749 Section 4.1.2.1.
const (
	oauthErrInvalidRequest          = "invalid_request"
	oauthErrInvalidClient           = "invalid_client"
	oauthErrInvalidGrant            = "invalid_grant"
	oauthErrUnauthorizedClient      = "unauthorized_client"
	oauthErrAccessDenied            = "access_denied"
	oauthErrUnsupportedResponseType = "unsupported_response_type"
	oauthErrInvalidScope            = "invalid_scope"
	oauthErrServerError             = "server_error"
)

// OAuthAuthorizeRequest holds the parsed parameters of an authorization request.
type OAuthAuthorizeRequest struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	Scope               string
	State               string
	Nonce               string
	CodeChallenge       string
	CodeChallengeMethod string
}

// parseOAuthAuthorizeRequest extracts and validates the authorization request
// parameters. GET requests carry them in the query string; the consent form
// POSTs them in the request body, so both sources are checked.
func parseOAuthAuthorizeRequest(c *gin.Context) (*OAuthAuthorizeRequest, error) {
	get := func(name string) string {
		if v := c.Query(name); v != "" {
			return v
		}
		return c.PostForm(name)
	}
	req := &OAuthAuthorizeRequest{
		ResponseType:        get("response_type"),
		ClientID:            strings.TrimSpace(get("client_id")),
		RedirectURI:         strings.TrimSpace(get("redirect_uri")),
		Scope:               strings.TrimSpace(get("scope")),
		State:               get("state"),
		Nonce:               get("nonce"),
		CodeChallenge:       strings.TrimSpace(get("code_challenge")),
		CodeChallengeMethod: strings.TrimSpace(get("code_challenge_method")),
	}
	if req.ResponseType == "" {
		return nil, errors.New("response_type is required")
	}
	if req.ResponseType != "code" {
		return nil, errors.New("only response_type=code is supported")
	}
	if req.ClientID == "" {
		return nil, errors.New("client_id is required")
	}
	if req.RedirectURI == "" {
		return nil, errors.New("redirect_uri is required")
	}
	if req.Scope == "" {
		return nil, errors.New("scope is required")
	}
	// PKCE is mandatory for all clients.
	if req.CodeChallenge == "" {
		return nil, errors.New("code_challenge is required")
	}
	if req.CodeChallengeMethod == "" {
		req.CodeChallengeMethod = "plain"
	}
	if req.CodeChallengeMethod != model.OAuthPKCEMethodS256 {
		return nil, errors.New("only code_challenge_method=S256 is supported")
	}
	return req, nil
}

// OAuthAuthorize handles GET /oauth/authorize.
func OAuthAuthorize(c *gin.Context) {
	req, err := parseOAuthAuthorizeRequest(c)
	if err != nil {
		// If we can't identify the client or redirect_uri, show a plain error.
		c.String(http.StatusBadRequest, "Invalid authorization request: %s", err.Error())
		return
	}

	app, err := model.GetOAuthAppByClientID(req.ClientID)
	if err != nil {
		// Unknown client — cannot redirect safely.
		c.String(http.StatusBadRequest, "Unknown client_id")
		return
	}
	if !app.IsEnabled() {
		c.String(http.StatusBadRequest, "Client is disabled")
		return
	}
	if !app.HasRedirectURI(req.RedirectURI) {
		c.String(http.StatusBadRequest, "redirect_uri does not match registered URIs")
		return
	}

	requestedScopes := strings.Fields(req.Scope)
	for _, s := range requestedScopes {
		if !model.IsValidOAuthScope(s) {
			redirectWithOAuthError(c, req.RedirectURI, req.State, oauthErrInvalidScope, "unknown scope: "+s)
			return
		}
	}
	if !app.ScopeAllowed(requestedScopes) {
		redirectWithOAuthError(c, req.RedirectURI, req.State, oauthErrInvalidScope, "requested scope is not allowed for this client")
		return
	}

	// Require an active dashboard session.
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		redirectToLogin(c, req)
		return
	}

	// Check whether the user has already consented to this scope set.
	consented, err := model.HasOAuthConsent(app.Id, identity.UserID, req.Scope)
	if err != nil {
		redirectWithOAuthError(c, req.RedirectURI, req.State, oauthErrServerError, "internal error")
		return
	}
	if !consented {
		redirectToConsent(c, app, req)
		return
	}

	issueOAuthCode(c, app, identity, req)
}

// OAuthAuthorizeConsent handles POST /oauth/authorize for consent submission.
func OAuthAuthorizeConsent(c *gin.Context) {
	req, err := parseOAuthAuthorizeRequest(c)
	if err != nil {
		c.String(http.StatusBadRequest, "Invalid authorization request: %s", err.Error())
		return
	}

	app, err := model.GetOAuthAppByClientID(req.ClientID)
	if err != nil {
		c.String(http.StatusBadRequest, "Unknown client_id")
		return
	}
	if !app.IsEnabled() {
		c.String(http.StatusBadRequest, "Client is disabled")
		return
	}
	if !app.HasRedirectURI(req.RedirectURI) {
		c.String(http.StatusBadRequest, "redirect_uri does not match registered URIs")
		return
	}

	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		redirectToLogin(c, req)
		return
	}

	decision := c.PostForm("consent")
	if decision != "allow" {
		redirectWithOAuthError(c, req.RedirectURI, req.State, oauthErrAccessDenied, "user denied the authorization request")
		return
	}

	if err := model.GrantOAuthConsent(app.Id, identity.UserID, req.Scope); err != nil {
		redirectWithOAuthError(c, req.RedirectURI, req.State, oauthErrServerError, "internal error")
		return
	}

	issueOAuthCode(c, app, identity, req)
}

// issueOAuthCode generates a single-use authorization code and redirects the
// user back to the redirect_uri.
func issueOAuthCode(c *gin.Context, app *model.OAuthApp, identity service.AuthIdentity, req *OAuthAuthorizeRequest) {
	codePlaintext := model.GenerateOAuthAuthorizationCode()
	code := &model.OAuthAuthorizationCode{
		CodeHash:            model.OAuthCodeHash(codePlaintext),
		AppID:               app.Id,
		UserID:              identity.UserID,
		SessionID:           identity.SessionID,
		Scope:               model.NormalizeScopeString(req.Scope),
		Nonce:               req.Nonce,
		RedirectURI:         req.RedirectURI,
		CodeChallenge:       req.CodeChallenge,
		CodeChallengeMethod: req.CodeChallengeMethod,
		ExpiresAt:           time.Now().Add(model.OAuthAuthorizationCodeTTL).Unix(),
	}
	if err := model.CreateOAuthAuthorizationCode(code); err != nil {
		redirectWithOAuthError(c, req.RedirectURI, req.State, oauthErrServerError, "internal error")
		return
	}

	recordOAuthAudit(c, identity.UserID, c.GetInt("role"), "oauth.authorize", true, http.StatusFound, map[string]any{
		"client_id": app.ClientID,
		"scope":     code.Scope,
	})

	redirectURL, err := url.Parse(req.RedirectURI)
	if err != nil {
		redirectWithOAuthError(c, req.RedirectURI, req.State, oauthErrServerError, "internal error")
		return
	}
	q := redirectURL.Query()
	q.Set("code", codePlaintext)
	if req.State != "" {
		q.Set("state", req.State)
	}
	redirectURL.RawQuery = q.Encode()
	c.Redirect(http.StatusFound, redirectURL.String())
}

// redirectToLogin redirects an unauthenticated user to the sign-in page with a
// redirect parameter pointing back at the original authorize URL.
func redirectToLogin(c *gin.Context, req *OAuthAuthorizeRequest) {
	authorizeURL := buildOAuthAuthorizeURL(req)
	loginURL := url.URL{
		Path: "/sign-in",
	}
	q := loginURL.Query()
	q.Set("redirect", authorizeURL)
	loginURL.RawQuery = q.Encode()
	c.Redirect(http.StatusFound, loginURL.String())
}

// redirectToConsent redirects the user to the frontend consent page.
func redirectToConsent(c *gin.Context, app *model.OAuthApp, req *OAuthAuthorizeRequest) {
	consentURL := url.URL{
		Path: "/oauth/consent",
	}
	q := consentURL.Query()
	q.Set("client_id", req.ClientID)
	q.Set("redirect_uri", req.RedirectURI)
	q.Set("scope", req.Scope)
	if req.State != "" {
		q.Set("state", req.State)
	}
	if req.Nonce != "" {
		q.Set("nonce", req.Nonce)
	}
	q.Set("code_challenge", req.CodeChallenge)
	q.Set("code_challenge_method", req.CodeChallengeMethod)
	consentURL.RawQuery = q.Encode()
	c.Redirect(http.StatusFound, consentURL.String())
}

// buildOAuthAuthorizeURL reconstructs the full authorize URL from a parsed
// request, suitable for use as a login redirect target.
func buildOAuthAuthorizeURL(req *OAuthAuthorizeRequest) string {
	u := url.URL{
		Path: "/oauth/authorize",
	}
	q := u.Query()
	q.Set("response_type", req.ResponseType)
	q.Set("client_id", req.ClientID)
	q.Set("redirect_uri", req.RedirectURI)
	q.Set("scope", req.Scope)
	if req.State != "" {
		q.Set("state", req.State)
	}
	if req.Nonce != "" {
		q.Set("nonce", req.Nonce)
	}
	q.Set("code_challenge", req.CodeChallenge)
	q.Set("code_challenge_method", req.CodeChallengeMethod)
	u.RawQuery = q.Encode()
	return u.String()
}

// redirectWithOAuthError redirects to the redirect_uri with an OAuth error
// response per RFC 6749 Section 4.1.2.1.
func redirectWithOAuthError(c *gin.Context, redirectURI, state, errorCode, errorDescription string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		c.String(http.StatusBadRequest, "invalid redirect_uri")
		return
	}
	q := u.Query()
	q.Set("error", errorCode)
	if errorDescription != "" {
		q.Set("error_description", errorDescription)
	}
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	c.Redirect(http.StatusFound, u.String())
}

// VerifyPKCE checks that the provided code_verifier matches the stored
// code_challenge using the S256 method.
func VerifyPKCE(codeVerifier, codeChallenge string) bool {
	codeVerifier = strings.TrimSpace(codeVerifier)
	codeChallenge = strings.TrimSpace(codeChallenge)
	if codeVerifier == "" || codeChallenge == "" {
		return false
	}
	sum := sha256.Sum256([]byte(codeVerifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return computed == codeChallenge
}
