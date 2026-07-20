package ratelimit

import (
	"github.com/redis/go-redis/v9"

	"poc-app-hydra/backend/auth/domain"
	commonratelimit "poc-app-hydra/backend/common/ratelimit"
)

// NOTE: 再送（UC-004・VAR-13）はメール確認（IPキー）と違いメールキー。fail-openは総当たり抑止より正規ユーザーの再送継続を優先するため。
func NewResendEmailLimiter(client *redis.Client) *commonratelimit.FailModeLimiter {
	return commonratelimit.NewFailModeLimiter(
		commonratelimit.NewFixedWindowLimiter(
			client,
			"resend:ratelimit:",
			domain.ResendEmailRateLimitWindow,
			commonratelimit.PassthroughHasher{},
		),
		commonratelimit.FailOpen,
	)
}
