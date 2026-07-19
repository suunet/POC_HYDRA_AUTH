package tests_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
	authclient "poc-app-hydra/backend/auth/api/http/client"
)

func uniqueEmail(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("component-%s-%s@example.com", t.Name(), uuid.NewString())
}

func seedExistingUser(t *testing.T, ctx context.Context, email string) {
	t.Helper()
	q := dbmodels.New(pool)
	userUUID := uuid.New()
	require.NoError(t, q.InsertUser(ctx, dbmodels.InsertUserParams{
		UserUuid:     userUUID,
		Email:        email,
		PasswordHash: "$2a$10$dummydummydummydummydummydummydummydummydummydummydu",
		Status:       "mail_unverified",
	}))
	require.NoError(t, q.InsertUserRole(ctx, dbmodels.InsertUserRoleParams{
		UserUuid: userUUID,
		Role:     "user",
	}))
}

// UC-002: 主成功シナリオ — 実サーバー・実DB・実Redisに対して201を返し、スタブMailerにEVT-01送信が届く
func TestUC002_Register_RealServer_Returns201_AndSendsMail(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)

	resp, err := client.RegisterAccountWithResponse(ctx, authclient.RegisterAccountJSONRequestBody{
		Email:    openapi_types.Email(email),
		Password: "secret-passw0rd!",
	})
	require.NoError(t, err)
	assert.Equal(t, 201, resp.StatusCode())

	found := false
	for _, m := range mailer.Sent() {
		if m.To == email {
			found = true
			assert.NotEmpty(t, m.Token)
			break
		}
	}
	assert.True(t, found, "EVT-01: 確認メールがスタブMailerに届いていること")
}

// UC-002: A1 — 登録済みメールアドレスでも201を返す（列挙攻撃対策・未登録の場合と区別しない）
func TestUC002_Register_RealServer_ExistingEmail_Returns201(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	seedExistingUser(t, ctx, email)

	resp, err := client.RegisterAccountWithResponse(ctx, authclient.RegisterAccountJSONRequestBody{
		Email:    openapi_types.Email(email),
		Password: "secret-passw0rd!",
	})
	require.NoError(t, err)
	assert.Equal(t, 201, resp.StatusCode(), "A1: 登録済みでも未登録の場合と区別しない")

	for _, m := range mailer.Sent() {
		assert.NotEqual(t, email, m.To, "A1: 登録済みの場合はメールを送信しない")
	}
}

// UC-002: E1 — VAR-01（最大254文字）を超えるメールアドレスは400 problem+json。
func TestUC002_Register_RealServer_EmailTooLong_Returns400(t *testing.T) {
	ctx := context.Background()
	// NOTE: レート判定（ステップ2）はメール検証（ステップ3）より前に走るため、実行のたびに異なるローカルパートにしないと前回実行のレート制限キーに阻まれる
	longEmail := strings.Repeat("a", 250) + "-" + uuid.NewString() + "@example.com"

	resp, err := client.RegisterAccountWithResponse(ctx, authclient.RegisterAccountJSONRequestBody{
		Email:    openapi_types.Email(longEmail),
		Password: "secret-passw0rd!",
	})
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode())
	require.NotNil(t, resp.ApplicationproblemJSON400)
	assert.Contains(t, resp.ApplicationproblemJSON400.Type, "validation-error")
}

// UC-002: E4 — 同一メールの2連続POSTは429。retry_afterはTTL残秒数（VAR-16）でRetry-Afterヘッダにも出す
func TestUC002_Register_RealServer_SecondPost_Returns429WithRetryAfter(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	body := authclient.RegisterAccountJSONRequestBody{
		Email:    openapi_types.Email(email),
		Password: "secret-passw0rd!",
	}

	first, err := client.RegisterAccountWithResponse(ctx, body)
	require.NoError(t, err)
	require.Equal(t, 201, first.StatusCode())

	second, err := client.RegisterAccountWithResponse(ctx, body)
	require.NoError(t, err)
	require.Equal(t, 429, second.StatusCode())
	p := second.ApplicationproblemJSON429
	require.NotNil(t, p)
	assert.Contains(t, p.Type, "rate-limit-exceeded")
	require.NotNil(t, p.RetryAfter)
	assert.Greater(t, *p.RetryAfter, 295, "直後の再POSTなのでウィンドウ5分の残がほぼ全部残る")
	assert.LessOrEqual(t, *p.RetryAfter, 300, "固定値でなく実TTL残（300を超えない）")
	assert.Equal(t, strconv.Itoa(*p.RetryAfter), second.HTTPResponse.Header.Get("Retry-After"))
}

// UC-002: E3 — メール送信失敗は503。実DBで登録・トークンがロールバックされている
func TestUC002_Register_RealServer_MailFails_Returns503_RollsBack(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	mailer.FailWith(errors.New("smtp down"))
	t.Cleanup(func() { mailer.FailWith(nil) })

	resp, err := client.RegisterAccountWithResponse(ctx, authclient.RegisterAccountJSONRequestBody{
		Email:    openapi_types.Email(email),
		Password: "secret-passw0rd!",
	})
	require.NoError(t, err)
	require.Equal(t, 503, resp.StatusCode())
	require.NotNil(t, resp.ApplicationproblemJSON503)
	assert.Contains(t, resp.ApplicationproblemJSON503.Type, "mail-delivery-error")

	var users int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM auth.users WHERE email = $1", email).Scan(&users))
	assert.Zero(t, users, "ユーザー行が不存在（論理削除でなくTXロールバック。トークン・ロール行はFKで不存在が保証される）")
}
