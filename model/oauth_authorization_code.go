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
	OAuthAuthorizationCodeBytes = 32
	OAuthAuthorizationCodeTTL   = 60 * time.Second
	OAuthPKCEMethodS256         = "S256"
)

var (
	ErrOAuthCodeNotFound = errors.New("authorization code not found")
	ErrOAuthCodeExpired  = errors.New("authorization code has expired")
	ErrOAuthCodeConsumed = errors.New("authorization code has already been consumed")
	ErrOAuthCodeMismatch = errors.New("authorization code does not match client or redirect uri")
)

// OAuthAuthorizationCode is a single-use, short-lived code issued by the
// authorization endpoint and consumed at the token endpoint. Only the SHA-256
// hash of the code is stored; the plaintext is returned to the client once.
type OAuthAuthorizationCode struct {
	CodeHash            string `json:"-" gorm:"type:varchar(64);primaryKey"`
	AppID               int    `json:"app_id" gorm:"type:int;not null;index"`
	UserID              int    `json:"user_id" gorm:"type:int;not null"`
	SessionID           string `json:"session_id" gorm:"type:varchar(64);not null"`
	Scope               string `json:"scope" gorm:"type:varchar(512);not null"`
	Nonce               string `json:"nonce" gorm:"type:varchar(255)"`
	RedirectURI         string `json:"redirect_uri" gorm:"type:varchar(1024);not null"`
	CodeChallenge       string `json:"-" gorm:"type:varchar(128);not null"`
	CodeChallengeMethod string `json:"-" gorm:"type:varchar(16);not null"`
	ExpiresAt           int64  `json:"expires_at" gorm:"type:bigint;not null;index"`
	ConsumedAt          int64  `json:"consumed_at" gorm:"type:bigint;default:0"`
	CreatedAt           int64  `json:"created_at" gorm:"type:bigint;not null"`
}

func (OAuthAuthorizationCode) TableName() string {
	return "oauth_authorization_codes"
}

// OAuthCodeHash returns the SHA-256 hex digest of an authorization code.
func OAuthCodeHash(code string) string {
	digest := sha256.Sum256([]byte(code))
	return fmt.Sprintf("%x", digest)
}

// GenerateOAuthAuthorizationCode returns a new opaque authorization code.
func GenerateOAuthAuthorizationCode() string {
	random := make([]byte, OAuthAuthorizationCodeBytes)
	if _, err := rand.Read(random); err != nil {
		return common.GetRandomString(48)
	}
	return base64.RawURLEncoding.EncodeToString(random)
}

// CreateOAuthAuthorizationCode stores the hash of a code and returns the row.
func CreateOAuthAuthorizationCode(code *OAuthAuthorizationCode) error {
	if code == nil || code.CodeHash == "" || code.AppID <= 0 || code.UserID <= 0 {
		return ErrOAuthCodeNotFound
	}
	now := common.GetTimestamp()
	if code.CreatedAt == 0 {
		code.CreatedAt = now
	}
	return DB.Create(code).Error
}

// ConsumeOAuthAuthorizationCode atomically validates and consumes a code.
// It enforces that the code matches the given app and redirect URI, has not
// been consumed, and has not expired. The consumed row is returned.
func ConsumeOAuthAuthorizationCode(codePlaintext string, appID int, redirectURI string) (*OAuthAuthorizationCode, error) {
	codePlaintext = strings.TrimSpace(codePlaintext)
	if codePlaintext == "" {
		return nil, ErrOAuthCodeNotFound
	}
	hash := OAuthCodeHash(codePlaintext)
	now := common.GetTimestamp()

	var consumed OAuthAuthorizationCode
	err := DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&OAuthAuthorizationCode{}).
			Where("code_hash = ? AND consumed_at = 0", hash).
			Update("consumed_at", now)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			// Either not found or already consumed — return a generic error
			// to avoid leaking existence.
			var row OAuthAuthorizationCode
			if err := tx.Where("code_hash = ?", hash).First(&row).Error; err == nil {
				if row.ConsumedAt != 0 {
					return ErrOAuthCodeConsumed
				}
			}
			return ErrOAuthCodeNotFound
		}
		if err := tx.Where("code_hash = ?", hash).First(&consumed).Error; err != nil {
			return err
		}
		if consumed.ExpiresAt <= now {
			return ErrOAuthCodeExpired
		}
		if consumed.AppID != appID || consumed.RedirectURI != redirectURI {
			return ErrOAuthCodeMismatch
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &consumed, nil
}

// DeleteExpiredOAuthAuthorizationCodes removes codes that expired before the
// cutoff. Consumed codes are also removed once they pass the retention window.
func DeleteExpiredOAuthAuthorizationCodes(before int64) error {
	return DB.Where("expires_at < ?", before).Delete(&OAuthAuthorizationCode{}).Error
}

// GetOAuthAuthorizationCodeByHash looks up a code by hash without consuming it.
func GetOAuthAuthorizationCodeByHash(hash string) (*OAuthAuthorizationCode, error) {
	if hash == "" {
		return nil, ErrOAuthCodeNotFound
	}
	var code OAuthAuthorizationCode
	err := DB.Where("code_hash = ?", hash).First(&code).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrOAuthCodeNotFound
	}
	if err != nil {
		return nil, err
	}
	return &code, nil
}
