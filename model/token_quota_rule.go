package model

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrTokenQuotaWindowDenied = common.NewMessage("This API key cannot be used on the current weekday")
	ErrTokenQuotaExhausted    = common.NewMessage("API key quota for the current period is exhausted")
)

// A template is reusable; each bound token has its own calendar-period budget.
type TokenQuotaRule struct {
	Id                 int    `json:"id"`
	UserId             int    `json:"user_id" gorm:"index"`
	Name               string `json:"name" gorm:"type:varchar(128)"`
	Description        string `json:"description" gorm:"type:varchar(255)"`
	Enabled            bool   `json:"enabled"`
	QuotaLimitEnabled  bool   `json:"quota_limit_enabled"`
	QuotaPeriod        string `json:"quota_period" gorm:"type:varchar(16)"`
	QuotaLimit         int64  `json:"quota_limit"`
	TimeWindowEnabled  bool   `json:"time_window_enabled"`
	TimeWindowTimezone string `json:"time_window_timezone" gorm:"type:varchar(32)"`
	AllowedWeekdays    string `json:"allowed_weekdays" gorm:"type:text"`
	CreatedAt          int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt          int64  `json:"updated_at" gorm:"autoUpdateTime"`
	BoundTokenCount    int64  `json:"bound_token_count" gorm:"-"`
}

type TokenQuotaRuleUsage struct {
	Id        int    `json:"id"`
	TokenId   int    `json:"token_id" gorm:"uniqueIndex:idx_token_rule_period,priority:1"`
	RuleId    int    `json:"rule_id" gorm:"uniqueIndex:idx_token_rule_period,priority:2"`
	PeriodKey string `json:"period_key" gorm:"type:varchar(64);uniqueIndex:idx_token_rule_period,priority:3"`
	UsedQuota int64  `json:"used_quota"`
}

func (rule *TokenQuotaRule) Validate() error {
	rule.Name = strings.TrimSpace(rule.Name)
	if rule.Name == "" || utf8.RuneCountInString(rule.Name) > 128 || utf8.RuneCountInString(rule.Description) > 255 {
		return common.NewMessage("Quota rule name is required (up to 128 characters); description allows up to 255 characters")
	}
	if rule.QuotaPeriod != "daily" && rule.QuotaPeriod != "weekly" && rule.QuotaPeriod != "monthly" {
		return common.NewMessage("Invalid quota period")
	}
	if rule.QuotaLimit < 0 || rule.QuotaLimit > int64(common.MaxWalletQuota) || rule.QuotaLimitEnabled && rule.QuotaLimit == 0 {
		return common.NewMessage("Quota rule limit must be a positive safe integer")
	}
	if rule.TimeWindowTimezone != "UTC" && rule.TimeWindowTimezone != "Asia/Shanghai" {
		return common.NewMessage("Quota rule timezone must be UTC or Asia/Shanghai")
	}
	var days []int
	if err := common.UnmarshalJsonStr(rule.AllowedWeekdays, &days); err != nil || days == nil || len(days) > 7 || rule.TimeWindowEnabled && len(days) == 0 {
		return common.NewMessage("Select valid weekdays for the quota rule")
	}
	for _, day := range days {
		if day < 1 || day > 7 {
			return common.NewMessage("Select valid weekdays for the quota rule")
		}
	}
	return nil
}

func ListTokenQuotaRules(userID, offset, limit int, keyword, status, bound string) ([]TokenQuotaRule, int64, error) {
	rules := make([]TokenQuotaRule, 0)
	query := DB.Model(&TokenQuotaRule{}).Where("user_id = ?", userID)
	if keyword != "" {
		pattern := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(keyword)
		query = query.Where("(name LIKE ? ESCAPE '!' OR description LIKE ? ESCAPE '!')", "%"+pattern+"%", "%"+pattern+"%")
	}
	if status == "enabled" || status == "disabled" {
		query = query.Where("enabled = ?", status == "enabled")
	}
	if bound == "bound" || bound == "unbound" {
		tokens := DB.Model(&Token{}).Select("quota_rule_id").Where("user_id = ? AND quota_rule_id > 0", userID)
		if bound == "bound" {
			query = query.Where("id IN (?)", tokens)
		} else {
			query = query.Where("id NOT IN (?)", tokens)
		}
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&rules).Error; err != nil {
		return nil, 0, err
	}
	ids := make([]int, 0, len(rules))
	for _, rule := range rules {
		ids = append(ids, rule.Id)
	}
	if len(ids) == 0 {
		return rules, total, nil
	}
	var counts []struct {
		QuotaRuleId int
		Count       int64
	}
	if err := DB.Model(&Token{}).Select("quota_rule_id, COUNT(*) AS count").Where("user_id = ? AND quota_rule_id IN ?", userID, ids).Group("quota_rule_id").Scan(&counts).Error; err != nil {
		return nil, 0, err
	}
	countsByID := make(map[int]int64, len(counts))
	for _, count := range counts {
		countsByID[count.QuotaRuleId] = count.Count
	}
	for i := range rules {
		rules[i].BoundTokenCount = countsByID[rules[i].Id]
	}
	return rules, total, nil
}

func SaveTokenQuotaRule(rule *TokenQuotaRule) error {
	if rule.UserId <= 0 {
		return common.NewMessage("Invalid quota rule")
	}
	if err := rule.Validate(); err != nil {
		return err
	}
	if rule.Id == 0 {
		return DB.Create(rule).Error
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var existing TokenQuotaRule
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", rule.Id, rule.UserId).First(&existing).Error; err != nil {
			return err
		}
		return tx.Model(&existing).Select("name", "description", "enabled", "quota_limit_enabled", "quota_period", "quota_limit", "time_window_enabled", "time_window_timezone", "allowed_weekdays", "updated_at").Updates(rule).Error
	})
}

func DeleteTokenQuotaRule(userID, ruleID int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var rule TokenQuotaRule
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", ruleID, userID).First(&rule).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&Token{}).Where("quota_rule_id = ?", ruleID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return common.NewMessage("Unbind all API keys before deleting this quota rule")
		}
		// Usage survives deletion so in-flight requests can still settle/refund.
		return tx.Delete(&rule).Error
	})
}

func SetTokenQuotaRuleBinding(userID, tokenID, ruleID int) error {
	if ruleID < 0 {
		return common.NewMessage("Invalid quota rule")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if ruleID > 0 {
			var rule TokenQuotaRule
			if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", ruleID, userID).First(&rule).Error; err != nil {
				return err
			}
		}
		var token Token
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", tokenID, userID).First(&token).Error; err != nil {
			return err
		}
		if err := invalidateTokenCacheForMutation(token.Key); err != nil {
			return err
		}
		return tx.Model(&token).Update("quota_rule_id", ruleID).Error
	})
}

func tokenQuotaRulePeriod(now time.Time, timezone, period string) string {
	loc, _ := time.LoadLocation(timezone) // Validate restricts this to bundled zones.
	if loc == nil {
		loc = time.UTC
	}
	now = now.In(loc)
	key := now.Format("2006-01-02")
	switch period {
	case "weekly":
		year, week := now.ISOWeek()
		key = fmt.Sprintf("%04d-W%02d", year, week)
	case "monthly":
		key = now.Format("2006-01")
	}
	return timezone + ":" + period + ":" + key
}

// requireRemainingQuota also rejects exhausted zero-estimate paid requests; free checks and settlement lookups pass false.
func ReserveTokenQuotaRule(tokenID int, amount int64, requireRemainingQuota bool) (*relaycommon.TokenQuotaReservation, error) {
	if tokenID <= 0 {
		return nil, nil
	}
	if amount < 0 || amount > int64(common.MaxWalletQuota) {
		return nil, errors.New("invalid quota reservation")
	}
	reservation := &relaycommon.TokenQuotaReservation{}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var token Token
		if err := tx.First(&token, tokenID).Error; err != nil {
			return err
		}
		if token.QuotaRuleId == 0 {
			return nil
		}
		var rule TokenQuotaRule
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", token.QuotaRuleId, token.UserId).First(&rule).Error; err != nil {
			return err
		}
		if !rule.Enabled {
			return nil
		}
		reservation.RuleID = rule.Id
		now := time.Now()
		if rule.TimeWindowEnabled {
			loc, err := time.LoadLocation(rule.TimeWindowTimezone)
			if err != nil {
				return err
			}
			day := int(now.In(loc).Weekday())
			if day == 0 {
				day = 7
			}
			var days []int
			if err := common.UnmarshalJsonStr(rule.AllowedWeekdays, &days); err != nil {
				return err
			}
			if !slices.Contains(days, day) {
				return ErrTokenQuotaWindowDenied
			}
		}
		if !rule.QuotaLimitEnabled {
			return nil
		}
		usage := TokenQuotaRuleUsage{TokenId: tokenID, RuleId: rule.Id, PeriodKey: tokenQuotaRulePeriod(now, rule.TimeWindowTimezone, rule.QuotaPeriod)}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&usage).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("token_id = ? AND rule_id = ? AND period_key = ?", tokenID, rule.Id, usage.PeriodKey).First(&usage).Error; err != nil {
			return err
		}
		if requireRemainingQuota && usage.UsedQuota >= rule.QuotaLimit {
			return ErrTokenQuotaExhausted
		}
		if amount > 0 {
			result := tx.Model(&usage).Where("used_quota <= ?", rule.QuotaLimit-amount).Update("used_quota", gorm.Expr("used_quota + ?", amount))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrTokenQuotaExhausted
			}
		}
		reservation.UsageID, reservation.Amount = usage.Id, amount
		return nil
	})
	if err != nil {
		return nil, err
	}
	if reservation.RuleID == 0 {
		return nil, nil
	}
	return reservation, nil
}

// enforceLimit is only for admission/reservation. Settlement records the actual
// cost even when upstream exceeds the estimate; the next request is then denied.
func AdjustTokenQuotaRuleUsage(reservation *relaycommon.TokenQuotaReservation, delta int64, enforceLimit bool) error {
	if reservation == nil || reservation.UsageID == 0 || delta == 0 {
		return nil
	}
	if delta > int64(common.MaxWalletQuota) || delta < -int64(common.MaxWalletQuota) {
		return errors.New("invalid quota adjustment")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&TokenQuotaRuleUsage{}).Where("id = ?", reservation.UsageID)
		if enforceLimit && delta > 0 {
			var rule TokenQuotaRule
			if err := lockForUpdate(tx).First(&rule, reservation.RuleID).Error; err != nil {
				return err
			}
			query = query.Where("used_quota <= ?", rule.QuotaLimit-delta)
		} else if delta > 0 {
			query = query.Where("used_quota <= ?", int64(^uint64(0)>>1)-delta)
		}
		// Refunds never make usage negative (portable CASE, no GREATEST).
		result := query.Update("used_quota", gorm.Expr("CASE WHEN used_quota < ? THEN 0 ELSE used_quota + ? END", -delta, delta))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrTokenQuotaExhausted
		}
		return nil
	})
}

func AttachTokenQuotaRuleUsage(tokens []*Token) error {
	ids := make([]int, 0)
	for _, token := range tokens {
		if token.QuotaRuleId > 0 {
			ids = append(ids, token.QuotaRuleId)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var rules []TokenQuotaRule
	if err := DB.Where("id IN ?", ids).Find(&rules).Error; err != nil {
		return err
	}
	now := time.Now()
	byID := make(map[int]TokenQuotaRule, len(rules))
	keys := make([]string, 0, len(rules))
	for _, rule := range rules {
		byID[rule.Id] = rule
		keys = append(keys, tokenQuotaRulePeriod(now, rule.TimeWindowTimezone, rule.QuotaPeriod))
	}
	tokenIDs := make([]int, 0, len(tokens))
	for _, token := range tokens {
		tokenIDs = append(tokenIDs, token.Id)
	}
	var usages []TokenQuotaRuleUsage
	if err := DB.Where("token_id IN ? AND period_key IN ?", tokenIDs, keys).Find(&usages).Error; err != nil {
		return err
	}
	type usageKey struct {
		tokenID, ruleID int
		period          string
	}
	usageByKey := make(map[usageKey]int64, len(usages))
	for _, usage := range usages {
		usageByKey[usageKey{usage.TokenId, usage.RuleId, usage.PeriodKey}] = usage.UsedQuota
	}
	for _, token := range tokens {
		rule, ok := byID[token.QuotaRuleId]
		if !ok || rule.UserId != token.UserId {
			continue
		}
		token.QuotaRule = &rule
		key := tokenQuotaRulePeriod(now, rule.TimeWindowTimezone, rule.QuotaPeriod)
		token.QuotaRuleUsed = usageByKey[usageKey{token.Id, rule.Id, key}]
	}
	return nil
}
