package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskPluginChannelSelectionFiltersBothCachePaths(t *testing.T) {
	truncateTables(t)
	priority := int64(0)
	weight := uint(1)
	baseURL := "https://example.com"
	alphaSetting := `{"task_plugin_key":"alpha"}`
	betaSetting := `{"task_plugin_key":"beta"}`
	channels := []Channel{
		{Id: 900001, Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled, Name: "alpha", Models: "shared", Group: "default", Priority: &priority, Weight: &weight, BaseURL: &baseURL, Setting: &alphaSetting},
		{Id: 900002, Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled, Name: "beta", Models: "shared", Group: "default", Priority: &priority, Weight: &weight, BaseURL: &baseURL, Setting: &betaSetting},
		{Id: 900003, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "ordinary", Models: "ordinary", Group: "default", Priority: &priority, Weight: &weight},
		{Id: 900004, Type: constant.ChannelTypeKling, Status: common.ChannelStatusEnabled, Name: "legacy-alpha", Models: "legacy", Group: "default", Priority: &priority, Weight: &weight},
		{Id: 900005, Type: constant.ChannelTypeJimeng, Status: common.ChannelStatusEnabled, Name: "legacy-beta", Models: "legacy", Group: "default", Priority: &priority, Weight: &weight},
	}
	for i := range channels {
		require.NoError(t, channels[i].Insert())
	}

	selected, err := GetChannel("default", "shared", 0, identityFilters("alpha", nil))
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, "alpha", selected.Name)
	selected, err = GetChannel("default", "shared", 0, identityFilters("", nil))
	require.NoError(t, err)
	assert.Nil(t, selected)
	selected, err = GetChannel("default", "ordinary", 0, identityFilters("", nil))
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, "ordinary", selected.Name)
	selected, err = GetChannel("default", "legacy", 0, identityFilters("legacy-alpha", []int{constant.ChannelTypeKling}))
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, "legacy-alpha", selected.Name)
	selected, err = GetChannel("default", "legacy", 0, identityFilters("legacy-alpha", []int{constant.ChannelTypeKling, constant.ChannelTypeJimeng}))
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Contains(t, []string{"legacy-alpha", "legacy-beta"}, selected.Name)
}

func identityFilters(key string, channelTypes []int) []dto.ChannelFilter {
	return []dto.ChannelFilter{{
		Kind:                   dto.FilterTaskPluginIdentity,
		TaskPluginKey:          key,
		TaskPluginChannelTypes: channelTypes,
	}}
}

func TestSharedPluginKeysFilterBothChannelSources(t *testing.T) {
	truncateTables(t)
	originalMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = originalMemoryCache; InitChannelCache() })
	filters := []dto.ChannelFilter{{Kind: dto.FilterTaskPluginIdentity, TaskPluginKey: "alpha", TaskPluginKeys: []string{"alpha", "beta"}}}
	var abilities []Ability
	for index, key := range []string{"alpha", "beta", "unrelated"} {
		setting := `{"task_plugin_key":"` + key + `"}`
		channel := Channel{Id: 910001 + index, Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled, Name: key, Models: "shared", Group: "default", Setting: &setting}
		require.NoError(t, channel.Insert())
		abilities = append(abilities, Ability{ChannelId: channel.Id, Model: "shared", Group: "default", Enabled: true})
		matches, _ := ChannelSatisfiesFilters(&channel, "shared", filters)
		assert.Equal(t, index < 2, matches)
	}
	assert.Equal(t, abilities[:2], filterAbilitiesByConstraints(abilities, "shared", filters))
	InitChannelCache()
	channelSyncLock.RLock()
	kept, emptied := filterCandidateIDs([]int{910001, 910002, 910003}, "shared", filters)
	channelSyncLock.RUnlock()
	assert.Equal(t, []int{910001, 910002}, kept)
	assert.Empty(t, emptied)
}

func TestNewAPIChannelServesExtendedTaskPlugins(t *testing.T) {
	truncateTables(t)
	priority := int64(0)
	weight := uint(1)
	baseURL := "https://gateway.example"
	extended := `{"task_extend_plugin_keys":["alpha","gamma"]}`
	single := `{"task_plugin_key":"delta"}`
	channels := []Channel{
		{Id: 920001, Type: constant.ChannelTypeNewAPI, Status: common.ChannelStatusEnabled, Name: "gateway", Models: "shared,chat", Group: "default", Priority: &priority, Weight: &weight, BaseURL: &baseURL, Setting: &extended},
		{Id: 920002, Type: constant.ChannelTypeNewAPI, Status: common.ChannelStatusEnabled, Name: "gateway-single", Models: "shared", Group: "default", Priority: &priority, Weight: &weight, BaseURL: &baseURL, Setting: &single},
	}
	for i := range channels {
		require.NoError(t, channels[i].Insert())
	}

	selected, err := GetChannel("default", "shared", 0, identityFilters("alpha", nil))
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, "gateway", selected.Name)
	selected, err = GetChannel("default", "shared", 0, identityFilters("delta", nil))
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, "gateway-single", selected.Name, "a single task_plugin_key still binds a New API channel")
	selected, err = GetChannel("default", "shared", 0, identityFilters("beta", nil))
	require.NoError(t, err)
	assert.Nil(t, selected, "unbound plugins never reach the gateway channel")
	shared := []dto.ChannelFilter{{Kind: dto.FilterTaskPluginIdentity, TaskPluginKey: "beta", TaskPluginKeys: []string{"beta", "gamma"}}}
	selected, err = GetChannel("default", "shared", 0, shared)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, "gateway", selected.Name, "a bound shared-model candidate admits the channel")
	selected, err = GetChannel("default", "chat", 0, identityFilters("", nil))
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, "gateway", selected.Name, "requests without a pinned plugin keep using the gateway")
}

func TestModelModifierNamesRouteWithAndWithoutMemoryCache(t *testing.T) {
	truncateTables(t)
	originalMemoryCache := common.MemoryCacheEnabled
	t.Cleanup(func() { common.MemoryCacheEnabled = originalMemoryCache; InitChannelCache() })
	channel := Channel{Id: 930001, Type: constant.ChannelTypeDeepSeek, Status: common.ChannelStatusEnabled, Name: "deepseek", Models: "deepseek-flash", Group: "default"}
	require.NoError(t, channel.Insert())

	for _, memoryCache := range []bool{false, true} {
		common.MemoryCacheEnabled = memoryCache
		InitChannelCache()
		selected, err := GetRandomSatisfiedChannel("default", "deepseek-flash@effort:max", 0, nil)
		require.NoError(t, err)
		require.NotNil(t, selected, "memory cache %v", memoryCache)
		assert.Equal(t, "deepseek", selected.Name)
		selected, err = GetRandomSatisfiedChannel("default", "deepseek-pro@effort:max", 0, nil)
		require.NoError(t, err)
		assert.Nil(t, selected, "memory cache %v", memoryCache)
	}
}

// TestChannelSelectionExcludesSaturatedCandidatesBothCachePaths pins the
// selector-side exclusion of the per-request saturation set (BCD D-3 option A)
// on BOTH selection paths: with the saturated channel A excluded, N
// consecutive picks must never return A — neither on the !MemoryCacheEnabled
// database path (GetChannel) nor on the memory-cache path
// (GetRandomSatisfiedChannel) — while excluding every candidate yields no
// channel (the empty-selection outcome the relay layer classifies) and an
// empty exclusion set keeps the existing pick-any behavior. Exclusion happens
// before the priority layer mapping, so the layer clamp falls back over the
// remaining candidates.
func TestChannelSelectionExcludesSaturatedCandidatesBothCachePaths(t *testing.T) {
	truncateTables(t)
	priority := int64(0)
	weightA, weightB := uint(1000), uint(1)
	channels := []Channel{
		{Id: 930001, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "excl-a", Models: "excl-shared", Group: "default", Priority: &priority, Weight: &weightA},
		{Id: 930002, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "excl-b", Models: "excl-shared", Group: "default", Priority: &priority, Weight: &weightB},
	}
	for i := range channels {
		require.NoError(t, channels[i].Insert())
	}

	// Database path: GetChannel always queries the abilities table.
	for i := range 20 {
		chosen, err := GetChannel("default", "excl-shared", 0, nil, 930001)
		require.NoError(t, err)
		require.NotNil(t, chosen, "db-path selection %d must find the non-excluded channel", i)
		assert.Equal(t, 930002, chosen.Id, "db-path selection %d must never return the excluded channel", i)
	}
	allExcluded, err := GetChannel("default", "excl-shared", 0, nil, 930001, 930002)
	require.NoError(t, err)
	assert.Nil(t, allExcluded, "excluding every candidate must yield no channel")
	emptyExclusion, err := GetChannel("default", "excl-shared", 0, nil)
	require.NoError(t, err)
	require.NotNil(t, emptyExclusion, "an empty exclusion set must keep the existing selection behavior")
	assert.Contains(t, []int{930001, 930002}, emptyExclusion.Id)

	// Memory-cache path (mirrors the both-cache-paths switch of
	// TestSharedPluginKeysFilterBothChannelSources).
	originalMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = originalMemoryCache
		InitChannelCache()
	})
	InitChannelCache()
	for i := range 20 {
		chosen, err := GetRandomSatisfiedChannel("default", "excl-shared", 0, nil, 930001)
		require.NoError(t, err)
		require.NotNil(t, chosen, "cache-path selection %d must find the non-excluded channel", i)
		assert.Equal(t, 930002, chosen.Id, "cache-path selection %d must never return the excluded channel", i)
	}
	allExcludedCached, err := GetRandomSatisfiedChannel("default", "excl-shared", 0, nil, 930001, 930002)
	require.NoError(t, err)
	assert.Nil(t, allExcludedCached, "excluding every candidate must yield no channel on the cache path either")
	emptyExclusionCached, err := GetRandomSatisfiedChannel("default", "excl-shared", 0, nil)
	require.NoError(t, err)
	require.NotNil(t, emptyExclusionCached, "an empty exclusion set must keep the existing selection behavior")
	assert.Contains(t, []int{930001, 930002}, emptyExclusionCached.Id)
}

// ---------------------------------------------------------------------------
// RTE-01: channel routing health — gates, clamps, half-life bounds (02-17)
// ---------------------------------------------------------------------------

// seedRoutingHealthChannel inserts one channel row (the recompute's slot gate
// reads channels for existence + ConcurrencyLimit on the !MemoryCacheEnabled
// database path) and removes it again after the test.
func seedRoutingHealthChannel(t *testing.T, id int, modelName string, weight uint, concurrencyLimit *int) {
	t.Helper()
	channel := Channel{
		Id:               id,
		Type:             constant.ChannelTypeOpenAI,
		Status:           common.ChannelStatusEnabled,
		Name:             fmt.Sprintf("routing-health-%d", id),
		Models:           modelName,
		Group:            "default",
		Weight:           &weight,
		ConcurrencyLimit: concurrencyLimit,
	}
	require.NoError(t, channel.Insert())
	t.Cleanup(func() {
		DB.Where("id = ?", id).Delete(&Channel{})
	})
}

func recordRoutingHealthSamples(channelID int, modelName string, samples int, latencyMs int64, success bool) {
	for range samples {
		RecordChannelAttempt(channelID, modelName, latencyMs, success)
	}
}

func routingHealthRow(t *testing.T, channelID int, modelName string) ChannelRoutingHealth {
	t.Helper()
	for _, row := range SnapshotChannelRoutingHealth() {
		if row.ChannelID == channelID && row.Model == modelName {
			return row
		}
	}
	require.FailNowf(t, "routing health row missing", "channel=%d model=%s", channelID, modelName)
	return ChannelRoutingHealth{}
}

// TestChannelRoutingHealthGatesAndFormulas pins the D-R1 degradation semantics
// and the anti-oscillation parameters as deterministic cases: the warm gate
// (<10 samples → multiplier exactly 1.0), the slot gate (no configured
// ConcurrencyLimit → multiplier exactly 1.0 regardless of samples), the exact
// planner formulas (successFactor = 0.2+0.8×successEWMA, latencyFactor =
// clamp(warm-mean/record, 0.5, 2.0)) with both latency clamp directions
// observable, the neutral single-warm-record identity (multiplier exactly
// 1.0), the stale drop with warm-gate self-heal, the documented constant
// bounds, and the byte-equivalent neutral consumption of the static weight.
func TestChannelRoutingHealthGatesAndFormulas(t *testing.T) {
	t.Cleanup(ResetChannelRoutingHealth)
	// The recompute interval seam: 0 = recompute on every trigger, so every
	// assertion below observes the state right after its samples land. The
	// production default must stay the documented 10s anti-oscillation pace.
	require.Equal(t, 10*time.Second, channelRoutingRecomputeInterval,
		"the production recompute cadence must stay the documented 10s anti-oscillation pace")
	originalInterval := channelRoutingRecomputeInterval
	channelRoutingRecomputeInterval = 0
	t.Cleanup(func() { channelRoutingRecomputeInterval = originalInterval })

	limit := 1

	t.Run("warm gate 9 samples keep 1.0, 10 samples leave it", func(t *testing.T) {
		const modelName = "routing-warm-gate"
		seedRoutingHealthChannel(t, 940001, modelName, 90, &limit)
		seedRoutingHealthChannel(t, 940002, modelName, 10, &limit)
		recordRoutingHealthSamples(940001, modelName, 9, 5000, false)
		recordRoutingHealthSamples(940002, modelName, 10, 5000, false)
		maybeRecomputeChannelRouting()
		assert.Equal(t, 1.0, routingHealthRow(t, 940001, modelName).Multiplier,
			"9 samples stay below the warm threshold: the effective weight component must equal the static weight")
		// 10 failing samples: successFactor = 0.2+0.8×0 = 0.2, latencyFactor = 1
		// (the only warm record is its own mean) → exactly the 0.2 floor.
		assert.InDelta(t, 0.2, routingHealthRow(t, 940002, modelName).Multiplier, 1e-9,
			"10 samples cross the warm threshold and the failing record must land on the 0.2 clamp floor")
	})

	t.Run("slot gate keeps unconfigured channels at 1.0", func(t *testing.T) {
		const modelName = "routing-slot-gate"
		seedRoutingHealthChannel(t, 940011, modelName, 90, nil)
		seedRoutingHealthChannel(t, 940012, modelName, 10, &limit)
		recordRoutingHealthSamples(940011, modelName, 20, 5000, false)
		recordRoutingHealthSamples(940012, modelName, 20, 5000, false)
		maybeRecomputeChannelRouting()
		assert.Equal(t, 1.0, routingHealthRow(t, 940011, modelName).Multiplier,
			"an unconfigured channel is permanently gated to multiplier 1.0 even with warm failing samples")
		assert.InDelta(t, 0.2, routingHealthRow(t, 940012, modelName).Multiplier, 1e-9,
			"the configured twin proves the formula WOULD shift the multiplier — the slot gate is what holds the unconfigured one at 1.0")
	})

	t.Run("neutral single warm record is exactly 1.0", func(t *testing.T) {
		const modelName = "routing-neutral"
		seedRoutingHealthChannel(t, 940021, modelName, 90, &limit)
		recordRoutingHealthSamples(940021, modelName, 20, 123, true)
		maybeRecomputeChannelRouting()
		assert.Equal(t, 1.0, routingHealthRow(t, 940021, modelName).Multiplier,
			"all-success single record: successFactor 1 × latencyFactor 1 (mean = itself) must stay exactly neutral")
	})

	t.Run("mixed warm stats pin the exact planner formulas", func(t *testing.T) {
		const modelName = "routing-formulas"
		seedRoutingHealthChannel(t, 940031, modelName, 90, &limit)
		seedRoutingHealthChannel(t, 940032, modelName, 10, &limit)
		recordRoutingHealthSamples(940031, modelName, 20, 5000, false)
		recordRoutingHealthSamples(940032, modelName, 20, 100, true)
		maybeRecomputeChannelRouting()
		// Warm mean latency = (5000+100)/2 = 2550.
		// A: (0.2+0.8×0) × clamp(2550/5000, 0.5, 2.0) = 0.2×0.51 = 0.102 → 0.2 floor.
		assert.InDelta(t, 0.2, routingHealthRow(t, 940031, modelName).Multiplier, 1e-9,
			"the failing/latency-heavy record must sit exactly on the multiplier floor")
		// B: (0.2+0.8×1) × clamp(2550/100, 0.5, 2.0) = 1×2.0 → latency-factor ceiling.
		assert.InDelta(t, 2.0, routingHealthRow(t, 940032, modelName).Multiplier, 1e-9,
			"the healthy/fast record must sit exactly on the 2.0 latency-factor ceiling")
	})

	t.Run("stale records drop on recompute and re-seed through the warm gate", func(t *testing.T) {
		const modelName = "routing-stale"
		seedRoutingHealthChannel(t, 940041, modelName, 90, &limit)
		originalNow := channelRoutingNow
		base := channelRoutingNow()
		current := base
		channelRoutingNow = func() time.Time { return current }
		t.Cleanup(func() { channelRoutingNow = originalNow })

		recordRoutingHealthSamples(940041, modelName, 20, 123, true)
		maybeRecomputeChannelRouting()
		require.Equal(t, 20, routingHealthRow(t, 940041, modelName).Samples)

		current = base.Add(channelRoutingStaleDrop + time.Second)
		maybeRecomputeChannelRouting()
		assert.Empty(t, SnapshotChannelRoutingHealth(),
			"a record silent for longer than the stale window must be dropped wholesale on the next recompute")

		RecordChannelAttempt(940041, modelName, 123, true)
		maybeRecomputeChannelRouting()
		row := routingHealthRow(t, 940041, modelName)
		assert.Equal(t, 1, row.Samples, "the re-seeded record starts over from its first fresh sample")
		assert.Equal(t, 1.0, row.Multiplier, "and must re-enter through the warm gate at multiplier 1.0")

		// The recompute above stamped lastRecompute on the advanced test clock
		// (5+ minutes ahead of wall time), which would make the interval
		// double-check skip every later subtest's recompute. Clear the store
		// (Reset also zeroes the recompute timestamp) before handing back.
		ResetChannelRoutingHealth()
	})

	t.Run("constants carry the documented anti-oscillation bounds", func(t *testing.T) {
		assert.Equal(t, 10, channelEWMAWarmSamples)
		assert.Equal(t, 60*time.Second, channelRoutingLatencyHalfLife)
		assert.GreaterOrEqual(t, channelRoutingLatencyHalfLife, channelRoutingLatencyHalfLifeMin)
		assert.LessOrEqual(t, channelRoutingLatencyHalfLife, channelRoutingLatencyHalfLifeMax)
		assert.Equal(t, 300*time.Second, channelRoutingSuccessHalfLife)
		assert.GreaterOrEqual(t, channelRoutingSuccessHalfLife, channelRoutingSuccessHalfLifeMin)
		assert.LessOrEqual(t, channelRoutingSuccessHalfLife, channelRoutingSuccessHalfLifeMax)
		assert.Equal(t, 0.2, channelRoutingMultiplierMin)
		assert.Equal(t, 5.0, channelRoutingMultiplierMax)
		assert.Equal(t, 5*time.Minute, channelRoutingStaleDrop)
	})

	t.Run("neutral consumption is byte-equivalent to the static weight", func(t *testing.T) {
		const modelName = "routing-neutral-consume"
		seedRoutingHealthChannel(t, 940051, modelName, 90, &limit)
		recordRoutingHealthSamples(940051, modelName, 20, 5000, false)
		maybeRecomputeChannelRouting()
		assert.Equal(t, 90.0, channelEffectiveRoutingWeight(90, 949999, modelName),
			"a channel without records must keep its static weight bit-for-bit")
		assert.InDelta(t, 18.0, channelEffectiveRoutingWeight(90, 940051, modelName), 1e-9,
			"a warmed channel must consume the static weight scaled by its recomputed multiplier")
	})
}

// TestChannelRoutingHealthLoadProviderMatrix pins the D-R1 "stacked" load term
// (02-17-02): the channel:<id> in-flight counts arrive through the injected
// ChannelLoadProvider and scale the multiplier by loadFactor
// = 1/(1+inFlight/limit). The degradation matrix is fail-open at every step —
// nil provider, nil map and empty map all reproduce the EWMA-only Task 01
// multipliers bit-for-bit — and the health stats below are the tracer's A/B
// fixture so every expectation is an exact formula value.
func TestChannelRoutingHealthLoadProviderMatrix(t *testing.T) {
	t.Cleanup(ResetChannelRoutingHealth)
	t.Cleanup(func() { SetChannelLoadProvider(nil) })
	originalInterval := channelRoutingRecomputeInterval
	channelRoutingRecomputeInterval = 0
	t.Cleanup(func() { channelRoutingRecomputeInterval = originalInterval })

	const modelName = "routing-load-matrix"
	const channelA = 940101
	const channelB = 940102
	limit := 1
	seedRoutingHealthChannel(t, channelA, modelName, 90, &limit)
	seedRoutingHealthChannel(t, channelB, modelName, 10, &limit)
	recordRoutingHealthSamples(channelA, modelName, 20, 5000, false)
	recordRoutingHealthSamples(channelB, modelName, 20, 100, true)

	multipliersOf := func(t *testing.T) (float64, float64) {
		t.Helper()
		rows := SnapshotChannelRoutingHealth()
		var wantA, wantB float64
		foundA, foundB := false, false
		for _, row := range rows {
			if row.Model != modelName {
				continue
			}
			switch row.ChannelID {
			case channelA:
				wantA, foundA = row.Multiplier, true
			case channelB:
				wantB, foundB = row.Multiplier, true
			}
		}
		require.True(t, foundA && foundB, "both warm records must survive the recompute")
		return wantA, wantB
	}

	scenarios := []struct {
		name      string
		provider  ChannelLoadProvider
		wantA     float64
		wantB     float64
		rationale string
	}{
		{
			name:      "nil provider fail-open",
			provider:  nil,
			wantA:     0.2,
			wantB:     2.0,
			rationale: "unloaded provider must reproduce the Task 01 EWMA-only multipliers exactly",
		},
		{
			name:      "nil map fail-open",
			provider:  func() map[int]int { return nil },
			wantA:     0.2,
			wantB:     2.0,
			rationale: "a snapshot failure (nil map) degrades to loadFactor 1.0, never to a wrong count",
		},
		{
			name:      "empty map counts zero in-flight",
			provider:  func() map[int]int { return map[int]int{} },
			wantA:     0.2,
			wantB:     2.0,
			rationale: "a channel absent from the snapshot is at zero in-flight → loadFactor 1.0",
		},
		{
			name:      "in-flight at limit halves the healthy channel",
			provider:  func() map[int]int { return map[int]int{channelB: 1} },
			wantA:     0.2,
			wantB:     1.0,
			rationale: "B: 2.0 × 1/(1+1/1) = 1.0 exactly (loadFactor 0.5); A stays floored at 0.2",
		},
		{
			name:      "in-flight at 3x limit floors A through the total clamp",
			provider:  func() map[int]int { return map[int]int{channelA: 3} },
			wantA:     0.2,
			wantB:     2.0,
			rationale: "A: 0.2×0.51×1/(1+3) = 0.0255 → the 0.2 total clamp catches the stack; B untouched",
		},
		{
			name:      "in-flight at 9x limit floors the healthy channel too",
			provider:  func() map[int]int { return map[int]int{channelB: 9} },
			wantA:     0.2,
			wantB:     0.2,
			rationale: "B: 2.0 × 1/(1+9/1) = 0.2 exactly on the total clamp floor",
		},
	}

	var previousB float64
	for i, scenario := range scenarios {
		SetChannelLoadProvider(scenario.provider)
		maybeRecomputeChannelRouting()
		gotA, gotB := multipliersOf(t)
		assert.InDeltaf(t, scenario.wantA, gotA, 1e-9, "scenario %d (%s): %s", i, scenario.name, scenario.rationale)
		assert.InDeltaf(t, scenario.wantB, gotB, 1e-9, "scenario %d (%s): %s", i, scenario.name, scenario.rationale)
		if i == 0 {
			previousB = gotB
		}
		if i == 3 {
			assert.Less(t, gotB, previousB,
				"adding in-flight to the same record must strictly lower its multiplier vs the no-load control")
		}
	}
	SetChannelLoadProvider(nil)
}

// TestChannelRoutingHealthDropsRecordsOfRemovedChannels pins the memory bound:
// a record whose channel no longer exists (deleted between attempts) is
// cleaned out on the next recompute instead of accumulating forever.
func TestChannelRoutingHealthDropsRecordsOfRemovedChannels(t *testing.T) {
	t.Cleanup(ResetChannelRoutingHealth)
	originalInterval := channelRoutingRecomputeInterval
	channelRoutingRecomputeInterval = 0
	t.Cleanup(func() { channelRoutingRecomputeInterval = originalInterval })

	const modelName = "routing-missing-channel"
	const channelID = 940111
	limit := 1
	seedRoutingHealthChannel(t, channelID, modelName, 90, &limit)
	recordRoutingHealthSamples(channelID, modelName, 20, 100, true)
	maybeRecomputeChannelRouting()
	require.Equal(t, 20, routingHealthRow(t, channelID, modelName).Samples)

	require.NoError(t, DB.Where("id = ?", channelID).Delete(&Channel{}).Error)
	maybeRecomputeChannelRouting()
	assert.Empty(t, SnapshotChannelRoutingHealth(),
		"the record of a removed channel must be dropped on the next recompute")
}
