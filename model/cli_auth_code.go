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
	CLIAuthCodeBytes = 32
	CLIAuthCodeTTL   = 10 * time.Minute
)

var (
	ErrCLIAuthCodeNotFound = errors.New("cli authorization code not found")
	ErrCLIAuthCodeExpired  = errors.New("cli authorization code has expired")
	ErrCLIAuthCodeConsumed = errors.New("cli authorization code has already been consumed")
)

// CLIAuthCode is a single-use, short-lived code issued to a signed-in browser
// session and consumed by the CLI exchange endpoint to obtain a personal
// access token. Only the SHA-256 hash of the code is stored; the plaintext is
// returned to the browser once. The granted token name and scopes are bound
// to the code at issuance so the CLI cannot pick its own grant.
type CLIAuthCode struct {
	CodeHash   string `json:"-" gorm:"type:varchar(64);primaryKey"`
	UserID     int    `json:"user_id" gorm:"type:int;not null;index"`
	Name       string `json:"name" gorm:"type:varchar(64);not null"`
	Scopes     string `json:"-" gorm:"type:varchar(2048);not null"`
	ExpiresAt  int64  `json:"expires_at" gorm:"type:bigint;not null;index"`
	ConsumedAt int64  `json:"consumed_at" gorm:"type:bigint;default:0"`
	CreatedAt  int64  `json:"created_at" gorm:"type:bigint;not null"`
}

func (CLIAuthCode) TableName() string {
	return "cli_auth_codes"
}

// CLIAuthCodeHash returns the SHA-256 hex digest of a CLI authorization code.
func CLIAuthCodeHash(code string) string {
	digest := sha256.Sum256([]byte(code))
	return fmt.Sprintf("%x", digest)
}

// GenerateCLIAuthCode returns a new opaque CLI authorization code.
func GenerateCLIAuthCode() string {
	random := make([]byte, CLIAuthCodeBytes)
	if _, err := rand.Read(random); err != nil {
		return common.GetRandomString(48)
	}
	return base64.RawURLEncoding.EncodeToString(random)
}

// GetScopes returns the stored scope list; a malformed value grants nothing.
func (code *CLIAuthCode) GetScopes() []string {
	if code == nil || code.Scopes == "" {
		return nil
	}
	var scopes []string
	if err := common.UnmarshalJsonStr(code.Scopes, &scopes); err != nil {
		common.SysError(common.LogText("invalid cli auth code scopes: %s", err.Error()))
		return nil
	}
	return scopes
}

func (code *CLIAuthCode) SetScopes(scopes []string) error {
	data, err := common.Marshal(scopes)
	if err != nil {
		return err
	}
	code.Scopes = string(data)
	return nil
}

// CreateCLIAuthCode stores the hash of a code and returns the row.
func CreateCLIAuthCode(code *CLIAuthCode) error {
	if code == nil || code.CodeHash == "" || code.UserID <= 0 {
		return ErrCLIAuthCodeNotFound
	}
	now := common.GetTimestamp()
	if code.CreatedAt == 0 {
		code.CreatedAt = now
	}
	return DB.Create(code).Error
}

// ConsumeCLIAuthCode atomically validates and consumes a code. It enforces
// that the code exists, has not been consumed, and has not expired. The
// consumed row is returned.
func ConsumeCLIAuthCode(codePlaintext string) (*CLIAuthCode, error) {
	codePlaintext = strings.TrimSpace(codePlaintext)
	if codePlaintext == "" {
		return nil, ErrCLIAuthCodeNotFound
	}
	hash := CLIAuthCodeHash(codePlaintext)
	now := common.GetTimestamp()

	var consumed CLIAuthCode
	err := DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&CLIAuthCode{}).
			Where("code_hash = ? AND consumed_at = 0", hash).
			Update("consumed_at", now)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			// Either not found or already consumed — report consumption only
			// when the row is known to exist, to avoid leaking existence.
			var row CLIAuthCode
			if err := tx.Where("code_hash = ?", hash).First(&row).Error; err == nil {
				if row.ConsumedAt != 0 {
					return ErrCLIAuthCodeConsumed
				}
			}
			return ErrCLIAuthCodeNotFound
		}
		if err := tx.Where("code_hash = ?", hash).First(&consumed).Error; err != nil {
			return err
		}
		if consumed.ExpiresAt <= now {
			return ErrCLIAuthCodeExpired
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &consumed, nil
}

// DeleteExpiredCLIAuthCodes removes codes that expired before the cutoff.
func DeleteExpiredCLIAuthCodes(before int64) error {
	return DB.Where("expires_at < ?", before).Delete(&CLIAuthCode{}).Error
}
