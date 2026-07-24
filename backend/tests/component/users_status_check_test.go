package tests_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
)

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
