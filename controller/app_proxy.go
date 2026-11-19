package controller

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func ProvisionAppProxyUser(c *gin.Context) {
	var input struct {
		AppId   int    `json:"app_id"`
		Issuer  string `json:"issuer"`
		Subject string `json:"subject"`
	}
	if err := common.DecodeJson(c.Request.Body, &input); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	user, err := model.ProvisionAppProxyUser(input.AppId, input.Issuer, input.Subject)
	if err != nil {
		appProxyError(c, err)
		return
	}
	recordManageAuditFor(c, user.Id, "app_proxy.provision", map[string]any{"app_id": user.OwnerAppId})
	common.ApiSuccess(c, gin.H{"user_id": user.Id, "app_id": user.OwnerAppId, "identity_source": user.Source(), "status": user.Status})
}

func appProxyError(c *gin.Context, err error) {
	status, code := http.StatusBadRequest, "invalid_request"
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, model.ErrAppOperationConflict):
		status, code = http.StatusConflict, "operation_conflict"
	case errors.Is(err, model.ErrProxyIdentity):
		status, code = http.StatusForbidden, "invalid_app_ownership"
	case errors.Is(err, model.ErrLimitedKeyRotation):
		status, code = http.StatusConflict, "shared_key_budget_unavailable"
	case errors.Is(err, model.ErrRotationCancelled):
		status, code = http.StatusConflict, "rotation_cancelled"
	case errors.Is(err, model.ErrProxySubscriptionUnsupported):
		status, code = http.StatusBadRequest, "subscription_unsupported"
	default:
		common.ApiError(c, err)
		return
	}
	c.JSON(status, gin.H{"success": false, "code": code, "message": err.Error()})
}

func AdminAppWallet(c *gin.Context) {
	userID, err := strconv.Atoi(c.Param("user_id"))
	appID, appErr := strconv.Atoi(c.Query("app_id"))
	if err != nil || appErr != nil || userID <= 0 || appID < 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	wallet, err := model.ReadAppWallet(userID, appID)
	if err != nil {
		appProxyError(c, err)
		return
	}
	common.ApiSuccess(c, wallet)
}

func SelfAppWallet(c *gin.Context) {
	appID, err := strconv.Atoi(c.Query("app_id"))
	if err != nil || appID < 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	wallet, err := model.ReadAppWallet(c.GetInt("id"), appID)
	if err != nil {
		appProxyError(c, err)
		return
	}
	common.ApiSuccess(c, wallet)
}

func OAuthAppWallet(c *gin.Context) {
	user, app, ok := oauthAppAccess(c, model.OAuthScopeAPIRead)
	if !ok {
		return
	}
	if requested := c.Query("app_id"); requested != "" && requested != strconv.Itoa(app.Id) {
		appProxyError(c, model.ErrProxyIdentity)
		return
	}
	wallet, err := model.ReadAppWallet(user.Id, app.Id)
	if err != nil {
		appProxyError(c, err)
		return
	}
	common.ApiSuccess(c, wallet)
}

func appProxyTarget(c *gin.Context) (*model.User, int, bool) {
	userID, err := strconv.Atoi(c.Param("user_id"))
	appID, appErr := strconv.Atoi(c.Query("app_id"))
	if err != nil || appErr != nil || userID <= 0 || appID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return nil, 0, false
	}
	user, err := model.GetUserById(userID, true)
	if err != nil {
		appProxyError(c, err)
		return nil, 0, false
	}
	if !user.IsProxy() || model.ValidateUserAppScope(user, appID) != nil {
		appProxyError(c, model.ErrProxyIdentity)
		return nil, 0, false
	}
	return user, appID, true
}

func CreateAppProxyKey(c *gin.Context) {
	user, appID, ok := appProxyTarget(c)
	if !ok {
		return
	}
	var input struct {
		Name               string  `json:"name"`
		ExpiredTime        int64   `json:"expired_time"`
		RemainQuota        int     `json:"remain_quota"`
		UnlimitedQuota     bool    `json:"unlimited_quota"`
		ModelLimitsEnabled bool    `json:"model_limits_enabled"`
		ModelLimits        string  `json:"model_limits"`
		AllowIps           *string `json:"allow_ips"`
		Group              string  `json:"group"`
		OldTokenId         int     `json:"old_token_id"`
		QuotaRuleId        int     `json:"quota_rule_id"`
	}
	input.ExpiredTime = -1
	var fields map[string]common.RawMessage
	if err := common.DecodeJson(c.Request.Body, &fields); err != nil || fields == nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	for name := range fields {
		switch name {
		case "name", "expired_time", "remain_quota", "unlimited_quota", "model_limits_enabled", "model_limits", "allow_ips", "group", "old_token_id", "quota_rule_id":
		default:
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	}
	encoded, err := common.Marshal(fields)
	if err != nil || common.Unmarshal(encoded, &input) != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if len(input.Name) > 100 || input.RemainQuota < 0 || input.RemainQuota > maxTokenQuota() || (input.ExpiredTime != -1 && input.ExpiredTime <= common.GetTimestamp()) || len(input.ModelLimits) > 65535 || input.OldTokenId < 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if input.Group != "" && input.Group != user.Group {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if input.AllowIps != nil {
		if len(*input.AllowIps) > 4096 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		probe := model.Token{AllowIps: input.AllowIps}
		for _, ip := range probe.GetIpLimits() {
			if _, _, cidrErr := net.ParseCIDR(ip); net.ParseIP(ip) == nil && cidrErr != nil {
				common.ApiErrorT(c, "Invalid parameters")
				return
			}
		}
	}
	token := &model.Token{Name: input.Name, ExpiredTime: input.ExpiredTime, RemainQuota: input.RemainQuota, UnlimitedQuota: input.UnlimitedQuota, ModelLimitsEnabled: input.ModelLimitsEnabled, ModelLimits: input.ModelLimits, AllowIps: input.AllowIps, Group: input.Group}
	token.QuotaRuleId = input.QuotaRuleId
	token, err = model.CreateAppProxyKey(user.Id, appID, strings.TrimSpace(c.GetHeader("Idempotency-Key")), token, input.OldTokenId)
	if err != nil {
		appProxyError(c, err)
		return
	}
	recordManageAuditFor(c, user.Id, "app_proxy.key.create", map[string]any{"app_id": appID, "token_id": token.Id, "old_token_id": input.OldTokenId})
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, token)
}

func ListAppProxyKeys(c *gin.Context) {
	user, appID, ok := appProxyTarget(c)
	if !ok {
		return
	}
	page := common.GetPageQuery(c)
	query := model.DB.Model(&model.Token{}).Where("user_id = ? AND app_id = ?", user.Id, appID)
	if status := c.Query("status"); status != "" {
		value, err := strconv.Atoi(status)
		if err != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		query = query.Where("status = ?", value)
	}
	var count int64
	var tokens []*model.Token
	if err := query.Count(&count).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if err := query.Order("id desc").Offset(page.GetStartIdx()).Limit(page.GetPageSize()).Find(&tokens).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	for _, token := range tokens {
		token.Key = token.GetMaskedKey()
	}
	if err := model.AttachTokenQuotaRuleUsage(tokens); err != nil {
		common.ApiError(c, err)
		return
	}
	ids := make([]int, 0, len(tokens))
	for _, token := range tokens {
		ids = append(ids, token.Id)
	}
	var operations []model.AppKeyOperation
	if len(ids) > 0 {
		if err := model.DB.Where("token_id IN ? AND old_token_id > 0", ids).Find(&operations).Error; err != nil {
			common.ApiError(c, err)
			return
		}
	}
	for _, op := range operations {
		for _, token := range tokens {
			if token.Id == op.TokenId {
				token.RotationStatus = "pending"
				if op.Committed {
					token.RotationStatus = "committed"
				}
				if op.Cancelled {
					token.RotationStatus = "cancelled"
				}
			}
		}
	}
	common.ApiSuccess(c, gin.H{"items": tokens, "total": count, "server_time": common.GetTimestamp()})
}

func UpdateAppProxyKey(c *gin.Context) {
	user, appID, ok := appProxyTarget(c)
	if !ok {
		return
	}
	tokenID, err := strconv.Atoi(c.Param("token_id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	token, err := model.GetTokenByIds(tokenID, user.Id)
	if err != nil || token.AppId != appID {
		appProxyError(c, model.ErrProxyIdentity)
		return
	}
	var input struct {
		Status      int     `json:"status"`
		Name        *string `json:"name"`
		RemainQuota *int    `json:"remain_quota"`
	}
	if err := common.DecodeJson(c.Request.Body, &input); err != nil || (input.Status != common.TokenStatusEnabled && input.Status != common.TokenStatusDisabled) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if input.Status == common.TokenStatusEnabled {
		app, err := model.GetOAuthAppByID(appID)
		if err != nil || !app.IsEnabled() || !app.APIKeyEnabled || user.Status != common.UserStatusEnabled {
			appProxyError(c, model.ErrProxyIdentity)
			return
		}
	}
	if input.Name != nil {
		if len(*input.Name) > 100 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		token.Name = *input.Name
	}
	if input.RemainQuota != nil {
		if *input.RemainQuota < 0 || *input.RemainQuota > maxTokenQuota() {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		token.RemainQuota = *input.RemainQuota
	}
	token.Status = input.Status
	updates := map[string]any{"status": token.Status}
	if input.Name != nil {
		updates["name"] = token.Name
	}
	if input.RemainQuota != nil {
		updates["remain_quota"] = token.RemainQuota
	}
	if err := model.UpdateAppProxyKeyPolicy(token, updates); err != nil {
		appProxyError(c, err)
		return
	}
	recordManageAuditFor(c, user.Id, "app_proxy.key.update", map[string]any{"app_id": appID, "token_id": tokenID, "status": input.Status})
	common.ApiSuccess(c, nil)
}

func CancelAppProxyKeyRotation(c *gin.Context) {
	user, appID, ok := appProxyTarget(c)
	if !ok {
		return
	}
	tokenID, err := strconv.Atoi(c.Param("token_id"))
	if err != nil || tokenID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := model.CancelAppProxyKeyRotation(user.Id, appID, tokenID); err != nil {
		appProxyError(c, err)
		return
	}
	recordManageAuditFor(c, user.Id, "app_proxy.key.rotation_cancel", map[string]any{"app_id": appID, "token_id": tokenID})
	common.ApiSuccess(c, gin.H{"rotation_status": "cancelled"})
}

func CommitAppProxyKeyRotation(c *gin.Context) {
	user, appID, ok := appProxyTarget(c)
	if !ok {
		return
	}
	tokenID, err := strconv.Atoi(c.Param("token_id"))
	var input struct {
		Key string `json:"replacement_key"`
	}
	if err != nil || common.DecodeJson(c.Request.Body, &input) != nil || input.Key == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := model.CommitAppProxyKeyRotation(user.Id, appID, tokenID, strings.TrimPrefix(input.Key, "sk-")); err != nil {
		appProxyError(c, err)
		return
	}
	recordManageAuditFor(c, user.Id, "app_proxy.key.rotation_commit", map[string]any{"app_id": appID, "token_id": tokenID})
	common.ApiSuccess(c, nil)
}

func AdminAppProxyCatalog(c *gin.Context) {
	user, appID, ok := appProxyTarget(c)
	if !ok {
		return
	}
	app, err := model.GetOAuthAppByID(appID)
	if err != nil || !app.IsEnabled() || !app.APIKeyEnabled || user.Status != common.UserStatusEnabled {
		appProxyError(c, model.ErrProxyIdentity)
		return
	}
	products, err := model.ListEnabledAppProducts(appID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	for i := range products {
		products[i].Credits = app.Credits(int(products[i].QuotaAmount))
	}
	methods := []string{}
	if isWaffoPancakeTopUpEnabled() && paymentComplianceError() == nil {
		methods = append(methods, model.PaymentMethodWaffoPancake)
	}
	c.JSON(http.StatusOK, gin.H{"app_id": appID, "subscription_supported": false, "plans": []any{}, "products": products, "payment_methods": methods})
}

func AdminAppProxyLogs(c *gin.Context) {
	user, appID, ok := appProxyTarget(c)
	if !ok {
		return
	}
	renderAppLogs(c, user.Id, appID)
}
