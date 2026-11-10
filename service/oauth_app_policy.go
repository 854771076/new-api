package service

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const (
	oauthAppCacheKeyPrefix = "oauth_app:"
	oauthAppCacheTTL       = 5 * time.Minute
)

var (
	ErrOAuthAppUnknown  = errors.New("oauth app not found")
	ErrOAuthAppDisabled = errors.New("oauth app is disabled")
)

// cachedOAuthApp is the memory-cache shape for an OAuthApp. Keeping a tiny
// subset avoids serializing full redirect URIs / scope arrays every hit.
type cachedOAuthApp struct {
	id                 int
	clientID           string
	status             int
	aiModelWhitelist   string // JSON array string; "" means no restriction
	supabaseProjectRef string
	cachedAt           time.Time
}

var (
	oauthAppMemoryCacheMu sync.RWMutex
	oauthAppMemoryCache   = make(map[string]cachedOAuthApp)
)

// LoadOAuthAppByClientID returns the OAuthApp for the given client_id, using
// Redis when enabled and falling back to an in-process memory cache when it
// is not. Database errors return any fresh cached value (stale) with a warn
// log so that a transient outage never blocks AI relay traffic.
func LoadOAuthAppByClientID(clientID string) (*model.OAuthApp, error) {
	if clientID == "" {
		return nil, ErrOAuthAppUnknown
	}

	// 1. Try Redis when available.
	if common.RedisEnabled && common.RDB != nil {
		redisKey := oauthAppCacheKeyPrefix + clientID
		val, err := common.RedisGet(redisKey)
		if err == nil && val != "" {
			if app := parseCachedOAuthApp(val); app != nil {
				return app, nil
			}
		}
	}

	// 2. Try memory cache.
	if cached, ok := loadOAuthAppFromMemory(clientID); ok {
		return cached, nil
	}

	// 3. Query DB. On failure, return any stale cached copy.
	app, err := model.GetOAuthAppByClientID(clientID)
	if err != nil {
		if stale, ok := loadOAuthAppFromMemory(clientID); ok {
			common.SysLog(common.LogText("oauth_app_policy: DB lookup failed for client_id=%s, using stale cache: %v", clientID, err))
			return stale, nil
		}
		return nil, fmt.Errorf("%w: %v", ErrOAuthAppUnknown, err)
	}

	cache := cachedOAuthApp{
		id:                 app.Id,
		clientID:           app.ClientID,
		status:             app.Status,
		aiModelWhitelist:   app.AIModelWhitelist,
		supabaseProjectRef: app.SupabaseProjectRef,
		cachedAt:           time.Now(),
	}
	storeOAuthAppInMemory(cache)

	if common.RedisEnabled && common.RDB != nil {
		common.RedisSet(oauthAppCacheKeyPrefix+clientID, serializeCachedOAuthApp(cache), oauthAppCacheTTL)
	}

	return app, nil
}

// CheckOAuthAppModelWhitelist reports whether the OAuthApp identified by
// clientID is allowed to call the given origin model. When the whitelist is
// unset (empty) it always returns true — the app has no model restrictions.
//
// The policy is fail-closed: when the app record cannot be loaded the request
// is rejected (allowed=false) and the load error is returned so callers can
// distinguish a transient failure from a policy denial.
func CheckOAuthAppModelWhitelist(clientID, modelName string) (bool, error) {
	app, err := LoadOAuthAppByClientID(clientID)
	if err != nil {
		return false, err
	}
	return app.AllowsModel(modelName), nil
}

// InvalidateOAuthAppCache clears the cached OAuthApp for a specific client.
// Call this after Create/Update/Delete/SecretRotate to push fresh values.
func InvalidateOAuthAppCache(clientID string) {
	oauthAppMemoryCacheMu.Lock()
	delete(oauthAppMemoryCache, clientID)
	oauthAppMemoryCacheMu.Unlock()
	if common.RedisEnabled && common.RDB != nil {
		common.RedisDel(oauthAppCacheKeyPrefix + clientID)
	}
}

// --- memory cache helpers ---

func storeOAuthAppInMemory(c cachedOAuthApp) {
	oauthAppMemoryCacheMu.Lock()
	oauthAppMemoryCache[c.clientID] = c
	oauthAppMemoryCacheMu.Unlock()
}

func loadOAuthAppFromMemory(clientID string) (*model.OAuthApp, bool) {
	oauthAppMemoryCacheMu.RLock()
	c, ok := oauthAppMemoryCache[clientID]
	oauthAppMemoryCacheMu.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Since(c.cachedAt) > oauthAppCacheTTL {
		return nil, false
	}
	return cachedOAuthAppToModel(c), true
}

func cachedOAuthAppToModel(c cachedOAuthApp) *model.OAuthApp {
	return &model.OAuthApp{
		Id:                 c.id,
		ClientID:           c.clientID,
		Status:             c.status,
		AIModelWhitelist:   c.aiModelWhitelist,
		SupabaseProjectRef: c.supabaseProjectRef,
	}
}

// --- string serialize helpers (avoids pulling encoding/json at hot path) ---

// serializeCachedOAuthApp produces a compact single-line string; the shape is
// private to this file. Using common.Marshal keeps JSON output stable and
// consistent with the rest of the codebase.
func serializeCachedOAuthApp(c cachedOAuthApp) string {
	data, err := common.Marshal(map[string]any{
		"id":  c.id,
		"cid": c.clientID,
		"st":  c.status,
		"wl":  c.aiModelWhitelist,
		"pr":  c.supabaseProjectRef,
		"ts":  c.cachedAt.Unix(),
	})
	if err != nil {
		return ""
	}
	return string(data)
}

func parseCachedOAuthApp(raw string) *model.OAuthApp {
	var payload struct {
		ID  int    `json:"id"`
		CID string `json:"cid"`
		St  int    `json:"st"`
		WL  string `json:"wl"`
		PR  string `json:"pr"`
		TS  int64  `json:"ts"`
	}
	if err := common.UnmarshalJsonStr(raw, &payload); err != nil {
		return nil
	}
	if payload.CID == "" || payload.ID <= 0 {
		// Entries written before the app id was cached cannot be used for
		// app-scoped billing, so treat them as a cache miss and reload.
		return nil
	}
	// Stale protection (Redis side TTL should already handle it, but we double
	// check in case a migration or operator action left a lingering key).
	if time.Since(time.Unix(payload.TS, 0)) > oauthAppCacheTTL {
		return nil
	}
	return &model.OAuthApp{
		Id:                 payload.ID,
		ClientID:           payload.CID,
		Status:             payload.St,
		AIModelWhitelist:   payload.WL,
		SupabaseProjectRef: payload.PR,
	}
}
