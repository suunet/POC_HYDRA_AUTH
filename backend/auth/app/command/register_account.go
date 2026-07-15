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
	// EmailExists は CND-01（メールアドレスが未登録であること）の照会
	EmailExists(ctx context.Context, email string) (bool, error)
	// CreateUser はユーザー（INF-01）とロール（INF-02）を単一トランザクションで登録する
	CreateUser(ctx context.Context, r domain.Registration) error
}

type RegisterAccountHandler struct {
	users UserRepository
}

func NewRegisterAccountHandler(users UserRepository) *RegisterAccountHandler {
	return &RegisterAccountHandler{users: users}
}

// Handle は UC-002（アカウントを登録する）の主成功シナリオ ステップ3〜7（サイクル4範囲）
func (h *RegisterAccountHandler) Handle(ctx context.Context, email, password string) error {
	logger := applog.FromContext(ctx).With("usecase", "UC-002", "ctx", "user_registration")
	logger.InfoContext(ctx, "usecase started")

	if err := domain.ValidateEmail(email); err != nil {
		logger.WarnContext(ctx, "メールアドレス形式不正") // E1（NFR-08: ビジネス例外=WARNING）
		return err
	}
	if err := domain.ValidatePassword(password); err != nil {
		logger.WarnContext(ctx, "パスワード強度不足") // E2
		return err
	}

	exists, err := h.users.EmailExists(ctx, email)
	if err != nil {
		return fmt.Errorf("could not check email existence: %w", err)
	}
	if exists {
		// A1: 列挙攻撃対策 — 登録処理を行わず成功扱い（201は呼び出し側）
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
		Status:       domain.StatusMailUnverified, // STM-01.メール未確認へ
		Role:         domain.RoleUser,             // VAR-08
	}
	if err := h.users.CreateUser(ctx, registration); err != nil {
		return fmt.Errorf("could not create user: %w", err)
	}

	// TODO: T-005 サイクル5 — ステップ2（VAR-16/INF-13レート制限・E4=429）と INF-06（メール確認トークン）生成・保存・EVT-01（メール送信）・E3ロールバック
	logger.InfoContext(ctx, "usecase finished")
	return nil
}
