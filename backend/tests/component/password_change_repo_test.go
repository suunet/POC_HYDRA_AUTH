package tests_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authdb "poc-app-hydra/backend/auth/adapters/db"
	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
	"poc-app-hydra/backend/auth/domain"
)

// UC-010: ChangePassword は単一Txでパスワード更新＋当該ユーザーの全RT失効（password_changed）。
// family横断のuser単位で失効し、他ユーザーのトークンは無傷（FR-10）。
func TestUC010_ChangePasswordRepo_UpdatesHash_RevokesAllUserTokens(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	seedLoginUser(t, ctx, email, domain.StatusInactive)
	row, err := dbmodels.New(pool).GetUserByEmail(ctx, email)
	require.NoError(t, err)
	// 同一ユーザーに別family2本（複数デバイス相当）
	p1, _ := seedRefreshToken(t, ctx, row.UserUuid, uuid.New(), nil)
	p2, _ := seedRefreshToken(t, ctx, row.UserUuid, uuid.New(), nil)
	// 他ユーザーのトークン
	otherEmail := uniqueEmail(t)
	seedLoginUser(t, ctx, otherEmail, domain.StatusInactive)
	otherRow, err := dbmodels.New(pool).GetUserByEmail(ctx, otherEmail)
	require.NoError(t, err)
	otherPlain, _ := seedRefreshToken(t, ctx, otherRow.UserUuid, uuid.New(), nil)

	newHash, err := bcryptHash("new-secret-passw0rd!")
	require.NoError(t, err)
	require.NoError(t, authdb.NewUserRepository(pool).ChangePassword(ctx, row.UserUuid, newHash))

	// パスワード更新を実DB確認
	after, err := dbmodels.New(pool).GetUserByUuid(ctx, row.UserUuid)
	require.NoError(t, err)
	assert.Equal(t, newHash, after.PasswordHash, "password_hashが更新される")

	// 当該ユーザーの全RT失効（family横断・reason=password_changed）
	for _, p := range []string{p1, p2} {
		rec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(p))
		require.NoError(t, err)
		require.NotNil(t, rec.RevokedAt, "user単位で全family失効")
		require.NotNil(t, rec.RevocationReason)
		assert.Equal(t, "password_changed", *rec.RevocationReason, "VAR-10: password_changed")
	}
	// 他ユーザーは無傷
	otherRec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(otherPlain))
	require.NoError(t, err)
	assert.Nil(t, otherRec.RevokedAt, "他ユーザーのトークンは失効しない")
}

// UC-010: 既失効トークンの先行理由は上書きしない（WHERE revoked_at IS NULL・冪等ガード）
func TestUC010_ChangePasswordRepo_PreservesPriorRevocationReason(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	seedLoginUser(t, ctx, email, domain.StatusInactive)
	row, err := dbmodels.New(pool).GetUserByEmail(ctx, email)
	require.NoError(t, err)
	pRevoked, revokedID := seedRefreshToken(t, ctx, row.UserUuid, uuid.New(), nil)
	pActive, _ := seedRefreshToken(t, ctx, row.UserUuid, uuid.New(), nil)
	// 先行失効（forced_revocation・VAR-10）を注入
	_, err = pool.Exec(ctx,
		"UPDATE auth.refresh_tokens SET revoked_at = now(), revocation_reason = 'forced_revocation' WHERE token_id = $1", revokedID)
	require.NoError(t, err)

	newHash, err := bcryptHash("new-secret-passw0rd!")
	require.NoError(t, err)
	require.NoError(t, authdb.NewUserRepository(pool).ChangePassword(ctx, row.UserUuid, newHash))

	recPrior, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(pRevoked))
	require.NoError(t, err)
	require.NotNil(t, recPrior.RevocationReason)
	assert.Equal(t, "forced_revocation", *recPrior.RevocationReason, "先行失効の理由を上書きしない")
	recActive, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(pActive))
	require.NoError(t, err)
	require.NotNil(t, recActive.RevocationReason)
	assert.Equal(t, "password_changed", *recActive.RevocationReason)
}

// UC-010 E3系（片系失敗＝更新0行の全ロールバック実証）: 削除レースで更新が0行になると
// エラーになり、当該ユーザーのパスワードハッシュもトークンも一切変化しない。
// NOTE: 失効側の失敗注入はcomponent層では不能（列制約なし・固定reason）のため、片系失敗の
// 実証は更新側0行経路で行い、Tx原子性の残余は共通基盤 common.UpdateInTx（CreateUser等で実証済み）に依拠する
func TestUC010_ChangePasswordRepo_DeletedRace_ErrorsAndNothingChanges(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	seedLoginUser(t, ctx, email, domain.StatusInactive)
	row, err := dbmodels.New(pool).GetUserByEmail(ctx, email)
	require.NoError(t, err)
	p, _ := seedRefreshToken(t, ctx, row.UserUuid, uuid.New(), nil)
	oldHash := row.PasswordHash

	// 削除レースの再現: ChangePassword直前に論理削除される
	_, err = pool.Exec(ctx, "UPDATE auth.users SET deleted_at = now() WHERE user_uuid = $1", row.UserUuid)
	require.NoError(t, err)

	newHash, err := bcryptHash("new-secret-passw0rd!")
	require.NoError(t, err)
	err = authdb.NewUserRepository(pool).ChangePassword(ctx, row.UserUuid, newHash)
	require.Error(t, err, "更新0行（削除レース）はエラー＝Tx全体ロールバック")

	// 片系適用が残っていないことを実DBで確認（削除済みはGetUserByUuidで読めないため生SQL）
	var curHash string
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT password_hash FROM auth.users WHERE user_uuid = $1", row.UserUuid).Scan(&curHash))
	assert.Equal(t, oldHash, curHash, "パスワードは変更前のまま（更新未適用）")
	rec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(p))
	require.NoError(t, err)
	assert.Nil(t, rec.RevokedAt, "当該ユーザー自身のトークンも失効していない（失効側未到達）")
}
