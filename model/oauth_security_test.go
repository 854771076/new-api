package model

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/google/uuid"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const oauthTestVerifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"

func TestOAuthGrantLifecycle(t *testing.T) {
	runOAuthGrantLifecycle(t, setupOAuthTestDB(t))
}

func runOAuthGrantLifecycle(t *testing.T, db *gorm.DB) {
	t.Helper()
	app := createTestOAuthApp(t, db)
	newCode := func(raw string) *OAuthAuthorizationCode {
		return &OAuthAuthorizationCode{
			CodeHash: OAuthCodeHash(raw), AppID: app.Id, UserID: 1, SessionID: "session-1",
			AuthVersion: 1, AuthTime: time.Now().Unix(), Scope: "openid",
			RedirectURI:         "https://app.example.com/callback",
			CodeChallenge:       "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
			CodeChallengeMethod: OAuthPKCEMethodS256, ExpiresAt: 9999999999,
		}
	}
	reset := func() {
		require.NoError(t, db.Unscoped().Model(&User{}).Where("id = ?", 1).
			Updates(map[string]interface{}{"status": common.UserStatusEnabled, "auth_version": 1, "deleted_at": nil}).Error)
		require.NoError(t, db.Model(&UserSession{}).Where("sid = ?", "session-1").
			Updates(map[string]interface{}{"user_id": 1, "user_auth_version": 1, "status": UserSessionStatusActive, "revoked_at": 0, "expires_at": int64(9999999999)}).Error)
	}
	t.Cleanup(reset)
	tests := []struct {
		name   string
		mutate func(*OAuthAuthorizationCode, *OAuthRefreshToken)
	}{
		{"credential-reset", func(_ *OAuthAuthorizationCode, _ *OAuthRefreshToken) {
			require.NoError(t, db.Model(&User{}).Where("id = ?", 1).Update("auth_version", 2).Error)
			// Even a still-live session advanced to the new generation cannot upgrade old grants.
			require.NoError(t, db.Model(&UserSession{}).Where("sid = ?", "session-1").Update("user_auth_version", 2).Error)
		}},
		{"logout", func(_ *OAuthAuthorizationCode, _ *OAuthRefreshToken) {
			revoked, err := RevokeUserSession(1, "session-1", "logout")
			require.NoError(t, err)
			require.True(t, revoked)
		}},
		{"session-expired", func(_ *OAuthAuthorizationCode, _ *OAuthRefreshToken) {
			require.NoError(t, db.Model(&UserSession{}).Where("sid = ?", "session-1").Update("expires_at", 1).Error)
		}},
		{"wrong-owner", func(_ *OAuthAuthorizationCode, _ *OAuthRefreshToken) {
			require.NoError(t, db.Model(&UserSession{}).Where("sid = ?", "session-1").Update("user_id", 2).Error)
		}},
		{"disabled-user", func(_ *OAuthAuthorizationCode, _ *OAuthRefreshToken) {
			require.NoError(t, db.Model(&User{}).Where("id = ?", 1).Update("status", common.UserStatusDisabled).Error)
		}},
		{"deleted-user", func(_ *OAuthAuthorizationCode, _ *OAuthRefreshToken) {
			require.NoError(t, db.Delete(&User{}, 1).Error)
		}},
		{"legacy-unbound", func(code *OAuthAuthorizationCode, refresh *OAuthRefreshToken) {
			require.NoError(t, db.Model(code).Updates(map[string]interface{}{"auth_version": 0, "auth_time": 0}).Error)
			require.NoError(t, db.Model(refresh).Updates(map[string]interface{}{"auth_version": 0, "auth_time": 0}).Error)
		}},
		{"missing-session", func(code *OAuthAuthorizationCode, refresh *OAuthRefreshToken) {
			require.NoError(t, db.Model(code).Update("session_id", "missing-session").Error)
			require.NoError(t, db.Model(refresh).Update("session_id", "missing-session").Error)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reset()
			raw := "lifecycle-" + test.name
			code := newCode(raw)
			require.NoError(t, CreateOAuthAuthorizationCode(code))
			assert.Equal(t, int64(1700000000), code.AuthTime, "issuance uses actual session authentication time")
			refresh := &OAuthRefreshToken{TokenHash: OAuthRefreshTokenHash(raw), AppID: app.Id, UserID: 1,
				SessionID: "session-1", AuthVersion: 1, AuthTime: code.AuthTime, Scope: "openid", ExpiresAt: 9999999999}
			require.NoError(t, CreateOAuthRefreshToken(refresh))
			test.mutate(code, refresh)
			_, err := ConsumeOAuthAuthorizationCode(raw, app.Id, code.RedirectURI, oauthTestVerifier)
			require.ErrorIs(t, err, ErrOAuthGrantSessionInvalid)
			_, err = RotateOAuthRefreshToken(refresh.TokenHash, OAuthRefreshTokenHash(raw+"-new"), 9999999999, app.Id)
			require.ErrorIs(t, err, ErrOAuthGrantSessionInvalid)
			var storedCode OAuthAuthorizationCode
			require.NoError(t, db.First(&storedCode, "code_hash = ?", code.CodeHash).Error)
			assert.Zero(t, storedCode.ConsumedAt)
			storedRefresh, err := GetOAuthRefreshTokenByHash(refresh.TokenHash)
			require.NoError(t, err)
			assert.Zero(t, storedRefresh.RevokedAt)
		})
	}
	reset()
	t.Run("issuance-and-pkce", func(t *testing.T) {
		code := newCode("lifecycle-pkce")
		code.AuthVersion = 0
		require.ErrorIs(t, CreateOAuthAuthorizationCode(code), ErrOAuthGrantSessionInvalid)
		code.AuthVersion = 2
		require.ErrorIs(t, CreateOAuthAuthorizationCode(code), ErrOAuthGrantSessionInvalid)
		code.AuthVersion = 1
		require.NoError(t, CreateOAuthAuthorizationCode(code))
		for _, verifier := range []string{"", "wrong-verifier"} {
			_, err := ConsumeOAuthAuthorizationCode("lifecycle-pkce", app.Id, code.RedirectURI, verifier)
			require.ErrorIs(t, err, ErrOAuthCodeMismatch)
		}
		consumed, err := ConsumeOAuthAuthorizationCode("lifecycle-pkce", app.Id, code.RedirectURI, oauthTestVerifier)
		require.NoError(t, err)
		assert.Equal(t, int64(1700000000), consumed.AuthTime)
		refresh := &OAuthRefreshToken{TokenHash: "lifecycle-preserve", AppID: app.Id, UserID: 1,
			SessionID: "session-1", AuthVersion: 1, AuthTime: time.Now().Unix(), Scope: "openid", ExpiresAt: 9999999999}
		require.ErrorIs(t, CreateOAuthRefreshToken(refresh), ErrOAuthGrantSessionInvalid)
		refresh.AuthTime = consumed.AuthTime
		require.NoError(t, CreateOAuthRefreshToken(refresh))
		for i := range 2 {
			refresh, err = RotateOAuthRefreshToken(refresh.TokenHash, fmt.Sprintf("lifecycle-preserve-%d", i), 9999999999, app.Id)
			require.NoError(t, err)
			assert.Equal(t, int64(1), refresh.AuthVersion)
			assert.Equal(t, int64(1700000000), refresh.AuthTime)
		}
		require.NoError(t, db.Model(refresh).Update("expires_at", 1).Error)
		_, err = RotateOAuthRefreshToken(refresh.TokenHash, "expired-replacement", 9999999999, app.Id)
		require.ErrorIs(t, err, ErrOAuthRefreshTokenExpired)
		expiredCode := newCode("lifecycle-expired")
		expiredCode.ExpiresAt = 1
		require.NoError(t, CreateOAuthAuthorizationCode(expiredCode))
		_, err = ConsumeOAuthAuthorizationCode("lifecycle-expired", app.Id, expiredCode.RedirectURI, oauthTestVerifier)
		require.ErrorIs(t, err, ErrOAuthCodeExpired)
	})
	t.Run("concurrent-code-redemption", func(t *testing.T) {
		code := newCode("lifecycle-concurrent-code")
		require.NoError(t, CreateOAuthAuthorizationCode(code))
		start, results := make(chan struct{}), make(chan error, 2)
		for range 2 {
			go func() {
				<-start
				_, err := ConsumeOAuthAuthorizationCode("lifecycle-concurrent-code", app.Id, code.RedirectURI, oauthTestVerifier)
				results <- err
			}()
		}
		close(start)
		var succeeded, rejected int
		for range 2 {
			err := <-results
			if err == nil {
				succeeded++
			} else {
				require.ErrorIs(t, err, ErrOAuthCodeConsumed)
				rejected++
			}
		}
		assert.Equal(t, 1, succeeded)
		assert.Equal(t, 1, rejected)
	})
	t.Run("in-flight-reset", func(t *testing.T) {
		code := newCode("lifecycle-reset-race")
		require.NoError(t, CreateOAuthAuthorizationCode(code))
		refresh := &OAuthRefreshToken{TokenHash: "lifecycle-reset-race-refresh", AppID: app.Id, UserID: 1,
			SessionID: "session-1", AuthVersion: 1, AuthTime: code.AuthTime, Scope: "openid", ExpiresAt: 9999999999}
		require.NoError(t, CreateOAuthRefreshToken(refresh))
		updated, release, mutation := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			mutation <- db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Model(&User{}).Where("id = ?", 1).Update("auth_version", 2).Error; err != nil {
					close(updated)
					return err
				}
				close(updated)
				<-release
				return tx.Model(&UserSession{}).Where("sid = ?", "session-1").Update("user_auth_version", 2).Error
			})
		}()
		<-updated
		results := make(chan error, 2)
		go func() {
			_, err := ConsumeOAuthAuthorizationCode("lifecycle-reset-race", app.Id, code.RedirectURI, oauthTestVerifier)
			results <- err
		}()
		go func() {
			_, err := RotateOAuthRefreshToken(refresh.TokenHash, "lifecycle-reset-race-new", 9999999999, app.Id)
			results <- err
		}()
		close(release)
		require.NoError(t, <-mutation)
		for range 2 {
			require.ErrorIs(t, <-results, ErrOAuthGrantSessionInvalid)
		}
	})
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
		AuthVersion:         1,
		CodeChallenge:       "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
		CodeChallengeMethod: OAuthPKCEMethodS256,
		ExpiresAt:           9999999999,
	}
	require.NoError(t, CreateOAuthAuthorizationCode(code))

	// First consumption succeeds.
	consumed, err := ConsumeOAuthAuthorizationCode("test-code-123", app.Id, "https://app.example.com/callback", "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
	require.NoError(t, err)
	require.NotNil(t, consumed)
	assert.NotZero(t, consumed.ConsumedAt)

	// Second consumption fails (already used).
	_, err = ConsumeOAuthAuthorizationCode("test-code-123", app.Id, "https://app.example.com/callback", "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
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
		AuthVersion:         1,
		CodeChallenge:       "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
		CodeChallengeMethod: OAuthPKCEMethodS256,
		ExpiresAt:           1, // expired long ago
	}
	require.NoError(t, CreateOAuthAuthorizationCode(code))

	_, err := ConsumeOAuthAuthorizationCode("expired-code", app.Id, "https://app.example.com/callback", "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
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
		AuthVersion:         1,
		CodeChallenge:       "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
		CodeChallengeMethod: OAuthPKCEMethodS256,
		ExpiresAt:           9999999999,
	}
	require.NoError(t, CreateOAuthAuthorizationCode(code))

	// Wrong app ID.
	_, err := ConsumeOAuthAuthorizationCode("mismatch-code", app2.Id, "https://app.example.com/callback", "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
	assert.ErrorIs(t, err, ErrOAuthCodeMismatch)

	// Wrong redirect URI.
	_, err = ConsumeOAuthAuthorizationCode("mismatch-code", app1.Id, "https://evil.example.com/callback", "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
	assert.ErrorIs(t, err, ErrOAuthCodeMismatch)
}

func TestOAuthRefreshTokenRotation(t *testing.T) {
	db := setupOAuthTestDB(t)
	app := createTestOAuthApp(t, db)

	oldHash := OAuthRefreshTokenHash("old-refresh-token")
	rt := &OAuthRefreshToken{
		TokenHash:   oldHash,
		AppID:       app.Id,
		UserID:      1,
		SessionID:   "session-1",
		AuthVersion: 1,
		AuthTime:    1700000000,
		Scope:       "openid",
		ExpiresAt:   9999999999,
	}
	require.NoError(t, CreateOAuthRefreshToken(rt))

	newHash := OAuthRefreshTokenHash("new-refresh-token")
	rotated, err := RotateOAuthRefreshToken(oldHash, newHash, 9999999999, app.Id)
	require.NoError(t, err)
	assert.Equal(t, app.Id, rotated.AppID)
	assert.Equal(t, oldHash, rotated.PrevTokenHash)

	// Old token is now revoked.
	oldToken, err := GetOAuthRefreshTokenByHash(oldHash)
	require.NoError(t, err)
	assert.NotZero(t, oldToken.RevokedAt)

	// Replay: using the old (revoked) token should revoke the entire chain.
	_, err = RotateOAuthRefreshToken(oldHash, "another-hash", 9999999999, app.Id)
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
		TokenHash:   hash,
		AppID:       app1.Id,
		UserID:      1,
		SessionID:   "session-1",
		AuthVersion: 1,
		AuthTime:    1700000000,
		Scope:       "openid",
		ExpiresAt:   9999999999,
	}
	require.NoError(t, CreateOAuthRefreshToken(rt))

	otherHash := OAuthRefreshTokenHash("other-app-token")
	require.NoError(t, CreateOAuthRefreshToken(&OAuthRefreshToken{
		TokenHash:   otherHash,
		AppID:       app2.Id,
		UserID:      1,
		SessionID:   "session-2",
		AuthVersion: 1,
		AuthTime:    1700000000,
		Scope:       "openid",
		ExpiresAt:   9999999999,
	}))

	// A different client must not consume a token or revoke its family.
	_, err := RotateOAuthRefreshToken(hash, "wrong-app-hash", 9999999999, app2.Id)
	require.ErrorIs(t, err, ErrOAuthRefreshTokenNotFound)
	old, err := GetOAuthRefreshTokenByHash(hash)
	require.NoError(t, err)
	assert.Zero(t, old.RevokedAt)
	_, err = RotateOAuthRefreshToken(hash, "new-hash", 9999999999, app1.Id)
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
		TokenHash:   oldHash,
		AppID:       app.Id,
		UserID:      1,
		SessionID:   "session-1",
		AuthVersion: 1,
		AuthTime:    1700000000,
		Scope:       "openid",
		ExpiresAt:   9999999999,
	}))
	// A second chain for the same app+user: replay detection must revoke the
	// whole app+user chain, not only the session that owned the rotated token.
	otherHash := OAuthRefreshTokenHash("concurrent-other-session")
	require.NoError(t, CreateOAuthRefreshToken(&OAuthRefreshToken{
		TokenHash:   otherHash,
		AppID:       app.Id,
		UserID:      1,
		SessionID:   "session-2",
		AuthVersion: 1,
		AuthTime:    1700000000,
		Scope:       "openid",
		ExpiresAt:   9999999999,
	}))

	start := make(chan struct{})
	results := make(chan error, 2)
	for i := range 2 {
		go func(i int) {
			<-start
			_, err := RotateOAuthRefreshToken(oldHash, fmt.Sprintf("concurrent-new-%d", i), 9999999999, app.Id)
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

			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			if dialect == "sqlite" {
				sqlDB.SetMaxOpenConns(1)
			}
			previousDB, previousDialect, previousRedis := DB, common.MainDatabaseType(), common.RedisEnabled
			DB = db
			common.SetMainDatabaseType(common.DatabaseType(dialect))
			common.RedisEnabled = false
			// TablePrefix does not override Tabler.TableName. Remap this DB's
			// private schema cache explicitly, including globally named indexes.
			prefix := "o" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8] + "_"
			var createdTables []string
			t.Cleanup(func() {
				for _, table := range createdTables {
					assert.NoError(t, db.Migrator().DropTable(table))
				}
				DB = previousDB
				common.SetMainDatabaseType(previousDialect)
				common.RedisEnabled = previousRedis
				assert.NoError(t, sqlDB.Close())
			})
			models := []any{&User{}, &UserSession{}, &OAuthApp{}, &OAuthAuthorizationCode{}, &OAuthRefreshToken{}}
			for _, value := range models {
				statement := &gorm.Statement{DB: db}
				require.NoError(t, statement.Parse(value))
				statement.Schema.Table = prefix + statement.Schema.Table
				for _, field := range statement.Schema.Fields {
					if index := field.TagSettings["INDEX"]; index != "" {
						field.TagSettings["INDEX"] = prefix + strings.ReplaceAll(index, ";", ";"+prefix)
					}
					if index := field.TagSettings["UNIQUEINDEX"]; index != "" {
						field.TagSettings["UNIQUEINDEX"] = prefix + index
					}
				}
			}
			for _, value := range models {
				statement := &gorm.Statement{DB: db}
				require.NoError(t, statement.Parse(value))
				require.True(t, strings.HasPrefix(statement.Table, prefix))
				require.False(t, db.Migrator().HasTable(value), "never migrate an existing table")
				require.NoError(t, db.Migrator().CreateTable(value))
				createdTables = append(createdTables, statement.Table)
			}
			seedOAuthSessionOwner(t, db)
			var version string
			if dialect == "sqlite" {
				require.NoError(t, db.Raw("SELECT sqlite_version()").Scan(&version).Error)
			} else {
				require.NoError(t, db.Raw("SELECT version()").Scan(&version).Error)
			}
			t.Logf("engine=%s tables=%v", version, createdTables)

			app := createTestOAuthApp(t, db)
			oldHash := OAuthRefreshTokenHash("matrix-old-token")
			require.NoError(t, CreateOAuthRefreshToken(&OAuthRefreshToken{
				TokenHash:   oldHash,
				AppID:       app.Id,
				UserID:      1,
				SessionID:   "matrix-session-1",
				AuthVersion: 1,
				AuthTime:    1700000000,
				Scope:       "openid",
				ExpiresAt:   9999999999,
			}))
			otherHash := OAuthRefreshTokenHash("matrix-other-session")
			require.NoError(t, CreateOAuthRefreshToken(&OAuthRefreshToken{
				TokenHash:   otherHash,
				AppID:       app.Id,
				UserID:      1,
				SessionID:   "matrix-session-2",
				AuthVersion: 1,
				AuthTime:    1700000000,
				Scope:       "openid",
				ExpiresAt:   9999999999,
			}))

			start := make(chan struct{})
			results := make(chan error, 2)
			for i := range 2 {
				go func(i int) {
					<-start
					_, err := RotateOAuthRefreshToken(oldHash, fmt.Sprintf("matrix-new-%d", i), 9999999999, app.Id)
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
			runOAuthGrantLifecycle(t, db)
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
		&User{},
		&UserSession{},
	))
	previousDB, previousRedis, previousDialect := DB, common.RedisEnabled, common.MainDatabaseType()
	DB = db
	common.RedisEnabled = false
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB = previousDB
		common.RedisEnabled = previousRedis
		common.SetMainDatabaseType(previousDialect)
		assert.NoError(t, sqlDB.Close())
	})
	seedOAuthSessionOwner(t, db)
	return db
}

func seedOAuthSessionOwner(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Create(&User{Id: 1, Username: "oauth-owner", Password: "unused", Status: common.UserStatusEnabled, AuthVersion: 1}).Error)
	for _, sid := range []string{"session-1", "session-2", "matrix-session-1", "matrix-session-2"} {
		require.NoError(t, db.Create(&UserSession{
			SID: sid, UserID: 1, UserAuthVersion: 1, Version: 1,
			Status: UserSessionStatusActive, RefreshHash: "unused", LoginMethod: "password",
			CreatedAt: 1700000000, LastActiveAt: time.Now().Unix(), ExpiresAt: 9999999999,
		}).Error)
	}
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
