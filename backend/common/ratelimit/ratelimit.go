// Package ratelimit は横断のレート制限基盤。レート値・キー・fail-mode は呼び出し側が
// 注入する（VAR-12〜16「同値でも独立した設定値として管理」）。
package ratelimit

import (
	"context"
	"time"
)

type Result struct {
	Allowed    bool
	RetryAfter time.Duration // TTL 残秒数（VAR-16）
	Remaining  int
	ResetAt    time.Time
}

type Limiter interface {
	Allow(ctx context.Context, key string) (Result, error)
}

// KeyHasher はレートキーの保存前変換の差し込み口。
// NOTE: 現時点の結線は素通しのみ。IP は個人データのため、メール確認（UC-003）で
// IP キーを導入する時点で HMAC 実装を結線する（意図的な先行定義）。
type KeyHasher interface {
	Hash(key string) string
}

// NOTE: メールキーはハッシュ化しない。同一メールが DB に平文保存済みで秘匿の実益が薄いため。
type PassthroughHasher struct{}

var _ KeyHasher = PassthroughHasher{}

func (PassthroughHasher) Hash(key string) string { return key }

func NewResult(allowed bool, retryAfter time.Duration, remaining int, resetAt time.Time) Result {
	if remaining < 0 {
		remaining = 0
	}
	return Result{
		Allowed:    allowed,
		RetryAfter: retryAfter,
		Remaining:  remaining,
		ResetAt:    resetAt,
	}
}
