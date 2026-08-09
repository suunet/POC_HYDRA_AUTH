package tests_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	oapitypes "github.com/oapi-codegen/runtime/types"
	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
	authclient "poc-app-hydra/backend/auth/api/http/client"
	"poc-app-hydra/backend/auth/domain"
)

const resetNewPassword = "reset-new-secret-pw-01!"

func requestPasswordReset(t *testing.T, ctx context.Context, email string) *authclient.RequestPasswordResetResponse {
	t.Helper()
	resp, err := client.RequestPasswordResetWithResponse(ctx, authclient.RequestPasswordResetJSONRequestBody{
		Email: oapitypes.Email(email),
	})
	require.NoError(t, err)
	return resp
}

func confirmPasswordReset(t *testing.T, ctx context.Context, token, newPW string) *authclient.ConfirmPasswordResetResponse {
	t.Helper()
	resp, err := client.ConfirmPasswordResetWithResponse(ctx, authclient.ConfirmPasswordResetJSONRequestBody{
		Token:       token,
		NewPassword: newPW,
	})
	require.NoError(t, err)
	return resp
}

// clearResetRateWindow はレート窓（INF-10・TTL5分）を消し、同一メールでの連続要求を試験可能にする
func clearResetRateWindow(t *testing.T, ctx context.Context, email string) {
	t.Helper()
	require.NoError(t, redisClient.Del(ctx, "password_reset:"+email).Err())
}

// sentResetTokenFor は before 以降にメール送信された平文トークンを返す（EVT-02・平文はメールのみ）
func sentResetTokenFor(t *testing.T, email string, before int) string {
	t.Helper()
	var token string
	for _, m := range mailer.Sent()[before:] {
		if m.To == email {
			token = m.Token
		}
	}
	require.NotEmpty(t, token, "リセットトークンがメール送信される")
	return token
}

// UC-008→UC-009 主成功E2E: 要求→メールの平文トークンで完了→新PWでログイン可・旧PW不可・
// 全RT失効（password_changed・family横断）・当該トークンused_at打刻（STM-03終端）
func TestUC008_UC009_PasswordReset_RealServer_FullFlow(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	_, rt := loginTokens(t, ctx, email)
	stored, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rt))
	require.NoError(t, err)
	otherFamilyPlain, _ := seedRefreshToken(t, ctx, stored.UserUuid, uuid.New(), nil)
	t.Cleanup(func() { clearResetRateWindow(t, ctx, email) })

	before := len(mailer.Sent())
	reqResp := requestPasswordReset(t, ctx, email)
	require.Equal(t, 200, reqResp.StatusCode())
	plainToken := sentResetTokenFor(t, email, before)

	confResp := confirmPasswordReset(t, ctx, plainToken, resetNewPassword)
	require.Equal(t, 200, confResp.StatusCode())

	// パスワード更新の実効性（STM-01不変＝そのままログイン可能）
	oldLogin := login(t, ctx, email, loginPassword)
	assert.Equal(t, 401, oldLogin.StatusCode(), "旧パスワードは401")
	newLogin := login(t, ctx, email, resetNewPassword)
	assert.Equal(t, 200, newLogin.StatusCode(), "新パスワードでログイン可能")

	// 全RT失効（STM-02.セッション失効・password_changed・family横断）
	for _, p := range []string{rt, otherFamilyPlain} {
		rec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(p))
		require.NoError(t, err)
		require.NotNil(t, rec.RevokedAt, "user単位の全失効")
		require.NotNil(t, rec.RevocationReason)
		assert.Equal(t, "password_changed", *rec.RevocationReason, "VAR-10")
	}
	// 当該トークンは使用済み（STM-03: 使用済みで終了）
	tokenRec, err := dbmodels.New(pool).GetPasswordResetTokenByHash(ctx, domain.HashPasswordResetToken(plainToken))
	require.NoError(t, err)
	assert.NotNil(t, tokenRec.UsedAt)
}

// UC-008 E2: 同一メール2回目は429（VAR-12・retry_after=TTL残秒＋Retry-Afterヘッダ）。
// 未登録メールでも挙動一様（列挙オラクル封じ）
func TestUC008_PasswordReset_RealServer_RateLimited_Uniform429(t *testing.T) {
	ctx := context.Background()
	// NOTE: uniqueEmailはt.Name()を含むため親スコープで生成する（サブテスト名の非ASCIIはtypes.Emailの検証を通らない）
	registeredEmail := uniqueEmail(t)
	seedLoginUser(t, ctx, registeredEmail, domain.StatusInactive)
	unregisteredEmail := uniqueEmail(t)
	for name, email := range map[string]string{
		"登録済み(inactive)": registeredEmail,
		"未登録":            unregisteredEmail,
	} {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(func() { clearResetRateWindow(t, ctx, email) })

			first := requestPasswordReset(t, ctx, email)
			require.Equal(t, 200, first.StatusCode(), "1回目は一律200")

			second := requestPasswordReset(t, ctx, email)
			require.Equal(t, 429, second.StatusCode(), "窓内2回目は存在に依らず一様429")
			require.NotNil(t, second.ApplicationproblemJSON429)
			require.NotNil(t, second.ApplicationproblemJSON429.RetryAfter)
			assert.Greater(t, *second.ApplicationproblemJSON429.RetryAfter, 0, "retry_after=TTL残秒")
			assert.NotEmpty(t, second.HTTPResponse.Header.Get("Retry-After"), "Retry-Afterヘッダ（VAR-12③）")
		})
	}
}

// UC-008 A1: 不適格（mail_unverified＝inactive以外）は200・メール送信なし・トークン発行なし
func TestUC008_PasswordReset_RealServer_Ineligible_Silent200(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	seedLoginUser(t, ctx, email, domain.StatusMailUnverified)
	t.Cleanup(func() { clearResetRateWindow(t, ctx, email) })

	before := len(mailer.Sent())
	resp := requestPasswordReset(t, ctx, email)

	require.Equal(t, 200, resp.StatusCode(), "A1: 成功時と区別しない")
	for _, m := range mailer.Sent()[before:] {
		assert.NotEqual(t, email, m.To, "A1: メール送信しない")
	}
	var count int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM auth.password_reset_tokens t JOIN auth.users u ON u.user_uuid = t.user_uuid WHERE u.email = $1", email).Scan(&count))
	assert.Zero(t, count, "A1: トークン発行なし")
}

// CND-18 E2E: 再要求で旧トークンは無効化され（used_at打刻・未使用のまま終端）、完了後の同一トークン再利用も400
func TestUC008_UC009_PasswordReset_RealServer_CND18_SingleActiveToken(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	seedLoginUser(t, ctx, email, domain.StatusInactive)
	t.Cleanup(func() { clearResetRateWindow(t, ctx, email) })

	// 1本目発行 → 窓を消して再要求 → 旧トークンは使用不能（E6経路へ合流）
	before1 := len(mailer.Sent())
	require.Equal(t, 200, requestPasswordReset(t, ctx, email).StatusCode())
	token1 := sentResetTokenFor(t, email, before1)
	clearResetRateWindow(t, ctx, email)

	before2 := len(mailer.Sent())
	require.Equal(t, 200, requestPasswordReset(t, ctx, email).StatusCode())
	token2 := sentResetTokenFor(t, email, before2)
	require.NotEqual(t, token1, token2)

	reuseOld := confirmPasswordReset(t, ctx, token1, resetNewPassword)
	require.Equal(t, 400, reuseOld.StatusCode(), "CND-18: 再要求で旧トークンは無効化済み")
	require.NotNil(t, reuseOld.ApplicationproblemJSON400)
	assert.Contains(t, reuseOld.ApplicationproblemJSON400.Type, "invalid-token")

	// 2本目で完了 → 同一トークンの再利用は400（使い切り）
	require.Equal(t, 200, confirmPasswordReset(t, ctx, token2, resetNewPassword).StatusCode())
	replay := confirmPasswordReset(t, ctx, token2, "another-secret-pw-02!")
	require.Equal(t, 400, replay.StatusCode(), "完了済みトークンの再利用不可")
	assert.Contains(t, replay.ApplicationproblemJSON400.Type, "invalid-token")
}

// UC-008 E3: メール送信失敗は503・無効化含む全ロールバック＝既存有効トークンはそのまま使える（Q-6確定）
func TestUC008_PasswordReset_RealServer_MailFailure_503_RollsBackAll(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	seedLoginUser(t, ctx, email, domain.StatusInactive)
	t.Cleanup(func() { clearResetRateWindow(t, ctx, email) })

	before := len(mailer.Sent())
	require.Equal(t, 200, requestPasswordReset(t, ctx, email).StatusCode())
	token1 := sentResetTokenFor(t, email, before)
	clearResetRateWindow(t, ctx, email)

	mailer.FailWith(errors.New("smtp down"))
	t.Cleanup(func() { mailer.FailWith(nil) })
	failed := requestPasswordReset(t, ctx, email)
	require.Equal(t, 503, failed.StatusCode(), "E3: mail-delivery-error")
	mailer.FailWith(nil)

	// 旧トークンの無効化はロールバック済み＝そのまま完了できる（有効トークンゼロにならない）
	confResp := confirmPasswordReset(t, ctx, token1, resetNewPassword)
	assert.Equal(t, 200, confResp.StatusCode(), "旧有効トークンが残る（全ロールバック）")
}
