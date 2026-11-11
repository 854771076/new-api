package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenAutoGroupsRoundTripThroughRedisHashCache(t *testing.T) {
	useUserCacheMiniRedis(t)
	token := Token{
		Id:         42,
		UserId:     7,
		Key:        "token-auto-groups-cache-key",
		Name:       "auto-cache",
		Group:      "auto",
		AutoGroups: `["vip","default"]`,
	}

	require.NoError(t, cacheSetTokenForTest(token))
	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, token.AutoGroups, cached.AutoGroups)
	groups, err := cached.GetAutoGroups()
	require.NoError(t, err)
	assert.Equal(t, []string{"vip", "default"}, groups)
}

func TestTokenAllowUserQuotaQueryRoundTripsThroughRedisHashCache(t *testing.T) {
	server := useUserCacheMiniRedis(t)
	token := Token{Id: 43, UserId: 7, Key: "token-user-quota-cache-key", Name: "user-quota-cache", AllowUserQuotaQuery: true}

	initialized, err := cacheInitToken(token)
	require.NoError(t, err)
	require.Equal(t, 1, initialized)
	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.True(t, cached.AllowUserQuotaQuery)
	assert.Positive(t, server.TTL(getTokenCacheKey(token.Key)))
}

func TestTokenUpdateSynchronouslyNarrowsPreheatedAutoGroupsCache(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	token := Token{
		UserId:          7,
		Key:             "token-auto-groups-update-cache-key",
		Name:            "auto-cache-update",
		Status:          common.TokenStatusEnabled,
		ExpiredTime:     -1,
		UnlimitedQuota:  true,
		Group:           "auto",
		CrossGroupRetry: true,
		AutoGroups:      `["default","vip"]`,
	}
	require.NoError(t, token.Insert())
	require.NoError(t, cacheSetTokenForTest(token))

	preheated, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.JSONEq(t, `["default","vip"]`, preheated.AutoGroups)

	require.NoError(t, token.SetAutoGroups([]string{"vip"}))
	require.NoError(t, token.Update())
	// Update 是限制性变更：写库前删除缓存并设置 fence。缓存不再提供旧的
	// 宽分组值，下一次读取必须看到收紧后的分组。
	_, cacheErr := cacheGetTokenByKey(token.Key)
	require.Error(t, cacheErr, "the pre-update cache entry must be invalidated")
	reloaded, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.JSONEq(t, `["vip"]`, reloaded.AutoGroups)
}

// TestTokenCache_ConcurrencyFieldsRoundTrip 钉住 cacheInitToken 写入的
// 哈希必须携带并发两字段与 schema 版本字段：缺字段会被 RedisHGetObj 静默
// 解码为 0=不限流（ENF-01，威胁 T-02-01）。
func TestTokenCache_ConcurrencyFieldsRoundTrip(t *testing.T) {
	useUserCacheMiniRedis(t)
	token := Token{
		Id:                      43,
		UserId:                  7,
		Key:                     "token-concurrency-roundtrip-key",
		Name:                    "concurrency-cache",
		Group:                   "auto",
		ConcurrencyLimit:        3,
		ConcurrencyLimitByModel: "gpt-4o=2,gpt-4o-mini=1",
	}

	require.NoError(t, cacheSetTokenForTest(token))
	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, 3, cached.ConcurrencyLimit)
	assert.Equal(t, "gpt-4o=2,gpt-4o-mini=1", cached.ConcurrencyLimitByModel)
	assert.Equal(t, 1, cached.CacheSchema, "round-trip must carry tokenCacheSchemaVersion=1")
}

// TestTokenCache_RejectsStaleSchemaHash 钉住 schema 完整性门：部署瞬间的
// 旧哈希（缺并发字段、无 CacheSchema）必须被 cacheGetTokenByKey 拒绝并
// 回源 DB，而不是把并发上限静默解码为 0=不限流（威胁 T-02-01）。
func TestTokenCache_RejectsStaleSchemaHash(t *testing.T) {
	useUserCacheMiniRedis(t)

	// 旧 schema 哈希：手工 HSET，缺 ConcurrencyLimit/ConcurrencyLimitByModel/
	// CacheSchema 三字段（旧 16 字段形态），CacheSchema 解码为 0。
	staleKey := "token-stale-schema-key"
	require.NoError(t, common.RDB.HSet(t.Context(), getTokenCacheKey(staleKey),
		"Id", "88", "UserId", "7", "Status", "1", "Name", "legacy-no-schema").Err())
	rejected, err := cacheGetTokenByKey(staleKey)
	require.Error(t, err)
	assert.Nil(t, rejected, "a stale-schema hash must never surface as a usable token")
	assert.Equal(t, "token cache is incomplete", err.Error())

	// 完整哈希（cacheInitToken 写入，CacheSchema=tokenCacheSchemaVersion）正常命中。
	freshKey := "token-complete-schema-key"
	token := Token{
		Id:                      89,
		UserId:                  7,
		Key:                     freshKey,
		Name:                    "fresh-schema",
		Group:                   "auto",
		ConcurrencyLimit:        3,
		ConcurrencyLimitByModel: "gpt-4o=2",
	}
	require.NoError(t, cacheSetTokenForTest(token))
	cached, err := cacheGetTokenByKey(freshKey)
	require.NoError(t, err)
	assert.Equal(t, freshKey, cached.Key, "cache hit must backfill token.Key")
	assert.Equal(t, tokenCacheSchemaVersion, cached.CacheSchema)
	assert.Equal(t, 3, cached.ConcurrencyLimit)
}

// TestTokenCache_StaleSchemaHashReprimesOnDbFallback 钉住 must-have truth
// 「回源 DB 重灌」：部署前遗留的旧 schema 哈希被读取门拒绝后，DB 回源路径的
// cacheInitToken 必须补写缺失的并发/schema 字段（重灌），使下一次读取命中；
// 重灌只补 schema 字段，不得覆盖哈希中已原子预扣的余额（live 配额领先 DB 快照）。
func TestTokenCache_StaleSchemaHashReprimesOnDbFallback(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)

	token := Token{
		UserId:                  7,
		Key:                     "token-reprime-key",
		Name:                    "reprime",
		Status:                  common.TokenStatusEnabled,
		ExpiredTime:             -1,
		ConcurrencyLimit:        5,
		ConcurrencyLimitByModel: "gpt-4o=4",
	}
	require.NoError(t, token.Insert())

	// 遗留哈希：旧 16 字段形态（无 CacheSchema），且余额已被原子预扣到 100
	// 而 DB 快照为 0——模拟部署瞬间在线运行的旧二进制写入的哈希。
	require.NoError(t, common.RDB.HSet(t.Context(), getTokenCacheKey(token.Key),
		"Id", token.Id, "UserId", token.UserId, "Status", token.Status,
		"Name", "legacy", "RemainQuota", "100").Err())

	// 首次读取：旧哈希被 schema 门拒绝，回源 DB 拿到正确并发上限。
	_, err := cacheGetTokenByKey(token.Key)
	require.Error(t, err, "the legacy hash must be rejected before re-prime")
	loaded, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	require.Equal(t, 5, loaded.ConcurrencyLimit)

	// 重灌后的下一次读取必须命中缓存并携带并发字段（否则持续流量下永久回落 DB）。
	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err, "DB fallback must re-prime the stale hash")
	assert.Equal(t, tokenCacheSchemaVersion, cached.CacheSchema)
	assert.Equal(t, 5, cached.ConcurrencyLimit)
	assert.Equal(t, "gpt-4o=4", cached.ConcurrencyLimitByModel)
	assert.Equal(t, 100, cached.RemainQuota, "re-prime must not clobber live pre-consumed quota")
}

// TestTokenUpdateServesFreshConcurrencyLimitsThroughCachePath 钉住 TUI-04
// 「保存即生效」的缓存命中路径：保存（Token.Update() 写前 fence+DEL）后旧哈希
// 立即不可命中、请求侧 GetTokenByKey 读到新上限，fence 过期重灌后缓存命中读到
// 新上限；显式清除为 0 与空串后任何路径都不返回旧上限（精确值断言）。fence+DEL
// 与 cacheInitToken 三字段是腿 0/02-02/02-06 既成机制，本用例只验证不重写。
func TestTokenUpdateServesFreshConcurrencyLimitsThroughCachePath(t *testing.T) {
	truncateTables(t)
	server := useUserCacheMiniRedis(t)
	token := Token{
		UserId:                  7,
		Key:                     "token-concurrency-fresh-cache-key",
		Name:                    "concurrency-fresh",
		Status:                  common.TokenStatusEnabled,
		ExpiredTime:             -1,
		UnlimitedQuota:          true,
		Group:                   "default",
		ConcurrencyLimit:        3,
		ConcurrencyLimitByModel: "gpt-4o=2",
	}
	require.NoError(t, token.Insert())
	require.NoError(t, cacheSetTokenForTest(token))

	t.Run("preheat_hit_serves_old_limits", func(t *testing.T) {
		preheated, err := cacheGetTokenByKey(token.Key)
		require.NoError(t, err)
		assert.Equal(t, 3, preheated.ConcurrencyLimit)
		assert.Equal(t, "gpt-4o=2", preheated.ConcurrencyLimitByModel)
	})

	// 保存新上限：UpdateToken 的持久化 seam（写库前 invalidateTokenCacheForMutation）。
	token.ConcurrencyLimit = 5
	token.ConcurrencyLimitByModel = "claude=1"
	require.NoError(t, token.Update())

	t.Run("save_deletes_pre_update_hash", func(t *testing.T) {
		_, err := cacheGetTokenByKey(token.Key)
		require.Error(t, err, "the pre-update hash must be invalidated")
	})

	t.Run("request_path_serves_new_limits", func(t *testing.T) {
		loaded, err := GetTokenByKey(token.Key, false)
		require.NoError(t, err)
		assert.Equal(t, 5, loaded.ConcurrencyLimit)
		assert.Equal(t, "claude=1", loaded.ConcurrencyLimitByModel)
	})

	t.Run("cache_hit_serves_new_limits_after_fence", func(t *testing.T) {
		server.FastForward(time.Duration(tokenCacheFenceSeconds+1) * time.Second)
		// fence 已过期：回源 DB 读新值并由 cacheInitToken 重灌哈希。
		_, err := GetTokenByKey(token.Key, false)
		require.NoError(t, err)
		cached, err := cacheGetTokenByKey(token.Key)
		require.NoError(t, err, "cache hit must serve the post-save value")
		assert.Equal(t, 5, cached.ConcurrencyLimit)
		assert.Equal(t, "claude=1", cached.ConcurrencyLimitByModel)
	})

	// 显式清除为 0 / ''（写入边界的清空语义）后任何路径不返回旧上限。
	token.ConcurrencyLimit = 0
	token.ConcurrencyLimitByModel = ""
	require.NoError(t, token.Update())

	t.Run("cleared_request_path_returns_zero_and_empty", func(t *testing.T) {
		cleared, err := GetTokenByKey(token.Key, false)
		require.NoError(t, err)
		assert.Equal(t, 0, cleared.ConcurrencyLimit)
		assert.Equal(t, "", cleared.ConcurrencyLimitByModel)
	})

	t.Run("cleared_cache_hit_returns_zero_and_empty", func(t *testing.T) {
		server.FastForward(time.Duration(tokenCacheFenceSeconds+1) * time.Second)
		_, err := GetTokenByKey(token.Key, false)
		require.NoError(t, err)
		cached, err := cacheGetTokenByKey(token.Key)
		require.NoError(t, err, "cache hit must not return the pre-clear limits")
		assert.Equal(t, 0, cached.ConcurrencyLimit)
		assert.Equal(t, "", cached.ConcurrencyLimitByModel)
	})
}

// cacheSetTokenForTest 以测试身份写入完整 token 缓存（含额度字段），
// 模拟“已水合”的缓存状态。
func cacheSetTokenForTest(token Token) error {
	_, err := cacheInitToken(token)
	return err
}
