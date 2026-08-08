package ratelimit

import (
	"github.com/redis/go-redis/v9"

	"poc-app-hydra/backend/auth/domain"
	commonratelimit "poc-app-hydra/backend/common/ratelimit"
)

// NOTE: リセット要求（UC-008・VAR-12）はメールキー・チェック時に窓確定・fail-open（VAR-13と同根拠＝正規ユーザーの要求継続を優先）
func NewPasswordResetLimiter(client *redis.Client) *commonratelimit.FailModeLimiter {
	return commonratelimit.NewFailModeLimiter(
		commonratelimit.NewFixedWindowLimiter(
			client,
			"password_reset:",
			domain.PasswordResetRateLimitWindow,
			commonratelimit.PassthroughHasher{},
		),
		commonratelimit.FailOpen,
	)
}
