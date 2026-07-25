package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// 失敗カウントを加算し（初回のみTTL設定＝固定窓・T-013 Q-8決定）、閾値到達でロックキーを立てる。
// NOTE: カウントとロック設定を1スクリプトで行い、並行失敗時の閾値すり抜けを防ぐ（Lua原子性）。
// TTL無しキー（運用ミス等）はウィンドウ長を設定し直して自癒する（countingwindow.goと同流儀）。
var lockoutFailureScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
else
  local pttl = redis.call('PTTL', KEYS[1])
  if pttl < 0 then
    redis.call('PEXPIRE', KEYS[1], ARGV[1])
  end
end
if count == tonumber(ARGV[2]) then
  redis.call('SET', KEYS[2], '1', 'PX', ARGV[3])
  return 1
end
return 0
`)

type LockoutStatus struct {
	Locked     bool
	RetryAfter time.Duration // ロック解除までのTTL残（秒への変換は呼び出し側・VAR-11）
}

// LockoutLimiter はログイン失敗カウント（INF-08）とロックアウト状態（INF-09）の2キーを管理する
// （NFR-03: 失敗threshold回でlockTTLのロック。カウント窓はwindow固定・失敗加算で延長しない）。
type LockoutLimiter struct {
	client    *redis.Client
	prefix    string
	window    time.Duration
	lockTTL   time.Duration
	threshold int64
	hasher    KeyHasher
}

func NewLockoutLimiter(client *redis.Client, prefix string, window, lockTTL time.Duration, threshold int64, hasher KeyHasher) *LockoutLimiter {
	return &LockoutLimiter{client: client, prefix: prefix, window: window, lockTTL: lockTTL, threshold: threshold, hasher: hasher}
}

func (l *LockoutLimiter) countKey(key string) string { return l.prefix + "count:" + l.hasher.Hash(key) }
func (l *LockoutLimiter) lockKey(key string) string  { return l.prefix + "lock:" + l.hasher.Hash(key) }

// Check はロック状態のみを判定する（カウントには触れない。UC-005 基本フロー3）。
func (l *LockoutLimiter) Check(ctx context.Context, key string) (LockoutStatus, error) {
	pttl, err := l.client.PTTL(ctx, l.lockKey(key)).Result()
	if err != nil {
		return LockoutStatus{}, fmt.Errorf("could not check lockout state: %w", err)
	}
	if pttl <= 0 {
		// -2=キー無し・-1=TTL無し（想定外だがロック永続を避けるため未ロック扱いにしない）。
		// TTL無しロックは自癒として lockTTL を設定し直す
		if pttl == -1 {
			if err := l.client.PExpire(ctx, l.lockKey(key), l.lockTTL).Err(); err != nil {
				return LockoutStatus{}, fmt.Errorf("could not heal lockout ttl: %w", err)
			}
			return LockoutStatus{Locked: true, RetryAfter: l.lockTTL}, nil
		}
		return LockoutStatus{}, nil
	}
	return LockoutStatus{Locked: true, RetryAfter: pttl}, nil
}

// RecordFailure は失敗を1加算し、閾値到達でロックを立てたかを返す（UC-005 E3/E4）。
// NOTE: ロックは閾値ちょうどの1回のみ設定する（超過分の再SETでロックTTLが延命しない＝NFR-03「15分後に自動解除」保証）。
// 呼び出し側はCheck先行（ロック中はRecordFailureへ到達しない・UC-005フロー3）を前提とする。
func (l *LockoutLimiter) RecordFailure(ctx context.Context, key string) (bool, error) {
	raw, err := lockoutFailureScript.Run(ctx, l.client,
		[]string{l.countKey(key), l.lockKey(key)},
		l.window.Milliseconds(), l.threshold, l.lockTTL.Milliseconds(),
	).Result()
	if err != nil {
		return false, fmt.Errorf("could not record login failure: %w", err)
	}
	locked, ok := raw.(int64)
	if !ok {
		return false, fmt.Errorf("unexpected lockout script result: %v", raw)
	}
	return locked == 1, nil
}

// Reset は失敗カウントを消す（UC-005 基本フロー8: ログイン成功時）。ロックキーは触れない。
func (l *LockoutLimiter) Reset(ctx context.Context, key string) error {
	if err := l.client.Del(ctx, l.countKey(key)).Err(); err != nil {
		return fmt.Errorf("could not reset login failure count: %w", err)
	}
	return nil
}
