package model

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrAppOperationConflict = errors.New("operation id was already used with different parameters")
var ErrLimitedKeyRotation = errors.New("limited key rotation is unavailable without a shared spending budget")
var ErrRotationCancelled = errors.New("rotation_cancelled")

func UpdateAppProxyKeyPolicy(token *Token, updates map[string]any) error {
	if token == nil || token.AppId <= 0 {
		return ErrProxyIdentity
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var op AppKeyOperation
		err := tx.Where("token_id = ? AND old_token_id > 0", token.Id).First(&op).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			var source Token
			if err := lockForUpdate(tx).First(&source, op.OldTokenId).Error; err != nil {
				return err
			}
			if err := lockForUpdate(tx).First(&op, "operation_key = ?", op.OperationKey).Error; err != nil {
				return err
			}
			if updates["status"] == common.TokenStatusEnabled && op.Cancelled {
				return ErrRotationCancelled
			}
		}
		var current Token
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ? AND app_id = ?", token.Id, token.UserId, token.AppId).First(&current).Error; err != nil {
			return err
		}
		if err := invalidateTokenCacheForMutation(current.Key); err != nil {
			return err
		}
		return tx.Model(&current).Updates(updates).Error
	})
}

// AppKeyOperation makes initial issuance and staged rotation recoverable after
// a lost response or a failure to persist the secret in the App's key store.
type AppKeyOperation struct {
	OperationKey string `gorm:"primaryKey;type:varchar(64)"`
	Fingerprint  string `gorm:"type:varchar(64);not null"`
	TokenId      int    `gorm:"not null"`
	OldTokenId   int    `gorm:"not null"`
	Committed    bool
	Cancelled    bool `gorm:"not null;default:false"`
}

func CreateAppProxyKey(userID, appID int, operationID string, token *Token, oldID int) (*Token, error) {
	if len(operationID) < 8 || len(operationID) > 128 || token == nil {
		return nil, ErrProxyIdentity
	}
	encoded, err := common.Marshal([]any{userID, appID, operationID})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	payloadValues := []any{token.Name, token.ExpiredTime, token.RemainQuota, token.UnlimitedQuota, token.ModelLimitsEnabled, token.ModelLimits, token.AllowIps, token.Group, oldID}
	// Preserve existing operation fingerprints when the optional binding is absent.
	if token.QuotaRuleId != 0 {
		payloadValues = append(payloadValues, token.QuotaRuleId)
	}
	payload, err := common.Marshal(payloadValues)
	if err != nil {
		return nil, err
	}
	fingerprint := sha256.Sum256(payload)
	op := AppKeyOperation{OperationKey: fmt.Sprintf("%x", digest), Fingerprint: fmt.Sprintf("%x", fingerprint), OldTokenId: oldID}
	var result Token
	err = DB.Transaction(func(tx *gorm.DB) error {
		// All rotation mutations and binding serialize source -> operation -> replacement.
		var old Token
		if oldID > 0 {
			if err := lockForUpdate(tx).First(&old, oldID).Error; err != nil {
				return err
			}
			if old.UserId != userID || old.AppId != appID {
				return ErrProxyIdentity
			}
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&op).Error; err != nil {
			return err
		}
		stored := AppKeyOperation{}
		if err := lockForUpdate(tx).First(&stored, "operation_key = ?", op.OperationKey).Error; err != nil {
			return err
		}
		if stored.Fingerprint != op.Fingerprint {
			return ErrAppOperationConflict
		}
		if stored.Cancelled {
			return ErrRotationCancelled
		}
		var user User
		if err := tx.First(&user, userID).Error; err != nil {
			return err
		}
		if !user.IsProxy() || user.OwnerAppId != appID {
			return ErrProxyIdentity
		}
		if stored.TokenId > 0 {
			if err := tx.First(&result, stored.TokenId).Error; err != nil {
				return err
			}
			if result.UserId != userID || result.AppId != appID {
				return ErrProxyIdentity
			}
			return nil
		}
		var app OAuthApp
		if err := tx.First(&app, appID).Error; err != nil {
			return err
		}
		if user.Status != common.UserStatusEnabled || !app.APIKeyEnabled || !app.IsEnabled() {
			return ErrProxyIdentity
		}
		if oldID > 0 {
			if old.UserId != userID || old.AppId != appID || old.Status != common.TokenStatusEnabled {
				return ErrProxyIdentity
			}
			if !old.UnlimitedQuota || old.QuotaRuleId > 0 || token.QuotaRuleId > 0 {
				return ErrLimitedKeyRotation
			}
			// Copy the old policy, never accept broader replacement permissions.
			*token = old
			token.Id, token.UsedQuota, token.RemainQuota = 0, 0, 0
		}
		if token.QuotaRuleId < 0 {
			return ErrProxyIdentity
		}
		if token.QuotaRuleId > 0 {
			var rule TokenQuotaRule
			if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", token.QuotaRuleId, userID).First(&rule).Error; err != nil {
				return err
			}
		}
		key, err := common.GenerateKey()
		if err != nil {
			return err
		}
		token.Id, token.UserId, token.AppId, token.Key = 0, userID, appID, key
		token.Status, token.CreatedTime, token.AccessedTime = common.TokenStatusEnabled, common.GetTimestamp(), common.GetTimestamp()
		token.AllowUserQuotaQuery = true
		if err := tx.Create(token).Error; err != nil {
			return err
		}
		result = *token
		return tx.Model(&AppKeyOperation{}).Where("operation_key = ?", op.OperationKey).Update("token_id", token.Id).Error
	})
	return &result, err
}

// CommitAppProxyKeyRotation is called only after the backend has saved and
// verified the replacement. Retrying cannot revoke a different old key.
func CommitAppProxyKeyRotation(userID, appID, tokenID int, replacementKey string) error {
	return finishAppProxyKeyRotation(userID, appID, tokenID, replacementKey, false)
}

func CancelAppProxyKeyRotation(userID, appID, tokenID int) error {
	return finishAppProxyKeyRotation(userID, appID, tokenID, "", true)
}

func finishAppProxyKeyRotation(userID, appID, tokenID int, replacementKey string, cancel bool) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var op AppKeyOperation
		if err := tx.Where("token_id = ? AND old_token_id > 0", tokenID).First(&op).Error; err != nil {
			return err
		}
		var old Token
		if err := lockForUpdate(tx).First(&old, op.OldTokenId).Error; err != nil {
			return err
		}
		if old.UserId != userID || old.AppId != appID {
			return ErrProxyIdentity
		}
		if err := lockForUpdate(tx).First(&op, "operation_key = ?", op.OperationKey).Error; err != nil {
			return err
		}
		var replacement Token
		if err := lockForUpdate(tx).First(&replacement, tokenID).Error; err != nil {
			return err
		}
		if replacement.UserId != userID || replacement.AppId != appID {
			return ErrProxyIdentity
		}
		if cancel {
			if op.Committed {
				return ErrAppOperationConflict
			}
			if err := invalidateTokenCacheForMutation(replacement.Key); err != nil {
				return err
			}
			if err := tx.Model(&replacement).Update("status", common.TokenStatusDisabled).Error; err != nil {
				return err
			}
			return tx.Model(&op).Update("cancelled", true).Error
		}
		if op.Cancelled {
			return ErrRotationCancelled
		}
		if replacement.Status != common.TokenStatusEnabled || replacement.Key != replacementKey {
			return ErrProxyIdentity
		}
		if op.Committed {
			return nil
		}
		if !old.UnlimitedQuota || old.QuotaRuleId > 0 {
			return ErrLimitedKeyRotation
		}
		var app OAuthApp
		if err := tx.First(&app, appID).Error; err != nil {
			return err
		}
		if !app.IsEnabled() || !app.APIKeyEnabled {
			return ErrProxyIdentity
		}
		if err := invalidateTokenCacheForMutation(old.Key); err != nil {
			return err
		}
		if err := tx.Model(&Token{}).Where("id = ?", old.Id).Update("status", common.TokenStatusDisabled).Error; err != nil {
			return err
		}
		return tx.Model(&AppKeyOperation{}).Where("operation_key = ?", op.OperationKey).Update("committed", true).Error
	})
}
