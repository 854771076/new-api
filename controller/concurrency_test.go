package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/service/concurrency"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newAuthzDB mirrors the authz package's newAuthzTestDB but lives here so the
// controller tests don't have to drop into a different test package.
func newAuthzDB(t *testing.T) *gorm.DB {
	t.Helper()
	wasMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() {
		common.IsMasterNode = wasMaster
	})
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.CasbinRule{}, &model.AuthzRole{}))
	return db
}

// helper: build a gin engine with auth context keys and the snapshot route.
func newSnapshotEngine(t *testing.T, userID, role int) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// Inject identity keys the way middleware.AdminAuth + RequirePermission would
	r.Use(func(c *gin.Context) {
		c.Set("id", userID)
		c.Set("role", role)
		c.Next()
	})
	r.GET("/api/concurrency/snapshot", GetConcurrencySnapshot)
	return r
}

func TestSnapshot_Authz_ForbiddenForCommonUser(t *testing.T) {
	// Install a limiter so the 403 path doesn't depend on a nil limiter.
	lim := concurrency.NewMemoryLimiter()
	SetConcurrencyLimiter(lim)
	r := newSnapshotEngine(t, 5, common.RoleCommonUser)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/concurrency/snapshot", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestSnapshot_ReturnsScopesInStableOrder(t *testing.T) {
	lim := concurrency.NewMemoryLimiter()
	ctx := context.Background()
	require.NoError(t, lim.SetLimit(ctx, concurrency.ScopeKey("key:1:gpt-4o"), 2))
	require.NoError(t, lim.SetLimit(ctx, concurrency.ScopeKey("user:9:gpt-4o"), 3))
	SetConcurrencyLimiter(lim)

	// Run in authz scope where admin role resolves to seeded policies.
	db := newAuthzDB(t)
	require.NoError(t, authz.Init(db))
	r := newSnapshotEngine(t, 1, common.RoleAdminUser)

	hit := func() []byte {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/api/concurrency/snapshot", nil)
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		return w.Body.Bytes()
	}

	body1 := hit()
	body2 := hit()

	var r1, r2 SnapshotResponse
	require.NoError(t, json.Unmarshal(body1, &r1))
	require.NoError(t, json.Unmarshal(body2, &r2))

	require.Equal(t, len(r1.Scopes), len(r2.Scopes))
	for i := range r1.Scopes {
		assert.Equal(t, r1.Scopes[i].Scope, r2.Scopes[i].Scope, "scope order at index %d", i)
	}
	assert.NotEmpty(t, r1.GeneratedAt)
}

// import shim for context used inside the helper.
var _ = func() bool { return true }
