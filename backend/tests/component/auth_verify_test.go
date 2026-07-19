package tests_test

import (
	"context"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
	authclient "poc-app-hydra/backend/auth/api/http/client"
	"poc-app-hydra/backend/auth/domain"
)

func withClientIP(ip string) authclient.RequestEditorFn {
	return func(ctx context.Context, req *nethttp.Request) error {
		req.Header.Set("X-Forwarded-For", ip)
		return nil
	}
}

func registerAndTakeToken(t *testing.T, ctx context.Context, email string) string {
	t.Helper()
	resp, err := client.RegisterAccountWithResponse(ctx, authclient.RegisterAccountJSONRequestBody{
		Email:    openapi_types.Email(email),
		Password: "secret-passw0rd!",
	})
	require.NoError(t, err)
	require.Equal(t, 201, resp.StatusCode())
	for _, m := range mailer.Sent() {
		if m.To == email {
			return m.Token
		}
	}
	t.Fatal("確認メールが見つからない")
	return ""
}

func userStatus(t *testing.T, ctx context.Context, email string) string {
	t.Helper()
	row, err := dbmodels.New(pool).GetUserByEmail(ctx, email)
	require.NoError(t, err)
	return row.Status
}

// UC-003: 主成功シナリオ — 登録→確認で200・実DBで mail_unverified→inactive・トークン使い切り
func TestUC003_Verify_RealServer_HappyPath_TransitionsToInactive(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	token := registerAndTakeToken(t, ctx, email)
	ip := withClientIP("198.51.100.h" + uuid.NewString()[:8]) // NOTE: VAR-17のIPバケットをテスト毎に分離

	resp, err := client.VerifyEmailWithResponse(ctx, authclient.VerifyEmailJSONRequestBody{Token: token}, ip)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode())
	assert.Equal(t, "inactive", userStatus(t, ctx, email), "STM-01: mail_unverified→inactive")

	// E1b: 使い切り＝同一トークン再送は invalid-token
	again, err := client.VerifyEmailWithResponse(ctx, authclient.VerifyEmailJSONRequestBody{Token: token}, ip)
	require.NoError(t, err)
	require.Equal(t, 400, again.StatusCode())
	require.NotNil(t, again.ApplicationproblemJSON400)
	assert.Contains(t, again.ApplicationproblemJSON400.Type, "invalid-token")
}

// UC-003: E1a — 不在トークンは400 invalid-token
func TestUC003_Verify_RealServer_UnknownToken_Returns400(t *testing.T) {
	ctx := context.Background()
	resp, err := client.VerifyEmailWithResponse(ctx, authclient.VerifyEmailJSONRequestBody{Token: uuid.NewString()}, withClientIP("198.51.100.u"+uuid.NewString()[:8]))
	require.NoError(t, err)
	require.Equal(t, 400, resp.StatusCode())
	require.NotNil(t, resp.ApplicationproblemJSON400)
	assert.Contains(t, resp.ApplicationproblemJSON400.Type, "invalid-token")
}

// UC-003: E2 — 期限切れトークン（実DBへ直接シード）は400 token-expired
func TestUC003_Verify_RealServer_ExpiredToken_Returns400(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	seedExistingUser(t, ctx, email)
	row, err := dbmodels.New(pool).GetUserByEmail(ctx, email)
	require.NoError(t, err)

	plain := uuid.NewString()
	require.NoError(t, dbmodels.New(pool).InsertEmailConfirmationToken(ctx, dbmodels.InsertEmailConfirmationTokenParams{
		TokenUuid: uuid.New(),
		UserUuid:  row.UserUuid,
		TokenHash: domain.HashEmailConfirmationToken(plain),
		ExpiresAt: time.Now().Add(-time.Minute),
	}))

	resp, err := client.VerifyEmailWithResponse(ctx, authclient.VerifyEmailJSONRequestBody{Token: plain}, withClientIP("198.51.100.e"+uuid.NewString()[:8]))
	require.NoError(t, err)
	require.Equal(t, 400, resp.StatusCode())
	require.NotNil(t, resp.ApplicationproblemJSON400)
	assert.Contains(t, resp.ApplicationproblemJSON400.Type, "token-expired")
	assert.Equal(t, "mail_unverified", userStatus(t, ctx, email), "状態は遷移しない")
}

// UC-003: E4 — 同一IPから1分に10回を超えると429（VAR-17・実Redis・Retry-Afterヘッダ）
func TestUC003_Verify_RealServer_RateLimit_Returns429(t *testing.T) {
	ctx := context.Background()
	ip := "198.51.100." + uuid.NewString()[:8] // NOTE: 他テスト（既定IP）と衝突しない一意IP
	for i := 0; i < 10; i++ {
		resp, err := client.VerifyEmailWithResponse(ctx, authclient.VerifyEmailJSONRequestBody{Token: uuid.NewString()}, withClientIP(ip))
		require.NoError(t, err)
		require.Equal(t, 400, resp.StatusCode(), "上限内は通常評価（不在トークン=400）")
	}

	resp, err := client.VerifyEmailWithResponse(ctx, authclient.VerifyEmailJSONRequestBody{Token: uuid.NewString()}, withClientIP(ip))
	require.NoError(t, err)
	require.Equal(t, 429, resp.StatusCode())
	require.NotNil(t, resp.ApplicationproblemJSON429)
	assert.Contains(t, resp.ApplicationproblemJSON429.Type, "rate-limit-exceeded")
	require.NotNil(t, resp.ApplicationproblemJSON429.RetryAfter)
	assert.Positive(t, *resp.ApplicationproblemJSON429.RetryAfter)
	assert.LessOrEqual(t, *resp.ApplicationproblemJSON429.RetryAfter, 60)
	assert.NotEmpty(t, resp.HTTPResponse.Header.Get("Retry-After"))
}
