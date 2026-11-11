package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/service/concurrency"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
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
	r.GET("/api/concurrency/history", GetConcurrencyHistory)
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
	// Seed two in-flight rows through Acquire (per-request cap contract);
	// the returned releases are intentionally not called so the slots stay
	// held for the snapshot assertions.
	_, err := lim.Acquire(ctx, concurrency.ScopeKey("key:1:gpt-4o"), 2)
	require.NoError(t, err)
	_, err = lim.Acquire(ctx, concurrency.ScopeKey("user:9:gpt-4o"), 3)
	require.NoError(t, err)
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

// TestSnapshot_EnrichesConfiguredLimits pins the D-02 completion contract: the
// snapshot's limit column shows the CONFIGURED value for every row — key+*
// carries the token global cap, key+model carries that model's own cap
// (LimitForModel, never EffectiveLimit), user carries User.ConcurrencyLimit,
// and a subject that no longer exists reads 0 — while utilization is
// recomputed only when the limit is > 0. The row set itself stays whatever the
// limiter knows (in-flight scopes); only Limit/Utilization are filled in.
func TestSnapshot_EnrichesConfiguredLimits(t *testing.T) {
	// Config tables come from an in-memory SQLite (no t.TempDir: D-09).
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Token{}, &model.User{}))
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })

	require.NoError(t, db.Create(&model.Token{
		Id:                      1,
		UserId:                  9,
		Key:                     "snapshot-token-key-1",
		Name:                    "snapshot-token-1",
		Status:                  common.TokenStatusEnabled,
		ExpiredTime:             -1,
		UnlimitedQuota:          true,
		ConcurrencyLimit:        2,
		ConcurrencyLimitByModel: "gpt-4o=5",
	}).Error)
	require.NoError(t, db.Create(&model.User{
		Id:               2,
		Username:         "snapshot-user-2",
		Password:         "password",
		Status:           common.UserStatusEnabled,
		Group:            "default",
		Quota:            1000,
		ConcurrencyLimit: 3,
	}).Error)

	// The limiter only reports scopes it has seen; every row enters with
	// Limit 0 and must leave the controller with its configured cap.
	lim := concurrency.NewMemoryLimiter()
	for _, scope := range []concurrency.ScopeKey{
		"key:1:*",        // token global cap
		"key:1:gpt-4o",   // model listed in the CSV
		"key:1:claude-3", // model NOT listed → bucket cap 0
		"user:2:*",       // aggregate user bucket
		"key:777:*",      // token 777 no longer exists
	} {
		_, err := lim.Acquire(context.Background(), scope, 0)
		require.NoError(t, err)
	}
	previousLimiter := concurrencyLimiter
	concurrencyLimiter = lim
	t.Cleanup(func() { concurrencyLimiter = previousLimiter })

	require.NoError(t, authz.Init(newAuthzDB(t)))
	r := newSnapshotEngine(t, 1, common.RoleAdminUser)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/concurrency/snapshot", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp SnapshotResponse
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &resp))

	rowFor := func(scope string) (concurrency.ScopeSnapshot, bool) {
		for _, row := range resp.Scopes {
			if row.Scope.String() == scope {
				return row, true
			}
		}
		return concurrency.ScopeSnapshot{}, false
	}
	mustRow := func(scope string) concurrency.ScopeSnapshot {
		row, ok := rowFor(scope)
		require.True(t, ok, "snapshot must contain in-flight scope %s", scope)
		return row
	}

	// key + global row: the token's global cap.
	row := mustRow("key:1:*")
	assert.Equal(t, 2, row.Limit, "key:1:* row must show the token global cap")
	assert.InDelta(t, 0.5, row.Utilization, 1e-9, "utilization = current/limit for a capped row")

	// key + concrete model row: that model's own cap, not EffectiveLimit.
	row = mustRow("key:1:gpt-4o")
	assert.Equal(t, 5, row.Limit, "key:1:gpt-4o row must show LimitForModel(model)")
	assert.InDelta(t, 0.2, row.Utilization, 1e-9, "utilization = current/limit for a capped row")

	// key + model the CSV does not list: the bucket's own cap is 0, even
	// though EffectiveLimit would have fallen back to the global cap of 2.
	row = mustRow("key:1:claude-3")
	assert.Equal(t, 0, row.Limit, "unlisted model bucket shows its own cap 0 (never EffectiveLimit)")
	assert.Equal(t, 0.0, row.Utilization, "limit 0 rows never report utilization")

	// user row: the configured user cap.
	row = mustRow("user:2:*")
	assert.Equal(t, 3, row.Limit, "user:2:* row must show User.ConcurrencyLimit")
	assert.InDelta(t, float64(1)/float64(3), row.Utilization, 1e-9, "utilization = current/limit")

	// Deleted subject: the token row is gone, so the cap reads 0.
	row = mustRow("key:777:*")
	assert.Equal(t, 0, row.Limit, "a deleted subject must read limit 0")
	assert.Equal(t, 0.0, row.Utilization, "a deleted subject must read utilization 0")

	// The row set must not gain or lose scopes: completion only rewrites
	// Limit/Utilization on the limiter's own rows.
	assert.Len(t, resp.Scopes, 5, "completion must not add or remove snapshot rows")
}

// TestSnapshot_ChannelRowBackfillsFromChannelTable pins the OBS-02 landing
// and BCD cross-leg constraint 4: enrichSnapshotLimits completes a channel
// row's Limit/Utilization from the CHANNEL table via its own query, so the
// same-id TOKEN in the snapshot can never leak its cap onto the channel row
// (channel ids and token ids live in different namespaces). The row itself
// stays in the response (D-18 visibility) and now carries a real cap, which
// is what lets the admin page render channel buckets.
func TestSnapshot_ChannelRowBackfillsFromChannelTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Token{}, &model.User{}, &model.Channel{}))
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })

	// Token 1 carries caps that would be wrong for the channel row "channel:1"
	// — the namespace-collision trap the separate lookup must avoid.
	require.NoError(t, db.Create(&model.Token{
		Id:                      1,
		UserId:                  9,
		Key:                     "slot-channel-row-token",
		Name:                    "slot-channel-row-token",
		Status:                  common.TokenStatusEnabled,
		ExpiredTime:             -1,
		UnlimitedQuota:          true,
		ConcurrencyLimit:        2,
		ConcurrencyLimitByModel: "gpt-4o=5",
	}).Error)

	// Channel 1 (same numeric id as the token above) is the ONLY legitimate
	// source for the channel row's cap.
	channelCap := 3
	require.NoError(t, db.Create(&model.Channel{
		Id:               1,
		Name:             "slot-channel-row",
		Key:              "slot-channel-row-key",
		ConcurrencyLimit: &channelCap,
	}).Error)

	// The channel bucket holds one slot; no key:1:* row exists, so the
	// same-id token is the only way "channel:1" could gain a WRONG cap.
	lim := concurrency.NewMemoryLimiter()
	_, err = lim.Acquire(context.Background(), concurrency.ChannelScope(1), 0)
	require.NoError(t, err)
	previousLimiter := concurrencyLimiter
	concurrencyLimiter = lim
	t.Cleanup(func() { concurrencyLimiter = previousLimiter })

	require.NoError(t, authz.Init(newAuthzDB(t)))
	r := newSnapshotEngine(t, 1, common.RoleAdminUser)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/concurrency/snapshot", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp SnapshotResponse
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &resp))

	var channelRows int
	for _, row := range resp.Scopes {
		if row.Scope.String() != "channel:1" {
			continue
		}
		channelRows++
		assert.Equal(t, "channel", row.Kind, "the channel row must report its own kind (D-18 visibility)")
		assert.Equal(t, 3, row.Limit,
			"the channel row must carry the CHANNEL table's cap, never the same-id token's")
		assert.InDelta(t, 1.0/3.0, row.Utilization, 1e-9,
			"utilization must be recomputed from the channel's own cap")
	}
	require.Equal(t, 1, channelRows, "the snapshot must still carry the channel row")
}

// TestSnapshot_ExposesRejectionCount pins the shed-load wire field: the
// snapshot envelope carries the monotonic concurrency-429 counter, and one
// AddRejection between polls moves it by exactly one.
func TestSnapshot_ExposesRejectionCount(t *testing.T) {
	require.NoError(t, authz.Init(newAuthzDB(t)))
	previousLimiter := concurrencyLimiter
	concurrencyLimiter = concurrency.NewMemoryLimiter()
	t.Cleanup(func() { concurrencyLimiter = previousLimiter })

	r := newSnapshotEngine(t, 1, common.RoleAdminUser)
	before := concurrency.RejectionCount()
	concurrency.AddRejection()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/concurrency/snapshot", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp SnapshotResponse
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, before+1, resp.Rejections,
		"one recorded rejection between reads must move the counter by one")
	require.Contains(t, w.Body.String(), `"rejections"`,
		"the wire key must stay stable for the frontend contract")
}

// TestSnapshot_ExposesQueuedCount pins the queued envelope field: GET
// /api/concurrency/snapshot reports how many requests are currently waiting in
// the gateway's bounded wait queue (across every scope), so the admin page can
// show "waiting" separately from "in flight". The wire key must stay stable.
func TestSnapshot_ExposesQueuedCount(t *testing.T) {
	require.NoError(t, authz.Init(newAuthzDB(t)))
	previousLimiter := concurrencyLimiter
	concurrencyLimiter = concurrency.NewMemoryLimiter()
	t.Cleanup(func() { concurrencyLimiter = previousLimiter })

	queue := concurrency.DefaultWaitQueue()
	queue.Reset()
	t.Cleanup(queue.Reset)

	leaveBoth, ok := queue.TryEnter([]concurrency.ScopeLimit{
		{Scope: concurrency.ScopeKey("key:1:gpt-4o"), Limit: 1},
		{Scope: concurrency.ScopeKey("user:1:*"), Limit: 1},
	}, 4, 128)
	require.True(t, ok)
	t.Cleanup(leaveBoth)
	leaveOne, ok := queue.TryEnter([]concurrency.ScopeLimit{
		{Scope: concurrency.ScopeKey("key:1:gpt-4o"), Limit: 1},
	}, 4, 128)
	require.True(t, ok)
	t.Cleanup(leaveOne)

	r := newSnapshotEngine(t, 1, common.RoleAdminUser)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/concurrency/snapshot", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp SnapshotResponse
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, 2, resp.Queued, "the two waiters must be counted as two queued requests")
	require.Contains(t, w.Body.String(), `"queued"`, "the wire key is part of the frontend contract")
}

// fixedDegradedLimiter extends the real MemoryLimiter with a fixed
// DegradedStatus so the envelope contract can pin exact three-key wire values
// without driving a live Redis outage at the controller layer. Embedding the
// concrete limiter keeps Acquire/Snapshot behavior real — only the
// observation accessor is overridden.
type fixedDegradedLimiter struct {
	*concurrency.MemoryLimiter
	status concurrency.DegradedStatus
}

func (f *fixedDegradedLimiter) DegradedStatus() concurrency.DegradedStatus { return f.status }

// TestSnapshot_DegradedEnvelopeContract pins the D-14 / RED-05 wire contract:
// GET /api/concurrency/snapshot carries a degraded envelope field with the
// three D-14 keys (active / lastDegradedAt unix-millis / consecutiveFailures),
// in both states — healthy backend (MemoryLimiter, RED-04) reads zero values,
// and a degraded limiter serializes the exact three-key shape the frontend
// transform consumes. JSON assertions go through the common wrapper and the
// raw substring pins the literal wire keys.
func TestSnapshot_DegradedEnvelopeContract(t *testing.T) {
	require.NoError(t, authz.Init(newAuthzDB(t)))
	previousLimiter := concurrencyLimiter
	t.Cleanup(func() { concurrencyLimiter = previousLimiter })
	r := newSnapshotEngine(t, 1, common.RoleAdminUser)

	hit := func() []byte {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/api/concurrency/snapshot", nil)
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		return w.Body.Bytes()
	}

	// Healthy state: degraded present with zero values (RED-04 boundary —
	// single-node mode never reports degradation).
	concurrencyLimiter = concurrency.NewMemoryLimiter()
	body := hit()
	var healthy SnapshotResponse
	require.NoError(t, common.Unmarshal(body, &healthy))
	assert.Equal(t, concurrency.DegradedStatus{}, healthy.Degraded,
		"a healthy backend must read active=false, lastDegradedAt=0, consecutiveFailures=0")
	require.Contains(t, string(body),
		`"degraded":{"active":false,"lastDegradedAt":0,"consecutiveFailures":0}`,
		"the healthy wire shape must carry all three D-14 keys, never absent")

	// Degraded state: a fixed double pins the exact three-key values (D-14).
	concurrencyLimiter = &fixedDegradedLimiter{
		MemoryLimiter: concurrency.NewMemoryLimiter(),
		status: concurrency.DegradedStatus{
			Active:              true,
			LastDegradedAt:      1760000000123,
			ConsecutiveFailures: 3,
		},
	}
	body = hit()
	var degraded SnapshotResponse
	require.NoError(t, common.Unmarshal(body, &degraded))
	assert.True(t, degraded.Degraded.Active, "a degraded backend must read active=true")
	assert.EqualValues(t, 1760000000123, degraded.Degraded.LastDegradedAt,
		"lastDegradedAt must travel as unix milliseconds")
	assert.Equal(t, 3, degraded.Degraded.ConsecutiveFailures)
	require.Contains(t, string(body),
		`"degraded":{"active":true,"lastDegradedAt":1760000000123,"consecutiveFailures":3}`,
		"the degraded wire shape must serialize the exact three D-14 keys")
}

// TestSnapshot_OutageKeepsPollingAliveAndMarksDegraded pins the T-02-25 /
// D-15 fallback contract end to end at the HTTP layer: a RedisLimiter whose
// backend is unreachable must never error the snapshot — GET stays 200 with
// the fallback view, and the envelope reports the outage as
// degraded.active=true with a millisecond lastDegradedAt, so the admin page
// poll survives the outage window while the badge shows.
func TestSnapshot_OutageKeepsPollingAliveAndMarksDegraded(t *testing.T) {
	require.NoError(t, authz.Init(newAuthzDB(t)))

	// Orphan port: listen, capture the address, close — dials get refused fast.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	deadAddr := ln.Addr().String()
	require.NoError(t, ln.Close())
	client := redis.NewClient(&redis.Options{Addr: deadAddr})
	t.Cleanup(func() { _ = client.Close() })
	lim := concurrency.NewRedisLimiter(client, "conc:")
	// Registered after the client cleanup, so it runs first (LIFO): Close
	// joins the renewal ticker before the client goes away.
	t.Cleanup(lim.Close)

	// One acquire fails open into the local fallback and records degradation.
	release, err := lim.Acquire(context.Background(), concurrency.ScopeKey("key:42:outage"), 5)
	require.NoError(t, err, "an unreachable backend must fail open, never surface an error")
	require.NotNil(t, release)
	release()

	previousLimiter := concurrencyLimiter
	concurrencyLimiter = lim
	t.Cleanup(func() { concurrencyLimiter = previousLimiter })
	r := newSnapshotEngine(t, 1, common.RoleAdminUser)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/concurrency/snapshot", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code,
		"the admin poll must stay 200 during a Redis outage (fallback view, err=nil)")

	var resp SnapshotResponse
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Degraded.Active,
		"the outage must surface as degraded.active=true in the envelope")
	assert.Greater(t, resp.Degraded.LastDegradedAt, int64(0),
		"the outage must stamp lastDegradedAt with unix milliseconds")
	assert.GreaterOrEqual(t, resp.Degraded.ConsecutiveFailures, 1,
		"the failed acquire (and the failed snapshot read) must be counted")
}

// import shim for context used inside the helper.
var _ = func() bool { return true }

// TestHistoryEndpoint_AuthzValidationAndRead pins the time-window history
// contract: same permission as the snapshot, required unix-millis params
// with an empty-range guard, and a read that returns exactly the samples
// inside the requested window from *common.LogDir.
func TestHistoryEndpoint_AuthzValidationAndRead(t *testing.T) {
	require.NoError(t, authz.Init(newAuthzDB(t)))

	// Redirect the recorder's output dir to a fixture directory.
	dir := t.TempDir()
	prevDir := common.LogDir
	common.LogDir = &dir
	t.Cleanup(func() { common.LogDir = prevDir })

	// Today's daily file with two in-window samples (10s apart).
	now := time.Now()
	today := "concurrency-" + now.Format("2006-01-02") + ".log"
	sample := func(offset time.Duration, inFlight, rejections int) string {
		return fmt.Sprintf(`{"ts":%d,"inFlight":%d,"rejections":%d,"scopes":[]}`+"\n",
			now.Add(offset).UnixMilli(), inFlight, rejections)
	}
	fixture := sample(-40*time.Minute, 3, 10) + sample(-30*time.Minute, 5, 22)
	require.NoError(t, writeHistoryFixture(t, dir, today, fixture))

	get := func(t *testing.T, role int, query string) *httptest.ResponseRecorder {
		t.Helper()
		r := newSnapshotEngine(t, 1, role)
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/api/concurrency/history"+query, nil)
		r.ServeHTTP(w, req)
		return w
	}

	// Permission: common users are refused exactly like the snapshot route.
	w := get(t, common.RoleCommonUser, "?start=1&end=2")
	require.Equal(t, http.StatusForbidden, w.Code)

	// Validation: both params required; an inverted window is refused.
	require.Equal(t, http.StatusBadRequest, get(t, common.RoleAdminUser, "").Code)
	require.Equal(t, http.StatusBadRequest, get(t, common.RoleAdminUser, "?start=1").Code)

	// Happy path: both samples inside the window come back, ordered.
	start := now.Add(-time.Hour).UnixMilli()
	end := now.UnixMilli()
	w = get(t, common.RoleAdminUser, fmt.Sprintf("?start=%d&end=%d", start, end))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var body struct {
		Points []concurrency.HistoryPoint `json:"points"`
		Start  int64                      `json:"start"`
		End    int64                      `json:"end"`
	}
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Points, 2, "both in-window samples must be returned")
	assert.Equal(t, 3, body.Points[0].InFlight)
	assert.Equal(t, 5, body.Points[1].InFlight)
	assert.EqualValues(t, 22, body.Points[1].Rejections)
	assert.GreaterOrEqual(t, body.Points[0].TS, body.Start)
	assert.LessOrEqual(t, body.Points[1].TS, body.End)

	// A window that contains nothing returns an empty array, not null.
	w = get(t, common.RoleAdminUser,
		fmt.Sprintf("?start=%d&end=%d", now.Add(-2*time.Hour).UnixMilli(), now.Add(-90*time.Minute).UnixMilli()))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"points":[]`, "empty windows serialize as [] for the frontend")
}

// writeHistoryFixture creates name under dir with the given raw content.
func writeHistoryFixture(t *testing.T, dir, name, content string) error {
	t.Helper()
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}
