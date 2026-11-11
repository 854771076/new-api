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
	CodeHash  string `json:"-" gorm:"type:varchar(64);primaryKey"`
	AppID     int    `json:"app_id" gorm:"type:int;not null;index"`
	UserID    int    `json:"user_id" gorm:"type:int;not null"`
	SessionID string `json:"session_id" gorm:"type:varchar(64);not null"`
	// Zero defaults deliberately invalidate grants issued before generation binding.
	AuthVersion         int64  `json:"-" gorm:"type:bigint;not null;default:0"`
	AuthTime            int64  `json:"-" gorm:"type:bigint;not null;default:0"`
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
func GenerateOAuthAuthorizationCode() (string, error) {
	random := make([]byte, OAuthAuthorizationCodeBytes)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
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
	return DB.Transaction(func(tx *gorm.DB) error {
		session, err := validateOAuthGrantSession(tx, code.UserID, code.SessionID, code.AuthVersion, 0, true)
		if err != nil {
			return err
		}
		code.AuthTime = session.CreatedAt
		return tx.Create(code).Error
	})
}

// ConsumeOAuthAuthorizationCode atomically validates and consumes a code.
// It enforces that the code matches the given app and redirect URI, has not
// been consumed, and has not expired. The consumed row is returned.
func ConsumeOAuthAuthorizationCode(codePlaintext string, appID int, redirectURI, codeVerifier string) (*OAuthAuthorizationCode, error) {
	codePlaintext = strings.TrimSpace(codePlaintext)
	if codePlaintext == "" {
		return nil, ErrOAuthCodeNotFound
	}
	hash := OAuthCodeHash(codePlaintext)
	now := common.GetTimestamp()

	var consumed OAuthAuthorizationCode
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("code_hash = ?", hash).First(&consumed).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOAuthCodeNotFound
			}
			return err
		}
		if consumed.ConsumedAt != 0 {
			return ErrOAuthCodeConsumed
		}
		if consumed.AppID != appID || consumed.RedirectURI != redirectURI {
			return ErrOAuthCodeMismatch
		}
		sum := sha256.Sum256([]byte(codeVerifier))
		if !validOAuthCodeVerifier(codeVerifier) || consumed.CodeChallengeMethod != OAuthPKCEMethodS256 ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != consumed.CodeChallenge {
			return ErrOAuthCodeMismatch
		}
		if consumed.AuthTime <= 0 {
			return ErrOAuthGrantSessionInvalid
		}
		if _, err := validateOAuthGrantSession(tx, consumed.UserID, consumed.SessionID, consumed.AuthVersion, consumed.AuthTime, true); err != nil {
			return err
		}
		now = common.GetTimestamp()
		if consumed.ExpiresAt <= now {
			return ErrOAuthCodeExpired
		}
		result := tx.Model(&OAuthAuthorizationCode{}).
			Where("code_hash = ? AND consumed_at = 0 AND expires_at > ?", hash, now).
			Update("consumed_at", now)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrOAuthCodeConsumed
		}
		consumed.ConsumedAt = now
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &consumed, nil
}

func validOAuthCodeVerifier(verifier string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	for _, char := range verifier {
		if !(char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || strings.ContainsRune("-._~", char)) {
			return false
		}
	}
	return true
}

// ErrOAuthGrantSessionInvalid includes legacy unbound grants and grants whose
// authentication generation or owning browser session is no longer live.
var ErrOAuthGrantSessionInvalid = errors.New("oauth grant session is invalid")

// ValidateOAuthGrantSession checks authoritative state, never a cached session.
// It is a read-only check used on every access-token validation, so it must not
// take row locks; grant issuance/consumption/rotation serialize separately.
func ValidateOAuthGrantSession(userID int, sid string, authVersion, authTime int64) error {
	if authTime <= 0 {
		return ErrOAuthGrantSessionInvalid
	}
	_, err := validateOAuthGrantSession(DB, userID, sid, authVersion, authTime, false)
	return err
}

// When forUpdate is true, lock the user before the session to serialize with
// credential changes and logout, and keep these locks through grant insertion,
// consumption, and rotation. SQLite uses its transactional single-writer gate
// instead of FOR UPDATE.
func validateOAuthGrantSession(db *gorm.DB, userID int, sid string, authVersion, authTime int64, forUpdate bool) (*UserSession, error) {
	if userID <= 0 || sid == "" || authVersion <= 0 {
		return nil, ErrOAuthGrantSessionInvalid
	}
	query := db
	if forUpdate {
		query = lockForUpdate(db)
	}
	var user User
	if err := query.Select("id", "status", "auth_version").Where("id = ?", userID).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrOAuthGrantSessionInvalid
		}
		return nil, err
	}
	if user.Status != common.UserStatusEnabled || user.AuthVersion != authVersion {
		return nil, ErrOAuthGrantSessionInvalid
	}
	query = db
	if forUpdate {
		query = lockForUpdate(db)
	}
	var session UserSession
	if err := query.Where("sid = ? AND user_id = ?", sid, userID).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrOAuthGrantSessionInvalid
		}
		return nil, err
	}
	now := common.GetTimestamp()
	if session.Status != UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= now ||
		session.UserAuthVersion != authVersion || session.CreatedAt <= 0 || session.CreatedAt > now ||
		(authTime != 0 && session.CreatedAt != authTime) {
		return nil, ErrOAuthGrantSessionInvalid
	}
	return &session, nil
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
