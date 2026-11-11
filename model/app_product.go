package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// App product (purchasable quota package scoped to an external app)
type AppProduct struct {
	Id    int `json:"id"`
	AppId int `json:"app_id" gorm:"not null;default:0;index"`

	Name        string `json:"name" gorm:"type:varchar(128);not null"`
	Description string `json:"description" gorm:"type:varchar(512);default:''"`

	PriceAmount float64 `json:"price_amount" gorm:"type:decimal(10,6);not null;default:0"`
	Currency    string  `json:"currency" gorm:"type:varchar(8);not null;default:'USD'"`

	QuotaAmount int64 `json:"quota_amount" gorm:"type:bigint;not null;default:0"`

	Enabled   bool `json:"enabled"`
	SortOrder int  `json:"sort_order" gorm:"type:int;default:0"`

	// Max purchases per user (0 = unlimited)
	MaxPurchasePerUser int `json:"max_purchase_per_user" gorm:"type:int;default:0"`

	StripePriceId         string `json:"stripe_price_id" gorm:"type:varchar(128);default:''"`
	CreemProductId        string `json:"creem_product_id" gorm:"type:varchar(128);default:''"`
	WaffoPancakeProductId string `json:"waffo_pancake_product_id" gorm:"type:varchar(128);default:''"`

	CreatedAt int64 `json:"created_at" gorm:"bigint"`
	UpdatedAt int64 `json:"updated_at" gorm:"bigint"`
}

func (p *AppProduct) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	p.CreatedAt = now
	p.UpdatedAt = now
	return nil
}

func (p *AppProduct) BeforeUpdate(tx *gorm.DB) error {
	p.UpdatedAt = common.GetTimestamp()
	return nil
}

func GetAppProductById(id int) (*AppProduct, error) {
	if id <= 0 {
		return nil, errors.New("invalid product id")
	}
	var product AppProduct
	if err := DB.Where("id = ?", id).First(&product).Error; err != nil {
		return nil, err
	}
	return &product, nil
}

func ListEnabledAppProducts(appId int) ([]AppProduct, error) {
	var products []AppProduct
	if err := DB.Where("app_id = ? AND enabled = ?", appId, true).
		Order("sort_order desc, id desc").
		Find(&products).Error; err != nil {
		return nil, err
	}
	return products, nil
}

const (
	AppProductOrderStatusPending = "pending"
	AppProductOrderStatusSuccess = "success"
	AppProductOrderStatusFailed  = "failed"
	AppProductOrderStatusExpired = "expired"
)

// App product order (payment -> webhook -> credit app wallet)
type AppProductOrder struct {
	Id        int     `json:"id"`
	UserId    int     `json:"user_id" gorm:"index"`
	AppId     int     `json:"app_id" gorm:"index"`
	ProductId int     `json:"product_id" gorm:"index"`
	Money     float64 `json:"money"`

	// Product terms are snapshotted at order creation so later edits cannot
	// change what an already-created order credits.
	PriceAmount float64 `json:"price_amount" gorm:"type:decimal(10,6);not null;default:0"`
	Currency    string  `json:"currency" gorm:"type:varchar(8);not null;default:'USD'"`
	QuotaAmount int64   `json:"quota_amount" gorm:"type:bigint;not null;default:0"`

	TradeNo         string `json:"trade_no" gorm:"unique;type:varchar(255);index"`
	PaymentMethod   string `json:"payment_method" gorm:"type:varchar(50)"`
	PaymentProvider string `json:"payment_provider" gorm:"type:varchar(50);default:''"`
	Status          string `json:"status"`
	ProviderPayload string `json:"provider_payload" gorm:"type:text"`

	CreateTime   int64 `json:"create_time"`
	CompleteTime int64 `json:"complete_time"`
}

func (o *AppProductOrder) Insert() error {
	if o.CreateTime == 0 {
		o.CreateTime = common.GetTimestamp()
	}
	return DB.Create(o).Error
}

func (o *AppProductOrder) Update() error {
	return DB.Save(o).Error
}

func GetAppProductOrderByTradeNo(tradeNo string) *AppProductOrder {
	if tradeNo == "" {
		return nil
	}
	var order AppProductOrder
	if err := DB.Where("trade_no = ?", tradeNo).First(&order).Error; err != nil {
		return nil
	}
	return &order
}

// AppProductOrderTradeNoPrefix identifies app product orders in payment
// callbacks, which dispatch on the trade_no prefix.
const AppProductOrderTradeNoPrefix = "APP_PRODUCT-"

var (
	ErrAppProductNotFound              = common.NewMessage("Product does not exist")
	ErrAppProductDisabled              = common.NewMessage("App product is disabled")
	ErrAppProductCrossApp              = common.NewMessage("App product does not belong to this app")
	ErrAppProductQuotaInvalid          = common.NewMessage("App product quota must be greater than 0")
	ErrAppProductPurchaseLimitExceeded = common.NewMessage("Purchase limit reached")
	ErrAppProductOrderNotFound         = common.NewMessage("App product order not found")
	ErrAppProductOrderStatusInvalid    = common.NewMessage("Invalid app product order status")
	ErrAppProductOrderAmountMismatch   = common.NewMessage("Payment amount does not match the order")
)

// ListAppProducts returns a page of products; appId 0 means all apps.
func ListAppProducts(appId int, pageInfo *common.PageInfo) ([]AppProduct, int64, error) {
	query := DB.Model(&AppProduct{})
	if appId > 0 {
		query = query.Where("app_id = ?", appId)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	products := make([]AppProduct, 0)
	err := query.Order("sort_order desc, id desc").
		Limit(pageInfo.GetPageSize()).
		Offset(pageInfo.GetStartIdx()).
		Find(&products).Error
	if err != nil {
		return nil, 0, err
	}
	return products, total, nil
}

// ListAppProductOrders returns a page of orders; appId or userId 0 means no
// filter on that dimension.
func ListAppProductOrders(appId, userId int, pageInfo *common.PageInfo) ([]AppProductOrder, int64, error) {
	query := DB.Model(&AppProductOrder{})
	if appId > 0 {
		query = query.Where("app_id = ?", appId)
	}
	if userId > 0 {
		query = query.Where("user_id = ?", userId)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	orders := make([]AppProductOrder, 0)
	err := query.Order("id desc").
		Limit(pageInfo.GetPageSize()).
		Offset(pageInfo.GetStartIdx()).
		Find(&orders).Error
	if err != nil {
		return nil, 0, err
	}
	return orders, total, nil
}

// CreateAppProductOrder validates the product against the purchasing app and
// creates a pending order that snapshots the product price and quota.
//
// The per-user purchase limit counts pending and successful orders. A pending
// order represents a checkout the user may still pay, so counting only
// successes would let one user open several checkouts and pay past the limit.
// Abandoned checkouts must be moved to expired (see
// UpdateAppProductOrderStatus) to release the slot.
func CreateAppProductOrder(userId, appId, productId int, paymentMethod, paymentProvider string) (*AppProductOrder, error) {
	var order *AppProductOrder
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		order, err = createAppProductOrderTx(tx, userId, appId, productId, paymentMethod, paymentProvider)
		return err
	})
	if err != nil {
		return nil, err
	}
	return order, nil
}

func createAppProductOrderTx(tx *gorm.DB, userId, appId, productId int, paymentMethod, paymentProvider string) (*AppProductOrder, error) {
	if userId <= 0 || appId <= 0 || productId <= 0 {
		return nil, ErrAppProductNotFound
	}
	// SQLite needs a write before the read to serialize limit checks too.
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		if err := tx.Model(&AppProduct{}).Where("id = ?", productId).
			UpdateColumn("id", gorm.Expr("id")).Error; err != nil {
			return nil, err
		}
	}
	order := &AppProductOrder{}
	// Lock the product row so concurrent checkouts for the same product
	// serialize their purchase-limit check.
	var product AppProduct
	if err := lockForUpdate(tx).Where("id = ?", productId).First(&product).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAppProductNotFound
		}
		return nil, err
	}
	if product.AppId != appId {
		return nil, ErrAppProductCrossApp
	}
	if !product.Enabled {
		return nil, ErrAppProductDisabled
	}
	if product.QuotaAmount <= 0 || product.QuotaAmount > int64(common.MaxWalletQuota) {
		return nil, ErrAppProductQuotaInvalid
	}
	if product.MaxPurchasePerUser > 0 {
		var purchased int64
		if err := tx.Model(&AppProductOrder{}).
			Where("user_id = ? AND product_id = ? AND status IN ?", userId, productId,
				[]string{AppProductOrderStatusPending, AppProductOrderStatusSuccess}).
			Count(&purchased).Error; err != nil {
			return nil, err
		}
		if purchased >= int64(product.MaxPurchasePerUser) {
			return nil, ErrAppProductPurchaseLimitExceeded
		}
	}
	*order = AppProductOrder{
		UserId: userId, AppId: appId, ProductId: productId,
		Money: product.PriceAmount, PriceAmount: product.PriceAmount,
		Currency: product.Currency, QuotaAmount: product.QuotaAmount,
		TradeNo:       fmt.Sprintf("%s%d-%d-%d-%s", AppProductOrderTradeNoPrefix, appId, userId, time.Now().UnixMilli(), common.GetRandomString(6)),
		PaymentMethod: paymentMethod, PaymentProvider: paymentProvider,
		Status: AppProductOrderStatusPending, CreateTime: common.GetTimestamp(),
	}
	if err := tx.Create(order).Error; err != nil {
		return nil, err
	}
	return order, nil
}

// UpdateAppProductOrderStatus moves a pending order to failed or expired. A
// successful order is never downgraded.
func UpdateAppProductOrderStatus(tradeNo, expectedPaymentProvider, targetStatus string) error {
	if tradeNo == "" {
		return errors.New("tradeNo is empty")
	}
	if targetStatus != AppProductOrderStatusFailed && targetStatus != AppProductOrderStatusExpired {
		return ErrAppProductOrderStatusInvalid
	}
	refCol := "`trade_no`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		refCol = `"trade_no"`
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var order AppProductOrder
		if err := lockForUpdate(tx).Where(refCol+" = ?", tradeNo).First(&order).Error; err != nil {
			return ErrAppProductOrderNotFound
		}
		if expectedPaymentProvider != "" && order.PaymentProvider != expectedPaymentProvider {
			return ErrPaymentMethodMismatch
		}
		if order.Status == targetStatus {
			return nil
		}
		if order.Status != AppProductOrderStatusPending {
			return ErrAppProductOrderStatusInvalid
		}
		order.Status = targetStatus
		order.CompleteTime = common.GetTimestamp()
		return tx.Save(&order).Error
	})
}

// CompleteAppProductOrder credits the app wallet for a paid product order. The
// pending -> success transition, the status write and the wallet credit share
// one transaction under a row lock, so repeated or concurrent payment
// callbacks credit at most once. A successful order is a no-op, which makes
// duplicate callbacks idempotent.
//
// actualPayAmount is the amount reported by the payment provider; > 0 requires
// it to match the order amount. Pass 0 when the caller has already verified or
// has no provider amount (admin reconciliation).
func CompleteAppProductOrder(tradeNo, providerPayload, expectedPaymentProvider string, actualPayAmount float64) error {
	if tradeNo == "" {
		return errors.New("tradeNo is empty")
	}
	pendingOrder := GetAppProductOrderByTradeNo(tradeNo)
	if pendingOrder == nil {
		return ErrAppProductOrderNotFound
	}
	// Ensure the wallet exists before handling the callback, including replays.
	if _, err := EnsureUserAppWallet(pendingOrder.UserId, pendingOrder.AppId); err != nil {
		return err
	}
	refCol := "`trade_no`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		refCol = `"trade_no"`
	}
	var (
		alreadyDone  bool
		logUserId    int
		logProductId int
		logQuota     int64
		logMoney     float64
		logMethod    string
	)
	err := DB.Transaction(func(tx *gorm.DB) error {
		var order AppProductOrder
		if err := lockForUpdate(tx).Where(refCol+" = ?", tradeNo).First(&order).Error; err != nil {
			return ErrAppProductOrderNotFound
		}
		if expectedPaymentProvider != "" && order.PaymentProvider != expectedPaymentProvider {
			return ErrPaymentMethodMismatch
		}
		if order.Status == AppProductOrderStatusSuccess {
			alreadyDone = true
			return nil
		}
		if order.Status != AppProductOrderStatusPending {
			return ErrAppProductOrderStatusInvalid
		}
		if actualPayAmount > 0 &&
			decimal.NewFromFloat(order.Money).Round(2).Cmp(decimal.NewFromFloat(actualPayAmount).Round(2)) != 0 {
			return ErrAppProductOrderAmountMismatch
		}
		quota := int(order.QuotaAmount)
		if order.QuotaAmount <= 0 || int64(quota) != order.QuotaAmount {
			return ErrAppProductQuotaInvalid
		}
		if err := increaseUserAppQuota(tx, order.UserId, order.AppId, quota); err != nil {
			return err
		}
		order.Status = AppProductOrderStatusSuccess
		order.CompleteTime = common.GetTimestamp()
		if providerPayload != "" {
			order.ProviderPayload = providerPayload
		}
		if err := tx.Save(&order).Error; err != nil {
			return err
		}
		logUserId = order.UserId
		logProductId = order.ProductId
		logQuota = order.QuotaAmount
		logMoney = order.Money
		logMethod = order.PaymentMethod
		return nil
	})
	if err != nil {
		if !errors.Is(err, ErrAppProductOrderNotFound) &&
			!errors.Is(err, ErrPaymentMethodMismatch) &&
			!errors.Is(err, ErrAppProductOrderStatusInvalid) &&
			!errors.Is(err, ErrAppProductOrderAmountMismatch) {
			common.SysError(common.LogText("app product order completion failed trade_no=%s: %s", tradeNo, err.Error()))
		}
		return err
	}
	if alreadyDone || logUserId <= 0 {
		return nil
	}
	RecordLog(logUserId, LogTypeTopup, common.NewMessage(
		"App product purchased, product ID: {{product}}, quota added: {{quota}}, payment amount: {{amount}}, payment method: {{method}}",
		map[string]any{
			"product": logProductId,
			"quota":   logger.FormatQuota(int(logQuota)),
			"amount":  fmt.Sprintf("%.2f", logMoney),
			"method":  logMethod,
		},
	))
	return nil
}

// PurchaseAppProductWithBalance validates, debits, credits and completes the
// order in one transaction. A failed purchase leaves no charge or pending order.
func PurchaseAppProductWithBalance(userId, appId, productId int) (*AppProductOrder, error) {
	var order *AppProductOrder
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		order, err = createAppProductOrderTx(tx, userId, appId, productId, PaymentMethodBalance, PaymentProviderBalance)
		if err != nil {
			return err
		}
		if order.PriceAmount < 0 {
			return common.NewMessage("Price cannot be negative")
		}
		requiredQuota, err := calcSubscriptionBalanceQuota(order.PriceAmount)
		if err != nil {
			return err
		}
		if _, err := ensureUserAppWallet(tx, userId, appId); err != nil {
			return err
		}
		if requiredQuota > 0 {
			result := tx.Model(&UserAppWallet{}).
				Where("user_id = ? AND app_id = ? AND quota >= ?", userId, appId, requiredQuota).
				Update("quota", gorm.Expr("quota - ?", requiredQuota))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return common.NewMessage("Insufficient balance")
			}
		}
		if err := increaseUserAppQuota(tx, userId, appId, int(order.QuotaAmount)); err != nil {
			return err
		}
		order.Status = AppProductOrderStatusSuccess
		order.CompleteTime = common.GetTimestamp()
		order.ProviderPayload = fmt.Sprintf("charged_quota=%d", requiredQuota)
		return tx.Save(order).Error
	})
	if err != nil {
		return nil, err
	}
	RecordLog(userId, LogTypeTopup, common.NewMessage(
		"App product purchased, product ID: {{product}}, quota added: {{quota}}, payment amount: {{amount}}, payment method: {{method}}",
		map[string]any{
			"product": productId, "quota": logger.FormatQuota(int(order.QuotaAmount)),
			"amount": fmt.Sprintf("%.2f", order.Money), "method": order.PaymentMethod,
		},
	))
	return order, nil
}
