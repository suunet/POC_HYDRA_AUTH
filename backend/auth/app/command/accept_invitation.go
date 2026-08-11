package command

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"poc-app-hydra/backend/auth/domain"
	applog "poc-app-hydra/backend/common/log"
)

var (
	// ErrInvalidInvitationToken は UC-012 E2/E3（不存在・使用済み）。応答を同一化しトークン状態を区別させない
	ErrInvalidInvitationToken = errors.New("invalid invitation token")
	// ErrInvitationTokenExpired は UC-012 E4（CND-12違反・状態更新なしno-op）
	ErrInvitationTokenExpired = errors.New("invitation token expired")
)

// InvitationAcceptRepository は UC-012 が必要とする最小の永続化操作（消費側IF）。
type InvitationAcceptRepository interface {
	GetInvitationTokenByHash(ctx context.Context, hash string) (domain.InvitationTokenRecord, error)
	// FindUserRolesByEmail はE6（既存アカウント実在）の事前確認に使う（UC-012 §3ステップ6・Q-6二重防御の外側）
	FindUserRolesByEmail(ctx context.Context, email string) (roles []string, found bool, err error)
	// AcceptInvitation は当該トークン消費・同一メール宛の他有効トークン一括無効化（CND-19）・
	// ユーザー作成（STM-01.未認証）・紐付けロール付与（FR-13）を単一Txで行う（E5は全ロールバック）。
	// 消費0行（レース）は domain.ErrInvitationTokenConsumeConflict・既存メール衝突は domain.ErrEmailAlreadyRegistered（E6）
	AcceptInvitation(ctx context.Context, tokenUUID uuid.UUID, email, role, passwordHash string) (uuid.UUID, error)
}

type AcceptInvitationHandler struct {
	repo InvitationAcceptRepository
}

func NewAcceptInvitationHandler(repo InvitationAcceptRepository) *AcceptInvitationHandler {
	return &AcceptInvitationHandler{repo: repo}
}

// Handle は UC-012（招待を受け付ける）: 強度検証（E1）→SHA-256照合で検索（E2）→
// 未使用確認（E3）→期限確認（E4・no-op）→bcryptハッシュ化→受付Tx（E5・E6）。
func (h *AcceptInvitationHandler) Handle(ctx context.Context, plainToken, password string) error {
	logger := applog.FromContext(ctx).With("usecase", "UC-012", "ctx", "invitation_accept")
	logger.InfoContext(ctx, "usecase started")

	fail := func(msg string, cause error) error {
		logger.ErrorContext(ctx, msg, "error", cause)
		return fmt.Errorf("%s: %w", msg, cause)
	}

	// E1: パスワード強度（VAR-02・72バイト条項含む）
	if err := domain.ValidatePassword(password); err != nil {
		logger.WarnContext(ctx, "パスワード強度不足")
		return err
	}

	rec, err := h.repo.GetInvitationTokenByHash(ctx, domain.HashInvitationToken(plainToken))
	if errors.Is(err, domain.ErrInvitationTokenNotFound) {
		logger.WarnContext(ctx, "無効な招待トークン")
		return ErrInvalidInvitationToken
	}
	if err != nil {
		return fail("could not look up invitation token", err)
	}
	// E3: 使用済み（E2と同msg・同応答＝状態を区別しない）
	if rec.UsedAt != nil {
		logger.WarnContext(ctx, "無効な招待トークン")
		return ErrInvalidInvitationToken
	}
	// E4: 期限切れ（CND-12・expires_at判定のみ・DB書き込みなし）
	if time.Now().UTC().After(rec.ExpiresAt) {
		logger.WarnContext(ctx, "招待トークン有効期限切れ")
		return ErrInvitationTokenExpired
	}

	// E6（Q-6二重防御の外側）: 招待発行〜受付間の自己登録レースを事前SELECTで検出（トークンは消費しない）。
	// 確認〜Txの間に割り込むレースはTx内のusers_email_unique捕捉（内側）が同じエラーへ写像する
	if _, found, err := h.repo.FindUserRolesByEmail(ctx, rec.Email); err != nil {
		return fail("could not look up user", err)
	} else if found {
		logger.WarnContext(ctx, "既存アカウントとの競合")
		return domain.ErrEmailAlreadyRegistered
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), domain.PasswordBcryptCost)
	if err != nil {
		return fail("could not hash password", err)
	}

	// フロー8〜9: 単一Txで消費＋一括無効化＋ユーザー作成＋ロール付与（E5は全ロールバック・E6は409）
	if _, err := h.repo.AcceptInvitation(ctx, rec.TokenUUID, rec.Email, rec.Role, string(hash)); err != nil {
		// NOTE: 検証読取〜Txの間に別リクエストが消費したレースは使用済み（E3）相当へ倒す
		if errors.Is(err, domain.ErrInvitationTokenConsumeConflict) {
			logger.WarnContext(ctx, "無効な招待トークン")
			return ErrInvalidInvitationToken
		}
		// E6: 招待発行〜受付間の自己登録レース（users_email_unique違反）＝409（500にしない・Q-6）
		if errors.Is(err, domain.ErrEmailAlreadyRegistered) {
			logger.WarnContext(ctx, "既存アカウントとの競合")
			return domain.ErrEmailAlreadyRegistered
		}
		logger.ErrorContext(ctx, "招待受付トランザクション失敗", "error", err)
		return fmt.Errorf("invitation accept transaction failed: %w", err)
	}

	logger.InfoContext(ctx, "usecase finished")
	return nil
}
