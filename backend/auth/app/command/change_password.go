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

// ErrPasswordMismatch は UC-010 E2（現在のパスワード不一致・CND-16違反＝403 password-mismatch）。
// currentPasswordは形式検証せず、72バイト超等のbcrypt照合エラーも本エラーへ倒す（資格情報の検証失敗・安全側）
var ErrPasswordMismatch = errors.New("current password mismatch")

// UserCredentials はパスワード変更の判定に必要なユーザー属性（INF-01）。
type UserCredentials struct {
	PasswordHash string
	Status       string
}

// PasswordChangeRepository は UC-010 が必要とする最小の永続化操作（消費側IF）。
type PasswordChangeRepository interface {
	// GetUserCredentials は user_uuid でユーザーを検証読取する（削除済み除外）。未存在は found=false。
	GetUserCredentials(ctx context.Context, userUUID uuid.UUID) (UserCredentials, bool, error)
	// ChangePassword はパスワード更新と当該ユーザーの全リフレッシュトークン失効
	// （revocation_reason: RevocationReasonPasswordChanged）を単一Txで行う（FR-10・E3は全ロールバック）。
	ChangePassword(ctx context.Context, userUUID uuid.UUID, newPasswordHash string) error
}

type ChangePasswordHandler struct {
	users PasswordChangeRepository
}

func NewChangePasswordHandler(users PasswordChangeRepository) *ChangePasswordHandler {
	return &ChangePasswordHandler{users: users}
}

// Handle は UC-010（パスワードを変更する）: 新パスワード強度（E1）→ユーザー取得（E4）→
// 無効化確認（CND-04・E5）→現在パスワード照合（CND-16・E2）→単一Txで更新＋user単位全失効（E3）。
func (h *ChangePasswordHandler) Handle(ctx context.Context, authenticatedUserID, currentPassword, newPassword string) error {
	logger := applog.FromContext(ctx).With("usecase", "UC-010", "ctx", "password_change")
	logger.InfoContext(ctx, "usecase started")

	fail := func(msg string, cause error) error {
		logger.ErrorContext(ctx, msg, "error", cause)
		return fmt.Errorf("%s: %w", msg, cause)
	}

	// E1: 新パスワード強度（VAR-02・72バイト条項含む）
	if err := domain.ValidatePassword(newPassword); err != nil {
		logger.WarnContext(ctx, "パスワード強度不足")
		return err
	}

	// フロー4: ユーザー取得（削除済み除外・E4）。
	// NOTE: sub のUUID parse不能もE4と同一応答に倒す（存在情報を漏らさない。FR-19通過後のsubは自家発行UUIDのため実質到達不能）
	authUUID, parseErr := uuid.Parse(authenticatedUserID)
	if parseErr != nil {
		// 内部ログは事実どおり区別する（応答のみE4と同一化して存在情報を秘匿）
		logger.WarnContext(ctx, "不正な認証サブジェクトでの操作試行", "user_id", authenticatedUserID)
		return &SessionRevokedError{Reason: RevocationReasonAccountDeleted}
	}
	creds, found, err := h.users.GetUserCredentials(ctx, authUUID)
	if err != nil {
		return fail("could not look up user credentials", err)
	}
	if !found {
		logger.WarnContext(ctx, "削除済みアカウントの操作試行", "user_id", authenticatedUserID)
		return &SessionRevokedError{Reason: RevocationReasonAccountDeleted}
	}

	// フロー5: 無効化済みでない（CND-04・E5）
	if creds.Status == domain.StatusDisabled {
		logger.WarnContext(ctx, "無効化済みアカウントの操作試行", "user_id", authenticatedUserID)
		return &SessionRevokedError{Reason: RevocationReasonAccountDisabled}
	}

	// フロー6: 現在パスワード照合（CND-16・E2・domain.VerifyPassword経由=CND-02照合器と共通）。
	// 照合エラー（不一致・72バイト超等）は一律E2
	if err := domain.VerifyPassword(creds.PasswordHash, currentPassword); err != nil {
		logger.WarnContext(ctx, "現在のパスワード不一致", "user_id", authenticatedUserID)
		return ErrPasswordMismatch
	}

	// フロー7: 新パスワードのハッシュ化（NFR-01・コスト12）
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), domain.PasswordBcryptCost)
	if err != nil {
		return fail("could not hash new password", err)
	}

	// フロー8-9: 単一Txで更新＋user単位全失効（E3は全ロールバック）
	if err := h.users.ChangePassword(ctx, authUUID, string(newHash)); err != nil {
		logger.ErrorContext(ctx, "パスワード変更トランザクション失敗", "error", err)
		return fmt.Errorf("password change transaction failed: %w", err)
	}

	// フロー10: 監査ログ（NFR-07・target=user_id）
	logger.InfoContext(ctx, "パスワード変更", "user_id", authenticatedUserID)
	logger.InfoContext(ctx, "usecase finished", "user_id", authenticatedUserID)
	return nil
}
