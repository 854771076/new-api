package controller

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/thanhpk/randstr"
)

type SubscriptionCreemPayRequest struct {
	PlanId int `json:"plan_id"`
}

func SubscriptionRequestCreemPay(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	var req SubscriptionCreemPayRequest

	// Keep body for debugging consistency (like RequestCreemPay)
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.LogError(c.Request.Context(), common.LogText("Creem failed to read subscription payment request error=%q", err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "read query error"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		paymentError(c, common.NewMessage("Invalid parameters"))
		return
	}

	plan, err := model.GetSubscriptionPlanById(req.PlanId)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	userId := c.GetInt("id")
	user, err := model.GetUserById(userId, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if user == nil {
		common.ApiErrorT(c, "User does not exist")
		return
	}

	checkoutUrl, orderId, err := createCreemSubscriptionOrder(c.Request.Context(), user, plan)
	if err != nil {
		paymentError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"checkout_url": checkoutUrl,
			"order_id":     orderId,
		},
	})
}

// createCreemSubscriptionOrder validates the plan and Creem configuration,
// persists the pending order and creates the Creem checkout link. It is shared
// by the dashboard and the OAuth app checkout endpoint.
func createCreemSubscriptionOrder(ctx context.Context, user *model.User, plan *model.SubscriptionPlan) (string, string, error) {
	if plan == nil {
		return "", "", common.NewMessage("Subscription plan does not exist")
	}
	if !plan.Enabled {
		return "", "", common.NewMessage("Subscription plan is not enabled")
	}
	if plan.CreemProductId == "" {
		return "", "", common.NewMessage("CreemProductId is not configured for this plan")
	}
	if setting.CreemWebhookSecret == "" && !setting.CreemTestMode {
		return "", "", common.NewMessage("Creem webhook is not configured")
	}
	if user == nil {
		return "", "", common.NewMessage("User does not exist")
	}
	if plan.MaxPurchasePerUser > 0 {
		count, err := model.CountUserSubscriptionsByPlan(user.Id, plan.Id)
		if err != nil {
			return "", "", err
		}
		if count >= int64(plan.MaxPurchasePerUser) {
			return "", "", common.NewMessage("Purchase limit for this plan has been reached")
		}
	}

	reference := "sub-creem-ref-" + randstr.String(6)
	referenceId := "sub_ref_" + common.Sha1([]byte(reference+time.Now().String()+user.Username))

	// create pending order first
	order := &model.SubscriptionOrder{
		AppId:           plan.AppId,
		UserId:          user.Id,
		PlanId:          plan.Id,
		Money:           plan.PriceAmount,
		TradeNo:         referenceId,
		PaymentMethod:   model.PaymentMethodCreem,
		PaymentProvider: model.PaymentProviderCreem,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	if err := order.Insert(); err != nil {
		return "", "", common.NewMessage("Failed to create order")
	}

	// Reuse Creem checkout generator by building a lightweight product reference.
	currency := "USD"
	switch operation_setting.GetGeneralSetting().QuotaDisplayType {
	case operation_setting.QuotaDisplayTypeCNY:
		currency = "CNY"
	case operation_setting.QuotaDisplayTypeUSD:
		currency = "USD"
	default:
		currency = "USD"
	}
	product := &CreemProduct{
		ProductId: plan.CreemProductId,
		Name:      plan.Title,
		Price:     plan.PriceAmount,
		Currency:  currency,
		Quota:     0,
	}

	checkoutUrl, err := genCreemLink(ctx, referenceId, product, user.Email, user.Username)
	if err != nil {
		logger.LogError(ctx, common.LogText("Creem failed to create subscription payment link trade_no=%s product_id=%s error=%q", referenceId, product.ProductId, err.Error()))
		return "", "", common.NewMessage("Failed to start payment")
	}
	return checkoutUrl, referenceId, nil
}
