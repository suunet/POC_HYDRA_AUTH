package tests_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend/common/ratelimit"
)

func newTestLockout(window, lockTTL time.Duration, threshold int64) *ratelimit.LockoutLimiter {
	return ratelimit.NewLockoutLimiter(redisClient, "test:lockout:", window, lockTTL, threshold, ratelimit.PassthroughHasher{})
}

func lockoutKey(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s-%s", t.Name(), uuid.NewString())
}

// UC-005 / NFR-03: 失敗10回目でロックが立ち、ロック中はRetryAfter付きで検出される
func TestUC005_Lockout_ThresholdLocksWithRetryAfter(t *testing.T) {
	ctx := context.Background()
	l := newTestLockout(15*time.Minute, 15*time.Minute, 10)
	key := lockoutKey(t)

	for i := 0; i < 9; i++ {
		lockedNow, err := l.RecordFailure(ctx, key)
		require.NoError(t, err)
		assert.False(t, lockedNow, "9回目まではロックしない")
	}
	st, err := l.Check(ctx, key)
	require.NoError(t, err)
	assert.False(t, st.Locked, "閾値未満はロックなし")

	lockedNow, err := l.RecordFailure(ctx, key)
	require.NoError(t, err)
	assert.True(t, lockedNow, "10回目でロックが立つ")

	st, err = l.Check(ctx, key)
	require.NoError(t, err)
	require.True(t, st.Locked)
	assert.Positive(t, st.RetryAfter, "解除までのTTL残が返る（VAR-11のretry_after秒の源）")
	assert.LessOrEqual(t, st.RetryAfter, 15*time.Minute)
}

// UC-005 / NFR-03（T-013 Q-8決定）: カウント窓は固定TTL。失敗加算でTTLは延長されない
func TestUC005_Lockout_FixedWindow_NoTTLExtensionOnFailure(t *testing.T) {
	ctx := context.Background()
	l := newTestLockout(1500*time.Millisecond, time.Minute, 10)
	key := lockoutKey(t)

	_, err := l.RecordFailure(ctx, key)
	require.NoError(t, err)
	ttl1, err := redisClient.PTTL(ctx, "test:lockout:count:"+key).Result()
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)
	_, err = l.RecordFailure(ctx, key)
	require.NoError(t, err)
	ttl2, err := redisClient.PTTL(ctx, "test:lockout:count:"+key).Result()
	require.NoError(t, err)

	assert.Less(t, ttl2, ttl1-100*time.Millisecond, "2回目の失敗でTTLがリフレッシュされない（固定窓・sleep 200msぶんの減少をマージン付きで検証）")
}

// UC-005 / NFR-03: 成功時リセットでカウントが消え、以後の失敗は新規カウントとなる
func TestUC005_Lockout_ResetClearsFailureCount(t *testing.T) {
	ctx := context.Background()
	l := newTestLockout(15*time.Minute, 15*time.Minute, 10)
	key := lockoutKey(t)

	for i := 0; i < 9; i++ {
		_, err := l.RecordFailure(ctx, key)
		require.NoError(t, err)
	}
	require.NoError(t, l.Reset(ctx, key))

	lockedNow, err := l.RecordFailure(ctx, key)
	require.NoError(t, err)
	assert.False(t, lockedNow, "リセット後の1回目は新規カウント（累積しない）")

	st, err := l.Check(ctx, key)
	require.NoError(t, err)
	assert.False(t, st.Locked)
}

// UC-005 / NFR-03: ロックキーのTTLが失われても（運用ミス等）Check がロック維持しつつlockTTLを再設定して自癒する
func TestUC005_Lockout_Check_HealsLockKeyWithoutTTL(t *testing.T) {
	ctx := context.Background()
	// window と lockTTL を異値にし、自癒で lockTTL 側が設定されたことを識別する（BJ c9#1）
	l := newTestLockout(1*time.Minute, 15*time.Minute, 1)
	key := lockoutKey(t)

	locked, err := l.RecordFailure(ctx, key) // threshold=1で即ロック
	require.NoError(t, err)
	require.True(t, locked)

	// ロックキーのTTLを剥がす（PERSIST＝pttl -1 を再現）
	require.NoError(t, redisClient.Persist(ctx, "test:lockout:lock:"+key).Err())
	pttl, err := redisClient.PTTL(ctx, "test:lockout:lock:"+key).Result()
	require.NoError(t, err)
	require.Equal(t, time.Duration(-1), pttl, "TTL無し状態")

	st, err := l.Check(ctx, key)
	require.NoError(t, err)
	assert.True(t, st.Locked, "TTL喪失でも未ロック扱いにしない（永続ロック回避＝自癒）")
	assert.Positive(t, st.RetryAfter, "lockTTLが再設定される")

	healed, err := redisClient.PTTL(ctx, "test:lockout:lock:"+key).Result()
	require.NoError(t, err)
	assert.Greater(t, healed, 1*time.Minute, "window(1m)でなくlockTTL(15m)側が再設定される")
	assert.LessOrEqual(t, healed, 15*time.Minute, "lockTTLを超えない")
}

// UC-005 / NFR-03: カウントキーのTTLが失われても RecordFailure が窓長を再設定して自癒する
func TestUC005_Lockout_RecordFailure_HealsCountKeyWithoutTTL(t *testing.T) {
	ctx := context.Background()
	// window と lockTTL を異値にし、自癒で window(カウント窓)側が設定されたことを識別する（BJ c9#1）
	l := newTestLockout(1*time.Minute, 15*time.Minute, 10)
	key := lockoutKey(t)

	_, err := l.RecordFailure(ctx, key)
	require.NoError(t, err)
	require.NoError(t, redisClient.Persist(ctx, "test:lockout:count:"+key).Err())
	pttl, err := redisClient.PTTL(ctx, "test:lockout:count:"+key).Result()
	require.NoError(t, err)
	require.Equal(t, time.Duration(-1), pttl, "カウントキーTTL無し状態")

	_, err = l.RecordFailure(ctx, key)
	require.NoError(t, err)

	healed, err := redisClient.PTTL(ctx, "test:lockout:count:"+key).Result()
	require.NoError(t, err)
	assert.LessOrEqual(t, healed, 1*time.Minute, "lockTTL(15m)でなくwindow(1m)側が再設定される")
	assert.Positive(t, healed, "永続カウント回避＝TTLが再設定される")
}
