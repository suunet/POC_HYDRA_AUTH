// Package ratelimit は横断のレート制限基盤。レート値・キー・fail-mode は呼び出し側が
// 注入する（VAR-12〜14・VAR-16「同値でも独立した設定値として管理」）。
package ratelimit

import (
	"context"
	"time"
)

type Result struct {
	Allowed    bool
	RetryAfter time.Duration // TTL 残（秒への変換は呼び出し側・VAR-16）
	Remaining  int
	ResetAt    time.Time
}

type Limiter interface {
	Allow(ctx context.Context, key string) (Result, error)
}

// NOTE: 現時点の結線は素通しのみ。IP は個人データのため、メール確認（UC-003）で
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
