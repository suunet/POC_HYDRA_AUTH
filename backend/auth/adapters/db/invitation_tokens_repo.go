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

func (r *UserRepository) GetInvitationTokenByHash(ctx context.Context, hash string) (domain.InvitationTokenRecord, error) {
	row, err := dbmodels.New(r.db).GetInvitationTokenByHash(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.InvitationTokenRecord{}, domain.ErrInvitationTokenNotFound
	}
	if err != nil {
		return domain.InvitationTokenRecord{}, err
	}
	return domain.InvitationTokenRecord{
		TokenUUID: row.TokenUuid,
		Email:     row.Email,
		Role:      row.Role,
		TokenHash: row.TokenHash,
		ExpiresAt: row.ExpiresAt,
		UsedAt:    row.UsedAt,
	}, nil
}

// NOTE: 無効化を token_uuid 単位でなくメールアドレス単位の一括UPDATEにするのは、不変条件（有効トークン最大1本・INF-07/CND-19）が
// 破れても残存有効トークンを取りこぼさないため（ReissueEmailConfirmationToken/IssuePasswordResetTokenと同型）。
// afterInsert（メール送信）失敗時は無効化を含む全ロールバック（BUC-A01 E5・旧有効トークンが残る）。
func (r *UserRepository) IssueInvitationToken(ctx context.Context, email, role string, token domain.InvitationToken, afterInsert func(context.Context) error) error {
	return common.UpdateInTx(ctx, r.db, func(ctx context.Context, tx pgx.Tx) error {
		q := dbmodels.New(tx)
		if _, err := q.InvalidateActiveInvitationTokensByEmail(ctx, email); err != nil {
			return err
		}
		if err := q.InsertInvitationToken(ctx, dbmodels.InsertInvitationTokenParams{
			TokenUuid: token.TokenUUID,
			Email:     email,
			Role:      role,
			TokenHash: token.Hash,
			ExpiresAt: token.ExpiresAt,
		}); err != nil {
			return err
		}
		return afterInsert(ctx)
	})
}

// AcceptInvitation は当該トークンの消費・同一メール宛の他有効トークン一括無効化（CND-19）・
// ユーザー作成（STM-01.未認証）・紐付けロール付与（FR-13）を単一Txで行う（BUC-A02 ステップ8〜9）。
// NOTE: 消費（0行=競合→E3相当）を先頭に置き、後続の失敗で used_at ごとロールバックする（片系適用を残さない）。
// createの users_email_unique 違反は ErrEmailAlreadyRegistered へ写像（E6の二重防御のTx内側・500にしない）
func (r *UserRepository) AcceptInvitation(ctx context.Context, tokenUUID uuid.UUID, email, role, passwordHash string) (uuid.UUID, error) {
	userUUID := uuid.New()
	err := common.UpdateInTx(ctx, r.db, func(ctx context.Context, tx pgx.Tx) error {
		q := dbmodels.New(tx)
		rows, err := q.MarkInvitationTokenUsed(ctx, tokenUUID)
		if err != nil {
			return err
		}
		if rows == 0 {
			return domain.ErrInvitationTokenConsumeConflict
		}
		if _, err := q.InvalidateActiveInvitationTokensByEmail(ctx, email); err != nil {
			return err
		}
		if err := q.InsertUser(ctx, dbmodels.InsertUserParams{
			UserUuid:     userUUID,
			Email:        email,
			PasswordHash: passwordHash,
			Status:       domain.StatusInactive,
		}); err != nil {
			if common.IsUniqueViolationError(err, "users_email_unique") {
				return domain.ErrEmailAlreadyRegistered
			}
			return err
		}
		return q.InsertUserRole(ctx, dbmodels.InsertUserRoleParams{
			UserUuid: userUUID,
			Role:     role,
		})
	})
	if err != nil {
		return uuid.Nil, err
	}
	return userUUID, nil
}
