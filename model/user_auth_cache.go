package model

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// User auth cache fencing uses three Redis keys per user: the cached user
// hash, a short-lived pending fence published before a restrictive database
// transaction, and a monotonic committed version floor published after
// commit. Cache writes below either floor are rejected, readers below the
// effective floor fall back to the database, and the pending fence outlives
// every user-hash TTL so a rolled-back transaction heals without allowing a
// stale snapshot to re-authorize the user.

var ErrUserAuthCachePending = errors.New("user authentication state update is pending")

var ErrUserAuthVersionConflict = errors.New("user authentication version update conflicted")

// Fence and version keys use the user cache key as their hash tag so the
// cache fill script and the floor MGET stay in one slot on sharded Redis.
func getUserAuthFenceKey(userId int) string {
	return fmt.Sprintf("auth:user:fence:{%s}", getUserCacheKey(userId))
}

func getUserAuthVersionKey(userId int) string {
	return fmt.Sprintf("auth:user:version:{%s}", getUserCacheKey(userId))
}

// A pending fence only covers the interval between publishing the next
// version and the surrounding database transaction reaching a decision. Its
// TTL must outlive every user hash that could have been populated before the
// fence, while still allowing an automatically rolled-back transaction to
// recover without an operator repairing Redis.
func userAuthFenceTTLSeconds() int {
	cacheTTL := userCacheTTLSeconds()
	extra := max(cacheTTL, 60)
	return cacheTTL + extra
}

func writeUserCache(user *UserBase, includeQuota bool) error {
	if user == nil || user.Id <= 0 || !common.RedisEnabled {
		return nil
	}
	user.CacheSchema = userCacheSchemaVersion
	if user.AuthVersion <= 0 {
		return fmt.Errorf("invalid user auth version")
	}
	includeQuotaArg := "0"
	if includeQuota {
		includeQuotaArg = "1"
	}
	ttl := userCacheTTLSeconds()
	user.CacheExpiresAt = time.Now().Add(time.Duration(ttl) * time.Second).UnixMilli()
	mac, err := user.cacheMAC()
	if err != nil {
		return err
	}
	const script = `
local incoming = tonumber(ARGV[1])
local pending = tonumber(redis.call('GET', KEYS[2]) or '0')
local committed = tonumber(redis.call('GET', KEYS[3]) or '0')
local current = tonumber(redis.call('HGET', KEYS[1], 'AuthVersion') or '0')
if pending > incoming or committed > incoming or current > incoming then
  return 0
end
if committed < incoming then
  redis.call('SET', KEYS[3], ARGV[1])
end
if pending > 0 and pending <= incoming then
  redis.call('DEL', KEYS[2])
end
if ARGV[10] == '0' and redis.call('EXISTS', KEYS[1]) == 0 then
  return 1
end
redis.call('HSET', KEYS[1],
  'Id', ARGV[2], 'Group', ARGV[3], 'Email', ARGV[4],
  'Status', ARGV[5], 'Role', ARGV[6], 'Username', ARGV[7],
  'Setting', ARGV[8], 'AuthVersion', ARGV[1], 'CacheSchema', ARGV[9],
  'ConcurrencyLimit', ARGV[13], 'CacheExpiresAt', ARGV[14], 'CacheMAC', ARGV[15])
if ARGV[10] == '1' and not string.match(redis.call('HGET', KEYS[1], 'Quota') or '', '^-?%d+$') then
  redis.call('HSET', KEYS[1], 'Quota', ARGV[11])
end
redis.call('EXPIRE', KEYS[1], ARGV[12])
return 1`
	// ARGV layout: [1]=AuthVersion, [2..9]=identity fields + CacheSchema,
	// [10]=includeQuota flag, [11]=Quota, [12]=TTL — all pinned in place;
	// [13]=ConcurrencyLimit (USR-03 full rewrite on admin save, so a cache hit
	// never serves a stale limit), [14]=CacheExpiresAt, [15]=CacheMAC.
	result, err := common.RDB.Eval(context.Background(), script,
		[]string{getUserCacheKey(user.Id), getUserAuthFenceKey(user.Id), getUserAuthVersionKey(user.Id)},
		user.AuthVersion, user.Id, user.Group, user.Email, user.Status, user.Role,
		user.Username, user.Setting, user.CacheSchema, includeQuotaArg, user.Quota, ttl,
		user.ConcurrencyLimit, user.CacheExpiresAt, mac,
	).Int()
	if err != nil {
		return err
	}
	if result == 0 {
		return ErrUserAuthCachePending
	}
	return nil
}

func getUserAuthVersionFloor(userId int) (int64, error) {
	if !common.RedisEnabled {
		return 0, nil
	}
	values, err := common.RDB.MGet(context.Background(), getUserAuthFenceKey(userId), getUserAuthVersionKey(userId)).Result()
	if err != nil {
		return 0, err
	}
	var floor int64
	for _, value := range values {
		if value == nil {
			continue
		}
		parsed, err := strconv.ParseInt(fmt.Sprint(value), 10, 64)
		if err != nil {
			return 0, err
		}
		if parsed > floor {
			floor = parsed
		}
	}
	return floor, nil
}

// SetUserAuthVersionFence publishes a fail-closed version before a restrictive
// database update. Pending fences expire only after every pre-existing user
// hash must have expired; a committed update is promoted separately to a
// permanent monotonic version floor.
func SetUserAuthVersionFence(userId int, authVersion int64) error {
	if !common.RedisEnabled {
		return nil
	}
	if userId <= 0 || authVersion <= 0 {
		return fmt.Errorf("invalid user auth fence")
	}
	const script = `
local current = tonumber(redis.call('GET', KEYS[1]) or '0')
local incoming = tonumber(ARGV[1])
if current < incoming then
  redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[2])
elseif current == incoming then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
elseif redis.call('TTL', KEYS[1]) < 0 then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
end
return 1`
	return common.RDB.Eval(context.Background(), script, []string{getUserAuthFenceKey(userId)}, authVersion, userAuthFenceTTLSeconds()).Err()
}

// publishCommittedUserAuthVersion records the durable lower bound used to
// reject an arbitrarily delayed cache fill after a committed security change.
// It also removes this transaction's now-obsolete pending fence.
func publishCommittedUserAuthVersion(userId int, authVersion int64) error {
	if !common.RedisEnabled {
		return nil
	}
	if userId <= 0 || authVersion <= 0 {
		return fmt.Errorf("invalid committed user auth version")
	}
	const script = `
local incoming = tonumber(ARGV[1])
local committed = tonumber(redis.call('GET', KEYS[1]) or '0')
local pending = tonumber(redis.call('GET', KEYS[2]) or '0')
if committed < incoming then
  redis.call('SET', KEYS[1], ARGV[1])
end
if pending > 0 and pending <= incoming then
  redis.call('DEL', KEYS[2])
end
return 1`
	return common.RDB.Eval(context.Background(), script,
		[]string{getUserAuthVersionKey(userId), getUserAuthFenceKey(userId)}, authVersion,
	).Err()
}

// IncrementUserAuthVersionWithTx locks the user, publishes the next deny
// fence, then persists the version in the caller's transaction. Unscoped is
// intentional so the same fail-closed path also covers hard deletion of an
// already soft-deleted user.
func IncrementUserAuthVersionWithTx(tx *gorm.DB, userId int) (int64, error) {
	if tx == nil || userId <= 0 {
		return 0, fmt.Errorf("invalid user auth version update")
	}
	for range 3 {
		var user User
		if err := lockForUpdate(tx.Unscoped()).Select("id", "auth_version").Where("id = ?", userId).First(&user).Error; err != nil {
			return 0, err
		}
		current := max(user.AuthVersion, 1)
		next := current + 1
		if err := SetUserAuthVersionFence(userId, next); err != nil {
			return 0, err
		}
		result := tx.Unscoped().Model(&User{}).
			Where("id = ? AND auth_version = ?", userId, user.AuthVersion).
			Update("auth_version", next)
		if result.Error != nil {
			return 0, result.Error
		}
		if result.RowsAffected == 1 {
			return next, nil
		}
	}
	return 0, ErrUserAuthVersionConflict
}

// BumpUserAuthVersion is the transaction-owning variant used by password,
// role, status and security-factor changes outside another transaction.
func BumpUserAuthVersion(userId int) (int64, error) {
	var next int64
	if err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		next, err = IncrementUserAuthVersionWithTx(tx, userId)
		return err
	}); err != nil {
		return 0, err
	}
	if err := PublishUserAuthCache(userId); err != nil {
		return next, err
	}
	return next, nil
}

// PublishUserAuthCache refreshes user metadata from the database. Quota stays
// under the atomic quota-delta paths.
func PublishUserAuthCache(userId int) error {
	if !common.RedisEnabled {
		return nil
	}
	if userId <= 0 {
		return fmt.Errorf("invalid user id")
	}
	authoritative, err := GetUserById(userId, false)
	if err != nil {
		return err
	}
	// Group and profile updates keep the same authentication version. Re-read
	// after publishing so a delayed refresh repairs any newer metadata it replaced.
	for range 3 {
		if err := updateUserCache(*authoritative); err != nil {
			return err
		}
		verified, err := GetUserById(userId, false)
		if err != nil {
			return err
		}
		previousBase, verifiedBase := authoritative.ToBaseUser(), verified.ToBaseUser()
		previousBase.Quota, verifiedBase.Quota = 0, 0
		if *previousBase == *verifiedBase {
			return nil
		}
		authoritative = verified
	}
	if err := updateUserCache(*authoritative); err != nil {
		return err
	}
	return fmt.Errorf("user metadata changed repeatedly during cache refresh")
}

// InitializeUserAuthVersions must run after AutoMigrate when upgrading an
// existing database. It is idempotent and portable across all supported DBs.
func InitializeUserAuthVersions() error {
	return DB.Model(&User{}).Where("auth_version IS NULL OR auth_version < ?", 1).Update("auth_version", 1).Error
}

// updateUserCacheFieldAtVersion writes a single user cache field without
// rebuilding the whole hash. The cache MAC covers every field of the hash, so
// the write must re-sign the hash it changes: the new expiry and MAC are
// computed from the snapshot the field value is applied to, and the script
// writes field, expiry and MAC together, so the hash never carries a stale
// signature.
//
// A hash whose CacheSchema predates the current field set is left untouched:
// the read gate rejects it forever, so stamping the current schema onto it
// would carry it past the gate while the new fields are still missing,
// decoding them as 0=unlimited (fail-open, CR-01). The read path re-primes
// the full field set from the database instead.
func updateUserCacheFieldAtVersion(userId int, field string, value any, authVersion int64) error {
	if !common.RedisEnabled {
		return nil
	}
	if userId <= 0 || authVersion <= 0 {
		return fmt.Errorf("invalid user auth version")
	}
	exists, err := common.RDB.Exists(context.Background(), getUserCacheKey(userId)).Result()
	if err != nil {
		return err
	}
	if exists == 0 {
		return nil
	}
	var current UserBase
	if err := common.RedisHGetObj(getUserCacheKey(userId), &current); err != nil {
		return err
	}
	if current.Id != userId || current.CacheSchema != userCacheSchemaVersion {
		// Legacy hash: skip the partial write, the read path re-primes it.
		return nil
	}
	if err := applyUserCacheField(&current, field, value); err != nil {
		return err
	}
	ttl := userCacheTTLSeconds()
	current.CacheExpiresAt = time.Now().Add(time.Duration(ttl) * time.Second).UnixMilli()
	mac, err := current.cacheMAC()
	if err != nil {
		return err
	}
	const script = `
local incoming = tonumber(ARGV[1])
local pending = tonumber(redis.call('GET', KEYS[2]) or '0')
local committed = tonumber(redis.call('GET', KEYS[3]) or '0')
local current = tonumber(redis.call('HGET', KEYS[1], 'AuthVersion') or '0')
if pending > incoming or committed > incoming or current > incoming then
  return 0
end
if committed < incoming then
  redis.call('SET', KEYS[3], ARGV[1])
end
if pending > 0 and pending <= incoming then
  redis.call('DEL', KEYS[2])
end
if redis.call('EXISTS', KEYS[1]) == 0 then
  return 1
end
if current ~= incoming then
  return 1
end
-- A schema mismatch means this hash predates the current field set (e.g. a
-- legacy hash with no ConcurrencyLimit). Stamping the current CacheSchema
-- would carry it past the read gate while the new fields are still missing,
-- decoding them as 0=unlimited (fail-open). Skip the write instead: the hash
-- stays stale, the gate keeps rejecting it, and the read path re-primes the
-- full field set from the database (CR-01).
if redis.call('HGET', KEYS[1], 'CacheSchema') ~= ARGV[4] then
  return 1
end
redis.call('HSET', KEYS[1], ARGV[2], ARGV[3], 'CacheExpiresAt', ARGV[5], 'CacheMAC', ARGV[6])
return 1`
	result, err := common.RDB.Eval(context.Background(), script,
		[]string{getUserCacheKey(userId), getUserAuthFenceKey(userId), getUserAuthVersionKey(userId)},
		authVersion, field, value, userCacheSchemaVersion, current.CacheExpiresAt, mac,
	).Int()
	if err != nil {
		return err
	}
	if result == 0 {
		return ErrUserAuthCachePending
	}
	return nil
}

// applyUserCacheField applies one cache field value to the snapshot the MAC
// is computed from. The Redis hash field names are the Go field names, so the
// caller names a UserBase field directly.
func applyUserCacheField(user *UserBase, field string, value any) error {
	target := reflect.ValueOf(user).Elem().FieldByName(field)
	if !target.IsValid() || !target.CanSet() {
		return fmt.Errorf("unknown user cache field %q", field)
	}
	text := fmt.Sprintf("%v", value)
	switch target.Kind() {
	case reflect.String:
		target.SetString(text)
	case reflect.Int, reflect.Int64:
		parsed, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return err
		}
		target.SetInt(parsed)
	case reflect.Bool:
		parsed, err := strconv.ParseBool(text)
		if err != nil {
			return err
		}
		target.SetBool(parsed)
	default:
		return fmt.Errorf("unsupported user cache field type for %q", field)
	}
	return nil
}
