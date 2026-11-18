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

func UpdateAppProxyKeyPolicy(token *Token, updates map[string]any) error {
	if token == nil || token.AppId <= 0 {
		return ErrProxyIdentity
	}
	if err := invalidateTokenCacheForMutation(token.Key); err != nil {
		return err
	}
	return DB.Model(&Token{}).Where("id = ? AND user_id = ? AND app_id = ?", token.Id, token.UserId, token.AppId).Updates(updates).Error
}

// AppKeyOperation makes initial issuance and staged rotation recoverable after
// a lost response or a failure to persist the secret in the App's key store.
type AppKeyOperation struct {
	OperationKey string `gorm:"primaryKey;type:varchar(64)"`
	Fingerprint  string `gorm:"type:varchar(64);not null"`
	TokenId      int    `gorm:"not null"`
	OldTokenId   int    `gorm:"not null"`
	Committed    bool
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
	payload, err := common.Marshal([]any{token.Name, token.ExpiredTime, token.RemainQuota, token.UnlimitedQuota, token.ModelLimitsEnabled, token.ModelLimits, token.AllowIps, token.Group, oldID})
	if err != nil {
		return nil, err
	}
	fingerprint := sha256.Sum256(payload)
	op := AppKeyOperation{OperationKey: fmt.Sprintf("%x", digest), Fingerprint: fmt.Sprintf("%x", fingerprint), OldTokenId: oldID}
	var result Token
	err = DB.Transaction(func(tx *gorm.DB) error {
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
			var old Token
			if err := lockForUpdate(tx).First(&old, oldID).Error; err != nil {
				return err
			}
			if old.UserId != userID || old.AppId != appID || old.Status != common.TokenStatusEnabled {
				return ErrProxyIdentity
			}
			if !old.UnlimitedQuota {
				return ErrLimitedKeyRotation
			}
			// Copy the old policy, never accept broader replacement permissions.
			*token = old
			token.Id, token.UsedQuota, token.RemainQuota = 0, 0, 0
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
	var old Token
	err := DB.Transaction(func(tx *gorm.DB) error {
		var op AppKeyOperation
		if err := lockForUpdate(tx).Where("token_id = ? AND old_token_id > 0", tokenID).First(&op).Error; err != nil {
			return err
		}
		var replacement Token
		if err := tx.First(&replacement, tokenID).Error; err != nil {
			return err
		}
		if replacement.UserId != userID || replacement.AppId != appID || replacement.Status != common.TokenStatusEnabled || replacement.Key != replacementKey {
			return ErrProxyIdentity
		}
		if op.Committed {
			return nil
		}
		if err := tx.First(&old, op.OldTokenId).Error; err != nil {
			return err
		}
		if old.UserId != userID || old.AppId != appID || !old.UnlimitedQuota {
			return ErrProxyIdentity
		}
		if err := tx.Model(&Token{}).Where("id = ?", old.Id).Update("status", common.TokenStatusDisabled).Error; err != nil {
			return err
		}
		return tx.Model(&AppKeyOperation{}).Where("operation_key = ?", op.OperationKey).Update("committed", true).Error
	})
	if err == nil && old.Id > 0 {
		_ = invalidateTokenCacheForMutation(old.Key)
	}
	return err
}
