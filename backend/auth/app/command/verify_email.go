package command

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"poc-app-hydra/backend/auth/domain"
	applog "poc-app-hydra/backend/common/log"
)

var (
	ErrInvalidToken = errors.New("invalid email confirmation token")
	ErrTokenExpired = errors.New("email confirmation token expired")
)

type EmailConfirmationTokenRepository interface {
	GetEmailConfirmationTokenByHash(ctx context.Context, hash string) (domain.EmailConfirmationTokenRecord, error)
	// NOTE: used_at更新とstatus遷移を単一トランザクションで行う。更新0行はErrTokenConsumeConflict
	ConsumeEmailConfirmationToken(ctx context.Context, tokenUUID, userUUID uuid.UUID) error
}

type VerifyEmailHandler struct {
	tokens  EmailConfirmationTokenRepository
	limiter RateLimiter
}

func NewVerifyEmailHandler(tokens EmailConfirmationTokenRepository, limiter RateLimiter) *VerifyEmailHandler {
	return &VerifyEmailHandler{tokens: tokens, limiter: limiter}
}

func (h *VerifyEmailHandler) Handle(ctx context.Context, plainToken, clientIP string) error {
	logger := applog.FromContext(ctx).With("usecase", "UC-003", "ctx", "email_verification")
	logger.InfoContext(ctx, "usecase started")

	// NOTE: レート判定（E4）はトークン照合より先（UC-003 §4の評価順序）
	res, err := h.limiter.Allow(ctx, clientIP)
	if err != nil {
		return fmt.Errorf("could not check rate limit: %w", err)
	}
	if !res.Allowed {
		logger.WarnContext(ctx, "メール確認レートリミット超過")
		return &RateLimitedError{RetryAfter: res.RetryAfter}
	}

	record, err := h.tokens.GetEmailConfirmationTokenByHash(ctx, domain.HashEmailConfirmationToken(plainToken))
	if errors.Is(err, domain.ErrTokenNotFound) {
		logger.WarnContext(ctx, "無効なメール確認トークン")
		return ErrInvalidToken
	}
	if err != nil {
		return fmt.Errorf("could not look up confirmation token: %w", err)
	}

	// NOTE: 使用済み判定を期限判定より先に倒す（トークンの実在を漏らさない・UC-003 §4）
	if record.Used() {
		logger.WarnContext(ctx, "無効なメール確認トークン")
		return ErrInvalidToken
	}
	if record.Expired(time.Now()) {
		logger.WarnContext(ctx, "メール確認トークン期限切れ")
		return ErrTokenExpired
	}

	if err := h.tokens.ConsumeEmailConfirmationToken(ctx, record.TokenUUID, record.UserUUID); err != nil {
		if errors.Is(err, domain.ErrTokenConsumeConflict) {
			logger.WarnContext(ctx, "無効なメール確認トークン")
			return ErrInvalidToken
		}
		return fmt.Errorf("could not consume confirmation token: %w", err)
	}

	logger.InfoContext(ctx, "usecase finished")
	return nil
}
