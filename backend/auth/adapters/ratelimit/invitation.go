package ratelimit

import (
	"github.com/redis/go-redis/v9"

	"poc-app-hydra/backend/auth/domain"
	commonratelimit "poc-app-hydra/backend/common/ratelimit"
)

// NOTE: 招待（UC-011・VAR-14）はメールキー・チェック時に窓確定・fail-open（VAR-12/13と同根拠＝正規運用の招待継続を優先）
func NewInvitationLimiter(client *redis.Client) *commonratelimit.FailModeLimiter {
	return commonratelimit.NewFailModeLimiter(
		commonratelimit.NewFixedWindowLimiter(
			client,
			"invitation:",
			domain.InvitationRateLimitWindow,
			commonratelimit.PassthroughHasher{},
		),
		commonratelimit.FailOpen,
	)
}
