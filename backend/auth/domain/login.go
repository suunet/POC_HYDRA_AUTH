package domain

import "time"

// NFR-03: ログイン失敗10回で15分ロックアウト。カウント窓は固定15分（失敗加算で延長しない・T-013 Q-8決定）
const (
	LoginFailureCountWindow = 15 * time.Minute
	LoginLockoutTTL         = 15 * time.Minute
	LoginLockoutThreshold   = 10
)
