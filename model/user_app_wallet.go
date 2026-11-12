package model

import (
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInvalidAppId = errors.New("app id must be greater than 0")

// UserWithAppQuota is one user row joined with that user's wallet in a single
// app. The embedded User keeps the platform fields and JSON shape unchanged.
type UserWithAppQuota struct {
	User
	AppQuota     int `json:"app_quota"`
	AppUsedQuota int `json:"app_used_quota"`
}

var appUserSortColumns = map[string]string{
	"id":            "users.id",
	"username":      "users.username",
	"quota":         "w.quota",
	"used_quota":    "w.used_quota",
	"group":         "users.group",
	"created_at":    "users.created_at",
	"last_login_at": "users.last_login_at",
}

func (options UserSortOptions) applyApp(query *gorm.DB) *gorm.DB {
	columnName, ok := appUserSortColumns[options.SortBy]
	if !ok {
		columnName = "users.id"
	}
	q := query.Order(clause.OrderByColumn{
		Column: clause.Column{Name: columnName},
		Desc:   options.SortOrder != "asc",
	})
	if columnName != "users.id" {
		q = q.Order(clause.OrderByColumn{
			Column: clause.Column{Name: "users.id"},
			Desc:   true,
		})
	}
	return q
}

func appUsersQuery(query *gorm.DB, appId int) *gorm.DB {
	return query.Unscoped().Table("users").
		Joins("LEFT JOIN user_app_wallets AS w ON w.user_id = users.id AND w.app_id = ?", appId)
}

// GetAllUsersForApp lists users together with their quota in the given app.
// Callers keep using GetAllUsers for the platform (app_id = 0) view.
func GetAllUsersForApp(appId int, pageInfo *common.PageInfo, sortOptions ...UserSortOptions) ([]*UserWithAppQuota, int64, error) {
	if appId <= 0 {
		return nil, 0, ErrInvalidAppId
	}
	var users []*UserWithAppQuota
	var total int64
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := appUsersQuery(tx, appId)
	if err := query.Count(&total).Error; err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	order := resolveUserSortOptions(sortOptions)
	err := order.applyApp(query).
		Select("users.*, COALESCE(w.quota, 0) AS app_quota, COALESCE(w.used_quota, 0) AS app_used_quota").
		Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).
		Scan(&users).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}
	if err := tx.Commit().Error; err != nil {
		return nil, 0, err
	}
	clearUserSecrets(users)
	return users, total, nil
}

// SearchUsersForApp applies the same filters as SearchUsers while joining the
// given app wallet. Every column reference is qualified because both tables
// expose id, created_at, and quota columns.
func SearchUsersForApp(appId int, keyword string, group string, role *int, status *int, startIdx int, num int, sortOptions ...UserSortOptions) ([]*UserWithAppQuota, int64, error) {
	if appId <= 0 {
		return nil, 0, ErrInvalidAppId
	}
	var users []*UserWithAppQuota
	var total int64
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := appUsersQuery(tx, appId)
	likeCondition := "users.username LIKE ? OR users.email LIKE ? OR users.display_name LIKE ?"
	likeArgs := []any{"%" + keyword + "%", "%" + keyword + "%", "%" + keyword + "%"}
	if keywordInt, err := strconv.Atoi(keyword); err == nil {
		likeCondition = "users.id = ? OR " + likeCondition
		likeArgs = append([]any{keywordInt}, likeArgs...)
	}
	query = query.Where("("+likeCondition+")", likeArgs...)
	if group != "" {
		query = query.Where("users."+commonGroupCol+" = ?", group)
	}
	if role != nil {
		query = query.Where("users.role = ?", *role)
	}
	if status != nil {
		if *status == -1 {
			query = query.Where("users.deleted_at IS NOT NULL")
		} else {
			query = query.Where("users.deleted_at IS NULL").Where("users.status = ?", *status)
		}
	}
	if err := query.Count(&total).Error; err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	order := resolveUserSortOptions(sortOptions)
	err := order.applyApp(query).
		Select("users.*, COALESCE(w.quota, 0) AS app_quota, COALESCE(w.used_quota, 0) AS app_used_quota").
		Limit(num).Offset(startIdx).
		Scan(&users).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}
	if err := tx.Commit().Error; err != nil {
		return nil, 0, err
	}
	clearUserSecrets(users)
	return users, total, nil
}

// clearUserSecrets mirrors the password column omission of the platform list
// queries after selecting users.* for the join.
func clearUserSecrets(users []*UserWithAppQuota) {
	for _, user := range users {
		user.Password = ""
		user.AccessToken = nil
	}
}

// UserAppWallet is the quota pool a user holds inside one external app.
type UserAppWallet struct {
	Id int `json:"id"`

	UserId int `json:"user_id" gorm:"uniqueIndex:idx_user_app_wallet,priority:1"`
	AppId  int `json:"app_id" gorm:"uniqueIndex:idx_user_app_wallet,priority:2;index"`

	Quota     int `json:"quota"`
	UsedQuota int `json:"used_quota"`

	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

func (w *UserAppWallet) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	w.CreatedAt = now
	w.UpdatedAt = now
	return nil
}

func (w *UserAppWallet) BeforeUpdate(tx *gorm.DB) error {
	w.UpdatedAt = common.GetTimestamp()
	return nil
}

// EnsureUserAppWallet lazily creates the wallet row with quota 0. The unique
// index is the concurrency guarantee; a lost create race falls back to the
// row created by the winner.
func EnsureUserAppWallet(userId, appId int) (*UserAppWallet, error) {
	return ensureUserAppWallet(DB, userId, appId)
}

func ensureUserAppWallet(db *gorm.DB, userId, appId int) (*UserAppWallet, error) {
	if appId <= 0 {
		return nil, ErrInvalidAppId
	}
	wallet := UserAppWallet{UserId: userId, AppId: appId}
	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "app_id"}},
		DoNothing: true,
	}).Create(&wallet).Error; err != nil {
		return nil, err
	}
	// Read the winning row, not the attempted insert's zero quota or timestamps.
	// A locking read also sees a newly committed winner in MySQL transactions
	// that already established a repeatable-read snapshot before the insert.
	var existing UserAppWallet
	if err := lockForUpdate(db).Where("user_id = ? AND app_id = ?", userId, appId).First(&existing).Error; err != nil {
		return nil, err
	}
	return &existing, nil
}

// TransferUserQuotaToApp atomically moves platform quota into an app wallet and
// records the completed balance top-up. Cache changes happen only after commit.
func TransferUserQuotaToApp(userId, appId int, amount int64, quota int) error {
	if userId <= 0 || amount <= 0 {
		return ErrInvalidTopUpQuota
	}
	if appId <= 0 {
		return ErrInvalidAppId
	}
	if _, err := topUpQuotaMaxCurrent(quota); err != nil {
		return err
	}
	// Batched relay deductions can exist only in Redis (including other
	// instances), so a database transaction cannot safely spend that balance.
	// Fail closed rather than reserve/refund in a second financial system.
	if common.BatchUpdateEnabled {
		return errors.New("platform balance transfers require immediate quota persistence")
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&User{}).Where("id = ? AND quota >= ?", userId, quota).
			Update("quota", gorm.Expr("quota - ?", quota))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return common.NewMessage("Insufficient balance")
		}
		if err := increaseUserAppQuota(tx, userId, appId, quota); err != nil {
			if errors.Is(err, ErrWalletQuotaLimitExceeded) {
				return ErrTopUpQuotaLimitExceeded
			}
			return err
		}
		now := common.GetTimestamp()
		return tx.Create(&TopUp{
			UserId: userId, AppId: appId, Amount: amount,
			TradeNo:       "APP_BALANCE-" + strconv.Itoa(appId) + "-" + strconv.Itoa(userId) + "-" + common.GetRandomString(16),
			PaymentMethod: PaymentMethodBalance, PaymentProvider: PaymentProviderBalance,
			CreateTime: now, CompleteTime: now, Status: common.TopUpStatusSuccess,
		}).Error
	})
	if err != nil {
		return err
	}
	if err := cacheDecrUserQuota(userId, int64(quota)); err != nil {
		common.SysLog(common.LogText("failed to decrease user quota cache after app balance top-up: %s", err.Error()))
	}
	return nil
}

func GetUserAppQuota(userId, appId int) (int, error) {
	if appId <= 0 {
		return 0, ErrInvalidAppId
	}
	var wallet UserAppWallet
	err := DB.Select("quota").Where("user_id = ? AND app_id = ?", userId, appId).First(&wallet).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return wallet.Quota, nil
}

// TryReserveUserAppQuota atomically checks and deducts the app wallet balance.
func TryReserveUserAppQuota(userId, appId, quota int) (bool, error) {
	if appId <= 0 {
		return false, ErrInvalidAppId
	}
	if quota < 0 {
		return false, errors.New("quota cannot be negative")
	}
	if quota == 0 {
		return true, nil
	}
	if _, err := EnsureUserAppWallet(userId, appId); err != nil {
		return false, err
	}
	result := DB.Model(&UserAppWallet{}).
		Where("user_id = ? AND app_id = ? AND quota >= ?", userId, appId, quota).
		Update("quota", gorm.Expr("quota - ?", quota))
	return result.RowsAffected == 1, result.Error
}

func IncreaseUserAppQuota(userId, appId, quota int) error {
	return increaseUserAppQuota(DB, userId, appId, quota)
}

// increaseUserAppQuota is the transaction-aware form used by settlements that
// must write the wallet and the order status atomically.
func increaseUserAppQuota(db *gorm.DB, userId, appId, quota int) error {
	if appId <= 0 {
		return ErrInvalidAppId
	}
	if quota < 0 {
		return errors.New("quota cannot be negative")
	}
	if err := common.ValidateWalletQuota(quota); err != nil {
		return err
	}
	if _, err := ensureUserAppWallet(db, userId, appId); err != nil {
		return err
	}
	result := db.Model(&UserAppWallet{}).
		Where("user_id = ? AND app_id = ? AND quota <= ?", userId, appId, common.MaxWalletQuota-quota).
		Update("quota", gorm.Expr("quota + ?", quota))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrWalletQuotaLimitExceeded
	}
	return nil
}

// DecreaseUserAppQuota settles a charge. Like DecreaseUserQuota it allows the
// balance to go negative when the settled amount exceeds the reserved one.
func DecreaseUserAppQuota(userId, appId, quota int) error {
	if appId <= 0 {
		return ErrInvalidAppId
	}
	if quota < 0 {
		return errors.New("quota cannot be negative")
	}
	if _, err := EnsureUserAppWallet(userId, appId); err != nil {
		return err
	}
	return DB.Model(&UserAppWallet{}).
		Where("user_id = ? AND app_id = ?", userId, appId).
		Update("quota", gorm.Expr("quota - ?", quota)).Error
}

func IncreaseUserAppUsedQuota(userId, appId, quota int) error {
	if appId <= 0 {
		return ErrInvalidAppId
	}
	if quota < 0 {
		return errors.New("quota cannot be negative")
	}
	if _, err := EnsureUserAppWallet(userId, appId); err != nil {
		return err
	}
	return DB.Model(&UserAppWallet{}).
		Where("user_id = ? AND app_id = ?", userId, appId).
		Update("used_quota", gorm.Expr("used_quota + ?", quota)).Error
}

// AdjustUserAppUsedQuota applies a signed delta to the app wallet used_quota.
// The balance floor is clamped in one UPDATE because GREATEST/MAX do not share
// SQLite, MySQL, and PostgreSQL semantics; a conditional CASE expression does.
func AdjustUserAppUsedQuota(userId, appId, delta int) error {
	return adjustUserAppUsedQuota(DB, userId, appId, delta)
}

func adjustUserAppUsedQuota(db *gorm.DB, userId, appId, delta int) error {
	if appId <= 0 {
		return ErrInvalidAppId
	}
	if delta == 0 {
		return nil
	}
	if _, err := ensureUserAppWallet(db, userId, appId); err != nil {
		return err
	}
	return db.Model(&UserAppWallet{}).
		Where("user_id = ? AND app_id = ?", userId, appId).
		Update("used_quota", gorm.Expr(
			"CASE WHEN used_quota + ? < 0 THEN 0 ELSE used_quota + ? END", delta, delta,
		)).Error
}

// GetUserAppWallets lists every app wallet the user owns (app_id > 0). It is
// the self-service view and must not leak other users' wallets.
func GetUserAppWallets(userId int) ([]UserAppWallet, error) {
	if userId <= 0 {
		return nil, errors.New("invalid userId")
	}
	var wallets []UserAppWallet
	if err := DB.Where("user_id = ? AND app_id > ?", userId, 0).
		Order("app_id asc").Find(&wallets).Error; err != nil {
		return nil, err
	}
	return wallets, nil
}

// ListUserAppWalletsForApp lists the wallet rows (balance + used quota) of one
// app. Admin only; appId must be positive.
func ListUserAppWalletsForApp(appId int, pageInfo *common.PageInfo) ([]UserAppWallet, int64, error) {
	if appId <= 0 {
		return nil, 0, ErrInvalidAppId
	}
	var wallets []UserAppWallet
	var total int64
	query := DB.Model(&UserAppWallet{}).Where("app_id = ?", appId)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := query.Order("id desc").
		Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).
		Find(&wallets).Error; err != nil {
		return nil, 0, err
	}
	return wallets, total, nil
}
