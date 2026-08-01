package db

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
	"poc-app-hydra/backend/auth/app/command"
	"poc-app-hydra/backend/common"
)

// NOTE: リフレッシュトークン（INF-04）のローテーション/失効はUserRepositoryのメソッドとして実装する
// （login用 SaveRefreshToken/GetLoginUser と同居・同一注入。エンティティ別ファイルへ分割配置）。

// GetRefreshTokenByHash は SHA-256ハッシュで対象トークンを検証読取する（E2/E3/E4判定の材料）。未存在は found=false。
// NOTE: 二重消費の直列化は本読取ではなく RotateRefreshToken の条件付きMarkUsed（used_at IS NULL）が担う。
func (r *UserRepository) GetRefreshTokenByHash(ctx context.Context, hash string) (command.StoredRefreshToken, bool, error) {
	row, err := dbmodels.New(r.db).GetRefreshTokenByHash(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return command.StoredRefreshToken{}, false, nil
	}
	if err != nil {
		return command.StoredRefreshToken{}, false, err
	}
	return command.StoredRefreshToken{
		TokenID:   row.TokenID,
		UserUUID:  row.UserUuid,
		FamilyID:  row.FamilyID,
		ExpiresAt: row.ExpiresAt,
		UsedAt:    row.UsedAt,
		RevokedAt: row.RevokedAt,
	}, true, nil
}

// GetUserForRefresh は token→user を取得する（削除済み除外・UC-006 E5）。roleも合わせて返す。
func (r *UserRepository) GetUserForRefresh(ctx context.Context, userUUID uuid.UUID) (command.RefreshUser, bool, error) {
	q := dbmodels.New(r.db)
	row, err := q.GetUserByUuid(ctx, userUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return command.RefreshUser{}, false, nil
	}
	if err != nil {
		return command.RefreshUser{}, false, err
	}
	roles, err := q.GetUserRoles(ctx, userUUID)
	if err != nil {
		return command.RefreshUser{}, false, err
	}
	return command.RefreshUser{UserUUID: row.UserUuid, Status: row.Status, Roles: roles}, true, nil
}

// RotateRefreshToken は旧トークンに used_at を記録し新トークンを挿入する（単一Tx・UC-006 Q-3）。
// 条件付きMarkUsed（used_at IS NULL）が0行なら並行リクエストに先を越された＝再利用相当として
// command.ErrRefreshTokenAlreadyUsed を返す（直列化ポイント・NFR-14）。
func (r *UserRepository) RotateRefreshToken(ctx context.Context, oldTokenID uuid.UUID, newToken command.RefreshTokenRecord) error {
	return common.UpdateInTx(ctx, r.db, func(ctx context.Context, tx pgx.Tx) error {
		q := dbmodels.New(tx)
		affected, err := q.MarkRefreshTokenUsed(ctx, oldTokenID)
		if err != nil {
			return err
		}
		if affected == 0 {
			return command.ErrRefreshTokenAlreadyUsed
		}
		return q.InsertRefreshToken(ctx, dbmodels.InsertRefreshTokenParams{
			TokenID:       newToken.TokenID,
			UserUuid:      newToken.UserUUID,
			FamilyID:      newToken.FamilyID,
			ParentTokenID: newToken.ParentTokenID,
			TokenHash:     newToken.TokenHash,
			ExpiresAt:     newToken.ExpiresAt,
		})
	})
}

// RevokeRefreshToken は当該トークンのみ失効する（UC-006 E3/E5/E6・reason=nilでNULL）。
func (r *UserRepository) RevokeRefreshToken(ctx context.Context, tokenID uuid.UUID, reason *string) error {
	return dbmodels.New(r.db).RevokeRefreshToken(ctx, dbmodels.RevokeRefreshTokenParams{
		TokenID:          tokenID,
		RevocationReason: reason,
	})
}

// RevokeRefreshTokenFamily は同一family（ローテーションチェーン）を一括失効する（UC-006 E4・NFR-14）。
func (r *UserRepository) RevokeRefreshTokenFamily(ctx context.Context, familyID uuid.UUID, reason string) error {
	return dbmodels.New(r.db).RevokeRefreshTokenFamily(ctx, dbmodels.RevokeRefreshTokenFamilyParams{
		FamilyID:         familyID,
		RevocationReason: &reason,
	})
}
