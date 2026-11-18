package controller

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
)

type appCheckoutWriter struct {
	gin.ResponseWriter
	body   bytes.Buffer
	status int
}

func (w *appCheckoutWriter) Write(data []byte) (int, error)       { return w.body.Write(data) }
func (w *appCheckoutWriter) WriteString(data string) (int, error) { return w.body.WriteString(data) }
func (w *appCheckoutWriter) WriteHeader(status int)               { w.status = status }
func (w *appCheckoutWriter) WriteHeaderNow()                      {}
func (w *appCheckoutWriter) Status() int                          { return w.status }
func (w *appCheckoutWriter) Size() int                            { return w.body.Len() }
func (w *appCheckoutWriter) Written() bool                        { return w.body.Len() > 0 }

// beginAppCheckout buffers the response until its recovery record is durable.
// The original payment handlers still perform validation and provider dispatch.
func beginAppCheckout(c *gin.Context, user *model.User, appID int, kind, method string, input any) (func(), bool) {
	if err := model.ValidateUserAppScope(user, appID); err != nil {
		appProxyError(c, err)
		return nil, false
	}
	if user.Status != common.UserStatusEnabled {
		appProxyError(c, model.ErrProxyIdentity)
		return nil, false
	}
	if user.IsProxy() && (kind == "subscription" || method != model.PaymentMethodWaffoPancake) {
		appProxyError(c, model.ErrProxySubscriptionUnsupported)
		return nil, false
	}
	var ratio *int64
	if appID > 0 {
		app, err := model.GetOAuthAppByID(appID)
		if err != nil {
			common.ApiError(c, err)
			return nil, false
		}
		if !app.IsEnabled() || (user.IsProxy() && !app.APIKeyEnabled) || (!user.IsProxy() && !app.OAuthAllowed()) {
			appProxyError(c, model.ErrProxyIdentity)
			return nil, false
		}
		ratio = app.QuotaPerCredit
	}
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if key == "" {
		var err error
		key, err = common.GenerateKey()
		if err != nil {
			common.ApiError(c, err)
			return nil, false
		}
	}
	actor := fmt.Sprintf("user:%d", user.Id)
	if admin := c.GetInt("id"); c.GetInt("role") >= common.RoleAdminUser && admin != user.Id {
		actor = fmt.Sprintf("admin:%d", admin)
	}
	intent := &model.AppCheckout{Actor: actor, IdentitySource: user.Source(), UserId: user.Id, AppId: appID, Kind: kind, PaymentMethod: method, QuotaPerCredit: ratio, Currency: "USD", Money: "0"}
	if kind == "topup" {
		body, ok := input.(scopedCheckoutRequest)
		if !ok {
			common.ApiErrorT(c, "Invalid parameters")
			return nil, false
		}
		quota, err := validateTopUpQuota(body.Amount)
		if err != nil {
			common.ApiError(c, err)
			return nil, false
		}
		intent.Quota = quota
	}
	created, err := model.ReserveAppCheckout(intent, key, input)
	if err != nil {
		appProxyError(c, err)
		return nil, false
	}
	c.Header("Cache-Control", "no-store")
	if !created {
		if intent.Response != "" {
			c.Data(intent.ResponseStatus, "application/json", []byte(intent.Response))
			return nil, false
		}
		c.JSON(http.StatusAccepted, gin.H{"order_id": intent.OrderId, "status": "processing", "user_id": intent.UserId, "app_id": intent.AppId})
		return nil, false
	}
	c.Request = c.Request.WithContext(model.WithAppCheckout(c.Request.Context(), intent))
	original := c.Writer
	buffer := &appCheckoutWriter{ResponseWriter: original, status: http.StatusOK}
	c.Writer = buffer
	return func() {
		c.Writer = original
		var response map[string]any
		if common.Unmarshal(buffer.body.Bytes(), &response) != nil {
			response = map[string]any{"status": "reconciliation_required"}
		}
		response["order_id"], response["user_id"], response["app_id"] = intent.OrderId, intent.UserId, intent.AppId
		stored, err := model.ReadAppCheckout(user.Id, appID, intent.OrderId)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"order_id": intent.OrderId, "status": "reconciliation_required"})
			return
		}
		if stored.SnapshotReady {
			response["quota_per_credit"], response["credited_quota"], response["currency"], response["money"] = stored.QuotaPerCredit, stored.Quota, stored.Currency, stored.Money
			response["credits"] = (&model.OAuthApp{QuotaPerCredit: stored.QuotaPerCredit}).Credits(stored.Quota)
		}
		encoded, err := common.Marshal(response)
		if err == nil {
			err = model.SaveAppCheckoutResponse(intent, buffer.status, encoded)
		}
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"order_id": intent.OrderId, "status": "reconciliation_required", "message": "checkout response could not be saved"})
			return
		}
		c.Data(buffer.status, "application/json", encoded)
	}, true
}

type scopedCheckoutRequest struct {
	AppId         int    `json:"app_id"`
	Kind          string `json:"purchase_type"`
	Amount        int64  `json:"amount"`
	ProductId     int    `json:"product_id"`
	PlanId        int    `json:"plan_id"`
	PaymentMethod string `json:"payment_method"`
}

func ScopedAppCheckout(c *gin.Context) {
	var body scopedCheckoutRequest
	var payload map[string]any
	if common.DecodeJson(c.Request.Body, &payload) != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	encoded, err := common.Marshal(payload)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	_, hasApp := payload["app_id"]
	if common.Unmarshal(encoded, &body) != nil || !hasApp || payload["app_id"] == nil || body.AppId < 0 || strings.TrimSpace(c.GetHeader("Idempotency-Key")) == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	userID := c.GetInt("id")
	if param := c.Param("user_id"); param != "" {
		var err error
		userID, err = strconv.Atoi(param)
		if err != nil || userID <= 0 || body.PaymentMethod != model.PaymentMethodWaffoPancake || body.Kind == "subscription" {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	}
	user, err := model.GetUserById(userID, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	dispatchScopedCheckout(c, user, body)
}

func dispatchScopedCheckout(c *gin.Context, user *model.User, body scopedCheckoutRequest) {
	if body.PaymentMethod == "" {
		body.PaymentMethod = model.PaymentMethodWaffoPancake
	}
	if body.Kind != "topup" && body.Kind != "product" && body.Kind != "subscription" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if body.Kind == "topup" && (body.Amount <= 0 || (body.AppId == 0 && body.PaymentMethod == model.PaymentMethodBalance)) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := paymentComplianceError(); err != nil {
		oauthCheckoutError(c, err)
		return
	}
	finish, ok := beginAppCheckout(c, user, body.AppId, body.Kind, body.PaymentMethod, body)
	if !ok {
		return
	}
	defer finish()
	app := &model.OAuthApp{Id: body.AppId}
	switch body.Kind {
	case "topup":
		dispatchAppTopup(c, user, app, body)
	case "product":
		dispatchAppProduct(c, user, app, oauthProductCheckoutRequest{AppId: body.AppId, ProductId: body.ProductId, PaymentMethod: body.PaymentMethod})
	case "subscription":
		dispatchAppSubscription(c, user, app, oauthSubscriptionCheckoutRequest{AppId: body.AppId, PlanId: body.PlanId, PaymentMethod: body.PaymentMethod})
	}
}

func ScopedAppOrder(c *gin.Context) {
	userID := c.GetInt("id")
	if param := c.Param("user_id"); param != "" {
		var err error
		userID, err = strconv.Atoi(param)
		if err != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	}
	appID, err := strconv.Atoi(c.Query("app_id"))
	if err != nil || appID < 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	order, err := model.ReadAppCheckout(userID, appID, c.Param("order_id"))
	if err != nil {
		appProxyError(c, err)
		return
	}
	common.ApiSuccess(c, order)
}

func OAuthAppOrder(c *gin.Context) {
	user, app, ok := oauthAppAccess(c, model.OAuthScopeAPIRead)
	if !ok {
		return
	}
	if appID := c.Query("app_id"); appID != "" && appID != strconv.Itoa(app.Id) {
		appProxyError(c, model.ErrProxyIdentity)
		return
	}
	order, err := model.ReadAppCheckout(user.Id, app.Id, c.Param("order_id"))
	if err != nil {
		appProxyError(c, err)
		return
	}
	common.ApiSuccess(c, order)
}

func ScopedAppOrderList(c *gin.Context) {
	userID := c.GetInt("id")
	if param := c.Param("user_id"); param != "" {
		var err error
		userID, err = strconv.Atoi(param)
		if err != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	}
	appID, err := strconv.Atoi(c.Query("app_id"))
	if err != nil || appID < 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	page := common.GetPageQuery(c)
	items, total, err := model.ListScopedAppOrders(userID, appID, c.Query("keyword"), page)
	if err != nil {
		appProxyError(c, err)
		return
	}
	page.SetTotal(int(total))
	page.SetItems(items)
	common.ApiSuccess(c, page)
}

func OAuthAppOrderList(c *gin.Context) {
	user, app, ok := oauthAppAccess(c, model.OAuthScopeAPIRead)
	if !ok {
		return
	}
	if requested := c.Query("app_id"); requested != "" && requested != strconv.Itoa(app.Id) {
		appProxyError(c, model.ErrProxyIdentity)
		return
	}
	page := common.GetPageQuery(c)
	items, total, err := model.ListScopedAppOrders(user.Id, app.Id, c.Query("keyword"), page)
	if err != nil {
		appProxyError(c, err)
		return
	}
	page.SetTotal(int(total))
	page.SetItems(items)
	common.ApiSuccess(c, page)
}

func SelfAppAccessList(c *gin.Context) {
	apps, err := model.UserAppAccessList(c.GetInt("id"), c.Query("include_historical") == "true")
	if err != nil {
		appProxyError(c, err)
		return
	}
	common.ApiSuccess(c, apps)
}

func SelfAppCatalog(c *gin.Context) {
	appID, err := strconv.Atoi(c.Query("app_id"))
	if err != nil || appID < 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	userID := c.GetInt("id")
	if err := model.RequireNativeUserTx(model.DB, userID); err != nil {
		appProxyError(c, err)
		return
	}
	plans, err := model.ListEnabledSubscriptionPlans(appID)
	if err != nil {
		appProxyError(c, err)
		return
	}
	subscriptions, err := model.GetAllUserSubscriptions(userID, appID)
	if err != nil {
		appProxyError(c, err)
		return
	}
	products, err := model.ListEnabledAppProducts(appID)
	if err != nil {
		appProxyError(c, err)
		return
	}
	if appID > 0 {
		app, err := model.GetOAuthAppByID(appID)
		if err != nil {
			appProxyError(c, err)
			return
		}
		for i := range products {
			products[i].Credits = app.Credits(int(products[i].QuotaAmount))
		}
		for i := range plans {
			if plans[i].TotalAmount > 0 {
				plans[i].Credits = app.Credits(int(plans[i].TotalAmount))
			}
		}
	}
	common.ApiSuccess(c, gin.H{"app_id": appID, "plans": plans, "subscriptions": subscriptions, "products": products})
}

// Preview is scoped to the same user/App as checkout and has no side effects.
func ScopedAppTopupPreview(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}
	var body scopedCheckoutRequest
	if common.DecodeJson(c.Request.Body, &body) != nil || body.AppId <= 0 || body.Amount <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	userID := c.GetInt("id")
	if id := c.Param("user_id"); id != "" {
		var err error
		userID, err = strconv.Atoi(id)
		if err != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	}
	user, err := model.GetUserById(userID, true)
	if err != nil {
		appProxyError(c, err)
		return
	}
	if err := model.ValidateUserAppScope(user, body.AppId); err != nil {
		appProxyError(c, err)
		return
	}
	app, err := model.GetOAuthAppByID(body.AppId)
	if err != nil {
		appProxyError(c, err)
		return
	}
	if !app.IsEnabled() || user.Status != common.UserStatusEnabled || (user.IsProxy() && !app.APIKeyEnabled) || (!user.IsProxy() && !app.OAuthAllowed()) {
		appProxyError(c, model.ErrProxyIdentity)
		return
	}
	if body.PaymentMethod != model.PaymentMethodWaffoPancake {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if !isWaffoPancakeTopUpEnabled() {
		common.ApiErrorT(c, "Waffo Pancake configuration is incomplete")
		return
	}
	if body.Amount < int64(setting.WaffoPancakeMinTopUp) {
		common.ApiError(c, common.NewMessage("Top-up amount cannot be less than {{min}}", map[string]any{"min": setting.WaffoPancakeMinTopUp}))
		return
	}
	quota, err := validateTopUpQuota(body.Amount)
	if err != nil {
		appProxyError(c, err)
		return
	}
	money := getWaffoPancakePayMoney(body.Amount, user.Group)
	if math.IsNaN(money) || math.IsInf(money, 0) || money < 0.01 {
		common.ApiErrorT(c, "Payment amount is too low")
		return
	}
	if err := model.ValidateAppTopUpQuotaCapacity(userID, body.AppId, quota); err != nil {
		appProxyError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"user_id": userID, "app_id": app.Id, "money": money, "currency": "USD", "quota": quota, "credits": app.Credits(quota), "quota_per_credit": app.QuotaPerCredit})
}

// OAuth preview derives both principals from the token, not browser fields.
func OAuthAppTopupPreview(c *gin.Context) {
	user, app, ok := oauthAppAccess(c, model.OAuthScopeAPIRead)
	if !ok {
		return
	}
	var input struct {
		Amount        int64  `json:"amount"`
		PaymentMethod string `json:"payment_method"`
		AppId         int    `json:"app_id"`
	}
	if common.DecodeJson(c.Request.Body, &input) != nil || (input.AppId != 0 && input.AppId != app.Id) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	body, err := common.Marshal(scopedCheckoutRequest{AppId: app.Id, Amount: input.Amount, PaymentMethod: input.PaymentMethod})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.Set("id", user.Id)
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	ScopedAppTopupPreview(c)
}
