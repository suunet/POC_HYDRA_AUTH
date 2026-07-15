package command

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"poc-app-hydra/backend/auth/domain"
	applog "poc-app-hydra/backend/common/log"
)

var (
	ErrRateLimited      = errors.New("registration rate limit exceeded")
	ErrMailDeliveryFail = errors.New("could not send confirmation email")
)

type UserRepository interface {
	EmailExists(ctx context.Context, email string) (bool, error)
	// NOTE: 単一トランザクションで登録する。afterInsertはコミット前に呼ばれ、エラーを返すと登録・トークン保存ごとロールバックする
	CreateUser(ctx context.Context, r domain.Registration, token domain.EmailConfirmationToken, afterInsert func(context.Context) error) error
}

type RateLimiter interface {
	Allow(ctx context.Context, key string) (bool, error)
}

type Mailer interface {
	SendConfirmationEmail(ctx context.Context, to, plainToken string) error
}

type RegisterAccountHandler struct {
	users   UserRepository
	limiter RateLimiter
	mailer  Mailer
}

func NewRegisterAccountHandler(users UserRepository, limiter RateLimiter, mailer Mailer) *RegisterAccountHandler {
	return &RegisterAccountHandler{users: users, limiter: limiter, mailer: mailer}
}

func (h *RegisterAccountHandler) Handle(ctx context.Context, email, password string) error {
	logger := applog.FromContext(ctx).With("usecase", "UC-002", "ctx", "user_registration")
	logger.InfoContext(ctx, "usecase started")

	allowed, err := h.limiter.Allow(ctx, email)
	if err != nil {
		return fmt.Errorf("could not check rate limit: %w", err)
	}
	if !allowed {
		logger.WarnContext(ctx, "登録レートリミット超過")
		return ErrRateLimited
	}

	if err := domain.ValidateEmail(email); err != nil {
		logger.WarnContext(ctx, "メールアドレス形式不正")
		return err
	}
	if err := domain.ValidatePassword(password); err != nil {
		logger.WarnContext(ctx, "パスワード強度不足")
		return err
	}

	exists, err := h.users.EmailExists(ctx, email)
	if err != nil {
		return fmt.Errorf("could not check email existence: %w", err)
	}
	if exists {
		logger.InfoContext(ctx, "usecase finished")
		return nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("could not hash password: %w", err)
	}

	registration := domain.Registration{
		UserUUID:     uuid.New(),
		Email:        email,
		PasswordHash: string(hash),
		Status:       domain.StatusMailUnverified,
		Role:         domain.RoleUser,
	}

	plainToken, token, err := domain.NewEmailConfirmationToken()
	if err != nil {
		return fmt.Errorf("could not generate confirmation token: %w", err)
	}

	err = h.users.CreateUser(ctx, registration, token, func(ctx context.Context) error {
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
		return fmt.Errorf("could not create user: %w", err)
	}

	logger.InfoContext(ctx, "usecase finished")
	return nil
}
