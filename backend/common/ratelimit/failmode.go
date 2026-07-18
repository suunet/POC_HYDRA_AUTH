package ratelimit

import (
	"context"
	"time"

	applog "poc-app-hydra/backend/common/log"
)

// FailMode はレート判定不能（Redis 障害等）時の挙動。エンドポイントの重要度に応じて
// 呼び出し側が選ぶ。
type FailMode int

const (
	FailClosed FailMode = iota // 障害時は拒否（安全優先）。ゼロ値
	FailOpen                   // 障害時は通過（可用性優先）
)

// FailModeLimiter は内側 Limiter の失敗を FailMode で吸収し、外部依存失敗として
// ERROR でログ化する（NFR-08）。
type FailModeLimiter struct {
	inner Limiter
	mode  FailMode
}

var _ Limiter = (*FailModeLimiter)(nil)

func NewFailModeLimiter(inner Limiter, mode FailMode) *FailModeLimiter {
	return &FailModeLimiter{inner: inner, mode: mode}
}

func (l *FailModeLimiter) Allow(ctx context.Context, key string) (Result, error) {
	res, err := l.inner.Allow(ctx, key)
	if err == nil {
		return res, nil
	}

	// NOTE: 任意の Limiter をラップするため、内側 err にキー等の個人情報（メール・IP）を
	// 含めない前提に依存する（NFR-09）。
	logger := applog.FromContext(ctx)
	if l.mode == FailOpen {
		logger.ErrorContext(ctx, "レート制限の判定に失敗したため通過させます（fail-open）", "error", err)
		return NewResult(true, 0, 0, time.Now()), nil
	}
	logger.ErrorContext(ctx, "レート制限の判定に失敗したため拒否します（fail-closed）", "error", err)
	return NewResult(false, 0, 0, time.Now()), nil
}
