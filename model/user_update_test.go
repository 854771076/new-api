package model

import (
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupUserUpdateTestState(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM users").Error)

	oldRedisEnabled := common.RedisEnabled
	oldBatchUpdateEnabled := common.BatchUpdateEnabled
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = oldRedisEnabled
		common.BatchUpdateEnabled = oldBatchUpdateEnabled
	})
}

func createUserBindTestUser(t *testing.T) User {
	t.Helper()
	user := User{
		Username:    "bind-test-user",
		Password:    "unused-password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		AffCode:     "bind-test-aff-code",
	}
	require.NoError(t, DB.Create(&user).Error)
	return user
}

func TestUserUpdateDoesNotOverwriteConcurrentAccountingOrTokenChanges(t *testing.T) {
	setupUserUpdateTestState(t)

	user := User{
		Id:              1,
		Username:        "quota-race-user",
		Password:        "password",
		DisplayName:     "before",
		Status:          common.UserStatusEnabled,
		Quota:           1000,
		UsedQuota:       20,
		RequestCount:    3,
		AffCount:        2,
		AffQuota:        800,
		AffHistoryQuota: 1200,
	}
	user.SetAccessToken("old-token")
	require.NoError(t, DB.Create(&user).Error)

	staleUser, err := GetUserById(user.Id, true)
	require.NoError(t, err)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]any{
		"quota":         gorm.Expr("quota - ?", 400),
		"used_quota":    gorm.Expr("used_quota + ?", 400),
		"request_count": gorm.Expr("request_count + ?", 1),
		"aff_count":     gorm.Expr("aff_count + ?", 1),
		"aff_quota":     gorm.Expr("aff_quota - ?", 500),
		"aff_history":   gorm.Expr("aff_history + ?", 500),
		"access_token":  "rotated-token",
	}).Error)

	staleUser.DisplayName = "after"
	require.NoError(t, staleUser.Update(false))

	var got User
	require.NoError(t, DB.First(&got, user.Id).Error)
	assert.Equal(t, "after", got.DisplayName)
	assert.Equal(t, 600, got.Quota)
	assert.Equal(t, 420, got.UsedQuota)
	assert.Equal(t, 4, got.RequestCount)
	assert.Equal(t, 3, got.AffCount)
	assert.Equal(t, 300, got.AffQuota)
	assert.Equal(t, 1700, got.AffHistoryQuota)
	assert.Equal(t, "rotated-token", got.GetAccessToken())
}

func TestUsageAccountingSupportsSignedDirectAndBatchDeltas(t *testing.T) {
	setupUserUpdateTestState(t)
	resetBatchUpdateTestState(t)

	user := User{
		Id:           10,
		Username:     "usage-adjustment-user",
		Password:     "password",
		Status:       common.UserStatusEnabled,
		UsedQuota:    1000,
		RequestCount: 3,
	}
	channel := Channel{
		Id:        10,
		Name:      "usage-adjustment-channel",
		Key:       "sk-test",
		Status:    common.ChannelStatusEnabled,
		UsedQuota: 1000,
	}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Create(&channel).Error)

	UpdateUserUsedQuota(user.Id, -200)
	UpdateUserUsedQuota(user.Id, 50)
	UpdateChannelUsedQuota(channel.Id, -200)
	UpdateChannelUsedQuota(channel.Id, 50)

	var got User
	require.NoError(t, DB.Select("used_quota", "request_count").First(&got, user.Id).Error)
	assert.Equal(t, 850, got.UsedQuota)
	assert.Equal(t, 3, got.RequestCount)
	var gotChannel Channel
	require.NoError(t, DB.Select("used_quota").First(&gotChannel, channel.Id).Error)
	assert.Equal(t, int64(850), gotChannel.UsedQuota)

	common.BatchUpdateEnabled = true
	UpdateUserUsedQuota(user.Id, 400)
	UpdateUserUsedQuota(user.Id, -100)
	UpdateChannelUsedQuota(channel.Id, 400)
	UpdateChannelUsedQuota(channel.Id, -100)

	require.NoError(t, DB.Select("used_quota", "request_count").First(&got, user.Id).Error)
	assert.Equal(t, 850, got.UsedQuota, "batch deltas must remain queued until flush")
	assert.Equal(t, 3, got.RequestCount)
	require.NoError(t, DB.Select("used_quota").First(&gotChannel, channel.Id).Error)
	assert.Equal(t, int64(850), gotChannel.UsedQuota, "batch deltas must remain queued until flush")

	batchUpdate()
	require.NoError(t, DB.Select("used_quota", "request_count").First(&got, user.Id).Error)
	assert.Equal(t, 1150, got.UsedQuota)
	assert.Equal(t, 3, got.RequestCount)
	require.NoError(t, DB.Select("used_quota").First(&gotChannel, channel.Id).Error)
	assert.Equal(t, int64(1150), gotChannel.UsedQuota)
}

func TestRevokeUserAccessTokenOnlyClearsLegacyToken(t *testing.T) {
	setupUserUpdateTestState(t)

	createdAt := int64(100)
	user := User{
		Id:                   2,
		Username:             "token-revoke-user",
		Password:             "password",
		DisplayName:          "before",
		Status:               common.UserStatusEnabled,
		Quota:                1000,
		AffQuota:             800,
		AffHistoryQuota:      1200,
		AccessTokenCreatedAt: &createdAt,
	}
	user.SetAccessToken("legacy-token")
	require.NoError(t, DB.Create(&user).Error)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]any{
		"quota":        gorm.Expr("quota + ?", 500),
		"aff_quota":    gorm.Expr("aff_quota - ?", 500),
		"display_name": "concurrent-update",
	}).Error)

	tokenRef, err := RevokeUserAccessToken(user.Id)
	require.NoError(t, err)
	assert.Equal(t, AccessTokenFingerprint("legacy-token"), tokenRef)

	var got User
	require.NoError(t, DB.First(&got, user.Id).Error)
	assert.Empty(t, got.GetAccessToken())
	assert.Nil(t, got.AccessTokenCreatedAt)
	assert.Equal(t, "concurrent-update", got.DisplayName)
	assert.Equal(t, 1500, got.Quota)
	assert.Equal(t, 300, got.AffQuota)
	assert.Equal(t, 1200, got.AffHistoryQuota)
}

func TestRevokeUserAccessTokenRejectsSoftDeletedUser(t *testing.T) {
	setupUserUpdateTestState(t)

	user := User{
		Id:       3,
		Username: "deleted-token-revoke-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
	}
	user.SetAccessToken("old-token")
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Delete(&user).Error)

	_, err := RevokeUserAccessToken(user.Id)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	var got User
	require.NoError(t, DB.Unscoped().First(&got, user.Id).Error)
	assert.Equal(t, "old-token", got.GetAccessToken())
}

func TestUpdateUserSettingOnlyUpdatesSetting(t *testing.T) {
	setupUserUpdateTestState(t)

	user := User{
		Id:           2,
		Username:     "setting-user",
		Password:     "password",
		Status:       common.UserStatusEnabled,
		Quota:        1000,
		UsedQuota:    20,
		RequestCount: 3,
	}
	require.NoError(t, DB.Create(&user).Error)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]any{
		"quota":         gorm.Expr("quota - ?", 250),
		"used_quota":    gorm.Expr("used_quota + ?", 250),
		"request_count": gorm.Expr("request_count + ?", 1),
	}).Error)

	require.NoError(t, UpdateUserSetting(user.Id, dto.UserSetting{Language: "zh"}))

	var got User
	require.NoError(t, DB.First(&got, user.Id).Error)
	assert.Equal(t, 750, got.Quota)
	assert.Equal(t, 270, got.UsedQuota)
	assert.Equal(t, 4, got.RequestCount)
	assert.Equal(t, "zh", got.GetSetting().Language)
}

func TestEnsureEmailAvailableRejectsExistingEmailCaseInsensitive(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "existing",
		Password: "old-password",
		Email:    "Taken@Example.com",
		Status:   common.UserStatusEnabled,
	}).Error)

	err := EnsureEmailAvailable(" taken@example.COM ", 0)
	require.ErrorIs(t, err, ErrEmailAlreadyTaken)

	user, err := GetUniqueUserByEmail("TAKEN@example.com")
	require.NoError(t, err)
	assert.Equal(t, "existing", user.Username)

	require.NoError(t, EnsureEmailAvailable("taken@example.com", user.Id))
}

func TestInsertRejectsDuplicateEmailWithoutUniqueIndex(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "existing",
		Password: "old-password",
		Email:    "taken@example.com",
		Status:   common.UserStatusEnabled,
	}).Error)

	user := &User{
		Username: "oauth-user",
		Email:    "TAKEN@example.com",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}

	err := user.Insert(0)
	require.ErrorIs(t, err, ErrEmailAlreadyTaken)

	var count int64
	require.NoError(t, DB.Model(&User{}).Where("username = ?", "oauth-user").Count(&count).Error)
	assert.Zero(t, count)
}

func TestInsertKeepsBlankPasswordForPasswordlessUser(t *testing.T) {
	setupUserUpdateTestState(t)

	user := &User{
		Username: "passwordless-user",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}

	require.NoError(t, user.Insert(0))

	var stored User
	require.NoError(t, DB.Where("username = ?", user.Username).First(&stored).Error)
	assert.Empty(t, stored.Password)
}

func TestUpdateUserBindColumnOnlyTouchesTheBindingColumn(t *testing.T) {
	truncateTables(t)

	user := createUserBindTestUser(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]any{
		"role":   common.RoleAdminUser,
		"status": common.UserStatusEnabled,
		"group":  "vip",
	}).Error)

	require.NoError(t, UpdateUserBindColumn(user.Id, "github_id", "gh-12345"))

	reloaded, err := GetUserById(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "gh-12345", reloaded.GitHubId)
	assert.Equal(t, common.RoleAdminUser, reloaded.Role)
	assert.Equal(t, common.UserStatusEnabled, reloaded.Status)
	assert.Equal(t, "vip", reloaded.Group)
}

func TestUpdateUserBindColumnPreservesRestrictiveChange(t *testing.T) {
	truncateTables(t)

	user := createUserBindTestUser(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).
		Update("status", common.UserStatusDisabled).Error)
	require.NoError(t, UpdateUserBindColumn(user.Id, "wechat_id", "wx-open-id"))

	reloaded, err := GetUserById(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "wx-open-id", reloaded.WeChatId)
	assert.Equal(t, common.UserStatusDisabled, reloaded.Status)
}

func TestUpdateUserBindColumnRejectsNonWhitelistedColumns(t *testing.T) {
	truncateTables(t)

	user := createUserBindTestUser(t)
	for _, column := range []string{"role", "status", "group", "quota", "username", "password", "id"} {
		assert.Error(t, UpdateUserBindColumn(user.Id, column, "1"), "column %s must be rejected", column)
	}
	assert.Error(t, UpdateUserBindColumn(user.Id, "github_id; DROP TABLE users", "x"))
	assert.Error(t, UpdateUserBindColumn(0, "github_id", "x"))
}

func TestValidateAndFillRejectsPasswordlessUser(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "passwordless-user",
		Password: "",
		Status:   common.UserStatusEnabled,
	}).Error)

	loginUser := User{
		Username: "passwordless-user",
		Password: "NewPassword123",
	}
	err := loginUser.ValidateAndFill()
	require.ErrorIs(t, err, ErrInvalidCredentials)

	var stored User
	require.NoError(t, DB.Where("username = ?", "passwordless-user").First(&stored).Error)
	assert.Empty(t, stored.Password)
}

func TestResetUserPasswordByEmailRequiresSingleActiveMatch(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "duplicate-1",
		Password: "old-1",
		Email:    "legacy@example.com",
		AffCode:  "dupe1",
		Status:   common.UserStatusEnabled,
	}).Error)
	require.NoError(t, DB.Create(&User{
		Username: "duplicate-2",
		Password: "old-2",
		Email:    "LEGACY@example.com",
		AffCode:  "dupe2",
		Status:   common.UserStatusEnabled,
	}).Error)

	err := ResetUserPasswordByEmail("legacy@example.com", "NewPassword123")
	require.ErrorIs(t, err, ErrEmailAmbiguous)

	var duplicates []User
	require.NoError(t, DB.Where("LOWER(email) = ?", "legacy@example.com").Order("username asc").Find(&duplicates).Error)
	require.Len(t, duplicates, 2)
	assert.Equal(t, "old-1", duplicates[0].Password)
	assert.Equal(t, "old-2", duplicates[1].Password)

	require.NoError(t, DB.Create(&User{
		Username: "unique",
		Password: "old",
		Email:    "unique@example.com",
		AffCode:  "unique",
		Status:   common.UserStatusEnabled,
	}).Error)

	require.NoError(t, ResetUserPasswordByEmail("UNIQUE@example.com", "NewPassword123"))

	var unique User
	require.NoError(t, DB.Where("username = ?", "unique").First(&unique).Error)
	assert.True(t, common.ValidatePasswordAndHash("NewPassword123", unique.Password))

	err = ResetUserPasswordByEmail("missing@example.com", "NewPassword123")
	require.True(t, errors.Is(err, ErrEmailNotFound))
}

func createAccessTokenTestUser(t *testing.T, username string) User {
	t.Helper()
	user := User{
		Username:    username,
		Password:    "unused-password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		AffCode:     username + "-aff",
	}
	require.NoError(t, DB.Create(&user).Error)
	return user
}

func createTestUserAccessToken(t *testing.T, userID int, raw string, expiresAt int64) UserAccessToken {
	t.Helper()
	token := UserAccessToken{Name: raw, TokenHash: AccessTokenFingerprint(raw), TokenHint: AccessTokenHint(raw), ExpiresAt: expiresAt}
	require.NoError(t, token.SetScopes([]string{"profile:read"}))
	require.NoError(t, CreateUserAccessToken(userID, &token, 20))
	return token
}

func TestLegacyAccessTokenRetireAtIsWrittenOnceAndReadOnly(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Option{}))
	previous := legacyAccessTokenRetireAt.Load()
	originalMap := common.OptionMap
	common.OptionMap = map[string]string{}
	require.NoError(t, DB.Where(&Option{Key: legacyAccessTokenRetireAtKey}).Delete(&Option{}).Error)
	t.Cleanup(func() {
		legacyAccessTokenRetireAt.Store(previous)
		common.OptionMap = originalMap
		DB.Where(&Option{Key: legacyAccessTokenRetireAtKey}).Delete(&Option{})
	})

	const firstStart = int64(1_800_000_000)
	want := firstStart + 30*24*60*60
	require.NoError(t, EnsureLegacyAccessTokenRetireAt(firstStart))
	assert.Equal(t, want, LegacyAccessTokenRetireAt())

	require.NoError(t, EnsureLegacyAccessTokenRetireAt(firstStart+24*60*60))
	assert.Equal(t, want, LegacyAccessTokenRetireAt())
	var stored Option
	require.NoError(t, DB.Where(&Option{Key: legacyAccessTokenRetireAtKey}).First(&stored).Error)
	assert.Equal(t, strconv.FormatInt(want, 10), stored.Value)

	require.ErrorIs(t, UpdateOption(legacyAccessTokenRetireAtKey, "1"), errLegacyRetireAtReadOnly)
	require.ErrorIs(t, UpdateOptionsBulk(map[string]string{legacyAccessTokenRetireAtKey: "1"}), errLegacyRetireAtReadOnly)
	require.NoError(t, DB.Where(&Option{Key: legacyAccessTokenRetireAtKey}).First(&stored).Error)
	assert.Equal(t, strconv.FormatInt(want, 10), stored.Value)

	require.NoError(t, updateOptionMap(legacyAccessTokenRetireAtKey, stored.Value))
	assert.NotContains(t, common.OptionMap, legacyAccessTokenRetireAtKey)
}

func TestValidateAccessTokenStopsAtLegacyDeadline(t *testing.T) {
	setupUserUpdateTestState(t)
	previous := legacyAccessTokenRetireAt.Load()
	t.Cleanup(func() { legacyAccessTokenRetireAt.Store(previous) })

	user := createAccessTokenTestUser(t, "legacy-deadline-user")
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Update("access_token", "legacy-deadline-token").Error)

	legacyAccessTokenRetireAt.Store(time.Now().Add(time.Hour).Unix())
	found, err := ValidateAccessToken("legacy-deadline-token")
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, user.Id, found.Id)

	var userQueries int
	const callbackName = "test:count_legacy_token_queries"
	require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			userQueries++
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Query().Remove(callbackName) })

	for _, retireAt := range []int64{time.Now().Add(-time.Second).Unix(), 0} {
		legacyAccessTokenRetireAt.Store(retireAt)
		found, err = ValidateAccessToken("legacy-deadline-token")
		require.ErrorIs(t, err, ErrLegacyAccessTokenRetired)
		assert.Nil(t, found)
	}
	assert.Zero(t, userQueries)
}

func TestUserAccessTokenLimitAndOwnership(t *testing.T) {
	setupUserUpdateTestState(t)
	owner := createAccessTokenTestUser(t, "token-owner")
	other := createAccessTokenTestUser(t, "token-other")

	var first UserAccessToken
	for i := range 20 {
		token := createTestUserAccessToken(t, owner.Id, "nap_limit_"+strconv.Itoa(i), 0)
		if i == 0 {
			first = token
		}
	}
	extra := UserAccessToken{TokenHash: AccessTokenFingerprint("nap_limit_extra")}
	require.ErrorIs(t, CreateUserAccessToken(owner.Id, &extra, 20), ErrAccessTokenLimit)
	var count int64
	require.NoError(t, DB.Model(&UserAccessToken{}).Where("user_id = ?", owner.Id).Count(&count).Error)
	assert.EqualValues(t, 20, count)

	_, err := UpdateUserAccessToken(other.Id, first.Id, "stolen", []string{"profile:read"})
	require.ErrorIs(t, err, ErrAccessTokenNotFound)
	_, err = DeleteUserAccessToken(other.Id, first.Id)
	require.ErrorIs(t, err, ErrAccessTokenNotFound)
	stored, err := GetUserAccessToken(owner.Id, first.Id)
	require.NoError(t, err)
	assert.Equal(t, first.Name, stored.Name)

	assert.Equal(t, first.Scopes, stored.Scopes)

	renamed, err := UpdateUserAccessToken(owner.Id, first.Id, "renamed", nil)
	require.NoError(t, err)
	assert.Equal(t, "renamed", renamed.Name)
	assert.Equal(t, first.Scopes, renamed.Scopes, "a rename keeps the grant")
	regranted, err := UpdateUserAccessToken(owner.Id, first.Id, "regranted", []string{"usage:read"})
	require.NoError(t, err)
	assert.Equal(t, "regranted", regranted.Name)
	assert.Equal(t, []string{"usage:read"}, regranted.GetScopes())
	deleted, err := DeleteUserAccessToken(owner.Id, first.Id)
	require.NoError(t, err)
	assert.Equal(t, first.TokenHash, deleted.TokenHash)
	found, err := FindUserAccessTokenByHash(first.TokenHash)
	require.NoError(t, err)
	assert.Nil(t, found)
}

func TestDisablingUserKeepsAccessTokensAndDeletingRemovesThem(t *testing.T) {
	setupUserUpdateTestState(t)
	user := createAccessTokenTestUser(t, "token-lifecycle-user")
	createTestUserAccessToken(t, user.Id, "nap_lifecycle_one", 0)
	createTestUserAccessToken(t, user.Id, "nap_lifecycle_two", time.Now().Add(time.Hour).Unix())
	countTokens := func() int64 {
		var count int64
		require.NoError(t, DB.Model(&UserAccessToken{}).Where("user_id = ?", user.Id).Count(&count).Error)
		return count
	}

	user.Status = common.UserStatusDisabled
	require.NoError(t, user.Update(false))
	assert.EqualValues(t, 2, countTokens())

	revoked, err := user.Delete()
	require.NoError(t, err)
	assert.EqualValues(t, 2, revoked)
	assert.Zero(t, countTokens())

	hardDeleted := createAccessTokenTestUser(t, "token-hard-delete-user")
	createTestUserAccessToken(t, hardDeleted.Id, "nap_hard_delete", 0)
	revoked, err = hardDeleted.HardDelete()
	require.NoError(t, err)
	assert.EqualValues(t, 1, revoked)
	var remaining int64
	require.NoError(t, DB.Model(&UserAccessToken{}).Where("user_id = ?", hardDeleted.Id).Count(&remaining).Error)
	assert.Zero(t, remaining)
}

func TestValidateAuthSessionWithTxChecksAccessTokenIdentity(t *testing.T) {
	setupUserUpdateTestState(t)
	now := time.Now()

	tests := []struct {
		name    string
		prepare func(user *User, token *UserAccessToken, identity *AuthSessionIdentity)
		wantErr bool
	}{
		{name: "active token", prepare: func(*User, *UserAccessToken, *AuthSessionIdentity) {}},
		{name: "expired token", wantErr: true, prepare: func(_ *User, token *UserAccessToken, _ *AuthSessionIdentity) {
			require.NoError(t, DB.Model(token).Update("expires_at", now.Add(-time.Second).Unix()).Error)
		}},
		{name: "deleted token", wantErr: true, prepare: func(user *User, token *UserAccessToken, _ *AuthSessionIdentity) {
			_, err := DeleteUserAccessToken(user.Id, token.Id)
			require.NoError(t, err)
		}},
		{name: "another user's token", wantErr: true, prepare: func(_ *User, _ *UserAccessToken, identity *AuthSessionIdentity) {
			other := createAccessTokenTestUser(t, "token-identity-other")
			identity.UserID = other.Id
		}},
		{name: "stale auth version", wantErr: true, prepare: func(user *User, _ *UserAccessToken, _ *AuthSessionIdentity) {
			require.NoError(t, DB.Model(user).Update("auth_version", 2).Error)
		}},
		{name: "disabled user", wantErr: true, prepare: func(user *User, _ *UserAccessToken, _ *AuthSessionIdentity) {
			require.NoError(t, DB.Model(user).Update("status", common.UserStatusDisabled).Error)
		}},
		{name: "unexpected session version", wantErr: true, prepare: func(_ *User, _ *UserAccessToken, identity *AuthSessionIdentity) {
			identity.SessionVersion = 2
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, DB.Exec("DELETE FROM user_access_tokens").Error)
			require.NoError(t, DB.Exec("DELETE FROM users").Error)
			user := createAccessTokenTestUser(t, "token-identity-user")
			token := createTestUserAccessToken(t, user.Id, "nap_identity_token", now.Add(time.Hour).Unix())
			identity := AuthSessionIdentity{
				UserID: user.Id, SessionID: AccessTokenSessionID(token.Id),
				UserAuthVersion: 1, SessionVersion: AccessTokenSessionVersion,
			}
			tt.prepare(&user, &token, &identity)

			txErr := DB.Transaction(func(tx *gorm.DB) error { return ValidateAuthSessionWithTx(tx, identity) })
			plainErr := ValidateAccessTokenIdentity(identity)
			if !tt.wantErr {
				assert.NoError(t, txErr)
				assert.NoError(t, plainErr)
				return
			}
			assert.ErrorIs(t, txErr, ErrUserSessionInactive)
			assert.ErrorIs(t, plainErr, ErrUserSessionInactive)
		})
	}
}

// applyConcurrencyEdit mirrors the admin UpdateUser admission path for the
// user concurrency limit: request JSON decoded into model.User, then persisted
// through the EditWithTx map whitelist (USR-02, threat T-02-02).
func applyConcurrencyEdit(t *testing.T, userID int, limit int) {
	t.Helper()
	edit, err := GetUserById(userID, false)
	require.NoError(t, err)
	payload, err := common.Marshal(map[string]any{"concurrency_limit": limit})
	require.NoError(t, err)
	require.NoError(t, common.Unmarshal(payload, edit))
	require.NoError(t, edit.EditWithTx(DB, false))
}

func TestUserUpdatePersistsConcurrencyLimitThroughEditWithTx(t *testing.T) {
	setupUserUpdateTestState(t)

	user := User{
		Id:          41,
		Username:    "concurrency-limit-user",
		Password:    "password",
		DisplayName: "before",
		Group:       "default",
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
	}
	require.NoError(t, DB.Create(&user).Error)

	applyConcurrencyEdit(t, user.Id, 5)

	var stored int
	require.NoError(t, DB.Raw("SELECT concurrency_limit FROM users WHERE id = ?", user.Id).Scan(&stored).Error)
	assert.Equal(t, 5, stored, "EditWithTx must persist concurrency_limit=5")
}

func TestUserUpdateClearsConcurrencyLimitToZeroThroughEditWithTx(t *testing.T) {
	setupUserUpdateTestState(t)

	user := User{
		Id:          42,
		Username:    "concurrency-clear-user",
		Password:    "password",
		DisplayName: "before",
		Group:       "default",
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
	}
	require.NoError(t, DB.Create(&user).Error)

	applyConcurrencyEdit(t, user.Id, 5)
	applyConcurrencyEdit(t, user.Id, 0)

	var stored int
	require.NoError(t, DB.Raw("SELECT concurrency_limit FROM users WHERE id = ?", user.Id).Scan(&stored).Error)
	assert.Equal(t, 0, stored, "clearing to 0 (unlimited) must persist, not keep 5")
}

// TestUserUpdateEditWithTxIgnoresStructFieldsOutsideWhitelist pins that only
// the explicit updates-map keys are writable through EditWithTx: privileged
// struct fields (role/status/quota) set on the incoming object never reach the
// database (T-02-03 — admin map whitelist is the sole write channel).
func TestUserUpdateEditWithTxIgnoresStructFieldsOutsideWhitelist(t *testing.T) {
	setupUserUpdateTestState(t)

	user := User{
		Id:          43,
		Username:    "whitelist-pin-user",
		Password:    "password",
		Group:       "default",
		Status:      common.UserStatusEnabled,
		Role:        common.RoleCommonUser,
		Quota:       100,
		AuthVersion: 1,
	}
	require.NoError(t, DB.Create(&user).Error)

	edit, err := GetUserById(user.Id, false)
	require.NoError(t, err)
	edit.Role = common.RoleRootUser
	edit.Status = common.UserStatusDisabled
	edit.Quota = 999999
	require.NoError(t, edit.EditWithTx(DB, false))

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Equal(t, common.RoleCommonUser, stored.Role, "role must not be writable outside the map whitelist")
	assert.Equal(t, common.UserStatusEnabled, stored.Status, "status must not be writable outside the map whitelist")
	assert.Equal(t, 100, stored.Quota, "quota must not be writable outside the map whitelist")
}

// TestUserUpdateWriteContextPublishesConcurrencyLimit pins the request-context
// publication of the user limit (USR-03): both auth chains (TokenAuth and
// UserAuth/playground) read it through UserBase.WriteContext under the bare
// key the concurrency middleware consumes.
func TestUserUpdateWriteContextPublishesConcurrencyLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	base := &UserBase{Id: 44, Group: "default", Username: "ctx-user", Status: 1, Role: 1, ConcurrencyLimit: 9}
	base.WriteContext(c)

	got, ok := common.GetContextKey(c, "user_concurrency_limit")
	require.True(t, ok, "WriteContext must publish the user limit under the middleware bare key")
	assert.Equal(t, 9, got)

	// The named constant must resolve to the same bare key, so 02-04's typed
	// reads and the middleware's literal key cannot drift apart.
	assert.Equal(t, "user_concurrency_limit", string(constant.ContextKeyUserConcurrencyLimit))
	named, ok := common.GetContextKey(c, constant.ContextKeyUserConcurrencyLimit)
	require.True(t, ok)
	assert.Equal(t, 9, named)
}

func TestUserUpdateMigrateUserConcurrencyLimitAddsColumnOnUpgrade(t *testing.T) {
	// USR-02 three-database matrix, SQLite leg (file database): fresh and
	// upgrade startups each run twice; the second startup must be zero DDL.
	dir, err := os.MkdirTemp("", "newapi-0203-matrix")
	require.NoError(t, err)
	matrixDB, err := gorm.Open(sqlite.Open(filepath.Join(dir, "matrix.db")), &gorm.Config{})
	require.NoError(t, err)
	matrixSQL, err := matrixDB.DB()
	require.NoError(t, err)

	original := DB
	DB = matrixDB
	t.Cleanup(func() {
		DB = original
		_ = matrixSQL.Close()
		_ = os.RemoveAll(dir)
	})
	testUserConcurrencyLimitMigrationMatrix(t, common.DatabaseTypeSQLite, "SELECT sqlite_version()")
}

func TestUserUpdateConcurrencyLimitMigrationMatrixMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	matrixDB, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	matrixSQL, err := matrixDB.DB()
	require.NoError(t, err)
	original := DB
	DB = matrixDB
	t.Cleanup(func() {
		DB = original
		_ = matrixSQL.Close()
	})
	testUserConcurrencyLimitMigrationMatrix(t, common.DatabaseTypeMySQL, "SELECT version()")
}

func TestUserUpdateConcurrencyLimitMigrationMatrixPostgreSQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not configured")
	}
	matrixDB, err := gorm.Open(postgres.New(postgres.Config{
		DSN:                  dsn,
		PreferSimpleProtocol: true,
	}), &gorm.Config{})
	require.NoError(t, err)
	matrixSQL, err := matrixDB.DB()
	require.NoError(t, err)
	original := DB
	DB = matrixDB
	t.Cleanup(func() {
		DB = original
		_ = matrixSQL.Close()
	})
	testUserConcurrencyLimitMigrationMatrix(t, common.DatabaseTypePostgreSQL, "SELECT version()")
}

// testUserConcurrencyLimitMigrationMatrix exercises the USR-02 migration
// contract on a real database handle: fresh startups and an upgrade from a
// release-equivalent users table (current schema minus concurrency_limit,
// with pre-existing rows), each started twice. The second startup must issue
// zero DDL, keep existing user rows intact, and leave concurrency_limit
// defaulted to 0 on rows created before the column existed.
func testUserConcurrencyLimitMigrationMatrix(t *testing.T, dialect common.DatabaseType, versionQuery string) {
	t.Helper()

	original := DB
	originalMain, originalLog := common.MainDatabaseType(), common.LogDatabaseType()
	recorder := &migrationSQLRecorder{}
	DB = DB.Session(&gorm.Session{Logger: recorder})
	common.SetDatabaseTypes(dialect, dialect)
	t.Cleanup(func() {
		DB = original
		common.SetDatabaseTypes(originalMain, originalLog)
	})

	var version string
	require.NoError(t, DB.Raw(versionQuery).Scan(&version).Error)
	t.Logf("database: %s %s", dialect, version)

	// Idempotency is asserted for THIS migration (statements touching
	// concurrency_limit). Other startup DDL is logged rather than failed: on
	// PostgreSQL a pre-existing AutoMigrate quirk re-issues an access_token
	// TYPE statement on every startup independently of this column (observed
	// in the fresh path, where migrateUserConcurrencyLimit runs zero DDL) —
	// recorded as a deferred finding in the phase summary, not fixed here.
	splitDDL := func(statements []string) (migration, other []string) {
		for _, statement := range statements {
			if strings.Contains(strings.ToLower(statement), "concurrency_limit") {
				migration = append(migration, statement)
			} else {
				other = append(other, statement)
			}
		}
		return migration, other
	}

	if DB.Migrator().HasTable(&User{}) {
		require.NoError(t, DB.Migrator().DropTable(&User{}))
	}

	// Fresh database: the migration is a no-op (no users table yet) and
	// AutoMigrate builds the column; second startup must be zero DDL for this
	// migration's column.
	for pass := range 2 {
		recorder.reset()
		require.NoError(t, migrateUserConcurrencyLimit())
		require.NoError(t, DB.AutoMigrate(&User{}))
		if pass == 1 {
			migrationDDL, otherDDL := splitDDL(recorder.schemaMutations())
			assert.Empty(t, migrationDDL, "fresh second startup must not re-issue migration DDL")
			if len(otherDDL) > 0 {
				t.Logf("fresh second startup DDL outside this migration: %v", otherDDL)
			}
		}
	}
	assertUserConcurrencyLimitColumn(t)

	// Upgrade: current schema minus the new column, with pre-existing rows —
	// the representative shape of a database created before this migration.
	require.NoError(t, DB.Migrator().DropTable(&User{}))
	require.NoError(t, DB.AutoMigrate(&User{}))
	require.NoError(t, DB.Create(&User{
		Id: 71, Username: "upgrade-user", Password: "hash", DisplayName: "legacy",
		Group: "default", Quota: 1234, UsedQuota: 17, Status: common.UserStatusEnabled,
		AuthVersion: 1, ConcurrencyLimit: 5,
	}).Error)
	require.NoError(t, DB.Exec("ALTER TABLE users DROP COLUMN concurrency_limit").Error)

	for pass := range 2 {
		recorder.reset()
		require.NoError(t, migrateUserConcurrencyLimit())
		require.NoError(t, DB.AutoMigrate(&User{}))
		migrationDDL, otherDDL := splitDDL(recorder.schemaMutations())
		if pass == 0 {
			t.Logf("upgrade startup DDL: %v", recorder.schemaMutations())
			require.NotEmpty(t, migrationDDL, "upgrade startup must add the missing column")
		}
		if len(otherDDL) > 0 {
			t.Logf("upgrade startup DDL outside this migration: %v", otherDDL)
		}
		if pass == 1 {
			assert.Empty(t, migrationDDL, "upgrade second startup must not re-issue migration DDL")
		}
	}
	assertUserConcurrencyLimitColumn(t)

	var upgraded User
	require.NoError(t, DB.First(&upgraded, 71).Error)
	assert.Equal(t, "upgrade-user", upgraded.Username, "existing user rows must survive the migration")
	assert.Equal(t, 1234, upgraded.Quota, "existing wallet data must survive the migration")
	assert.Equal(t, 17, upgraded.UsedQuota, "existing usage data must survive the migration")
	assert.Equal(t, 0, upgraded.ConcurrencyLimit, "pre-existing rows must default to 0 (unlimited)")
}

func assertUserConcurrencyLimitColumn(t *testing.T) {
	t.Helper()
	columnTypes, err := DB.Migrator().ColumnTypes(&User{})
	require.NoError(t, err)
	for _, columnType := range columnTypes {
		if !strings.EqualFold(columnType.Name(), "concurrency_limit") {
			continue
		}
		assert.Contains(t, strings.ToLower(columnType.DatabaseTypeName()), "int",
			"concurrency_limit must be an integer column")
		defaultValue, ok := columnType.DefaultValue()
		require.True(t, ok, "concurrency_limit must report a column default")
		assert.Equal(t, "0", defaultValue, "concurrency_limit must default to 0 (unlimited)")
		return
	}
	require.Fail(t, "users.concurrency_limit column is missing")
}
