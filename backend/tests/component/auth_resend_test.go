package tests_test

import (
	"context"
	"errors"
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

func seedActiveToken(t *testing.T, ctx context.Context, email, plain string) {
	t.Helper()
	row, err := dbmodels.New(pool).GetUserByEmail(ctx, email)
	require.NoError(t, err)
	require.NoError(t, dbmodels.New(pool).InsertEmailConfirmationToken(ctx, dbmodels.InsertEmailConfirmationTokenParams{
		TokenUuid: uuid.New(),
		UserUuid:  row.UserUuid,
		TokenHash: domain.HashEmailConfirmationToken(plain),
		ExpiresAt: time.Now().Add(domain.EmailConfirmationTokenTTL),
	}))
}

func resend(t *testing.T, ctx context.Context, email string) *authclient.ResendEmailVerificationResponse {
	t.Helper()
	resp, err := client.ResendEmailVerificationWithResponse(ctx, authclient.ResendEmailVerificationJSONRequestBody{
		Email: openapi_types.Email(email),
	})
	require.NoError(t, err)
	return resp
}

// UC-004: 主成功シナリオ — mail_unverified で200・既存有効トークン無効化・新トークン送信
func TestUC004_Resend_RealServer_Unverified_InvalidatesOldAndSendsNew(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	seedExistingUser(t, ctx, email)
	oldPlain := uuid.NewString()
	seedActiveToken(t, ctx, email, oldPlain)

	resp := resend(t, ctx, email)
	assert.Equal(t, 200, resp.StatusCode())

	old, err := dbmodels.New(pool).GetEmailConfirmationTokenByHash(ctx, domain.HashEmailConfirmationToken(oldPlain))
	require.NoError(t, err)
	assert.NotNil(t, old.UsedAt, "CND-10: 既存有効トークンは無効化される")

	var newToken string
	for _, m := range mailer.Sent() {
		if m.To == email {
			newToken = m.Token
		}
	}
	require.NotEmpty(t, newToken, "新トークンがメール送信される")
	assert.NotEqual(t, oldPlain, newToken)
	assert.Equal(t, "mail_unverified", userStatus(t, ctx, email), "STM-01: 状態は遷移しない")
}

// UC-004: A1 — 未登録は200・メール送信なし
func TestUC004_Resend_RealServer_Unregistered_Returns200_NoMail(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)

	before := len(mailer.Sent())
	resp := resend(t, ctx, email)

	assert.Equal(t, 200, resp.StatusCode())
	for _, m := range mailer.Sent()[before:] {
		assert.NotEqual(t, email, m.To, "A1: 未登録にメール送信しない")
	}
}

// UC-004: A2 — mail_unverified 以外（確認済み inactive）は200・メール送信なし
func TestUC004_Resend_RealServer_Confirmed_Returns200_NoMail(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	seedExistingUser(t, ctx, email)
	_, err := pool.Exec(ctx, "UPDATE auth.users SET status = 'inactive' WHERE email = $1", email)
	require.NoError(t, err)

	before := len(mailer.Sent())
	resp := resend(t, ctx, email)

	assert.Equal(t, 200, resp.StatusCode())
	for _, m := range mailer.Sent()[before:] {
		assert.NotEqual(t, email, m.To, "A2: 確認済みにメール送信しない")
	}
}

// UC-004: E1 — 同一メールは5分に1回。2回目は429＋Retry-Afterヘッダ（VAR-13・メールキー）
func TestUC004_Resend_RealServer_RateLimit_Returns429(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	seedExistingUser(t, ctx, email)

	first := resend(t, ctx, email)
	require.Equal(t, 200, first.StatusCode())

	second := resend(t, ctx, email)
	require.Equal(t, 429, second.StatusCode())
	require.NotNil(t, second.ApplicationproblemJSON429)
	assert.Contains(t, second.ApplicationproblemJSON429.Type, "rate-limit-exceeded")
	require.NotNil(t, second.ApplicationproblemJSON429.RetryAfter)
	assert.Positive(t, *second.ApplicationproblemJSON429.RetryAfter)
	assert.NotEmpty(t, second.HTTPResponse.Header.Get("Retry-After"))
}

// UC-004: E2 — メール送信失敗は503・再発行をロールバック（既存トークンは無効化されないまま）
func TestUC004_Resend_RealServer_MailFails_RollsBack_Returns503(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	seedExistingUser(t, ctx, email)
	oldPlain := uuid.NewString()
	seedActiveToken(t, ctx, email, oldPlain)

	mailer.FailWith(errors.New("smtp connection refused"))
	defer mailer.FailWith(nil)

	resp := resend(t, ctx, email)
	assert.Equal(t, 503, resp.StatusCode())
	require.NotNil(t, resp.ApplicationproblemJSON503)
	assert.Contains(t, resp.ApplicationproblemJSON503.Type, "mail-delivery-error")

	old, err := dbmodels.New(pool).GetEmailConfirmationTokenByHash(ctx, domain.HashEmailConfirmationToken(oldPlain))
	require.NoError(t, err)
	assert.Nil(t, old.UsedAt, "E2: ロールバックで既存トークンの無効化も取り消される")

	// VAR-13①: トークンはロールバックされてもレート窓はTx外で消費済み＝同一メールの2回目は429（試行を数える）
	second := resend(t, ctx, email)
	assert.Equal(t, 429, second.StatusCode(), "E2でもレート窓は消費される（VAR-13①・INF-11）")
}
