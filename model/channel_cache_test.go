package model

import (
	"math/rand"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Priority-tier regression tests for GetRandomSatisfiedChannel (cache path)
// and GetChannel (DB fallback).
//
// Background (see .planning/debug/concurrency-priority-not-applied.md):
// Before Fix A the priority list was rebuilt from the post-exclusion candidate
// set, so excluding the highest tier caused retry=N to skip intermediate
// priority tiers (retry=1 ended up on the bottom tier instead of the N-th
// tier). Fix A keeps the priority list (and the retry clamp) anchored to the
// full pre-exclusion candidate set so the retry index stays aligned with the
// original priority ranking.

const priorityTestModel = "priority-tier-regression"

func newPriorityChannel(id int, priority int64, weight uint) Channel {
	return Channel{
		Id:       id,
		Type:     constant.ChannelTypeOpenAI,
		Key:      "key-" + strconv.Itoa(id),
		Status:   common.ChannelStatusEnabled,
		Name:     "ch-" + strconv.Itoa(id),
		Models:   priorityTestModel,
		Group:    "default",
		Priority: &priority,
		Weight:   &weight,
	}
}

// seedPriorityChannels inserts the three channels used by the priority-tier
// regression tests and rebuilds the in-memory cache.
func seedPriorityChannels(t *testing.T, ids [3]int, priorities [3]int64, weights [3]uint) {
	t.Helper()
	for i, id := range ids {
		ch := newPriorityChannel(id, priorities[i], weights[i])
		require.NoError(t, ch.Insert())
	}
	InitChannelCache()
}

// withMemoryCache flips MemoryCacheEnabled for the duration of the test.
func withMemoryCache(t *testing.T, enabled bool) {
	t.Helper()
	original := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = enabled
	t.Cleanup(func() {
		common.MemoryCacheEnabled = original
		InitChannelCache()
	})
	InitChannelCache()
}

// TestGetRandomSatisfiedChannel_PriorityRetryStepsThroughAllTiers is the core
// Fix A regression: with three priority tiers (100/50/0) and the highest-tier
// channel excluded, retry=1 must step to priority=50 — not skip it for the
// bottom tier as the pre-fix code did. The pre-fix code rebuilt the priority
// list from the post-exclusion set, so retry=1 (with the top tier excluded)
// ended up at sorted[1] = priority=0, skipping priority=50 entirely.
func TestGetRandomSatisfiedChannel_PriorityRetryStepsThroughAllTiers(t *testing.T) {
	truncateTables(t)
	withMemoryCache(t, true)
	seedPriorityChannels(t, [3]int{100001, 100002, 100003}, [3]int64{100, 50, 0}, [3]uint{10, 10, 10})

	// retry=0 with no exclusion — must pick the priority=100 channel.
	chosen, err := GetRandomSatisfiedChannel("default", priorityTestModel, 0, nil)
	require.NoError(t, err)
	require.NotNil(t, chosen)
	assert.Equal(t, 100001, chosen.Id, "retry=0 must pick the highest-priority channel")

	// retry=1 with the priority=100 channel excluded — this is the core
	// regression. Pre-fix code (priority list rebuilt from post-exclusion
	// candidates) would end up at sorted[1] = priority=0 because the only
	// remaining tiers are {50, 0}. Fix A keeps the priority list anchored
	// to the original {100, 50, 0} and lands on the original second tier
	// (priority=50).
	chosen, err = GetRandomSatisfiedChannel("default", priorityTestModel, 1, nil, 100001)
	require.NoError(t, err)
	require.NotNil(t, chosen)
	assert.Equal(t, 100002, chosen.Id,
		"with the highest-priority channel excluded, retry=1 must step to the second tier (priority=50), not the bottom tier (priority=0)")

	// retry=2 with the priority=100 channel excluded — must reach the
	// original third tier (priority=0). Pre-fix code would have landed on
	// priority=0 already at retry=1, so this distinguishes the two.
	chosen, err = GetRandomSatisfiedChannel("default", priorityTestModel, 2, nil, 100001)
	require.NoError(t, err)
	require.NotNil(t, chosen)
	assert.Equal(t, 100003, chosen.Id,
		"retry=2 with the highest tier excluded must land on the original third tier (priority=0)")

	// retry=2 with the top two tiers excluded — must clamp to the only
	// surviving tier (priority=0) instead of erroring. The retry clamp
	// against the ORIGINAL tier count (3) is what makes this work: the
	// post-exclusion tier count is only 1, but the clamp uses 3 so retry=2
	// does not index past the end of sortedUniquePriorities.
	chosen, err = GetRandomSatisfiedChannel("default", priorityTestModel, 2, nil, 100001, 100002)
	require.NoError(t, err)
	require.NotNil(t, chosen)
	assert.Equal(t, 100003, chosen.Id,
		"retry=2 with the top two tiers excluded must clamp to the only remaining tier (priority=0)")
}

// TestGetRandomSatisfiedChannel_RetryClampUsesOriginalTierCount guards the
// retry clamp boundary: retry >= len(uniquePriorities) must use the ORIGINAL
// (pre-exclusion) priority tier count, not the surviving (post-exclusion)
// count, otherwise a saturated request would silently under-utilise the
// remaining channels.
func TestGetRandomSatisfiedChannel_RetryClampUsesOriginalTierCount(t *testing.T) {
	truncateTables(t)
	withMemoryCache(t, true)
	seedPriorityChannels(t, [3]int{101001, 101002, 101003}, [3]int64{100, 50, 0}, [3]uint{10, 10, 10})

	// Exclude the two highest tiers so only priority=0 remains.
	// retry=99 must clamp to the highest-surviving tier, which is the
	// original third tier (priority=0) — proving the clamp used len=3 not len=1.
	chosen, err := GetRandomSatisfiedChannel("default", priorityTestModel, 99, nil, 101001, 101002)
	require.NoError(t, err)
	require.NotNil(t, chosen)
	assert.Equal(t, 101003, chosen.Id,
		"retry=99 with the top two tiers excluded must clamp to the only remaining tier (priority=0)")
}

// TestGetRandomSatisfiedChannel_NormalPathUnchanged pins the pre-existing
// behavior with no exclusion and a small retry index: the fix must not change
// the existing one-tier / two-tier / three-tier-with-no-exclusion path.
func TestGetRandomSatisfiedChannel_NormalPathUnchanged(t *testing.T) {
	truncateTables(t)
	withMemoryCache(t, true)
	seedPriorityChannels(t, [3]int{102001, 102002, 102003}, [3]int64{100, 50, 0}, [3]uint{10, 10, 10})

	// No exclusion: retry=0..2 must each land on its original tier.
	chosen, err := GetRandomSatisfiedChannel("default", priorityTestModel, 0, nil)
	require.NoError(t, err)
	require.NotNil(t, chosen)
	assert.Equal(t, 102001, chosen.Id)

	chosen, err = GetRandomSatisfiedChannel("default", priorityTestModel, 1, nil)
	require.NoError(t, err)
	require.NotNil(t, chosen)
	assert.Equal(t, 102002, chosen.Id)

	chosen, err = GetRandomSatisfiedChannel("default", priorityTestModel, 2, nil)
	require.NoError(t, err)
	require.NotNil(t, chosen)
	assert.Equal(t, 102003, chosen.Id)

	// retry=10 (well past len(uniquePriorities)=3) must clamp to the bottom tier.
	chosen, err = GetRandomSatisfiedChannel("default", priorityTestModel, 10, nil)
	require.NoError(t, err)
	require.NotNil(t, chosen)
	assert.Equal(t, 102003, chosen.Id, "retry beyond the last tier must clamp to the bottom priority")
}

// TestGetRandomSatisfiedChannel_SamePriorityPicksByWeight is the same-priority
// weighted-random path: when several channels share the same priority tier
// the picker must still be governed by the weight (no regression to either the
// exclusion or the priority-tier fix).
func TestGetRandomSatisfiedChannel_SamePriorityPicksByWeight(t *testing.T) {
	truncateTables(t)
	withMemoryCache(t, true)
	seedPriorityChannels(t, [3]int{103001, 103002, 103003}, [3]int64{100, 100, 100}, [3]uint{1, 10, 100})

	rand.Seed(1)

	counts := map[int]int{103001: 0, 103002: 0, 103003: 0}
	for range 200 {
		chosen, err := GetRandomSatisfiedChannel("default", priorityTestModel, 0, nil)
		require.NoError(t, err)
		require.NotNil(t, chosen)
		counts[chosen.Id]++
	}
	// weight 1:10:100 — the high-weight channel must dominate.
	assert.Greater(t, counts[103003], counts[103002],
		"high-weight channel should be picked more often than mid-weight")
	assert.Greater(t, counts[103002], counts[103001],
		"mid-weight channel should be picked more often than low-weight")
}

// TestGetRandomSatisfiedChannel_AllTiersExcludedReturnsNil guards the
// all-saturated boundary: when every channel is in the exclusion set the
// function must return (nil, nil) so the relay layer classifies the empty
// selection as "all channels saturated" upstream.
func TestGetRandomSatisfiedChannel_AllTiersExcludedReturnsNil(t *testing.T) {
	truncateTables(t)
	withMemoryCache(t, true)
	seedPriorityChannels(t, [3]int{105001, 105002, 105003}, [3]int64{100, 50, 0}, [3]uint{10, 10, 10})

	chosen, err := GetRandomSatisfiedChannel("default", priorityTestModel, 0, nil, 105001, 105002, 105003)
	require.NoError(t, err)
	assert.Nil(t, chosen, "excluding every candidate must return nil")
}

// TestGetRandomSatisfiedChannel_AllZeroWeightsKeepTheTierReachable pins the
// all-zero-weight degradation branch of the picker: when every candidate's
// static weight is 0 the tier total is 0, so the picker smooths the tier to
// equal weights (each candidate contributes the same smoothing adjustment)
// instead of drawing against a zero total. Without that rule the random draw
// is always 0 and every request lands on the SAME candidate — the
// load-balancing degeneration reported in
// concurrency-test/REPORT-LOADBALANCE.md. The observable contract is that the
// selection still returns a channel and stays spread over the whole tier.
func TestGetRandomSatisfiedChannel_AllZeroWeightsKeepTheTierReachable(t *testing.T) {
	truncateTables(t)
	withMemoryCache(t, true)
	seedPriorityChannels(t, [3]int{106001, 106002, 106003}, [3]int64{100, 100, 100}, [3]uint{0, 0, 0})

	rand.Seed(1)

	counts := map[int]int{106001: 0, 106002: 0, 106003: 0}
	const draws = 300
	for range draws {
		chosen, err := GetRandomSatisfiedChannel("default", priorityTestModel, 0, nil)
		require.NoError(t, err)
		require.NotNil(t, chosen, "an all-zero-weight tier must still select a channel")
		counts[chosen.Id]++
	}
	// Equal smoothing means every candidate keeps a share of the draw; a
	// degenerate picker hands every draw to one candidate and starves the
	// other two.
	for id, count := range counts {
		assert.Greater(t, count, draws/10,
			"channel %d must keep a share of the draw under the equal-weight smoothing", id)
	}
}

// TestGetChannel_DBPath_PriorityRetryStepsThroughAllTiers mirrors the cache
// regression for the !MemoryCacheEnabled DB fallback path: same priority-tier
// skipping must not happen there either. The two paths must stay in sync.
func TestGetChannel_DBPath_PriorityRetryStepsThroughAllTiers(t *testing.T) {
	truncateTables(t)
	withMemoryCache(t, false)
	seedPriorityChannels(t, [3]int{104001, 104002, 104003}, [3]int64{100, 50, 0}, [3]uint{10, 10, 10})

	// retry=0 with the highest tier excluded — must step to the next tier
	// (priority=50), not skip to priority=0. The DB fallback path mirrors
	// the cache path: it has the same exclusion-mirrors-cache contract, so
	// the fix must apply on both branches.
	chosen, err := GetChannel("default", priorityTestModel, 0, nil, 104001)
	require.NoError(t, err)
	require.NotNil(t, chosen)
	assert.Equal(t, 104002, chosen.Id,
		"DB fallback: with the highest tier excluded, retry=0 must land on the next tier (priority=50)")

	// retry=1 with the highest tier excluded — must reach the original
	// third tier (priority=0).
	chosen, err = GetChannel("default", priorityTestModel, 1, nil, 104001)
	require.NoError(t, err)
	require.NotNil(t, chosen)
	assert.Equal(t, 104003, chosen.Id,
		"DB fallback: retry=1 with the highest tier excluded must land on the original third tier (priority=0)")

	// retry=2 with the top two tiers excluded — must clamp to the only
	// surviving tier (priority=0) instead of erroring.
	chosen, err = GetChannel("default", priorityTestModel, 2, nil, 104001, 104002)
	require.NoError(t, err)
	require.NotNil(t, chosen)
	assert.Equal(t, 104003, chosen.Id,
		"DB fallback: retry=2 with the top two tiers excluded must clamp to the only remaining tier (priority=0)")

	// All tiers excluded must return nil.
	chosen, err = GetChannel("default", priorityTestModel, 0, nil, 104001, 104002, 104003)
	require.NoError(t, err)
	assert.Nil(t, chosen, "DB fallback: excluding every candidate must return nil")
}
