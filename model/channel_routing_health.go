// RTE-01 (02-17): per-(channel, model) upstream health carrier that turns the
// static weighted-random selection into D-R1's dynamic effective weight:
//
//	effective weight = static weight × multiplier, recomputed periodically
//
// The state rides PROCESS MEMORY on purpose (plan decision, D-R1 follow-up):
// upstream latency and success rate are physical quantities every node
// observes independently, so per-node statistics converge unbiasedly without
// any cross-node coordination, and multi-node deployments need no new schema,
// no Redis key, and no migration — each node warms its own records
// (its own minWarmSamples) and converges on the same upstream facts. The load
// term (Task 02-17-02) reads the shared channel:<id> in-flight counters when
// Redis is enabled, so that part is cross-node for free.
//
// Lock contract (deadlock hard rule): this store owns a dedicated mutex and
// MUST NEVER take channelSyncLock while it is held (reverse order is
// forbidden). The sanctioned nesting direction is the other way: the selection
// paths already hold channelSyncLock.RLock when they briefly read a multiplier
// (channelSyncLock → health read). Recompute is serialized by its own mutex,
// snapshots the records, then reads channel facts / the load provider and
// writes multipliers in separate short lock windows — channelSyncLock and any
// provider I/O are taken WITHOUT the health write lock.
package model

import (
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// --- Anti-oscillation parameters (plan-mandated, rationale on every constant) ---

const (
	// channelEWMAWarmSamples: a record needs this many samples before its
	// multiplier can leave 1.0. Fewer samples cannot move the weight at all —
	// this is the first anti-tampering fence (T-02-17-01): a handful of
	// constructed failures cannot steer routing.
	channelEWMAWarmSamples = 10

	// Latency EWMA half-life with its admissible band. 60s reacts to a
	// sustained slowdown within minutes while single-digit-ms jitter washes
	// out; the band documents the range a future tuning may pick without
	// re-justifying the design.
	channelRoutingLatencyHalfLife    = 60 * time.Second
	channelRoutingLatencyHalfLifeMin = 10 * time.Second
	channelRoutingLatencyHalfLifeMax = 600 * time.Second

	// Success-rate EWMA half-life with its band: deliberately ~5× slower than
	// latency so a burst of failures only dents the weight, never flips it.
	channelRoutingSuccessHalfLife    = 300 * time.Second
	channelRoutingSuccessHalfLifeMin = 30 * time.Second
	channelRoutingSuccessHalfLifeMax = 3600 * time.Second

	// Final multiplier clamps. The floor keeps a degraded channel selectable
	// (never starves, bounded worst-case impact = static/5) and the ceiling
	// bounds a favoured channel (never monopolises, ≤5× static). With the
	// formula below the ceiling is structurally unreachable today
	// (successFactor ≤ 1 × latencyFactor ≤ 2 × loadFactor ≤ 1 ≤ 5.0); it is
	// pinned as a constant so a future factor can never run away unbounded.
	channelRoutingMultiplierMin = 0.2
	channelRoutingMultiplierMax = 5.0

	// A record silent for longer than this is dropped wholesale on the next
	// recompute; a later attempt re-seeds it and re-enters through the warm
	// gate — the self-heal path that bounds how long poisoned health survives
	// (T-02-17-01).
	channelRoutingStaleDrop = 5 * time.Minute

	// Success-factor blend: successFactor = 0.2 + 0.8 × successEWMA, i.e. a
	// fully failing record degrades to the 0.2 floor and a fully healthy one
	// contributes 1.0 (planner formula, D-R1 "parameters by planner").
	channelRoutingSuccessFactorFloor = 0.2

	// Latency-factor clamps: clamp(warm-mean latency / record latency, 0.5,
	// 2.0). A record twice as slow as the same-model warm mean halves its
	// weight contribution; one more than twice as fast doubles it. A single
	// warm record is its own mean → exactly 1.0 (neutral).
	channelRoutingLatencyFactorMin = 0.5
	channelRoutingLatencyFactorMax = 2.0
)

// channelRoutingRecomputeInterval caps how often the effective weights are
// recomputed (lazy, at most once per interval — the "recompute at most every
// 0.2-17 rhythm" anti-oscillation pace). var = test seam (0 = recompute on
// every selection trigger); production never touches it.
var channelRoutingRecomputeInterval = 10 * time.Second

// channelRoutingNow is the clock seam (mirrors the limiter's nowUnix seam) so
// tests can age records without sleeping.
var channelRoutingNow = time.Now

// --- Load term (D-R1 "stacked" ruling, 02-17-02) ---

// ChannelLoadProvider reports the current in-flight request count per channel
// id (channelID → count). The production instance is injected by main.go as a
// closure over the concurrency limiter's Snapshot (rows with Kind "channel"),
// so Redis deployments read the shared cross-node counters and MemoryLimiter
// deployments read the single-node ones — zero new counting infrastructure.
// Returning nil (a failed snapshot) must degrade to "no load signal".
type ChannelLoadProvider func() map[int]int

// channelRoutingLoadProvider is read on every recompute (at most once per
// channelRoutingRecomputeInterval) and invoked with NO lock held — a Redis
// snapshot I/O can never deadlock a selection. Atomic so SetChannelLoadProvider
// never needs the health mutex.
var channelRoutingLoadProvider atomic.Pointer[ChannelLoadProvider]

// SetChannelLoadProvider installs (or, with nil, unloads) the in-flight feed.
// Wiring is fail-open by construction: without a provider the multipliers are
// exactly the EWMA-only values.
func SetChannelLoadProvider(provider ChannelLoadProvider) {
	if provider == nil {
		channelRoutingLoadProvider.Store(nil)
		return
	}
	channelRoutingLoadProvider.Store(&provider)
}

func loadChannelRoutingProvider() ChannelLoadProvider {
	if stored := channelRoutingLoadProvider.Load(); stored != nil {
		return *stored
	}
	return nil
}

// --- Store ---

type channelRoutingKey struct {
	channelID int
	model     string
}

type channelRoutingRecord struct {
	samples      int
	successEWMA  float64 // [0,1]
	latencyEWMA  float64 // milliseconds
	lastSampleAt time.Time
	multiplier   float64
}

var channelRoutingStore = struct {
	mu            sync.Mutex
	records       map[channelRoutingKey]*channelRoutingRecord
	lastRecompute time.Time
}{
	records: make(map[channelRoutingKey]*channelRoutingRecord),
}

// channelRoutingRecompute serializes recomputes; separate from the data mutex
// so the (potentially slow) channel-fact and provider reads never block
// RecordChannelAttempt.
var channelRoutingRecompute sync.Mutex

// ChannelRoutingHealth is one observation row of the routing-health store
// (test assertions and ops diagnostics; exposed read-only at
// GET /api/concurrency/routing-health behind AdminAuth+ConcurrencyRead, see
// controller.GetChannelRoutingHealth). No json tags on purpose: the JSON keys
// are the Go field names and the admin card mirrors that contract verbatim.
type ChannelRoutingHealth struct {
	ChannelID    int
	Model        string
	Samples      int
	SuccessEWMA  float64
	LatencyEWMA  float64
	Multiplier   float64
	LastSampleAt time.Time
}

// RecordChannelAttempt records ONE dispatched upstream attempt of the Relay
// loop: its wall-clock duration and outcome. Callers must only record
// attempts that actually ran (the gateway-side saturation rejection is
// skipped by the relay loop) so constructed gateway-side 429s can never
// pollute channel health (T-02-17-01).
//
// The first sample seeds the EWMAs verbatim; later samples fold in with
// α = 1−0.5^(Δt/half-life), so a Δt≈0 update barely moves the EWMA — the
// natural slow-update that keeps the multiplier calm.
//
// latencyMs = 0 is the outcome-only sample (Realtime WS sessions use it): the
// record still counts toward samples and successEWMA, but a zero latencyEWMA
// is excluded from the same-model warm mean and keeps this record's
// latencyFactor at the neutral 1.0 (both guards read latencyEWMA > 0).
func RecordChannelAttempt(channelID int, model string, latencyMs int64, success bool) {
	if channelID <= 0 || model == "" {
		return
	}
	if latencyMs < 0 {
		latencyMs = 0
	}
	now := channelRoutingNow()

	channelRoutingStore.mu.Lock()
	defer channelRoutingStore.mu.Unlock()

	key := channelRoutingKey{channelID: channelID, model: model}
	record, ok := channelRoutingStore.records[key]
	if !ok {
		successSeed := 0.0
		if success {
			successSeed = 1.0
		}
		channelRoutingStore.records[key] = &channelRoutingRecord{
			samples:      1,
			successEWMA:  successSeed,
			latencyEWMA:  float64(latencyMs),
			lastSampleAt: now,
			multiplier:   1.0,
		}
		return
	}

	elapsed := now.Sub(record.lastSampleAt)
	if elapsed < 0 {
		elapsed = 0
	}
	sample := 0.0
	if success {
		sample = 1.0
	}
	record.successEWMA = channelRoutingEWMAStep(record.successEWMA, sample, elapsed, channelRoutingSuccessHalfLife)
	record.latencyEWMA = channelRoutingEWMAStep(record.latencyEWMA, float64(latencyMs), elapsed, channelRoutingLatencyHalfLife)
	record.samples++
	record.lastSampleAt = now
}

// channelRoutingEWMAStep folds one sample into an EWMA with half-life
// α = 1−0.5^(Δt/half-life). Δt≈0 ⇒ α≈0 ⇒ the value barely moves.
func channelRoutingEWMAStep(previous, sample float64, elapsed, halfLife time.Duration) float64 {
	if halfLife <= 0 {
		return sample
	}
	if elapsed <= 0 {
		return previous
	}
	alpha := 1 - math.Pow(0.5, float64(elapsed)/float64(halfLife))
	return previous + alpha*(sample-previous)
}

// SnapshotChannelRoutingHealth returns a stable, sorted copy of every health
// record (test assertions and ops diagnostics only).
func SnapshotChannelRoutingHealth() []ChannelRoutingHealth {
	channelRoutingStore.mu.Lock()
	defer channelRoutingStore.mu.Unlock()

	rows := make([]ChannelRoutingHealth, 0, len(channelRoutingStore.records))
	for key, record := range channelRoutingStore.records {
		rows = append(rows, ChannelRoutingHealth{
			ChannelID:    key.channelID,
			Model:        key.model,
			Samples:      record.samples,
			SuccessEWMA:  record.successEWMA,
			LatencyEWMA:  record.latencyEWMA,
			Multiplier:   record.multiplier,
			LastSampleAt: record.lastSampleAt,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ChannelID != rows[j].ChannelID {
			return rows[i].ChannelID < rows[j].ChannelID
		}
		return rows[i].Model < rows[j].Model
	})
	return rows
}

// ResetChannelRoutingHealth clears every record and the recompute timestamp
// (test isolation and ops diagnostics only — never wired to an HTTP handler).
// Clearing the timestamp also forces the next selection to recompute.
func ResetChannelRoutingHealth() {
	channelRoutingStore.mu.Lock()
	defer channelRoutingStore.mu.Unlock()
	channelRoutingStore.records = make(map[channelRoutingKey]*channelRoutingRecord)
	channelRoutingStore.lastRecompute = time.Time{}
}

// channelRoutingMultiplier is the selection-side read: the last recomputed
// multiplier of (channelID, model), exactly 1.0 when the record is absent,
// below the warm threshold, unconfigured, or stale — the byte-for-byte
// degradation to the status-quo static weighting.
func channelRoutingMultiplier(channelID int, model string) float64 {
	channelRoutingStore.mu.Lock()
	defer channelRoutingStore.mu.Unlock()
	if record, ok := channelRoutingStore.records[channelRoutingKey{channelID: channelID, model: model}]; ok {
		return record.multiplier
	}
	return 1.0
}

// channelEffectiveRoutingWeight scales one candidate's static weight by its
// recomputed multiplier. With multiplier 1.0 this is bit-for-bit the static
// value, so the degraded path (no records / gates closed / stale) is the
// status-quo selection; static 0 stays 0 so a zero-weight channel is still
// never picked and the all-zero smoothing rule keeps working.
func channelEffectiveRoutingWeight(base float64, channelID int, model string) float64 {
	multiplier := channelRoutingMultiplier(channelID, model)
	if multiplier == 1 {
		return base
	}
	return base * multiplier
}

// maybeRecomputeChannelRouting refreshes every record's multiplier, at most
// once per channelRoutingRecomputeInterval, lazily triggered from the two
// selection entry points (GetRandomSatisfiedChannel's cache branch and
// GetChannel). It must be called WITHOUT channelSyncLock held.
func maybeRecomputeChannelRouting() {
	now := channelRoutingNow()
	interval := channelRoutingRecomputeInterval

	channelRoutingStore.mu.Lock()
	if !channelRoutingStore.lastRecompute.IsZero() && now.Sub(channelRoutingStore.lastRecompute) < interval {
		channelRoutingStore.mu.Unlock()
		return
	}
	channelRoutingStore.mu.Unlock()

	channelRoutingRecompute.Lock()
	defer channelRoutingRecompute.Unlock()

	// Re-check under the recompute mutex: another selection may have just
	// completed a full pass while this one waited.
	channelRoutingStore.mu.Lock()
	if !channelRoutingStore.lastRecompute.IsZero() && now.Sub(channelRoutingStore.lastRecompute) < interval {
		channelRoutingStore.mu.Unlock()
		return
	}
	snapshot := make(map[channelRoutingKey]channelRoutingRecord, len(channelRoutingStore.records))
	for key, record := range channelRoutingStore.records {
		snapshot[key] = *record
	}
	channelRoutingStore.mu.Unlock()

	// Channel facts (existence + concurrency cap for the slot gate) are read
	// OUTSIDE the health lock: channelSyncLock is taken only for the snapshot
	// itself and released before any health-lock window, and the database
	// branch runs plain queries with no model lock held.
	facts := make(map[int]channelRoutingChannelFact)
	for key := range snapshot {
		if _, seen := facts[key.channelID]; seen {
			continue
		}
		facts[key.channelID] = lookupChannelRoutingFact(key.channelID)
	}

	// Load term (D-R1 "stacked"): the provider closure is invoked with no lock
	// held so a Redis snapshot can never deadlock selection. fail-open: nil
	// provider, nil map or snapshot error ⇒ inFlight stays nil ⇒ loadFactor
	// 1.0 — routing never depends on the load feed.
	var inFlight map[int]int
	if provider := loadChannelRoutingProvider(); provider != nil {
		if counts := provider(); counts != nil {
			inFlight = counts
		}
	}

	// Same-model warm-record mean latency drives the latency factor.
	warmMeanByModel := make(map[string]float64)
	warmSumByModel := make(map[string]float64)
	warmCountByModel := make(map[string]int)
	for key, record := range snapshot {
		if !channelRoutingRecordWarm(record, now) || record.latencyEWMA <= 0 {
			continue
		}
		warmSumByModel[key.model] += record.latencyEWMA
		warmCountByModel[key.model]++
	}
	for model, sum := range warmSumByModel {
		warmMeanByModel[model] = sum / float64(warmCountByModel[model])
	}

	multipliers := make(map[channelRoutingKey]float64, len(snapshot))
	for key, record := range snapshot {
		fact, known := facts[key.channelID]
		if !known || !fact.exists {
			continue // channel gone: the record is dropped below
		}
		if channelRoutingRecordStale(record, now) {
			continue // stale: the record is dropped below
		}
		// Slot gate: an unconfigured channel (limit 0/empty) always keeps the
		// static weight — EWMA never touches channels outside the 02-14 slot
		// regime. Warm gate: fewer than channelEWMAWarmSamples samples cannot
		// move the multiplier either.
		if fact.limit <= 0 || record.samples < channelEWMAWarmSamples {
			multipliers[key] = 1.0
			continue
		}

		successFactor := channelRoutingSuccessFactorFloor + (1-channelRoutingSuccessFactorFloor)*record.successEWMA
		latencyFactor := 1.0
		if mean := warmMeanByModel[key.model]; mean > 0 && record.latencyEWMA > 0 {
			latencyFactor = clampChannelRoutingFloat(mean/record.latencyEWMA, channelRoutingLatencyFactorMin, channelRoutingLatencyFactorMax)
		}
		// loadFactor = 1/(1+inFlight/limit): the fuller the channel's slot
		// budget, the less new traffic it attracts; an absent snapshot row is
		// zero in-flight → 1.0. The 0.2 total clamp below bounds the stack.
		loadFactor := 1.0
		if inFlight != nil {
			loadFactor = 1 / (1 + float64(inFlight[key.channelID])/float64(fact.limit))
		}
		multipliers[key] = clampChannelRoutingFloat(successFactor*latencyFactor*loadFactor, channelRoutingMultiplierMin, channelRoutingMultiplierMax)
	}

	channelRoutingStore.mu.Lock()
	defer channelRoutingStore.mu.Unlock()
	for key, record := range channelRoutingStore.records {
		if channelRoutingRecordStale(*record, now) {
			delete(channelRoutingStore.records, key)
			continue
		}
		fact, known := facts[key.channelID]
		if known && !fact.exists {
			delete(channelRoutingStore.records, key)
			continue
		}
		if multiplier, computed := multipliers[key]; computed {
			record.multiplier = multiplier
		}
	}
	channelRoutingStore.lastRecompute = now
}

func channelRoutingRecordWarm(record channelRoutingRecord, now time.Time) bool {
	return record.samples >= channelEWMAWarmSamples && !channelRoutingRecordStale(record, now)
}

func channelRoutingRecordStale(record channelRoutingRecord, now time.Time) bool {
	return now.Sub(record.lastSampleAt) > channelRoutingStaleDrop
}

func clampChannelRoutingFloat(value, min, max float64) float64 {
	return math.Min(math.Max(value, min), max)
}

type channelRoutingChannelFact struct {
	exists bool
	limit  int
}

// lookupChannelRoutingFact resolves one channel's existence and concurrency
// cap for the slot gate. Memory-cache deployments read the in-process cache
// under channelSyncLock.RLock (released before any health-lock window);
// database deployments batch-query the channels table. Degradation is
// fail-open: with neither a cache nor a database the channel counts as
// present with no cap, so records keep multiplier 1.0 instead of being
// wrongly dropped.
func lookupChannelRoutingFact(channelID int) channelRoutingChannelFact {
	if common.MemoryCacheEnabled {
		channelSyncLock.RLock()
		channel, ok := channelsIDM[channelID]
		channelSyncLock.RUnlock()
		if !ok {
			return channelRoutingChannelFact{exists: false}
		}
		return channelRoutingChannelFact{exists: true, limit: channel.GetConcurrencyLimit()}
	}
	if DB == nil {
		return channelRoutingChannelFact{exists: true}
	}
	var channel Channel
	if err := DB.First(&channel, "id = ?", channelID).Error; err != nil {
		return channelRoutingChannelFact{exists: false}
	}
	return channelRoutingChannelFact{exists: true, limit: channel.GetConcurrencyLimit()}
}
