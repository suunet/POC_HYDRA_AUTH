package tests_test

import (
	"context"
	"fmt"
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
	require.NoError(t, q.InsertUser(ctx, dbmodels.InsertUserParams{
		UserUuid:     uuid.New(),
		Email:        email,
		PasswordHash: "$2a$10$dummydummydummydummydummydummydummydummydummydummydu",
		Status:       "mail_unverified",
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
