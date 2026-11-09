package service

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
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
	jwt.RegisteredClaims
}

var (
	oauthSigningKeyOnce sync.Once
	oauthSigningKeyRSA  *rsa.PrivateKey
	oauthSigningKeyErr  error
)

// oauthSigningKey returns the RSA key used to sign OAuth access and ID tokens.
// The key is persisted in the options table so JWKS clients can cache it and
// tokens survive restarts. When the database is unavailable (for example in
// unit tests) an in-memory key is used instead.
func oauthSigningKey() (*rsa.PrivateKey, error) {
	oauthSigningKeyOnce.Do(func() {
		oauthSigningKeyRSA, oauthSigningKeyErr = loadOrCreateOAuthSigningKey()
	})
	return oauthSigningKeyRSA, oauthSigningKeyErr
}

func loadOrCreateOAuthSigningKey() (*rsa.PrivateKey, error) {
	if model.DB != nil {
		var option model.Option
		err := model.DB.Where("key = ?", oauthSigningKeyOption).First(&option).Error
		switch {
		case err == nil:
			if key, parseErr := decodeOAuthPrivateKey(option.Value); parseErr == nil {
				return key, nil
			}
		case !errors.Is(err, gorm.ErrRecordNotFound):
			// Missing table or a transient read error: fall back to an in-memory
			// key so OAuth endpoints keep working.
			return generateOAuthPrivateKey()
		}

		key, genErr := generateOAuthPrivateKey()
		if genErr != nil {
			return nil, genErr
		}
		pemText, encErr := encodeOAuthPrivateKey(key)
		if encErr != nil {
			return key, nil
		}
		if createErr := model.DB.Create(&model.Option{Key: oauthSigningKeyOption, Value: pemText}).Error; createErr == nil {
			return key, nil
		}
		// Another instance stored the key first; use that one.
		var existing model.Option
		if readErr := model.DB.Where("key = ?", oauthSigningKeyOption).First(&existing).Error; readErr == nil {
			if existingKey, parseErr := decodeOAuthPrivateKey(existing.Value); parseErr == nil {
				return existingKey, nil
			}
		}
		return key, nil
	}
	return generateOAuthPrivateKey()
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
	if !ok {
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
	if userID <= 0 || clientID == "" {
		return "", 0, ErrOAuthTokenInvalid
	}
	user, err := model.GetUserById(userID, true)
	if err != nil {
		return "", 0, ErrOAuthTokenInvalid
	}
	now := time.Now()
	expiresAt := now.Add(OAuthAccessTokenTTL)
	claims := oauthAccessClaims{
		TokenUse:    oauthAccessTokenUse,
		Scope:       scope,
		ClientID:    clientID,
		AuthVersion: user.AuthVersion,
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
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, "", "", 0, ErrOAuthTokenInvalid
	}
	claims := &oauthAccessClaims{}
	parsed, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodRS256.Alg() {
			return nil, fmt.Errorf("%w: unexpected signing method", ErrOAuthTokenInvalid)
		}
		key, err := oauthSigningKey()
		if err != nil {
			return nil, ErrOAuthTokenInvalid
		}
		return &key.PublicKey, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}), jwt.WithIssuer(OAuthIssuer()), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(5*time.Second))
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return 0, "", "", 0, ErrOAuthTokenExpired
		}
		return 0, "", "", 0, fmt.Errorf("%w: %v", ErrOAuthTokenInvalid, err)
	}
	if !parsed.Valid || claims.TokenUse != oauthAccessTokenUse || claims.ClientID == "" {
		return 0, "", "", 0, ErrOAuthTokenInvalid
	}
	userID, err = strconv.Atoi(claims.Subject)
	if err != nil || userID <= 0 {
		return 0, "", "", 0, ErrOAuthTokenInvalid
	}
	return userID, claims.ClientID, claims.Scope, claims.AuthVersion, nil
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
	AuthTime int64  `json:"auth_time"`
	Nonce    string `json:"nonce,omitempty"`
	jwt.RegisteredClaims
}

// IssueOAuthIDToken signs an OIDC ID token for the given user.
func IssueOAuthIDToken(userID int, clientID, nonce string, authTime int64) (string, int64, error) {
	if userID <= 0 || clientID == "" {
		return "", 0, ErrOAuthTokenInvalid
	}
	now := time.Now()
	expiresAt := now.Add(OAuthIDTokenTTL)
	claims := idTokenClaims{
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

// ValidateOAuthAccessToken checks the token signature, whether the owning user
// account is still active, and whether the token's auth version is current.
// Returns the user on success.
func ValidateOAuthAccessToken(raw string) (*model.User, string, error) {
	userID, _, scope, authVersion, err := ParseOAuthAccessToken(raw)
	if err != nil {
		return nil, "", err
	}
	user, err := model.GetUserById(userID, true)
	if err != nil {
		return nil, "", ErrOAuthTokenInvalid
	}
	if user.Status != common.UserStatusEnabled || user.AuthVersion != authVersion {
		return nil, "", ErrOAuthTokenInvalid
	}
	return user, scope, nil
}
