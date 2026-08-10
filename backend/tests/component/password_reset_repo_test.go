package tests_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authdb "poc-app-hydra/backend/auth/adapters/db"
	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
	"poc-app-hydra/backend/auth/domain"
)

func seedResetUser(t *testing.T, ctx context.Context) uuid.UUID {
	t.Helper()
	email := uniqueEmail(t)
	seedLoginUser(t, ctx, email, domain.StatusInactive)
	row, err := dbmodels.New(pool).GetUserByEmail(ctx, email)
	require.NoError(t, err)
	return row.UserUuid
}

func issueResetToken(t *testing.T, ctx context.Context, userUUID uuid.UUID) (string, domain.PasswordResetToken) {
	t.Helper()
	plain, token, err := domain.NewPasswordResetToken()
	require.NoError(t, err)
	require.NoError(t, authdb.NewUserRepository(pool).IssuePasswordResetToken(ctx, userUUID, token,
		func(context.Context) error { return nil }))
	return plain, token
}

// UC-008: 発行Txは既存の有効リセットトークンを無効化してから新トークンを保存する（CND-18・有効は常に最大1本）。
// 他ユーザーの有効トークンは無傷。
func TestUC008_IssueResetTokenRepo_InvalidatesActiveAndInsertsNew(t *testing.T) {
	ctx := context.Background()
	repo := authdb.NewUserRepository(pool)
	userID := seedResetUser(t, ctx)
	_, first := issueResetToken(t, ctx, userID)
	otherID := seedResetUser(t, ctx)
	_, otherTok := issueResetToken(t, ctx, otherID)

	firstBefore, err := repo.GetPasswordResetTokenByHash(ctx, first.Hash)
	require.NoError(t, err)
	require.Nil(t, firstBefore.UsedAt, "前提: 1本目は有効")

	_, second := issueResetToken(t, ctx, userID)

	firstAfter, err := repo.GetPasswordResetTokenByHash(ctx, first.Hash)
	require.NoError(t, err)
	assert.NotNil(t, firstAfter.UsedAt, "CND-18: 再要求で旧トークンは used_at 打ち")
	secondRec, err := repo.GetPasswordResetTokenByHash(ctx, second.Hash)
	require.NoError(t, err)
	assert.Nil(t, secondRec.UsedAt, "新トークンは有効")
	assert.Equal(t, userID, secondRec.UserUUID)
	otherRec, err := repo.GetPasswordResetTokenByHash(ctx, otherTok.Hash)
	require.NoError(t, err)
	assert.Nil(t, otherRec.UsedAt, "他ユーザーの有効トークンは無傷")
}

// UC-008 E3: 送信コールバック失敗で無効化を含む全ロールバック（Q-6確定）。
// 旧有効トークンはそのまま残り、新トークンは保存されない（DB副作用ゼロ）。
func TestUC008_IssueResetTokenRepo_SendFailureRollsBackAll(t *testing.T) {
	ctx := context.Background()
	repo := authdb.NewUserRepository(pool)
	userID := seedResetUser(t, ctx)
	_, first := issueResetToken(t, ctx, userID)

	sendErr := errors.New("smtp unavailable")
	_, second, err := domain.NewPasswordResetToken()
	require.NoError(t, err)
	err = repo.IssuePasswordResetToken(ctx, userID, second,
		func(context.Context) error { return sendErr })
	require.ErrorIs(t, err, sendErr)

	firstRec, err := repo.GetPasswordResetTokenByHash(ctx, first.Hash)
	require.NoError(t, err)
	assert.Nil(t, firstRec.UsedAt, "旧トークンの無効化もロールバックされ有効なまま")
	_, err = repo.GetPasswordResetTokenByHash(ctx, second.Hash)
	assert.ErrorIs(t, err, domain.ErrResetTokenNotFound, "新トークンは保存されない")
}

// UC-009: 完了Txはパスワード更新・当該トークン used_at・他の有効トークン一括無効化（CND-18）・
// 全RT失効（password_changed・VAR-10）を単一Txで適用する。他ユーザーは無傷。
func TestUC009_ConfirmResetRepo_AppliesAllFourInSingleTx(t *testing.T) {
	ctx := context.Background()
	repo := authdb.NewUserRepository(pool)
	userID := seedResetUser(t, ctx)
	p1, _ := seedRefreshToken(t, ctx, userID, uuid.New(), nil)
	p2, _ := seedRefreshToken(t, ctx, userID, uuid.New(), nil)
	otherID := seedResetUser(t, ctx)
	otherRT, _ := seedRefreshToken(t, ctx, otherID, uuid.New(), nil)
	_, otherTok := issueResetToken(t, ctx, otherID)

	_, current := issueResetToken(t, ctx, userID)
	// NOTE: 不変条件（有効最大1本・CND-18）が破れた状態を直接注入し、完了時の一括無効化（防御側）を検証する
	_, stray, err := domain.NewPasswordResetToken()
	require.NoError(t, err)
	require.NoError(t, dbmodels.New(pool).InsertPasswordResetToken(ctx, dbmodels.InsertPasswordResetTokenParams{
		TokenUuid: stray.TokenUUID,
		UserUuid:  userID,
		TokenHash: stray.Hash,
		ExpiresAt: stray.ExpiresAt,
	}))

	newHash, err := bcryptHash("brand-new-secret-pw!")
	require.NoError(t, err)
	require.NoError(t, repo.ConfirmPasswordReset(ctx, userID, current.TokenUUID, newHash))

	after, err := dbmodels.New(pool).GetUserByUuid(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, newHash, after.PasswordHash, "パスワード更新")
	curRec, err := repo.GetPasswordResetTokenByHash(ctx, current.Hash)
	require.NoError(t, err)
	assert.NotNil(t, curRec.UsedAt, "当該トークンは使用済み")
	strayRec, err := repo.GetPasswordResetTokenByHash(ctx, stray.Hash)
	require.NoError(t, err)
	assert.NotNil(t, strayRec.UsedAt, "CND-18: 他の有効トークンも一括無効化")
	for _, p := range []string{p1, p2} {
		rec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(p))
		require.NoError(t, err)
		require.NotNil(t, rec.RevokedAt, "当該ユーザーの全RT失効")
		require.NotNil(t, rec.RevocationReason)
		assert.Equal(t, "password_changed", *rec.RevocationReason, "VAR-10: password_changed")
	}
	otherRTRec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(otherRT))
	require.NoError(t, err)
	assert.Nil(t, otherRTRec.RevokedAt, "他ユーザーのRTは無傷")
	otherTokRec, err := repo.GetPasswordResetTokenByHash(ctx, otherTok.Hash)
	require.NoError(t, err)
	assert.Nil(t, otherTokRec.UsedAt, "他ユーザーの有効リセットトークンは無傷")
}

// UC-009 E8（片系失敗の実証）: パスワード更新0行（削除レース）でTx全体がロールバックし、
// 先行して打った当該トークンの used_at も未使用へ戻る。
func TestUC009_ConfirmResetRepo_UserMissingRollsBackWhole(t *testing.T) {
	ctx := context.Background()
	repo := authdb.NewUserRepository(pool)
	userID := seedResetUser(t, ctx)
	rtPlain, _ := seedRefreshToken(t, ctx, userID, uuid.New(), nil)
	_, token := issueResetToken(t, ctx, userID)
	_, err := pool.Exec(ctx, "UPDATE auth.users SET deleted_at = now() WHERE user_uuid = $1", userID)
	require.NoError(t, err)

	newHash, err := bcryptHash("brand-new-secret-pw!")
	require.NoError(t, err)
	err = repo.ConfirmPasswordReset(ctx, userID, token.TokenUUID, newHash)
	require.Error(t, err)

	rec, err := repo.GetPasswordResetTokenByHash(ctx, token.Hash)
	require.NoError(t, err)
	assert.Nil(t, rec.UsedAt, "MarkUsedはロールバックされ未使用のまま（片系適用なし）")
	rtRec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rtPlain))
	require.NoError(t, err)
	assert.Nil(t, rtRec.RevokedAt, "RTも失効しない")
}

// UC-009 E6（repo層ガード）: 使用済みトークンでの完了は競合エラーとなり、状態は一切変更されない。
func TestUC009_ConfirmResetRepo_UsedTokenConflictLeavesStateUntouched(t *testing.T) {
	ctx := context.Background()
	repo := authdb.NewUserRepository(pool)
	userID := seedResetUser(t, ctx)
	rtPlain, _ := seedRefreshToken(t, ctx, userID, uuid.New(), nil)
	_, token := issueResetToken(t, ctx, userID)
	_, err := pool.Exec(ctx, "UPDATE auth.password_reset_tokens SET used_at = now() WHERE token_uuid = $1", token.TokenUUID)
	require.NoError(t, err)
	before, err := dbmodels.New(pool).GetUserByUuid(ctx, userID)
	require.NoError(t, err)

	newHash, err := bcryptHash("brand-new-secret-pw!")
	require.NoError(t, err)
	err = repo.ConfirmPasswordReset(ctx, userID, token.TokenUUID, newHash)
	require.ErrorIs(t, err, domain.ErrResetTokenConsumeConflict)

	after, err := dbmodels.New(pool).GetUserByUuid(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, before.PasswordHash, after.PasswordHash, "パスワードは変更されない")
	rtRec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rtPlain))
	require.NoError(t, err)
	assert.Nil(t, rtRec.RevokedAt, "RTは失効しない")
}
