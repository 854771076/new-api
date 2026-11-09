package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestOAuthApp(t *testing.T, clientID string, status int) *model.OAuthApp {
	t.Helper()
	require.NoError(t, model.DB.AutoMigrate(&model.OAuthApp{}))
	app := &model.OAuthApp{
		Name:             clientID,
		ClientID:         clientID,
		ClientSecretHash: "unused-secret-hash",
		Status:           status,
		CreatedAt:        common.GetTimestamp(),
		UpdatedAt:        common.GetTimestamp(),
	}
	require.NoError(t, model.DB.Create(app).Error)
	InvalidateOAuthAppCache(clientID)
	t.Cleanup(func() { InvalidateOAuthAppCache(clientID) })
	return app
}

func TestOAuthAccessTokenInvalidatedByAuthVersionChange(t *testing.T) {
	user := setupAuthSessionTestDB(t)
	createTestOAuthApp(t, "client-abc", model.OAuthAppStatusEnabled)

	raw, expiresAt, err := IssueOAuthAccessToken(user.Id, "client-abc", "openid profile email")
	require.NoError(t, err)
	require.Positive(t, expiresAt)

	userID, clientID, scope, authVersion, err := ParseOAuthAccessToken(raw)
	require.NoError(t, err)
	assert.Equal(t, user.Id, userID)
	assert.Equal(t, "client-abc", clientID)
	assert.Equal(t, "openid profile email", scope)
	assert.Equal(t, int64(1), authVersion)

	_, _, err = ValidateOAuthAccessToken(raw)
	require.NoError(t, err)

	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("auth_version", 2).Error)

	_, _, err = ValidateOAuthAccessToken(raw)
	require.ErrorIs(t, err, ErrOAuthTokenInvalid)
}

func TestOAuthAccessTokenRejectedWhenAppDisabled(t *testing.T) {
	user := setupAuthSessionTestDB(t)
	app := createTestOAuthApp(t, "client-disable", model.OAuthAppStatusEnabled)

	raw, _, err := IssueOAuthAccessToken(user.Id, app.ClientID, "openid")
	require.NoError(t, err)
	_, _, err = ValidateOAuthAccessToken(raw)
	require.NoError(t, err)

	require.NoError(t, model.DB.Model(&model.OAuthApp{}).Where("id = ?", app.Id).Update("status", model.OAuthAppStatusDisabled).Error)

	_, _, err = ValidateOAuthAccessToken(raw)
	require.ErrorIs(t, err, ErrOAuthTokenInvalid)
}

func TestOAuthAccessTokenRejectedWhenAppMissing(t *testing.T) {
	user := setupAuthSessionTestDB(t)
	createTestOAuthApp(t, "client-present", model.OAuthAppStatusEnabled)

	raw, _, err := IssueOAuthAccessToken(user.Id, "client-missing", "openid")
	require.NoError(t, err)

	_, _, err = ValidateOAuthAccessToken(raw)
	require.ErrorIs(t, err, ErrOAuthTokenInvalid)
}

func TestParseOAuthIDToken(t *testing.T) {
	setupAuthSessionTestDB(t)
	previousSecret := common.SessionSecret
	common.SessionSecret = "oauth-id-token-test-secret"
	t.Cleanup(func() { common.SessionSecret = previousSecret })

	valid, _, err := IssueOAuthIDToken(42, "client-abc", "nonce-1", time.Now().Unix())
	require.NoError(t, err)

	userID, clientID, err := ParseOAuthIDToken(valid, "")
	require.NoError(t, err)
	assert.Equal(t, 42, userID)
	assert.Equal(t, "client-abc", clientID)

	_, _, err = ParseOAuthIDToken(valid, "client-abc")
	require.NoError(t, err)
	_, _, err = ParseOAuthIDToken(valid, "client-other")
	require.ErrorIs(t, err, ErrOAuthTokenInvalid)

	_, _, err = ParseOAuthIDToken("not-a-jwt", "")
	require.ErrorIs(t, err, ErrOAuthTokenInvalid)

	expiredClaims := idTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    OAuthIssuer(),
			Subject:   "42",
			Audience:  jwt.ClaimStrings{"client-abc"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
	}
	expired, err := signOAuthJWT(expiredClaims)
	require.NoError(t, err)
	_, _, err = ParseOAuthIDToken(expired, "")
	require.ErrorIs(t, err, ErrOAuthTokenExpired)

	wrongIssuerClaims := idTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "https://attacker.example",
			Subject:   "42",
			Audience:  jwt.ClaimStrings{"client-abc"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	wrongIssuer, err := signOAuthJWT(wrongIssuerClaims)
	require.NoError(t, err)
	_, _, err = ParseOAuthIDToken(wrongIssuer, "")
	require.ErrorIs(t, err, ErrOAuthTokenInvalid)

	wrongAlgorithm, err := jwt.NewWithClaims(jwt.SigningMethodHS256, wrongIssuerClaims).SignedString(authSigningKey("oauth-id-token-test"))
	require.NoError(t, err)
	_, _, err = ParseOAuthIDToken(wrongAlgorithm, "")
	require.ErrorIs(t, err, ErrOAuthTokenInvalid)
}

func TestOAuthConsentCSRFToken(t *testing.T) {
	previousSecret := common.SessionSecret
	common.SessionSecret = "oauth-consent-csrf-test-secret"
	t.Cleanup(func() { common.SessionSecret = previousSecret })

	token := IssueOAuthConsentCSRFToken("session-1", 7)
	require.NoError(t, ValidateOAuthConsentCSRFToken(token, "session-1", 7))

	require.ErrorIs(t, ValidateOAuthConsentCSRFToken("", "session-1", 7), ErrOAuthConsentCSRFInvalid)
	require.ErrorIs(t, ValidateOAuthConsentCSRFToken(token, "session-2", 7), ErrOAuthConsentCSRFInvalid)
	require.ErrorIs(t, ValidateOAuthConsentCSRFToken(token, "session-1", 8), ErrOAuthConsentCSRFInvalid)

	tampered := token[:len(token)-1] + "A"
	if token[len(token)-1] == 'A' {
		tampered = token[:len(token)-1] + "B"
	}
	require.ErrorIs(t, ValidateOAuthConsentCSRFToken(tampered, "session-1", 7), ErrOAuthConsentCSRFInvalid)

	expired := issueOAuthConsentCSRFToken("session-1", 7, time.Now().Add(-OAuthConsentCSRFTTL-time.Minute))
	require.ErrorIs(t, ValidateOAuthConsentCSRFToken(expired, "session-1", 7), ErrOAuthConsentCSRFInvalid)
}
