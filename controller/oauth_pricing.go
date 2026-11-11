package controller

import (
	"maps"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// OAuthPricing handles GET /oauth/pricing.
//
// Requires Authorization: Bearer <oauth access token> with scope api:read.
// Returns the model pricing list visible to the token owner's group, together
// with the group ratio and quota-per-unit so an OAuth client can derive an
// estimated quota without hard-coding the conversion constant.
func OAuthPricing(c *gin.Context) {
	token := extractBearerToken(c)
	if token == "" {
		c.Header("WWW-Authenticate", "Bearer")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token", "error_description": "access token is required"})
		return
	}

	user, scope, err := service.ValidateOAuthAccessToken(token)
	if err != nil {
		c.Header("WWW-Authenticate", "Bearer error=\"invalid_token\"")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token", "error_description": "access token is invalid or expired"})
		return
	}

	scopeSet := make(map[string]struct{})
	for _, s := range strings.Fields(scope) {
		scopeSet[s] = struct{}{}
	}
	if _, ok := scopeSet[model.OAuthScopeAPIRead]; !ok {
		c.Header("WWW-Authenticate", "Bearer error=\"insufficient_scope\"")
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient_scope", "error_description": "api:read scope is required"})
		return
	}

	group := user.Group
	usableGroup := service.GetUserUsableGroups(group)
	pricing := filterPricingByUsableGroups(model.GetPricing(), usableGroup)

	groupRatio := map[string]float64{}
	maps.Copy(groupRatio, ratio_setting.GetGroupRatioCopy())
	for g := range groupRatio {
		ratio, ok := ratio_setting.GetGroupGroupRatio(group, g)
		if ok {
			groupRatio[g] = ratio
		}
	}
	for g := range ratio_setting.GetGroupRatioCopy() {
		if _, ok := usableGroup[g]; !ok {
			delete(groupRatio, g)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":        true,
		"data":           pricing,
		"group_ratio":    groupRatio,
		"usable_group":   usableGroup,
		"quota_per_unit": common.QuotaPerUnit,
	})
}
