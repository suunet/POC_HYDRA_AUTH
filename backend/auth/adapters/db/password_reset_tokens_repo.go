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

func (r *UserRepository) GetPasswordResetTokenByHash(ctx context.Context, hash string) (domain.PasswordResetTokenRecord, error) {
	row, err := dbmodels.New(r.db).GetPasswordResetTokenByHash(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PasswordResetTokenRecord{}, domain.ErrResetTokenNotFound
	}
	if err != nil {
		return domain.PasswordResetTokenRecord{}, err
	}
	return domain.PasswordResetTokenRecord{
		TokenUUID: row.TokenUuid,
		UserUUID:  row.UserUuid,
		TokenHash: row.TokenHash,
		ExpiresAt: row.ExpiresAt,
		UsedAt:    row.UsedAt,
	}, nil
}

// NOTE: 無効化を token_uuid 単位でなくユーザー単位の一括UPDATEにするのは、不変条件（有効トークン最大1本・INF-05/CND-18）が
// 破れても残存有効トークンを取りこぼさないため（ReissueEmailConfirmationTokenと同型）。
// afterInsert（メール送信）失敗時は無効化を含む全ロールバック（BUC-U07 E3・旧有効トークンが残る）。
func (r *UserRepository) IssuePasswordResetToken(ctx context.Context, userUUID uuid.UUID, token domain.PasswordResetToken, afterInsert func(context.Context) error) error {
	return common.UpdateInTx(ctx, r.db, func(ctx context.Context, tx pgx.Tx) error {
		q := dbmodels.New(tx)
		if _, err := q.InvalidateActivePasswordResetTokensByUser(ctx, userUUID); err != nil {
			return err
		}
		if err := q.InsertPasswordResetToken(ctx, dbmodels.InsertPasswordResetTokenParams{
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

// ConfirmPasswordReset は当該トークンの消費・他の有効トークン一括無効化（CND-18）・パスワード更新・
// 当該ユーザーの全リフレッシュトークン失効（password_changed・VAR-10）を単一Txで行う（BUC-U07 ステップ16〜18）。
// NOTE: 消費（0行=競合→E6相当）を先頭に置き、後続の失敗で used_at ごとロールバックする（片系適用を残さない）
func (r *UserRepository) ConfirmPasswordReset(ctx context.Context, userUUID, tokenUUID uuid.UUID, newPasswordHash string) error {
	return common.UpdateInTx(ctx, r.db, func(ctx context.Context, tx pgx.Tx) error {
		q := dbmodels.New(tx)
		rows, err := q.MarkPasswordResetTokenUsed(ctx, tokenUUID)
		if err != nil {
			return err
		}
		if rows == 0 {
			return domain.ErrResetTokenConsumeConflict
		}
		if _, err := q.InvalidateActivePasswordResetTokensByUser(ctx, userUUID); err != nil {
			return err
		}
		affected, err := q.UpdateUserPassword(ctx, dbmodels.UpdateUserPasswordParams{
			UserUuid:     userUUID,
			PasswordHash: newPasswordHash,
		})
		if err != nil {
			return err
		}
		// NOTE: 更新0行（削除レースでの不存在）はエラーとしてTx全体をロールバックする（ChangePasswordと同じ安全網）
		if affected == 0 {
			return errors.New("user not found for password reset")
		}
		reason := command.RevocationReasonPasswordChanged
		return q.RevokeRefreshTokensByUser(ctx, dbmodels.RevokeRefreshTokensByUserParams{
			UserUuid:         userUUID,
			RevocationReason: &reason,
		})
	})
}
