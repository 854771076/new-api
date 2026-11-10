package controller

import (
	"context"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
)

type AppProductUpsertRequest struct {
	AppId                 int     `json:"app_id"`
	Name                  string  `json:"name"`
	Description           string  `json:"description"`
	PriceAmount           float64 `json:"price_amount"`
	Currency              string  `json:"currency"`
	QuotaAmount           int64   `json:"quota_amount"`
	Enabled               *bool   `json:"enabled"`
	SortOrder             int     `json:"sort_order"`
	MaxPurchasePerUser    int     `json:"max_purchase_per_user"`
	StripePriceId         string  `json:"stripe_price_id"`
	CreemProductId        string  `json:"creem_product_id"`
	WaffoPancakeProductId string  `json:"waffo_pancake_product_id"`
}

// normalizeAppProductRequest validates an admin product payload and returns the
// normalized product. The OAuth app must exist because a product always belongs
// to exactly one external app.
func normalizeAppProductRequest(c *gin.Context, req *AppProductUpsertRequest) (*model.AppProduct, bool) {
	if req.AppId <= 0 {
		common.ApiErrorT(c, "Invalid ID")
		return nil, false
	}
	if _, err := model.GetOAuthAppByID(req.AppId); err != nil {
		common.ApiErrorT(c, "OAuth app not found")
		return nil, false
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		common.ApiErrorT(c, "App product name cannot be empty")
		return nil, false
	}
	if req.PriceAmount < 0 {
		common.ApiErrorT(c, "Price cannot be negative")
		return nil, false
	}
	if req.PriceAmount > 9999 {
		common.ApiErrorT(c, "Price cannot exceed 9999")
		return nil, false
	}
	if req.QuotaAmount < 0 {
		common.ApiErrorT(c, "Quota cannot be negative")
		return nil, false
	}
	if req.QuotaAmount > int64(common.MaxWalletQuota) {
		common.ApiErrorT(c, "Quota exceeds the maximum wallet quota")
		return nil, false
	}
	if req.MaxPurchasePerUser < 0 {
		common.ApiErrorT(c, "Purchase limit cannot be negative")
		return nil, false
	}
	currency := strings.ToUpper(strings.TrimSpace(req.Currency))
	if currency == "" {
		currency = "USD"
	}
	if len(currency) > 8 {
		common.ApiErrorT(c, "Invalid currency")
		return nil, false
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return &model.AppProduct{
		AppId:                 req.AppId,
		Name:                  name,
		Description:           strings.TrimSpace(req.Description),
		PriceAmount:           req.PriceAmount,
		Currency:              currency,
		QuotaAmount:           req.QuotaAmount,
		Enabled:               enabled,
		SortOrder:             req.SortOrder,
		MaxPurchasePerUser:    req.MaxPurchasePerUser,
		StripePriceId:         strings.TrimSpace(req.StripePriceId),
		CreemProductId:        strings.TrimSpace(req.CreemProductId),
		WaffoPancakeProductId: strings.TrimSpace(req.WaffoPancakeProductId),
	}, true
}

func parseOptionalIntQuery(c *gin.Context, key string) (int, bool) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return 0, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return 0, false
	}
	return value, true
}

func AdminListAppProducts(c *gin.Context) {
	appId, ok := parseOptionalIntQuery(c, "app_id")
	if !ok {
		return
	}
	pageInfo := common.GetPageQuery(c)
	products, total, err := model.ListAppProducts(appId, pageInfo)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(products)
	common.ApiSuccess(c, pageInfo)
}

func AdminCreateAppProduct(c *gin.Context) {
	var req AppProductUpsertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	product, ok := normalizeAppProductRequest(c, &req)
	if !ok {
		return
	}
	if err := model.DB.Create(product).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	// The model column defaults enabled to true, so GORM omits a false value on
	// insert. Persist an explicit "create disabled" choice with a map update.
	if !product.Enabled {
		if err := model.DB.Model(&model.AppProduct{}).Where("id = ?", product.Id).
			Update("enabled", false).Error; err != nil {
			common.ApiError(c, err)
			return
		}
	}
	common.ApiSuccess(c, product)
}

func AdminGetAppProduct(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		common.ApiErrorT(c, "Invalid ID")
		return
	}
	product, err := model.GetAppProductById(id)
	if err != nil {
		common.ApiError(c, model.ErrAppProductNotFound)
		return
	}
	common.ApiSuccess(c, product)
}

func AdminUpdateAppProduct(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		common.ApiErrorT(c, "Invalid ID")
		return
	}
	var req AppProductUpsertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	product, ok := normalizeAppProductRequest(c, &req)
	if !ok {
		return
	}
	if _, err := model.GetAppProductById(id); err != nil {
		common.ApiError(c, model.ErrAppProductNotFound)
		return
	}
	// enabled is written through the map on purpose: the struct field has a
	// gorm default, so Updates(struct) would silently skip false.
	updateMap := map[string]any{
		"app_id":                   product.AppId,
		"name":                     product.Name,
		"description":              product.Description,
		"price_amount":             product.PriceAmount,
		"currency":                 product.Currency,
		"quota_amount":             product.QuotaAmount,
		"enabled":                  product.Enabled,
		"sort_order":               product.SortOrder,
		"max_purchase_per_user":    product.MaxPurchasePerUser,
		"stripe_price_id":          product.StripePriceId,
		"creem_product_id":         product.CreemProductId,
		"waffo_pancake_product_id": product.WaffoPancakeProductId,
		"updated_at":               common.GetTimestamp(),
	}
	if err := model.DB.Model(&model.AppProduct{}).Where("id = ?", id).Updates(updateMap).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	product.Id = id
	common.ApiSuccess(c, product)
}

type AdminUpdateAppProductStatusRequest struct {
	Enabled *bool `json:"enabled"`
}

func AdminSetAppProductEnabled(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		common.ApiErrorT(c, "Invalid ID")
		return
	}
	var req AdminUpdateAppProductStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if _, err := model.GetAppProductById(id); err != nil {
		common.ApiError(c, model.ErrAppProductNotFound)
		return
	}
	if err := model.DB.Model(&model.AppProduct{}).Where("id = ?", id).
		Update("enabled", *req.Enabled).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminDeleteAppProduct removes a product only when it has no orders; ordered
// products must be disabled instead so order history stays intact.
func AdminDeleteAppProduct(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		common.ApiErrorT(c, "Invalid ID")
		return
	}
	if _, err := model.GetAppProductById(id); err != nil {
		common.ApiError(c, model.ErrAppProductNotFound)
		return
	}
	var orderCount int64
	if err := model.DB.Model(&model.AppProductOrder{}).Where("product_id = ?", id).Count(&orderCount).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if orderCount > 0 {
		common.ApiErrorT(c, "App products with orders cannot be deleted; disable the product instead")
		return
	}
	if err := model.DB.Delete(&model.AppProduct{}, id).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

func AdminListAppProductOrders(c *gin.Context) {
	appId, ok := parseOptionalIntQuery(c, "app_id")
	if !ok {
		return
	}
	userId, ok := parseOptionalIntQuery(c, "user_id")
	if !ok {
		return
	}
	pageInfo := common.GetPageQuery(c)
	orders, total, err := model.ListAppProductOrders(appId, userId, pageInfo)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(orders)
	common.ApiSuccess(c, pageInfo)
}

// AdminCompleteAppProductOrder reconciles an order manually (for example after
// a payment provider outage). It credits the app wallet exactly once and is
// subject to the same idempotency as an online callback.
func AdminCompleteAppProductOrder(c *gin.Context) {
	tradeNo := strings.TrimSpace(c.Param("trade_no"))
	if tradeNo == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := model.CompleteAppProductOrder(tradeNo, "", "", 0); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// AppProductCheckoutResult is returned by CreateAppProductWaffoPancakeCheckout.
type AppProductCheckoutResult struct {
	CheckoutURL string
	TradeNo     string
	SessionID   string
	ExpiresAt   string
}

// CreateAppProductWaffoPancakeCheckout creates a pending product order and a
// Waffo Pancake checkout session bound to it. The order is created first, so a
// session failure can be recorded as a failed order instead of leaving an
// untraceable payment.
func CreateAppProductWaffoPancakeCheckout(ctx context.Context, user *model.User, product *model.AppProduct) (*AppProductCheckoutResult, error) {
	if user == nil {
		return nil, common.NewMessage("User does not exist")
	}
	if product == nil {
		return nil, model.ErrAppProductNotFound
	}
	// Share the top-up gate with the webhook handler so checkout and callback
	// availability cannot diverge.
	if !isWaffoPancakeTopUpEnabled() {
		return nil, common.NewMessage("Waffo Pancake configuration is incomplete")
	}
	if !strings.EqualFold(product.Currency, "USD") {
		return nil, common.NewMessage("Only USD products can be paid with Waffo Pancake")
	}
	if product.PriceAmount < 0.01 {
		return nil, common.NewMessage("Payment amount is too low")
	}
	productID := strings.TrimSpace(product.WaffoPancakeProductId)
	if productID == "" {
		productID = strings.TrimSpace(setting.WaffoPancakeProductID)
	}
	if productID == "" {
		return nil, common.NewMessage("Waffo Pancake configuration is incomplete")
	}

	order, err := model.CreateAppProductOrder(user.Id, product.AppId, product.Id,
		model.PaymentMethodWaffoPancake, model.PaymentProviderWaffoPancake)
	if err != nil {
		return nil, err
	}

	expiresInSeconds := 45 * 60
	session, err := service.CreateWaffoPancakeCheckoutSession(ctx, &service.WaffoPancakeCreateSessionParams{
		ProductID:               productID,
		BuyerIdentity:           service.WaffoPancakeBuyerIdentityFromUserID(user.Id),
		PriceSnapshot:           &service.WaffoPancakePriceSnapshot{Amount: formatWaffoPancakeAmount(product.PriceAmount), TaxCategory: "saas"},
		BuyerEmail:              getWaffoPancakeBuyerEmail(user),
		ExpiresInSeconds:        &expiresInSeconds,
		OrderMerchantExternalID: order.TradeNo,
	})
	if err != nil {
		logger.LogError(ctx, common.LogText(
			"Waffo Pancake failed to create app product checkout trade_no=%s product_id=%d error=%q",
			order.TradeNo, product.Id, err.Error(),
		))
		_ = model.UpdateAppProductOrderStatus(order.TradeNo, model.PaymentProviderWaffoPancake, model.AppProductOrderStatusFailed)
		return nil, common.NewMessage("Failed to start payment")
	}
	logger.LogInfo(ctx, common.LogText(
		"App product order created trade_no=%s user_id=%d app_id=%d product_id=%d quota=%d money=%.2f",
		order.TradeNo, order.UserId, order.AppId, order.ProductId, order.QuotaAmount, order.Money,
	))

	return &AppProductCheckoutResult{
		CheckoutURL: session.CheckoutURL,
		TradeNo:     order.TradeNo,
		SessionID:   session.SessionID,
		ExpiresAt:   session.ExpiresAt,
	}, nil
}
