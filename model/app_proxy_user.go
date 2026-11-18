package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const UserSourceProxy = "proxy"

var ErrProxyIdentity = errors.New("proxy identity or app ownership is invalid")
var ErrProxySubscriptionUnsupported = errors.New("subscriptions are not supported for proxy users")

// AppProxyIdentity is a durable recovery key, independent of email or browser metadata.
type AppProxyIdentity struct {
	IdentityKey string `gorm:"primaryKey;type:varchar(64)"`
	AppId       int    `gorm:"not null;index"`
	Issuer      string `gorm:"type:text;not null"`
	Subject     string `gorm:"type:text;not null"`
	UserId      int    `gorm:"not null;index"`
}

func (u *User) IsProxy() bool { return u.IdentitySource == UserSourceProxy }

func RequireNativeUserTx(tx *gorm.DB, userID int) error {
	if tx == nil || userID <= 0 {
		return ErrProxyIdentity
	}
	var user User
	if err := tx.Select("id", "identity_source").First(&user, userID).Error; err != nil {
		return err
	}
	if user.IsProxy() {
		return ErrProxyIdentity
	}
	return nil
}

// ProvisionAppProxyUser atomically creates or recovers a separate account for an
// App and a verified native subject. Only a trusted, scoped backend may call it.
func ProvisionAppProxyUser(appID int, issuer, subject string) (*User, error) {
	if appID <= 0 || issuer == "" || len(issuer) > 2048 || subject == "" || len(subject) > 512 || strings.TrimSpace(subject) != subject {
		return nil, ErrProxyIdentity
	}
	encoded, err := common.Marshal([]any{appID, issuer, subject})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	identity := AppProxyIdentity{IdentityKey: hex.EncodeToString(digest[:]), AppId: appID, Issuer: issuer, Subject: subject}
	var user User
	err = DB.Transaction(func(tx *gorm.DB) error {
		// INSERT first also serializes SQLite writers before any read snapshot.
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&identity).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(&identity, "identity_key = ?", identity.IdentityKey).Error; err != nil {
			return err
		}
		if identity.AppId != appID || identity.Issuer != issuer || identity.Subject != subject {
			return ErrProxyIdentity
		}
		if identity.UserId > 0 {
			if err := tx.First(&user, identity.UserId).Error; err != nil {
				return err
			}
			if !user.IsProxy() || user.OwnerAppId != appID {
				return ErrProxyIdentity
			}
			// Recovery never re-enables the user or requires an active App.
			return nil
		}
		var app OAuthApp
		if err := lockForUpdate(tx).First(&app, appID).Error; err != nil {
			return err
		}
		if !app.IsEnabled() || !app.APIKeyEnabled || app.ProxyIssuer == "" || app.ProxyIssuer != issuer {
			return ErrProxyIdentity
		}
		key, err := common.GenerateKey()
		if err != nil {
			return err
		}
		user = User{Username: "p_" + key[:18], Password: "", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", IdentitySource: UserSourceProxy, OwnerAppId: appID, AffCode: key[18:34], AuthVersion: 1}
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		if _, err := ensureUserAppWallet(tx, user.Id, appID); err != nil {
			return err
		}
		return tx.Model(&AppProxyIdentity{}).Where("identity_key = ? AND user_id = 0", identity.IdentityKey).Update("user_id", user.Id).Error
	})
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func ValidateUserAppScope(user *User, appID int) error {
	if user == nil || appID < 0 || (user.IsProxy() && (appID == 0 || user.OwnerAppId != appID)) {
		return ErrProxyIdentity
	}
	return nil
}

func (u *User) Source() string {
	if u.IsProxy() {
		return UserSourceProxy
	}
	return "platform"
}

type AppWalletResponse struct {
	UserId           int     `json:"user_id"`
	AppId            int     `json:"app_id"`
	IdentitySource   string  `json:"identity_source"`
	Quota            int     `json:"quota"`
	UsedQuota        int     `json:"used_quota"`
	QuotaPerCredit   *int64  `json:"quota_per_credit"`
	Credits          *string `json:"credits"`
	UsedCredits      *string `json:"used_credits"`
	RequestCount     *int    `json:"request_count"`
	StatisticsWindow any     `json:"statistics_window"`
	Capabilities     struct {
		SubscriptionSupported bool `json:"subscription_supported"`
	} `json:"capabilities"`
}

func ReadAppWallet(userID, appID int) (*AppWalletResponse, error) {
	user, err := GetUserById(userID, true)
	if err != nil {
		return nil, err
	}
	if err := ValidateUserAppScope(user, appID); err != nil {
		return nil, err
	}
	result := &AppWalletResponse{UserId: userID, AppId: appID, IdentitySource: user.Source()}
	result.Capabilities.SubscriptionSupported = !user.IsProxy()
	if appID == 0 {
		result.Quota, result.UsedQuota, result.RequestCount = user.Quota, user.UsedQuota, &user.RequestCount
		result.StatisticsWindow = "lifetime"
		return result, nil
	}
	app, err := GetOAuthAppByID(appID)
	if err != nil {
		return nil, err
	}
	var wallet UserAppWallet
	err = DB.Where("user_id = ? AND app_id = ?", userID, appID).First(&wallet).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("read app wallet: %w", err)
	}
	result.Quota, result.UsedQuota = wallet.Quota, wallet.UsedQuota
	result.QuotaPerCredit, result.Credits, result.UsedCredits = app.QuotaPerCredit, app.Credits(wallet.Quota), app.Credits(wallet.UsedQuota)
	return result, nil
}

// Source filtering is shared by count, search and page queries.
func (options UserSortOptions) FilterSource(query *gorm.DB) *gorm.DB {
	switch options.IdentitySource {
	case UserSourceProxy:
		return query.Where("users.identity_source = ?", UserSourceProxy)
	case "platform":
		return query.Where("users.identity_source IS NULL OR users.identity_source <> ?", UserSourceProxy)
	default:
		return query
	}
}

// Identity ownership is output-only and can only be set by provisioning.
func (u *User) UnmarshalJSON(data []byte) error {
	type userJSON User
	var decoded userJSON
	if err := common.Unmarshal(data, &decoded); err != nil {
		return err
	}
	decoded.IdentitySource, decoded.OwnerAppId = "", 0
	*u = User(decoded)
	return nil
}
