package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const (
	OAuthAccessTokenTTL = time.Hour
	OAuthIDTokenTTL     = time.Hour
	oauthAccessTokenUse = "oauth_access"
	oauthIDTokenUse     = "id_token"
	oauthDefaultIssuer  = "new-api"
	// oauthSigningKeyOption stores the PEM-encoded RSA private key so that
	// tokens stay valid across restarts and instances share the same JWKS.
	oauthSigningKeyOption = "OAuthSigningPrivateKey"
)

// OAuthIssuer returns the issuer advertised by discovery and embedded in
// tokens. It follows the configured server address so that standard OIDC
// clients validating `iss` accept the tokens.
func OAuthIssuer() string {
	if v := strings.TrimSpace(system_setting.ServerAddress); v != "" {
		return strings.TrimRight(v, "/")
	}
	return oauthDefaultIssuer
}

var (
	ErrOAuthTokenInvalid = errors.New("oauth token is invalid")
	ErrOAuthTokenExpired = errors.New("oauth token has expired")
)

// oauthAccessClaims is the JWT claims structure for OAuth access tokens.
// AuthVersion mirrors the user's auth_version so that security-sensitive
// account changes invalidate previously issued tokens.
type oauthAccessClaims struct {
	TokenUse    string `json:"token_use"`
	Scope       string `json:"scope"`
	ClientID    string `json:"client_id"`
	AuthVersion int64  `json:"uv"`
	SessionID   string `json:"sid,omitempty"`
	AuthTime    int64  `json:"auth_time,omitempty"`
	jwt.RegisteredClaims
}

var (
	oauthSigningKeyMutex sync.Mutex
	oauthSigningKeyRSA   *rsa.PrivateKey
	oauthSigningKeyDB    *gorm.DB
)

// oauthSigningKey returns the RSA key used to sign OAuth access and ID tokens.
// The key is persisted in the options table so JWKS clients can cache it and
// tokens survive restarts. Failed initialization is not cached: endpoints fail
// closed until a persisted key can be read, and a later request may retry.
func oauthSigningKey() (*rsa.PrivateKey, error) {
	oauthSigningKeyMutex.Lock()
	defer oauthSigningKeyMutex.Unlock()
	if oauthSigningKeyRSA != nil && oauthSigningKeyDB == model.DB {
		return oauthSigningKeyRSA, nil
	}
	key, err := loadOrCreateOAuthSigningKey()
	if err != nil {
		return nil, err
	}
	oauthSigningKeyRSA, oauthSigningKeyDB = key, model.DB
	return key, nil
}

func loadOrCreateOAuthSigningKey() (*rsa.PrivateKey, error) {
	if model.DB == nil {
		return nil, errors.New("oauth signing key database is unavailable")
	}
	// Failed INSERT tracing can otherwise include the private PEM as SQL data.
	// Suppress SQL logging only for this secret-bearing initialization path.
	db := model.DB.Session(&gorm.Session{Logger: logger.Discard})
	var option model.Option
	err := db.Where(&model.Option{Key: oauthSigningKeyOption}).First(&option).Error
	if err == nil {
		// Corruption must not silently replace the key used by other replicas.
		return decodeOAuthPrivateKey(option.Value)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	key, err := generateOAuthPrivateKey()
	if err != nil {
		return nil, err
	}
	pemText, err := encodeOAuthPrivateKey(key)
	if err != nil {
		return nil, err
	}
	// Concurrent replicas race only on insertion, never overwrite. Even the
	// winner reads back the shared persisted key before signing or advertising it.
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).
		Create(&model.Option{Key: oauthSigningKeyOption, Value: pemText}).Error; err != nil {
		return nil, err
	}
	if err := db.Where(&model.Option{Key: oauthSigningKeyOption}).First(&option).Error; err != nil {
		return nil, err
	}
	return decodeOAuthPrivateKey(option.Value)
}

func generateOAuthPrivateKey() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048)
}

func encodeOAuthPrivateKey(key *rsa.PrivateKey) (string, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
}

func decodeOAuthPrivateKey(pemText string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, ErrOAuthTokenInvalid
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok || key.N.BitLen() < 2048 {
		return nil, ErrOAuthTokenInvalid
	}
	if err := key.Validate(); err != nil {
		return nil, ErrOAuthTokenInvalid
	}
	return key, nil
}

// oauthKeyID derives a stable key identifier from the public key.
func oauthKeyID(pub *rsa.PublicKey) string {
	raw := append([]byte{}, pub.N.Bytes()...)
	raw = append(raw, byte(pub.E>>8), byte(pub.E))
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(sum[:16])
}

// OAuthSigningPublicKey exposes the public key and key ID for JWKS.
func OAuthSigningPublicKey() (*rsa.PublicKey, string, error) {
	key, err := oauthSigningKey()
	if err != nil {
		return nil, "", err
	}
	return &key.PublicKey, oauthKeyID(&key.PublicKey), nil
}

// OAuthPublicJWK renders the signing public key as a JWK for /oauth/jwks.
func OAuthPublicJWK() (map[string]any, error) {
	pub, kid, err := OAuthSigningPublicKey()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"kty": "RSA",
		"kid": kid,
		"use": "sig",
		"alg": "RS256",
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}, nil
}

func signOAuthJWT(claims jwt.Claims) (string, error) {
	key, err := oauthSigningKey()
	if err != nil {
		return "", err
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = oauthKeyID(&key.PublicKey)
	return token.SignedString(key)
}

// IssueOAuthAccessToken signs an OAuth access token (JWT) for the given user,
// app, and scope. The token carries the user's auth version so that logging
// out or changing credentials invalidates it.
func IssueOAuthAccessToken(userID int, clientID, scope string) (string, int64, error) {
	user, err := model.GetUserById(userID, true)
	if err != nil || user.Status != common.UserStatusEnabled {
		return "", 0, ErrOAuthTokenInvalid
	}
	return issueOAuthAccessToken(userID, clientID, scope, user.AuthVersion, "", 0)
}

// IssueOAuthAccessTokenForGrant never upgrades a redeemed grant to the user's
// current generation. Session-bound tokens remain invalid after concurrent logout.
func IssueOAuthAccessTokenForGrant(grant *model.OAuthRefreshToken, clientID string) (string, int64, error) {
	if grant == nil {
		return "", 0, ErrOAuthTokenInvalid
	}
	if err := model.ValidateOAuthGrantSession(grant.UserID, grant.SessionID, grant.AuthVersion, grant.AuthTime); err != nil {
		return "", 0, ErrOAuthTokenInvalid
	}
	return issueOAuthAccessToken(grant.UserID, clientID, grant.Scope, grant.AuthVersion, grant.SessionID, grant.AuthTime)
}

func issueOAuthAccessToken(userID int, clientID, scope string, authVersion int64, sid string, authTime int64) (string, int64, error) {
	if userID <= 0 || clientID == "" || authVersion <= 0 {
		return "", 0, ErrOAuthTokenInvalid
	}
	now := time.Now()
	expiresAt := now.Add(OAuthAccessTokenTTL)
	claims := oauthAccessClaims{
		TokenUse:    oauthAccessTokenUse,
		Scope:       scope,
		ClientID:    clientID,
		AuthVersion: authVersion,
		SessionID:   sid,
		AuthTime:    authTime,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    OAuthIssuer(),
			Subject:   strconv.Itoa(userID),
			Audience:  jwt.ClaimStrings{clientID},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			NotBefore: jwt.NewNumericDate(now.Add(-5 * time.Second)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
	}
	signed, err := signOAuthJWT(claims)
	return signed, expiresAt.Unix(), err
}

// ParseOAuthAccessToken validates an OAuth access token and returns the user
// ID, client ID, scope, and auth version. It checks the signature, expiry, and
// token use.
func ParseOAuthAccessToken(raw string) (userID int, clientID, scope string, authVersion int64, err error) {
	claims, err := parseOAuthAccessClaims(raw)
	if err != nil {
		return 0, "", "", 0, err
	}
	userID, err = strconv.Atoi(claims.Subject)
	if err != nil || userID <= 0 {
		return 0, "", "", 0, ErrOAuthTokenInvalid
	}
	return userID, claims.ClientID, claims.Scope, claims.AuthVersion, nil
}

func parseOAuthAccessClaims(raw string) (*oauthAccessClaims, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrOAuthTokenInvalid
	}
	claims := &oauthAccessClaims{}
	parsed, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		key, err := oauthSigningKey()
		if err != nil {
			return nil, ErrOAuthTokenInvalid
		}
		return &key.PublicKey, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}), jwt.WithIssuer(OAuthIssuer()), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(5*time.Second))
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrOAuthTokenExpired
		}
		return nil, fmt.Errorf("%w: %v", ErrOAuthTokenInvalid, err)
	}
	if !parsed.Valid || claims.TokenUse != oauthAccessTokenUse || claims.ClientID == "" ||
		claims.AuthVersion <= 0 || !slices.Contains(claims.Audience, claims.ClientID) {
		return nil, ErrOAuthTokenInvalid
	}
	return claims, nil
}

// IsOAuthAccessToken reports whether the raw token carries the OAuth issuer and
// token use, without verifying the signature. Used by middleware to distinguish
// OAuth tokens from dashboard tokens and relay API keys.
func IsOAuthAccessToken(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	claims := &oauthAccessClaims{}
	parsed, _, err := jwt.NewParser().ParseUnverified(raw, claims)
	if err != nil || parsed == nil {
		return false
	}
	return claims.Issuer == OAuthIssuer() && claims.TokenUse == oauthAccessTokenUse
}

// idTokenClaims is the OIDC ID token claims structure.
type idTokenClaims struct {
	TokenUse string `json:"token_use"`
	AuthTime int64  `json:"auth_time"`
	Nonce    string `json:"nonce,omitempty"`
	jwt.RegisteredClaims
}

// IssueOAuthIDToken signs an OIDC ID token for the given user.
func IssueOAuthIDToken(userID int, clientID, nonce string, authTime int64) (string, int64, error) {
	if userID <= 0 || clientID == "" || authTime <= 0 || authTime > time.Now().Unix() {
		return "", 0, ErrOAuthTokenInvalid
	}
	now := time.Now()
	expiresAt := now.Add(OAuthIDTokenTTL)
	claims := idTokenClaims{
		TokenUse: oauthIDTokenUse,
		AuthTime: authTime,
		Nonce:    nonce,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    OAuthIssuer(),
			Subject:   strconv.Itoa(userID),
			Audience:  jwt.ClaimStrings{clientID},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			NotBefore: jwt.NewNumericDate(now.Add(-5 * time.Second)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
	}
	signed, err := signOAuthJWT(claims)
	return signed, expiresAt.Unix(), err
}

// OAuthConsentCSRFTTL bounds how long the token embedded in the consent page
// may be submitted with the consent form.
const OAuthConsentCSRFTTL = 10 * time.Minute

// ErrOAuthConsentCSRFInvalid is returned for a missing, tampered, expired, or
// session/app-mismatched consent CSRF token.
var ErrOAuthConsentCSRFInvalid = errors.New("oauth consent csrf token is invalid")

// IssueOAuthConsentCSRFToken binds a consent form submission to the dashboard
// session and OAuth app that rendered the form. The token is signed with a key
// derived from common.SessionSecret, so a cross-site attacker cannot mint one
// for a victim's session.
func IssueOAuthConsentCSRFToken(sessionID string, appID int) string {
	return issueOAuthConsentCSRFToken(sessionID, appID, time.Now())
}

func issueOAuthConsentCSRFToken(sessionID string, appID int, now time.Time) string {
	payload := fmt.Sprintf("v1:%d:%d:%s", appID, now.Add(OAuthConsentCSRFTTL).Unix(), sessionID)
	signature := oauthConsentCSRFSignature(payload)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func oauthConsentCSRFSignature(payload string) []byte {
	mac := hmac.New(sha256.New, authSigningKey("oauth-consent-csrf"))
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}

// ValidateOAuthConsentCSRFToken verifies that token was issued for exactly this
// dashboard session and OAuth app and has not expired.
func ValidateOAuthConsentCSRFToken(token, sessionID string, appID int) error {
	token = strings.TrimSpace(token)
	if token == "" || sessionID == "" || appID <= 0 || len(token) > 1024 {
		return ErrOAuthConsentCSRFInvalid
	}
	payloadPart, signaturePart, ok := strings.Cut(token, ".")
	if !ok || strings.Contains(signaturePart, ".") {
		return ErrOAuthConsentCSRFInvalid
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return ErrOAuthConsentCSRFInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(signaturePart)
	if err != nil {
		return ErrOAuthConsentCSRFInvalid
	}
	payload := string(payloadBytes)
	if !hmac.Equal(signature, oauthConsentCSRFSignature(payload)) {
		return ErrOAuthConsentCSRFInvalid
	}

	version, rest, ok := strings.Cut(payload, ":")
	if !ok || version != "v1" {
		return ErrOAuthConsentCSRFInvalid
	}
	appIDPart, rest, ok := strings.Cut(rest, ":")
	if !ok {
		return ErrOAuthConsentCSRFInvalid
	}
	expiresAtPart, payloadSessionID, ok := strings.Cut(rest, ":")
	if !ok || payloadSessionID == "" {
		return ErrOAuthConsentCSRFInvalid
	}
	payloadAppID, err := strconv.Atoi(appIDPart)
	if err != nil || payloadAppID != appID {
		return ErrOAuthConsentCSRFInvalid
	}
	expiresAt, err := strconv.ParseInt(expiresAtPart, 10, 64)
	if err != nil || expiresAt <= time.Now().Unix() {
		return ErrOAuthConsentCSRFInvalid
	}
	if payloadSessionID != sessionID {
		return ErrOAuthConsentCSRFInvalid
	}
	return nil
}

// ParseOAuthIDToken validates an OIDC ID token and returns the user ID and the
// client ID carried in the audience claim. It checks the RS256 signature,
// issuer, optional expected audience, and expiry, mirroring
// ParseOAuthAccessToken's verification options.
func ParseOAuthIDToken(raw, expectedClientID string) (userID int, clientID string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, "", ErrOAuthTokenInvalid
	}
	claims := &idTokenClaims{}
	options := []jwt.ParserOption{
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithIssuer(OAuthIssuer()),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(5 * time.Second),
	}
	if expectedClientID = strings.TrimSpace(expectedClientID); expectedClientID != "" {
		options = append(options, jwt.WithAudience(expectedClientID))
	}
	parsed, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodRS256.Alg() {
			return nil, fmt.Errorf("%w: unexpected signing method", ErrOAuthTokenInvalid)
		}
		key, err := oauthSigningKey()
		if err != nil {
			return nil, ErrOAuthTokenInvalid
		}
		return &key.PublicKey, nil
	}, options...)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return 0, "", ErrOAuthTokenExpired
		}
		return 0, "", fmt.Errorf("%w: %v", ErrOAuthTokenInvalid, err)
	}
	if !parsed.Valid || claims.TokenUse != oauthIDTokenUse || len(claims.Audience) == 0 {
		return 0, "", ErrOAuthTokenInvalid
	}
	userID, err = strconv.Atoi(claims.Subject)
	if err != nil || userID <= 0 {
		return 0, "", ErrOAuthTokenInvalid
	}
	clientID = strings.TrimSpace(claims.Audience[0])
	if clientID == "" {
		return 0, "", ErrOAuthTokenInvalid
	}
	return userID, clientID, nil
}

// ValidateOAuthAccessToken checks the token signature, whether the owning user
// account is still active, whether the issuing OAuth app still exists and is
// enabled, and whether the token's auth version is current. Returns the user on
// success. Every check is fail-closed: an unreadable or disabled app
// invalidates the token immediately, so disabling or deleting an app revokes
// its unexpired access tokens without waiting for them to expire.
func ValidateOAuthAccessToken(raw string) (*model.User, string, error) {
	claims, err := parseOAuthAccessClaims(raw)
	if err != nil {
		return nil, "", err
	}
	userID, err := strconv.Atoi(claims.Subject)
	if err != nil || userID <= 0 {
		return nil, "", ErrOAuthTokenInvalid
	}
	if claims.SessionID != "" {
		if err := model.ValidateOAuthGrantSession(userID, claims.SessionID, claims.AuthVersion, claims.AuthTime); err != nil {
			return nil, "", ErrOAuthTokenInvalid
		}
	}
	user, err := model.GetUserById(userID, true)
	if err != nil {
		return nil, "", ErrOAuthTokenInvalid
	}
	if user.Status != common.UserStatusEnabled || user.AuthVersion != claims.AuthVersion {
		return nil, "", ErrOAuthTokenInvalid
	}
	app, err := model.GetOAuthAppByClientID(claims.ClientID)
	if err != nil || !app.IsEnabled() {
		return nil, "", ErrOAuthTokenInvalid
	}
	return user, claims.Scope, nil
}
