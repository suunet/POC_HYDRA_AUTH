package tests_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
	authclient "poc-app-hydra/backend/auth/api/http/client"
	"poc-app-hydra/backend/auth/domain"
)

const newPassword = "brand-new-secret-passw0rd!"

func changePassword(t *testing.T, ctx context.Context, accessToken, current, newPW string) *authclient.ChangePasswordResponse {
	t.Helper()
	editors := []authclient.RequestEditorFn{}
	if accessToken != "" {
		editors = append(editors, withBearer(accessToken))
	}
	resp, err := client.ChangePasswordWithResponse(ctx, authclient.ChangePasswordJSONRequestBody{
		CurrentPassword: current,
		NewPassword:     newPW,
	}, editors...)
	require.NoError(t, err)
	return resp
}

// UC-010 主成功E2E: login→change→当該ユーザーの全RT失効（password_changed・family横断）・
// 他ユーザー生存・旧PWでlogin不可・新PWでlogin可
func TestUC010_ChangePassword_RealServer_Success_RevokesAllAndRotatesCredential(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	at, rt := loginTokens(t, ctx, email)
	stored, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rt))
	require.NoError(t, err)
	// 同一ユーザーの別family（別デバイス相当）と他ユーザーのトークン
	otherFamilyPlain, _ := seedRefreshToken(t, ctx, stored.UserUuid, uuid.New(), nil)
	victimEmail := uniqueEmail(t)
	_, victimRT := loginTokens(t, ctx, victimEmail)

	resp := changePassword(t, ctx, at, loginPassword, newPassword)
	require.Equal(t, 200, resp.StatusCode())
	require.NotNil(t, resp.JSON200)
	assert.Equal(t, "password_changed", resp.JSON200.RevocationReason, "FR-10: 応答に失効理由")

	// 当該ユーザーの全RT失効（family横断・reason=password_changed）
	for _, p := range []string{rt, otherFamilyPlain} {
		rec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(p))
		require.NoError(t, err)
		require.NotNil(t, rec.RevokedAt, "user単位の全失効（STM-02.セッション失効）")
		require.NotNil(t, rec.RevocationReason)
		assert.Equal(t, "password_changed", *rec.RevocationReason)
	}
	// 他ユーザーは無傷
	victimRec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(victimRT))
	require.NoError(t, err)
	assert.Nil(t, victimRec.RevokedAt, "他ユーザーのセッションは失効しない")

	// 旧PWはlogin不可・新PWはlogin可（パスワード更新の実効性）
	oldLogin := login(t, ctx, email, loginPassword)
	require.Equal(t, 401, oldLogin.StatusCode(), "旧パスワードでのログインは401 authentication-failed")
	newLogin := login(t, ctx, email, newPassword)
	require.Equal(t, 200, newLogin.StatusCode(), "新パスワードでログインできる")
}

// UC-010 Q-2: 現在と同一の新パスワードでも200（実質的な全セッション失効操作）
func TestUC010_ChangePassword_RealServer_SamePassword_Returns200(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	at, rt := loginTokens(t, ctx, email)

	resp := changePassword(t, ctx, at, loginPassword, loginPassword)
	require.Equal(t, 200, resp.StatusCode())

	rec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rt))
	require.NoError(t, err)
	assert.NotNil(t, rec.RevokedAt, "同一PWでも全失効は実行される")
	// 同一PWで引き続きログイン可能
	relogin := login(t, ctx, email, loginPassword)
	assert.Equal(t, 200, relogin.StatusCode())
}

// UC-010 E2: 現在パスワード不一致は403 password-mismatch・何も変わらない
func TestUC010_ChangePassword_RealServer_WrongCurrent_Returns403_NoChange(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	at, rt := loginTokens(t, ctx, email)

	resp := changePassword(t, ctx, at, "WRONG-secret-passw0rd!", newPassword)
	require.Equal(t, 403, resp.StatusCode())
	require.NotNil(t, resp.ApplicationproblemJSON403)
	assert.Contains(t, resp.ApplicationproblemJSON403.Type, "password-mismatch")

	rec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rt))
	require.NoError(t, err)
	assert.Nil(t, rec.RevokedAt, "失効しない")
	stillOld := login(t, ctx, email, loginPassword)
	assert.Equal(t, 200, stillOld.StatusCode(), "パスワードは変わっていない")
}

// UC-010 E4/E5: 削除済み・無効化済みは401 session-revoked＋reason・no-op（トークン状態不変）
func TestUC010_ChangePassword_RealServer_DeletedOrDisabled_Returns401(t *testing.T) {
	ctx := context.Background()

	t.Run("E4削除済み", func(t *testing.T) {
		email := uniqueEmail(t)
		at, rt := loginTokens(t, ctx, email)
		stored, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rt))
		require.NoError(t, err)
		var hashBefore string
		require.NoError(t, pool.QueryRow(ctx,
			"SELECT password_hash FROM auth.users WHERE user_uuid = $1", stored.UserUuid).Scan(&hashBefore))
		_, err = pool.Exec(ctx, "UPDATE auth.users SET deleted_at = now() WHERE user_uuid = $1", stored.UserUuid)
		require.NoError(t, err)

		resp := changePassword(t, ctx, at, loginPassword, newPassword)
		require.Equal(t, 401, resp.StatusCode())
		require.NotNil(t, resp.ApplicationproblemJSON401)
		require.NotNil(t, resp.ApplicationproblemJSON401.RevocationReason)
		assert.Equal(t, "account_deleted", *resp.ApplicationproblemJSON401.RevocationReason)
		rec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rt))
		require.NoError(t, err)
		assert.Nil(t, rec.RevokedAt, "no-op: トークン状態は変更しない")
		var hashAfter string
		require.NoError(t, pool.QueryRow(ctx,
			"SELECT password_hash FROM auth.users WHERE user_uuid = $1", stored.UserUuid).Scan(&hashAfter))
		assert.Equal(t, hashBefore, hashAfter, "no-op: パスワードも変更しない")
	})

	t.Run("E5無効化済み", func(t *testing.T) {
		email := uniqueEmail(t)
		at, rt := loginTokens(t, ctx, email)
		stored, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rt))
		require.NoError(t, err)
		var hashBefore string
		require.NoError(t, pool.QueryRow(ctx,
			"SELECT password_hash FROM auth.users WHERE user_uuid = $1", stored.UserUuid).Scan(&hashBefore))
		_, err = pool.Exec(ctx, "UPDATE auth.users SET status = 'disabled' WHERE user_uuid = $1", stored.UserUuid)
		require.NoError(t, err)

		resp := changePassword(t, ctx, at, loginPassword, newPassword)
		require.Equal(t, 401, resp.StatusCode())
		require.NotNil(t, resp.ApplicationproblemJSON401)
		require.NotNil(t, resp.ApplicationproblemJSON401.RevocationReason)
		assert.Equal(t, "account_disabled", *resp.ApplicationproblemJSON401.RevocationReason)
		rec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rt))
		require.NoError(t, err)
		assert.Nil(t, rec.RevokedAt, "no-op")
		var hashAfter string
		require.NoError(t, pool.QueryRow(ctx,
			"SELECT password_hash FROM auth.users WHERE user_uuid = $1", stored.UserUuid).Scan(&hashAfter))
		assert.Equal(t, hashBefore, hashAfter, "no-op: パスワードも変更しない")
	})
}

// UC-010 M1（FR-19）: AT欠落・不正はPUTルートでも401 invalid-token一様＋WWW-Authenticate
func TestUC010_ChangePassword_RealServer_M1_Returns401Uniformly(t *testing.T) {
	ctx := context.Background()
	expiredAT, err := domain.GenerateAccessToken(jwtSigningKey, uuid.New().String(), []string{"user"}, -time.Minute)
	require.NoError(t, err)
	cases := []struct {
		name string
		at   string
	}{
		{"AT欠落", ""},
		{"フォーマット不正", "not-a-jwt"},
		{"期限切れAT（実署名）", expiredAT},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := changePassword(t, ctx, c.at, loginPassword, newPassword)
			require.Equal(t, 401, resp.StatusCode())
			require.NotNil(t, resp.ApplicationproblemJSON401)
			assert.Contains(t, resp.ApplicationproblemJSON401.Type, "invalid-token")
			assert.Equal(t, "Bearer", resp.HTTPResponse.Header.Get("WWW-Authenticate"))
		})
	}
}
