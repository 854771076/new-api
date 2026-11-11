package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMain(m *testing.M) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		panic("failed to open test db: " + err.Error())
	}
	sqlDB, err := db.DB()
	if err != nil {
		panic("failed to get sql.DB: " + err.Error())
	}
	sqlDB.SetMaxOpenConns(1)

	model.DB = db
	model.LOG_DB = db

	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = true
	if err := i18n.Init(); err != nil {
		panic("failed to load locales: " + err.Error())
	}

	if err := db.AutoMigrate(
		&model.Task{},
		&model.User{},
		&model.Token{},
		&model.Log{},
		&model.Channel{},
		&model.Midjourney{},
		&model.TopUp{},
		&model.UserSubscription{},
		&model.UserAppWallet{},
		&model.SystemTask{},
		&model.SystemTaskLock{},
	); err != nil {
		panic("failed to migrate: " + err.Error())
	}

	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// Seed helpers
// ---------------------------------------------------------------------------

func truncate(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM tasks")
		model.DB.Exec("DELETE FROM users")
		model.DB.Exec("DELETE FROM tokens")
		model.DB.Exec("DELETE FROM logs")
		model.DB.Exec("DELETE FROM channels")
		model.DB.Exec("DELETE FROM midjourneys")
		model.DB.Exec("DELETE FROM top_ups")
		model.DB.Exec("DELETE FROM user_subscriptions")
		model.DB.Exec("DELETE FROM user_app_wallets")
		model.DB.Exec("DELETE FROM system_task_locks")
		model.DB.Exec("DELETE FROM system_tasks")
	})
}

func seedUser(t *testing.T, id int, quota int) {
	t.Helper()
	user := &model.User{Id: id, Username: "test_user", Quota: quota, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(user).Error)
}

func seedToken(t *testing.T, id int, userId int, key string, remainQuota int) {
	t.Helper()
	token := &model.Token{
		Id:          id,
		UserId:      userId,
		Key:         key,
		Name:        "test_token",
		Status:      common.TokenStatusEnabled,
		RemainQuota: remainQuota,
		UsedQuota:   0,
	}
	require.NoError(t, model.DB.Create(token).Error)
}

func seedSubscription(t *testing.T, id int, userId int, amountTotal int64, amountUsed int64) {
	t.Helper()
	sub := &model.UserSubscription{
		Id:          id,
		UserId:      userId,
		AmountTotal: amountTotal,
		AmountUsed:  amountUsed,
		Status:      "active",
		StartTime:   time.Now().Unix(),
		EndTime:     time.Now().Add(30 * 24 * time.Hour).Unix(),
	}
	require.NoError(t, model.DB.Create(sub).Error)
}

func seedChannel(t *testing.T, id int) {
	t.Helper()
	ch := &model.Channel{Id: id, Name: "test_channel", Key: "sk-test", Status: common.ChannelStatusEnabled}
	require.NoError(t, model.DB.Create(ch).Error)
}

func seedChargedAccounting(t *testing.T, userID, channelID, tokenID, quota, requestCount int) {
	t.Helper()
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"used_quota":    quota,
		"request_count": requestCount,
	}).Error)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channelID).
		Update("used_quota", quota).Error)
	if tokenID > 0 {
		require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", tokenID).
			Update("used_quota", quota).Error)
	}
}

func seedAppWallet(t *testing.T, userId, appId, quota, usedQuota int) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.UserAppWallet{UserId: userId, AppId: appId, Quota: quota, UsedQuota: usedQuota}).Error)
}

func getAppWallet(t *testing.T, userId, appId int) model.UserAppWallet {
	t.Helper()
	var wallet model.UserAppWallet
	require.NoError(t, model.DB.Where("user_id = ? AND app_id = ?", userId, appId).First(&wallet).Error)
	return wallet
}

func makeTask(userId, channelId, quota, tokenId int, billingSource string, subscriptionId int, appIds ...int) *model.Task {
	appId := 0
	if len(appIds) > 0 {
		appId = appIds[0]
	}
	return &model.Task{
		TaskID:    "task_" + time.Now().Format("150405.000"),
		UserId:    userId,
		AppId:     appId,
		ChannelId: channelId,
		Quota:     quota,
		Status:    model.TaskStatus(model.TaskStatusInProgress),
		Group:     "default",
		Data:      json.RawMessage(`{}`),
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
		Properties: model.Properties{
			OriginModelName: "test-model",
		},
		PrivateData: model.TaskPrivateData{
			BillingSource:  billingSource,
			SubscriptionId: subscriptionId,
			TokenId:        tokenId,
			BillingContext: &model.TaskBillingContext{
				ModelPrice:      0.02,
				GroupRatio:      1.0,
				OriginModelName: "test-model",
			},
		},
	}
}

func TestPriceDataOtherRatiosFilterAndSnapshot(t *testing.T) {
	priceData := types.PriceData{}

	priceData.AddOtherRatio("zero", 0)
	priceData.AddOtherRatio("negative", -0.5)
	priceData.AddOtherRatio("nan", math.NaN())
	priceData.AddOtherRatio("inf", math.Inf(1))
	priceData.AddOtherRatio("one", 1)
	priceData.AddOtherRatio("positive", 2.5)

	ratios := priceData.OtherRatios()
	require.Len(t, ratios, 2)
	assert.Equal(t, 1.0, ratios["one"])
	assert.Equal(t, 2.5, ratios["positive"])
	assert.True(t, priceData.HasOtherRatio("one"))
	assert.False(t, priceData.HasOtherRatio("zero"))

	ratios["positive"] = 99
	ratios["new"] = 3
	nextSnapshot := priceData.OtherRatios()
	assert.Equal(t, 2.5, nextSnapshot["positive"])
	assert.NotContains(t, nextSnapshot, "new")
}

func TestPriceDataReplaceAndApplyOtherRatios(t *testing.T) {
	priceData := types.PriceData{}

	replaced := priceData.ReplaceOtherRatios(map[string]float64{
		"zero":     0,
		"negative": -3,
		"nan":      math.NaN(),
		"inf":      math.Inf(1),
		"one":      1,
		"duration": 2,
		"size":     1.5,
	})

	require.True(t, replaced)
	assert.Equal(t, 3.0, priceData.OtherRatioMultiplier())
	assert.Equal(t, 30.0, priceData.ApplyOtherRatiosToFloat(10))
	assert.Equal(t, 10.0, priceData.RemoveOtherRatiosFromFloat(30))
	assert.True(t, decimal.NewFromInt(30).Equal(priceData.ApplyOtherRatiosToDecimal(decimal.NewFromInt(10))))

	replaced = priceData.ReplaceOtherRatios(map[string]float64{
		"zero": 0,
		"nan":  math.NaN(),
	})

	require.False(t, replaced)
	assert.Nil(t, priceData.OtherRatios())
	assert.Equal(t, 1.0, priceData.OtherRatioMultiplier())
}

func TestTaskBillingOtherFiltersHistoricalOtherRatios(t *testing.T) {
	task := makeTask(1, 1, 100, 0, BillingSourceWallet, 0)
	task.PrivateData.BillingContext.OtherRatios = map[string]float64{
		"seconds":  2,
		"identity": 1,
		"zero":     0,
		"negative": -1,
		"nan":      math.NaN(),
		"inf":      math.Inf(1),
	}

	other := taskBillingOther(task).Snapshot()

	assert.Equal(t, 2.0, other["seconds"])
	assert.Equal(t, 1.0, other["identity"])
	assert.NotContains(t, other, "zero")
	assert.NotContains(t, other, "negative")
	assert.NotContains(t, other, "nan")
	assert.NotContains(t, other, "inf")
	assert.NotContains(t, other, "billing_mode")
	assert.NotContains(t, other, "expr_b64")
	assert.NotContains(t, other, "matched_tier")
	assert.NotContains(t, other, "usage_facts")
}

func TestTaskBillingOtherIncludesTieredSnapshotAndKeepsUsageFactsNested(t *testing.T) {
	task := makeTask(1, 1, 100, 0, BillingSourceWallet, 0)
	expression := `tier("720P", u("seconds") * 5)`
	task.PrivateData.BillingContext.TieredSnapshot = &billingexpr.BillingSnapshot{
		ExprString:    expression,
		EstimatedTier: "720P",
		UsageFacts: map[string]any{
			"resolution": "720P",
			"seconds":    5,
		},
	}

	other := taskBillingOther(task).Snapshot()

	assert.Equal(t, "tiered_expr", other["billing_mode"])
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte(expression)), other["expr_b64"])
	assert.Equal(t, "720P", other["matched_tier"])
	facts, ok := other["usage_facts"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{
		"resolution": "720P",
		"seconds":    5,
	}, facts)
	assert.NotContains(t, other, "resolution")
	assert.NotContains(t, other, "seconds")
}

func TestTaskBillingOtherOmitsEmptyUsageFacts(t *testing.T) {
	task := makeTask(1, 1, 100, 0, BillingSourceWallet, 0)
	expression := `tier("base", 1)`
	task.PrivateData.BillingContext.TieredSnapshot = &billingexpr.BillingSnapshot{
		ExprString:    expression,
		EstimatedTier: "base",
		UsageFacts:    map[string]any{},
	}

	other := taskBillingOther(task).Snapshot()

	assert.Equal(t, "tiered_expr", other["billing_mode"])
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte(expression)), other["expr_b64"])
	assert.Equal(t, "base", other["matched_tier"])
	assert.NotContains(t, other, "usage_facts")
}

func callLogTaskConsumption(t *testing.T, info *relaycommon.RelayInfo, task *model.Task) *model.Log {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	ctx.Set("token_name", "test_token")
	LogTaskConsumption(ctx, info, task)
	log := getLastLog(t)
	require.NotNil(t, log)
	return log
}

func TestLogTaskConsumptionIncludesTieredSnapshotUsageFacts(t *testing.T) {
	truncate(t)
	const userID, channelID = 40, 40
	seedUser(t, userID, 10_000)
	seedChannel(t, channelID)

	expression := `tier("720P", u("seconds") * 5)`
	task := makeTask(userID, channelID, 100, 0, BillingSourceWallet, 0)
	info := &relaycommon.RelayInfo{
		UserId:          userID,
		TokenId:         0,
		OriginModelName: "wan2.5-i2v-preview",
		UsingGroup:      "default",
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channelID},
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{Action: "GENERATE"},
		PriceData: types.PriceData{
			ModelPrice:     0.02,
			Quota:          100,
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
		},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			ExprString:    expression,
			EstimatedTier: "720P",
			UsageFacts: map[string]any{
				"resolution": "720P",
				"seconds":    5,
			},
		},
	}

	log := callLogTaskConsumption(t, info, task)

	var other map[string]any
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
	assert.Equal(t, "tiered_expr", other["billing_mode"])
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte(expression)), other["expr_b64"])
	assert.Equal(t, "720P", other["matched_tier"])
	facts, ok := other["usage_facts"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "720P", facts["resolution"])
	assert.Equal(t, float64(5), facts["seconds"])
	assert.NotContains(t, other, "resolution")
	assert.NotContains(t, other, "seconds")
	assert.Contains(t, log.Content, "Billing parameters: ")
	assert.Contains(t, log.Content, "resolution: 720P")
	assert.Contains(t, log.Content, "seconds: 5")
}

func TestLogTaskConsumptionWithoutSnapshotKeepsRatioMode(t *testing.T) {
	truncate(t)
	const userID, channelID = 41, 41
	seedUser(t, userID, 10_000)
	seedChannel(t, channelID)

	priceData := types.PriceData{
		ModelPrice:     0.02,
		Quota:          100,
		GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
	}
	priceData.AddOtherRatio("size", 2)
	task := makeTask(userID, channelID, 100, 0, BillingSourceWallet, 0)
	info := &relaycommon.RelayInfo{
		UserId:          userID,
		TokenId:         0,
		OriginModelName: "test-model",
		UsingGroup:      "default",
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channelID},
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{Action: "GENERATE"},
		PriceData:       priceData,
	}

	log := callLogTaskConsumption(t, info, task)

	var other map[string]any
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
	assert.Equal(t, true, other["is_task"])
	assert.Equal(t, "/v1/videos", other["request_path"])
	assert.NotContains(t, other, "billing_mode")
	assert.NotContains(t, other, "expr_b64")
	assert.NotContains(t, other, "matched_tier")
	assert.NotContains(t, other, "usage_facts")
	assert.Equal(t, "Action GENERATE, Billing parameters: size: 2.00", log.Content)
	// The web console renders content_parts in the viewer's language; content
	// keeps the English text.
	assert.Equal(t, []any{
		map[string]any{"key": "Action {{action}}", "params": map[string]any{"action": "GENERATE"}},
		map[string]any{"key": "Billing parameters: {{params}}", "params": map[string]any{"params": "size: 2.00"}},
	}, other["content_parts"])
}

// Task logs distinguish jobs the client polls from requests whose HTTP call
// returned the deliverable itself, and flag results the gateway did not keep.
func TestLogTaskConsumptionMarksInlineResultsAndDiscardedArtifacts(t *testing.T) {
	for _, tc := range []struct {
		name               string
		status             model.TaskStatus
		discarded          bool
		pinnedProtocol     string
		wantSync, wantKept bool
	}{
		{"asynchronous job", model.TaskStatusNotStart, false, "", false, true},
		{"immediate result on a discarding route", model.TaskStatusSuccess, true, "", true, false},
		{"openai image request waits for an asynchronous upstream task", model.TaskStatusNotStart, false, jsplugin.ProtocolOpenAIImage, true, true},
		{"immediate failure", model.TaskStatusFailure, false, "", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			truncate(t)
			const userID, channelID = 43, 43
			seedUser(t, userID, 10_000)
			seedChannel(t, channelID)
			task := makeTask(userID, channelID, 100, 0, BillingSourceWallet, 0)
			task.Status = tc.status
			task.PrivateData.ResultDiscarded = tc.discarded
			info := &relaycommon.RelayInfo{
				UserId: userID, OriginModelName: "qwen-image-plus", UsingGroup: "default",
				ChannelMeta:   &relaycommon.ChannelMeta{ChannelId: channelID},
				TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: "text_to_image"},
				PriceData:     types.PriceData{ModelPrice: 0.03, Quota: 100, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}},
			}
			gin.SetMode(gin.TestMode)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
			ctx.Set("token_name", "test_token")
			if tc.pinnedProtocol != "" {
				ctx.Set(jsplugin.ContextKeyPinnedEndpoint, jsplugin.PinnedEndpoint{Protocol: tc.pinnedProtocol})
			}
			LogTaskConsumption(ctx, info, task)
			log := getLastLog(t)
			require.NotNil(t, log)
			var other map[string]any
			require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
			assert.Equal(t, true, other["is_task"])
			if tc.wantSync {
				assert.Equal(t, true, other["task_sync"])
			} else {
				assert.NotContains(t, other, "task_sync")
			}
			if tc.wantKept {
				assert.NotContains(t, other, "result_discarded")
			} else {
				assert.Equal(t, true, other["result_discarded"])
			}
		})
	}
}

func TestTaskBillingOtherSeparatesPluginAndRootDiagnostics(t *testing.T) {
	task := makeTask(1, 1, 100, 0, BillingSourceWallet, 0)
	task.TaskID = "task_public"
	task.PrivateData.UpstreamTaskID = "upstream-private"
	task.PrivateData.NodeName = "node-a"
	task.PrivateData.Execution = &model.TaskExecutionSnapshot{
		TaskPlugin: &model.TaskPluginSnapshot{
			Key:     "document-parser",
			Name:    "Document Parser",
			Version: "1.2.3",
			Author: &model.TaskPluginAuthorSnapshot{
				Name: "Community Author",
				URL:  "https://plugins.example/author",
			},
			APIVersion: 1,
			Generation: 42,
		},
	}

	other := taskBillingOther(task).Snapshot()

	assert.Equal(t, "task_public", other["task_id"])
	adminInfo, ok := other["admin_info"].(map[string]any)
	require.True(t, ok)
	pluginInfo, ok := adminInfo["task_plugin"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "document-parser", pluginInfo["key"])
	assert.Equal(t, "1.2.3", pluginInfo["version"])
	assert.Equal(t, map[string]any{
		"name": "Community Author",
		"url":  "https://plugins.example/author",
	}, pluginInfo["author"])

	rootInfo, ok := other["root_info"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "upstream-private", rootInfo["upstream_task_id"])
	assert.Equal(t, "node-a", rootInfo["node_name"])
	runtimeInfo, ok := rootInfo["task_plugin"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, uint64(42), runtimeInfo["generation"])
	assert.NotContains(t, runtimeInfo, "author")
}

func TestTaskBillingContextPriceDataFiltersMultiplier(t *testing.T) {
	priceData := taskBillingContextPriceData(&model.TaskBillingContext{
		OtherRatios: map[string]float64{
			"seconds":  2,
			"size":     3,
			"identity": 1,
			"zero":     0,
			"negative": -1,
			"nan":      math.NaN(),
			"inf":      math.Inf(1),
		},
	})

	require.NotNil(t, priceData)
	assert.Equal(t, 6.0, priceData.OtherRatioMultiplier())
	assert.Equal(t, map[string]float64{
		"seconds":  2,
		"size":     3,
		"identity": 1,
	}, priceData.OtherRatios())
}

// ---------------------------------------------------------------------------
// Read-back helpers
// ---------------------------------------------------------------------------

func getUserQuota(t *testing.T, id int) int {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.Select("quota").Where("id = ?", id).First(&user).Error)
	return user.Quota
}

func getUserUsageAccounting(t *testing.T, id int) (int, int) {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.Select("used_quota", "request_count").Where("id = ?", id).First(&user).Error)
	return user.UsedQuota, user.RequestCount
}

func getChannelUsedQuota(t *testing.T, id int) int64 {
	t.Helper()
	var channel model.Channel
	require.NoError(t, model.DB.Select("used_quota").Where("id = ?", id).First(&channel).Error)
	return channel.UsedQuota
}

func getTokenRemainQuota(t *testing.T, id int) int {
	t.Helper()
	var token model.Token
	require.NoError(t, model.DB.Select("remain_quota").Where("id = ?", id).First(&token).Error)
	return token.RemainQuota
}

func getTokenUsedQuota(t *testing.T, id int) int {
	t.Helper()
	var token model.Token
	require.NoError(t, model.DB.Select("used_quota").Where("id = ?", id).First(&token).Error)
	return token.UsedQuota
}

func getSubscriptionUsed(t *testing.T, id int) int64 {
	t.Helper()
	var sub model.UserSubscription
	require.NoError(t, model.DB.Select("amount_used").Where("id = ?", id).First(&sub).Error)
	return sub.AmountUsed
}

func getTaskQuota(t *testing.T, id int64) int {
	t.Helper()
	var task model.Task
	require.NoError(t, model.DB.Select("quota").Where("id = ?", id).First(&task).Error)
	return task.Quota
}

func getMidjourneyTask(t *testing.T, id int) model.Midjourney {
	t.Helper()
	var task model.Midjourney
	require.NoError(t, model.DB.First(&task, id).Error)
	return task
}

func getLastLog(t *testing.T) *model.Log {
	t.Helper()
	var log model.Log
	err := model.LOG_DB.Order("id desc").First(&log).Error
	if err != nil {
		return nil
	}
	return &log
}

func countLogs(t *testing.T) int64 {
	t.Helper()
	var count int64
	model.LOG_DB.Model(&model.Log{}).Count(&count)
	return count
}

// ===========================================================================
// Legacy Midjourney billing tests
// ===========================================================================

func TestPrepareMidjourneyTaskBillingKeepsUnbilledMarkerClear(t *testing.T) {
	task := &model.Midjourney{Quota: 900, TokenId: 7, BillingChannelId: 8}

	prepared, err := PrepareMidjourneyTaskBilling(&relaycommon.RelayInfo{}, task, 900, false)

	require.NoError(t, err)
	assert.False(t, prepared)
	assert.Zero(t, task.Quota)
	assert.Zero(t, task.TokenId)
	assert.Zero(t, task.BillingChannelId)
}

func TestSettleMidjourneyTaskBillingRequiresPersistedTask(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID = 49, 49, 49
	const initialUserQuota, initialTokenQuota, chargedQuota = 10000, 5000, 3000
	seedUser(t, userID, initialUserQuota)
	seedToken(t, tokenID, userID, "sk-midjourney-unpersisted", initialTokenQuota)
	seedChannel(t, channelID)

	relayInfo := &relaycommon.RelayInfo{
		UserId:    userID,
		TokenId:   tokenID,
		TokenKey:  "sk-midjourney-unpersisted",
		UserQuota: initialUserQuota,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId: channelID,
		},
	}
	task := &model.Midjourney{UserId: userID, ChannelId: channelID}
	prepared, err := PrepareMidjourneyTaskBilling(relayInfo, task, chargedQuota, true)
	require.NoError(t, err)
	require.True(t, prepared)

	billed, err := SettleMidjourneyTaskBilling(context.Background(), relayInfo, task, prepared)

	require.Error(t, err)
	assert.False(t, billed)
	assert.Equal(t, initialUserQuota, getUserQuota(t, userID))
	assert.Equal(t, initialTokenQuota, getTokenRemainQuota(t, tokenID))
}

func TestMidjourneyRefundRestoresEveryAccountingElementOnBillingChannel(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, billingChannelID, executionChannelID = 50, 50, 50, 51
	const initialUserQuota, initialTokenQuota, chargedQuota = 10000, 5000, 3000
	seedUser(t, userID, initialUserQuota)
	seedToken(t, tokenID, userID, "sk-midjourney", initialTokenQuota)
	seedChannel(t, billingChannelID)
	seedChannel(t, executionChannelID)

	relayInfo := &relaycommon.RelayInfo{
		UserId:     userID,
		TokenId:    tokenID,
		TokenKey:   "sk-midjourney",
		UserQuota:  initialUserQuota,
		UsingGroup: "default",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId: billingChannelID,
		},
	}
	task := &model.Midjourney{
		UserId:    userID,
		Action:    "IMAGINE",
		MjId:      "mj-accounting-refund",
		ChannelId: executionChannelID,
		Progress:  "0%",
	}

	prepared, err := PrepareMidjourneyTaskBilling(relayInfo, task, chargedQuota, true)
	require.NoError(t, err)
	require.True(t, prepared)
	assert.Equal(t, chargedQuota, task.Quota)
	assert.Zero(t, task.TokenId)
	assert.Equal(t, billingChannelID, task.BillingChannelId)
	require.NoError(t, task.Insert())

	billed, err := SettleMidjourneyTaskBilling(context.Background(), relayInfo, task, prepared)
	require.NoError(t, err)
	require.True(t, billed)
	assert.Equal(t, initialUserQuota-chargedQuota, getUserQuota(t, userID))
	assert.Equal(t, initialTokenQuota-chargedQuota, getTokenRemainQuota(t, tokenID))
	persisted := getMidjourneyTask(t, task.Id)
	assert.Equal(t, chargedQuota, persisted.Quota)
	assert.Equal(t, tokenID, persisted.TokenId)
	assert.Equal(t, billingChannelID, persisted.BillingChannelId)

	seedChargedAccounting(t, userID, billingChannelID, tokenID, chargedQuota, 1)

	assert.True(t, RefundMidjourneyQuota(ctx, task, "Composition failed"))
	assert.Equal(t, initialUserQuota, getUserQuota(t, userID))
	assert.Equal(t, initialTokenQuota, getTokenRemainQuota(t, tokenID))
	assert.Zero(t, getTokenUsedQuota(t, tokenID))
	usedQuota, requestCount := getUserUsageAccounting(t, userID)
	assert.Zero(t, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Zero(t, getChannelUsedQuota(t, billingChannelID))
	assert.Zero(t, getChannelUsedQuota(t, executionChannelID))

	persisted = getMidjourneyTask(t, task.Id)
	assert.Zero(t, persisted.Quota)
	assert.Equal(t, tokenID, persisted.TokenId)
	assert.Equal(t, billingChannelID, persisted.BillingChannelId)
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Equal(t, chargedQuota, log.Quota)
	assert.Equal(t, tokenID, log.TokenId)
	assert.Equal(t, billingChannelID, log.ChannelId)

	assert.True(t, RefundMidjourneyQuota(ctx, task, "duplicate poll"))
	assert.Equal(t, int64(1), countLogs(t))
}

func TestMidjourneyAppBillingSettlesAndRefundsAppWallet(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID, appID = 55, 55, 55, 45
	const platformQuota, appQuota, tokenQuota, chargedQuota = 10000, 10000, 5000, 3000
	seedUser(t, userID, platformQuota)
	seedToken(t, tokenID, userID, "sk-midjourney-app", tokenQuota)
	seedChannel(t, channelID)
	seedAppWallet(t, userID, appID, appQuota, 0)

	relayInfo := &relaycommon.RelayInfo{
		UserId:    userID,
		AppId:     appID,
		TokenId:   tokenID,
		TokenKey:  "sk-midjourney-app",
		UserQuota: appQuota,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId: channelID,
		},
	}
	task := &model.Midjourney{UserId: userID, Action: "IMAGINE", MjId: "mj-app-accounting", ChannelId: channelID, Progress: "0%"}

	prepared, err := PrepareMidjourneyTaskBilling(relayInfo, task, chargedQuota, true)
	require.NoError(t, err)
	require.True(t, prepared)
	assert.Equal(t, appID, task.AppId)
	require.NoError(t, task.Insert())

	billed, err := SettleMidjourneyTaskBilling(context.Background(), relayInfo, task, prepared)
	require.NoError(t, err)
	require.True(t, billed)

	wallet := getAppWallet(t, userID, appID)
	assert.Equal(t, appQuota-chargedQuota, wallet.Quota, "app pool is charged")
	assert.Equal(t, chargedQuota, wallet.UsedQuota, "app usage is recorded at settle")
	assert.Equal(t, platformQuota, getUserQuota(t, userID), "platform wallet is untouched")
	assert.Equal(t, tokenQuota-chargedQuota, getTokenRemainQuota(t, tokenID))
	persisted := getMidjourneyTask(t, task.Id)
	assert.Equal(t, appID, persisted.AppId)

	seedChargedAccounting(t, userID, channelID, tokenID, chargedQuota, 1)

	assert.True(t, RefundMidjourneyQuota(ctx, task, "Composition failed"))
	wallet = getAppWallet(t, userID, appID)
	assert.Equal(t, appQuota, wallet.Quota, "app pool is refunded")
	assert.Zero(t, wallet.UsedQuota)
	assert.Equal(t, platformQuota, getUserQuota(t, userID), "platform wallet is untouched")
	assert.Equal(t, tokenQuota, getTokenRemainQuota(t, tokenID))
	usedQuota, requestCount := getUserUsageAccounting(t, userID)
	assert.Zero(t, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Zero(t, getChannelUsedQuota(t, channelID))
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Equal(t, chargedQuota, log.Quota)
}

func TestSettleMidjourneyTaskBillingFundingFailureClearsMarkers(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID = 52, 52, 52
	const initialUserQuota, initialTokenQuota, chargedQuota = 10000, 5000, 3000
	seedUser(t, userID, initialUserQuota)
	seedToken(t, tokenID, userID, "sk-midjourney-funding-failure", initialTokenQuota)
	seedChannel(t, channelID)

	relayInfo := &relaycommon.RelayInfo{
		UserId:    userID,
		TokenId:   tokenID,
		TokenKey:  "sk-midjourney-funding-failure",
		UserQuota: initialUserQuota,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId: channelID,
		},
	}
	task := &model.Midjourney{UserId: userID, MjId: "mj-funding-failure", ChannelId: channelID}
	prepared, err := PrepareMidjourneyTaskBilling(relayInfo, task, chargedQuota, true)
	require.NoError(t, err)
	require.True(t, prepared)
	require.NoError(t, task.Insert())

	require.NoError(t, model.DB.Exec(`
		CREATE TRIGGER fail_midjourney_user_update
		BEFORE UPDATE ON users
		WHEN OLD.id = 52
		BEGIN
			SELECT RAISE(ABORT, 'forced user quota failure');
		END;
	`).Error)
	t.Cleanup(func() {
		model.DB.Exec("DROP TRIGGER IF EXISTS fail_midjourney_user_update")
	})

	billed, err := SettleMidjourneyTaskBilling(context.Background(), relayInfo, task, prepared)

	require.Error(t, err)
	assert.False(t, billed)
	assert.Equal(t, initialUserQuota, getUserQuota(t, userID))
	assert.Equal(t, initialTokenQuota, getTokenRemainQuota(t, tokenID))
	persisted := getMidjourneyTask(t, task.Id)
	assert.Zero(t, persisted.Quota)
	assert.Zero(t, persisted.TokenId)
	assert.Zero(t, persisted.BillingChannelId)
	usedQuota, requestCount := getUserUsageAccounting(t, userID)
	assert.Zero(t, usedQuota)
	assert.Zero(t, requestCount)
	assert.Zero(t, getChannelUsedQuota(t, channelID))
	assert.Zero(t, countLogs(t))
}

func TestSettleMidjourneyTaskBillingTokenFailureKeepsFundingRefundable(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 53, 53, 53
	const initialUserQuota, initialTokenQuota, chargedQuota = 10000, 5000, 3000
	seedUser(t, userID, initialUserQuota)
	seedToken(t, tokenID, userID, "sk-midjourney-token-failure", initialTokenQuota)
	seedChannel(t, channelID)

	relayInfo := &relaycommon.RelayInfo{
		UserId:    userID,
		TokenId:   tokenID,
		TokenKey:  "sk-midjourney-token-failure",
		UserQuota: initialUserQuota,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId: channelID,
		},
	}
	task := &model.Midjourney{UserId: userID, MjId: "mj-token-failure", ChannelId: channelID}
	prepared, err := PrepareMidjourneyTaskBilling(relayInfo, task, chargedQuota, true)
	require.NoError(t, err)
	require.True(t, prepared)
	require.NoError(t, task.Insert())

	require.NoError(t, model.DB.Exec(`
		CREATE TRIGGER fail_midjourney_token_update
		BEFORE UPDATE ON tokens
		WHEN OLD.id = 53
		BEGIN
			SELECT RAISE(ABORT, 'forced token quota failure');
		END;
	`).Error)
	t.Cleanup(func() {
		model.DB.Exec("DROP TRIGGER IF EXISTS fail_midjourney_token_update")
	})

	billed, err := SettleMidjourneyTaskBilling(context.Background(), relayInfo, task, prepared)

	require.Error(t, err)
	require.True(t, billed)
	assert.Equal(t, initialUserQuota-chargedQuota, getUserQuota(t, userID))
	assert.Equal(t, initialTokenQuota, getTokenRemainQuota(t, tokenID))
	assert.Zero(t, getTokenUsedQuota(t, tokenID))
	persisted := getMidjourneyTask(t, task.Id)
	assert.Equal(t, chargedQuota, persisted.Quota)
	assert.Zero(t, persisted.TokenId)
	assert.Equal(t, channelID, persisted.BillingChannelId)

	seedChargedAccounting(t, userID, channelID, 0, chargedQuota, 1)
	assert.True(t, RefundMidjourneyQuota(ctx, task, "token settlement failed"))
	assert.Equal(t, initialUserQuota, getUserQuota(t, userID))
	assert.Equal(t, initialTokenQuota, getTokenRemainQuota(t, tokenID))
	usedQuota, requestCount := getUserUsageAccounting(t, userID)
	assert.Zero(t, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Zero(t, getChannelUsedQuota(t, channelID))
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Zero(t, log.TokenId)
}

func TestPrepareMidjourneyTaskBillingRejectsSubscriptionBeforeCharge(t *testing.T) {
	task := &model.Midjourney{Quota: 900, TokenId: 7, BillingChannelId: 8}
	relayInfo := &relaycommon.RelayInfo{BillingSource: BillingSourceSubscription, SubscriptionId: 1}

	prepared, err := PrepareMidjourneyTaskBilling(relayInfo, task, 900, true)

	require.Error(t, err)
	assert.False(t, prepared)
	assert.Zero(t, task.Quota)
	assert.Zero(t, task.TokenId)
	assert.Zero(t, task.BillingChannelId)
}

func TestRefundMidjourneyQuotaUsesLegacyChannelFallbackWithoutTokenAdjustment(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 54, 54, 54
	const walletAfterCharge, tokenQuota, chargedQuota = 7000, 5000, 3000
	seedUser(t, userID, walletAfterCharge)
	seedToken(t, tokenID, userID, "sk-midjourney-legacy", tokenQuota)
	seedChannel(t, channelID)
	seedChargedAccounting(t, userID, channelID, 0, chargedQuota, 1)
	task := &model.Midjourney{
		UserId:    userID,
		MjId:      "mj-legacy-fallback",
		Action:    "IMAGINE",
		ChannelId: channelID,
		Quota:     chargedQuota,
		TokenId:   0,
		Progress:  "0%",
	}
	require.NoError(t, task.Insert())

	assert.True(t, RefundMidjourneyQuota(ctx, task, "legacy failure"))

	assert.Equal(t, walletAfterCharge+chargedQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenQuota, getTokenRemainQuota(t, tokenID))
	assert.Zero(t, getTokenUsedQuota(t, tokenID))
	usedQuota, requestCount := getUserUsageAccounting(t, userID)
	assert.Zero(t, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Zero(t, getChannelUsedQuota(t, channelID))
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, channelID, log.ChannelId)
	assert.Zero(t, log.TokenId)
}

// ===========================================================================
// RefundTaskQuota tests
// ===========================================================================

func TestRefundTaskQuota_Wallet(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 1, 1, 1
	const initQuota, preConsumed = 10000, 3000
	const tokenRemain = 5000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-test-key", tokenRemain)
	seedChannel(t, channelID)
	seedChargedAccounting(t, userID, channelID, tokenID, preConsumed, 1)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	require.NoError(t, model.DB.Create(task).Error)

	assert.True(t, RefundTaskQuota(ctx, task, "task failed: upstream error"))

	// User quota should increase by preConsumed
	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID))

	// Token remain_quota should increase, used_quota should decrease
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))
	assert.Zero(t, getTokenUsedQuota(t, tokenID))
	usedQuota, requestCount := getUserUsageAccounting(t, userID)
	assert.Zero(t, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Zero(t, getChannelUsedQuota(t, channelID))

	// A refund log should be created
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Equal(t, preConsumed, log.Quota)
	assert.Equal(t, "test-model", log.ModelName)
	assert.Zero(t, task.Quota)
	assert.Zero(t, getTaskQuota(t, task.ID))
}

func TestAppWalletSettlementUsesPersistedQuota(t *testing.T) {
	for _, actual := range []int{0, 40, 140} {
		t.Run(fmt.Sprint(actual), func(t *testing.T) {
			truncate(t)
			seedUser(t, 91, 1000)
			seedChannel(t, 91)
			seedAppWallet(t, 91, 7, 900, 100)
			seedChargedAccounting(t, 91, 91, 0, 100, 1)
			task := makeTask(91, 91, 100, 0, BillingSourceWallet, 0, 7)
			require.NoError(t, task.Insert())
			stale := *task
			// The caller's quota may even already equal the requested target.
			task.Quota = actual
			if actual == 0 {
				require.True(t, RefundTaskQuota(context.Background(), task, "failed"))
				require.True(t, RefundTaskQuota(context.Background(), &stale, "duplicate"))
			} else {
				require.True(t, RecalculateTaskQuota(context.Background(), task, actual, common.NewMessage("actual usage")))
				require.True(t, RecalculateTaskQuota(context.Background(), &stale, actual, common.NewMessage("duplicate")))
			}
			assert.Equal(t, actual, stale.Quota)
			assert.Equal(t, actual, getTaskQuota(t, task.ID))
			wallet := getAppWallet(t, 91, 7)
			assert.Equal(t, 1000-actual, wallet.Quota)
			assert.Equal(t, actual, wallet.UsedQuota)
			used, requests := getUserUsageAccounting(t, 91)
			assert.Equal(t, actual, used)
			assert.Equal(t, 1, requests)
			assert.EqualValues(t, actual, getChannelUsedQuota(t, 91))
			assert.EqualValues(t, 1, countLogs(t))
		})
	}
}

func TestAppWalletSettlementConcurrentObservers(t *testing.T) {
	truncate(t)
	seedAppWallet(t, 92, 7, 10, 100)
	task := makeTask(92, 0, 100, 0, BillingSourceWallet, 0, 7)
	require.NoError(t, task.Insert())
	observers := []model.Task{*task, *task}
	start := make(chan struct{})
	deltas := make([]int, len(observers))
	errs := make([]error, len(observers))
	var wg sync.WaitGroup
	for i := range observers {
		wg.Go(func() {
			<-start
			deltas[i], errs[i] = observers[i].SettleAppWalletQuota(context.Background(), 140)
		})
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	assert.ElementsMatch(t, []int{40, 0}, deltas)
	wallet := getAppWallet(t, 92, 7)
	assert.Equal(t, -30, wallet.Quota, "final charges can overdraw")
	assert.Equal(t, 140, wallet.UsedQuota)
	assert.Equal(t, 140, getTaskQuota(t, task.ID))
}

func TestAppWalletSettlementRejectsForeignTaskAndCapacityOverflow(t *testing.T) {
	truncate(t)
	seedAppWallet(t, 93, 7, common.MaxWalletQuota-10, 100)
	task := makeTask(93, 0, 100, 0, BillingSourceWallet, 0, 7)
	require.NoError(t, task.Insert())
	for _, tc := range []struct {
		name              string
		user, app, actual int
	}{
		{"refund capacity", 93, 7, 0},
		{"downward capacity", 93, 7, 40},
		{"wrong owner", 94, 7, 140},
		{"wrong app", 93, 8, 140},
		{"oversized charge", 93, 7, common.MaxQuota + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := *task
			caller.UserId, caller.AppId = tc.user, tc.app
			delta, err := caller.SettleAppWalletQuota(context.Background(), tc.actual)
			require.Error(t, err)
			assert.Zero(t, delta)
			assert.Equal(t, 100, caller.Quota)
			wallet := getAppWallet(t, 93, 7)
			assert.Equal(t, common.MaxWalletQuota-10, wallet.Quota)
			assert.Equal(t, 100, wallet.UsedQuota)
			assert.Equal(t, 100, getTaskQuota(t, task.ID))
		})
	}
}

func TestAppWalletTerminalWriteFailuresRemainRetryable(t *testing.T) {
	for _, actual := range []int{0, 40, 140} {
		for _, failure := range []string{"wallet", "usage", "marker", "terminal"} {
			t.Run(fmt.Sprintf("%d/%s", actual, failure), func(t *testing.T) {
				truncate(t)
				seedUser(t, 94, 1000)
				seedChannel(t, 94)
				seedAppWallet(t, 94, 7, 900, 100)
				seedChargedAccounting(t, 94, 94, 0, 100, 1)
				task := makeTask(94, 94, 100, 0, BillingSourceWallet, 0, 7)
				task.PrivateData.BillingContext.TieredSnapshot = &billingexpr.BillingSnapshot{
					ExprString: `tier("actual", u("units"))`, GroupRatio: 1, QuotaPerUnit: 1,
					ExprVersion: 1, TaskUsageBilling: true, UsageFacts: map[string]any{"units": float64(100)},
				}
				require.NoError(t, task.Insert())
				injected := true
				require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register("test:app_terminal_failure", func(tx *gorm.DB) {
					if !injected || tx.Statement.Schema == nil {
						return
					}
					values, _ := tx.Statement.Dest.(map[string]any)
					_, quota := values["quota"]
					_, usage := values["used_quota"]
					wallet := tx.Statement.Schema.Name == "UserAppWallet"
					marker := tx.Statement.Schema.Name == "Task" && quota
					_, terminal := tx.Statement.Dest.(*model.Task)
					if failure == "wallet" && wallet && quota || failure == "usage" && wallet && usage || failure == "marker" && marker || failure == "terminal" && terminal {
						tx.AddError(errors.New("injected app terminal write failure"))
					}
				}))
				t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove("test:app_terminal_failure")) })
				adaptor := &scriptedPollingAdaptor{parse: &relaycommon.TaskInfo{
					Status: model.TaskStatusSuccess, UsageFacts: map[string]any{"units": float64(actual)},
				}}
				ch := &model.Channel{Id: 94, Type: constant.ChannelTypeTaskPlugin}
				poll := func() error {
					return updateVideoSingleTask(context.Background(), adaptor, ch, task.TaskID, map[string]*model.Task{task.TaskID: task})
				}
				require.Error(t, poll())
				var persisted model.Task
				require.NoError(t, model.DB.First(&persisted, task.ID).Error)
				assert.EqualValues(t, model.TaskStatusInProgress, persisted.Status)
				assert.EqualValues(t, model.TaskStatusInProgress, task.Status, "failed publication is not exposed in memory")
				want := 100
				if failure == "terminal" {
					want = actual
				}
				assert.Equal(t, want, persisted.Quota)
				assert.Equal(t, want, task.Quota)
				wallet := getAppWallet(t, 94, 7)
				assert.Equal(t, 1000-want, wallet.Quota)
				assert.Equal(t, want, wallet.UsedQuota)
				injected = false
				*task = persisted
				require.NoError(t, poll())
				require.NoError(t, model.DB.First(&persisted, task.ID).Error)
				assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), persisted.Status)
				assert.Equal(t, actual, persisted.Quota)
				wallet = getAppWallet(t, 94, 7)
				assert.Equal(t, 1000-actual, wallet.Quota)
				assert.Equal(t, actual, wallet.UsedQuota)
				assert.EqualValues(t, 1, countLogs(t), "committed financial adjustment is not logged twice")
			})
		}
	}
}

func TestAppWalletPendingSnapshotCannotOverwriteFinancialMarker(t *testing.T) {
	truncate(t)
	seedAppWallet(t, 95, 7, 900, 100)
	task := makeTask(95, 0, 100, 0, BillingSourceWallet, 0, 7)
	require.NoError(t, task.Insert())
	stale := *task
	delta, err := task.SettleAppWalletQuota(context.Background(), 40)
	require.NoError(t, err)
	assert.Equal(t, -60, delta)
	stale.Progress = "60%"
	_, err = stale.UpdateWithStatus(model.TaskStatusInProgress)
	require.NoError(t, err)
	assert.Equal(t, 40, getTaskQuota(t, task.ID))
	task.Status = model.TaskStatusSuccess
	won, err := task.UpdateWithStatus(model.TaskStatusInProgress)
	require.NoError(t, err)
	require.True(t, won)
	// A late timeout/failure must not refund an already published success.
	stale.Status = model.TaskStatusFailure
	_, err = stale.SettleAppWalletQuota(context.Background(), 0)
	require.Error(t, err)
	wallet := getAppWallet(t, 95, 7)
	assert.Equal(t, 960, wallet.Quota)
	assert.Equal(t, 40, wallet.UsedQuota)
}

func TestAppWalletFailureEntryPointsDoNotPublishOnFundingFailure(t *testing.T) {
	for _, entry := range []string{"single", "batch", "poll cutoff", "timeout", "missing channel", "missing task id"} {
		t.Run(entry, func(t *testing.T) {
			truncate(t)
			seedUser(t, 96, 1000)
			seedChannel(t, 96)
			seedAppWallet(t, 96, 7, 900, 100)
			task := makeTask(96, 96, 100, 0, BillingSourceWallet, 0, 7)
			task.Progress = "50%"
			task.SubmitTime = time.Now().Unix()
			oldTimeout, oldLimit, oldFactory := constant.TaskTimeoutMinutes, constant.TaskQueryLimit, GetTaskAdaptorFunc
			constant.TaskTimeoutMinutes, constant.TaskQueryLimit = 60, 100
			GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return &mockAdaptor{} }
			t.Cleanup(func() {
				constant.TaskTimeoutMinutes, constant.TaskQueryLimit, GetTaskAdaptorFunc = oldTimeout, oldLimit, oldFactory
			})
			if entry == "timeout" {
				task.SubmitTime -= 7200
			}
			if entry == "missing channel" {
				task.ChannelId = 99999
			}
			if entry == "missing task id" {
				task.TaskID = ""
			}
			require.NoError(t, task.Insert())
			injected := true
			require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register("test:app_failure_marker", func(tx *gorm.DB) {
				if injected && tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Task" {
					if values, ok := tx.Statement.Dest.(map[string]any); ok {
						if _, marker := values["quota"]; marker {
							tx.AddError(errors.New("injected refund marker failure"))
						}
					}
				}
			}))
			t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove("test:app_failure_marker")) })
			poll := func() {
				switch entry {
				case "single":
					_ = updateVideoSingleTask(context.Background(), &scriptedPollingAdaptor{statusCode: 404}, &model.Channel{Id: 96}, task.TaskID, map[string]*model.Task{task.TaskID: task})
				case "batch", "missing channel":
					a := &scriptedBatchPollingAdaptor{results: map[string]*BatchTaskResult{task.TaskID: {TaskInfo: *relaycommon.FailTaskInfo("failed")}}}
					_ = updateBatchTasks(context.Background(), a, task.ChannelId, []string{task.TaskID}, map[string]*model.Task{task.TaskID: task})
				case "poll cutoff":
					_ = failTaskFromPoll(context.Background(), nil, task, task.Status, "Failed to query the upstream task status")
				case "timeout":
					sweepTimedOutTasks(context.Background())
				case "missing task id":
					RunTaskPollingOnce(context.Background(), nil)
				}
			}
			poll()
			var persisted model.Task
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			assert.EqualValues(t, model.TaskStatusInProgress, persisted.Status)
			assert.Equal(t, 100, persisted.Quota)
			wallet := getAppWallet(t, 96, 7)
			assert.Equal(t, 900, wallet.Quota)
			assert.Equal(t, 100, wallet.UsedQuota)
			injected = false
			*task = persisted
			poll()
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			assert.EqualValues(t, model.TaskStatusFailure, persisted.Status)
			assert.Zero(t, persisted.Quota)
			wallet = getAppWallet(t, 96, 7)
			assert.Equal(t, 1000, wallet.Quota)
			assert.Zero(t, wallet.UsedQuota)
		})
	}
}

func TestRefundTaskQuota_AppWallet(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID, appID = 61, 61, 61, 21
	const initQuota, appInit, preConsumed = 10000, 8000, 3000
	const tokenRemain = 5000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-app-refund", tokenRemain)
	seedChannel(t, channelID)
	// The submit phase already charged the app pool and recorded its usage.
	seedAppWallet(t, userID, appID, appInit-preConsumed, preConsumed)
	seedChargedAccounting(t, userID, channelID, tokenID, preConsumed, 1)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0, appID)
	require.NoError(t, model.DB.Create(task).Error)

	assert.True(t, RefundTaskQuota(ctx, task, "app task failed"))

	wallet := getAppWallet(t, userID, appID)
	assert.Equal(t, appInit, wallet.Quota, "app pool is refunded")
	assert.Zero(t, wallet.UsedQuota, "app usage is reversed")
	assert.Equal(t, initQuota, getUserQuota(t, userID), "platform wallet is untouched")
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))
	assert.Zero(t, getTokenUsedQuota(t, tokenID))
	usedQuota, requestCount := getUserUsageAccounting(t, userID)
	assert.Zero(t, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Zero(t, getChannelUsedQuota(t, channelID))
	assert.Zero(t, task.Quota)
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Equal(t, preConsumed, log.Quota)
}

func TestRecalculate_AppWalletDeltas(t *testing.T) {
	cases := []struct {
		name        string
		userID      int
		appID       int
		preConsumed int
		actualQuota int
		appQuota    int
		appUsed     int
		wantQuota   int
		wantUsed    int
	}{
		{"positive delta", 71, 31, 2000, 3000, 8000, 2000, 7000, 3000},
		{"negative delta", 72, 32, 5000, 3000, 8000, 5000, 10000, 3000},
		{"negative delta floors usage at zero", 73, 33, 5000, 0, 8000, 1000, 13000, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			truncate(t)
			ctx := context.Background()
			const initQuota = 20000

			seedUser(t, tc.userID, initQuota)
			seedToken(t, tc.userID, tc.userID, fmt.Sprintf("sk-app-%d", tc.userID), 5000)
			seedChannel(t, tc.userID)
			seedAppWallet(t, tc.userID, tc.appID, tc.appQuota, tc.appUsed)
			seedChargedAccounting(t, tc.userID, tc.userID, tc.userID, tc.preConsumed, 1)

			task := makeTask(tc.userID, tc.userID, tc.preConsumed, tc.userID, BillingSourceWallet, 0, tc.appID)
			require.NoError(t, model.DB.Create(task).Error)
			require.True(t, RecalculateTaskQuota(ctx, task, tc.actualQuota, common.NewMessage("adaptor adjustment")))

			wallet := getAppWallet(t, tc.userID, tc.appID)
			assert.Equal(t, tc.wantQuota, wallet.Quota)
			assert.Equal(t, tc.wantUsed, wallet.UsedQuota)
			assert.Equal(t, initQuota, getUserQuota(t, tc.userID), "platform wallet is untouched")
			usedQuota, _ := getUserUsageAccounting(t, tc.userID)
			assert.Equal(t, tc.actualQuota, usedQuota)
			assert.Equal(t, tc.actualQuota, task.Quota)
		})
	}
}

func TestRecalculate_AppSubscriptionSkipsAppWalletUsage(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, channelID, appID, subID = 81, 81, 41, 8
	const preConsumed, actualQuota = 2000, 3000
	const subTotal, subUsed int64 = 100000, 50000
	const appQuota, appUsed = 8000, 5000

	seedUser(t, userID, 0)
	seedChannel(t, channelID)
	seedSubscription(t, subID, userID, subTotal, subUsed)
	seedAppWallet(t, userID, appID, appQuota, appUsed)
	seedChargedAccounting(t, userID, channelID, 0, preConsumed, 1)

	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceSubscription, subID, appID)
	RecalculateTaskQuota(ctx, task, actualQuota, common.NewMessage("subscription adjustment"))

	assert.Equal(t, subUsed+int64(actualQuota-preConsumed), getSubscriptionUsed(t, subID))
	wallet := getAppWallet(t, userID, appID)
	assert.Equal(t, appQuota, wallet.Quota, "subscription tasks never touch the app pool")
	assert.Equal(t, appUsed, wallet.UsedQuota, "subscription tasks never write app wallet usage")
}

func TestRefundTaskQuota_Subscription(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID, subID = 2, 2, 2, 1
	const preConsumed = 2000
	const subTotal, subUsed int64 = 100000, 50000
	const tokenRemain = 8000

	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "sk-sub-key", tokenRemain)
	seedChannel(t, channelID)
	seedSubscription(t, subID, userID, subTotal, subUsed)
	seedChargedAccounting(t, userID, channelID, tokenID, preConsumed, 1)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceSubscription, subID)
	require.NoError(t, model.DB.Create(task).Error)

	assert.True(t, RefundTaskQuota(ctx, task, "subscription task failed"))

	// Subscription used should decrease by preConsumed
	assert.Equal(t, subUsed-int64(preConsumed), getSubscriptionUsed(t, subID))

	// Token should also be refunded
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))
	assert.Zero(t, getTokenUsedQuota(t, tokenID))
	usedQuota, requestCount := getUserUsageAccounting(t, userID)
	assert.Zero(t, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Zero(t, getChannelUsedQuota(t, channelID))

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Zero(t, getTaskQuota(t, task.ID))
}

func TestRefundTaskQuota_ZeroQuota(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID = 3
	seedUser(t, userID, 5000)

	task := makeTask(userID, 0, 0, 0, BillingSourceWallet, 0)

	assert.True(t, RefundTaskQuota(ctx, task, "zero quota task"))

	// No change to user quota
	assert.Equal(t, 5000, getUserQuota(t, userID))

	// No log created
	assert.Equal(t, int64(0), countLogs(t))
}

func TestRefundTaskQuota_NoToken(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, channelID = 4, 4
	const initQuota, preConsumed = 10000, 1500

	seedUser(t, userID, initQuota)
	seedChannel(t, channelID)
	seedChargedAccounting(t, userID, channelID, 0, preConsumed, 1)

	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0) // TokenId=0
	require.NoError(t, model.DB.Create(task).Error)

	assert.True(t, RefundTaskQuota(ctx, task, "no token task failed"))

	// User quota refunded
	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID))
	usedQuota, requestCount := getUserUsageAccounting(t, userID)
	assert.Zero(t, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Zero(t, getChannelUsedQuota(t, channelID))

	// Log created
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Zero(t, getTaskQuota(t, task.ID))
}

func TestRefundTaskQuota_FundingFailureKeepsAccountingAndPendingMarker(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, channelID, preConsumed = 5, 5, 1200
	seedUser(t, userID, 5000)
	seedChannel(t, channelID)
	seedChargedAccounting(t, userID, channelID, 0, preConsumed, 1)
	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceSubscription, 9999)
	task.Status = model.TaskStatusFailure
	require.NoError(t, model.DB.Create(task).Error)

	assert.False(t, RefundTaskQuota(ctx, task, "subscription missing"))
	assert.Equal(t, 5000, getUserQuota(t, userID))
	assert.Equal(t, preConsumed, task.Quota)
	assert.Equal(t, preConsumed, getTaskQuota(t, task.ID))
	usedQuota, requestCount := getUserUsageAccounting(t, userID)
	assert.Equal(t, preConsumed, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Equal(t, int64(preConsumed), getChannelUsedQuota(t, channelID))
	assert.Equal(t, int64(0), countLogs(t))
}

// ===========================================================================
// RecalculateTaskQuota tests
// ===========================================================================

func TestRecalculate_PositiveDelta(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 10, 10, 10
	const initQuota, preConsumed = 10000, 2000
	const actualQuota = 3000 // under-charged by 1000
	const tokenRemain = 5000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-recalc-pos", tokenRemain)
	seedChannel(t, channelID)
	seedChargedAccounting(t, userID, channelID, tokenID, preConsumed, 1)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)

	RecalculateTaskQuota(ctx, task, actualQuota, common.NewMessage("adaptor adjustment"))

	// User quota should decrease by the delta (1000 additional charge)
	assert.Equal(t, initQuota-(actualQuota-preConsumed), getUserQuota(t, userID))

	// Token should also be charged the delta
	assert.Equal(t, tokenRemain-(actualQuota-preConsumed), getTokenRemainQuota(t, tokenID))
	assert.Equal(t, actualQuota, getTokenUsedQuota(t, tokenID))
	usedQuota, requestCount := getUserUsageAccounting(t, userID)
	assert.Equal(t, actualQuota, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Equal(t, int64(actualQuota), getChannelUsedQuota(t, channelID))

	// task.Quota should be updated to actualQuota
	assert.Equal(t, actualQuota, task.Quota)

	// Log type should be Consume (additional charge)
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeConsume, log.Type)
	assert.Equal(t, actualQuota-preConsumed, log.Quota)
}

func TestRecalculate_NegativeDelta(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 11, 11, 11
	const initQuota, preConsumed = 10000, 5000
	const actualQuota = 3000 // over-charged by 2000
	const tokenRemain = 5000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-recalc-neg", tokenRemain)
	seedChannel(t, channelID)
	seedChargedAccounting(t, userID, channelID, tokenID, preConsumed, 1)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)

	RecalculateTaskQuota(ctx, task, actualQuota, common.NewMessage("adaptor adjustment"))

	// User quota should increase by abs(delta) = 2000 (refund overpayment)
	assert.Equal(t, initQuota+(preConsumed-actualQuota), getUserQuota(t, userID))

	// Token should be refunded the difference
	assert.Equal(t, tokenRemain+(preConsumed-actualQuota), getTokenRemainQuota(t, tokenID))
	assert.Equal(t, actualQuota, getTokenUsedQuota(t, tokenID))
	usedQuota, requestCount := getUserUsageAccounting(t, userID)
	assert.Equal(t, actualQuota, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Equal(t, int64(actualQuota), getChannelUsedQuota(t, channelID))

	// task.Quota updated
	assert.Equal(t, actualQuota, task.Quota)

	// Log type should be Refund
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Equal(t, preConsumed-actualQuota, log.Quota)
}

func TestRecalculate_ZeroDelta(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID = 12
	const initQuota, preConsumed = 10000, 3000

	seedUser(t, userID, initQuota)

	task := makeTask(userID, 0, preConsumed, 0, BillingSourceWallet, 0)

	RecalculateTaskQuota(ctx, task, preConsumed, common.NewMessage("exact match"))

	// No change to user quota
	assert.Equal(t, initQuota, getUserQuota(t, userID))

	// No log created (delta is zero)
	assert.Equal(t, int64(0), countLogs(t))
}

func TestRecalculate_ActualQuotaZero(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, preConsumed = 13, 5000
	const initQuota = 10000

	seedUser(t, userID, initQuota)

	task := makeTask(userID, 0, preConsumed, 0, BillingSourceWallet, 0)
	require.NoError(t, model.DB.Create(task).Error)

	RecalculateTaskQuota(ctx, task, 0, common.NewMessage("zero actual"))

	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID))
	assert.Zero(t, task.Quota)
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Equal(t, preConsumed, log.Quota)
}

func TestRecalculate_RejectsNegativeActualQuota(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, preConsumed = 34, 5000
	const initQuota = 10000
	seedUser(t, userID, initQuota)
	task := makeTask(userID, 0, preConsumed, 0, BillingSourceWallet, 0)

	RecalculateTaskQuota(ctx, task, -1, common.NewMessage("invalid negative actual"))

	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, preConsumed, task.Quota)
	assert.Equal(t, int64(0), countLogs(t))
}

func TestRecalculate_Subscription_NegativeDelta(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID, subID = 14, 14, 14, 2
	const preConsumed = 5000
	const actualQuota = 2000 // over-charged by 3000
	const subTotal, subUsed int64 = 100000, 50000
	const tokenRemain = 8000

	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "sk-sub-recalc", tokenRemain)
	seedChannel(t, channelID)
	seedSubscription(t, subID, userID, subTotal, subUsed)
	seedChargedAccounting(t, userID, channelID, tokenID, preConsumed, 1)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceSubscription, subID)

	RecalculateTaskQuota(ctx, task, actualQuota, common.NewMessage("subscription over-charge"))

	// Subscription used should decrease by delta (refund 3000)
	assert.Equal(t, subUsed-int64(preConsumed-actualQuota), getSubscriptionUsed(t, subID))

	// Token refunded
	assert.Equal(t, tokenRemain+(preConsumed-actualQuota), getTokenRemainQuota(t, tokenID))
	assert.Equal(t, actualQuota, getTokenUsedQuota(t, tokenID))
	usedQuota, requestCount := getUserUsageAccounting(t, userID)
	assert.Equal(t, actualQuota, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Equal(t, int64(actualQuota), getChannelUsedQuota(t, channelID))

	assert.Equal(t, actualQuota, task.Quota)

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}

// ===========================================================================
// CAS + Billing integration tests
// Simulates the flow in updateVideoSingleTask (service/task_polling.go)
// ===========================================================================

// simulatePollBilling reproduces the CAS + billing logic from updateVideoSingleTask.
// It takes a persisted task (already in DB), applies the new status, and performs
// the conditional update + billing exactly as the polling loop does.
func simulatePollBilling(ctx context.Context, task *model.Task, newStatus model.TaskStatus, actualQuota int) {
	snap := task.Snapshot()

	shouldRefund := false
	shouldSettle := false
	quota := task.Quota

	task.Status = newStatus
	switch string(newStatus) {
	case model.TaskStatusSuccess:
		task.Progress = "100%"
		task.FinishTime = 9999
		shouldSettle = true
	case model.TaskStatusFailure:
		task.Progress = "100%"
		task.FinishTime = 9999
		task.FailReason = "upstream error"
		if quota != 0 {
			shouldRefund = true
		}
	default:
		task.Progress = "50%"
	}

	isDone := task.Status == model.TaskStatus(model.TaskStatusSuccess) || task.Status == model.TaskStatus(model.TaskStatusFailure)
	if isDone && snap.Status != task.Status {
		won, err := task.UpdateWithStatus(snap.Status)
		if err != nil {
			shouldRefund = false
			shouldSettle = false
		} else if !won {
			shouldRefund = false
			shouldSettle = false
		}
	} else if !snap.Equal(task.Snapshot()) {
		_, _ = task.UpdateWithStatus(snap.Status)
	}

	if shouldSettle && actualQuota > 0 {
		RecalculateTaskQuota(ctx, task, actualQuota, common.NewMessage("test settle"))
	}
	if shouldRefund {
		RefundTaskQuota(ctx, task, task.FailReason)
	}
}

func TestCASGuardedRefund_Win(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 20, 20, 20
	const initQuota, preConsumed = 10000, 4000
	const tokenRemain = 6000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-cas-refund-win", tokenRemain)
	seedChannel(t, channelID)
	seedChargedAccounting(t, userID, channelID, tokenID, preConsumed, 1)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	require.NoError(t, model.DB.Create(task).Error)

	simulatePollBilling(ctx, task, model.TaskStatus(model.TaskStatusFailure), 0)

	// CAS wins: task in DB should now be FAILURE
	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, reloaded.Status)
	assert.Zero(t, reloaded.Quota)

	// Refund should have happened
	assert.Equal(t, initQuota+preConsumed, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain+preConsumed, getTokenRemainQuota(t, tokenID))
	usedQuota, requestCount := getUserUsageAccounting(t, userID)
	assert.Zero(t, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Zero(t, getChannelUsedQuota(t, channelID))

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}

func TestCASGuardedRefund_Lose(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 21, 21, 21
	const initQuota, preConsumed = 10000, 4000
	const tokenRemain = 6000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-cas-refund-lose", tokenRemain)
	seedChannel(t, channelID)
	seedChargedAccounting(t, userID, channelID, tokenID, preConsumed, 1)

	// Create task with IN_PROGRESS in DB
	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	require.NoError(t, model.DB.Create(task).Error)

	// Simulate another process already transitioning to FAILURE
	model.DB.Model(&model.Task{}).Where("id = ?", task.ID).Update("status", model.TaskStatusFailure)

	// Our process still has the old in-memory state (IN_PROGRESS) and tries to transition
	// task.Status is still IN_PROGRESS in the snapshot
	simulatePollBilling(ctx, task, model.TaskStatus(model.TaskStatusFailure), 0)

	// CAS lost: user quota should NOT change (no double refund)
	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))
	usedQuota, requestCount := getUserUsageAccounting(t, userID)
	assert.Equal(t, preConsumed, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Equal(t, int64(preConsumed), getChannelUsedQuota(t, channelID))

	// No billing log should be created
	assert.Equal(t, int64(0), countLogs(t))
}

func TestCASGuardedSettle_Win(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 22, 22, 22
	const initQuota, preConsumed = 10000, 5000
	const actualQuota = 3000 // over-charged, should get partial refund
	const tokenRemain = 8000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-cas-settle-win", tokenRemain)
	seedChannel(t, channelID)
	seedChargedAccounting(t, userID, channelID, tokenID, preConsumed, 1)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	require.NoError(t, model.DB.Create(task).Error)

	simulatePollBilling(ctx, task, model.TaskStatus(model.TaskStatusSuccess), actualQuota)

	// CAS wins: task should be SUCCESS
	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusSuccess, reloaded.Status)

	// Settlement should refund the over-charge (5000 - 3000 = 2000 back to user)
	assert.Equal(t, initQuota+(preConsumed-actualQuota), getUserQuota(t, userID))
	assert.Equal(t, tokenRemain+(preConsumed-actualQuota), getTokenRemainQuota(t, tokenID))
	usedQuota, requestCount := getUserUsageAccounting(t, userID)
	assert.Equal(t, actualQuota, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Equal(t, int64(actualQuota), getChannelUsedQuota(t, channelID))

	// task.Quota should be updated to actualQuota
	assert.Equal(t, actualQuota, task.Quota)
}

func TestNonTerminalUpdate_NoBilling(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, channelID = 23, 23
	const initQuota, preConsumed = 10000, 3000

	seedUser(t, userID, initQuota)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceWallet, 0)
	task.Status = model.TaskStatus(model.TaskStatusInProgress)
	task.Progress = "20%"
	require.NoError(t, model.DB.Create(task).Error)

	// Simulate a non-terminal poll update (still IN_PROGRESS, progress changed)
	simulatePollBilling(ctx, task, model.TaskStatus(model.TaskStatusInProgress), 0)

	// User quota should NOT change
	assert.Equal(t, initQuota, getUserQuota(t, userID))

	// No billing log
	assert.Equal(t, int64(0), countLogs(t))

	// Task progress should be updated in DB
	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.Equal(t, "50%", reloaded.Progress)
}

// ===========================================================================
// Mock adaptor for settleTaskBillingOnComplete tests
// ===========================================================================

type mockAdaptor struct {
	adjustReturn int
}

func (m *mockAdaptor) Init(_ *relaycommon.RelayInfo) {}
func (m *mockAdaptor) FetchTask(string, string, *model.Task, string) (*http.Response, error) {
	return nil, nil
}
func (m *mockAdaptor) ParseTaskResult(*model.Task, *http.Response, []byte) (*relaycommon.TaskInfo, error) {
	return nil, nil
}
func (m *mockAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
	return m.adjustReturn
}

// ===========================================================================
// PerCallBilling tests — settleTaskBillingOnComplete
// ===========================================================================

func TestSettle_PerCallBilling_SkipsAdaptorAdjust(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 30, 30, 30
	const initQuota, preConsumed = 10000, 5000
	const tokenRemain = 8000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-percall-adaptor", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.BillingContext.PerCallBilling = true

	adaptor := &mockAdaptor{adjustReturn: 2000}
	taskResult := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	settled, _ := settleTaskBillingOnComplete(ctx, adaptor, task, taskResult)

	// Per-call: no adjustment despite adaptor returning 2000
	assert.False(t, settled)
	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, preConsumed, task.Quota)
	assert.Equal(t, int64(0), countLogs(t))
}

func TestSettle_PerCallBilling_SkipsTotalTokens(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 31, 31, 31
	const initQuota, preConsumed = 10000, 4000
	const tokenRemain = 7000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-percall-tokens", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.BillingContext.PerCallBilling = true

	adaptor := &mockAdaptor{adjustReturn: 0}
	taskResult := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, TotalTokens: 9999}

	settled, _ := settleTaskBillingOnComplete(ctx, adaptor, task, taskResult)

	// Per-call: no recalculation by tokens
	assert.False(t, settled)
	assert.Equal(t, initQuota, getUserQuota(t, userID))
	assert.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, preConsumed, task.Quota)
	assert.Equal(t, int64(0), countLogs(t))
}

func TestSettle_NonPerCallBilling_AppliesAdaptorAdjustment(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 32, 32, 32
	const initQuota, preConsumed = 10000, 5000
	const adaptorQuota = 3000
	const tokenRemain = 8000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-nonpercall-adj", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	// PerCallBilling defaults to false

	adaptor := &mockAdaptor{adjustReturn: adaptorQuota}
	taskResult := &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}

	settled, _ := settleTaskBillingOnComplete(ctx, adaptor, task, taskResult)

	// Non-per-call: adaptor adjustment applies (refund 2000)
	assert.True(t, settled)
	assert.Equal(t, initQuota+(preConsumed-adaptorQuota), getUserQuota(t, userID))
	assert.Equal(t, tokenRemain+(preConsumed-adaptorQuota), getTokenRemainQuota(t, tokenID))
	assert.Equal(t, adaptorQuota, task.Quota)

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
}

func TestSettle_TieredEvaluationFailureKeepsPreConsumedCharge(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, preConsumed = 33, 5_000
	const initialQuota = 10_000
	seedUser(t, userID, initialQuota)

	task := makeTask(userID, 0, preConsumed, 0, BillingSourceWallet, 0)
	task.PrivateData.BillingContext.TieredSnapshot = &billingexpr.BillingSnapshot{
		ExprString:       `tier("broken",`,
		ExprHash:         billingexpr.ExprHashString(`tier("broken",`),
		GroupRatio:       1,
		QuotaPerUnit:     1_000,
		ExprVersion:      1,
		TaskUsageBilling: true,
	}

	settled, _ := settleTaskBillingOnComplete(ctx, &mockAdaptor{}, task, &relaycommon.TaskInfo{Status: model.TaskStatusFailure})

	assert.True(t, settled)
	assert.Equal(t, preConsumed, task.Quota)
	assert.Equal(t, initialQuota, getUserQuota(t, userID))
	assert.Equal(t, int64(0), countLogs(t))
}

func TestSettle_TieredFailureReturnsFalseForCallerRefund(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID = 37
	const initialQuota, preConsumed = 10_000, 25
	seedUser(t, userID, initialQuota)

	expression := `tier("base", u("seconds") + u("clips") * 10)`
	task := makeTask(userID, 0, preConsumed, 0, BillingSourceWallet, 0)
	task.Status = model.TaskStatusFailure
	task.PrivateData.BillingContext.TieredSnapshot = &billingexpr.BillingSnapshot{
		ExprString:       expression,
		ExprHash:         billingexpr.ExprHashString(expression),
		GroupRatio:       1,
		QuotaPerUnit:     1,
		ExprVersion:      1,
		TaskUsageBilling: true,
		UsageFacts:       map[string]any{"seconds": float64(5), "clips": float64(2)},
		EstimatedTier:    "base",
	}

	settled, _ := settleTaskBillingOnComplete(
		ctx,
		&mockAdaptor{adjustReturn: 1},
		task,
		&relaycommon.TaskInfo{Status: model.TaskStatusFailure, UsageFacts: map[string]any{"seconds": float64(8)}},
	)

	assert.False(t, settled)
	assert.Equal(t, preConsumed, task.Quota)
	assert.Equal(t, map[string]any{"seconds": float64(5), "clips": float64(2)}, task.PrivateData.BillingContext.TieredSnapshot.UsageFacts)
	assert.Equal(t, "base", task.PrivateData.BillingContext.TieredSnapshot.EstimatedTier)
	assert.Equal(t, initialQuota, getUserQuota(t, userID))
	assert.Equal(t, int64(0), countLogs(t))
}

func TestSettle_TieredSuccessStillRecomputes(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID = 38
	const initialQuota, preConsumed = 10_000, 50
	seedUser(t, userID, initialQuota)

	expression := `tier("base", u("seconds") + u("clips") * 10)`
	task := makeTask(userID, 0, preConsumed, 0, BillingSourceWallet, 0)
	task.Status = model.TaskStatusSuccess
	task.PrivateData.BillingContext.TieredSnapshot = &billingexpr.BillingSnapshot{
		ExprString:       expression,
		ExprHash:         billingexpr.ExprHashString(expression),
		GroupRatio:       1,
		QuotaPerUnit:     1,
		ExprVersion:      1,
		TaskUsageBilling: true,
		UsageFacts:       map[string]any{"seconds": float64(5), "clips": float64(2)},
		EstimatedTier:    "base",
	}

	settled, _ := settleTaskBillingOnComplete(
		ctx,
		&mockAdaptor{adjustReturn: 1},
		task,
		&relaycommon.TaskInfo{Status: model.TaskStatusSuccess, UsageFacts: map[string]any{"seconds": float64(8)}},
	)

	assert.True(t, settled)
	assert.Equal(t, 28, task.Quota)
	assert.Equal(t, map[string]any{"seconds": float64(8), "clips": float64(2)}, task.PrivateData.BillingContext.TieredSnapshot.UsageFacts)
	assert.Equal(t, "base", task.PrivateData.BillingContext.TieredSnapshot.EstimatedTier)
	assert.Equal(t, initialQuota+(preConsumed-28), getUserQuota(t, userID))

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	var other map[string]any
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
	assert.Equal(t, "tiered_expr", other["billing_mode"])
	assert.Equal(t, "base", other["matched_tier"])
	facts, ok := other["usage_facts"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"seconds": float64(8), "clips": float64(2)}, facts)
}

func TestSettle_TieredUsageFactsMergeCompletionOverSubmission(t *testing.T) {
	tests := []struct {
		name            string
		completionFacts map[string]any
		expectedQuota   int
		expectedFacts   map[string]any
	}{
		{
			name:          "submission facts survive missing completion facts",
			expectedQuota: 25,
			expectedFacts: map[string]any{"seconds": float64(5), "clips": float64(2)},
		},
		{
			name:            "completion facts partially override submission facts",
			completionFacts: map[string]any{"seconds": float64(8)},
			expectedQuota:   28,
			expectedFacts:   map[string]any{"seconds": float64(8), "clips": float64(2)},
		},
		{
			name:            "completion facts fully override submission facts",
			completionFacts: map[string]any{"seconds": float64(8), "clips": float64(3)},
			expectedQuota:   38,
			expectedFacts:   map[string]any{"seconds": float64(8), "clips": float64(3)},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			truncate(t)
			const userID = 34
			const initialQuota = 10_000
			const preConsumed = 50
			seedUser(t, userID, initialQuota)

			expression := `tier("base", u("seconds") + u("clips") * 10)`
			submissionFacts := map[string]any{"seconds": float64(5), "clips": float64(2)}
			task := makeTask(userID, 0, preConsumed, 0, BillingSourceWallet, 0)
			task.PrivateData.BillingContext.TieredSnapshot = &billingexpr.BillingSnapshot{
				ExprString:       expression,
				ExprHash:         billingexpr.ExprHashString(expression),
				GroupRatio:       1,
				QuotaPerUnit:     1,
				ExprVersion:      1,
				TaskUsageBilling: true,
				UsageFacts:       submissionFacts,
				EstimatedTier:    "base",
			}

			settled, _ := settleTaskBillingOnComplete(
				context.Background(),
				&mockAdaptor{},
				task,
				&relaycommon.TaskInfo{Status: model.TaskStatusSuccess, UsageFacts: testCase.completionFacts},
			)

			assert.True(t, settled)
			assert.Equal(t, testCase.expectedQuota, task.Quota)
			assert.Equal(t, map[string]any{"seconds": float64(5), "clips": float64(2)}, submissionFacts)
			require.NotNil(t, task.PrivateData.BillingContext.TieredSnapshot)
			assert.Equal(t, testCase.expectedFacts, task.PrivateData.BillingContext.TieredSnapshot.UsageFacts)
			assert.Equal(t, "base", task.PrivateData.BillingContext.TieredSnapshot.EstimatedTier)

			log := getLastLog(t)
			require.NotNil(t, log)
			var other map[string]any
			require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
			assert.Equal(t, "tiered_expr", other["billing_mode"])
			assert.Equal(t, "base", other["matched_tier"])
			facts, ok := other["usage_facts"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, testCase.expectedFacts, facts)
			assert.NotContains(t, other, "seconds")
			assert.NotContains(t, other, "clips")
		})
	}
}

func TestSettle_TieredSnapshotWriteBackUsesSettledFactsAndMatchedTier(t *testing.T) {
	truncate(t)
	const userID = 36
	const initialQuota = 10_000
	const preConsumed = 25
	seedUser(t, userID, initialQuota)

	expression := `u("resolution") == "1080P" ? tier("1080P", u("seconds") * 10) : tier("720P", u("seconds") * 5)`
	task := makeTask(userID, 0, preConsumed, 0, BillingSourceWallet, 0)
	task.PrivateData.BillingContext.TieredSnapshot = &billingexpr.BillingSnapshot{
		ExprString:       expression,
		ExprHash:         billingexpr.ExprHashString(expression),
		GroupRatio:       1,
		QuotaPerUnit:     1,
		ExprVersion:      1,
		TaskUsageBilling: true,
		UsageFacts:       map[string]any{"resolution": "720P", "seconds": float64(5)},
		EstimatedTier:    "720P",
	}

	settled, _ := settleTaskBillingOnComplete(
		context.Background(),
		&mockAdaptor{},
		task,
		&relaycommon.TaskInfo{
			Status:     model.TaskStatusSuccess,
			UsageFacts: map[string]any{"resolution": "1080P"},
		},
	)

	require.True(t, settled)
	snap := task.PrivateData.BillingContext.TieredSnapshot
	require.NotNil(t, snap)
	assert.Equal(t, map[string]any{"resolution": "1080P", "seconds": float64(5)}, snap.UsageFacts)
	assert.Equal(t, "1080P", snap.EstimatedTier)
	assert.Equal(t, 50, task.Quota)

	log := getLastLog(t)
	require.NotNil(t, log)
	var other map[string]any
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
	assert.Equal(t, "tiered_expr", other["billing_mode"])
	assert.Equal(t, "1080P", other["matched_tier"])
	facts, ok := other["usage_facts"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "1080P", facts["resolution"])
	assert.Equal(t, float64(5), facts["seconds"])
	assert.NotContains(t, other, "resolution")
	assert.NotContains(t, other, "seconds")
}

func TestSettle_TokenRecalcFallsBackToCompletionTokens(t *testing.T) {
	previousRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"test-model":1}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios))
	})

	tests := []struct {
		name             string
		totalTokens      int
		completionTokens int
		wantSettled      bool
		wantQuota        int
	}{
		{
			name:             "total tokens still win when both are present",
			totalTokens:      80,
			completionTokens: 20,
			wantSettled:      true,
			wantQuota:        80,
		},
		{
			name:             "completion tokens trigger recalc when total is zero",
			totalTokens:      0,
			completionTokens: 80,
			wantSettled:      true,
			wantQuota:        80,
		},
		{
			name:        "neither token count skips recalc",
			wantSettled: false,
			wantQuota:   50,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			truncate(t)
			const userID, tokenID, channelID = 35, 35, 35
			const initialQuota, preConsumed, tokenRemain = 10_000, 50, 8_000
			seedUser(t, userID, initialQuota)
			seedToken(t, tokenID, userID, "sk-completion-fallback", tokenRemain)
			seedChannel(t, channelID)

			task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
			settled, _ := settleTaskBillingOnComplete(
				context.Background(),
				&mockAdaptor{},
				task,
				&relaycommon.TaskInfo{
					Status:           model.TaskStatusSuccess,
					TotalTokens:      testCase.totalTokens,
					CompletionTokens: testCase.completionTokens,
				},
			)

			assert.Equal(t, testCase.wantSettled, settled)
			assert.Equal(t, testCase.wantQuota, task.Quota)
		})
	}
}
