// Package ratelimit は横断のレート制限基盤（T-007）を提供する。
//
// 登録（UC-002）・メール確認（UC-003・T-006）・将来のログイン等が共通で乗る。
// アルゴリズムは固定ウィンドウ（VAR-16: 超過時は記録を更新しない・retry_after は
// TTL 残秒数）。判定結果は Result で返し、retry_after を実残秒で表現する。
package ratelimit

import (
	"context"
	"time"
)

// Result はレート制限の判定結果。retry_after を bool ではなく実残秒で返すことで
// VAR-16「retry_after は TTL 残秒数」を満たす（#1 根治）。
type Result struct {
	Allowed    bool
	RetryAfter time.Duration // 超過時の再試行までの残り時間（TTL 残・VAR-16）
	Remaining  int
	ResetAt    time.Time
}

// Limiter はキー単位のレート制限判定を行う。呼び出し側がキー・レート値・fail-mode を
// 独立注入する（VAR-12〜16 の独立管理制約を維持）。
type Limiter interface {
	Allow(ctx context.Context, key string) (Result, error)
}

// KeyHasher はレートキーの保存前変換を差し込むための拡張点。
//
// NOTE: HMAC ハッシュ化の最初の実利用者は T-006 の IP キー（純個人データ）。T-007 では
// 登録の平文メールキーを PassthroughHasher で素通しするのみ結線する（Q-7=C・意図的な先行実装）。
type KeyHasher interface {
	Hash(key string) string
}

// PassthroughHasher はキーを変換せずそのまま返す。登録の平文メールキー用（Q-7=C）。
// 同一メールは既に Postgres に平文保存されており、ハッシュ化の秘匿実益が薄いため。
type PassthroughHasher struct{}

// 実装がインターフェースを満たすことをコンパイル時に保証する。
var _ KeyHasher = PassthroughHasher{}

func (PassthroughHasher) Hash(key string) string { return key }

// NewResult は Result を組み立てる。Remaining は負にならないよう 0 で下限を切る。
func NewResult(allowed bool, retryAfter time.Duration, remaining int, resetAt time.Time) Result {
	if remaining < 0 {
		remaining = 0
	}
	return Result{
		Allowed:    allowed,
		RetryAfter: retryAfter,
		Remaining:  remaining,
		ResetAt:    resetAt,
	}
}
