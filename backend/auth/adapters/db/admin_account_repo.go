package db

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
	"poc-app-hydra/backend/auth/app/command"
	"poc-app-hydra/backend/auth/domain"
	"poc-app-hydra/backend/common"
)

// CountActiveSuperAdmins は稼働中super_adminの読取専用の状態観測（CND-14の不変条件検証）。
// 本番のE4判定はロック付きの LockActiveSuperAdmins（無効化Tx内）が担い、本メソッドはTx外の観測用。
func (r *UserRepository) CountActiveSuperAdmins(ctx context.Context) (int64, error) {
	return dbmodels.New(r.db).CountActiveSuperAdmins(ctx)
}

// findAdminAccount は user_uuid で対象（削除済み除外）のロールと状態を検証読取する。未存在は found=false（列挙防止）。
func (r *UserRepository) findAdminAccount(ctx context.Context, userUUID uuid.UUID) ([]string, string, bool, error) {
	q := dbmodels.New(r.db)
	row, err := q.GetUserByUuid(ctx, userUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, err
	}
	roles, err := q.GetUserRoles(ctx, userUUID)
	if err != nil {
		return nil, "", false, err
	}
	return roles, row.Status, true, nil
}

// FindAccountForDisable は無効化対象を検証読取する（UC-014 E1〜E3）。未存在は found=false（E1へ倒す）。
func (r *UserRepository) FindAccountForDisable(ctx context.Context, userUUID uuid.UUID) ([]string, string, bool, error) {
	return r.findAdminAccount(ctx, userUUID)
}

// FindAccountForReactivate は再有効化対象を検証読取する（UC-015 E1〜E3）。未存在は found=false（E1へ倒す）。
func (r *UserRepository) FindAccountForReactivate(ctx context.Context, userUUID uuid.UUID) ([]string, string, bool, error) {
	return r.findAdminAccount(ctx, userUUID)
}

// ReactivateAccount は再有効化（disabled→inactive）を遷移元ガード付き単一文で行う（UC-015・FR-16）。
// NOTE: 遷移0行（無効化済みでない・並行再有効化）は ErrNotDisabled を返す（E3へ合流・二重再有効化の競合を閉じる）
func (r *UserRepository) ReactivateAccount(ctx context.Context, userUUID uuid.UUID) error {
	affected, err := dbmodels.New(r.db).TransitionUserStatus(ctx, dbmodels.TransitionUserStatusParams{
		UserUuid: userUUID,
		Status:   domain.StatusInactive,
		Status_2: domain.StatusDisabled,
	})
	if err != nil {
		return err
	}
	if affected == 0 {
		return command.ErrNotDisabled
	}
	return nil
}

// DisableAccount は無効化（inactive→disabled）と全RT失効を単一Txで行う（UC-014・FR-15）。
// isSuperAdmin時はCND-14計数（稼働中super_admin行のFOR UPDATE）を同一Tx内で先行評価し、TOCTOUを閉じる
// （RepeatableRead＋直列化失敗リトライで並行無効化を逐次化＝LockActiveSuperAdmins NOTE）。
// NOTE: 遷移0行（読取〜遷移間の並行無効化）は ErrAlreadyDisabled でTx全体をロールバックする（片系適用を残さない）
func (r *UserRepository) DisableAccount(ctx context.Context, userUUID uuid.UUID, isSuperAdmin bool) error {
	return common.UpdateInTx(ctx, r.db, func(ctx context.Context, tx pgx.Tx) error {
		q := dbmodels.New(tx)
		if isSuperAdmin {
			active, err := q.LockActiveSuperAdmins(ctx)
			if err != nil {
				return err
			}
			// NOTE: 稀に、対象が読取〜Tx間に並行無効化されると本判定がErrLastSuperAdmin（E4）を返しうる
			// （本来はE3=既に無効化済み）。双方とも409で安全性は不変のため許容する（UC-014の二重無効化レース）
			if len(active) <= 1 {
				return command.ErrLastSuperAdmin
			}
		}
		affected, err := q.TransitionUserStatus(ctx, dbmodels.TransitionUserStatusParams{
			UserUuid: userUUID,
			Status:   domain.StatusDisabled,
			Status_2: domain.StatusInactive,
		})
		if err != nil {
			return err
		}
		if affected == 0 {
			return command.ErrAlreadyDisabled
		}
		reason := command.RevocationReasonAccountDisabled
		return q.RevokeRefreshTokensByUser(ctx, dbmodels.RevokeRefreshTokensByUserParams{
			UserUuid:         userUUID,
			RevocationReason: &reason,
		})
	})
}
