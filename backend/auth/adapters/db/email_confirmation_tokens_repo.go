package db

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
	"poc-app-hydra/backend/auth/domain"
	"poc-app-hydra/backend/common"
)

func (r *UserRepository) GetEmailConfirmationTokenByHash(ctx context.Context, hash string) (domain.EmailConfirmationTokenRecord, error) {
	row, err := dbmodels.New(r.db).GetEmailConfirmationTokenByHash(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.EmailConfirmationTokenRecord{}, domain.ErrTokenNotFound
	}
	if err != nil {
		return domain.EmailConfirmationTokenRecord{}, err
	}
	return domain.EmailConfirmationTokenRecord{
		TokenUUID: row.TokenUuid,
		UserUUID:  row.UserUuid,
		TokenHash: row.TokenHash,
		ExpiresAt: row.ExpiresAt,
		UsedAt:    row.UsedAt,
	}, nil
}

// NOTE: 無効化を token_uuid 単位でなくユーザー単位の一括UPDATEにするのは、不変条件（有効トークン最大1本・INF-06/CND-10）が
// 破れても残存有効トークンを取りこぼさないため。
func (r *UserRepository) ReissueEmailConfirmationToken(ctx context.Context, userUUID uuid.UUID, token domain.EmailConfirmationToken, afterInsert func(context.Context) error) error {
	return common.UpdateInTx(ctx, r.db, func(ctx context.Context, tx pgx.Tx) error {
		q := dbmodels.New(tx)
		if _, err := q.InvalidateActiveEmailConfirmationTokensByUser(ctx, userUUID); err != nil {
			return err
		}
		if err := q.InsertEmailConfirmationToken(ctx, dbmodels.InsertEmailConfirmationTokenParams{
			TokenUuid: token.TokenUUID,
			UserUuid:  userUUID,
			TokenHash: token.Hash,
			ExpiresAt: token.ExpiresAt,
		}); err != nil {
			return err
		}
		return afterInsert(ctx)
	})
}

func (r *UserRepository) ConsumeEmailConfirmationToken(ctx context.Context, tokenUUID, userUUID uuid.UUID) error {
	return common.UpdateInTx(ctx, r.db, func(ctx context.Context, tx pgx.Tx) error {
		q := dbmodels.New(tx)
		rows, err := q.MarkEmailConfirmationTokenUsed(ctx, tokenUUID)
		if err != nil {
			return err
		}
		if rows == 0 {
			return domain.ErrTokenConsumeConflict
		}
		// NOTE: 遷移元ガード付き（STM-01: mail_unverified→inactive のみ許す。0行=削除済み/他状態→競合）
		rows, err = q.TransitionUserStatus(ctx, dbmodels.TransitionUserStatusParams{
			UserUuid: userUUID,
			Status:   domain.StatusInactive,
			Status_2: domain.StatusMailUnverified,
		})
		if err != nil {
			return err
		}
		// NOTE: 論理削除済みユーザー等で0行＝ロールバックしinvalid-tokenへ倒す（Q-8）
		if rows == 0 {
			return domain.ErrTokenConsumeConflict
		}
		return nil
	})
}
