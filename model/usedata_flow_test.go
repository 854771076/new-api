package model

import (
	"sort"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func seedFlowQuotaData(t *testing.T, quotaData QuotaData) {
	t.Helper()
	require.NoError(t, DB.Create(&quotaData).Error)
}

func seedFlowLookupData(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.Create(&Channel{Id: 1, Name: "east"}).Error)
	require.NoError(t, DB.Create(&Channel{Id: 2, Name: "west"}).Error)
	require.NoError(t, DB.Create(&Token{Id: 11, UserId: 1, Key: "sk-primary", Name: "primary"}).Error)
	require.NoError(t, DB.Create(&Token{Id: 22, UserId: 2, Key: "sk-backup", Name: "backup"}).Error)
	require.NoError(t, DB.Delete(&Token{Id: 11}).Error)
}

func TestGetFlowQuotaDataUsesQuotaDataRoleSpecificDimensions(t *testing.T) {
	truncateTables(t)
	seedFlowLookupData(t)

	seedFlowQuotaData(t, QuotaData{
		UserID:    1,
		Username:  "alice",
		NodeName:  "node-a",
		TokenID:   11,
		UseGroup:  "vip",
		ModelName: "gpt-a",
		ChannelID: 1,
		CreatedAt: 1000,
		Count:     2,
		Quota:     100,
		TokenUsed: 40,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID:    1,
		Username:  "alice",
		NodeName:  "node-a",
		TokenID:   11,
		UseGroup:  "vip",
		ModelName: "gpt-a",
		ChannelID: 1,
		CreatedAt: 1100,
		Count:     1,
		Quota:     50,
		TokenUsed: 20,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID:    1,
		Username:  "alice",
		NodeName:  "node-a",
		TokenID:   11,
		UseGroup:  "vip",
		ModelName: "gpt-a",
		ChannelID: 2,
		CreatedAt: 1200,
		Count:     1,
		Quota:     25,
		TokenUsed: 10,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID:    2,
		Username:  "bob",
		NodeName:  "node-b",
		TokenID:   22,
		UseGroup:  "default",
		ModelName: "gpt-b",
		ChannelID: 1,
		CreatedAt: 1300,
		Count:     3,
		Quota:     70,
		TokenUsed: 30,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID:    1,
		Username:  "alice",
		ModelName: "legacy",
		CreatedAt: 1400,
		Count:     99,
		Quota:     999,
		TokenUsed: 999,
	})

	rootRows, err := GetFlowQuotaData(900, 2000, "", "", 0, common.RoleRootUser)
	require.NoError(t, err)
	require.Len(t, rootRows, 3)
	// Token 11 was soft-deleted, so its name is intentionally left empty for the
	// frontend to render a localized "deleted (id)" label instead.
	require.Equal(t, FlowQuotaData{
		UserID:      1,
		Username:    "alice",
		NodeName:    "node-a",
		TokenID:     11,
		TokenName:   "",
		UseGroup:    "vip",
		ChannelID:   1,
		ChannelName: "east",
		ModelName:   "gpt-a",
		TokenUsed:   60,
		Count:       3,
		Quota:       150,
	}, *rootRows[0])
	// A token that still exists resolves to its current name.
	require.Equal(t, 22, rootRows[1].TokenID)
	require.Equal(t, "backup", rootRows[1].TokenName)

	adminRows, err := GetFlowQuotaData(900, 2000, "alice", "", 0, common.RoleAdminUser)
	require.NoError(t, err)
	require.Len(t, adminRows, 2)
	require.Equal(t, 0, adminRows[0].TokenID)
	require.Empty(t, adminRows[0].TokenName)
	require.Empty(t, adminRows[0].NodeName)
	require.Equal(t, "alice", adminRows[0].Username)
	require.Equal(t, "vip", adminRows[0].UseGroup)
	require.Equal(t, "east", adminRows[0].ChannelName)
	require.Equal(t, 150, adminRows[0].Quota)

	selfRows, err := GetFlowQuotaData(900, 2000, "", "", 1, common.RoleCommonUser)
	require.NoError(t, err)
	require.Len(t, selfRows, 1)
	require.Empty(t, selfRows[0].Username)
	require.Equal(t, 0, selfRows[0].ChannelID)
	require.Empty(t, selfRows[0].ChannelName)
	require.Empty(t, selfRows[0].TokenName)
	require.Equal(t, "vip", selfRows[0].UseGroup)
	require.Equal(t, 175, selfRows[0].Quota)
}

func TestLogQuotaDataSplitsRowsByUseGroupTokenChannelAndNode(t *testing.T) {
	truncateTables(t)
	CacheQuotaDataLock.Lock()
	CacheQuotaData = make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()

	LogQuotaData(QuotaDataLogParams{
		UserID:    1,
		Username:  "alice",
		ModelName: "gpt-a",
		CreatedAt: 3661,
		UseGroup:  "vip",
		TokenID:   11,
		ChannelID: 1,
		NodeName:  "node-a",
		Quota:     100,
		TokenUsed: 40,
	})
	LogQuotaData(QuotaDataLogParams{
		UserID:    1,
		Username:  "alice",
		ModelName: "gpt-a",
		CreatedAt: 3700,
		UseGroup:  "vip",
		TokenID:   11,
		ChannelID: 1,
		NodeName:  "node-a",
		Quota:     50,
		TokenUsed: 20,
	})
	LogQuotaData(QuotaDataLogParams{
		UserID:    1,
		Username:  "alice",
		ModelName: "gpt-a",
		CreatedAt: 3700,
		UseGroup:  "default",
		TokenID:   11,
		ChannelID: 1,
		NodeName:  "node-a",
		Quota:     25,
		TokenUsed: 10,
	})

	SaveQuotaDataCache()

	var rows []QuotaData
	require.NoError(t, DB.Order("quota DESC").Find(&rows).Error)
	require.Len(t, rows, 2)
	require.Equal(t, int64(3600), rows[0].CreatedAt)
	require.Equal(t, "vip", rows[0].UseGroup)
	require.Equal(t, 11, rows[0].TokenID)
	require.Equal(t, 1, rows[0].ChannelID)
	require.Equal(t, "node-a", rows[0].NodeName)
	require.Equal(t, 2, rows[0].Count)
	require.Equal(t, 150, rows[0].Quota)
	require.Equal(t, 60, rows[0].TokenUsed)
	require.Equal(t, "default", rows[1].UseGroup)
	require.Equal(t, 25, rows[1].Quota)
}

// TestListDistinctTokenKeys 验证 token_key autocomplete 数据源：
// - distinct 字段 + 排除空字符串
// - keyword 模糊匹配
// - username 过滤（self/admin）
// - label 友好化：关联 tokens.name / 显示 username / 显示 (deleted) / 显示调用次数
func TestListDistinctTokenKeys(t *testing.T) {
	truncateTables(t)

	// 3 个 distinct token_key，其中 1 个空字符串应被过滤
	// 其中 1 个有 token.name 关联、2 个不关联（删除态）
	seedFlowQuotaData(t, QuotaData{
		UserID: 1, Username: "alice", ModelName: "gpt-4o",
		TokenKey: "sk-primary-aaa-bbb-ccc-111", TokenID: 1,
		CreatedAt: 1000, Quota: 100, Count: 3,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID: 2, Username: "bob", ModelName: "gpt-4o",
		TokenKey: "sk-backup-xxx-yyy-zzz-222", TokenID: 2,
		CreatedAt: 1100, Quota: 50, Count: 1,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID: 2, Username: "bob", ModelName: "gpt-4",
		TokenKey: "", TokenID: 3,
		CreatedAt: 1200, Quota: 30, Count: 1,
	})

	// 全局查询：应返回 2 条 distinct（空字符串被过滤）
	all, err := ListDistinctTokenKeys("", "", 0, 0, 100)
	require.NoError(t, err)
	require.Len(t, all, 2)

	// 找到两个 entry
	byValue := map[string]QuotaDataTokenKeyEntry{}
	for _, e := range all {
		byValue[e.Value] = e
	}
	primary, ok := byValue["sk-primary-aaa-bbb-ccc-111"]
	require.True(t, ok)
	backup, ok := byValue["sk-backup-xxx-yyy-zzz-222"]
	require.True(t, ok)

	// 两个都没有 token.name 关联（tokens 表为空） → 走 (deleted) 分支；
	// Label 是语言无关的英文兜底（次数不带单位），单位由前端 i18n 模板拼装
	require.Equal(t, "sk-p***-111 (deleted) · 3", primary.Label)
	require.Equal(t, "sk-b***-222 (deleted) · 1", backup.Label)

	// username 过滤
	bobOnly, err := ListDistinctTokenKeys("bob", "", 0, 0, 100)
	require.NoError(t, err)
	require.Len(t, bobOnly, 1)
	require.Equal(t, "sk-backup-xxx-yyy-zzz-222", bobOnly[0].Value)

	// 不存在的 username 应返回空
	empty, err := ListDistinctTokenKeys("nonexistent", "", 0, 0, 100)
	require.NoError(t, err)
	require.Empty(t, empty)

	// 按 usage 降序：primary=3 次应该排在 backup=1 次之前
	require.Equal(t, "sk-primary-aaa-bbb-ccc-111", all[0].Value)
	require.Equal(t, int64(3), all[0].Usage)
	require.Equal(t, "sk-backup-xxx-yyy-zzz-222", all[1].Value)
	require.Equal(t, int64(1), all[1].Usage)

	// limit 边界
	lim1, err := ListDistinctTokenKeys("", "", 0, 0, 1)
	require.NoError(t, err)
	require.Len(t, lim1, 1)

	// 关联到 tokens 表：label 用 token.name
	// 注意 truncateTables 只注册 t.Cleanup，测试中途要清数据需直接 DELETE
	require.NoError(t, DB.Exec("DELETE FROM quota_data").Error)
	require.NoError(t, DB.Create(&Token{Id: 11, UserId: 1, Key: "sk-with-name-aaa-111", Name: "Production Key", Status: 1}).Error)
	seedFlowQuotaData(t, QuotaData{
		UserID: 1, Username: "alice", ModelName: "gpt-4o",
		TokenKey: "sk-with-name-aaa-111", TokenID: 11,
		CreatedAt: 1000, Quota: 100, Count: 5,
	})
	withName, err := ListDistinctTokenKeys("", "", 0, 0, 100)
	require.NoError(t, err)
	require.Len(t, withName, 1)
	require.Equal(t, "Production Key (alice) · 5", withName[0].Label)
	require.Equal(t, "alice", withName[0].UserName)
	require.Equal(t, int64(5), withName[0].Usage)
}

// TestListDistinctTokenKeysTimeScopedStats 验证看板令牌 Key 下拉的数据源
// 按对话框里选择的时间范围聚合：
// - 只统计窗口内的 quota_data 行（窗口外调用次数/tokens 被排除）
// - usage = 窗口内 sum(count)，token_used = 窗口内 sum(token_used)
// - 结果按调用次数降序排列（下拉框排序契约）
// - 不传时间范围（0,0）保持全时段统计
func TestListDistinctTokenKeysTimeScopedStats(t *testing.T) {
	truncateTables(t)

	// key A：1 条窗口外 + 2 条窗口内
	seedFlowQuotaData(t, QuotaData{
		UserID: 1, Username: "alice", ModelName: "gpt-4o",
		TokenKey: "sk-window-aaa-111", TokenID: 11,
		CreatedAt: 1000, Quota: 100, Count: 5, TokenUsed: 500,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID: 1, Username: "alice", ModelName: "gpt-4o",
		TokenKey: "sk-window-aaa-111", TokenID: 11,
		CreatedAt: 2000, Quota: 60, Count: 3, TokenUsed: 300,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID: 1, Username: "alice", ModelName: "gpt-4",
		TokenKey: "sk-window-aaa-111", TokenID: 11,
		CreatedAt: 3000, Quota: 20, Count: 1, TokenUsed: 100,
	})
	// key B：1 条窗口内、窗口内次数最高
	seedFlowQuotaData(t, QuotaData{
		UserID: 2, Username: "bob", ModelName: "gpt-4o",
		TokenKey: "sk-other-bbb-222", TokenID: 22,
		CreatedAt: 2500, Quota: 80, Count: 4, TokenUsed: 400,
	})

	// 窗口 [1500, 3500]：A 只剩 3+1 次、400 tokens；B 4 次在前
	entries, err := ListDistinctTokenKeys("", "", 1500, 3500, 100)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, "sk-other-bbb-222", entries[0].Value)
	require.Equal(t, int64(4), entries[0].Usage)
	require.Equal(t, int64(400), entries[0].TokenUsed)
	require.Equal(t, "sk-window-aaa-111", entries[1].Value)
	require.Equal(t, int64(4), entries[1].Usage)
	require.Equal(t, int64(400), entries[1].TokenUsed)

	// 窗口 [4000, 9999]：全部行都在窗口外 → 空
	outside, err := ListDistinctTokenKeys("", "", 4000, 9999, 100)
	require.NoError(t, err)
	require.Empty(t, outside)

	// 不传时间范围：全时段统计（A = 9 次 / 900 tokens，排在 B 前）
	all, err := ListDistinctTokenKeys("", "", 0, 0, 100)
	require.NoError(t, err)
	require.Len(t, all, 2)
	require.Equal(t, "sk-window-aaa-111", all[0].Value)
	require.Equal(t, int64(9), all[0].Usage)
	require.Equal(t, int64(900), all[0].TokenUsed)
	require.Equal(t, int64(4), all[1].Usage)
	require.Equal(t, int64(400), all[1].TokenUsed)
}

// TestGetFlowQuotaDataFiltersByTokenKey 验证 admin/root 路径下 token_key 筛选
// 仅返回与该 token key 关联的行；其他 token / 匿名调用行被排除。
func TestGetFlowQuotaDataFiltersByTokenKey(t *testing.T) {
	truncateTables(t)
	seedFlowLookupData(t)

	seedFlowQuotaData(t, QuotaData{
		UserID:    1,
		Username:  "alice",
		NodeName:  "node-a",
		TokenID:   11,
		TokenKey:  "sk-primary",
		UseGroup:  "vip",
		ModelName: "gpt-a",
		ChannelID: 1,
		CreatedAt: 1000,
		Quota:     100,
		Count:     2,
		TokenUsed: 40,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID:    1,
		Username:  "alice",
		NodeName:  "node-a",
		TokenID:   11,
		TokenKey:  "sk-primary",
		UseGroup:  "vip",
		ModelName: "gpt-a",
		ChannelID: 1,
		CreatedAt: 1100,
		Quota:     50,
		Count:     1,
		TokenUsed: 20,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID:    2,
		Username:  "bob",
		NodeName:  "node-b",
		TokenID:   22,
		TokenKey:  "sk-backup",
		UseGroup:  "default",
		ModelName: "gpt-b",
		ChannelID: 1,
		CreatedAt: 1200,
		Quota:     30,
		Count:     1,
		TokenUsed: 10,
	})

	adminRows, err := GetFlowQuotaData(900, 2000, "", "sk-primary", 0, common.RoleAdminUser)
	require.NoError(t, err)
	require.Len(t, adminRows, 1)
	require.Equal(t, "alice", adminRows[0].Username)
	require.Equal(t, 150, adminRows[0].Quota)

	rootRows, err := GetFlowQuotaData(900, 2000, "", "sk-backup", 0, common.RoleRootUser)
	require.NoError(t, err)
	require.Len(t, rootRows, 1)
	require.Equal(t, 22, rootRows[0].TokenID)
	require.Equal(t, "bob", rootRows[0].Username)

	selfRows, err := GetFlowQuotaData(900, 2000, "", "sk-primary", 1, common.RoleCommonUser)
	require.NoError(t, err)
	require.Len(t, selfRows, 1)
	require.Equal(t, 150, selfRows[0].Quota)

	selfRows, err = GetFlowQuotaData(900, 2000, "", "sk-missing", 1, common.RoleCommonUser)
	require.NoError(t, err)
	require.Empty(t, selfRows)
}

// TestGetAllQuotaDatesFiltersByTokenKey 验证 admin 看板汇总接口同时支持
// 仅按 token_key 过滤、按 username + token_key 联合过滤，以及 token_key 不传时
// 与原行为一致。
func TestGetAllQuotaDatesFiltersByTokenKey(t *testing.T) {
	truncateTables(t)

	seedFlowQuotaData(t, QuotaData{
		UserID:    1,
		Username:  "alice",
		ModelName: "gpt-a",
		TokenKey:  "sk-primary",
		CreatedAt: 1000,
		Quota:     60,
		Count:     1,
		TokenUsed: 20,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID:    1,
		Username:  "alice",
		ModelName: "gpt-a",
		TokenKey:  "sk-backup",
		CreatedAt: 1000, // 与上一条相同 created_at，测试聚合
		Quota:     30,
		Count:     1,
		TokenUsed: 10,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID:    2,
		Username:  "bob",
		ModelName: "gpt-b",
		TokenKey:  "sk-primary",
		CreatedAt: 1000, // 相同 created_at 但不同 model_name，不聚合
		Quota:     15,
		Count:     1,
		TokenUsed: 5,
	})

	// GetAllQuotaDates groups by (model_name, created_at).
	// With all three rows at created_at=1000:
	//   - gpt-a/1000: sum(60+30)=90
	//   - gpt-b/1000: 15
	noFilter, err := GetAllQuotaDates(900, 2000, "", "")
	require.NoError(t, err)
	require.Len(t, noFilter, 2)
	// Sort by model_name for deterministic assertion
	sort.Slice(noFilter, func(i, j int) bool { return noFilter[i].ModelName < noFilter[j].ModelName })
	require.Equal(t, "gpt-a", noFilter[0].ModelName)
	require.Equal(t, 90, noFilter[0].Quota)
	require.Equal(t, "gpt-b", noFilter[1].ModelName)
	require.Equal(t, 15, noFilter[1].Quota)

	// Filter by sk-primary: gpt-a/1000 (sk-primary only)=60, gpt-b/1000=15
	onlyKey, err := GetAllQuotaDates(900, 2000, "", "sk-primary")
	require.NoError(t, err)
	require.Len(t, onlyKey, 2)
	sort.Slice(onlyKey, func(i, j int) bool { return onlyKey[i].ModelName < onlyKey[j].ModelName })
	require.Equal(t, "gpt-a", onlyKey[0].ModelName)
	require.Equal(t, 60, onlyKey[0].Quota)
	require.Equal(t, "gpt-b", onlyKey[1].ModelName)
	require.Equal(t, 15, onlyKey[1].Quota)

	// Filter by sk-backup: only gpt-a/1000 (sk-backup)=30
	onlyBackup, err := GetAllQuotaDates(900, 2000, "", "sk-backup")
	require.NoError(t, err)
	require.Len(t, onlyBackup, 1)
	require.Equal(t, "gpt-a", onlyBackup[0].ModelName)
	require.Equal(t, 30, onlyBackup[0].Quota)

	// Username + tokenKey filter goes through GetQuotaDataByUsernameAndKey which
	// groups by (user_id, username, model_name, created_at).
	userPlusKey, err := GetAllQuotaDates(900, 2000, "alice", "sk-primary")
	require.NoError(t, err)
	require.Len(t, userPlusKey, 1)
	require.Equal(t, "gpt-a", userPlusKey[0].ModelName)
	require.Equal(t, 60, userPlusKey[0].Quota)

	userMissingKey, err := GetAllQuotaDates(900, 2000, "alice", "sk-missing")
	require.NoError(t, err)
	require.Empty(t, userMissingKey)
}
