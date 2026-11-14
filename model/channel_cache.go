package model

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

var group2model2channels map[string]map[string][]int // enabled channel
var channelsIDM map[int]*Channel                     // all channels include disabled
// channel2advancedCustomConfig caches parsed Advanced Custom (type 58) configs so
// path-aware selection avoids re-parsing JSON per request. Refreshed on full sync.
var channel2advancedCustomConfig map[int]*kitdto.AdvancedCustomConfig
var channelSyncLock sync.RWMutex

func InitChannelCache() {
	if !common.MemoryCacheEnabled {
		InvalidatePricingCache()
		rebuildTaskAliasView()
		return
	}
	newChannelId2channel := make(map[int]*Channel)
	newChannel2advancedCustomConfig := make(map[int]*kitdto.AdvancedCustomConfig)
	var channels []*Channel
	DB.Find(&channels)
	for _, channel := range channels {
		newChannelId2channel[channel.Id] = channel
		if channel.Type == constant.ChannelTypeAdvancedCustom {
			if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
				newChannel2advancedCustomConfig[channel.Id] = config
			}
		}
	}
	// Abilities are the routing index, and this build now reads them the same
	// way the database selection path does (model.GetChannel filters on
	// `abilities`). AddAbilities (model/ability.go:277) mirrors a channel's
	// group/models/priority/weight onto one row per pair.
	//
	// Indexing the channels instead left the two paths disagreeing: pruning an
	// ability row retired a model on the database path while the cache kept
	// serving it. It also indexed a nil inner map whenever a channel's group
	// had no ability rows at all, panicking the periodic rebuild — and since
	// SyncChannelCache runs on a timer with no recover, that panic killed the
	// whole gateway process, not just the rebuild.
	//
	// The consequence to keep in mind: because abilities now gate routing, a
	// channel row that lost its ability rows (a swallowed UpdateAbilities
	// error at model/channel.go:943, or a direct SQL write) becomes unroutable
	// rather than crashing. Nothing repairs that automatically — FixAbility
	// only runs from InitChannelCache's recover branch (main.go:112), which
	// this fix makes unreachable. Startup repair is EnsureChannelAbilities
	// (model/ability.go), and the orphan check below reports the condition.
	var abilities []*Ability
	DB.Find(&abilities)
	newGroup2model2channels := make(map[string]map[string][]int)
	channelWithAbility := make(map[int]struct{}, len(abilities))
	var orphanChannels []int
	for _, ability := range abilities {
		channelWithAbility[ability.ChannelId] = struct{}{}
		if !ability.Enabled {
			continue
		}
		channel, ok := newChannelId2channel[ability.ChannelId]
		if !ok || channel.Status != common.ChannelStatusEnabled {
			continue // skip disabled channels and stale ability rows
		}
		group, model := ability.Group, ability.Model
		if group == "" || model == "" {
			continue
		}
		if _, ok := newGroup2model2channels[group]; !ok {
			newGroup2model2channels[group] = make(map[string][]int)
		}
		newGroup2model2channels[group][model] = append(newGroup2model2channels[group][model], ability.ChannelId)
	}
	for _, channel := range channels {
		if _, ok := channelWithAbility[channel.Id]; !ok && channel.Status == common.ChannelStatusEnabled {
			orphanChannels = append(orphanChannels, channel.Id)
		}
	}
	if len(orphanChannels) > 0 {
		common.SysError(fmt.Sprintf("%d enabled channel(s) have no ability rows and cannot be routed: %v",
			len(orphanChannels), orphanChannels))
	}

	// sort by priority
	for group, model2channels := range newGroup2model2channels {
		for model, channels := range model2channels {
			sort.Slice(channels, func(i, j int) bool {
				return newChannelId2channel[channels[i]].GetPriority() > newChannelId2channel[channels[j]].GetPriority()
			})
			newGroup2model2channels[group][model] = channels
		}
	}

	channelSyncLock.Lock()
	group2model2channels = newGroup2model2channels
	//channelsIDM = newChannelId2channel
	for i, channel := range newChannelId2channel {
		if channel.ChannelInfo.IsMultiKey {
			channel.Keys = channel.GetKeys()
			if channel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
				if oldChannel, ok := channelsIDM[i]; ok {
					// 存在旧的渠道，如果是多key且轮询，保留轮询索引信息
					if oldChannel.ChannelInfo.IsMultiKey && oldChannel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
						channel.ChannelInfo.MultiKeyPollingIndex = oldChannel.ChannelInfo.MultiKeyPollingIndex
					}
				}
			}
		}
	}
	channelsIDM = newChannelId2channel
	channel2advancedCustomConfig = newChannel2advancedCustomConfig
	channelSyncLock.Unlock()
	// Lock ordering: InvalidatePricingCache acquires updatePricingLock, and
	// GetPricing (holding updatePricingLock) nests channelSyncLock.RLock via
	// loadPricingAdvancedCustomConfigs. channelSyncLock MUST be released before
	// invalidating the pricing cache, otherwise the reversed order deadlocks.
	InvalidatePricingCache()
	rebuildTaskAliasView()
	common.SysLog(common.LogText("channels synced from database"))
}

func SyncChannelCache(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		common.SysLog(common.LogText("syncing channels from database"))
		InitChannelCache()
	}
}

// GetRandomSatisfiedChannel picks a random channel that satisfies the request.
// excluded carries the per-request saturation exclusion set (BCD D-3 option A):
// those channel ids are dropped from the candidate set AFTER the candidate
// build and BEFORE the priority layer mapping, so the layer list and its retry
// clamp operate on the remaining candidates and a saturated channel is never
// returned. An empty excluded list keeps the selection byte-for-byte as before,
// and no shared cache state is written (channelSyncLock discipline: read-only
// filtering inside the lock).
func GetRandomSatisfiedChannel(
	group string,
	model string,
	retry int,
	filters []dto.ChannelFilter,
	excluded ...int,
) (*Channel, error) {
	// if memory cache is disabled, get channel directly from database
	if !common.MemoryCacheEnabled {
		return GetChannel(group, model, retry, filters, excluded...)
	}

	// RTE-01 (02-17): refresh the dynamic effective-weight multipliers before
	// the candidate scan. The recompute runs outside channelSyncLock — it may
	// need that lock itself for the channel-fact snapshot, so it must never be
	// nested inside the selection lock (channelSyncLock → health read is the
	// only sanctioned nesting direction, and only for the brief multiplier
	// reads below).
	maybeRecomputeChannelRouting()

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	// First, try to find channels with the exact model name.
	channels, _ := filterCandidateIDs(group2model2channels[group][model], model, filters)

	// If no channels found, try to find channels with the normalized model name.
	if len(channels) == 0 {
		normalizedModel := ratio_setting.RoutingMatchModelName(model)
		channels, _ = filterCandidateIDs(group2model2channels[group][normalizedModel], model, filters)
	}

	if len(channels) == 0 {
		return nil, nil
	}

	// originalChannels captures the pre-exclusion candidate set so the priority
	// list (and the retry clamp) stay aligned to the full priority tier list
	// even when the highest tier is saturated and excluded. Otherwise the
	// post-exclusion `channels` slice loses intermediate priority tiers and
	// retry=N ends up picking the bottom priority instead of the N-th tier.
	originalChannels := make([]int, len(channels))
	copy(originalChannels, channels)

	// Saturation exclusion: build a fresh slice — filterCandidateIDs may hand
	// back the shared cache slice itself when no filter applies, so in-place
	// compaction would corrupt group2model2channels.
	if len(excluded) > 0 {
		kept := make([]int, 0, len(channels))
		for _, channelID := range channels {
			if !slices.Contains(excluded, channelID) {
				kept = append(kept, channelID)
			}
		}
		channels = kept
	}

	if len(channels) == 0 {
		return nil, nil
	}

	if len(channels) == 1 {
		if channel, ok := channelsIDM[channels[0]]; ok {
			return channel, nil
		}
		return nil, fmt.Errorf("database consistency error: channel #%d does not exist, please contact the administrator", channels[0])
	}

	uniquePriorities := make(map[int]bool)
	for _, channelId := range originalChannels {
		if channel, ok := channelsIDM[channelId]; ok {
			uniquePriorities[int(channel.GetPriority())] = true
		} else {
			return nil, fmt.Errorf("database consistency error: channel #%d does not exist, please contact the administrator", channelId)
		}
	}
	var sortedUniquePriorities []int
	for priority := range uniquePriorities {
		sortedUniquePriorities = append(sortedUniquePriorities, priority)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sortedUniquePriorities)))

	// Retry index is clamped against the ORIGINAL priority tier count so the
	// retry progression stays anchored to the pre-exclusion priority ranking.
	// Without this the post-exclusion priority list shrinks as saturation
	// accumulates and retry=N silently picks the bottom priority instead of
	// the N-th tier (see .planning/debug/concurrency-priority-not-applied.md).
	if retry >= len(sortedUniquePriorities) {
		retry = len(sortedUniquePriorities) - 1
	}

	// The retry index points at a priority tier; if every channel in that
	// tier was excluded (saturated), fall back to the next surviving tier
	// further down the sorted list. The production relay loop only ever
	// reaches this branch when retry>=1 — retry=0 starts with an empty
	// exclusion set, so retry=0 always finds the top tier.
	//
	// RTE-01 (02-17): the intra-layer weighted draw consumes the DYNAMIC
	// effective weight (static weight × the recomputed EWMA multiplier) of each
	// candidate. With every multiplier at 1.0 — the degraded default for
	// unrecorded / below-warm / unconfigured / stale channels — the float
	// arithmetic is bit-for-bit the previous integer arithmetic (the static
	// weights are integers, and rand.Float64()×total has the same distribution
	// as rand.Intn(total)), so the status-quo selection is preserved exactly.
	var sumWeight = 0.0
	var targetChannels []*Channel
	for tierIdx := retry; tierIdx < len(sortedUniquePriorities); tierIdx++ {
		candidatePriority := int64(sortedUniquePriorities[tierIdx])
		sumWeight = 0.0
		targetChannels = targetChannels[:0]
		for _, channelId := range channels {
			if channel, ok := channelsIDM[channelId]; ok {
				if channel.GetPriority() == candidatePriority {
					sumWeight += channelEffectiveRoutingWeight(float64(channel.GetWeight()), channel.Id, model)
					targetChannels = append(targetChannels, channel)
				}
			} else {
				return nil, fmt.Errorf("database consistency error: channel #%d does not exist, please contact the administrator", channelId)
			}
		}
		if len(targetChannels) > 0 {
			break
		}
	}

	if len(targetChannels) == 0 {
		return nil, errors.New(fmt.Sprintf("no channel found, group: %s, model: %s", group, model))
	}

	// smoothing factor and adjustment
	// (sumWeight/len < 10 compares exactly like the previous integer division:
	// sumWeight < 10·len either way)
	smoothingFactor := 1.0
	smoothingAdjustment := 0.0

	if sumWeight == 0 {
		// when all channels have weight 0, set sumWeight to the number of channels and set smoothing adjustment to 100
		// each channel's effective weight = 100
		sumWeight = float64(len(targetChannels)) * 100
		smoothingAdjustment = 100
	} else if sumWeight/float64(len(targetChannels)) < 10 {
		// when the average weight is less than 10, set smoothing factor to 100
		smoothingFactor = 100
	}

	// Calculate the total weight of all channels up to endIdx
	totalWeight := sumWeight * smoothingFactor

	// Generate a random value in the range [0, totalWeight)
	randomWeight := rand.Float64() * totalWeight

	// Find a channel based on its weight
	for i, channel := range targetChannels {
		randomWeight -= channelEffectiveRoutingWeight(float64(channel.GetWeight()), channel.Id, model)*smoothingFactor + smoothingAdjustment
		if randomWeight < 0 {
			return channel, nil
		}
		if i == len(targetChannels)-1 {
			// Floating-point dust guard: r < totalWeight guarantees a pick;
			// only accumulation rounding could leave a hair above zero here.
			return channel, nil
		}
	}
	// return null if no channel is not found
	return nil, errors.New("channel not found")
}

func CacheGetChannel(id int) (*Channel, error) {
	if !common.MemoryCacheEnabled {
		return GetChannelById(id, true)
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("channel #%d no longer exists", id)
	}
	return c, nil
}

func CacheGetChannelInfo(id int) (*ChannelInfo, error) {
	if !common.MemoryCacheEnabled {
		channel, err := GetChannelById(id, true)
		if err != nil {
			return nil, err
		}
		return &channel.ChannelInfo, nil
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("channel #%d no longer exists", id)
	}
	return &c.ChannelInfo, nil
}

func CacheUpdateChannelStatus(id int, status int) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	if channel, ok := channelsIDM[id]; ok {
		channel.Status = status
	}
	if status != common.ChannelStatusEnabled {
		// delete the channel from group2model2channels
		for group, model2channels := range group2model2channels {
			for model, channels := range model2channels {
				for i, channelId := range channels {
					if channelId == id {
						// remove the channel from the slice
						group2model2channels[group][model] = append(channels[:i], channels[i+1:]...)
						break
					}
				}
			}
		}
	}
}

func CacheUpdateChannel(channel *Channel) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	if channel == nil {
		channelSyncLock.Unlock()
		return
	}

	if channelsIDM == nil {
		channelsIDM = make(map[int]*Channel)
	}
	if oldChannel, ok := channelsIDM[channel.Id]; ok {
		logger.LogDebug(nil, "CacheUpdateChannel before: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, oldChannel.ChannelInfo.MultiKeyPollingIndex)
	}
	channelsIDM[channel.Id] = channel
	if channel2advancedCustomConfig == nil {
		channel2advancedCustomConfig = make(map[int]*kitdto.AdvancedCustomConfig)
	}
	delete(channel2advancedCustomConfig, channel.Id)
	if channel.Type == constant.ChannelTypeAdvancedCustom {
		if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
			channel2advancedCustomConfig[channel.Id] = config
		}
	}
	logger.LogDebug(nil, "CacheUpdateChannel after: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, channel.ChannelInfo.MultiKeyPollingIndex)
	// Lock ordering: do NOT hold channelSyncLock while calling
	// InvalidatePricingCache. GetPricing acquires updatePricingLock first and then
	// channelSyncLock.RLock (via loadPricingAdvancedCustomConfigs); acquiring
	// updatePricingLock while holding channelSyncLock would be an AB-BA deadlock.
	channelSyncLock.Unlock()
	InvalidatePricingCache()
}
