package ratelimit

import (
	"github.com/redis/go-redis/v9"

	"poc-app-hydra/backend/auth/domain"
	commonratelimit "poc-app-hydra/backend/common/ratelimit"
)

// NOTE: ロックアウト（UC-005・NFR-03）は専用IF（Check/RecordFailure/Reset）のため FailModeLimiter 非適用。
// Redis障害時はエラー伝播（実質fail-closed）＝ブルートフォース防御を優先し認証の安全側に倒す。
// メールキーは平文（T-007方針: DBに平文保存済みで秘匿実益が薄い）。
func NewLoginLockout(client *redis.Client) *commonratelimit.LockoutLimiter {
	return commonratelimit.NewLockoutLimiter(
		client,
		"login:lockout:",
		domain.LoginFailureCountWindow,
		domain.LoginLockoutTTL,
		domain.LoginLockoutThreshold,
		commonratelimit.PassthroughHasher{},
	)
}
