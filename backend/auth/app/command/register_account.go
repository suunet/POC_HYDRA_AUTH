package command

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"poc-app-hydra/backend/auth/domain"
	applog "poc-app-hydra/backend/common/log"
)

type UserRepository interface {
	EmailExists(ctx context.Context, email string) (bool, error)
	// CreateUser は単一トランザクションで登録する（失敗時は全体をロールバック）
	CreateUser(ctx context.Context, r domain.Registration, token domain.EmailConfirmationToken) error
}

type RegisterAccountHandler struct {
	users UserRepository
}

func NewRegisterAccountHandler(users UserRepository) *RegisterAccountHandler {
	return &RegisterAccountHandler{users: users}
}

func (h *RegisterAccountHandler) Handle(ctx context.Context, email, password string) error {
	logger := applog.FromContext(ctx).With("usecase", "UC-002", "ctx", "user_registration")
	logger.InfoContext(ctx, "usecase started")

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
		// 列挙攻撃対策のため登録処理を行わず成功扱いにする（201は呼び出し側が返す）
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

	_, token, err := domain.NewEmailConfirmationToken()
	if err != nil {
		return fmt.Errorf("could not generate confirmation token: %w", err)
	}

	if err := h.users.CreateUser(ctx, registration, token); err != nil {
		return fmt.Errorf("could not create user: %w", err)
	}

	// TODO: T-005 — レート制限（VAR-16）・メール送信（EVT-01）・送信失敗時のロールバック（E3）を実装する
	logger.InfoContext(ctx, "usecase finished")
	return nil
}
