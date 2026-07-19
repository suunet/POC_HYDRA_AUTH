package ratelimit

import (
	"github.com/redis/go-redis/v9"

	"poc-app-hydra/backend/auth/domain"
	commonratelimit "poc-app-hydra/backend/common/ratelimit"
)

// NOTE: メール確認（UC-003）はfail-open・IPキーはHMACで匿名化（VAR-17=1分に10回・INF-14）
func NewEmailVerifyLimiter(client *redis.Client, hmacSecret []byte) *commonratelimit.FailModeLimiter {
	return commonratelimit.NewFailModeLimiter(
		commonratelimit.NewCountingWindowLimiter(
			client,
			"emailverify:ratelimit:",
			domain.EmailVerifyRateLimitWindow,
			domain.EmailVerifyRateLimitMax,
			commonratelimit.NewHMACSHA256Hasher(hmacSecret),
		),
		commonratelimit.FailOpen,
	)
}
