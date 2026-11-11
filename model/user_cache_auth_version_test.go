package model

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Sharded Redis hashes only the text inside the first brace pair, so fence
// and version keys tagged with the untagged cache key share its slot.
// Miniredis does not enforce slots, so check the tags explicitly.
func TestAuthCacheKeysShareClusterHashTag(t *testing.T) {
	for _, tc := range []struct {
		cacheKey string
		related  []string
	}{
		{getUserCacheKey(5), []string{getUserAuthFenceKey(5), getUserAuthVersionKey(5)}},
		{getTokenCacheKey("test-token"), []string{getTokenCacheFenceKey("test-token")}},
	} {
		assert.NotContains(t, tc.cacheKey, "{")
		for _, key := range tc.related {
			_, suffix, ok := strings.Cut(key, "{")
			require.True(t, ok)
			tag, _, ok := strings.Cut(suffix, "}")
			require.True(t, ok)
			assert.Equal(t, tc.cacheKey, tag)
		}
	}
}

func useUserCacheMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	server := miniredis.RunT(t)
	oldRedisEnabled := common.RedisEnabled
	oldRDB := common.RDB
	oldSyncFrequency := common.SyncFrequency
	common.RedisEnabled = true
	common.SyncFrequency = 2
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		_ = common.RDB.Close()
		common.RedisEnabled = oldRedisEnabled
		common.RDB = oldRDB
		common.SyncFrequency = oldSyncFrequency
	})
	return server
}

func TestUserAuthFenceRollbackExpiresAndRecovers(t *testing.T) {
	truncateTables(t)
	server := useUserCacheMiniRedis(t)

	user := User{
		Username:    "auth-fence-rollback",
		Password:    "password",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
	}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, populateUserCache(user))

	tx := DB.Begin()
	require.NoError(t, tx.Error)
	next, err := IncrementUserAuthVersionWithTx(tx, user.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 2, next)

	_, err = cacheGetUserBase(user.Id)
	assert.ErrorIs(t, err, ErrUserAuthCachePending)
	cacheTTL, err := common.RDB.TTL(t.Context(), getUserCacheKey(user.Id)).Result()
	require.NoError(t, err)
	fenceTTL, err := common.RDB.TTL(t.Context(), getUserAuthFenceKey(user.Id)).Result()
	require.NoError(t, err)
	assert.Greater(t, fenceTTL, cacheTTL)
	require.NoError(t, tx.Rollback().Error)

	server.FastForward(time.Duration(userAuthFenceTTLSeconds()+1) * time.Second)
	assert.False(t, server.Exists(getUserAuthFenceKey(user.Id)))
	committed, err := common.RDB.Get(t.Context(), getUserAuthVersionKey(user.Id)).Result()
	require.NoError(t, err)
	assert.Equal(t, "1", committed)

	cached, err := GetUserCache(user.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 1, cached.AuthVersion)
}

func TestPendingUserAuthFenceRejectsStaleCacheWrite(t *testing.T) {
	server := useUserCacheMiniRedis(t)
	const userID = 4201
	require.NoError(t, SetUserAuthVersionFence(userID, 2))

	err := writeUserCache(&UserBase{
		Id: userID, Group: "default", Username: "stale", AuthVersion: 1,
	}, true)

	assert.ErrorIs(t, err, ErrUserAuthCachePending)
	assert.False(t, server.Exists(getUserCacheKey(userID)))
}

func TestUserAuthCacheUpdateRejectsVersionMismatch(t *testing.T) {
	useUserCacheMiniRedis(t)
	const userID = 4202
	require.NoError(t, writeUserCache(&UserBase{
		Id: userID, Group: "current", Username: "cached", AuthVersion: 3,
	}, true))

	err := writeUserCache(&UserBase{Id: userID, Group: "stale", AuthVersion: 2}, false)

	assert.ErrorIs(t, err, ErrUserAuthCachePending)
	group, err := common.RDB.HGet(t.Context(), getUserCacheKey(userID), "Group").Result()
	require.NoError(t, err)
	assert.Equal(t, "current", group)
}

func TestRefreshUserGroupCacheRepairsDelayedSameVersionWrite(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)

	user := User{
		Username:    "delayed-group-refresh",
		Password:    "password",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
	}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, populateUserCache(user))

	firstSnapshotRead := make(chan struct{})
	releaseDelayedRefresh := make(chan struct{})
	var intercepted atomic.Bool
	const callbackName = "test:block_delayed_group_refresh"
	require.NoError(t, DB.Callback().Query().After("gorm:query").Register(callbackName, func(*gorm.DB) {
		if intercepted.CompareAndSwap(false, true) {
			close(firstSnapshotRead)
			<-releaseDelayedRefresh
		}
	}))
	t.Cleanup(func() {
		_ = DB.Callback().Query().Remove(callbackName)
	})

	delayedResult := make(chan error, 1)
	go func() {
		delayedResult <- RefreshUserGroupCache(user.Id)
	}()
	<-firstSnapshotRead

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Update("group", "pro").Error)
	require.NoError(t, RefreshUserGroupCache(user.Id))
	cached, err := cacheGetUserBase(user.Id)
	require.NoError(t, err)
	assert.Equal(t, "pro", cached.Group)
	assert.EqualValues(t, 1, cached.AuthVersion)

	close(releaseDelayedRefresh)
	require.NoError(t, <-delayedResult)
	cached, err = cacheGetUserBase(user.Id)
	require.NoError(t, err)
	assert.Equal(t, "pro", cached.Group)
	assert.EqualValues(t, 1, cached.AuthVersion)
}

func TestCommittedUserAuthVersionPermanentlyRejectsDelayedCacheFill(t *testing.T) {
	truncateTables(t)
	server := useUserCacheMiniRedis(t)

	user := User{
		Username:    "auth-fence-commit",
		Password:    "password",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
	}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, populateUserCache(user))
	stale := *user.ToBaseUser()

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, err := IncrementUserAuthVersionWithTx(tx, user.Id)
		return err
	}))
	require.NoError(t, PublishUserAuthCache(user.Id))
	assert.False(t, server.Exists(getUserAuthFenceKey(user.Id)))
	committed, err := common.RDB.Get(t.Context(), getUserAuthVersionKey(user.Id)).Result()
	require.NoError(t, err)
	assert.Equal(t, "2", committed)

	server.FastForward(time.Duration(userAuthFenceTTLSeconds()+1) * time.Second)
	require.NoError(t, common.RedisDelKey(getUserCacheKey(user.Id)))
	err = writeUserCache(&stale, true)
	assert.True(t, errors.Is(err, ErrUserAuthCachePending))
	committed, err = common.RDB.Get(t.Context(), getUserAuthVersionKey(user.Id)).Result()
	require.NoError(t, err)
	assert.Equal(t, "2", committed)
}

func TestUserAuthVersionFenceAndCommittedFloorAreMonotonic(t *testing.T) {
	truncateTables(t)
	server := useUserCacheMiniRedis(t)

	const userID = 4101
	require.NoError(t, SetUserAuthVersionFence(userID, 5))
	require.NoError(t, SetUserAuthVersionFence(userID, 3))
	pending, err := common.RDB.Get(t.Context(), getUserAuthFenceKey(userID)).Result()
	require.NoError(t, err)
	assert.Equal(t, "5", pending)
	floor, err := getUserAuthVersionFloor(userID)
	require.NoError(t, err)
	assert.EqualValues(t, 5, floor)

	// Committing an older transaction must neither clear a newer pending fence
	// nor lower the effective deny floor.
	require.NoError(t, publishCommittedUserAuthVersion(userID, 3))
	pending, err = common.RDB.Get(t.Context(), getUserAuthFenceKey(userID)).Result()
	require.NoError(t, err)
	assert.Equal(t, "5", pending)
	floor, err = getUserAuthVersionFloor(userID)
	require.NoError(t, err)
	assert.EqualValues(t, 5, floor)

	require.NoError(t, publishCommittedUserAuthVersion(userID, 5))
	assert.False(t, server.Exists(getUserAuthFenceKey(userID)))
	committed, err := common.RDB.Get(t.Context(), getUserAuthVersionKey(userID)).Result()
	require.NoError(t, err)
	assert.Equal(t, "5", committed)

	require.NoError(t, publishCommittedUserAuthVersion(userID, 4))
	committed, err = common.RDB.Get(t.Context(), getUserAuthVersionKey(userID)).Result()
	require.NoError(t, err)
	assert.Equal(t, "5", committed)
}

// requireLegacyUserCacheHash writes the pre-schema-3 user hash shape (no
// ConcurrencyLimit field, CacheSchema=2) that a deployment upgrading from the
// previous release leaves behind in Redis.
func requireLegacyUserCacheHash(t *testing.T, userID int, username string) {
	t.Helper()
	require.NoError(t, common.RDB.HSet(t.Context(), getUserCacheKey(userID), map[string]any{
		"Id": userID, "Group": "default", "Email": "", "Status": 1, "Role": 1,
		"Username": username, "Setting": "", "AuthVersion": 1, "CacheSchema": 2,
	}).Err())
}

func TestUserCacheConcurrencyLimitRoundTrip(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)

	user := User{
		Username:         "user-cache-concurrency-user",
		Password:         "password",
		Role:             common.RoleCommonUser,
		Status:           common.UserStatusEnabled,
		Group:            "default",
		AuthVersion:      1,
		ConcurrencyLimit: 7,
	}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, populateUserCache(user))

	raw, err := common.RDB.HGet(t.Context(), getUserCacheKey(user.Id), "ConcurrencyLimit").Result()
	require.NoError(t, err, "writeUserCache must HSET the ConcurrencyLimit field")
	assert.Equal(t, "7", raw)

	cached, err := cacheGetUserBase(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 7, cached.ConcurrencyLimit, "UserBase must round-trip ConcurrencyLimit")
	assert.Equal(t, userCacheSchemaVersion, cached.CacheSchema)
}

func TestUserCacheRejectsHashWithPreviousSchemaVersion(t *testing.T) {
	useUserCacheMiniRedis(t)
	const userID = 4301
	requireLegacyUserCacheHash(t, userID, "legacy-schema-user")

	cached, err := cacheGetUserBase(userID)
	require.ErrorContains(t, err, "user cache schema is stale")
	assert.Nil(t, cached)
}

func TestUserCacheStaleHashReprimesFromDatabase(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)

	user := User{
		Username:         "stale-hash-user",
		Password:         "password",
		Role:             common.RoleCommonUser,
		Status:           common.UserStatusEnabled,
		Group:            "default",
		AuthVersion:      1,
		ConcurrencyLimit: 7,
	}
	require.NoError(t, DB.Create(&user).Error)
	requireLegacyUserCacheHash(t, user.Id, "stale-hash-user")

	cached, err := GetUserCache(user.Id)
	require.NoError(t, err)
	require.NotNil(t, cached)

	raw, err := common.RDB.HGet(t.Context(), getUserCacheKey(user.Id), "ConcurrencyLimit").Result()
	require.NoError(t, err, "rejected legacy hash must be re-primed from the database")
	assert.Equal(t, "7", raw)
	assert.Equal(t, 7, cached.ConcurrencyLimit, "DB fallback must serve the database limit")
	assert.Equal(t, userCacheSchemaVersion, cached.CacheSchema)
}

// TestUserCacheAdminSaveRewritesLimitIntoCache pins the USR-03 save-to-cache
// chain: EditWithTx persists the new limit, PublishUserAuthCache (the call
// UpdateUser makes after a successful save) rewrites the hash in full, and the
// next cache hit serves the new value — never the previously cached limit.
func TestUserCacheAdminSaveRewritesLimitIntoCache(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)

	user := User{
		Username:    "save-rewrite-user",
		Password:    "password",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
	}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, populateUserCache(user))

	before, err := cacheGetUserBase(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 0, before.ConcurrencyLimit, "cache starts with the unlimited default")

	applyConcurrencyEdit(t, user.Id, 7)
	require.NoError(t, PublishUserAuthCache(user.Id))

	after, err := cacheGetUserBase(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 7, after.ConcurrencyLimit, "cache hit must serve the newly saved limit")
	assert.Equal(t, userCacheSchemaVersion, after.CacheSchema)
}

// TestUserCachePartialUpdateDoesNotLaunderStaleSchema pins the CR-01 review
// finding: a single-field cache refresh that hits a legacy (schema-2) hash
// must NOT stamp the current CacheSchema onto it. Stamping would carry the
// hash past the read gate while ConcurrencyLimit is still absent, decoding
// the missing field as 0=unlimited (fail-open on the user axis, T-02-01).
// On schema mismatch the partial write is skipped; the hash stays stale, the
// gate keeps rejecting it, and the read path re-primes it from the database
// (quota-safe: populateUserCache never overwrites a live Quota field).
func TestUserCachePartialUpdateDoesNotLaunderStaleSchema(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)

	user := User{
		Username:         "partial-stale-user",
		Password:         "password",
		Role:             common.RoleCommonUser,
		Status:           common.UserStatusEnabled,
		Group:            "default",
		AuthVersion:      1,
		ConcurrencyLimit: 7,
	}
	require.NoError(t, DB.Create(&user).Error)
	requireLegacyUserCacheHash(t, user.Id, "partial-stale-user")

	// A single-field refresh (subscription group / setting / name path) hits
	// the legacy hash with a matching auth version — the laundering window.
	require.NoError(t, updateUserCacheFieldAtVersion(user.Id, "Setting", `{"k":"v"}`, 1))

	// The partial write must not have carried the legacy hash past the gate.
	_, err := cacheGetUserBase(user.Id)
	require.ErrorContains(t, err, "user cache schema is stale",
		"a schema-mismatched partial update must not stamp CacheSchema onto a legacy hash")

	// The full read path still heals: DB fallback serves the real limit.
	cached, err := GetUserCache(user.Id)
	require.NoError(t, err)
	require.NotNil(t, cached)
	assert.Equal(t, 7, cached.ConcurrencyLimit, "DB fallback must serve the database limit")
	assert.Equal(t, userCacheSchemaVersion, cached.CacheSchema)

	// On a current-schema hash the partial update still applies (no regression).
	require.NoError(t, updateUserCacheFieldAtVersion(user.Id, "Setting", `{"k":"v2"}`, 1))
	after, err := cacheGetUserBase(user.Id)
	require.NoError(t, err)
	assert.Equal(t, `{"k":"v2"}`, after.Setting, "partial updates must still write on healthy hashes")
}
