package tests_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
)

// STM-01: 'invited' はSTM-01から除去済み（states.md・招待済み未受付はINF-07の有効未使用トークンで表す=CND-19）。
// users_status_check（0005で再定義）は 'invited' でのINSERTを拒否する
func TestUsersStatusCheck_RejectsRemovedInvited(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	// NOTE: 制約未適用の環境ではINSERTが成功して行が残り、後続のmigration適用を阻害し得るため成否に依らず掃除する
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM auth.users WHERE email = $1", email)
	})

	err := dbmodels.New(pool).InsertUser(ctx, dbmodels.InsertUserParams{
		UserUuid:     uuid.New(),
		Email:        email,
		PasswordHash: "$2a$10$dummydummydummydummydummydummydummydummydummydummydu",
		Status:       "invited",
	})

	require.Error(t, err, "STM-01から除去された'invited'は許可されない")
	assert.Contains(t, err.Error(), "users_status_check")
}

// STM-01/INF-01: users_status_check はCHECK許可外のstatus値でのINSERTを拒否する
func TestUsersStatusCheck_RejectsValueOutsideSTM01(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)

	err := dbmodels.New(pool).InsertUser(ctx, dbmodels.InsertUserParams{
		UserUuid:     uuid.New(),
		Email:        email,
		PasswordHash: "$2a$10$dummydummydummydummydummydummydummydummydummydummydu",
		Status:       "bogus_status",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "users_status_check")
}
