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

// UC-011: 招待トークンは opaque（crypto/rand）を平文とし、保持するのはSHA-256ハッシュのみ（INF-07・NFR-15）。
// 有効期限は24時間（VAR-07）。
func TestUC011_NewInvitationToken_OpaqueSHA256TTL24h(t *testing.T) {
	assert.Equal(t, 24*time.Hour, domain.InvitationTokenTTL, "VAR-07: 24時間")

	before := time.Now().UTC()
	plain, token, err := domain.NewInvitationToken()
	require.NoError(t, err)

	require.NotEmpty(t, plain)
	sum := sha256.Sum256([]byte(plain))
	assert.Equal(t, hex.EncodeToString(sum[:]), token.Hash, "ハッシュは平文のSHA-256 hex（平文は保持しない）")
	assert.NotEqual(t, uuid.Nil, token.TokenUUID)
	assert.InDelta(t, domain.InvitationTokenTTL.Seconds(), token.ExpiresAt.Sub(before).Seconds(), 5)

	plain2, token2, err := domain.NewInvitationToken()
	require.NoError(t, err)
	assert.NotEqual(t, plain, plain2, "毎回異なるランダム値")
	assert.NotEqual(t, token.Hash, token2.Hash)
}
