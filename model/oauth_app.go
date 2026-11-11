package model

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const (
	OAuthAppStatusEnabled  = 1
	OAuthAppStatusDisabled = 2

	OAuthClientIDPrefix     = "nac_"
	OAuthClientSecretLength = 32

	OAuthAppRedirectURIMax       = 10
	OAuthAppPostLogoutURIMax     = 10
	OAuthAppScopeMaxLength       = 512
	OAuthAppRedirectURIMaxLength = 1024

	// OAuthAppLogoMaxLength bounds the stored logo. The value is a data URI or
	// URL stored in a TEXT column, so it must stay under MySQL's 64 KB TEXT
	// limit to remain compatible with every supported database.
	OAuthAppLogoMaxLength = 60 * 1024
)

var (
	ErrOAuthAppNotFound      = errors.New("oauth app not found")
	ErrOAuthAppDuplicateName = errors.New("oauth app name already exists")
	ErrOAuthAppInvalid       = errors.New("oauth app is invalid")
	// ErrOAuthAppPublicClient is returned when a caller tries to rotate a
	// secret for a public client, which must never hold one.
	ErrOAuthAppPublicClient = errors.New("public clients do not have a client secret")
)

// OAuthApp is a registered external application (OAuth 2.0 client).
// ClientSecret is never stored in plaintext; only its SHA-256 hash is kept.
type OAuthApp struct {
	Id                     int    `json:"id" gorm:"primaryKey"`
	Name                   string `json:"name" gorm:"type:varchar(100);not null;index"`
	LogoURL                string `json:"logo_url" gorm:"type:text;column:logo_url"`
	ClientID               string `json:"client_id" gorm:"type:varchar(64);uniqueIndex;not null;column:client_id"`
	ClientSecretHash       string `json:"-" gorm:"type:varchar(64);not null;column:client_secret_hash"`
	RedirectURIs           string `json:"-" gorm:"type:text"` // JSON array of allowed redirect URIs
	PostLogoutRedirectURIs string `json:"-" gorm:"type:text"` // JSON array of allowed post-logout redirect URIs
	Scopes                 string `json:"-" gorm:"type:text"` // JSON array of allowed scopes
	Status                 int    `json:"status" gorm:"type:int;default:1"`
	CreatedBy              int    `json:"created_by" gorm:"type:int;not null;default:0;column:created_by"`
	CreatedAt              int64  `json:"created_at" gorm:"type:bigint;autoCreateTime"`
	UpdatedAt              int64  `json:"updated_at" gorm:"type:bigint;autoUpdateTime"`

	// SupabaseProjectRef optionally binds this OAuthApp to a Supabase project.
	// It is pure metadata for operational lookup; it does not participate in
	// authentication or isolation enforcement.
	SupabaseProjectRef string `json:"-" gorm:"type:varchar(64);column:supabase_project_ref"`

	// AIModelWhitelist is a JSON array of origin model names that this app is
	// allowed to call. Empty string or empty array means no restriction. Only
	// requests authenticated by an OAuth access token are subject to this
	// check; Dashboard API keys and personal access tokens bypass it entirely.
	AIModelWhitelist string `json:"-" gorm:"type:text;column:ai_model_whitelist"`

	// PublicClient marks a public OAuth 2.0 client that cannot keep a secret
	// (for example a browser SPA). Such a client authenticates with client_id
	// only; PKCE remains mandatory. False keeps the confidential-client
	// behavior, so existing rows are unchanged after migration.
	PublicClient bool `json:"public_client" gorm:"column:public_client"`

	// WaffoPancakeReturnURL is an optional per-app override for the Waffo
	// Pancake checkout success redirect. Empty means fall back to the global
	// setting.WaffoPancakeReturnURL.
	WaffoPancakeReturnURL string `json:"-" gorm:"type:text;column:waffo_pancake_return_url"`
}

func (OAuthApp) TableName() string {
	return "oauth_apps"
}

// GetRedirectURIs returns the stored redirect URI list.
func (app *OAuthApp) GetRedirectURIs() []string {
	if app == nil || app.RedirectURIs == "" {
		return nil
	}
	var uris []string
	if err := common.UnmarshalJsonStr(app.RedirectURIs, &uris); err != nil {
		return nil
	}
	return uris
}

func (app *OAuthApp) SetRedirectURIs(uris []string) error {
	data, err := common.Marshal(uris)
	if err != nil {
		return err
	}
	app.RedirectURIs = string(data)
	return nil
}

// GetPostLogoutRedirectURIs returns the stored post-logout redirect URI list.
func (app *OAuthApp) GetPostLogoutRedirectURIs() []string {
	if app == nil || app.PostLogoutRedirectURIs == "" {
		return nil
	}
	var uris []string
	if err := common.UnmarshalJsonStr(app.PostLogoutRedirectURIs, &uris); err != nil {
		return nil
	}
	return uris
}

func (app *OAuthApp) SetPostLogoutRedirectURIs(uris []string) error {
	data, err := common.Marshal(uris)
	if err != nil {
		return err
	}
	app.PostLogoutRedirectURIs = string(data)
	return nil
}

// GetScopes returns the stored allowed scope list.
func (app *OAuthApp) GetScopes() []string {
	if app == nil || app.Scopes == "" {
		return nil
	}
	var scopes []string
	if err := common.UnmarshalJsonStr(app.Scopes, &scopes); err != nil {
		return nil
	}
	return scopes
}

func (app *OAuthApp) SetScopes(scopes []string) error {
	data, err := common.Marshal(scopes)
	if err != nil {
		return err
	}
	app.Scopes = string(data)
	return nil
}

// IsEnabled reports whether the app can participate in OAuth flows.
func (app *OAuthApp) IsEnabled() bool {
	return app != nil && app.Status == OAuthAppStatusEnabled
}

// GetAIModelWhitelist parses the stored AIModelWhitelist JSON array. It
// returns (nil, false) when the app has no whitelist (restriction disabled).
func (app *OAuthApp) GetAIModelWhitelist() ([]string, bool) {
	if app == nil || app.AIModelWhitelist == "" {
		return nil, false
	}
	var list []string
	if err := common.UnmarshalJsonStr(app.AIModelWhitelist, &list); err != nil {
		return nil, false
	}
	if len(list) == 0 {
		return nil, false
	}
	return list, true
}

// SetAIModelWhitelist stores the whitelist as a JSON array. Passing nil or
// an empty slice clears the restriction.
func (app *OAuthApp) SetAIModelWhitelist(list []string) error {
	if len(list) == 0 {
		app.AIModelWhitelist = ""
		return nil
	}
	data, err := common.Marshal(list)
	if err != nil {
		return err
	}
	app.AIModelWhitelist = string(data)
	return nil
}

// ValidateAIModelWhitelist reports whether the whitelist values are acceptable.
// Nil and empty slices are valid (they mean "no restriction"). Each entry must
// be a non-empty string with a bounded length; duplicates are rejected.
func ValidateAIModelWhitelist(list []string) error {
	const maxLen = 128
	if len(list) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(list))
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" {
			return common.NewMessage("Whitelist entry cannot be empty")
		}
		if len(s) > maxLen {
			return common.NewMessage("Whitelist entry exceeds {{max}} characters: {{name}}",
				map[string]any{"max": maxLen, "name": s})
		}
		if _, dup := seen[s]; dup {
			return common.NewMessage("Duplicate whitelist entry: {{name}}", map[string]any{"name": s})
		}
		seen[s] = struct{}{}
	}
	return nil
}

// AllowsModel reports whether the given origin model name is permitted by
// this OAuthApp's whitelist. When the whitelist is unset (no restriction) it
// always returns true.
func (app *OAuthApp) AllowsModel(modelName string) bool {
	list, ok := app.GetAIModelWhitelist()
	if !ok {
		return true
	}
	for _, m := range list {
		if m == modelName {
			return true
		}
	}
	return false
}

// HasRedirectURI reports whether uri is an exact match for one of the app's
// configured redirect URIs. Exact match only — no wildcards.
func (app *OAuthApp) HasRedirectURI(uri string) bool {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return false
	}
	for _, allowed := range app.GetRedirectURIs() {
		if allowed == uri {
			return true
		}
	}
	return false
}

// HasPostLogoutRedirectURI reports whether uri is whitelisted for logout.
func (app *OAuthApp) HasPostLogoutRedirectURI(uri string) bool {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return false
	}
	for _, allowed := range app.GetPostLogoutRedirectURIs() {
		if allowed == uri {
			return true
		}
	}
	return false
}

// ScopeAllowed reports whether every requested scope is in the app's allowed
// scope set.
func (app *OAuthApp) ScopeAllowed(requested []string) bool {
	allowed := app.GetScopes()
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, s := range allowed {
		allowedSet[s] = struct{}{}
	}
	for _, s := range requested {
		if _, ok := allowedSet[s]; !ok {
			return false
		}
	}
	return true
}

// OAuthAppSecretHash returns the SHA-256 hex digest of a client secret.
func OAuthAppSecretHash(secret string) string {
	digest := sha256.Sum256([]byte(secret))
	return fmt.Sprintf("%x", digest)
}

// GenerateOAuthClientID returns a new client identifier with the nac_ prefix.
func GenerateOAuthClientID() string {
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		// Fallback to alphanumeric random string if crypto/rand fails.
		return OAuthClientIDPrefix + common.GetRandomString(32)
	}
	return OAuthClientIDPrefix + base64.RawURLEncoding.EncodeToString(random)
}

// GenerateOAuthClientSecret returns a new opaque client secret. The plaintext
// is returned to the caller exactly once; only its hash is persisted.
func GenerateOAuthClientSecret() string {
	random := make([]byte, OAuthClientSecretLength)
	if _, err := rand.Read(random); err != nil {
		return common.GetRandomString(48)
	}
	return base64.RawURLEncoding.EncodeToString(random)
}

func GetOAuthAppByID(id int) (*OAuthApp, error) {
	var app OAuthApp
	err := DB.First(&app, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrOAuthAppNotFound
	}
	if err != nil {
		return nil, err
	}
	return &app, nil
}

func GetOAuthAppByClientID(clientID string) (*OAuthApp, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return nil, ErrOAuthAppNotFound
	}
	var app OAuthApp
	err := DB.Where("client_id = ?", clientID).First(&app).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrOAuthAppNotFound
	}
	if err != nil {
		return nil, err
	}
	return &app, nil
}

func ListOAuthApps() ([]OAuthApp, error) {
	apps := make([]OAuthApp, 0)
	err := DB.Order("id DESC").Find(&apps).Error
	return apps, err
}

// CreateOAuthApp persists a new app. A confidential client must set
// ClientSecretHash before calling; a public client has no secret at all.
func CreateOAuthApp(app *OAuthApp) error {
	if app == nil || app.Name == "" || app.ClientID == "" {
		return ErrOAuthAppInvalid
	}
	if !app.PublicClient && app.ClientSecretHash == "" {
		return ErrOAuthAppInvalid
	}
	app.Id = 0
	now := common.GetTimestamp()
	if app.CreatedAt == 0 {
		app.CreatedAt = now
	}
	app.UpdatedAt = now
	if app.Status == 0 {
		app.Status = OAuthAppStatusEnabled
	}
	return DB.Create(app).Error
}

// UpdateOAuthApp updates mutable fields of an existing app. ClientID and
// ClientSecretHash are immutable here; use RotateOAuthAppSecret for the latter.
func UpdateOAuthApp(id int, name string, redirectURIs, postLogoutURIs, scopes []string, status int, publicClient *bool) (*OAuthApp, error) {
	app, err := GetOAuthAppByID(id)
	if err != nil {
		return nil, err
	}
	if name != "" {
		app.Name = name
	}
	if redirectURIs != nil {
		if err := app.SetRedirectURIs(redirectURIs); err != nil {
			return nil, err
		}
	}
	if postLogoutURIs != nil {
		if err := app.SetPostLogoutRedirectURIs(postLogoutURIs); err != nil {
			return nil, err
		}
	}
	if scopes != nil {
		if err := app.SetScopes(scopes); err != nil {
			return nil, err
		}
	}
	if status != 0 {
		app.Status = status
	}
	if publicClient != nil {
		app.PublicClient = *publicClient
		// A public client authenticates with PKCE only; keeping a secret hash
		// would leave a credential that can never be used and could be
		// mistaken for a required secret.
		if app.PublicClient {
			app.ClientSecretHash = ""
		}
	}
	app.UpdatedAt = common.GetTimestamp()
	if err := DB.Save(app).Error; err != nil {
		return nil, err
	}
	return app, nil
}

// RotateOAuthAppSecret replaces the client secret hash. The new plaintext
// secret is returned to the caller exactly once.
func RotateOAuthAppSecret(id int) (string, error) {
	app, err := GetOAuthAppByID(id)
	if err != nil {
		return "", err
	}
	if app.PublicClient {
		return "", ErrOAuthAppPublicClient
	}
	secret := GenerateOAuthClientSecret()
	app.ClientSecretHash = OAuthAppSecretHash(secret)
	app.UpdatedAt = common.GetTimestamp()
	if err := DB.Save(app).Error; err != nil {
		return "", err
	}
	return secret, nil
}

// DeleteOAuthApp removes an app and all of its dependent authorization codes,
// refresh tokens, and consent records.
func DeleteOAuthApp(id int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("app_id = ?", id).Delete(&OAuthAuthorizationCode{}).Error; err != nil {
			return err
		}
		if err := tx.Where("app_id = ?", id).Delete(&OAuthRefreshToken{}).Error; err != nil {
			return err
		}
		if err := tx.Where("app_id = ?", id).Delete(&OAuthConsent{}).Error; err != nil {
			return err
		}
		return tx.Delete(&OAuthApp{}, id).Error
	})
}

// VerifyOAuthAppSecret reports whether the provided plaintext secret matches
// the app's stored hash.
func VerifyOAuthAppSecret(app *OAuthApp, secret string) bool {
	if app == nil || secret == "" {
		return false
	}
	return app.ClientSecretHash == OAuthAppSecretHash(secret)
}
