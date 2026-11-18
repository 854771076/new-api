package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func ListTokenQuotaRules(c *gin.Context) {
	page := common.GetPageQuery(c)
	rules, total, err := model.ListTokenQuotaRules(c.GetInt("id"), page.GetStartIdx(), page.GetPageSize(), c.Query("keyword"), c.Query("status"), c.Query("bind_status"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	page.SetTotal(int(total))
	page.SetItems(rules)
	common.ApiSuccess(c, page)
}

func SaveTokenQuotaRule(c *gin.Context) {
	var rule model.TokenQuotaRule
	if err := c.ShouldBindJSON(&rule); err != nil {
		common.ApiErrorT(c, "Invalid quota rule")
		return
	}
	rule.Id, rule.UserId = 0, c.GetInt("id")
	rule.CreatedAt, rule.UpdatedAt = 0, 0
	if c.Request.Method == "PUT" {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil || id <= 0 {
			common.ApiErrorT(c, "Invalid quota rule")
			return
		}
		rule.Id = id
	}
	if err := model.SaveTokenQuotaRule(&rule); err != nil {
		common.ApiError(c, err)
		return
	}
	tokenAuditParams(c)["id"] = rule.Id
	common.ApiSuccess(c, rule)
}

func DeleteTokenQuotaRule(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid quota rule")
		return
	}
	if err := model.DeleteTokenQuotaRule(c.GetInt("id"), id); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func BindTokenQuotaRule(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid quota rule")
		return
	}
	var input struct {
		RuleId *int `json:"rule_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.RuleId == nil {
		common.ApiErrorT(c, "Invalid quota rule")
		return
	}
	if err := model.SetTokenQuotaRuleBinding(c.GetInt("id"), id, *input.RuleId); err != nil {
		common.ApiError(c, err)
		return
	}
	tokenAuditParams(c)["quota_rule_id"] = *input.RuleId
	common.ApiSuccess(c, nil)
}
