package db

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
	"poc-app-hydra/backend/auth/app/command"
	"poc-app-hydra/backend/auth/domain"
	"poc-app-hydra/backend/common"
)

type UserRepository struct {
	db *pgxpool.Pool
}

func NewUserRepository(db *pgxpool.Pool) *UserRepository {
	return &UserRepository{db: db}
}

func (r *UserRepository) EmailExists(ctx context.Context, email string) (bool, error) {
	q := dbmodels.New(r.db)
	_, err := q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// NOTE: 未登録は found=false で返しエラーにしない（A1で沈黙200へ倒すため・呼び出し側でErrNoRows分岐を持たせない）
func (r *UserRepository) FindUserByEmail(ctx context.Context, email string) (uuid.UUID, string, bool, error) {
	row, err := dbmodels.New(r.db).GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", false, nil
	}
	if err != nil {
		return uuid.Nil, "", false, err
	}
	return row.UserUuid, row.Status, true, nil
}

// NOTE: user・role・tokenを単一トランザクションで登録する。afterInsertはコミット前（トランザクション内）で呼ばれ、エラーを返すと全体をロールバックする
func (r *UserRepository) CreateUser(ctx context.Context, reg domain.Registration, token domain.EmailConfirmationToken, afterInsert func(context.Context) error) error {
	return common.UpdateInTx(ctx, r.db, func(ctx context.Context, tx pgx.Tx) error {
		q := dbmodels.New(tx)
		if err := q.InsertUser(ctx, dbmodels.InsertUserParams{
			UserUuid:     reg.UserUUID,
			Email:        reg.Email,
			PasswordHash: reg.PasswordHash,
			Status:       reg.Status,
		}); err != nil {
			return err
		}
		if err := q.InsertUserRole(ctx, dbmodels.InsertUserRoleParams{
			UserUuid: reg.UserUUID,
			Role:     reg.Role,
		}); err != nil {
			return err
		}
		if err := q.InsertEmailConfirmationToken(ctx, dbmodels.InsertEmailConfirmationTokenParams{
			TokenUuid: token.TokenUUID,
			UserUuid:  reg.UserUUID,
			TokenHash: token.Hash,
			ExpiresAt: token.ExpiresAt,
		}); err != nil {
			return err
		}
		return afterInsert(ctx)
	})
}

// GetLoginUser は削除済みを除外してユーザー（INF-01）とロール（INF-02）を取得する（UC-005）。
// 未存在は found=false で返す（E3を沈黙で扱う・列挙防止）。
func (r *UserRepository) GetLoginUser(ctx context.Context, email string) (command.LoginUser, bool, error) {
	q := dbmodels.New(r.db)
	row, err := q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return command.LoginUser{}, false, nil
	}
	if err != nil {
		return command.LoginUser{}, false, err
	}
	roles, err := q.GetUserRoles(ctx, row.UserUuid)
	if err != nil {
		return command.LoginUser{}, false, err
	}
	return command.LoginUser{
		UserUUID:     row.UserUuid,
		PasswordHash: row.PasswordHash,
		Status:       row.Status,
		Roles:        roles,
	}, true, nil
}

// SaveRefreshToken はリフレッシュトークン（NFR-14: ハッシュのみ）を永続化する（INF-04）。
func (r *UserRepository) SaveRefreshToken(ctx context.Context, rt command.RefreshTokenRecord) error {
	return dbmodels.New(r.db).InsertRefreshToken(ctx, dbmodels.InsertRefreshTokenParams{
		TokenID:   rt.TokenID,
		UserUuid:  rt.UserUUID,
		FamilyID:  rt.FamilyID,
		TokenHash: rt.TokenHash,
		ExpiresAt: rt.ExpiresAt,
	})
}
