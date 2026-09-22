package controller

import (
	"context"
	"net/http"
	"sync"

	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/service/concurrency"
	"github.com/gin-gonic/gin"
)

// concurrencyLimiterGetter is the bridge from the global limiter singleton to
// the HTTP handlers. We resolve the limiter at request time (rather than
// caching the snapshot in a package init) so the snapshot always reflects
// the most recent SetLimit calls — important after an admin writes a new
// threshold and immediately re-reads the snapshot.
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

// SnapshotResponse is the JSON envelope returned by GET /api/concurrency/snapshot.
type SnapshotResponse struct {
	Scopes      []concurrency.ScopeSnapshot `json:"scopes"`
	GeneratedAt int64                       `json:"generatedAt"`
}

// GetConcurrencySnapshot returns live in-flight counts for every known scope.
// Requires admin role and ConcurrencyRead permission.
func GetConcurrencySnapshot(c *gin.Context) {
	userID := c.GetInt("id")
	role := c.GetInt("role")
	if !authz.Can(userID, role, authz.ConcurrencyRead) {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
			"message": "missing concurrency:read permission",
			"type":    "permission_denied",
		}})
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
	c.JSON(http.StatusOK, SnapshotResponse{Scopes: snap, GeneratedAt: nowUnixMilli()})
}

// nowUnixMilli is overridable by tests.
var nowUnixMilli = func() int64 {
	return realUnixMilli()
}

// realUnixMilli is the production binding.
func realUnixMilli() int64 {
	return timeNowUnixMilli()
}
