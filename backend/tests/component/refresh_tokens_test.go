package tests_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
)

// UC-005 / INF-04: refresh_tokens はNFR-14準拠（token_hash一意・users FK）で永続化できる
func TestUC005_RefreshTokens_Schema_HashUnique_And_UserFK(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	seedExistingUser(t, ctx, email)
	row, err := dbmodels.New(pool).GetUserByEmail(ctx, email)
	require.NoError(t, err)

	params := dbmodels.InsertRefreshTokenParams{
		TokenID:   uuid.New(),
		UserUuid:  row.UserUuid,
		FamilyID:  uuid.New(),
		TokenHash: uuid.NewString(),
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}
	require.NoError(t, dbmodels.New(pool).InsertRefreshToken(ctx, params))

	// token_hash 一意（NFR-14: ハッシュのみ保存・重複不可）
	dup := params
	dup.TokenID = uuid.New()
	err = dbmodels.New(pool).InsertRefreshToken(ctx, dup)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refresh_tokens_hash_unique")

	// users FK（存在しないユーザーへは発行不可）
	orphan := params
	orphan.TokenID = uuid.New()
	orphan.UserUuid = uuid.New()
	orphan.TokenHash = uuid.NewString()
	err = dbmodels.New(pool).InsertRefreshToken(ctx, orphan)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refresh_tokens_user_uuid_fkey")
}
