package controller

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/service/concurrency"
	"github.com/gin-gonic/gin"
)

// concurrencyLimiterGetter is the bridge from the global limiter singleton to
// the HTTP handlers. We resolve the limiter at request time (rather than
// caching the snapshot in a package init) so the snapshot always reflects the
// current holder set — limits are computed per request (Acquire(ctx, scope,
// limit)), never pushed into the limiter, so there is no threshold state to
// cache here.
var (
	concurrencyLimiterOnce sync.Once
	concurrencyLimiter     concurrency.Limiter
)

// SetConcurrencyLimiter installs the limiter used by the controller layer.
// Mirrors middleware.SetConcurrencyLimiter but targets the gin-side readers
// only (snapshot / recent_rejects).
func SetConcurrencyLimiter(l concurrency.Limiter) {
	concurrencyLimiterOnce.Do(func() { concurrencyLimiter = l })
}

func getConcurrencyLimiter() concurrency.Limiter {
	return concurrencyLimiter
}

// requireConcurrencyRead enforces the ConcurrencyRead permission on a
// concurrency read endpoint. On denial it writes the shared 403
// permission_denied envelope and returns false so the handler can return
// early. This is a controller-level gate, independent of any router middleware.
func requireConcurrencyRead(c *gin.Context, userID, role int) bool {
	if authz.Can(userID, role, authz.ConcurrencyRead) {
		return true
	}
	c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
		"message": "missing concurrency:read permission",
		"type":    "permission_denied",
	}})
	return false
}

// GetChannelRoutingHealth returns the live per-(channel,model) EWMA routing
// health (RTE-01) as an ops/test diagnostics view: samples, success/latency
// EWMAs, the current effective-weight multiplier and the last sample time.
// This deliberately amends the 02-17 in-code note ("never wired to an HTTP
// handler"): the load-balance acceptance run
// (concurrency-test/LOADBALANCE-PLAN.md) must observe the algorithm's
// parameters and decisions on a live gateway, so the snapshot — whose doc
// already reserves it for "ops diagnostics" — is exposed read-only behind the
// same AdminAuth + ConcurrencyRead gate as the concurrency snapshot. It reads
// process-memory state only and mutates nothing.
func GetChannelRoutingHealth(c *gin.Context) {
	userID := c.GetInt("id")
	role := c.GetInt("role")
	if !requireConcurrencyRead(c, userID, role) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": model.SnapshotChannelRoutingHealth()})
}

// SnapshotResponse is the JSON envelope returned by GET /api/concurrency/snapshot.
type SnapshotResponse struct {
	Scopes      []concurrency.ScopeSnapshot `json:"scopes"`
	GeneratedAt int64                       `json:"generatedAt"`
	// Rejections is the monotonic count of client-visible concurrency 429s
	// since process start (both enforcement surfaces increment it). The admin
	// page derives windowed deltas from consecutive polls, so the series
	// needs no reset and a restart simply restarts it.
	Rejections int64 `json:"rejections"`
	// Degraded carries the D-14/RED-05 observation state of the concurrency
	// backend (always present; zero values when healthy or when Redis is not
	// in use), so the admin page renders the degraded badge without relying
	// on logs.
	Degraded concurrency.DegradedStatus `json:"degraded"`
	// Queued is the number of requests currently waiting in the gateway's
	// bounded wait queue (per-node state, summed over every scope).
	Queued int `json:"queued"`
}

// GetConcurrencySnapshot returns live in-flight counts for every known scope.
// Requires admin role and ConcurrencyRead permission.
func GetConcurrencySnapshot(c *gin.Context) {
	userID := c.GetInt("id")
	role := c.GetInt("role")
	if !requireConcurrencyRead(c, userID, role) {
		return
	}
	l := getConcurrencyLimiter()
	if l == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"message": "concurrency limiter not initialised",
			"type":    "concurrency_unavailable",
		}})
		return
	}
	snap, err := l.Snapshot(context.Background())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"message": err.Error(),
			"type":    "internal_error",
		}})
		return
	}
	enrichSnapshotLimits(snap)
	c.JSON(http.StatusOK, SnapshotResponse{
		Scopes:      snap,
		GeneratedAt: nowUnixMilli(),
		Rejections:  concurrency.RejectionCount(),
		Degraded:    l.DegradedStatus(), // D-14 observation, read-only
		Queued:      concurrency.DefaultWaitQueue().Total(),
	})
}

// GetConcurrencyHistory serves GET /api/concurrency/history?start=&end=
// (unix millis): recorded load samples for any window inside the 7-day
// retention period, clamped server-side (a future end trims to now, an
// older start trims to the retention floor) and downsampled by the reader.
// Same permission as the snapshot; the response's points carry only
// chartable fields (t / inFlight / rejections).
func GetConcurrencyHistory(c *gin.Context) {
	userID := c.GetInt("id")
	role := c.GetInt("role")
	if !requireConcurrencyRead(c, userID, role) {
		return
	}
	startMs, err := strconv.ParseInt(c.Query("start"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "start (unix millis) is required",
			"type":    "invalid_parameter",
		}})
		return
	}
	endMs, err := strconv.ParseInt(c.Query("end"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "end (unix millis) is required",
			"type":    "invalid_parameter",
		}})
		return
	}

	now := time.Now()
	end := time.UnixMilli(endMs)
	if end.After(now) {
		end = now
	}
	start := time.UnixMilli(startMs)
	if oldest := now.AddDate(0, 0, -concurrency.HistoryRetentionDays); start.Before(oldest) {
		start = oldest
	}
	if !start.Before(end) {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "empty time range after clamping to the retention window",
			"type":    "invalid_parameter",
		}})
		return
	}

	result := concurrency.ReadHistory(*common.LogDir, start, end)
	points := result.Points
	if points == nil {
		points = []concurrency.HistoryPoint{} // stable JSON shape for the frontend
	}
	// Peak rows for the bucket list view of the SAME window; the shared
	// enrichment fills configured caps so the list matches live semantics.
	scopeStats := result.ScopeStats
	if scopeStats == nil {
		scopeStats = []concurrency.ScopeSnapshot{}
	} else {
		enrichSnapshotLimits(scopeStats)
	}
	c.JSON(http.StatusOK, gin.H{
		"points":     points,
		"scopeStats": scopeStats,
		"start":      start.UnixMilli(),
		"end":        end.UnixMilli(),
	})
}

// enrichSnapshotLimits completes the snapshot rows with their CONFIGURED caps
// and recomputes utilization from them (D-02). The limiter deliberately stores
// no limits (ARCHITECTURE Decision A: caps are computed per request and never
// pushed into it), so a snapshot straight out of Snapshot() reports 0 for every
// row — useless to the admin page, which must show the configured value:
//
//   - kind=key, model="*"        -> token global cap (ParseTokenConcurrencyLimits().Global)
//   - kind=key, model=<model>    -> that model's own cap (LimitForModel), NEVER
//     EffectiveLimit: an unlisted model bucket is capped at 0 even when the
//     token has a global cap, which is exactly what the bucket enforces
//   - kind=user                  -> User.ConcurrencyLimit (the aggregate cap)
//   - kind=channel               -> Channel.ConcurrencyLimit, read from the
//     CHANNEL table in its own query (BCD cross-leg constraint 4): channel ids
//     and token ids live in different namespaces, so the channel row must
//     never be backfilled through tokensByID — the separate lookup makes an
//     id collision harmless (OBS-02 landing: the admin page now renders
//     channel rows, so their caps must be real)
//   - subject no longer exists   -> 0
//
// Only Limit and Utilization are rewritten: the row set stays whatever the
// limiter has in flight, and ScopeSnapshot's JSON shape is untouched, so the
// frontend contract does not move.
func enrichSnapshotLimits(snap []concurrency.ScopeSnapshot) {
	if len(snap) == 0 || model.DB == nil {
		return
	}

	keyIDs := make([]int, 0, len(snap))
	userIDs := make([]int, 0, len(snap))
	channelIDs := make([]int, 0, len(snap))
	for _, row := range snap {
		if row.SubjectID <= 0 {
			continue
		}
		if row.Kind == "channel" {
			if !slices.Contains(channelIDs, row.SubjectID) {
				channelIDs = append(channelIDs, row.SubjectID)
			}
			continue
		}
		if row.Kind == "user" {
			if !slices.Contains(userIDs, row.SubjectID) {
				userIDs = append(userIDs, row.SubjectID)
			}
		} else if !slices.Contains(keyIDs, row.SubjectID) {
			keyIDs = append(keyIDs, row.SubjectID)
		}
	}

	tokensByID := make(map[int]model.Token, len(keyIDs))
	if len(keyIDs) > 0 {
		var tokens []model.Token
		if err := model.DB.Model(&model.Token{}).
			Select("id, concurrency_limit, concurrency_limit_by_model").
			Where("id IN ?", keyIDs).
			Find(&tokens).Error; err != nil {
			common.SysLog("concurrency snapshot: loading token caps failed: " + err.Error())
		}
		for _, token := range tokens {
			tokensByID[token.Id] = token
		}
	}

	usersByID := make(map[int]model.User, len(userIDs))
	if len(userIDs) > 0 {
		var users []model.User
		if err := model.DB.Model(&model.User{}).
			Select("id, concurrency_limit").
			Where("id IN ?", userIDs).
			Find(&users).Error; err != nil {
			common.SysLog("concurrency snapshot: loading user caps failed: " + err.Error())
		}
		for _, user := range users {
			usersByID[user.Id] = user
		}
	}

	channelsByID := make(map[int]model.Channel, len(channelIDs))
	if len(channelIDs) > 0 {
		var channels []model.Channel
		if err := model.DB.Model(&model.Channel{}).
			Select("id, concurrency_limit").
			Where("id IN ?", channelIDs).
			Find(&channels).Error; err != nil {
			common.SysLog("concurrency snapshot: loading channel caps failed: " + err.Error())
		}
		for _, channel := range channels {
			channelsByID[channel.Id] = channel
		}
	}

	for i := range snap {
		row := &snap[i]
		limit := 0
		if row.Kind == "channel" {
			// Channel ids resolve against the CHANNEL table only — the
			// same-id token in tokensByID above must never leak its cap here.
			// ConcurrencyLimit is the C-2 pointer field: nil means unset (=0).
			if channel, ok := channelsByID[row.SubjectID]; ok && channel.ConcurrencyLimit != nil {
				limit = *channel.ConcurrencyLimit
			}
		} else if row.Kind == "user" {
			if user, ok := usersByID[row.SubjectID]; ok {
				limit = user.ConcurrencyLimit
			}
		} else if token, ok := tokensByID[row.SubjectID]; ok {
			limits := concurrency.ParseTokenConcurrencyLimits(token.ConcurrencyLimit, token.ConcurrencyLimitByModel)
			if row.Model == "*" {
				limit = limits.Global
			} else {
				limit = limits.LimitForModel(row.Model)
			}
		}
		row.Limit = limit
		// Same formula as service/concurrency/limiter.go scopeRow: a row with
		// no cap is unlimited, so it never reports utilization.
		if limit > 0 {
			row.Utilization = float64(row.Current) / float64(limit)
		} else {
			row.Utilization = 0
		}
	}
}

// nowUnixMilli is overridable by tests.
var nowUnixMilli = func() int64 {
	return realUnixMilli()
}

// realUnixMilli is the production binding.
func realUnixMilli() int64 {
	return timeNowUnixMilli()
}
