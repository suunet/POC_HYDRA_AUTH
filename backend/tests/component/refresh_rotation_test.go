package tests_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
	"poc-app-hydra/backend/auth/domain"
)

// seedRefreshToken は指定family/親でリフレッシュトークンを1件挿入し、平文とtoken_idを返す。
func seedRefreshToken(t *testing.T, ctx context.Context, userUUID, familyID uuid.UUID, parent *uuid.UUID) (plain string, tokenID uuid.UUID) {
	t.Helper()
	plain, hash, err := domain.GenerateRefreshToken()
	require.NoError(t, err)
	tokenID = uuid.New()
	require.NoError(t, dbmodels.New(pool).InsertRefreshToken(ctx, dbmodels.InsertRefreshTokenParams{
		TokenID:       tokenID,
		UserUuid:      userUUID,
		FamilyID:      familyID,
		ParentTokenID: parent,
		TokenHash:     hash,
		ExpiresAt:     time.Now().Add(domain.RefreshTokenTTL),
	}))
	return plain, tokenID
}

// UC-006: MarkRefreshTokenUsed は used_at を記録し、GetRefreshTokenByHashForUpdate で読める
func TestUC006_RefreshQueries_MarkUsed_AndForUpdateRead(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	seedLoginUser(t, ctx, email, domain.StatusInactive)
	row, err := dbmodels.New(pool).GetUserByEmail(ctx, email)
	require.NoError(t, err)
	familyID := uuid.New()
	plain, tokenID := seedRefreshToken(t, ctx, row.UserUuid, familyID, nil)

	// クエリが有効で1行返ること（FOR UPDATEの直列化検証はサイクル5の同時リフレッシュで実施）
	got, err := dbmodels.New(pool).GetRefreshTokenByHashForUpdate(ctx, domain.HashRefreshToken(plain))
	require.NoError(t, err)
	assert.Equal(t, tokenID, got.TokenID)
	assert.Nil(t, got.UsedAt, "初期は未使用")

	require.NoError(t, dbmodels.New(pool).MarkRefreshTokenUsed(ctx, tokenID))
	after, err := dbmodels.New(pool).GetRefreshTokenByHashForUpdate(ctx, domain.HashRefreshToken(plain))
	require.NoError(t, err)
	assert.NotNil(t, after.UsedAt, "used_atが記録される")
}

// UC-006 E4: RevokeRefreshTokenFamily は同一familyの全レコードをrevokeし、他familyは残す
func TestUC006_RefreshQueries_RevokeFamily_OnlyThatChain(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	seedLoginUser(t, ctx, email, domain.StatusInactive)
	row, err := dbmodels.New(pool).GetUserByEmail(ctx, email)
	require.NoError(t, err)

	famA := uuid.New()
	famB := uuid.New()
	a1, _ := seedRefreshToken(t, ctx, row.UserUuid, famA, nil)
	a2, _ := seedRefreshToken(t, ctx, row.UserUuid, famA, nil)
	b1, _ := seedRefreshToken(t, ctx, row.UserUuid, famB, nil)

	require.NoError(t, dbmodels.New(pool).RevokeRefreshTokenFamily(ctx, dbmodels.RevokeRefreshTokenFamilyParams{
		FamilyID:         famA,
		RevocationReason: ptr("token_reuse_detected"),
	}))

	for _, p := range []string{a1, a2} {
		rec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(p))
		require.NoError(t, err)
		assert.NotNil(t, rec.RevokedAt, "famAは失効")
		require.NotNil(t, rec.RevocationReason)
		assert.Equal(t, "token_reuse_detected", *rec.RevocationReason)
	}
	recB, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(b1))
	require.NoError(t, err)
	assert.Nil(t, recB.RevokedAt, "famB（他family）は生存")
}

// UC-006 E3/E5/E6: RevokeRefreshToken は当該トークンのみ理由付きで失効
func TestUC006_RefreshQueries_RevokeSingle(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	seedLoginUser(t, ctx, email, domain.StatusInactive)
	row, err := dbmodels.New(pool).GetUserByEmail(ctx, email)
	require.NoError(t, err)
	plain, tokenID := seedRefreshToken(t, ctx, row.UserUuid, uuid.New(), nil)

	require.NoError(t, dbmodels.New(pool).RevokeRefreshToken(ctx, dbmodels.RevokeRefreshTokenParams{
		TokenID:          tokenID,
		RevocationReason: ptr("account_disabled"),
	}))
	rec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(plain))
	require.NoError(t, err)
	require.NotNil(t, rec.RevokedAt)
	require.NotNil(t, rec.RevocationReason)
	assert.Equal(t, "account_disabled", *rec.RevocationReason)
}

// UC-006: GetUserByUuid は token→user 取得（削除済み除外）に使える
func TestUC006_RefreshQueries_GetUserByUuid_ExcludesDeleted(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	seedLoginUser(t, ctx, email, domain.StatusInactive)
	row, err := dbmodels.New(pool).GetUserByEmail(ctx, email)
	require.NoError(t, err)

	got, err := dbmodels.New(pool).GetUserByUuid(ctx, row.UserUuid)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusInactive, got.Status)

	// 論理削除すると取得できない（E5）
	_, err = pool.Exec(ctx, "UPDATE auth.users SET deleted_at = now() WHERE user_uuid = $1", row.UserUuid)
	require.NoError(t, err)
	_, err = dbmodels.New(pool).GetUserByUuid(ctx, row.UserUuid)
	require.Error(t, err, "削除済みは取得できない")
}

func ptr(s string) *string { return &s }
