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
	err := db.Where("user_id = ? AND app_id = ?", userId, appId).FirstOrCreate(&wallet).Error
	if err == nil {
		return &wallet, nil
	}
	var existing UserAppWallet
	if findErr := db.Where("user_id = ? AND app_id = ?", userId, appId).First(&existing).Error; findErr == nil {
		return &existing, nil
	}
	return nil, err
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
