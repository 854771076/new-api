package model

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const (
	OAuthRefreshTokenBytes = 32
	OAuthRefreshTokenTTL   = 30 * 24 * time.Hour
)

var (
	ErrOAuthRefreshTokenNotFound = errors.New("refresh token not found")
	ErrOAuthRefreshTokenExpired  = errors.New("refresh token has expired")
	ErrOAuthRefreshTokenRevoked  = errors.New("refresh token has been revoked")
)

// OAuthRefreshToken is an opaque refresh token. Only the SHA-256 hash is
// stored. Refresh tokens are rotated on every use; the previous token's hash
// is recorded to detect replay attempts.
type OAuthRefreshToken struct {
	TokenHash     string `json:"-" gorm:"type:varchar(64);primaryKey"`
	AppID         int    `json:"app_id" gorm:"type:int;not null;index"`
	UserID        int    `json:"user_id" gorm:"type:int;not null;index"`
	SessionID     string `json:"session_id" gorm:"type:varchar(64);not null"`
	AuthVersion   int64  `json:"-" gorm:"type:bigint;not null;default:0"`
	AuthTime      int64  `json:"-" gorm:"type:bigint;not null;default:0"`
	Scope         string `json:"scope" gorm:"type:varchar(512);not null"`
	ExpiresAt     int64  `json:"expires_at" gorm:"type:bigint;not null;index"`
	RevokedAt     int64  `json:"revoked_at" gorm:"type:bigint;default:0;index"`
	PrevTokenHash string `json:"-" gorm:"type:varchar(64);default:''"`
	CreatedAt     int64  `json:"created_at" gorm:"type:bigint;not null"`
}

func (OAuthRefreshToken) TableName() string {
	return "oauth_refresh_tokens"
}

// OAuthRefreshTokenHash returns the SHA-256 hex digest of a refresh token.
func OAuthRefreshTokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", digest)
}

// GenerateOAuthRefreshToken returns a new opaque refresh token.
func GenerateOAuthRefreshToken() (string, error) {
	random := make([]byte, OAuthRefreshTokenBytes)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

// CreateOAuthRefreshToken stores the hash of a refresh token.
func CreateOAuthRefreshToken(token *OAuthRefreshToken) error {
	if token == nil || token.TokenHash == "" || token.AppID <= 0 || token.UserID <= 0 {
		return ErrOAuthRefreshTokenNotFound
	}
	if token.CreatedAt == 0 {
		token.CreatedAt = common.GetTimestamp()
	}
	if token.AuthTime <= 0 {
		return ErrOAuthGrantSessionInvalid
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if _, err := validateOAuthGrantSession(tx, token.UserID, token.SessionID, token.AuthVersion, token.AuthTime, true); err != nil {
			return err
		}
		return tx.Create(token).Error
	})
}

// GetOAuthRefreshTokenByHash looks up a refresh token by its hash.
func GetOAuthRefreshTokenByHash(hash string) (*OAuthRefreshToken, error) {
	if hash == "" {
		return nil, ErrOAuthRefreshTokenNotFound
	}
	var token OAuthRefreshToken
	err := DB.Where("token_hash = ?", hash).First(&token).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrOAuthRefreshTokenNotFound
	}
	if err != nil {
		return nil, err
	}
	return &token, nil
}

// RotateOAuthRefreshToken atomically revokes the old token and creates a new
// one. The old row is only consumed when it is still active, so concurrent
// rotations cannot both succeed. If the old token was already revoked or was
// rotated by a concurrent request, the entire app+user refresh token chain is
// revoked and ErrOAuthRefreshTokenRevoked is returned. Returns the newly
// created token row.
func RotateOAuthRefreshToken(oldHash, newHash string, newExpiresAt int64, appID int) (*OAuthRefreshToken, error) {
	oldHash = strings.TrimSpace(oldHash)
	if oldHash == "" || newHash == "" {
		return nil, ErrOAuthRefreshTokenNotFound
	}
	now := common.GetTimestamp()

	var newToken OAuthRefreshToken
	replayDetected := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var old OAuthRefreshToken
		if err := tx.Where("token_hash = ?", oldHash).First(&old).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOAuthRefreshTokenNotFound
			}
			return err
		}
		// Client binding is checked inside the transaction, before any side effects.
		if old.AppID != appID {
			return ErrOAuthRefreshTokenNotFound
		}
		if old.AuthTime <= 0 {
			return ErrOAuthGrantSessionInvalid
		}
		if _, err := validateOAuthGrantSession(tx, old.UserID, old.SessionID, old.AuthVersion, old.AuthTime, true); err != nil {
			return err
		}
		// A locking read observes a concurrent winner even at MySQL's default
		// REPEATABLE READ isolation, after acquiring the shared user/session locks.
		if err := lockForUpdate(tx).Where("token_hash = ?", oldHash).First(&old).Error; err != nil {
			return err
		}
		now = common.GetTimestamp()
		if newExpiresAt <= now || oldHash == newHash {
			return ErrOAuthRefreshTokenExpired
		}
		replay := old.RevokedAt != 0
		if !replay {
			if old.ExpiresAt <= now {
				return ErrOAuthRefreshTokenExpired
			}
			// Revoke the old token only if it is still active. The conditional
			// update is the single atomic gate: concurrent rotations of the same
			// token race here and exactly one of them observes RowsAffected == 1.
			result := tx.Model(&OAuthRefreshToken{}).
				Where("token_hash = ? AND revoked_at = 0", oldHash).
				Update("revoked_at", now)
			if result.Error != nil {
				return result.Error
			}
			replay = result.RowsAffected != 1
		}
		// Replay detection: a revoked or concurrently rotated token must never
		// be exchanged again. Revoke the whole chain for this app+user. The
		// update must commit, so the replay error is reported after the
		// transaction instead of rolling the revocation back.
		if replay {
			if err := tx.Model(&OAuthRefreshToken{}).
				Where("app_id = ? AND user_id = ?", old.AppID, old.UserID).
				Update("revoked_at", now).Error; err != nil {
				return err
			}
			replayDetected = true
			return nil
		}
		// Create the new token, linking back to the old one for chain tracing.
		newToken = OAuthRefreshToken{
			TokenHash:     newHash,
			AppID:         old.AppID,
			UserID:        old.UserID,
			SessionID:     old.SessionID,
			AuthVersion:   old.AuthVersion,
			AuthTime:      old.AuthTime,
			Scope:         old.Scope,
			ExpiresAt:     newExpiresAt,
			PrevTokenHash: oldHash,
			CreatedAt:     now,
		}
		return tx.Create(&newToken).Error
	})
	if err != nil {
		return nil, err
	}
	if replayDetected {
		return nil, ErrOAuthRefreshTokenRevoked
	}
	return &newToken, nil
}

// RevokeOAuthRefreshTokenByHash revokes a single refresh token.
func RevokeOAuthRefreshTokenByHash(hash string) error {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return nil
	}
	return DB.Model(&OAuthRefreshToken{}).
		Where("token_hash = ? AND revoked_at = 0", hash).
		Update("revoked_at", common.GetTimestamp()).Error
}

// RevokeOAuthRefreshTokensByAppAndUser revokes all refresh tokens for a user
// within an app. Used during logout or app-level revocation.
func RevokeOAuthRefreshTokensByAppAndUser(appID, userID int) error {
	if appID <= 0 || userID <= 0 {
		return nil
	}
	return DB.Model(&OAuthRefreshToken{}).
		Where("app_id = ? AND user_id = ? AND revoked_at = 0", appID, userID).
		Update("revoked_at", common.GetTimestamp()).Error
}

// RevokeOAuthRefreshTokensByApp revokes all refresh tokens issued to an app
// across every user. Used when an app is disabled or deleted.
func RevokeOAuthRefreshTokensByApp(appID int) error {
	if appID <= 0 {
		return nil
	}
	return DB.Model(&OAuthRefreshToken{}).
		Where("app_id = ? AND revoked_at = 0", appID).
		Update("revoked_at", common.GetTimestamp()).Error
}

// RevokeOAuthRefreshTokensByUser revokes all refresh tokens for a user across
// all apps. Used when the user logs out of new-api or changes credentials.
func RevokeOAuthRefreshTokensByUser(userID int) error {
	if userID <= 0 {
		return nil
	}
	return DB.Model(&OAuthRefreshToken{}).
		Where("user_id = ? AND revoked_at = 0", userID).
		Update("revoked_at", common.GetTimestamp()).Error
}

// DeleteExpiredOAuthRefreshTokens removes refresh tokens that expired before
// the cutoff. Revoked tokens are also cleaned up after the retention window.
func DeleteExpiredOAuthRefreshTokens(before int64) error {
	return DB.Where("expires_at < ?", before).Delete(&OAuthRefreshToken{}).Error
}
