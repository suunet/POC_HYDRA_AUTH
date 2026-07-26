package domain

import (
	"errors"
	"time"
)

// NFR-03: ログイン失敗10回で15分ロックアウト。カウント窓は固定15分（失敗加算で延長しない・T-013 Q-8決定）
const (
	LoginFailureCountWindow = 15 * time.Minute
	LoginLockoutTTL         = 15 * time.Minute
	LoginLockoutThreshold   = 10
)

// UC-005 ログイン時のアカウント状態バリデーション結果（CND-03/04）。
var (
	ErrEmailNotVerified = errors.New("email not verified")  // E5: メール未確認（CND-03不成立）
	ErrAccountDisabled  = errors.New("account is disabled") // E6: 無効化済み（CND-04不成立）
)

// CheckLoginableStatus は STM-01 のアカウント状態がログイン可能か判定する（UC-005 基本フロー6/7）。
// inactive（メール確認済み・ログイン可能）のみ許可。deleted は検索段階で除外済みのため到達しない。
func CheckLoginableStatus(status string) error {
	switch status {
	case StatusInactive:
		return nil
	case StatusDisabled:
		return ErrAccountDisabled
	default:
		// mail_unverified・invited（未確認相当）は E5
		return ErrEmailNotVerified
	}
}
