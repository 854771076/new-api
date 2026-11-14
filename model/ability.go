package model

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Ability struct {
	Group     string  `json:"group" gorm:"type:varchar(64);primaryKey;autoIncrement:false"`
	Model     string  `json:"model" gorm:"type:varchar(255);primaryKey;autoIncrement:false"`
	ChannelId int     `json:"channel_id" gorm:"primaryKey;autoIncrement:false;index"`
	Enabled   bool    `json:"enabled"`
	Priority  *int64  `json:"priority" gorm:"bigint;default:0;index"`
	Weight    uint    `json:"weight" gorm:"default:0;index"`
	Tag       *string `json:"tag" gorm:"index"`
}

type AbilityWithChannel struct {
	Ability
	ChannelType int `json:"channel_type"`
}

func GetAllEnableAbilityWithChannels() ([]AbilityWithChannel, error) {
	var abilities []AbilityWithChannel
	err := DB.Table("abilities").
		Select("abilities.*, channels.type as channel_type").
		Joins("left join channels on abilities.channel_id = channels.id").
		Where("abilities.enabled = ?", true).
		Scan(&abilities).Error
	return abilities, err
}

func GetGroupEnabledModels(group string) []string {
	var models []string
	// Find distinct models
	DB.Table("abilities").Where(commonGroupCol+" = ? and enabled = ?", group, true).Distinct("model").Pluck("model", &models)
	return models
}

func GetEnabledModels() []string {
	var models []string
	// Find distinct models
	DB.Table("abilities").Where("enabled = ?", true).Distinct("model").Pluck("model", &models)
	return models
}

func GetAllEnableAbilities() []Ability {
	var abilities []Ability
	DB.Find(&abilities, "enabled = ?", true)
	return abilities
}

func getPriority(group string, model string, retry int) (int, error) {

	var priorities []int
	err := DB.Model(&Ability{}).
		Select("DISTINCT(priority)").
		Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, model, true).
		Order("priority DESC").              // 按优先级降序排序
		Pluck("priority", &priorities).Error // Pluck用于将查询的结果直接扫描到一个切片中

	if err != nil {
		// 处理错误
		return 0, err
	}

	if len(priorities) == 0 {
		// 如果没有查询到优先级，则返回错误
		return 0, errors.New("database consistency is broken")
	}

	// 确定要使用的优先级
	var priorityToUse int
	if retry >= len(priorities) {
		// 如果重试次数大于优先级数，则使用最小的优先级
		priorityToUse = priorities[len(priorities)-1]
	} else {
		priorityToUse = priorities[retry]
	}
	return priorityToUse, nil
}

func getChannelQuery(group string, model string, retry int) (*gorm.DB, error) {
	maxPrioritySubQuery := DB.Model(&Ability{}).Select("MAX(priority)").Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, model, true)
	channelQuery := DB.Where(commonGroupCol+" = ? and model = ? and enabled = ? and priority = (?)", group, model, true, maxPrioritySubQuery)
	if retry != 0 {
		priority, err := getPriority(group, model, retry)
		if err != nil {
			return nil, err
		} else {
			channelQuery = DB.Where(commonGroupCol+" = ? and model = ? and enabled = ? and priority = ?", group, model, true, priority)
		}
	}

	return channelQuery, nil
}

// GetChannel is the !MemoryCacheEnabled database fallback of
// GetRandomSatisfiedChannel. excluded mirrors the cache path's per-request
// saturation exclusion (BCD D-3 option A): excluded channel ids are dropped
// from the ability candidates AFTER the request-filter pass and BEFORE the
// priority list is built, so the layer mapping and its retry clamp fall back
// over the remaining candidates and a saturated channel is never returned.
// An empty excluded list keeps the selection byte-for-byte as before.
func GetChannel(
	group string,
	model string,
	retry int,
	filters []dto.ChannelFilter,
	excluded ...int,
) (*Channel, error) {
	// RTE-01 (02-17): refresh the dynamic effective-weight multipliers at the
	// database-path entry, same as the cache path (never under channelSyncLock).
	maybeRecomputeChannelRouting()

	var abilities []Ability
	err := DB.Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, model, true).Order("priority DESC, weight DESC").Find(&abilities).Error
	if err != nil {
		return nil, err
	}
	abilities = filterAbilitiesByConstraints(abilities, model, filters)
	// Like the memory cache, retry with the routing name (for example without
	// @ modifiers) when no channel serves the exact name.
	if normalized := ratio_setting.RoutingMatchModelName(model); len(abilities) == 0 && normalized != "" && normalized != model {
		err = DB.Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, normalized, true).Order("priority DESC, weight DESC").Find(&abilities).Error
		if err != nil {
			return nil, err
		}
		abilities = filterAbilitiesByConstraints(abilities, model, filters)
	}
	// Saturation exclusion: dropped AFTER the request-filter pass and BEFORE
	// the priority list is built, so the layer mapping and its retry clamp stay
	// aligned to the full priority tier list and a saturated channel is never
	// returned. Mirrors the cache path in model/channel_cache.go.
	if len(excluded) > 0 {
		kept := make([]Ability, 0, len(abilities))
		for _, ability := range abilities {
			if !slices.Contains(excluded, ability.ChannelId) {
				kept = append(kept, ability)
			}
		}
		abilities = kept
	}
	if len(abilities) > 0 {
		// originalAbilities captures the pre-exclusion ability set so the
		// priority list (and the retry clamp) stay aligned to the full priority
		// tier list even when the highest tier is excluded. Mirrors the cache
		// path in model/channel_cache.go: the DB fallback must not skip
		// intermediate priority tiers when retry=N.
		originalAbilities := make([]Ability, len(abilities))
		copy(originalAbilities, abilities)
		priorities := make([]int64, 0)
		seen := make(map[int64]bool)
		for _, ability := range originalAbilities {
			priority := int64(0)
			if ability.Priority != nil {
				priority = *ability.Priority
			}
			if !seen[priority] {
				seen[priority] = true
				priorities = append(priorities, priority)
			}
		}
		sort.Slice(priorities, func(i, j int) bool { return priorities[i] > priorities[j] })
		// Retry index is clamped against the ORIGINAL priority tier count so
		// the retry progression stays anchored to the pre-exclusion ranking.
		// Mirrors the cache path in model/channel_cache.go: the DB fallback
		// must not skip intermediate priority tiers when retry=N.
		if retry >= len(priorities) {
			retry = len(priorities) - 1
		}
		// Fallback: if every channel in the targeted tier was excluded, drop
		// down to the next surviving tier. Mirrors the cache path.
		var remainingAbilities []Ability
		for tierIdx := retry; tierIdx < len(priorities); tierIdx++ {
			candidatePriority := priorities[tierIdx]
			kept := lo.Filter(abilities, func(ability Ability, _ int) bool {
				return ability.Priority == nil && candidatePriority == 0 || ability.Priority != nil && *ability.Priority == candidatePriority
			})
			if len(kept) > 0 {
				remainingAbilities = kept
				break
			}
		}
		if remainingAbilities == nil {
			// Every tier in sorted order is fully excluded. Mirror the cache
			// path: the production relay loop reaches this only when the
			// per-request exclusion set already covers every candidate, which
			// the relay classifies as "all channels saturated" upstream.
			return nil, nil
		}
		abilities = remainingAbilities
	}
	channel := Channel{}
	if len(abilities) > 0 {
		// Randomly choose one. RTE-01 (02-17): the DB-path base
		// (Ability.Weight+10) is scaled by the candidate's recomputed EWMA
		// multiplier — the same dynamic effective weight the cache path
		// consumes. With every multiplier at 1.0 (the degraded default) the
		// float arithmetic is bit-for-bit the previous integer arithmetic, so
		// the status-quo selection is preserved exactly.
		weightSum := 0.0
		for _, ability_ := range abilities {
			weightSum += channelEffectiveRoutingWeight(float64(ability_.Weight)+10, ability_.ChannelId, model)
		}
		// Randomly choose one
		weight := rand.Float64() * weightSum
		for i, ability_ := range abilities {
			weight -= channelEffectiveRoutingWeight(float64(ability_.Weight)+10, ability_.ChannelId, model)
			if weight <= 0 {
				channel.Id = ability_.ChannelId
				break
			}
			if i == len(abilities)-1 {
				// Floating-point dust guard: r < weightSum guarantees a pick;
				// only accumulation rounding could leave a hair above zero here.
				channel.Id = ability_.ChannelId
				break
			}
		}
	} else {
		return nil, nil
	}
	err = DB.First(&channel, "id = ?", channel.Id).Error
	return &channel, err
}

// filterAbilitiesByConstraints applies the same ChannelSatisfiesFilters
// predicate used by the memory-cache path. A failed channel lookup fails
// closed when a task-plugin identity is required and fails open otherwise.
func filterAbilitiesByConstraints(abilities []Ability, modelName string, filters []dto.ChannelFilter) []Ability {
	if len(abilities) == 0 {
		return nil
	}

	channelIds := make([]int, 0, len(abilities))
	seen := make(map[int]struct{}, len(abilities))
	for _, ability := range abilities {
		if _, ok := seen[ability.ChannelId]; ok {
			continue
		}
		seen[ability.ChannelId] = struct{}{}
		channelIds = append(channelIds, ability.ChannelId)
	}

	var channels []*Channel
	if err := DB.Where("id IN ?", channelIds).Find(&channels).Error; err != nil {
		if identityFilterRequiresKey(filters) {
			return nil
		}
		return abilities
	}

	channelsByID := make(map[int]*Channel, len(channels))
	for _, channel := range channels {
		channelsByID[channel.Id] = channel
	}

	filtered := make([]Ability, 0, len(abilities))
	for _, ability := range abilities {
		channel := channelsByID[ability.ChannelId]
		if ok, _ := ChannelSatisfiesFilters(channel, modelName, filters); ok {
			filtered = append(filtered, ability)
		}
	}
	return filtered
}

func identityFilterRequiresKey(filters []dto.ChannelFilter) bool {
	for _, filter := range filters {
		if filter.Kind == dto.FilterTaskPluginIdentity && filter.TaskPluginKey != "" {
			return true
		}
	}
	return false
}

func (channel *Channel) AddAbilities(tx *gorm.DB) error {
	models_ := strings.Split(channel.Models, ",")
	groups_ := strings.Split(channel.Group, ",")
	abilitySet := make(map[string]struct{})
	abilities := make([]Ability, 0, len(models_))
	for _, model := range models_ {
		for _, group := range groups_ {
			key := group + "|" + model
			if _, exists := abilitySet[key]; exists {
				continue
			}
			abilitySet[key] = struct{}{}
			ability := Ability{
				Group:     group,
				Model:     model,
				ChannelId: channel.Id,
				Enabled:   channel.Status == common.ChannelStatusEnabled,
				Priority:  channel.Priority,
				Weight:    uint(channel.GetWeight()),
				Tag:       channel.Tag,
			}
			abilities = append(abilities, ability)
		}
	}
	if len(abilities) == 0 {
		return nil
	}
	// choose DB or provided tx
	useDB := DB
	if tx != nil {
		useDB = tx
	}
	for _, chunk := range lo.Chunk(abilities, 50) {
		err := useDB.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
		if err != nil {
			return err
		}
	}
	return nil
}

func (channel *Channel) DeleteAbilities() error {
	return DB.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error
}

// UpdateAbilities updates abilities of this channel.
// Make sure the channel is completed before calling this function.
func (channel *Channel) UpdateAbilities(tx *gorm.DB) error {
	isNewTx := false
	// 如果没有传入事务，创建新的事务
	if tx == nil {
		tx = DB.Begin()
		if tx.Error != nil {
			return tx.Error
		}
		isNewTx = true
		defer func() {
			if r := recover(); r != nil {
				tx.Rollback()
			}
		}()
	}

	// First delete all abilities of this channel
	err := tx.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error
	if err != nil {
		if isNewTx {
			tx.Rollback()
		}
		return err
	}

	// Then add new abilities
	models_ := channel.GetModels()
	groups_ := strings.Split(channel.Group, ",")
	abilitySet := make(map[string]struct{})
	abilities := make([]Ability, 0, len(models_))
	for _, model := range models_ {
		for _, group := range groups_ {
			key := group + "|" + model
			if _, exists := abilitySet[key]; exists {
				continue
			}
			abilitySet[key] = struct{}{}
			ability := Ability{
				Group:     group,
				Model:     model,
				ChannelId: channel.Id,
				Enabled:   channel.Status == common.ChannelStatusEnabled,
				Priority:  channel.Priority,
				Weight:    uint(channel.GetWeight()),
				Tag:       channel.Tag,
			}
			abilities = append(abilities, ability)
		}
	}

	if len(abilities) > 0 {
		for _, chunk := range lo.Chunk(abilities, 50) {
			err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
			if err != nil {
				if isNewTx {
					tx.Rollback()
				}
				return err
			}
		}
	}

	// 如果是新创建的事务，需要提交
	if isNewTx {
		return tx.Commit().Error
	}

	return nil
}

func UpdateAbilityStatus(channelId int, status bool) error {
	return DB.Model(&Ability{}).Where("channel_id = ?", channelId).Select("enabled").Update("enabled", status).Error
}

func UpdateAbilityStatusByTag(tag string, status bool) error {
	return DB.Model(&Ability{}).Where("tag = ?", tag).Select("enabled").Update("enabled", status).Error
}

func UpdateAbilityByTag(tag string, newTag *string, priority *int64, weight *uint) error {
	ability := Ability{}
	if newTag != nil {
		ability.Tag = newTag
	}
	if priority != nil {
		ability.Priority = priority
	}
	if weight != nil {
		ability.Weight = *weight
	}
	return DB.Model(&Ability{}).Where("tag = ?", tag).Updates(ability).Error
}

var fixLock = sync.Mutex{}

// EnsureChannelAbilities restores the routing index for enabled channels that
// have no ability rows at all, without touching the rows that already exist.
//
// The routing index IS the ability table (model/channel_cache.go
// InitChannelCache), and the write paths keep the two in sync — but not
// atomically: Channel.Insert commits the channel row before AddAbilities, and
// EditChannelByTag swallows an UpdateAbilities failure (model/channel.go:943).
// A swallowed error therefore leaves an enabled channel permanently unroutable
// with no other symptom.
//
// FixAbility would repair that by TRUNCATE-then-rebuild, which is the wrong tool
// for startup: it drops and recreates every row, so on a multi-node deployment
// each node's rebuild races the others and briefly empties the live index. This
// only adds what is missing, and AddAbilities upserts with OnConflict
// DoNothing, so concurrent nodes converge on the same rows.
//
// Returns the number of channels repaired and the number that failed.
func EnsureChannelAbilities() (int, int, error) {
	var orphaned []Channel
	err := DB.Where("status = ?", common.ChannelStatusEnabled).
		Where("NOT EXISTS (SELECT 1 FROM abilities WHERE abilities.channel_id = channels.id)").
		Find(&orphaned).Error
	if err != nil {
		return 0, 0, err
	}
	if len(orphaned) == 0 {
		return 0, 0, nil
	}
	common.SysLog(fmt.Sprintf("EnsureChannelAbilities: repairing %d channel(s) missing ability rows", len(orphaned)))
	repaired, failed := 0, 0
	for idx := range orphaned {
		if err := orphaned[idx].AddAbilities(nil); err != nil {
			failed++
			common.SysError(fmt.Sprintf("EnsureChannelAbilities: failed to rebuild abilities for channel #%d: %v",
				orphaned[idx].Id, err))
			continue
		}
		repaired++
	}
	return repaired, failed, nil
}

func FixAbility() (int, int, error) {
	lock := fixLock.TryLock()
	if !lock {
		return 0, 0, common.NewMessage("A repair task is already running. Please try again later")
	}
	defer fixLock.Unlock()

	// truncate abilities table
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		err := DB.Exec("DELETE FROM abilities").Error
		if err != nil {
			common.SysLog(common.LogText("Delete abilities failed: %s", err.Error()))
			return 0, 0, err
		}
	} else {
		err := DB.Exec("TRUNCATE TABLE abilities").Error
		if err != nil {
			common.SysLog(common.LogText("Truncate abilities failed: %s", err.Error()))
			return 0, 0, err
		}
	}
	var channels []*Channel
	// Find all channels
	err := DB.Model(&Channel{}).Find(&channels).Error
	if err != nil {
		return 0, 0, err
	}
	if len(channels) == 0 {
		return 0, 0, nil
	}
	successCount := 0
	failCount := 0
	for _, chunk := range lo.Chunk(channels, 50) {
		ids := lo.Map(chunk, func(c *Channel, _ int) int { return c.Id })
		// Delete all abilities of this channel
		err = DB.Where("channel_id IN ?", ids).Delete(&Ability{}).Error
		if err != nil {
			common.SysLog(common.LogText("Delete abilities failed: %s", err.Error()))
			failCount += len(chunk)
			continue
		}
		// Then add new abilities
		for _, channel := range chunk {
			err = channel.AddAbilities(nil)
			if err != nil {
				common.SysLog(common.LogText("Add abilities for channel %d failed: %s", channel.Id, err.Error()))
				failCount++
			} else {
				successCount++
			}
		}
	}
	InitChannelCache()
	return successCount, failCount, nil
}
