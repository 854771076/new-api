package controller

import (
	"maps"
	"net/http"

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
	user, app, ok := oauthAppAccess(c, model.OAuthScopeAPIRead)
	if !ok {
		return
	}
	renderAppPricing(c, user, app)
}

func AdminAppPricing(c *gin.Context) {
	user, appID, ok := appProxyTarget(c)
	if !ok {
		return
	}
	app, err := model.GetOAuthAppByID(appID)
	if err != nil {
		appProxyError(c, err)
		return
	}
	if !app.IsEnabled() || !app.APIKeyEnabled || user.Status != common.UserStatusEnabled {
		appProxyError(c, model.ErrProxyIdentity)
		return
	}
	renderAppPricing(c, user, app)
}

func renderAppPricing(c *gin.Context, user *model.User, app *model.OAuthApp) {
	group := user.Group
	usableGroup := service.GetUserUsableGroups(group)
	visible := filterPricingByUsableGroups(model.GetPricing(), usableGroup)
	pricing := make([]model.Pricing, 0, len(visible))
	for _, entry := range visible {
		if app.AllowsModel(entry.ModelName) {
			pricing = append(pricing, entry)
		}
	}

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
		"success": true,
		"app_id":  app.Id, "user_id": user.Id, "identity_source": user.Source(), "quota_per_credit": app.QuotaPerCredit,
		"data":           pricing,
		"group_ratio":    groupRatio,
		"usable_group":   usableGroup,
		"quota_per_unit": common.QuotaPerUnit,
	})
}
