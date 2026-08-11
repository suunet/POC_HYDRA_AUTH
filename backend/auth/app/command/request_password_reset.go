package command

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"poc-app-hydra/backend/auth/domain"
	applog "poc-app-hydra/backend/common/log"
)

type PasswordResetRequestRepository interface {
	FindUserByEmail(ctx context.Context, email string) (userUUID uuid.UUID, status string, found bool, err error)
	// NOTE: 既存有効トークンの一括無効化・新発行・afterInsert(送信)は単一Tx。afterInsertがエラーを返すと
	// 無効化を含む全体をロールバックする（BUC-U07 E3・CND-18）
	IssuePasswordResetToken(ctx context.Context, userUUID uuid.UUID, token domain.PasswordResetToken, afterInsert func(context.Context) error) error
}

type RequestPasswordResetHandler struct {
	repo    PasswordResetRequestRepository
	limiter RateLimiter
	mailer  Mailer
}

func NewRequestPasswordResetHandler(repo PasswordResetRequestRepository, limiter RateLimiter, mailer Mailer) *RequestPasswordResetHandler {
	return &RequestPasswordResetHandler{repo: repo, limiter: limiter, mailer: mailer}
}

func (h *RequestPasswordResetHandler) Handle(ctx context.Context, email string) error {
	logger := applog.FromContext(ctx).With("usecase", "UC-008", "ctx", "password_reset_request")
	logger.InfoContext(ctx, "usecase started")

	// NOTE: レート判定（E2）は形式検証・状態確認より先（UC-008 §3・VAR-12。一様適用で挙動差から状態を漏らさない）
	res, err := h.limiter.Allow(ctx, email)
	if err != nil {
		return fmt.Errorf("could not check rate limit: %w", err)
	}
	if !res.Allowed {
		logger.WarnContext(ctx, "パスワードリセット要求レートリミット超過")
		return &RateLimitedError{RetryAfter: res.RetryAfter}
	}

	if err := domain.ValidateEmail(email); err != nil {
		logger.WarnContext(ctx, "メールアドレス形式不正")
		return err
	}

	userUUID, status, found, err := h.repo.FindUserByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("could not look up user: %w", err)
	}
	// NOTE: 未登録・inactive以外（A1・Q-7確定=適格はSTM-01.未認証のみ）は一律200・メール送信なし（FR-08・列挙秘匿）
	if !found || status != domain.StatusInactive {
		logger.InfoContext(ctx, "usecase finished")
		return nil
	}

	plainToken, token, err := domain.NewPasswordResetToken()
	if err != nil {
		return fmt.Errorf("could not generate reset token: %w", err)
	}

	err = h.repo.IssuePasswordResetToken(ctx, userUUID, token, func(ctx context.Context) error {
		if sendErr := h.mailer.SendPasswordResetEmail(ctx, email, plainToken); sendErr != nil {
			return fmt.Errorf("%w: %v", ErrMailDeliveryFail, sendErr)
		}
		return nil
	})
	if errors.Is(err, ErrMailDeliveryFail) {
		// WARNING: メールアドレスを含みうるためエラー詳細はログに出さない
		logger.ErrorContext(ctx, "リセットメール送信失敗")
		return ErrMailDeliveryFail
	}
	if err != nil {
		return fmt.Errorf("could not issue reset token: %w", err)
	}

	logger.InfoContext(ctx, "usecase finished")
	return nil
}
