package model

import (
	"errors"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrOAuthConsentNotFound = errors.New("oauth consent not found")

// OAuthConsent records that a user has granted a specific set of scopes to an
// app. It is used to skip the consent screen on subsequent authorizations for
// the same (app, user, scope) combination.
type OAuthConsent struct {
	Id        int    `json:"id" gorm:"primaryKey"`
	AppID     int    `json:"app_id" gorm:"type:int;not null;uniqueIndex:idx_oauth_consent_app_user"`
	UserID    int    `json:"user_id" gorm:"type:int;not null;uniqueIndex:idx_oauth_consent_app_user"`
	Scope     string `json:"scope" gorm:"type:varchar(512);not null"`
	GrantedAt int64  `json:"granted_at" gorm:"type:bigint;not null"`
}

func (OAuthConsent) TableName() string {
	return "oauth_consents"
}

// NormalizeScopeString sorts and de-duplicates a space-separated scope string
// so that equivalent scope sets compare equal.
func NormalizeScopeString(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return ""
	}
	parts := strings.Fields(scope)
	seen := make(map[string]struct{}, len(parts))
	unique := make([]string, 0, len(parts))
	for _, p := range parts {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		unique = append(unique, p)
	}
	sort.Strings(unique)
	return strings.Join(unique, " ")
}

// HasOAuthConsent reports whether the user has previously granted exactly the
// requested scope set to the app. Scope order and duplicates are normalized
// before comparison.
func HasOAuthConsent(appID, userID int, scope string) (bool, error) {
	if appID <= 0 || userID <= 0 {
		return false, nil
	}
	normalized := NormalizeScopeString(scope)
	var consent OAuthConsent
	err := DB.Where("app_id = ? AND user_id = ?", appID, userID).First(&consent).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return NormalizeScopeString(consent.Scope) == normalized, nil
}

// GrantOAuthConsent records or updates the user's consent for the app. If a
// consent row already exists for (app, user), the scope is updated.
func GrantOAuthConsent(appID, userID int, scope string) error {
	if appID <= 0 || userID <= 0 {
		return ErrOAuthConsentNotFound
	}
	normalized := NormalizeScopeString(scope)
	now := common.GetTimestamp()
	return DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "app_id"}, {Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"scope", "granted_at"}),
	}).Create(&OAuthConsent{
		AppID:     appID,
		UserID:    userID,
		Scope:     normalized,
		GrantedAt: now,
	}).Error
}

// DeleteOAuthConsent removes a user's consent for an app.
func DeleteOAuthConsent(appID, userID int) error {
	return DB.Where("app_id = ? AND user_id = ?", appID, userID).Delete(&OAuthConsent{}).Error
}

// DeleteOAuthConsentsByApp removes all consent records for an app.
func DeleteOAuthConsentsByApp(appID int) error {
	return DB.Where("app_id = ?", appID).Delete(&OAuthConsent{}).Error
}
