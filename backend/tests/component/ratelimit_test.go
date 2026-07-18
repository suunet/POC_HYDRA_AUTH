package tests_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend/common/ratelimit"
)

// UC-002 / VAR-16: 固定ウィンドウレート制限を実Redisで検証する。
// 1回目は許可、同一ウィンドウ内の2回目は超過で拒否し、retry_after は TTL 残秒数（PTTL 由来）。
func TestFixedWindowLimiterAllowsThenBlocksWithinWindow(t *testing.T) {
	ctx := context.Background()
	prefix := "test:ratelimit:"
	window := 5 * time.Second
	key := uuid.NewString()
	limiter := ratelimit.NewFixedWindowLimiter(redisClient, prefix, window, ratelimit.PassthroughHasher{})
	t.Cleanup(func() { redisClient.Del(ctx, prefix+key) })

	first, err := limiter.Allow(ctx, key)
	require.NoError(t, err)
	assert.True(t, first.Allowed, "1回目は許可される")
	assert.Equal(t, 0, first.Remaining, "1回/ウィンドウを消費したので残0")
	assert.WithinDuration(t, time.Now().Add(window), first.ResetAt, time.Second)

	time.Sleep(10 * time.Millisecond)

	second, err := limiter.Allow(ctx, key)
	require.NoError(t, err)
	assert.False(t, second.Allowed, "同一ウィンドウ内の2回目は拒否される")
	assert.Positive(t, second.RetryAfter, "retry_after は正の残秒数")
	assert.Less(t, second.RetryAfter, window, "retry_after はウィンドウ長より厳密に小さい（消費した時間ぶん減る）")
}

// UC-002 / VAR-16: 超過時に TTL を延長しない（連打で永続的に429になるのを防ぐ）。
// TTL を手動で短縮した後に超過リクエストを送り、retry_after が短縮後の残TTLに収まる
// （＝ウィンドウ長へリセットされない）ことで非延長を決定的に検証する。
func TestFixedWindowLimiterDoesNotExtendTTLOnExcess(t *testing.T) {
	ctx := context.Background()
	prefix := "test:ratelimit:"
	window := 5 * time.Second
	key := uuid.NewString()
	redisKey := prefix + key
	limiter := ratelimit.NewFixedWindowLimiter(redisClient, prefix, window, ratelimit.PassthroughHasher{})
	t.Cleanup(func() { redisClient.Del(ctx, redisKey) })

	first, err := limiter.Allow(ctx, key)
	require.NoError(t, err)
	require.True(t, first.Allowed)

	require.NoError(t, redisClient.PExpire(ctx, redisKey, 500*time.Millisecond).Err())

	second, err := limiter.Allow(ctx, key)
	require.NoError(t, err)
	assert.False(t, second.Allowed)
	assert.Positive(t, second.RetryAfter)
	assert.LessOrEqual(t, second.RetryAfter, 500*time.Millisecond,
		"超過時にTTLをウィンドウ長へ延長していない（VAR-16）")
}
