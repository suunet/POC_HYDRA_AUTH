package ratelimit

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

type RegistrationLimiter struct {
	client *redis.Client
	window time.Duration
}

func NewRegistrationLimiter(client *redis.Client, window time.Duration) *RegistrationLimiter {
	return &RegistrationLimiter{client: client, window: window}
}

// NOTE: SET NX EX による固定ウィンドウ判定。超過時はTTLを更新しない
func (l *RegistrationLimiter) Allow(ctx context.Context, key string) (bool, error) {
	ok, err := l.client.SetNX(ctx, "registration:ratelimit:"+key, "1", l.window).Result()
	if err != nil {
		return false, err
	}
	return ok, nil
}
