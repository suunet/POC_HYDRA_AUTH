package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
	"poc-app-hydra/backend/auth/domain"
	"poc-app-hydra/backend/common"
)

type UserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

func (r *UserRepository) EmailExists(ctx context.Context, email string) (bool, error) {
	q := dbmodels.New(r.pool)
	_, err := q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// CreateUser はユーザー（INF-01）とロール（INF-02）を単一トランザクションで登録する
func (r *UserRepository) CreateUser(ctx context.Context, reg domain.Registration) error {
	return common.UpdateInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := dbmodels.New(tx)
		if err := q.InsertUser(ctx, dbmodels.InsertUserParams{
			UserUuid:     reg.UserUUID,
			Email:        reg.Email,
			PasswordHash: reg.PasswordHash,
			Status:       reg.Status,
		}); err != nil {
			return err
		}
		return q.InsertUserRole(ctx, dbmodels.InsertUserRoleParams{
			UserUuid: reg.UserUUID,
			Role:     reg.Role,
		})
	})
}
