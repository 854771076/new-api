// FORK-LOCAL-EXTENSION: 2026-10-20 channel-lifecycle regression suite; remove-when: upstream grows an equivalent create→edit→enable/disable→delete→recreate contract suite.
//
// The production incident behind this file was a CONFIGURATION mistake, not a
// relay bug: an administrator typed a model_mapping into a channel and the
// request then 404'd. That class of mistake is invisible to wire-level relay
// tests, which start from a hand-written channel row and therefore assume the
// admin's configuration already produced what the relay expects.
//
// This file closes that gap by driving the channel the way the admin UI does —
// write a row, expand abilities, edit the model list, toggle status, delete and
// recreate — and asserting the STATE each step leaves behind. The routing
// table (`abilities`) is derived state, so "did the write path keep it in
// sync" is a real contract, not an implementation detail.
//
// Two of the cases below deliberately pin AS-IS behaviour rather than the
// desirable behaviour, because the gap between them is the finding:
//
//   - validateChannel (controller/channel.go) does NOT validate model_mapping.
//     A syntactically broken mapping is accepted, stored verbatim, and only
//     blows up much later as `unmarshal_model_mapping_failed` on every request
//     that channel serves.
//   - Channel.GetSetting() does NOT fail loudly on unparsable setting JSON.
//     It logs, nils the in-memory field and writes NULL back to the row —
//     the operator's saved channel settings are silently destroyed by a single
//     read.
//
// Both are recorded by tests so a future fix flips a red test on purpose.

package controller

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// channelLifecycleDB installs the shared in-memory SQLite fixture and, ahead of
// it, the channel-cache restore hook. Cleanup runs LIFO, so registering the
// cache restore FIRST makes it run LAST — after newChannelSlotDB has put the
// original model.DB back. That way InitChannelCache rebuilds from the restored
// database instead of from a fixture handle that is about to be closed.
func channelLifecycleDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousMemoryCache := common.MemoryCacheEnabled
	t.Cleanup(func() {
		common.MemoryCacheEnabled = previousMemoryCache
		model.InitChannelCache()
	})
	return newChannelSlotDB(t)
}

// abilityModelGroup renders one channel's ability rows as sorted "group|model"
// keys so a cartesian-product assertion never depends on row order.
func abilityModelGroup(t *testing.T, db *gorm.DB, channelID int) []string {
	t.Helper()
	var abilities []model.Ability
	require.NoError(t, db.Where("channel_id = ?", channelID).Find(&abilities).Error)
	keys := make([]string, 0, len(abilities))
	for _, ability := range abilities {
		keys = append(keys, ability.Group+"|"+ability.Model)
	}
	sort.Strings(keys)
	return keys
}

func sortedStrings(values ...string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

// TestChannelInsertExpandsAbilityCartesianProduct pins the derived-routing
// contract an administrator relies on when they type "models" and "group" into
// the channel form: the abilities table must gain exactly one enabled row per
// (model × group) pair. A channel that stores the comma list but never expands
// it is invisible to every database-path channel selection.
func TestChannelInsertExpandsAbilityCartesianProduct(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := channelLifecycleDB(t)

	channel := &model.Channel{
		Id:     9101,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "lifecycle-insert-key",
		Name:   "lifecycle-insert-channel",
		Status: common.ChannelStatusEnabled,
		Group:  "lc-g1,lc-g2",
		Models: "lc-m1,lc-m2,lc-m3",
	}
	require.NoError(t, channel.Insert())

	got := abilityModelGroup(t, db, channel.Id)
	want := sortedStrings(
		"lc-g1|lc-m1", "lc-g1|lc-m2", "lc-g1|lc-m3",
		"lc-g2|lc-m1", "lc-g2|lc-m2", "lc-g2|lc-m3",
	)
	assert.Equal(t, want, got,
		"Insert must expand the channel's (Models × Group) list into one ability row per pair")

	var enabled int64
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ? AND enabled = ?", channel.Id, true).Count(&enabled).Error)
	assert.Equal(t, int64(6), enabled,
		"an enabled channel must seed every expanded ability row as enabled")
}

// TestChannelUpdateRebuildsAbilitiesForNewModels pins the edit half of the
// lifecycle: after an administrator narrows or renames the model list, the
// abilities table must contain the NEW pairs and none of the old ones. Leaving
// a stale ability behind is worse than a missing one — the stale row keeps
// routing requests to a channel that no longer advertises that model.
func TestChannelUpdateRebuildsAbilitiesForNewModels(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := channelLifecycleDB(t)

	channel := &model.Channel{
		Id:     9102,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "lifecycle-update-key",
		Name:   "lifecycle-update-channel",
		Status: common.ChannelStatusEnabled,
		Group:  "lc-ug1,lc-ug2",
		Models: "lc-old-a,lc-old-b",
	}
	require.NoError(t, channel.Insert())
	require.Equal(t,
		sortedStrings("lc-ug1|lc-old-a", "lc-ug1|lc-old-b", "lc-ug2|lc-old-a", "lc-ug2|lc-old-b"),
		abilityModelGroup(t, db, channel.Id),
		"the insert baseline must be the full cartesian product before the edit is exercised")

	channel.Models = "lc-new-a,lc-old-b"
	require.NoError(t, channel.Update())

	got := abilityModelGroup(t, db, channel.Id)
	want := sortedStrings(
		"lc-ug1|lc-new-a", "lc-ug1|lc-old-b",
		"lc-ug2|lc-new-a", "lc-ug2|lc-old-b",
	)
	assert.Equal(t, want, got,
		"Update must rebuild abilities from the new model list: the dropped model must lose every group row")
	assert.NotContains(t, strings.Join(got, ","), "lc-old-a",
		"a model removed from the channel must leave no ability row behind (stale rows keep routing to it)")
	assert.Contains(t, strings.Join(got, ","), "lc-new-a",
		"a model added to the channel must be routable immediately")
}

// TestDisabledChannelDropsOutOfChannelSelection pins the enable/disable half of
// the lifecycle against the memory-cache routing path, which is what a running
// gateway actually reads. Disabling is only meaningful if the next cache build
// drops the channel from the candidate set — otherwise an operator who killed a
// broken channel keeps getting billed through it.
func TestDisabledChannelDropsOutOfChannelSelection(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := channelLifecycleDB(t)

	common.MemoryCacheEnabled = true

	const (
		group  = "lc-sel-group"
		model1 = "lc-sel-model"
	)
	channel := &model.Channel{
		Id:     9103,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "lifecycle-select-key",
		Name:   "lifecycle-select-channel",
		Status: common.ChannelStatusEnabled,
		Group:  group,
		Models: model1,
	}
	require.NoError(t, channel.Insert())
	model.InitChannelCache()

	selected, err := model.GetRandomSatisfiedChannel(group, model1, 0, nil)
	require.NoError(t, err)
	require.NotNil(t, selected, "the freshly inserted channel must be selectable")
	assert.Equal(t, channel.Id, selected.Id)

	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).
		Update("status", common.ChannelStatusManuallyDisabled).Error)
	model.InitChannelCache()

	selected, err = model.GetRandomSatisfiedChannel(group, model1, 0, nil)
	require.NoError(t, err)
	assert.Nil(t, selected,
		"a manually disabled channel must leave the routing candidate set after the cache rebuild")
}

// TestDeletingAbilityRowsStopsRoutingOnBothPaths pins that the ability table is
// the single routing index for BOTH selection paths.
//
// It used to be asymmetric: the database path (model.GetChannel) filters on
// `abilities`, while the memory-cache path rebuilt its index from the channel
// rows alone. Pruning one model's ability row therefore silenced the database
// path while the cache kept serving that model — an operator (or script) that
// retires a model on one path silently keeps serving it on the other.
func TestDeletingAbilityRowsStopsRoutingOnBothPaths(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := channelLifecycleDB(t)

	common.MemoryCacheEnabled = true

	const (
		group      = "lc-del-group"
		prunedName = "lc-del-pruned-model"
		keptName   = "lc-del-kept-model"
	)
	channel := &model.Channel{
		Id:     9104,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "lifecycle-delete-key",
		Name:   "lifecycle-delete-channel",
		Status: common.ChannelStatusEnabled,
		Group:  group,
		Models: prunedName + "," + keptName,
	}
	require.NoError(t, channel.Insert())
	require.NotEmpty(t, abilityModelGroup(t, db, channel.Id), "the insert baseline must seed ability rows")

	require.NoError(t, db.Where("channel_id = ? AND model = ?", channel.Id, prunedName).Delete(&model.Ability{}).Error)
	assert.NotContains(t, strings.Join(abilityModelGroup(t, db, channel.Id), ","), prunedName,
		"the pruned model's ability row must really be gone")

	model.InitChannelCache()
	cached, err := model.GetRandomSatisfiedChannel(group, prunedName, 0, nil)
	require.NoError(t, err)
	assert.Nil(t, cached,
		"the memory-cache path must not keep serving a model whose ability row was pruned")

	kept, err := model.GetRandomSatisfiedChannel(group, keptName, 0, nil)
	require.NoError(t, err)
	require.NotNil(t, kept, "pruning one model's ability must not retire the channel's other models")
	assert.Equal(t, channel.Id, kept.Id)

	common.MemoryCacheEnabled = false
	direct, err := model.GetRandomSatisfiedChannel(group, prunedName, 0, nil)
	require.NoError(t, err)
	assert.Nil(t, direct,
		"the database path reads abilities, so the pruned model must be unselectable there")
}

// TestInitChannelCacheToleratesChannelGroupWithNoAbilityRows pins the crash
// that used to end the whole gateway process.
//
// InitChannelCache used to seed its group index from the ability rows and then
// fill it from the channel rows, so a channel whose group had no ability row at
// all indexed a nil inner map and panicked on assignment
// (model/channel_cache.go:65). SyncChannelCache runs that rebuild on a timer,
// so any write that desynchronises a channel row from its abilities — a
// hand-pruned ability table, a partial delete, a direct SQL update to `group` —
// took the process down rather than degrading one route.
func TestInitChannelCacheToleratesChannelGroupWithNoAbilityRows(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := channelLifecycleDB(t)

	common.MemoryCacheEnabled = true

	const (
		group  = "lc-orphan-group"
		model1 = "lc-orphan-model"
	)
	channel := &model.Channel{
		Id:     9109,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "lifecycle-orphan-key",
		Name:   "lifecycle-orphan-channel",
		Status: common.ChannelStatusEnabled,
		Group:  group,
		Models: model1,
	}
	require.NoError(t, channel.Insert())
	require.NotEmpty(t, abilityModelGroup(t, db, channel.Id),
		"Insert seeds abilities, so the condition below is caused by the pruning step alone")

	require.NoError(t, db.Where("channel_id = ?", channel.Id).Delete(&model.Ability{}).Error)

	require.NotPanics(t, func() { model.InitChannelCache() },
		"the cache rebuild must survive a channel whose abilities were pruned away")

	orphaned, err := model.GetRandomSatisfiedChannel(group, model1, 0, nil)
	require.NoError(t, err)
	assert.Nil(t, orphaned,
		"an ability-less channel must be unroutable rather than served from the channel row alone")

	// Restoring the ability row must make the channel routable again.
	require.NoError(t, db.Create(&model.Ability{
		Group: group, Model: model1, ChannelId: channel.Id, Enabled: true,
	}).Error)
	model.InitChannelCache()
	restored, err := model.GetRandomSatisfiedChannel(group, model1, 0, nil)
	require.NoError(t, err)
	require.NotNil(t, restored,
		"the abilities table is the routing index, so restoring the row must restore routing")
	assert.Equal(t, channel.Id, restored.Id)
}

// TestInitChannelCacheRetiresOnlyThePrunedModel pins that pruning one model's
// ability row retires exactly that pair and leaves the channel's other models
// routable — the fix must not turn into "one bad row kills the channel".
func TestInitChannelCacheRetiresOnlyThePrunedModel(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := channelLifecycleDB(t)

	common.MemoryCacheEnabled = true

	const (
		group     = "lc-partial-group"
		keptModel = "lc-partial-kept-model"
		cutModel  = "lc-partial-cut-model"
	)
	channel := &model.Channel{
		Id:     9110,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "lifecycle-partial-key",
		Name:   "lifecycle-partial-channel",
		Status: common.ChannelStatusEnabled,
		Group:  group,
		Models: keptModel + "," + cutModel,
	}
	require.NoError(t, channel.Insert())
	require.NoError(t, db.Where("channel_id = ? AND model = ?", channel.Id, cutModel).
		Delete(&model.Ability{}).Error)

	model.InitChannelCache()
	cut, err := model.GetRandomSatisfiedChannel(group, cutModel, 0, nil)
	require.NoError(t, err)
	assert.Nil(t, cut, "the pruned model must be unroutable")

	kept, err := model.GetRandomSatisfiedChannel(group, keptModel, 0, nil)
	require.NoError(t, err)
	require.NotNil(t, kept, "the channel must remain routable for its other models")
	assert.Equal(t, channel.Id, kept.Id)
}

// TestChannelModelMappingStorageContract pins the write contract for
// model_mapping — the exact field whose misconfiguration caused the original
// incident. The blob is parsed per request, so the write path is the only place
// that can reject a mapping which would never work.
func TestChannelModelMappingStorageContract(t *testing.T) {
	require.NoError(t, i18n.Init())
	channelLifecycleDB(t)

	t.Run("valid mapping round-trips byte-for-byte and parses", func(t *testing.T) {
		mapping := `{"virtual-name":"real-upstream-name","second-virtual":"second-real"}`
		channel := &model.Channel{
			Id:           9105,
			Type:         constant.ChannelTypeOpenAI,
			Key:          "lifecycle-mapping-valid-key",
			Name:         "lifecycle-mapping-valid",
			Status:       common.ChannelStatusEnabled,
			Group:        "lc-map-group",
			Models:       "virtual-name,second-virtual",
			ModelMapping: &mapping,
		}
		require.NoError(t, validateChannel(channel, true),
			"a well-formed model_mapping must be accepted by the channel write path")
		require.NoError(t, channel.Insert())

		stored, err := model.GetChannelById(channel.Id, true)
		require.NoError(t, err)
		require.NotNil(t, stored.ModelMapping)
		assert.Equal(t, mapping, *stored.ModelMapping,
			"model_mapping must be persisted verbatim as the operator typed it (no re-serialisation, no key rewriting)")
		assert.Equal(t, mapping, stored.GetModelMapping(),
			"GetModelMapping must hand the request path the raw stored JSON")

		parsed := map[string]string{}
		require.NoError(t, common.UnmarshalJsonStr(stored.GetModelMapping(), &parsed))
		assert.Equal(t, map[string]string{
			"virtual-name":   "real-upstream-name",
			"second-virtual": "second-real",
		}, parsed, "the stored value must parse back into the operator's virtual→upstream pairs")
	})

	t.Run("invalid mapping JSON is rejected at write time", func(t *testing.T) {
		broken := `{"virtual-name":`
		channel := &model.Channel{
			Id:           9106,
			Type:         constant.ChannelTypeOpenAI,
			Key:          "lifecycle-mapping-broken-key",
			Name:         "lifecycle-mapping-broken",
			Status:       common.ChannelStatusEnabled,
			Group:        "lc-map-broken",
			Models:       "virtual-name",
			ModelMapping: &broken,
		}

		// The mapping blob used to be parsed only per request
		// (relay/helper/model_mapped.go), so an unparsable one saved cleanly
		// and then failed EVERY request that hit the channel with
		// "unmarshal_model_mapping_failed". The write path now rejects it.
		err := validateChannel(channel, true)
		require.Error(t, err,
			"a model_mapping that cannot be parsed must be rejected when the channel is saved")
		assert.Contains(t, err.Error(), "model mapping",
			"the error must name the field so the operator knows which input to fix")

		emptyTarget := `{"virtual-name":""}`
		channel.ModelMapping = &emptyTarget
		err = validateChannel(channel, true)
		require.Error(t, err,
			"an empty upstream target is silently skipped at request time (model_mapped.go), so it must be rejected at write time")
		assert.Contains(t, err.Error(), "virtual-name",
			"the error must name the offending mapping key")

		noMapping := "{}"
		channel.ModelMapping = &noMapping
		assert.NoError(t, validateChannel(channel, true),
			"an explicit empty mapping means 'no mapping' and must stay valid")

		channel.ModelMapping = nil
		assert.NoError(t, validateChannel(channel, true),
			"an unset mapping must stay valid")
	})
}

// TestChannelSettingJSONRoundTrip pins the `setting` blob round trip for the
// pass-through switch, and records the silent-destruction behaviour of an
// unparsable blob.
func TestChannelSettingJSONRoundTrip(t *testing.T) {
	require.NoError(t, i18n.Init())
	channelLifecycleDB(t)

	t.Run("pass_through_body_enabled survives the round trip", func(t *testing.T) {
		setting := `{"pass_through_body_enabled":true}`
		channel := &model.Channel{
			Id:      9107,
			Type:    constant.ChannelTypeOpenAI,
			Key:     "lifecycle-setting-valid-key",
			Name:    "lifecycle-setting-valid",
			Status:  common.ChannelStatusEnabled,
			Group:   "lc-set-group",
			Models:  "lc-set-model",
			Setting: &setting,
		}
		require.NoError(t, validateChannel(channel, true))
		require.NoError(t, channel.Insert())

		stored, err := model.GetChannelById(channel.Id, true)
		require.NoError(t, err)
		require.NotNil(t, stored.Setting)
		assert.Equal(t, setting, *stored.Setting,
			"the setting blob must be persisted verbatim")

		parsed := stored.GetSetting()
		assert.True(t, parsed.PassThroughBodyEnabled,
			"GetSetting must expose pass_through_body_enabled=true to the relay")
	})

	t.Run("unparsable setting JSON is silently cleared and persisted", func(t *testing.T) {
		broken := `{"pass_through_body_enabled":`
		channel := &model.Channel{
			Id:      9108,
			Type:    constant.ChannelTypeOpenAI,
			Key:     "lifecycle-setting-broken-key",
			Name:    "lifecycle-setting-broken",
			Status:  common.ChannelStatusEnabled,
			Group:   "lc-set-broken",
			Models:  "lc-set-broken-model",
			Setting: &broken,
		}
		require.NoError(t, channel.Insert())

		stored, err := model.GetChannelById(channel.Id, true)
		require.NoError(t, err)
		require.NotNil(t, stored.Setting)
		require.Equal(t, broken, *stored.Setting, "the broken blob must start out stored verbatim")

		parsed := stored.GetSetting()

		assert.False(t, parsed.PassThroughBodyEnabled,
			"a failed parse yields the zero-value settings, i.e. pass-through silently OFF for that read")
		require.NotNil(t, stored.Setting,
			"a failed parse must not clear the in-memory field either")
		assert.Equal(t, broken, *stored.Setting,
			"the blob must be left exactly as the operator's data, only unreadable")

		// The destructive half: GetSetting used to write the cleared value back
		// to the row, so one read (any request reaching this channel) destroyed
		// the operator's saved settings permanently. Reading must never write.
		reloaded, err := model.GetChannelById(channel.Id, true)
		require.NoError(t, err)
		require.NotNil(t, reloaded.Setting,
			"reading a channel with an unparsable setting blob must not destroy it")
		assert.Equal(t, broken, *reloaded.Setting,
			"the stored setting must survive a failed parse unchanged")
	})
}

// TestChannelSettingValidationRejectsUnparsableBlob is the counterpart of the
// AS-IS case above: it shows the write path DOES reject unparsable setting JSON
// when it goes through validateChannel, which is exactly why the silent
// destruction in TestChannelSettingJSONRoundTrip is reachable only from rows
// written before validation existed (or by a direct model write).
func TestChannelSettingValidationRejectsUnparsableBlob(t *testing.T) {
	require.NoError(t, i18n.Init())

	broken := `{"pass_through_body_enabled":`
	channel := &model.Channel{
		Type:    constant.ChannelTypeOpenAI,
		Key:     "lifecycle-setting-validate-key",
		Name:    "lifecycle-setting-validate",
		Status:  common.ChannelStatusEnabled,
		Group:   "lc-set-validate",
		Models:  "lc-set-validate-model",
		Setting: &broken,
	}
	err := validateChannel(channel, true)
	require.Error(t, err,
		"validateChannel must reject unparsable channel setting JSON — it validates Setting but not ModelMapping")
	assert.Contains(t, err.Error(), "channel setting",
		"the rejection must name the channel setting blob as the offending field")
	assert.Equal(t, broken, fmt.Sprintf("%s", *channel.Setting),
		"a rejected channel must come back with its setting blob untouched (no silent clearing during validation)")
}

// TestEnsureChannelAbilitiesRepairsOnlyMissingRows pins the startup repair that
// replaced the unreachable panic-branch FixAbility.
//
// The ability table is the routing index (model/channel_cache.go), and the write
// paths keep it in sync non-atomically: Channel.Insert commits the channel row
// before AddAbilities, and EditChannelByTag swallows an UpdateAbilities failure.
// FixAbility only ran from InitChannelCache's recover branch, which the nil-map
// guard made unreachable, so without an incremental repair a channel that lost
// its rows would stay unroutable across every restart.
func TestEnsureChannelAbilitiesRepairsOnlyMissingRows(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := channelLifecycleDB(t)

	common.MemoryCacheEnabled = true

	const orphanModel = "lc-orphan-repair-model"
	orphan := &model.Channel{
		Id: 9111, Type: constant.ChannelTypeOpenAI, Key: "lifecycle-repair-key",
		Name: "lifecycle-repair-channel", Status: common.ChannelStatusEnabled,
		Group: "lc-repair-group", Models: orphanModel,
	}
	require.NoError(t, db.Create(orphan).Error)

	// A second, healthy channel must not be rebuilt — the repair is additive,
	// not the truncate-and-rebuild FixAbility does (which is unsafe to run
	// concurrently on a multi-node deployment).
	healthy := &model.Channel{
		Id: 9112, Type: constant.ChannelTypeOpenAI, Key: "lifecycle-healthy-key",
		Name: "lifecycle-healthy-channel", Status: common.ChannelStatusEnabled,
		Group: "lc-repair-group", Models: "lc-healthy-model",
	}
	require.NoError(t, healthy.Insert())
	require.NotEmpty(t, abilityModelGroup(t, db, healthy.Id))

	repaired, failed, err := model.EnsureChannelAbilities()
	require.NoError(t, err)
	assert.Zero(t, failed, "the repair must not fail on an ordinary channel")
	assert.Equal(t, 1, repaired, "exactly the ability-less channel must be repaired")

	require.NotEmpty(t, abilityModelGroup(t, db, orphan.Id),
		"the ability-less channel must gain the ability rows its models and groups imply")

	second, secondFailed, err := model.EnsureChannelAbilities()
	require.NoError(t, err)
	assert.Zero(t, secondFailed)
	assert.Zero(t, second, "the repair must be idempotent — a healthy database is left untouched")

	model.InitChannelCache()
	restored, err := model.GetRandomSatisfiedChannel("lc-repair-group", orphanModel, 0, nil)
	require.NoError(t, err)
	require.NotNil(t, restored,
		"after the repair the channel must route again without any manual 'fix abilities' click")
	assert.Equal(t, orphan.Id, restored.Id)
}

// TestInitChannelCacheSkipsDisabledOrphanChannels pins that the orphan report
// only concerns channels that are supposed to route: a disabled channel with no
// ability rows is ordinary state (disabling flips abilities.enabled off), not a
// routing hole.
func TestInitChannelCacheSkipsDisabledOrphanChannels(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := channelLifecycleDB(t)

	common.MemoryCacheEnabled = true

	disabled := &model.Channel{
		Id: 9113, Type: constant.ChannelTypeOpenAI, Key: "lifecycle-disabled-orphan-key",
		Name: "lifecycle-disabled-orphan", Status: common.ChannelStatusManuallyDisabled,
		Group: "lc-disabled-orphan-group", Models: "lc-disabled-orphan-model",
	}
	require.NoError(t, db.Create(disabled).Error)
	require.Empty(t, abilityModelGroup(t, db, disabled.Id),
		"precondition: the disabled channel genuinely has no ability rows")

	require.NotPanics(t, func() { model.InitChannelCache() },
		"a disabled channel without ability rows is normal state and must not trip the orphan report")

	selected, err := model.GetRandomSatisfiedChannel("lc-disabled-orphan-group", "lc-disabled-orphan-model", 0, nil)
	require.NoError(t, err)
	assert.Nil(t, selected, "a disabled channel must not be selectable")
}
