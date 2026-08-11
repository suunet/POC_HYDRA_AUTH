package tests_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authdb "poc-app-hydra/backend/auth/adapters/db"
	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
	"poc-app-hydra/backend/auth/domain"
)

func issueInvitationToken(t *testing.T, ctx context.Context, email, role string) (string, domain.InvitationToken) {
	t.Helper()
	plain, token, err := domain.NewInvitationToken()
	require.NoError(t, err)
	require.NoError(t, authdb.NewUserRepository(pool).IssueInvitationToken(ctx, email, role, token,
		func(context.Context) error { return nil }))
	return plain, token
}

// UC-011: 発行Txは既存の有効招待トークンを無効化してから新トークンを保存する（CND-19・FR-12・有効は常に最大1本）。
// ロール・メールはDBレコードに紐付く（FR-11）。他メールの有効トークンは無傷
func TestUC011_IssueInvitationRepo_InvalidatesActiveAndInsertsNew(t *testing.T) {
	ctx := context.Background()
	repo := authdb.NewUserRepository(pool)
	email := uniqueEmail(t)
	_, first := issueInvitationToken(t, ctx, email, "operator")
	otherEmail := uniqueEmail(t)
	_, otherTok := issueInvitationToken(t, ctx, otherEmail, "system_admin")

	firstBefore, err := repo.GetInvitationTokenByHash(ctx, first.Hash)
	require.NoError(t, err)
	require.Nil(t, firstBefore.UsedAt, "前提: 1本目は有効")

	_, second := issueInvitationToken(t, ctx, email, "super_admin")

	firstAfter, err := repo.GetInvitationTokenByHash(ctx, first.Hash)
	require.NoError(t, err)
	assert.NotNil(t, firstAfter.UsedAt, "CND-19/FR-12: 再招待で旧トークンは used_at 打ち")
	secondRec, err := repo.GetInvitationTokenByHash(ctx, second.Hash)
	require.NoError(t, err)
	assert.Nil(t, secondRec.UsedAt, "新トークンは有効")
	assert.Equal(t, email, secondRec.Email)
	assert.Equal(t, "super_admin", secondRec.Role, "FR-11: 付与ロールはDBレコード紐付け")
	otherRec, err := repo.GetInvitationTokenByHash(ctx, otherTok.Hash)
	require.NoError(t, err)
	assert.Nil(t, otherRec.UsedAt, "他メールの有効トークンは無傷")
}

// UC-011 E5: 送信コールバック失敗で無効化を含む全ロールバック（旧有効トークンが残る・新トークンは保存されない）
func TestUC011_IssueInvitationRepo_SendFailureRollsBackAll(t *testing.T) {
	ctx := context.Background()
	repo := authdb.NewUserRepository(pool)
	email := uniqueEmail(t)
	_, first := issueInvitationToken(t, ctx, email, "operator")

	sendErr := errors.New("smtp unavailable")
	_, second, err := domain.NewInvitationToken()
	require.NoError(t, err)
	err = repo.IssueInvitationToken(ctx, email, "operator", second,
		func(context.Context) error { return sendErr })
	require.ErrorIs(t, err, sendErr)

	firstRec, err := repo.GetInvitationTokenByHash(ctx, first.Hash)
	require.NoError(t, err)
	assert.Nil(t, firstRec.UsedAt, "旧トークンの無効化もロールバックされ有効なまま")
	_, err = repo.GetInvitationTokenByHash(ctx, second.Hash)
	assert.ErrorIs(t, err, domain.ErrInvitationTokenNotFound, "新トークンは保存されない")
}

// UC-012: 受付Txはユーザー作成（未認証）・ロール付与・当該トークン消費・同一メール宛の他有効トークン
// 一括無効化（CND-19）を単一Txで適用する。他メールは無傷
func TestUC012_AcceptInvitationRepo_CreatesUserWithRole_InvalidatesOthers(t *testing.T) {
	ctx := context.Background()
	repo := authdb.NewUserRepository(pool)
	email := uniqueEmail(t)
	_, current := issueInvitationToken(t, ctx, email, "operator")
	// NOTE: 不変条件（有効最大1本・CND-19）が破れた状態を直接注入し、受付時の一括無効化（防御側）を検証する
	_, stray, err := domain.NewInvitationToken()
	require.NoError(t, err)
	require.NoError(t, dbmodels.New(pool).InsertInvitationToken(ctx, dbmodels.InsertInvitationTokenParams{
		TokenUuid: stray.TokenUUID,
		Email:     email,
		Role:      "operator",
		TokenHash: stray.Hash,
		ExpiresAt: stray.ExpiresAt,
	}))
	otherEmail := uniqueEmail(t)
	_, otherTok := issueInvitationToken(t, ctx, otherEmail, "system_admin")

	hash, err := bcryptHash("brand-new-admin-pw-01!")
	require.NoError(t, err)
	userUUID, err := repo.AcceptInvitation(ctx, current.TokenUUID, email, "operator", hash)
	require.NoError(t, err)

	row, err := dbmodels.New(pool).GetUserByEmail(ctx, email)
	require.NoError(t, err)
	assert.Equal(t, userUUID, row.UserUuid)
	assert.Equal(t, domain.StatusInactive, row.Status, "STM-01.未認証で作成（Q-2=B案）")
	var role string
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT role FROM auth.user_roles WHERE user_uuid = $1", userUUID).Scan(&role))
	assert.Equal(t, "operator", role, "FR-13: 紐付けロールを付与")
	curRec, err := repo.GetInvitationTokenByHash(ctx, current.Hash)
	require.NoError(t, err)
	assert.NotNil(t, curRec.UsedAt, "当該トークンは消費")
	strayRec, err := repo.GetInvitationTokenByHash(ctx, stray.Hash)
	require.NoError(t, err)
	assert.NotNil(t, strayRec.UsedAt, "CND-19: 他の有効トークンも一括無効化")
	otherRec, err := repo.GetInvitationTokenByHash(ctx, otherTok.Hash)
	require.NoError(t, err)
	assert.Nil(t, otherRec.UsedAt, "他メールの有効トークンは無傷")
}

// UC-012 E6（二重防御のTx内側）: 既存ユーザーと同一メールのcreateは users_email_unique 違反を
// 専用エラーへ写像し、先行して打った used_at ごと全ロールバックする（片系適用なし・500にしない）
func TestUC012_AcceptInvitationRepo_ExistingEmail_MapsUniqueViolation_RollsBack(t *testing.T) {
	ctx := context.Background()
	repo := authdb.NewUserRepository(pool)
	email := uniqueEmail(t)
	seedLoginUser(t, ctx, email, domain.StatusInactive)
	_, token := issueInvitationToken(t, ctx, email, "operator")

	hash, err := bcryptHash("brand-new-admin-pw-01!")
	require.NoError(t, err)
	_, err = repo.AcceptInvitation(ctx, token.TokenUUID, email, "operator", hash)
	require.ErrorIs(t, err, domain.ErrEmailAlreadyRegistered)

	rec, err := repo.GetInvitationTokenByHash(ctx, token.Hash)
	require.NoError(t, err)
	assert.Nil(t, rec.UsedAt, "MarkUsedはロールバックされ未使用のまま（トークン非消費・Q-6）")
}

// UC-012 E3（repo層ガード）: 使用済みトークンでの受付は競合エラーとなり、ユーザーは作成されない
func TestUC012_AcceptInvitationRepo_UsedTokenConflictLeavesStateUntouched(t *testing.T) {
	ctx := context.Background()
	repo := authdb.NewUserRepository(pool)
	email := uniqueEmail(t)
	_, token := issueInvitationToken(t, ctx, email, "operator")
	_, err := pool.Exec(ctx, "UPDATE auth.invitation_tokens SET used_at = now() WHERE token_uuid = $1", token.TokenUUID)
	require.NoError(t, err)

	hash, err := bcryptHash("brand-new-admin-pw-01!")
	require.NoError(t, err)
	_, err = repo.AcceptInvitation(ctx, token.TokenUUID, email, "operator", hash)
	require.ErrorIs(t, err, domain.ErrInvitationTokenConsumeConflict)

	_, err = dbmodels.New(pool).GetUserByEmail(ctx, email)
	require.Error(t, err, "ユーザーは作成されない")
}
