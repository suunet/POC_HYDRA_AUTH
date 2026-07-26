package ratelimit

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"

	"poc-app-hydra/backend/auth/domain"
	commonratelimit "poc-app-hydra/backend/common/ratelimit"
)

// loginLockout は common の LockoutLimiter を command.Lockout IF（Check が (bool, Duration, error)）へ適合させる。
type loginLockout struct {
	inner *commonratelimit.LockoutLimiter
}

// NewLoginLockout はログイン用ロックアウト（UC-005・NFR-03）を組み立てる。
// NOTE: ロックアウトはセキュリティ制御のため fail-closed（Redis障害時はエラー伝播）。
// メールキーは平文（T-007方針: DBに平文保存済みで秘匿実益が薄い）。
func NewLoginLockout(client *redis.Client) *loginLockout {
	return &loginLockout{
		inner: commonratelimit.NewLockoutLimiter(
			client,
			"login:lockout:",
			domain.LoginFailureCountWindow,
			domain.LoginLockoutTTL,
			domain.LoginLockoutThreshold,
			commonratelimit.PassthroughHasher{},
		),
	}
}

func (l *loginLockout) Check(ctx context.Context, key string) (bool, time.Duration, error) {
	st, err := l.inner.Check(ctx, key)
	if err != nil {
		return false, 0, err
	}
	return st.Locked, st.RetryAfter, nil
}

func (l *loginLockout) RecordFailure(ctx context.Context, key string) (bool, error) {
	return l.inner.RecordFailure(ctx, key)
}

func (l *loginLockout) Reset(ctx context.Context, key string) error {
	return l.inner.Reset(ctx, key)
}
