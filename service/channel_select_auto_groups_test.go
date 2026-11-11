package service

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupChannelSelectAutoGroupsTest(t *testing.T) *gorm.DB {
	t.Helper()

	originalDB := model.DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalRetryTimes := common.RetryTimes
	originalAutoGroups := setting.AutoGroups2JsonString()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalGroupRatios := ratio_setting.GroupRatio2JSONString()
	originalMaxTokenAutoGroups := setting.GetMaxTokenAutoGroups()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	model.DB = db
	common.MemoryCacheEnabled = true
	common.RetryTimes = 0

	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`[]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":2}`))
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("2"))

	t.Cleanup(func() {
		model.DB = originalDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		common.RetryTimes = originalRetryTimes
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroupRatios))
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprintf("%d", originalMaxTokenAutoGroups)))

		if originalMemoryCacheEnabled && originalDB != nil &&
			originalDB.Migrator().HasTable(&model.Channel{}) && originalDB.Migrator().HasTable(&model.Ability{}) {
			model.InitChannelCache()
		}
		sqlDB, err := db.DB()
		if err == nil {
			require.NoError(t, sqlDB.Close())
		}
	})

	return db
}

func createChannelSelectAutoGroupsChannel(t *testing.T, db *gorm.DB, id int, group, modelName string) {
	t.Helper()
	priority := int64(0)
	weight := uint(100)
	require.NoError(t, db.Create(&model.Channel{
		Id:       id,
		Type:     constant.ChannelTypeOpenAI,
		Key:      fmt.Sprintf("key-%d", id),
		Status:   common.ChannelStatusEnabled,
		Name:     fmt.Sprintf("channel-%d", id),
		Weight:   &weight,
		Models:   modelName,
		Group:    group,
		Priority: &priority,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     group,
		Model:     modelName,
		ChannelId: id,
		Enabled:   true,
		Priority:  &priority,
		Weight:    weight,
	}).Error)
}

func TestCacheGetRandomSatisfiedChannelUsesTokenAutoGroupsWhenGlobalAutoIsEmpty(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	const modelName = "auto-groups-runtime-model"
	createChannelSelectAutoGroupsChannel(t, db, 2101, "vip", modelName)
	createChannelSelectAutoGroupsChannel(t, db, 2102, "default", modelName)
	model.InitChannelCache()

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip", "default"})
	common.SetContextKey(ctx, constant.ContextKeyTokenCrossGroupRetry, true)

	retry := 0
	param := &RetryParam{
		Ctx:         ctx,
		TokenGroup:  "auto",
		ModelName:   modelName,
		RequestPath: "/v1/chat/completions",
		Retry:       &retry,
	}

	first, selectedGroup, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, 2101, first.Id)
	assert.Equal(t, "vip", selectedGroup)
	assert.Equal(t, "vip", common.GetContextKeyString(ctx, constant.ContextKeyAutoGroup))
	assert.Empty(t, setting.GetAutoGroups(), "the selection must not depend on the global Auto list")

	param.IncreaseRetry()
	second, selectedGroup, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, 2102, second.Id)
	assert.Equal(t, "default", selectedGroup)
	assert.Equal(t, "default", common.GetContextKeyString(ctx, constant.ContextKeyAutoGroup))
}

// TestCacheGetRandomSatisfiedChannelExcludesSaturatedChannels pins the
// per-request saturation exclusion funnel (BCD D-3 option A): with channel A
// recorded as saturated on the RetryParam, N consecutive selections must
// return only B — the weighted-random picker can never re-pick A within one
// client request. SetRetry(0)/ResetRetryNextTry (the auto-group cross-group
// resets) must not clear the set, and a fresh RetryParam — a new client
// request — starts empty so A is selectable again (T-02-15-05: the exclusion
// set never crosses requests).
func TestCacheGetRandomSatisfiedChannelExcludesSaturatedChannels(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	const modelName = "exclusion-funnel-model"
	createChannelSelectAutoGroupsChannel(t, db, 2201, "default", modelName)
	createChannelSelectAutoGroupsChannel(t, db, 2202, "default", modelName)
	model.InitChannelCache()

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")

	retry := 0
	param := &RetryParam{
		Ctx:         ctx,
		TokenGroup:  "default",
		ModelName:   modelName,
		RequestPath: "/v1/chat/completions",
		Retry:       &retry,
	}
	param.ExcludeSaturatedChannel(2201)
	assert.True(t, param.HasSaturatedChannels(), "the request must report that a saturation happened")
	assert.Equal(t, []int{2201}, param.SaturatedExclusions(), "the snapshot must expose the excluded id")

	for i := range 20 {
		chosen, _, err := CacheGetRandomSatisfiedChannel(param)
		require.NoError(t, err)
		require.NotNil(t, chosen, "selection %d must still find the non-excluded channel", i)
		assert.Equal(t, 2202, chosen.Id, "selection %d must never return the excluded saturated channel", i)
	}

	// The auto-group cross-group resets flip these two without touching the set.
	param.SetRetry(0)
	param.ResetRetryNextTry()
	param.IncreaseRetry()
	afterReset, _, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.NotNil(t, afterReset, "selection after the cross-group reset must still find channel 2202")
	assert.Equal(t, 2202, afterReset.Id, "the cross-group reset must not revive the excluded channel")
	assert.Equal(t, []int{2201}, param.SaturatedExclusions(),
		"SetRetry(0)/ResetRetryNextTry must not clear the per-request exclusion set")

	// A fresh RetryParam models the next client request: empty by construction.
	fresh := &RetryParam{
		Ctx:         ctx,
		TokenGroup:  "default",
		ModelName:   modelName,
		RequestPath: "/v1/chat/completions",
		Retry:       common.GetPointer(0),
	}
	assert.False(t, fresh.HasSaturatedChannels(), "a new request starts without any exclusion")
	assert.Nil(t, fresh.SaturatedExclusions(), "a new request starts without any exclusion")
}

// TestCacheGetRandomSatisfiedChannelExclusionSurvivesGroupAdvance pins the
// exclusion set across auto-group progression: channel A serves both auto
// groups (vip, default) while B serves only default. With A saturated, the vip
// group's in-group empty selection must advance to the default group and
// return B — the excluded A must not revive when SetRetry(0) resets the retry
// counter for the next group (saturation does not reset when the group
// changes), and no selection during the whole progression may return A.
func TestCacheGetRandomSatisfiedChannelExclusionSurvivesGroupAdvance(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	const modelName = "exclusion-auto-model"
	// Channel A spans both auto groups (vip + default); B lives in default only.
	priority := int64(0)
	weightA := uint(100)
	require.NoError(t, db.Create(&model.Channel{
		Id:       2301,
		Type:     constant.ChannelTypeOpenAI,
		Key:      "key-2301",
		Status:   common.ChannelStatusEnabled,
		Name:     "channel-2301",
		Weight:   &weightA,
		Models:   modelName,
		Group:    "vip,default",
		Priority: &priority,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "vip", Model: modelName, ChannelId: 2301, Enabled: true, Priority: &priority, Weight: weightA,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "default", Model: modelName, ChannelId: 2301, Enabled: true, Priority: &priority, Weight: weightA,
	}).Error)
	createChannelSelectAutoGroupsChannel(t, db, 2302, "default", modelName)
	model.InitChannelCache()

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip", "default"})
	common.SetContextKey(ctx, constant.ContextKeyTokenCrossGroupRetry, true)

	retry := 0
	param := &RetryParam{
		Ctx:         ctx,
		TokenGroup:  "auto",
		ModelName:   modelName,
		RequestPath: "/v1/chat/completions",
		Retry:       &retry,
	}
	param.ExcludeSaturatedChannel(2301)

	// Walk the groups: vip empties out (A excluded) → advance to default →
	// B. Every non-nil selection across the whole progression must be B.
	seen := make([]int, 0, 4)
	for range 4 {
		chosen, _, err := CacheGetRandomSatisfiedChannel(param)
		require.NoError(t, err)
		if chosen == nil {
			break
		}
		seen = append(seen, chosen.Id)
	}
	require.NotEmpty(t, seen, "the default group must yield channel B after vip empties out")
	for i, id := range seen {
		assert.Equal(t, 2302, id, "selection %d across the group progression must return B, never the excluded A", i)
	}
	assert.Equal(t, []int{2301}, param.SaturatedExclusions(),
		"the cross-group SetRetry(0) reset must not clear the per-request exclusion set")
	assert.True(t, param.HasSaturatedChannels())
}
