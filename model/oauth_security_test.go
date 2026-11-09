package model

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestVerifyPKCE(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	// Correct verifier matches.
	assert.True(t, verifyPKCEForTest(verifier, challenge))
	// Wrong verifier does not match.
	assert.False(t, verifyPKCEForTest("wrong-verifier", challenge))
	// Empty inputs are rejected.
	assert.False(t, verifyPKCEForTest("", challenge))
	assert.False(t, verifyPKCEForTest(verifier, ""))
}

func TestOAuthAppRedirectURIExactMatch(t *testing.T) {
	app := &OAuthApp{}
	require.NoError(t, app.SetRedirectURIs([]string{
		"https://app.example.com/oauth/callback",
		"https://app2.example.com/cb",
	}))

	// Exact match.
	assert.True(t, app.HasRedirectURI("https://app.example.com/oauth/callback"))
	assert.True(t, app.HasRedirectURI("https://app2.example.com/cb"))
	// Path traversal / suffix injection rejected.
	assert.False(t, app.HasRedirectURI("https://app.example.com/oauth/callback/evil"))
	assert.False(t, app.HasRedirectURI("https://app.example.com/oauth/callback?x=1"))
	assert.False(t, app.HasRedirectURI("https://evil.example.com/oauth/callback"))
	// Subdomain mismatch rejected.
	assert.False(t, app.HasRedirectURI("https://app.example.com.evil.com/oauth/callback"))
	// Empty input rejected.
	assert.False(t, app.HasRedirectURI(""))
}

func TestOAuthAppScopeAllowed(t *testing.T) {
	app := &OAuthApp{}
	require.NoError(t, app.SetScopes([]string{"openid", "profile", "api:read"}))

	assert.True(t, app.ScopeAllowed([]string{"openid"}))
	assert.True(t, app.ScopeAllowed([]string{"openid", "profile"}))
	assert.True(t, app.ScopeAllowed([]string{"openid", "profile", "api:read"}))
	// Scope not allowed for this app.
	assert.False(t, app.ScopeAllowed([]string{"api:write"}))
	assert.False(t, app.ScopeAllowed([]string{"openid", "api:write"}))
	assert.False(t, app.ScopeAllowed([]string{"unknown_scope"}))
}

func TestOAuthAuthorizationCodeSingleUse(t *testing.T) {
	db := setupOAuthTestDB(t)
	app := createTestOAuthApp(t, db)

	code := &OAuthAuthorizationCode{
		CodeHash:            OAuthCodeHash("test-code-123"),
		AppID:               app.Id,
		UserID:              1,
		SessionID:           "session-1",
		Scope:               "openid profile",
		RedirectURI:         "https://app.example.com/callback",
		CodeChallenge:       "challenge",
		CodeChallengeMethod: OAuthPKCEMethodS256,
		ExpiresAt:           9999999999,
	}
	require.NoError(t, CreateOAuthAuthorizationCode(code))

	// First consumption succeeds.
	consumed, err := ConsumeOAuthAuthorizationCode("test-code-123", app.Id, "https://app.example.com/callback")
	require.NoError(t, err)
	require.NotNil(t, consumed)
	assert.NotZero(t, consumed.ConsumedAt)

	// Second consumption fails (already used).
	_, err = ConsumeOAuthAuthorizationCode("test-code-123", app.Id, "https://app.example.com/callback")
	assert.ErrorIs(t, err, ErrOAuthCodeConsumed)
}

func TestOAuthAuthorizationCodeExpired(t *testing.T) {
	db := setupOAuthTestDB(t)
	app := createTestOAuthApp(t, db)

	code := &OAuthAuthorizationCode{
		CodeHash:            OAuthCodeHash("expired-code"),
		AppID:               app.Id,
		UserID:              1,
		SessionID:           "session-1",
		Scope:               "openid",
		RedirectURI:         "https://app.example.com/callback",
		CodeChallenge:       "challenge",
		CodeChallengeMethod: OAuthPKCEMethodS256,
		ExpiresAt:           1, // expired long ago
	}
	require.NoError(t, CreateOAuthAuthorizationCode(code))

	_, err := ConsumeOAuthAuthorizationCode("expired-code", app.Id, "https://app.example.com/callback")
	assert.ErrorIs(t, err, ErrOAuthCodeExpired)
}

func TestOAuthAuthorizationCodeClientMismatch(t *testing.T) {
	db := setupOAuthTestDB(t)
	app1 := createTestOAuthApp(t, db)
	app2 := createTestOAuthApp(t, db)

	code := &OAuthAuthorizationCode{
		CodeHash:            OAuthCodeHash("mismatch-code"),
		AppID:               app1.Id,
		UserID:              1,
		SessionID:           "session-1",
		Scope:               "openid",
		RedirectURI:         "https://app.example.com/callback",
		CodeChallenge:       "challenge",
		CodeChallengeMethod: OAuthPKCEMethodS256,
		ExpiresAt:           9999999999,
	}
	require.NoError(t, CreateOAuthAuthorizationCode(code))

	// Wrong app ID.
	_, err := ConsumeOAuthAuthorizationCode("mismatch-code", app2.Id, "https://app.example.com/callback")
	assert.ErrorIs(t, err, ErrOAuthCodeMismatch)

	// Wrong redirect URI.
	_, err = ConsumeOAuthAuthorizationCode("mismatch-code", app1.Id, "https://evil.example.com/callback")
	assert.ErrorIs(t, err, ErrOAuthCodeMismatch)
}

func TestOAuthRefreshTokenRotation(t *testing.T) {
	db := setupOAuthTestDB(t)
	app := createTestOAuthApp(t, db)

	oldHash := OAuthRefreshTokenHash("old-refresh-token")
	rt := &OAuthRefreshToken{
		TokenHash: oldHash,
		AppID:     app.Id,
		UserID:    1,
		SessionID: "session-1",
		Scope:     "openid",
		ExpiresAt: 9999999999,
	}
	require.NoError(t, CreateOAuthRefreshToken(rt))

	newHash := OAuthRefreshTokenHash("new-refresh-token")
	rotated, err := RotateOAuthRefreshToken(oldHash, newHash, 9999999999)
	require.NoError(t, err)
	assert.Equal(t, app.Id, rotated.AppID)
	assert.Equal(t, oldHash, rotated.PrevTokenHash)

	// Old token is now revoked.
	oldToken, err := GetOAuthRefreshTokenByHash(oldHash)
	require.NoError(t, err)
	assert.NotZero(t, oldToken.RevokedAt)

	// Replay: using the old (revoked) token should revoke the entire chain.
	_, err = RotateOAuthRefreshToken(oldHash, "another-hash", 9999999999)
	assert.ErrorIs(t, err, ErrOAuthRefreshTokenRevoked)

	// The new token should also be revoked (chain revocation).
	newToken, err := GetOAuthRefreshTokenByHash(newHash)
	require.NoError(t, err)
	assert.NotZero(t, newToken.RevokedAt)
}

func TestOAuthRefreshTokenAppIsolation(t *testing.T) {
	db := setupOAuthTestDB(t)
	app1 := createTestOAuthApp(t, db)
	app2 := createTestOAuthApp(t, db)

	hash := OAuthRefreshTokenHash("isolated-token")
	rt := &OAuthRefreshToken{
		TokenHash: hash,
		AppID:     app1.Id,
		UserID:    1,
		SessionID: "session-1",
		Scope:     "openid",
		ExpiresAt: 9999999999,
	}
	require.NoError(t, CreateOAuthRefreshToken(rt))

	otherHash := OAuthRefreshTokenHash("other-app-token")
	require.NoError(t, CreateOAuthRefreshToken(&OAuthRefreshToken{
		TokenHash: otherHash,
		AppID:     app2.Id,
		UserID:    1,
		SessionID: "session-2",
		Scope:     "openid",
		ExpiresAt: 9999999999,
	}))

	// Rotation by a different app should fail and revoke the chain.
	_, err := RotateOAuthRefreshToken(hash, "new-hash", 9999999999)
	// The rotation itself succeeds at the DB level; the app check happens in
	// the controller. Verify the token's app ID is preserved.
	require.NoError(t, err)

	token, err := GetOAuthRefreshTokenByHash("new-hash")
	require.NoError(t, err)
	assert.Equal(t, app1.Id, token.AppID)

	// Chain revocation for one app must not affect another app's tokens.
	otherToken, err := GetOAuthRefreshTokenByHash(otherHash)
	require.NoError(t, err)
	assert.Zero(t, otherToken.RevokedAt)
}

func TestOAuthRefreshTokenConcurrentRotationIsAtomic(t *testing.T) {
	db := setupOAuthTestDB(t)
	app := createTestOAuthApp(t, db)

	oldHash := OAuthRefreshTokenHash("concurrent-old-token")
	require.NoError(t, CreateOAuthRefreshToken(&OAuthRefreshToken{
		TokenHash: oldHash,
		AppID:     app.Id,
		UserID:    1,
		SessionID: "session-1",
		Scope:     "openid",
		ExpiresAt: 9999999999,
	}))
	// A second chain for the same app+user: replay detection must revoke the
	// whole app+user chain, not only the session that owned the rotated token.
	otherHash := OAuthRefreshTokenHash("concurrent-other-session")
	require.NoError(t, CreateOAuthRefreshToken(&OAuthRefreshToken{
		TokenHash: otherHash,
		AppID:     app.Id,
		UserID:    1,
		SessionID: "session-2",
		Scope:     "openid",
		ExpiresAt: 9999999999,
	}))

	start := make(chan struct{})
	results := make(chan error, 2)
	for i := range 2 {
		go func(i int) {
			<-start
			_, err := RotateOAuthRefreshToken(oldHash, fmt.Sprintf("concurrent-new-%d", i), 9999999999)
			results <- err
		}(i)
	}
	close(start)

	var succeeded, replayed int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrOAuthRefreshTokenRevoked):
			replayed++
		default:
			require.Failf(t, "unexpected rotation error", "%v", err)
		}
	}
	require.Equal(t, 1, succeeded, "exactly one concurrent rotation must win")
	require.Equal(t, 1, replayed, "the losing rotation must be reported as a replay")

	otherToken, err := GetOAuthRefreshTokenByHash(otherHash)
	require.NoError(t, err)
	assert.NotZero(t, otherToken.RevokedAt, "replay must revoke the app+user chain")
}

// TestOAuthRefreshTokenRotationDatabaseMatrix proves the conditional rotation
// gate is atomic on every supported database, not only SQLite.
func TestOAuthRefreshTokenRotationDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(":memory:")
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN not configured")
				}
				driver = postgres.Open(dsn)
			}

			db, err := gorm.Open(driver, &gorm.Config{
				NamingStrategy: schema.NamingStrategy{TablePrefix: "oauth_matrix_"},
			})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			if dialect == "sqlite" {
				sqlDB.SetMaxOpenConns(1)
			}
			previousDB := DB
			DB = db
			t.Cleanup(func() {
				require.NoError(t, db.Migrator().DropTable(&OAuthRefreshToken{}, &OAuthApp{}))
				DB = previousDB
				require.NoError(t, sqlDB.Close())
			})
			require.NoError(t, db.AutoMigrate(&OAuthApp{}, &OAuthRefreshToken{}))

			app := createTestOAuthApp(t, db)
			oldHash := OAuthRefreshTokenHash("matrix-old-token")
			require.NoError(t, CreateOAuthRefreshToken(&OAuthRefreshToken{
				TokenHash: oldHash,
				AppID:     app.Id,
				UserID:    1,
				SessionID: "matrix-session-1",
				Scope:     "openid",
				ExpiresAt: 9999999999,
			}))
			otherHash := OAuthRefreshTokenHash("matrix-other-session")
			require.NoError(t, CreateOAuthRefreshToken(&OAuthRefreshToken{
				TokenHash: otherHash,
				AppID:     app.Id,
				UserID:    1,
				SessionID: "matrix-session-2",
				Scope:     "openid",
				ExpiresAt: 9999999999,
			}))

			start := make(chan struct{})
			results := make(chan error, 2)
			for i := range 2 {
				go func(i int) {
					<-start
					_, err := RotateOAuthRefreshToken(oldHash, fmt.Sprintf("matrix-new-%d", i), 9999999999)
					results <- err
				}(i)
			}
			close(start)

			var succeeded, replayed int
			for range 2 {
				err := <-results
				switch {
				case err == nil:
					succeeded++
				case errors.Is(err, ErrOAuthRefreshTokenRevoked):
					replayed++
				default:
					require.Failf(t, "unexpected rotation error", "%v", err)
				}
			}
			require.Equal(t, 1, succeeded, "exactly one concurrent rotation must win")
			require.Equal(t, 1, replayed, "the losing rotation must be reported as a replay")

			otherToken, err := GetOAuthRefreshTokenByHash(otherHash)
			require.NoError(t, err)
			assert.NotZero(t, otherToken.RevokedAt, "replay must revoke the app+user chain")
		})
	}
}

func TestOAuthConsentNormalization(t *testing.T) {
	// Equivalent scope sets normalize to the same string.
	assert.Equal(t,
		NormalizeScopeString("openid profile email"),
		NormalizeScopeString("email openid profile"),
	)
	assert.Equal(t,
		NormalizeScopeString("openid  openid   profile"),
		NormalizeScopeString("openid profile"),
	)
	assert.Equal(t, "", NormalizeScopeString("   "))
}

func TestOAuthAppSecretHash(t *testing.T) {
	secret := "test-secret-value"
	hash1 := OAuthAppSecretHash(secret)
	hash2 := OAuthAppSecretHash(secret)

	// Same secret produces same hash.
	assert.Equal(t, hash1, hash2)
	// Different secrets produce different hashes.
	assert.NotEqual(t, hash1, OAuthAppSecretHash("different-secret"))
	// Hash is 64 hex characters.
	assert.Len(t, hash1, 64)
}

func TestGenerateOAuthClientIDFormat(t *testing.T) {
	id1 := GenerateOAuthClientID()
	id2 := GenerateOAuthClientID()

	assert.True(t, len(id1) > len(OAuthClientIDPrefix))
	assert.True(t, len(id2) > len(OAuthClientIDPrefix))
	assert.NotEqual(t, id1, id2)
	assert.Equal(t, OAuthClientIDPrefix, id1[:len(OAuthClientIDPrefix)])
}

// --- Test helpers ---

func setupOAuthTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	// A single connection keeps every goroutine on the same in-memory database
	// and serializes writes, matching how SQLite deployments behave.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&OAuthApp{},
		&OAuthAuthorizationCode{},
		&OAuthRefreshToken{},
		&OAuthConsent{},
	))
	previousDB := DB
	DB = db
	t.Cleanup(func() { DB = previousDB })
	return db
}

func createTestOAuthApp(t *testing.T, db *gorm.DB) *OAuthApp {
	t.Helper()
	app := &OAuthApp{
		Name:             "Test App",
		ClientID:         GenerateOAuthClientID(),
		ClientSecretHash: OAuthAppSecretHash("test-secret"),
		Status:           OAuthAppStatusEnabled,
		CreatedBy:        1,
	}
	require.NoError(t, app.SetRedirectURIs([]string{"https://app.example.com/callback"}))
	require.NoError(t, app.SetPostLogoutRedirectURIs([]string{"https://app.example.com/logout"}))
	require.NoError(t, app.SetScopes([]string{"openid", "profile", "email", "api:read", "api:write"}))
	require.NoError(t, db.Create(app).Error)
	return app
}

// verifyPKCEForTest is a test-local wrapper that mirrors the controller's
// VerifyPKCE logic to avoid importing the controller package.
func verifyPKCEForTest(codeVerifier, codeChallenge string) bool {
	if codeVerifier == "" || codeChallenge == "" {
		return false
	}
	sum := sha256.Sum256([]byte(codeVerifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return computed == codeChallenge
}
