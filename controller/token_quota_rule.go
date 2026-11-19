package controller

import (
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// Specified-user routes retain the actor in context for authorization and audit.
func quotaRuleOwner(c *gin.Context, mutation bool) (int, bool) {
	if c.Param("user_id") == "" {
		return c.GetInt("id"), true
	}
	user, appID, ok := appProxyTarget(c)
	if !ok {
		return 0, false
	}
	if mutation {
		app, err := model.GetOAuthAppByID(appID)
		if err != nil || !app.IsEnabled() || !app.APIKeyEnabled || user.Status != common.UserStatusEnabled {
			appProxyError(c, model.ErrProxyIdentity)
			return 0, false
		}
	}
	return user.Id, true
}

func ListTokenQuotaRules(c *gin.Context) {
	owner, ok := quotaRuleOwner(c, false)
	if !ok {
		return
	}
	page := common.GetPageQuery(c)
	rules, total, err := model.ListTokenQuotaRules(owner, page.GetStartIdx(), page.GetPageSize(), c.Query("keyword"), c.Query("status"), c.Query("bind_status"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	now := time.Now().Unix()
	for i := range rules {
		rules[i].ValidityStatus = rules[i].ValidityAt(now)
	}
	common.ApiSuccess(c, gin.H{"items": rules, "total": total, "server_time": now, "page": page.Page, "page_size": page.PageSize})
}

func SaveTokenQuotaRule(c *gin.Context) {
	owner, ok := quotaRuleOwner(c, true)
	if !ok {
		return
	}
	var rule model.TokenQuotaRule
	var fields map[string]common.RawMessage
	if err := common.DecodeJson(c.Request.Body, &fields); err != nil || fields == nil {
		common.ApiErrorT(c, "Invalid quota rule")
		return
	}
	for name := range fields {
		switch name {
		case "id", "user_id", "created_at", "updated_at", "bound_token_count", "validity_status":
			// Legacy self clients send DTO metadata; it is never trusted.
			if c.Param("user_id") != "" {
				common.ApiErrorT(c, "Invalid quota rule")
				return
			}
		case "name", "description", "enabled", "quota_limit_enabled", "quota_period", "quota_limit", "time_window_enabled", "time_window_timezone", "allowed_weekdays", "valid_from", "valid_until":
		default:
			if c.Param("user_id") != "" {
				common.ApiErrorT(c, "Invalid quota rule")
				return
			}
		}
	}
	encoded, err := common.Marshal(fields)
	if err != nil || common.Unmarshal(encoded, &rule) != nil {
		common.ApiErrorT(c, "Invalid quota rule")
		return
	}
	omitted := []string{}
	for _, name := range []string{"valid_from", "valid_until"} {
		if _, exists := fields[name]; !exists {
			omitted = append(omitted, name)
		}
	}
	rule.Id, rule.UserId = 0, owner
	rule.CreatedAt, rule.UpdatedAt = 0, 0
	if c.Request.Method == "PUT" {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil || id <= 0 {
			common.ApiErrorT(c, "Invalid quota rule")
			return
		}
		rule.Id = id
	}
	if err := model.SaveTokenQuotaRule(&rule, omitted...); err != nil {
		common.ApiError(c, err)
		return
	}
	tokenAuditParams(c)["id"] = rule.Id
	rule.ValidityStatus = rule.ValidityAt(time.Now().Unix())
	if c.Param("user_id") != "" {
		recordManageAuditFor(c, owner, "app_proxy.quota_rule.save", map[string]any{"app_id": c.Query("app_id"), "rule_id": rule.Id, "valid_from": rule.ValidFrom, "valid_until": rule.ValidUntil})
	}
	common.ApiSuccess(c, rule)
}

func DeleteTokenQuotaRule(c *gin.Context) {
	owner, ok := quotaRuleOwner(c, false)
	if !ok {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid quota rule")
		return
	}
	if err := model.DeleteTokenQuotaRule(owner, id); err != nil {
		common.ApiError(c, err)
		return
	}
	if c.Param("user_id") != "" {
		recordManageAuditFor(c, owner, "app_proxy.quota_rule.delete", map[string]any{"app_id": c.Query("app_id"), "rule_id": id})
	}
	common.ApiSuccess(c, nil)
}

func BindTokenQuotaRule(c *gin.Context) {
	idText := c.Param("id")
	if c.Param("token_id") != "" {
		idText = c.Param("token_id")
	}
	id, err := strconv.Atoi(idText)
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid quota rule")
		return
	}
	var input struct {
		RuleId *int `json:"rule_id"`
	}
	var fields map[string]common.RawMessage
	if common.DecodeJson(c.Request.Body, &fields) != nil || len(fields) != 1 || fields["rule_id"] == nil {
		common.ApiErrorT(c, "Invalid quota rule")
		return
	}
	encoded, err := common.Marshal(fields)
	if err != nil || common.Unmarshal(encoded, &input) != nil || input.RuleId == nil {
		common.ApiErrorT(c, "Invalid quota rule")
		return
	}
	owner, ok := quotaRuleOwner(c, *input.RuleId != 0)
	if !ok {
		return
	}
	if c.Param("user_id") != "" {
		token, err := model.GetTokenByIds(id, owner)
		appID, _ := strconv.Atoi(c.Query("app_id"))
		if err != nil || token.AppId != appID {
			appProxyError(c, model.ErrProxyIdentity)
			return
		}
	}
	if err := model.SetTokenQuotaRuleBinding(owner, id, *input.RuleId); err != nil {
		appProxyError(c, err)
		return
	}
	tokenAuditParams(c)["quota_rule_id"] = *input.RuleId
	if c.Param("user_id") != "" {
		recordManageAuditFor(c, owner, "app_proxy.quota_rule.bind", map[string]any{"app_id": c.Query("app_id"), "token_id": id, "rule_id": *input.RuleId})
	}
	common.ApiSuccess(c, nil)
}
