package model

// OAuth 2.0 scope constants. These are the scopes that OAuth apps may request.
// The openid scope is required for OIDC flows.
const (
	OAuthScopeOpenID   = "openid"
	OAuthScopeProfile  = "profile"
	OAuthScopeEmail    = "email"
	OAuthScopeAPIRead  = "api:read"
	OAuthScopeAPIWrite = "api:write"
)

// AllOAuthScopes is the complete set of scopes that apps may be granted.
var AllOAuthScopes = []string{
	OAuthScopeOpenID,
	OAuthScopeProfile,
	OAuthScopeEmail,
	OAuthScopeAPIRead,
	OAuthScopeAPIWrite,
}

// OAuthScopeDescriptions maps each scope to a human-readable description shown
// on the consent screen.
var OAuthScopeDescriptions = map[string]string{
	OAuthScopeOpenID:   "Verify your identity",
	OAuthScopeProfile:  "View your username and display name",
	OAuthScopeEmail:    "View your email address",
	OAuthScopeAPIRead:  "Read AI models, usage data, and your quota/usage on your behalf",
	OAuthScopeAPIWrite: "Make AI API requests on your behalf",
}

// IsValidOAuthScope reports whether s is a known OAuth scope.
func IsValidOAuthScope(s string) bool {
	for _, scope := range AllOAuthScopes {
		if scope == s {
			return true
		}
	}
	return false
}
