package model

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type appCheckoutContextKey struct{}

// AppCheckout is an immutable purchase intent, written before a financial or
// provider side effect. An ambiguous provider result is never automatically retried.
type AppCheckout struct {
	Credits        *string `json:"credits" gorm:"-"`
	RequestKey     string  `json:"-" gorm:"primaryKey;type:varchar(64)"`
	Fingerprint    string  `json:"-" gorm:"type:varchar(64);not null"`
	OrderId        string  `json:"order_id" gorm:"type:varchar(128);uniqueIndex;not null"`
	Actor          string  `json:"-" gorm:"type:varchar(128);not null"`
	IdentitySource string  `json:"identity_source" gorm:"type:varchar(16);not null"`
	UserId         int     `json:"user_id" gorm:"index;not null"`
	AppId          int     `json:"app_id" gorm:"index;not null"`
	Kind           string  `json:"purchase_type" gorm:"type:varchar(16);not null"`
	PaymentMethod  string  `json:"payment_method" gorm:"type:varchar(50);not null"`
	Currency       string  `json:"currency" gorm:"type:varchar(8);not null"`
	Money          string  `json:"money" gorm:"type:varchar(64);not null"`
	Quota          int     `json:"quota" gorm:"type:bigint;not null"`
	QuotaPerCredit *int64  `json:"quota_per_credit" gorm:"type:bigint"`
	PlanSnapshot   string  `json:"-" gorm:"type:text"`
	SnapshotReady  bool    `json:"snapshot_ready"`
	Response       string  `json:"-" gorm:"type:text"`
	ResponseStatus int     `json:"-"`
	State          string  `json:"status" gorm:"type:varchar(32);not null"`
	ExpiresAt      int64   `json:"expires_at" gorm:"type:bigint"`
	CreatedAt      int64   `json:"created_at" gorm:"autoCreateTime"`
}

func WithAppCheckout(ctx context.Context, order *AppCheckout) context.Context {
	return context.WithValue(ctx, appCheckoutContextKey{}, order)
}
func AppCheckoutFromContext(ctx context.Context) *AppCheckout {
	order, _ := ctx.Value(appCheckoutContextKey{}).(*AppCheckout)
	return order
}
func AppCheckoutTradeNo(ctx context.Context, fallback string) string {
	if order := AppCheckoutFromContext(ctx); order != nil {
		return order.OrderId
	}
	return fallback
}

func CheckoutOrderTradeNo(orders []*AppCheckout, fallback string) string {
	if len(orders) > 0 && orders[0] != nil {
		return orders[0].OrderId
	}
	return fallback
}

func attachAppCheckoutSnapshot(tx *gorm.DB, tradeNo string, userID, appID int, kind string, money float64, quota int, currency string) error {
	if !strings.Contains(tradeNo, "-REQUEST-") {
		return nil
	}
	if math.IsNaN(money) || math.IsInf(money, 0) || money < 0 {
		return common.NewMessage("Invalid parameters")
	}
	var intent AppCheckout
	if err := lockForUpdate(tx).Where("order_id = ?", tradeNo).First(&intent).Error; err != nil {
		return err
	}
	if intent.UserId != userID || intent.AppId != appID || intent.Kind != kind || intent.SnapshotReady || quota < 0 || quota > common.MaxWalletQuota {
		return ErrAppOperationConflict
	}
	var user User
	if err := tx.First(&user, userID).Error; err != nil {
		return err
	}
	if user.Source() != intent.IdentitySource || ValidateUserAppScope(&user, appID) != nil {
		return ErrProxyIdentity
	}
	if user.IsProxy() && kind == "subscription" {
		return ErrProxySubscriptionUnsupported
	}
	return tx.Model(&AppCheckout{}).Where("request_key = ?", intent.RequestKey).Updates(map[string]any{"money": decimal.NewFromFloat(money).String(), "quota": quota, "currency": currency, "snapshot_ready": true}).Error
}

func (order *TopUp) BeforeCreate(tx *gorm.DB) error {
	if !strings.Contains(order.TradeNo, "-REQUEST-") {
		return nil
	}
	var intent AppCheckout
	if err := tx.Where("order_id = ?", order.TradeNo).First(&intent).Error; err != nil {
		return err
	}
	if intent.Kind == "subscription" && intent.SnapshotReady && intent.UserId == order.UserId && intent.AppId == order.AppId {
		return nil
	}
	quota := decimal.NewFromInt(order.Amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	if order.PaymentProvider == PaymentProviderCreem || order.PaymentProvider == PaymentProviderBalance || order.PaymentProvider == PaymentProviderWaffoPancake {
		quota = decimal.NewFromInt(order.Amount)
		if order.PaymentProvider == PaymentProviderBalance || order.PaymentProvider == PaymentProviderWaffoPancake {
			var intent AppCheckout
			if err := tx.Where("order_id = ?", order.TradeNo).First(&intent).Error; err != nil {
				return err
			}
			quota = decimal.NewFromInt(int64(intent.Quota))
		}
	}
	if order.PaymentProvider == PaymentProviderStripe || order.PaymentProvider == PaymentProviderEpay {
		quota = decimal.NewFromInt(int64(intent.Quota))
	}
	amount, err := common.WalletQuotaFromDecimalStrict(quota)
	if err != nil {
		return err
	}
	currency := order.CheckoutCurrency
	if order.PaymentProvider == PaymentProviderEpay {
		currency = "CNY"
	}
	if currency == "" {
		currency = "USD"
	}
	return attachAppCheckoutSnapshot(tx, order.TradeNo, order.UserId, order.AppId, "topup", order.Money, amount, currency)
}

func (order *AppProductOrder) BeforeCreate(tx *gorm.DB) error {
	if order.QuotaAmount < 0 || order.QuotaAmount > int64(common.MaxWalletQuota) {
		return ErrAppProductQuotaInvalid
	}
	return attachAppCheckoutSnapshot(tx, order.TradeNo, order.UserId, order.AppId, "product", order.Money, int(order.QuotaAmount), order.Currency)
}

func (order *SubscriptionOrder) BeforeCreate(tx *gorm.DB) error {
	if !strings.Contains(order.TradeNo, "-REQUEST-") {
		return nil
	}
	if err := RequireNativeUserTx(tx, order.UserId); err != nil {
		return err
	}
	var plan SubscriptionPlan
	if err := tx.First(&plan, order.PlanId).Error; err != nil {
		return err
	}
	if plan.AppId != order.AppId || plan.TotalAmount < 0 || plan.TotalAmount > int64(common.MaxWalletQuota) {
		return ErrProxyIdentity
	}
	encoded, err := common.Marshal(plan)
	if err != nil {
		return err
	}
	if err := attachAppCheckoutSnapshot(tx, order.TradeNo, order.UserId, order.AppId, "subscription", order.Money, int(plan.TotalAmount), plan.Currency); err != nil {
		return err
	}
	return tx.Model(&AppCheckout{}).Where("order_id = ?", order.TradeNo).Update("plan_snapshot", string(encoded)).Error
}

func ReserveAppCheckout(order *AppCheckout, requestID string, input any) (bool, error) {
	if order == nil || len(requestID) < 8 || len(requestID) > 128 || order.UserId <= 0 || order.AppId < 0 {
		return false, ErrProxyIdentity
	}
	if order.Quota < 0 || order.Quota > common.MaxWalletQuota {
		return false, ErrInvalidTopUpQuota
	}
	keyData, err := common.Marshal([]any{order.Actor, requestID})
	if err != nil {
		return false, err
	}
	key := sha256.Sum256(keyData)
	canonical, err := common.Marshal([]any{order.UserId, order.AppId, order.IdentitySource, order.Kind, input})
	if err != nil {
		return false, err
	}
	fingerprint := sha256.Sum256(canonical)
	order.RequestKey, order.Fingerprint = fmt.Sprintf("%x", key), fmt.Sprintf("%x", fingerprint)
	// Preserve prefixes used by the existing provider callback dispatchers.
	prefix := "APP_TOPUP-"
	if order.Kind == "product" {
		prefix = AppProductOrderTradeNoPrefix
	}
	if order.Kind == "subscription" {
		prefix = "SUBUSR-"
	}
	if order.PaymentMethod == PaymentMethodWaffoPancake && order.Kind != "product" {
		prefix = "WAFFO_PANCAKE-"
		if order.Kind == "subscription" {
			prefix = "WAFFO_PANCAKE_SUB-"
		}
	}
	random, err := common.GenerateKey()
	if err != nil {
		return false, err
	}
	order.OrderId, order.State = prefix+"REQUEST-"+random[:32], "processing"
	order.ExpiresAt = common.GetTimestamp() + 45*60
	var stored AppCheckout
	created := false
	err = DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(order)
		if result.Error != nil {
			return result.Error
		}
		created = result.RowsAffected == 1
		if err := tx.First(&stored, "request_key = ?", order.RequestKey).Error; err != nil {
			return err
		}
		if stored.Fingerprint != order.Fingerprint {
			return ErrAppOperationConflict
		}
		return nil
	})
	if err == nil {
		*order = stored
	}
	return created, err
}

func SaveAppCheckoutResponse(order *AppCheckout, status int, response []byte) error {
	state := "pending"
	if status >= 400 {
		state = "reconciliation_required"
	}
	return DB.Model(&AppCheckout{}).Where("request_key = ?", order.RequestKey).Updates(map[string]any{"response": string(response), "response_status": status, "state": state}).Error
}

// CheckoutSettlementQuota keeps paid quota fixed even if the global conversion
// changes between checkout and webhook. Legacy orders retain their old behavior.
func CheckoutSettlementQuota(tx *gorm.DB, tradeNo string, legacy decimal.Decimal) (int, error) {
	if !strings.Contains(tradeNo, "-REQUEST-") {
		return common.WalletQuotaFromDecimalStrict(legacy)
	}
	var order AppCheckout
	if err := tx.Where("order_id = ?", tradeNo).First(&order).Error; err != nil {
		return 0, err
	}
	if order.Quota <= 0 || order.Quota > common.MaxWalletQuota {
		return 0, ErrInvalidTopUpQuota
	}
	return order.Quota, nil
}

func ReadAppCheckout(userID, appID int, orderID string) (*AppCheckout, error) {
	user, err := GetUserById(userID, true)
	if err != nil {
		return nil, err
	}
	if err := ValidateUserAppScope(user, appID); err != nil {
		return nil, err
	}
	var intent AppCheckout
	err = DB.Where("user_id = ? AND app_id = ? AND order_id = ?", userID, appID, orderID).First(&intent).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	var status string
	// Query the exact durable order; intent/response state is never proof of payment.
	tables := []any{&AppProductOrder{}, &SubscriptionOrder{}, &TopUp{}}
	if user.IsProxy() {
		tables = []any{&AppProductOrder{}, &TopUp{}}
	}
	for _, table := range tables {
		var row struct{ Status string }
		query := DB.Model(table).Select("status").Where("user_id = ? AND app_id = ? AND trade_no = ?", userID, appID, orderID).Take(&row)
		if query.Error == nil {
			status = row.Status
			break
		}
		if !errors.Is(query.Error, gorm.ErrRecordNotFound) {
			return nil, query.Error
		}
	}
	if intent.OrderId == "" {
		if status == "" {
			return nil, gorm.ErrRecordNotFound
		}
		// Legacy history has no trustworthy credit ratio snapshot.
		intent = AppCheckout{OrderId: orderID, UserId: userID, AppId: appID, IdentitySource: user.Source()}
	}
	if status != "" && (status == common.TopUpStatusSuccess || intent.State != "reconciliation_required") {
		intent.State = status
	}
	if intent.State == "pending" && intent.ExpiresAt > 0 && intent.ExpiresAt < common.GetTimestamp() {
		intent.State = "expired"
	}
	if intent.SnapshotReady && (intent.Kind != "subscription" || intent.Quota > 0) {
		intent.Credits = (&OAuthApp{QuotaPerCredit: intent.QuotaPerCredit}).Credits(intent.Quota)
	}
	return &intent, nil
}

// VerifyAppCheckoutPayment runs only after provider signature and paid-state
// verification. Legacy payment contracts retain their existing validation.
func VerifyAppCheckoutPayment(tradeNo, paidMoney, currency string) error {
	if !strings.Contains(tradeNo, "-REQUEST-") {
		return nil
	}
	var order AppCheckout
	if err := DB.Where("order_id = ?", tradeNo).First(&order).Error; err != nil {
		return err
	}
	amount, amountErr := decimal.NewFromString(strings.TrimSpace(paidMoney))
	expected, expectedErr := decimal.NewFromString(order.Money)
	expired := order.ExpiresAt > 0 && order.ExpiresAt < common.GetTimestamp()
	if expired {
		stored, err := ReadAppCheckout(order.UserId, order.AppId, tradeNo)
		if err != nil {
			return err
		}
		expired = stored.State != common.TopUpStatusSuccess
	}
	if !order.SnapshotReady || amountErr != nil || expectedErr != nil || !amount.IsPositive() || !amount.Equal(expected.Round(2)) || strings.TrimSpace(currency) == "" || !strings.EqualFold(strings.TrimSpace(currency), order.Currency) || expired {
		if err := DB.Model(&AppCheckout{}).Where("request_key = ?", order.RequestKey).Update("state", "reconciliation_required").Error; err != nil {
			return err
		}
		return ErrAppOperationConflict
	}
	return nil
}
