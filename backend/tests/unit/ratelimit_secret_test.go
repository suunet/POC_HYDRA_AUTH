package unit

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"poc-app-hydra/backend/auth/adapters/ratelimit"
)

// INF-14: 公知の開発用既定鍵は未設定・明示設定のいずれでも weak として検知する（T-006 c6-BJ#5）
func TestResolveHMACSecret_WeakKeyDetection(t *testing.T) {
	t.Run("未設定は既定鍵にフォールバックしweak", func(t *testing.T) {
		secret, weak := ratelimit.ResolveHMACSecret("")
		assert.Equal(t, ratelimit.DevDefaultHMACSecret, secret)
		assert.True(t, weak)
	})

	t.Run("既定鍵の明示設定もweak", func(t *testing.T) {
		secret, weak := ratelimit.ResolveHMACSecret(ratelimit.DevDefaultHMACSecret)
		assert.Equal(t, ratelimit.DevDefaultHMACSecret, secret)
		assert.True(t, weak, "本番で気付かず既定鍵を使う事故を検知する")
	})

	t.Run("独自鍵はそのまま採用しweakでない", func(t *testing.T) {
		secret, weak := ratelimit.ResolveHMACSecret("a-strong-production-secret")
		assert.Equal(t, "a-strong-production-secret", secret)
		assert.False(t, weak)
	})
}
