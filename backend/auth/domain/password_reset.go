package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
)

// VAR-05: パスワードリセットトークン有効期限
const PasswordResetTokenTTL = 30 * time.Minute

// VAR-12: 同一メールアドレスのリセット要求は5分に1回
const PasswordResetRateLimitWindow = 5 * time.Minute

var (
	ErrResetTokenNotFound = errors.New("password reset token not found")

	ErrResetTokenConsumeConflict = errors.New("password reset token consume conflict")
)

// NOTE: 平文は保存せずハッシュ（SHA-256）のみ持つ（NFR-15・INF-05）
type PasswordResetToken struct {
	TokenUUID uuid.UUID
	Hash      string
	ExpiresAt time.Time
}

func NewPasswordResetToken() (plain string, token PasswordResetToken, err error) {
	b := make([]byte, tokenPlainBytes)
	if _, err := rand.Read(b); err != nil {
		return "", PasswordResetToken{}, err
	}
	plain = base64.RawURLEncoding.EncodeToString(b)

	sum := sha256.Sum256([]byte(plain))
	token = PasswordResetToken{
		TokenUUID: uuid.New(),
		Hash:      hex.EncodeToString(sum[:]),
		ExpiresAt: time.Now().UTC().Add(PasswordResetTokenTTL),
	}
	return plain, token, nil
}

// PasswordResetTokenRecord は保存済みトークンの照合用ビュー（INF-05）。
type PasswordResetTokenRecord struct {
	TokenUUID uuid.UUID
	UserUUID  uuid.UUID
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
}

func (r PasswordResetTokenRecord) Used() bool { return r.UsedAt != nil }

func (r PasswordResetTokenRecord) Expired(now time.Time) bool { return now.After(r.ExpiresAt) }

// HashPasswordResetToken は提示された平文トークンをDB照合用のSHA-256 hexへ変換する。
func HashPasswordResetToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}
