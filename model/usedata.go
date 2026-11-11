package model

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// QuotaData 柱状图数据
type QuotaData struct {
	Id                  int    `json:"id"`
	UserID              int    `json:"user_id" gorm:"index"`
	Username            string `json:"username" gorm:"index:idx_qdt_model_user_name,priority:2;size:64;default:''"`
	ModelName           string `json:"model_name" gorm:"index:idx_qdt_model_user_name,priority:1;size:64;default:''"`
	CreatedAt           int64  `json:"created_at" gorm:"bigint;index:idx_qdt_created_at,priority:2"`
	UseGroup            string `json:"use_group" gorm:"index;size:64;default:''"`
	TokenID             int    `json:"token_id" gorm:"index;default:0"`
	TokenKey            string `json:"token_key" gorm:"index;size:64;default:''"`
	ChannelID           int    `json:"channel_id" gorm:"index;default:0"`
	NodeName            string `json:"node_name" gorm:"index;size:64;default:''"`
	TokenUsed           int    `json:"token_used" gorm:"default:0"`
	Count               int    `json:"count" gorm:"default:0"`
	Quota               int    `json:"quota" gorm:"default:0"`
	CacheReadTokens     int64  `json:"cache_read_tokens" gorm:"default:0"`
	CacheCreationTokens int64  `json:"cache_creation_tokens" gorm:"default:0"`
}

type QuotaDataLogParams struct {
	UserID              int
	Username            string
	ModelName           string
	Quota               int
	CreatedAt           int64
	TokenUsed           int
	CacheReadTokens     int64
	CacheCreationTokens int64
	UseGroup            string
	TokenID             int
	TokenKey            string
	ChannelID           int
	NodeName            string
}

func UpdateQuotaData() {
	for {
		if common.DataExportEnabled {
			common.SysLog(common.LogText("updating dashboard data..."))
			SaveQuotaDataCache()
		}
		time.Sleep(time.Duration(common.DataExportInterval) * time.Minute)
	}
}

var CacheQuotaData = make(map[string]*QuotaData)
var CacheQuotaDataLock = sync.Mutex{}

func logQuotaDataCache(quotaData *QuotaData) {
	key := fmt.Sprintf("%d\x00%s\x00%s\x00%d\x00%s\x00%d\x00%s\x00%d\x00%s",
		quotaData.UserID,
		quotaData.Username,
		quotaData.ModelName,
		quotaData.CreatedAt,
		quotaData.UseGroup,
		quotaData.TokenID,
		quotaData.TokenKey,
		quotaData.ChannelID,
		quotaData.NodeName,
	)
	count := quotaData.Count
	quota := quotaData.Quota
	tokenUsed := quotaData.TokenUsed
	cachedQuotaData, ok := CacheQuotaData[key]
	if ok {
		cachedQuotaData.Count += count
		cachedQuotaData.Quota += quota
		cachedQuotaData.TokenUsed += tokenUsed
		cachedQuotaData.CacheReadTokens += quotaData.CacheReadTokens
		cachedQuotaData.CacheCreationTokens += quotaData.CacheCreationTokens
		quotaData = cachedQuotaData
	}
	CacheQuotaData[key] = quotaData
}

func LogQuotaData(params QuotaDataLogParams) {
	// 只精确到小时
	createdAt := params.CreatedAt - (params.CreatedAt % 3600)
	quotaData := &QuotaData{
		UserID:              params.UserID,
		Username:            params.Username,
		ModelName:           params.ModelName,
		CreatedAt:           createdAt,
		UseGroup:            params.UseGroup,
		TokenID:             params.TokenID,
		TokenKey:            params.TokenKey,
		ChannelID:           params.ChannelID,
		NodeName:            params.NodeName,
		Count:               1,
		Quota:               params.Quota,
		TokenUsed:           params.TokenUsed,
		CacheReadTokens:     max(0, params.CacheReadTokens),
		CacheCreationTokens: max(0, params.CacheCreationTokens),
	}

	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	logQuotaDataCache(quotaData)
}

func SaveQuotaDataCache() {
	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	size := len(CacheQuotaData)
	// 如果缓存中有数据，就保存到数据库中
	// 1. 先查询数据库中是否有数据
	// 2. 如果有数据，就更新数据
	// 3. 如果没有数据，就插入数据
	for _, quotaData := range CacheQuotaData {
		quotaDataDB := &QuotaData{}
		DB.Table("quota_data").
			Where("user_id = ? and username = ? and model_name = ? and created_at = ? and use_group = ? and token_id = ? and token_key = ? and channel_id = ? and node_name = ?",
				quotaData.UserID, quotaData.Username, quotaData.ModelName, quotaData.CreatedAt, quotaData.UseGroup, quotaData.TokenID, quotaData.TokenKey, quotaData.ChannelID, quotaData.NodeName).
			First(quotaDataDB)
		if quotaDataDB.Id > 0 {
			//quotaDataDB.Count += quotaData.Count
			//quotaDataDB.Quota += quotaData.Quota
			//DB.Table("quota_data").Save(quotaDataDB)
			increaseQuotaData(quotaData)
		} else {
			DB.Table("quota_data").Create(quotaData)
		}
	}
	CacheQuotaData = make(map[string]*QuotaData)
	common.SysLog(common.LogText("saved dashboard data, %d records", size))
}

func increaseQuotaData(quotaData *QuotaData) {
	err := DB.Table("quota_data").
		Where("user_id = ? and username = ? and model_name = ? and created_at = ? and use_group = ? and token_id = ? and token_key = ? and channel_id = ? and node_name = ?",
			quotaData.UserID, quotaData.Username, quotaData.ModelName, quotaData.CreatedAt, quotaData.UseGroup, quotaData.TokenID, quotaData.TokenKey, quotaData.ChannelID, quotaData.NodeName).
		Updates(map[string]any{
			"count":                 gorm.Expr("count + ?", quotaData.Count),
			"quota":                 gorm.Expr("quota + ?", quotaData.Quota),
			"token_used":            gorm.Expr("token_used + ?", quotaData.TokenUsed),
			"cache_read_tokens":     gorm.Expr("cache_read_tokens + ?", quotaData.CacheReadTokens),
			"cache_creation_tokens": gorm.Expr("cache_creation_tokens + ?", quotaData.CacheCreationTokens),
		}).Error
	if err != nil {
		common.SysLog(common.LogText("increaseQuotaData error: %s", err))
	}
}

func GetQuotaDataByUsername(username string, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	err = DB.Table("quota_data").
		Select("user_id, username, model_name, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, sum(cache_read_tokens) as cache_read_tokens, sum(cache_creation_tokens) as cache_creation_tokens").
		Where("username = ? and created_at >= ? and created_at <= ?", username, startTime, endTime).
		Group("user_id, username, model_name, created_at").
		Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetQuotaDataByUserId(userId int, tokenKey string, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	query := DB.Table("quota_data").
		Select("user_id, username, model_name, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, sum(cache_read_tokens) as cache_read_tokens, sum(cache_creation_tokens) as cache_creation_tokens").
		Where("user_id = ? and created_at >= ? and created_at <= ?", userId, startTime, endTime)
	if tokenKey != "" {
		query = query.Where("token_key = ?", tokenKey)
	}
	err = query.Group("user_id, username, model_name, created_at").Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetQuotaDataGroupByUser(startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	err = DB.Table("quota_data").
		Select("username, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, sum(cache_read_tokens) as cache_read_tokens, sum(cache_creation_tokens) as cache_creation_tokens").
		Where("created_at >= ? and created_at <= ?", startTime, endTime).
		Group("username, created_at").
		Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetAllQuotaDates(startTime int64, endTime int64, username string, tokenKey string) (quotaData []*QuotaData, err error) {
	if username != "" {
		return GetQuotaDataByUsernameAndKey(username, tokenKey, startTime, endTime)
	}
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	// only select model_name, sum(count) as count, sum(quota) as quota, model_name, created_at from quota_data group by model_name, created_at;
	//err = DB.Table("quota_data").Where("created_at >= ? and created_at <= ?", startTime, endTime).Find(&quotaDatas).Error
	query := DB.Table("quota_data").
		Select("model_name, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, sum(cache_read_tokens) as cache_read_tokens, sum(cache_creation_tokens) as cache_creation_tokens, created_at").
		Where("created_at >= ? and created_at <= ?", startTime, endTime)
	if tokenKey != "" {
		query = query.Where("token_key = ?", tokenKey)
	}
	err = query.Group("model_name, created_at").Find(&quotaDatas).Error
	return quotaDatas, err
}

// GetQuotaDataByUsernameAndKey 精确按用户名 + 可选 token_key 过滤后做按模型聚合。
// 历史上 GetQuotaDataByUsername 仅按 username 过滤；新增 token_key 维度的精确筛选由本函数承担。
func GetQuotaDataByUsernameAndKey(username string, tokenKey string, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	query := DB.Table("quota_data").
		Select("user_id, username, model_name, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, sum(cache_read_tokens) as cache_read_tokens, sum(cache_creation_tokens) as cache_creation_tokens").
		Where("username = ? and created_at >= ? and created_at <= ?", username, startTime, endTime)
	if tokenKey != "" {
		query = query.Where("token_key = ?", tokenKey)
	}
	err = query.Group("user_id, username, model_name, created_at").Find(&quotaDatas).Error
	return quotaDatas, err
}

// QuotaDataTokenKeyEntry 是 quota_data 表里 distinct token_key 的展示条目。
// LabelParts 由前端用 i18n 模板拼装；这里只输出 raw parts 避免后端绑死语言。
// Value 是真正用于筛选的完整 token_key。
type QuotaDataTokenKeyEntry struct {
	// 拼装 Label 的零件：name + username + usage + 已删除标志。
	// 前端可读这些字段并用 i18n 模板渲染 "X · N calls" / "X (user) · N 次" 等。
	Label     string `json:"label"` // 已拼装好的 label 兜底（英文 default），前端 i18n 可覆盖
	Value     string `json:"value"`
	TokenName string `json:"token_name,omitempty"` // 关联的 token.name；空表示已删除
	UserName  string `json:"user_name,omitempty"`  // quota_data 里这条 key 的主用户名（多用户共用时取调用次数最多的）
	Usage     int64  `json:"usage,omitempty"`      // 该 key 在 quota_data 中的累计调用次数
	TokenUsed int64  `json:"token_used,omitempty"` // 该 key 在筛选时间窗口内累计消耗的 tokens
	Deleted   bool   `json:"deleted,omitempty"`    // true 表示 tokens 表里查不到该 key（已删除/失效）
}

// ListDistinctTokenKeys 返回 quota_data 表里 distinct token_key 列表，label 友好化。
//   - username 为空时返回全局 distinct token_key（admin 看板用）。
//   - username 非空时按 username 过滤（self 看板用）。
//   - keyword 非空时按 value LIKE 过滤（模糊搜索）。
//   - startTimestamp / endTimestamp 非 0 时按 quota_data.created_at 限定统计窗口
//     （看板筛选对话框选中的时间范围），0 表示不限。
//   - limit 用于防止一次返回过多，最多 200。
//
// Usage（调用次数）与 TokenUsed（消耗 tokens）只统计窗口内的行，
// 结果按调用次数降序排列（下拉框排序契约）。
//
// label 生成规则：
// - 关联到 tokens 表的：优先用 token.name 作 label，并附 "(username) · N"（次数不带单位，单位由前端 i18n 模板拼装）
// - 未关联（已删除）的：显示脱敏的 token_key + "(deleted)"
// - 多用户共用同一个 key 时，取 quota_data 中调用次数最多的用户名
func ListDistinctTokenKeys(username string, keyword string, startTimestamp int64, endTimestamp int64, limit int) ([]QuotaDataTokenKeyEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	// Step 1: 拿到 distinct token_key 列表 + 窗口内累计调用次数与 tokens 消耗
	type aggRow struct {
		TokenKey  string `gorm:"column:token_key"`
		Usage     int64  `gorm:"column:usage"`
		TokenUsed int64  `gorm:"column:token_used"`
	}
	query := DB.Table("quota_data").
		Select("token_key, sum(count) as usage, COALESCE(sum(token_used), 0) as token_used").
		Where("token_key <> ''")
	if username != "" {
		query = query.Where("username = ?", username)
	}
	if keyword != "" {
		query = query.Where("token_key LIKE ?", "%"+keyword+"%")
	}
	if startTimestamp != 0 {
		query = query.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		query = query.Where("created_at <= ?", endTimestamp)
	}
	query = query.Group("token_key")

	var aggRows []aggRow
	if err := query.Find(&aggRows).Error; err != nil {
		return nil, err
	}

	// Step 2: 拿这些 key 对应的 token name（一次批量查）
	keyToName := map[string]string{}
	if len(aggRows) > 0 {
		keys := make([]string, 0, len(aggRows))
		for _, r := range aggRows {
			keys = append(keys, r.TokenKey)
		}
		var tokenRows []struct {
			Key  string `gorm:"column:key"`
			Name string `gorm:"column:name"`
		}
		if err := DB.Table("tokens").Select("key, name").Where("key IN ?", keys).Find(&tokenRows).Error; err == nil {
			for _, tr := range tokenRows {
				keyToName[tr.Key] = tr.Name
			}
		}
	}

	// Step 3: 拿每个 key 调用次数最多的用户名（SQLite/MySQL/PG 通用 GROUP BY + ORDER BY 模拟）
	topUserMap := map[string]string{}
	if len(aggRows) > 0 {
		keys := make([]string, 0, len(aggRows))
		for _, r := range aggRows {
			keys = append(keys, r.TokenKey)
		}
		type kp struct {
			TokenKey string `gorm:"column:token_key"`
			Username string `gorm:"column:username"`
			Cnt      int64  `gorm:"column:cnt"`
		}
		var kps []kp
		topUserQuery := DB.Table("quota_data").
			Select("token_key, username, sum(count) as cnt").
			Where("token_key IN ?", keys)
		if startTimestamp != 0 {
			topUserQuery = topUserQuery.Where("created_at >= ?", startTimestamp)
		}
		if endTimestamp != 0 {
			topUserQuery = topUserQuery.Where("created_at <= ?", endTimestamp)
		}
		if err := topUserQuery.
			Group("token_key, username").
			Order("token_key asc, cnt desc, username asc").
			Find(&kps).Error; err == nil {
			for _, r := range kps {
				if _, ok := topUserMap[r.TokenKey]; !ok {
					topUserMap[r.TokenKey] = r.Username
				}
			}
		}
	}

	// Step 4: 组装结果
	entries := make([]QuotaDataTokenKeyEntry, 0, len(aggRows))
	for _, r := range aggRows {
		if r.TokenKey == "" {
			continue
		}
		name, known := keyToName[r.TokenKey]
		topUser := topUserMap[r.TokenKey]
		deleted := !known
		var label string
		switch {
		case known && name != "" && topUser != "":
			label = fmt.Sprintf("%s (%s) · %d", name, topUser, r.Usage)
		case known && name != "":
			label = fmt.Sprintf("%s · %d", name, r.Usage)
		case known:
			label = fmt.Sprintf("%s · %d", maskTokenKey(r.TokenKey), r.Usage)
		default:
			label = fmt.Sprintf("%s (deleted) · %d", maskTokenKey(r.TokenKey), r.Usage)
		}
		entries = append(entries, QuotaDataTokenKeyEntry{
			Label:     label,
			Value:     r.TokenKey,
			TokenName: name,
			UserName:  topUser,
			Usage:     r.Usage,
			TokenUsed: r.TokenUsed,
			Deleted:   deleted,
		})
	}

	// 按调用次数降序
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Usage > entries[j].Usage
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

// maskTokenKey 对 token key 做脱敏：保留前4+后4，中间用 *** 替代。
// 用于前端 autocomplete 列表展示，避免把完整 secret 暴露在 UI 上。
func maskTokenKey(key string) string {
	const head, tail = 4, 4
	if len(key) <= head+tail {
		return key
	}
	return key[:head] + "***" + key[len(key)-tail:]
}
