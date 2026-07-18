package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// fixedWindowScript は SET NX と PTTL を1往復で原子実行する。
//
// キーが未在なら TTL 付きで作成して許可（1）、既在なら拒否（0）。いずれも PTTL を返す。
// NOTE: 拒否時に TTL を更新しない（SET NX が既在キーに触れない）。連打で TTL が延長され
// 永続的に429になるのを防ぐ VAR-16 の要件を満たす。
var fixedWindowScript = redis.NewScript(`
local created = redis.call('SET', KEYS[1], '1', 'NX', 'PX', ARGV[1])
local pttl = redis.call('PTTL', KEYS[1])
if created then
  return {1, pttl}
end
return {0, pttl}
`)

// FixedWindowLimiter は固定ウィンドウ方式のレート制限（VAR-16: 同一キーは1ウィンドウに1回）。
// retry_after を TTL 残秒数で返し、超過時に記録を更新しない。
type FixedWindowLimiter struct {
	client *redis.Client
	prefix string
	window time.Duration
	hasher KeyHasher
}

var _ Limiter = (*FixedWindowLimiter)(nil)

// NewFixedWindowLimiter を生成する。prefix はキー名前空間、window はウィンドウ長、
// hasher はキーの保存前変換（登録は PassthroughHasher で平文・Q-7=C）。
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

	// 1ウィンドウ1回のため、許可・拒否いずれも残り許可回数は0。
	if created == 1 {
		// 許可時は retry_after 不要（0）。
		return NewResult(true, 0, 0, resetAt), nil
	}
	// 拒否時の retry_after は残TTL（VAR-16）。
	return NewResult(false, remainingTTL, 0, resetAt), nil
}
