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
	// ErrInvalidResetToken は UC-009 E5/E6（不存在・使用済み）。応答を同一化しトークン状態を区別させない
	ErrInvalidResetToken = errors.New("invalid password reset token")
	// ErrResetTokenExpired は UC-009 E7（CND-08違反・状態更新なしno-op）
	ErrResetTokenExpired = errors.New("password reset token expired")
)

// PasswordResetConfirmRepository は UC-009 が必要とする最小の永続化操作（消費側IF）。
type PasswordResetConfirmRepository interface {
	GetPasswordResetTokenByHash(ctx context.Context, hash string) (domain.PasswordResetTokenRecord, error)
	// ConfirmPasswordReset は当該トークン消費・他の有効トークン一括無効化（CND-18）・パスワード更新・
	// 全リフレッシュトークン失効（password_changed・VAR-10）を単一Txで行う（E8は全ロールバック）。
	// 消費0行（レース）は domain.ErrResetTokenConsumeConflict
	ConfirmPasswordReset(ctx context.Context, userUUID, tokenUUID uuid.UUID, newPasswordHash string) error
}

type ConfirmPasswordResetHandler struct {
	repo PasswordResetConfirmRepository
}

func NewConfirmPasswordResetHandler(repo PasswordResetConfirmRepository) *ConfirmPasswordResetHandler {
	return &ConfirmPasswordResetHandler{repo: repo}
}

// Handle は UC-009（パスワードリセットを完了する）: 強度検証（E4）→SHA-256照合で検索（E5）→
// 未使用確認（E6）→期限確認（E7・no-op）→bcryptハッシュ化→完了Tx（E8）。
func (h *ConfirmPasswordResetHandler) Handle(ctx context.Context, plainToken, newPassword string) error {
	logger := applog.FromContext(ctx).With("usecase", "UC-009", "ctx", "password_reset_confirm")
	logger.InfoContext(ctx, "usecase started")

	fail := func(msg string, cause error) error {
		logger.ErrorContext(ctx, msg, "error", cause)
		return fmt.Errorf("%s: %w", msg, cause)
	}

	// E4: 新パスワード強度（VAR-02・72バイト条項含む）
	if err := domain.ValidatePassword(newPassword); err != nil {
		logger.WarnContext(ctx, "パスワード強度不足")
		return err
	}

	rec, err := h.repo.GetPasswordResetTokenByHash(ctx, domain.HashPasswordResetToken(plainToken))
	if errors.Is(err, domain.ErrResetTokenNotFound) {
		logger.WarnContext(ctx, "無効なリセットトークン")
		return ErrInvalidResetToken
	}
	if err != nil {
		return fail("could not look up reset token", err)
	}
	// E6: 使用済み（E5と同msg・同応答＝状態を区別しない）
	if rec.Used() {
		logger.WarnContext(ctx, "無効なリセットトークン")
		return ErrInvalidResetToken
	}
	// E7: 期限切れ（CND-08・expires_at判定のみ・DB書き込みなし）
	if rec.Expired(time.Now().UTC()) {
		logger.WarnContext(ctx, "リセットトークン有効期限切れ")
		return ErrResetTokenExpired
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), domain.PasswordBcryptCost)
	if err != nil {
		return fail("could not hash new password", err)
	}

	// UC-009 ステップ7（BUC-U07 フロー16〜18）: 単一Txで消費＋一括無効化＋更新＋全失効（E8は全ロールバック）
	if err := h.repo.ConfirmPasswordReset(ctx, rec.UserUUID, rec.TokenUUID, string(newHash)); err != nil {
		// NOTE: 検証読取〜Txの間に別リクエストが消費したレースは使用済み（E6）相当へ倒す
		if errors.Is(err, domain.ErrResetTokenConsumeConflict) {
			logger.WarnContext(ctx, "無効なリセットトークン")
			return ErrInvalidResetToken
		}
		logger.ErrorContext(ctx, "パスワードリセットトランザクション失敗", "error", err)
		return fmt.Errorf("password reset transaction failed: %w", err)
	}

	// 監査ログ（NFR-07・target=user_id）
	logger.InfoContext(ctx, "パスワードリセット", "user_id", rec.UserUUID.String())
	logger.InfoContext(ctx, "usecase finished", "user_id", rec.UserUUID.String())
	return nil
}
