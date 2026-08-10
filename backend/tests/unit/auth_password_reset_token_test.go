package unit

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend/auth/domain"
)

// UC-008: リセットトークンは opaque（crypto/rand）を平文とし、保持するのはSHA-256ハッシュのみ（INF-05・NFR-15）。
// 有効期限は30分（VAR-05）。
func TestUC008_NewPasswordResetToken_OpaqueSHA256TTL30m(t *testing.T) {
	assert.Equal(t, 30*time.Minute, domain.PasswordResetTokenTTL, "VAR-05: 30分")

	before := time.Now().UTC()
	plain, token, err := domain.NewPasswordResetToken()
	require.NoError(t, err)

	require.NotEmpty(t, plain)
	sum := sha256.Sum256([]byte(plain))
	assert.Equal(t, hex.EncodeToString(sum[:]), token.Hash, "ハッシュは平文のSHA-256 hex（平文は保持しない）")
	assert.NotEqual(t, uuid.Nil, token.TokenUUID)
	assert.InDelta(t, domain.PasswordResetTokenTTL.Seconds(), token.ExpiresAt.Sub(before).Seconds(), 5)

	plain2, token2, err := domain.NewPasswordResetToken()
	require.NoError(t, err)
	assert.NotEqual(t, plain, plain2, "毎回異なるランダム値")
	assert.NotEqual(t, token.Hash, token2.Hash)
}
