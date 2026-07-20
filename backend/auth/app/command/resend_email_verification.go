package command

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"poc-app-hydra/backend/auth/domain"
	applog "poc-app-hydra/backend/common/log"
)

type ResendTokenRepository interface {
	FindUserByEmail(ctx context.Context, email string) (userUUID uuid.UUID, status string, found bool, err error)
	// NOTE: 既存無効化・新発行・afterInsert(送信)は単一Tx。afterInsertがエラーを返すと全体をロールバックする（E2の担保）
	ReissueEmailConfirmationToken(ctx context.Context, userUUID uuid.UUID, token domain.EmailConfirmationToken, afterInsert func(context.Context) error) error
}

type ResendEmailVerificationHandler struct {
	repo    ResendTokenRepository
	limiter RateLimiter
	mailer  Mailer
}

func NewResendEmailVerificationHandler(repo ResendTokenRepository, limiter RateLimiter, mailer Mailer) *ResendEmailVerificationHandler {
	return &ResendEmailVerificationHandler{repo: repo, limiter: limiter, mailer: mailer}
}

func (h *ResendEmailVerificationHandler) Handle(ctx context.Context, email string) error {
	logger := applog.FromContext(ctx).With("usecase", "UC-004", "ctx", "email_confirm_token_resend")
	logger.InfoContext(ctx, "usecase started")

	// NOTE: レート判定（E1）は状態確認より先（UC-004 §3・一様適用で挙動差から状態を漏らさない）
	res, err := h.limiter.Allow(ctx, email)
	if err != nil {
		return fmt.Errorf("could not check rate limit: %w", err)
	}
	if !res.Allowed {
		logger.WarnContext(ctx, "再送レートリミット超過")
		return &RateLimitedError{RetryAfter: res.RetryAfter}
	}

	userUUID, status, found, err := h.repo.FindUserByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("could not look up user: %w", err)
	}
	// NOTE: 未登録（A1）・mail_unverified以外（A2）は一律200・メール送信なし（FR-04・列挙秘匿）
	if !found || status != domain.StatusMailUnverified {
		logger.InfoContext(ctx, "usecase finished")
		return nil
	}

	plainToken, token, err := domain.NewEmailConfirmationToken()
	if err != nil {
		return fmt.Errorf("could not generate confirmation token: %w", err)
	}

	err = h.repo.ReissueEmailConfirmationToken(ctx, userUUID, token, func(ctx context.Context) error {
		if sendErr := h.mailer.SendConfirmationEmail(ctx, email, plainToken); sendErr != nil {
			return fmt.Errorf("%w: %v", ErrMailDeliveryFail, sendErr)
		}
		return nil
	})
	if errors.Is(err, ErrMailDeliveryFail) {
		// WARNING: メールアドレスを含みうるためエラー詳細はログに出さない
		logger.ErrorContext(ctx, "メール送信失敗")
		return ErrMailDeliveryFail
	}
	if err != nil {
		return fmt.Errorf("could not reissue confirmation token: %w", err)
	}

	logger.InfoContext(ctx, "usecase finished")
	return nil
}
