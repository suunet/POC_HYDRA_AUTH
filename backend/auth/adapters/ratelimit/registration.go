package ratelimit

import (
	"github.com/redis/go-redis/v9"

	"poc-app-hydra/backend/auth/app/command"
	"poc-app-hydra/backend/auth/domain"
	commonratelimit "poc-app-hydra/backend/common/ratelimit"
)

// NOTE: 登録（UC-002）は fail-open。abuse対策の一時停止より正規ユーザーの登録継続を優先する。
func NewRegistrationLimiter(client *redis.Client) command.RateLimiter {
	return commonratelimit.NewFailModeLimiter(
		commonratelimit.NewFixedWindowLimiter(
			client,
			"registration:ratelimit:",
			domain.RegistrationRateLimitWindow,
			commonratelimit.PassthroughHasher{},
		),
		commonratelimit.FailOpen,
	)
}
