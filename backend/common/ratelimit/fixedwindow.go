package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// キー未在なら TTL 付きで作成して許可（1）、既在なら拒否（0）。いずれも PTTL を併せ返す。
// NOTE: 拒否時に TTL を更新しない。連打で 429 が永続するのを防ぐ（VAR-16）。
var fixedWindowScript = redis.NewScript(`
local created = redis.call('SET', KEYS[1], '1', 'NX', 'PX', ARGV[1])
local pttl = redis.call('PTTL', KEYS[1])
if created then
  return {1, pttl}
end
return {0, pttl}
`)

// FixedWindowLimiter は固定ウィンドウ（同一キーは 1 ウィンドウに 1 回・VAR-16）の Limiter。
type FixedWindowLimiter struct {
	client *redis.Client
	prefix string
	window time.Duration
	hasher KeyHasher
}

var _ Limiter = (*FixedWindowLimiter)(nil)

func NewFixedWindowLimiter(client *redis.Client, prefix string, window time.Duration, hasher KeyHasher) *FixedWindowLimiter {
	return &FixedWindowLimiter{client: client, prefix: prefix, window: window, hasher: hasher}
}

func (l *FixedWindowLimiter) Allow(ctx context.Context, key string) (Result, error) {
	redisKey := l.prefix + l.hasher.Hash(key)

	raw, err := fixedWindowScript.Run(ctx, l.client, []string{redisKey}, l.window.Milliseconds()).Result()
	if err != nil {
		return Result{}, fmt.Errorf("could not evaluate rate limit script: %w", err)
	}

	vals, ok := raw.([]interface{})
	if !ok || len(vals) != 2 {
		return Result{}, fmt.Errorf("unexpected rate limit script result: %v", raw)
	}
	created, _ := vals[0].(int64)
	pttlMs, _ := vals[1].(int64)
	if pttlMs < 0 {
		pttlMs = 0
	}

	remainingTTL := time.Duration(pttlMs) * time.Millisecond
	resetAt := time.Now().Add(remainingTTL)

	// NOTE: 1ウィンドウ1回のため、許可・拒否いずれも Remaining は 0。
	if created == 1 {
		return NewResult(true, 0, 0, resetAt), nil
	}
	return NewResult(false, remainingTTL, 0, resetAt), nil
}
