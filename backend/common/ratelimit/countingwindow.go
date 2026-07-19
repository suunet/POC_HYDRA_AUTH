package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// 上限未満ならカウントし（初回のみTTL設定）、上限到達後は一切書き込まず {count, pttl} を返す。
// NOTE: 超過時は記録（カウント・TTL）を更新しない＝VAR-17の字義どおり（連打で429が永続するのを防ぐ）。
// TTL無しキー（運用ミス等）はウィンドウ長を設定し直して自癒する（無警告。観測前に回復するため）。
var countingWindowScript = redis.NewScript(`
local limit = tonumber(ARGV[2])
local count = tonumber(redis.call('GET', KEYS[1]) or '0')
if count < limit then
  count = redis.call('INCR', KEYS[1])
  if count == 1 then
    redis.call('PEXPIRE', KEYS[1], ARGV[1])
  end
else
  count = count + 1
end
local pttl = redis.call('PTTL', KEYS[1])
if pttl < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
  pttl = tonumber(ARGV[1])
end
return {count, pttl}
`)

// CountingWindowLimiter は固定ウィンドウ内に limit 回まで許可する Limiter（VAR-17: N回/ウィンドウ）。
type CountingWindowLimiter struct {
	client *redis.Client
	prefix string
	window time.Duration
	limit  int64
	hasher KeyHasher
}

var _ Limiter = (*CountingWindowLimiter)(nil)

func NewCountingWindowLimiter(client *redis.Client, prefix string, window time.Duration, limit int64, hasher KeyHasher) *CountingWindowLimiter {
	return &CountingWindowLimiter{client: client, prefix: prefix, window: window, limit: limit, hasher: hasher}
}

func (l *CountingWindowLimiter) Allow(ctx context.Context, key string) (Result, error) {
	redisKey := l.prefix + l.hasher.Hash(key)

	raw, err := countingWindowScript.Run(ctx, l.client, []string{redisKey}, l.window.Milliseconds(), l.limit).Result()
	if err != nil {
		return Result{}, fmt.Errorf("could not evaluate rate limit script: %w", err)
	}
	vals, ok := raw.([]interface{})
	if !ok || len(vals) != 2 {
		return Result{}, fmt.Errorf("unexpected rate limit script result: %v", raw)
	}
	count, _ := vals[0].(int64)
	pttlMs, _ := vals[1].(int64)

	remainingTTL := time.Duration(pttlMs) * time.Millisecond
	resetAt := time.Now().Add(remainingTTL)
	remaining := int(l.limit - count)

	if count > l.limit {
		return NewResult(false, remainingTTL, 0, resetAt), nil
	}
	return NewResult(true, 0, remaining, resetAt), nil
}
