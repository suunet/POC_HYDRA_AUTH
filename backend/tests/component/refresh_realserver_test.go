package tests_test

import (
	"context"
	"sync"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
	authclient "poc-app-hydra/backend/auth/api/http/client"
	"poc-app-hydra/backend/auth/domain"
)

func refresh(t *testing.T, ctx context.Context, plain string) *authclient.RefreshTokenResponse {
	t.Helper()
	resp, err := client.RefreshTokenWithResponse(ctx, authclient.RefreshTokenJSONRequestBody{RefreshToken: plain})
	require.NoError(t, err)
	return resp
}

// loginRefreshToken はログインして発行された平文リフレッシュトークンを返す（実サーバ経由・realistic）。
func loginRefreshToken(t *testing.T, ctx context.Context, email string) string {
	t.Helper()
	seedLoginUser(t, ctx, email, domain.StatusInactive)
	resp := login(t, ctx, email, loginPassword)
	require.Equal(t, 200, resp.StatusCode())
	require.NotNil(t, resp.JSON200)
	return resp.JSON200.RefreshToken
}

// UC-006 主成功: ローテーション成功・新JWTは公開鍵で検証でき・新トークンは同一family＋parent_token_id=旧token_id（NFR-14チェーン・c4#3）
func TestUC006_Refresh_RealServer_Success_RotatesChain_And_VerifiableJWT(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	r1 := loginRefreshToken(t, ctx, email)

	old, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(r1))
	require.NoError(t, err)

	resp := refresh(t, ctx, r1)
	require.Equal(t, 200, resp.StatusCode())
	require.NotNil(t, resp.JSON200)
	assert.NotEmpty(t, resp.JSON200.AccessToken)
	assert.NotEmpty(t, resp.JSON200.RefreshToken)
	assert.NotEqual(t, r1, resp.JSON200.RefreshToken, "リフレッシュトークンはローテーションされる")

	// 新アクセストークンは実サーバ秘密鍵で署名され公開鍵で検証できる（NFR-02）
	parsed, err := jwt.Parse(resp.JSON200.AccessToken, func(tok *jwt.Token) (interface{}, error) {
		assert.Equal(t, "RS256", tok.Method.Alg())
		return jwtPublicKey, nil
	})
	require.NoError(t, err)
	require.True(t, parsed.Valid)
	claims := parsed.Claims.(jwt.MapClaims)
	assert.Equal(t, old.UserUuid.String(), claims["sub"])
	assert.Contains(t, claims["roles"], "user", "再発行時点のロール（NFR-16）")

	// 旧トークンは used_at 記録済み（消費）
	oldAfter, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(r1))
	require.NoError(t, err)
	assert.NotNil(t, oldAfter.UsedAt, "旧トークンは使用済み")

	// 新トークンは同一family・parent_token_id=旧token_id（NFR-14チェーン・c4#3）
	newRec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(resp.JSON200.RefreshToken))
	require.NoError(t, err)
	assert.Equal(t, old.FamilyID, newRec.FamilyID, "同一familyでローテーション")
	require.NotNil(t, newRec.ParentTokenID)
	assert.Equal(t, old.TokenID, *newRec.ParentTokenID, "parent_token_id=旧token_id（NFR-14）")
}

// UC-006 E3: 期限切れは401 token-expired・当該トークンを失効（reason=NULL）
func TestUC006_Refresh_RealServer_Expired_Returns401_RevokesWithoutReason(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	seedLoginUser(t, ctx, email, domain.StatusInactive)
	row, err := dbmodels.New(pool).GetUserByEmail(ctx, email)
	require.NoError(t, err)
	plain, tokenID := seedRefreshToken(t, ctx, row.UserUuid, uuid.New(), nil)
	_, err = pool.Exec(ctx, "UPDATE auth.refresh_tokens SET expires_at = now() - interval '1 minute' WHERE token_id = $1", tokenID)
	require.NoError(t, err)

	resp := refresh(t, ctx, plain)
	require.Equal(t, 401, resp.StatusCode())
	require.NotNil(t, resp.ApplicationproblemJSON401)
	assert.Contains(t, resp.ApplicationproblemJSON401.Type, "token-expired")

	rec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(plain))
	require.NoError(t, err)
	assert.NotNil(t, rec.RevokedAt, "期限切れトークンは失効される")
	assert.Nil(t, rec.RevocationReason, "E3の失効理由はNULL（VAR-10に期限切れコードなし）")
}

// UC-006 E4: 使用済みトークンの再提示＝再利用検知。当該familyを一括失効し、他familyは生存する
func TestUC006_Refresh_RealServer_Reuse_RevokesFamily_OtherSurvives(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	r1 := loginRefreshToken(t, ctx, email)

	old, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(r1))
	require.NoError(t, err)
	// 別family（例: 別デバイスのセッション）を同一ユーザーに用意
	otherPlain, _ := seedRefreshToken(t, ctx, old.UserUuid, uuid.New(), nil)

	// 正常に1回ローテーション（R1→R2・R1は使用済みに）
	first := refresh(t, ctx, r1)
	require.Equal(t, 200, first.StatusCode())
	r2 := first.JSON200.RefreshToken

	// 使用済みR1を再提示＝再利用検知
	reuse := refresh(t, ctx, r1)
	require.Equal(t, 401, reuse.StatusCode())
	require.NotNil(t, reuse.ApplicationproblemJSON401)
	assert.Contains(t, reuse.ApplicationproblemJSON401.Type, "session-revoked")
	require.NotNil(t, reuse.ApplicationproblemJSON401.RevocationReason)
	assert.Equal(t, "token_reuse_detected", *reuse.ApplicationproblemJSON401.RevocationReason)

	// 当該family（R1・R2）は一括失効・理由コードも実DBで確認（E5/E6と対称・BJ c5#3）
	for _, p := range []string{r1, r2} {
		rec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(p))
		require.NoError(t, err)
		assert.NotNil(t, rec.RevokedAt, "再利用検知でfamily内の全トークンが失効")
		require.NotNil(t, rec.RevocationReason)
		assert.Equal(t, "token_reuse_detected", *rec.RevocationReason, "family失効の理由コード（VAR-10）")
	}
	// 他family（別セッション）は生存
	otherRec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(otherPlain))
	require.NoError(t, err)
	assert.Nil(t, otherRec.RevokedAt, "他familyのセッションは生かす（NFR-14）")
}

// UC-006 E5/E6: 削除済み/無効化済みは401 session-revoked＋失効理由コード・当該トークン失効
func TestUC006_Refresh_RealServer_DeletedAndDisabled_Returns401WithReason(t *testing.T) {
	ctx := context.Background()

	// E5: 削除済み
	t.Run("deleted", func(t *testing.T) {
		email := uniqueEmail(t)
		seedLoginUser(t, ctx, email, domain.StatusInactive)
		row, err := dbmodels.New(pool).GetUserByEmail(ctx, email)
		require.NoError(t, err)
		plain, _ := seedRefreshToken(t, ctx, row.UserUuid, uuid.New(), nil)
		_, err = pool.Exec(ctx, "UPDATE auth.users SET deleted_at = now() WHERE user_uuid = $1", row.UserUuid)
		require.NoError(t, err)

		resp := refresh(t, ctx, plain)
		require.Equal(t, 401, resp.StatusCode())
		require.NotNil(t, resp.ApplicationproblemJSON401.RevocationReason)
		assert.Equal(t, "account_deleted", *resp.ApplicationproblemJSON401.RevocationReason)
		rec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(plain))
		require.NoError(t, err)
		require.NotNil(t, rec.RevocationReason)
		assert.Equal(t, "account_deleted", *rec.RevocationReason)
	})

	// E6: 無効化済み
	t.Run("disabled", func(t *testing.T) {
		email := uniqueEmail(t)
		seedLoginUser(t, ctx, email, domain.StatusDisabled)
		row, err := dbmodels.New(pool).GetUserByEmail(ctx, email)
		require.NoError(t, err)
		plain, _ := seedRefreshToken(t, ctx, row.UserUuid, uuid.New(), nil)

		resp := refresh(t, ctx, plain)
		require.Equal(t, 401, resp.StatusCode())
		require.NotNil(t, resp.ApplicationproblemJSON401.RevocationReason)
		assert.Equal(t, "account_disabled", *resp.ApplicationproblemJSON401.RevocationReason)
		rec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(plain))
		require.NoError(t, err)
		require.NotNil(t, rec.RevocationReason)
		assert.Equal(t, "account_disabled", *rec.RevocationReason)
	})
}

// UC-006 同時リフレッシュ: 同一トークンの並行2リクエストは check-and-set で直列化され、
// ちょうど一方が200・他方が401 session-revoked（token_reuse_detected）になる（c4#1）
func TestUC006_Refresh_RealServer_ConcurrentRefresh_ExactlyOneWins(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	r1 := loginRefreshToken(t, ctx, email)

	// NOTE: goroutine内で require（FailNow）は使えないため、結果を収集し本体goroutineで検証する
	var wg sync.WaitGroup
	codes := make([]int, 2)
	reasons := make([]string, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp, err := client.RefreshTokenWithResponse(ctx, authclient.RefreshTokenJSONRequestBody{RefreshToken: r1})
			if err != nil {
				errs[idx] = err
				return
			}
			codes[idx] = resp.StatusCode()
			if resp.ApplicationproblemJSON401 != nil && resp.ApplicationproblemJSON401.RevocationReason != nil {
				reasons[idx] = *resp.ApplicationproblemJSON401.RevocationReason
			}
		}(i)
	}
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])

	got200, got401 := 0, 0
	for i := 0; i < 2; i++ {
		switch codes[i] {
		case 200:
			got200++
		case 401:
			got401++
			assert.Equal(t, "token_reuse_detected", reasons[i], "敗者は再利用検知（family失効）")
		default:
			t.Fatalf("想定外のステータス: %d", codes[i])
		}
	}
	assert.Equal(t, 1, got200, "二重発行は起きない（check-and-set直列化）")
	assert.Equal(t, 1, got401, "敗者はちょうど1つ")

	// fail-safe: 競合検知でfamily一括失効＝勝者のR2も失効している
	old, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(r1))
	require.NoError(t, err)
	cnt := 0
	err = pool.QueryRow(ctx,
		"SELECT count(*) FROM auth.refresh_tokens WHERE family_id = $1 AND revoked_at IS NULL", old.FamilyID,
	).Scan(&cnt)
	require.NoError(t, err)
	assert.Equal(t, 0, cnt, "並行競合検知でfamily全体が失効（fail-safe・NFR-14）")
}
