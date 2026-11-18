package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
)

type UserAppAccess struct {
	Id           int    `json:"id"`
	Name         string `json:"name"`
	LogoURL      string `json:"logo_url"`
	Status       int    `json:"status"`
	OAuthEnabled bool   `json:"oauth_enabled"`
	ReadOnly     bool   `json:"read_only"`
}

// UserAppAccessList is deliberately independent of marketplace visibility and
// of the administrator's full App configuration list.
func UserAppAccessList(userID int, historical bool) ([]UserAppAccess, error) {
	if err := RequireNativeUserTx(DB, userID); err != nil {
		return nil, err
	}
	wallets := DB.Model(&UserAppWallet{}).Select("app_id").Where("user_id = ?", userID)
	consents := DB.Model(&OAuthConsent{}).Select("app_id").Where("user_id = ?", userID)
	subscriptions := DB.Model(&UserSubscription{}).Select("app_id").Where("user_id = ?", userID)
	var apps []OAuthApp
	query := DB.Where("(id IN (?) OR id IN (?) OR id IN (?))", wallets, consents, subscriptions)
	if !historical {
		query = query.Where("oauth_enabled IS NULL OR oauth_enabled = ?", true)
	}
	if err := query.Order("id asc").Find(&apps).Error; err != nil {
		return nil, err
	}
	result := make([]UserAppAccess, 0, len(apps))
	for _, app := range apps {
		result = append(result, UserAppAccess{Id: app.Id, Name: app.Name, LogoURL: app.LogoURL, Status: app.Status, OAuthEnabled: app.OAuthAllowed(), ReadOnly: !app.IsOAuthEnabled()})
	}
	return result, nil
}

type AppOrderListItem struct {
	OrderId        string  `json:"order_id"`
	Kind           string  `json:"purchase_type"`
	Status         string  `json:"status"`
	UserId         int     `json:"user_id"`
	AppId          int     `json:"app_id"`
	Money          float64 `json:"money"`
	Currency       string  `json:"currency"`
	CreatedAt      int64   `json:"created_at"`
	Quota          *int    `json:"quota" gorm:"-"`
	QuotaPerCredit *int64  `json:"quota_per_credit" gorm:"-"`
	Credits        *string `json:"credits" gorm:"-"`
}

func ListScopedAppOrders(userID, appID int, keyword string, page *common.PageInfo) ([]AppOrderListItem, int64, error) {
	user, err := GetUserById(userID, true)
	if err != nil {
		return nil, 0, err
	}
	if err := ValidateUserAppScope(user, appID); err != nil {
		return nil, 0, err
	}
	// UNION ALL is supported by all three engines. The heterogeneous existing
	// order tables need one stable ordering and pagination before enrichment.
	union := "SELECT trade_no AS order_id, 'topup' AS kind, status, user_id, app_id, money, CASE WHEN payment_provider = 'epay' THEN 'CNY' WHEN payment_provider = 'balance' THEN 'USD' ELSE '' END AS currency, create_time AS created_at FROM top_ups WHERE user_id = ? AND app_id = ? AND trade_no LIKE ? AND NOT EXISTS (SELECT 1 FROM subscription_orders AS s WHERE s.trade_no = top_ups.trade_no) UNION ALL SELECT trade_no AS order_id, 'product' AS kind, status, user_id, app_id, money, currency, create_time AS created_at FROM app_product_orders WHERE user_id = ? AND app_id = ? AND trade_no LIKE ?"
	pattern := "%" + keyword + "%"
	args := []any{userID, appID, pattern, userID, appID, pattern}
	if !user.IsProxy() {
		union += " UNION ALL SELECT trade_no AS order_id, 'subscription' AS kind, status, user_id, app_id, money, '' AS currency, create_time AS created_at FROM subscription_orders WHERE user_id = ? AND app_id = ? AND trade_no LIKE ?"
		args = append(args, userID, appID, pattern)
	}
	var total int64
	if err := DB.Raw("SELECT COUNT(*) FROM ("+union+") AS app_orders", args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	args = append(args, page.GetPageSize(), page.GetStartIdx())
	items := make([]AppOrderListItem, 0)
	if err := DB.Raw("SELECT * FROM ("+union+") AS app_orders ORDER BY created_at DESC, order_id DESC LIMIT ? OFFSET ?", args...).Scan(&items).Error; err != nil {
		return nil, 0, err
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.OrderId)
	}
	var snapshots []AppCheckout
	if len(ids) > 0 {
		if err := DB.Where("user_id = ? AND app_id = ? AND order_id IN ?", userID, appID, ids).Find(&snapshots).Error; err != nil {
			return nil, 0, err
		}
	}
	byID := make(map[string]AppCheckout, len(snapshots))
	for _, snapshot := range snapshots {
		byID[snapshot.OrderId] = snapshot
	}
	for i := range items {
		if snapshot, ok := byID[items[i].OrderId]; ok && snapshot.SnapshotReady {
			if snapshot.Kind != "subscription" || snapshot.Quota > 0 {
				items[i].Quota = new(snapshot.Quota)
				items[i].Credits = (&OAuthApp{QuotaPerCredit: snapshot.QuotaPerCredit}).Credits(snapshot.Quota)
			}
			items[i].Currency = snapshot.Currency
			if money, err := decimal.NewFromString(snapshot.Money); err == nil {
				items[i].Money = money.InexactFloat64()
			}
			items[i].QuotaPerCredit = snapshot.QuotaPerCredit
		}
	}
	return items, total, nil
}
