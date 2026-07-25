package tests_test

import (
	"context"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
	authclient "poc-app-hydra/backend/auth/api/http/client"
	"poc-app-hydra/backend/auth/domain"
)

const loginPassword = "secret-passw0rd!"

func bcryptHash(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), domain.PasswordBcryptCost)
	return string(h), err
}

// seedLoginUser は指定statusのログイン可能ユーザーを実DBへ作る（パスワードはbcryptコスト12）。
func seedLoginUser(t *testing.T, ctx context.Context, email, status string) {
	t.Helper()
	seedExistingUser(t, ctx, email) // mail_unverified で作成
	hash, err := bcryptHash(loginPassword)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "UPDATE auth.users SET status = $2, password_hash = $3 WHERE email = $1", email, status, hash)
	require.NoError(t, err)
}

func login(t *testing.T, ctx context.Context, email, password string) *authclient.LoginResponse {
	t.Helper()
	resp, err := client.LoginWithResponse(ctx, authclient.LoginJSONRequestBody{
		Email:    openapi_types.Email(email),
		Password: password,
	})
	require.NoError(t, err)
	return resp
}

// UC-005: 主成功 — inactive ユーザーは200・JWTは署名検証でき sub/roles/exp を持つ・リフレッシュはDBにハッシュ保存
func TestUC005_Login_RealServer_Success_IssuesVerifiableTokens(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	seedLoginUser(t, ctx, email, domain.StatusInactive)

	resp := login(t, ctx, email, loginPassword)
	require.Equal(t, 200, resp.StatusCode())
	require.NotNil(t, resp.JSON200)
	assert.NotEmpty(t, resp.JSON200.AccessToken)
	assert.NotEmpty(t, resp.JSON200.RefreshToken)

	// アクセストークンは実サーバの秘密鍵で署名され、公開鍵で検証できる（NFR-02）
	parsed, err := jwt.Parse(resp.JSON200.AccessToken, func(tok *jwt.Token) (interface{}, error) {
		assert.Equal(t, "RS256", tok.Method.Alg())
		return jwtPublicKey, nil
	})
	require.NoError(t, err)
	require.True(t, parsed.Valid)
	claims := parsed.Claims.(jwt.MapClaims)
	row, err := dbmodels.New(pool).GetUserByEmail(ctx, email)
	require.NoError(t, err)
	assert.Equal(t, row.UserUuid.String(), claims["sub"])
	assert.Contains(t, claims["roles"], "user", "NFR-16: rolesクレーム")

	// リフレッシュトークンはDBにSHA-256ハッシュで保存され、平文は保存されない（NFR-14）
	stored, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(resp.JSON200.RefreshToken))
	require.NoError(t, err)
	assert.Equal(t, row.UserUuid, stored.UserUuid)
}

// UC-005 E3/E4: 未登録・パスワード不一致はともに401 authentication-failed（列挙防止・同一応答）
func TestUC005_Login_RealServer_UnknownAndWrongPassword_Return401(t *testing.T) {
	ctx := context.Background()

	unknown := login(t, ctx, uniqueEmail(t), loginPassword)
	require.Equal(t, 401, unknown.StatusCode())
	require.NotNil(t, unknown.ApplicationproblemJSON401)
	assert.Contains(t, unknown.ApplicationproblemJSON401.Type, "authentication-failed")

	email := uniqueEmail(t)
	seedLoginUser(t, ctx, email, domain.StatusInactive)
	wrong := login(t, ctx, email, "WRONG-passw0rd!")
	require.Equal(t, 401, wrong.StatusCode())
	require.NotNil(t, wrong.ApplicationproblemJSON401)
	assert.Contains(t, wrong.ApplicationproblemJSON401.Type, "authentication-failed")
}

// UC-005 E5/E6: メール未確認は403 email-not-verified・無効化済みは403 account-disabled
func TestUC005_Login_RealServer_StatusForbidden(t *testing.T) {
	ctx := context.Background()

	unverified := uniqueEmail(t)
	seedLoginUser(t, ctx, unverified, domain.StatusMailUnverified)
	r5 := login(t, ctx, unverified, loginPassword)
	require.Equal(t, 403, r5.StatusCode())
	require.NotNil(t, r5.ApplicationproblemJSON403)
	assert.Contains(t, r5.ApplicationproblemJSON403.Type, "email-not-verified")

	disabled := uniqueEmail(t)
	seedLoginUser(t, ctx, disabled, domain.StatusDisabled)
	r6 := login(t, ctx, disabled, loginPassword)
	require.Equal(t, 403, r6.StatusCode())
	require.NotNil(t, r6.ApplicationproblemJSON403)
	assert.Contains(t, r6.ApplicationproblemJSON403.Type, "account-disabled")
}

// UC-005 E2: 10回失敗でロックアウト。11回目は429 account-locked＋retry_after秒＋Retry-Afterヘッダ（NFR-03・VAR-11）
func TestUC005_Login_RealServer_LockoutAfter10Failures(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	seedLoginUser(t, ctx, email, domain.StatusInactive)

	// NOTE: 一意メールでキーを分離。10回連続の誤パスワードでロック閾値に到達
	for i := 0; i < 10; i++ {
		r := login(t, ctx, email, "WRONG-passw0rd!")
		require.Equal(t, 401, r.StatusCode(), "10回目まではauth-failed")
	}

	locked := login(t, ctx, email, loginPassword) // 正パスワードでもロック中は弾かれる
	require.Equal(t, 429, locked.StatusCode())
	require.NotNil(t, locked.ApplicationproblemJSON429)
	assert.Contains(t, locked.ApplicationproblemJSON429.Type, "account-locked")
	require.NotNil(t, locked.ApplicationproblemJSON429.ErrorCode)
	assert.Equal(t, "account_locked", *locked.ApplicationproblemJSON429.ErrorCode)
	require.NotNil(t, locked.ApplicationproblemJSON429.RetryAfter)
	assert.Positive(t, *locked.ApplicationproblemJSON429.RetryAfter)
	assert.LessOrEqual(t, *locked.ApplicationproblemJSON429.RetryAfter, 900)
	assert.NotEmpty(t, locked.HTTPResponse.Header.Get("Retry-After"))
}

// UC-005 E1: メールアドレス形式不正は400 validation-error
func TestUC005_Login_RealServer_InvalidEmail_Returns400(t *testing.T) {
	ctx := context.Background()
	resp, err := client.LoginWithResponse(ctx, authclient.LoginJSONRequestBody{
		Email:    openapi_types.Email("not-an-email"), // @なし＝RFC5322違反（net/mailが弾く）
		Password: loginPassword,
	})
	require.NoError(t, err)
	require.Equal(t, 400, resp.StatusCode())
	require.NotNil(t, resp.ApplicationproblemJSON400)
	assert.Contains(t, resp.ApplicationproblemJSON400.Type, "validation-error")
}
