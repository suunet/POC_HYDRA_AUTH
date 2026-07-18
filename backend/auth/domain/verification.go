package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
)

const StatusInactive = "inactive" // STM-01.未認証（UC-003完了後）

var (
	ErrTokenNotFound = errors.New("email confirmation token not found")
	// NOTE: Tx内で使い切り更新が0行（並行消費・論理削除済みユーザー等）だった競合
	ErrTokenConsumeConflict = errors.New("email confirmation token consume conflict")
)

// EmailConfirmationTokenRecord は保存済みトークンの照合用ビュー（INF-06）。
type EmailConfirmationTokenRecord struct {
	TokenUUID uuid.UUID
	UserUUID  uuid.UUID
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
}

func (r EmailConfirmationTokenRecord) Used() bool { return r.UsedAt != nil }

func (r EmailConfirmationTokenRecord) Expired(now time.Time) bool { return now.After(r.ExpiresAt) }

// HashEmailConfirmationToken は平文トークンを保存形式（SHA-256 hex・INF-06）へ変換する。
func HashEmailConfirmationToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}
