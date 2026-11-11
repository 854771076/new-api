package service

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
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
	user := setupAuthSessionTestDB(t)
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
	access, _, err := IssueOAuthAccessToken(user.Id, "client-abc", "openid")
	require.NoError(t, err)
	_, _, err = ParseOAuthIDToken(access, "client-abc")
	require.ErrorIs(t, err, ErrOAuthTokenInvalid, "an access token is not an ID token or logout hint")
	_, _, _, _, err = ParseOAuthAccessToken(valid)
	require.ErrorIs(t, err, ErrOAuthTokenInvalid)
	for _, authTime := range []int64{0, time.Now().Add(time.Hour).Unix()} {
		_, _, err = IssueOAuthIDToken(user.Id, "client-abc", "", authTime)
		require.ErrorIs(t, err, ErrOAuthTokenInvalid)
	}
}

func TestOAuthSigningKeyInitializationFailsClosedAndRetries(t *testing.T) {
	setupAuthSessionTestDB(t)
	runOAuthSigningKeyInitialization(t, model.DB)
}

func TestOAuthSigningKeyInitializationDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var dialector gorm.Dialector
			switch dialect {
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN not configured")
				}
				dialector = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN not configured")
				}
				dialector = postgres.Open(dsn)
			default:
				dialector = sqlite.Open(":memory:")
			}
			// Option has no explicit TableName; verify the prefix before any DDL.
			prefix := "ok" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12] + "_"
			db, err := gorm.Open(dialector, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: prefix}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			if dialect == "sqlite" {
				sqlDB.SetMaxOpenConns(1)
			}
			previousDB := model.DB
			model.DB = db
			created := false
			t.Cleanup(func() {
				if created {
					assert.NoError(t, db.Migrator().DropTable(&model.Option{}))
				}
				model.DB = previousDB
				assert.NoError(t, sqlDB.Close())
			})
			statement := &gorm.Statement{DB: db}
			require.NoError(t, statement.Parse(&model.Option{}))
			require.Equal(t, prefix+"options", statement.Table)
			require.False(t, db.Migrator().HasTable(&model.Option{}), "never migrate or remove an existing table")
			require.NoError(t, db.Migrator().CreateTable(&model.Option{}))
			created = true
			t.Logf("isolated signing-key table=%s", statement.Table)
			runOAuthSigningKeyInitialization(t, db)
		})
	}
}

func runOAuthSigningKeyInitialization(t *testing.T, db *gorm.DB) {
	t.Helper()
	statement := &gorm.Statement{DB: db}
	require.NoError(t, statement.Parse(&model.Option{}))
	table := statement.Table
	oauthSigningKeyMutex.Lock()
	previousKey, previousDB := oauthSigningKeyRSA, oauthSigningKeyDB
	oauthSigningKeyRSA, oauthSigningKeyDB = nil, nil
	oauthSigningKeyMutex.Unlock()
	t.Cleanup(func() {
		oauthSigningKeyMutex.Lock()
		oauthSigningKeyRSA, oauthSigningKeyDB = previousKey, previousDB
		oauthSigningKeyMutex.Unlock()
	})

	model.DB = nil
	key, err := oauthSigningKey()
	require.Error(t, err)
	require.Nil(t, key)
	model.DB = db
	require.NoError(t, db.Migrator().DropTable(&model.Option{}))
	key, err = oauthSigningKey()
	require.Error(t, err)
	require.Nil(t, key)
	require.NoError(t, db.AutoMigrate(&model.Option{}))

	readFailure := errors.New("injected key read failure")
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("oauth_key_read_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == table {
			tx.AddError(readFailure)
		}
	}))
	key, err = oauthSigningKey()
	require.ErrorIs(t, err, readFailure)
	require.Nil(t, key)
	require.NoError(t, db.Callback().Query().Remove("oauth_key_read_failure"))

	createFailure := errors.New("injected key persistence failure")
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("oauth_key_create_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == table {
			tx.AddError(createFailure)
		}
	}))
	key, err = oauthSigningKey()
	require.ErrorIs(t, err, createFailure)
	require.Nil(t, key, "no ephemeral key may escape a failed persistence attempt")
	require.NoError(t, db.Callback().Create().Remove("oauth_key_create_failure"))

	// Direct loaders represent replicas with independent process caches racing
	// their first insertion. Both must select the same single persisted winner.
	type result struct {
		kid string
		err error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			key, err := loadOrCreateOAuthSigningKey()
			if err != nil {
				results <- result{err: err}
				return
			}
			results <- result{kid: oauthKeyID(&key.PublicKey)}
		}()
	}
	close(start)
	first, second := <-results, <-results
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	assert.Equal(t, first.kid, second.kid)
	key, err = oauthSigningKey()
	require.NoError(t, err, "retry succeeds once the database recovers")
	assert.Equal(t, first.kid, oauthKeyID(&key.PublicKey))
	var persisted model.Option
	require.NoError(t, db.Where(&model.Option{Key: oauthSigningKeyOption}).First(&persisted).Error)
	var count int64
	require.NoError(t, db.Model(&model.Option{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)

	// Simulate a restarted replica confronting a corrupt persisted value.
	oauthSigningKeyMutex.Lock()
	oauthSigningKeyRSA, oauthSigningKeyDB = nil, nil
	oauthSigningKeyMutex.Unlock()
	originalPEM := persisted.Value
	require.NoError(t, db.Model(&persisted).Update("value", "corrupt-key").Error)
	key, err = oauthSigningKey()
	require.Error(t, err)
	require.Nil(t, key)
	var unchanged model.Option
	require.NoError(t, db.Where(&model.Option{Key: oauthSigningKeyOption}).First(&unchanged).Error)
	assert.Equal(t, "corrupt-key", unchanged.Value, "corruption must never replace the shared key")
	require.NoError(t, db.Model(&persisted).Update("value", originalPEM).Error)
	key, err = oauthSigningKey()
	require.NoError(t, err)
	assert.Equal(t, first.kid, oauthKeyID(&key.PublicKey))
}

func TestOAuthGrantAccessTokenInvalidatedByLogout(t *testing.T) {
	user := setupAuthSessionTestDB(t)
	app := createTestOAuthApp(t, "session-bound-client", model.OAuthAppStatusEnabled)
	login, err := CreateLoginSession(user.Id, "password", "127.0.0.1", "oauth-test")
	require.NoError(t, err)
	grant := &model.OAuthRefreshToken{UserID: user.Id, AppID: app.Id, SessionID: login.Session.SID,
		AuthVersion: user.AuthVersion, AuthTime: login.Session.CreatedAt, Scope: "openid"}
	raw, _, err := IssueOAuthAccessTokenForGrant(grant, app.ClientID)
	require.NoError(t, err)
	claims, err := parseOAuthAccessClaims(raw)
	require.NoError(t, err)
	assert.Equal(t, login.Session.SID, claims.SessionID)
	assert.Equal(t, login.Session.CreatedAt, claims.AuthTime)
	_, _, err = ValidateOAuthAccessToken(raw)
	require.NoError(t, err)
	revoked, err := model.RevokeUserSession(user.Id, login.Session.SID, "logout")
	require.NoError(t, err)
	require.True(t, revoked)
	_, _, err = ValidateOAuthAccessToken(raw)
	require.ErrorIs(t, err, ErrOAuthTokenInvalid)
	_, _, err = IssueOAuthAccessTokenForGrant(grant, app.ClientID)
	require.ErrorIs(t, err, ErrOAuthTokenInvalid)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("auth_version", 2).Error)
	_, _, err = IssueOAuthAccessTokenForGrant(grant, app.ClientID)
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
