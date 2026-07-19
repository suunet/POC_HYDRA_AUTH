package tests_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend/common/ratelimit"
)

// UC-002 / VAR-16: 固定ウィンドウレート制限を実Redisで検証する。
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

// UC-002 / VAR-16: 並行リクエストが原子判定（Lua 1往復）で上限をすり抜けない
func TestFixedWindowLimiterConcurrentAllowsExactlyOnce(t *testing.T) {
	ctx := context.Background()
	prefix := "test:ratelimit:"
	key := uuid.NewString()
	limiter := ratelimit.NewFixedWindowLimiter(redisClient, prefix, 5*time.Second, ratelimit.PassthroughHasher{})
	t.Cleanup(func() { redisClient.Del(ctx, prefix+key) })

	const n = 20
	var wg sync.WaitGroup
	var allowed atomic.Int64
	errs := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := limiter.Allow(ctx, key)
			if err != nil {
				errs <- err
				return
			}
			if res.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assert.EqualValues(t, 1, allowed.Load(), "同時20リクエストで許可はちょうど1回")
}

// UC-003 / VAR-17: カウント型は上限まで許可し、超過でTTL残のretry_afterを返す（実Redis）
func TestCountingWindowLimiterAllowsUpToLimit(t *testing.T) {
	ctx := context.Background()
	prefix := "test:ratelimit:counting:"
	key := uuid.NewString()
	limiter := ratelimit.NewCountingWindowLimiter(redisClient, prefix, 5*time.Second, 2, ratelimit.PassthroughHasher{})
	t.Cleanup(func() { redisClient.Del(ctx, prefix+key) })

	first, err := limiter.Allow(ctx, key)
	require.NoError(t, err)
	assert.True(t, first.Allowed)
	assert.Equal(t, 1, first.Remaining)

	second, err := limiter.Allow(ctx, key)
	require.NoError(t, err)
	assert.True(t, second.Allowed)
	assert.Equal(t, 0, second.Remaining)

	third, err := limiter.Allow(ctx, key)
	require.NoError(t, err)
	assert.False(t, third.Allowed, "上限2を超えた3回目は拒否")
	assert.Positive(t, third.RetryAfter)
	assert.LessOrEqual(t, third.RetryAfter, 5*time.Second)

	// VAR-17: 超過時は記録を更新しない（カウント値が増えない）
	stored, err := redisClient.Get(ctx, prefix+key).Result()
	require.NoError(t, err)
	assert.Equal(t, "2", stored, "超過リクエストでカウントを書き換えない")

	// VAR-17: 超過時にTTLを延長しない
	require.NoError(t, redisClient.PExpire(ctx, prefix+key, 500*time.Millisecond).Err())
	fourth, err := limiter.Allow(ctx, key)
	require.NoError(t, err)
	assert.False(t, fourth.Allowed)
	assert.LessOrEqual(t, fourth.RetryAfter, 500*time.Millisecond)
}

// VAR-17 / HMAC: 同一キーは同一ハッシュ・保存キーに平文IPが現れない（Q-7=C）
func TestCountingWindowLimiterHMACKeyAnonymized(t *testing.T) {
	ctx := context.Background()
	prefix := "test:ratelimit:hmac:"
	hasher := ratelimit.NewHMACSHA256Hasher([]byte("s3cret"))
	limiter := ratelimit.NewCountingWindowLimiter(redisClient, prefix, 5*time.Second, 1, hasher)
	t.Cleanup(func() { redisClient.Del(ctx, prefix+hasher.Hash("203.0.113.9")) })

	_, err := limiter.Allow(ctx, "203.0.113.9")
	require.NoError(t, err)

	keys, err := redisClient.Keys(ctx, prefix+"*").Result()
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.NotContains(t, keys[0], "203.0.113.9", "平文IPを保存しない")
}
