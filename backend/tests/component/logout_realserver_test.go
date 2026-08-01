package tests_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
	authclient "poc-app-hydra/backend/auth/api/http/client"
	"poc-app-hydra/backend/auth/domain"
)

func withBearer(token string) authclient.RequestEditorFn {
	return func(ctx context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
}

func logout(t *testing.T, ctx context.Context, accessToken, refreshToken string) *authclient.LogoutResponse {
	t.Helper()
	editors := []authclient.RequestEditorFn{}
	if accessToken != "" {
		editors = append(editors, withBearer(accessToken))
	}
	resp, err := client.LogoutWithResponse(ctx, authclient.LogoutJSONRequestBody{RefreshToken: refreshToken}, editors...)
	require.NoError(t, err)
	return resp
}

// loginTokens はログインして実サーバ発行のAT/RTを得る。
func loginTokens(t *testing.T, ctx context.Context, email string) (accessToken, refreshToken string) {
	t.Helper()
	seedLoginUser(t, ctx, email, domain.StatusInactive)
	resp := login(t, ctx, email, loginPassword)
	require.Equal(t, 200, resp.StatusCode())
	require.NotNil(t, resp.JSON200)
	return resp.JSON200.AccessToken, resp.JSON200.RefreshToken
}

// UC-007 主成功: login→logout でRT1本のみ失効（reason NULL）・同一ユーザーの他familyトークンは生存
func TestUC007_Logout_RealServer_Success_RevokesSingleToken(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	at, rt := loginTokens(t, ctx, email)

	stored, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rt))
	require.NoError(t, err)
	// 別family（別デバイス相当）のトークンを同一ユーザーへ用意
	otherPlain, _ := seedRefreshToken(t, ctx, stored.UserUuid, uuid.New(), nil)

	resp := logout(t, ctx, at, rt)
	require.Equal(t, 200, resp.StatusCode())

	rec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rt))
	require.NoError(t, err)
	require.NotNil(t, rec.RevokedAt, "当該RTが失効される")
	assert.Nil(t, rec.RevocationReason, "revocation_reasonはNULL（FR-07・自発的操作）")

	other, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(otherPlain))
	require.NoError(t, err)
	assert.Nil(t, other.RevokedAt, "他トークン（別family）は生存＝単一トークン失効（FR-07）")
}

// UC-007 A1/Q-2変種: 不存在・既失効・使用済み・期限切れ いずれも200（冪等）・family/他トークン生存・再利用検知なし
func TestUC007_Logout_RealServer_IdempotentVariants_Return200(t *testing.T) {
	ctx := context.Background()

	t.Run("不存在トークン", func(t *testing.T) {
		email := uniqueEmail(t)
		at, _ := loginTokens(t, ctx, email)
		resp := logout(t, ctx, at, "does-not-exist")
		assert.Equal(t, 200, resp.StatusCode(), "A1: 存在有無を漏洩しない")
	})

	t.Run("既失効トークン（二重ログアウト）", func(t *testing.T) {
		email := uniqueEmail(t)
		at, rt := loginTokens(t, ctx, email)
		first := logout(t, ctx, at, rt)
		require.Equal(t, 200, first.StatusCode())
		afterFirst, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rt))
		require.NoError(t, err)
		require.NotNil(t, afterFirst.RevokedAt)

		second := logout(t, ctx, at, rt)
		assert.Equal(t, 200, second.StatusCode(), "既失効も200（冪等・RFC 7009）")
		afterSecond, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rt))
		require.NoError(t, err)
		assert.Equal(t, *afterFirst.RevokedAt, *afterSecond.RevokedAt, "冪等ガードで revoked_at を再上書きしない（タイムスタンプ不変）")
	})

	t.Run("先行失効理由の保持（管理失効済みトークンへのログアウト）", func(t *testing.T) {
		email := uniqueEmail(t)
		at, rt := loginTokens(t, ctx, email)
		stored, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rt))
		require.NoError(t, err)
		// 先行して理由付き失効（forced_revocation・VAR-10）が入った状態を作る
		_, err = pool.Exec(ctx,
			"UPDATE auth.refresh_tokens SET revoked_at = now(), revocation_reason = 'forced_revocation' WHERE token_id = $1",
			stored.TokenID)
		require.NoError(t, err)

		resp := logout(t, ctx, at, rt)
		assert.Equal(t, 200, resp.StatusCode())
		rec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rt))
		require.NoError(t, err)
		require.NotNil(t, rec.RevocationReason)
		assert.Equal(t, "forced_revocation", *rec.RevocationReason, "先行失効の理由コードをNULLで上書きしない（冪等ガードの実効性）")
	})

	t.Run("使用済みトークン（ローテーション後の旧RT）", func(t *testing.T) {
		email := uniqueEmail(t)
		at, rt1 := loginTokens(t, ctx, email)
		// UC-006でローテーション（rt1は使用済みに・rt2が同一familyの現行）
		refreshed, err := client.RefreshTokenWithResponse(ctx, authclient.RefreshTokenJSONRequestBody{RefreshToken: rt1})
		require.NoError(t, err)
		require.Equal(t, 200, refreshed.StatusCode())
		rt2 := refreshed.JSON200.RefreshToken

		resp := logout(t, ctx, at, rt1)
		assert.Equal(t, 200, resp.StatusCode(), "使用済み提示でも200（Q-2・再利用検知を発火しない）")

		old, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rt1))
		require.NoError(t, err)
		assert.NotNil(t, old.RevokedAt, "使用済みRTも失効される（Q-2一律冪等失効）")
		cur, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rt2))
		require.NoError(t, err)
		assert.Nil(t, cur.RevokedAt, "同一familyの現行RTは生存＝family一括失効しない（FR-07経路限定）")
	})

	t.Run("期限切れトークン", func(t *testing.T) {
		email := uniqueEmail(t)
		at, rt := loginTokens(t, ctx, email)
		stored, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rt))
		require.NoError(t, err)
		_, err = pool.Exec(ctx, "UPDATE auth.refresh_tokens SET expires_at = now() - interval '1 minute' WHERE token_id = $1", stored.TokenID)
		require.NoError(t, err)

		resp := logout(t, ctx, at, rt)
		assert.Equal(t, 200, resp.StatusCode(), "期限切れでも200（UC-006 E3の401は再発行経路の意味論）")
		rec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rt))
		require.NoError(t, err)
		assert.NotNil(t, rec.RevokedAt, "期限切れRTも失効される")
	})
}

// UC-007 E2: 他ユーザーのRT提示は200（正常応答と同一）・失効されない
func TestUC007_Logout_RealServer_OthersToken_Returns200_NoRevoke(t *testing.T) {
	ctx := context.Background()
	victim := uniqueEmail(t)
	_, victimRT := loginTokens(t, ctx, victim)
	attacker := uniqueEmail(t)
	attackerAT, _ := loginTokens(t, ctx, attacker)

	resp := logout(t, ctx, attackerAT, victimRT)
	require.Equal(t, 200, resp.StatusCode(), "E2: 所有情報を漏洩しない（正常応答と同一）")

	rec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(victimRT))
	require.NoError(t, err)
	assert.Nil(t, rec.RevokedAt, "他ユーザーのRTは失効されない")
}

// UC-007 M1（FR-19）: AT欠落・期限切れ・署名不正いずれも401 invalid-token 一様＋WWW-Authenticate
func TestUC007_Logout_RealServer_M1Failures_Return401Uniformly(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	_, rt := loginTokens(t, ctx, email)

	expiredAT, err := domain.GenerateAccessToken(jwtSigningKey, uuid.New().String(), []string{"user"}, -time.Minute)
	require.NoError(t, err)

	cases := []struct {
		name string
		at   string
	}{
		{"AT欠落", ""},
		{"期限切れAT（実署名）", expiredAT},
		{"フォーマット不正", "not-a-jwt"}, // 署名不正（別鍵署名）はunitで被覆済み
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := logout(t, ctx, c.at, rt)
			require.Equal(t, 401, resp.StatusCode())
			require.NotNil(t, resp.ApplicationproblemJSON401)
			assert.Contains(t, resp.ApplicationproblemJSON401.Type, "invalid-token", "失敗種別を区別しない一様応答")
			assert.Equal(t, "Bearer", resp.HTTPResponse.Header.Get("WWW-Authenticate"))
		})
	}

	// M1で拒否されてもRTは無傷（失効はCND-06成立後のみ）
	rec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rt))
	require.NoError(t, err)
	assert.Nil(t, rec.RevokedAt)
}
