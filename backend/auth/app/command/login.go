package command

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"poc-app-hydra/backend/auth/domain"
	applog "poc-app-hydra/backend/common/log"
)

// ErrAuthenticationFailed はユーザー不存在（E3）とパスワード不一致（E4）を区別しない共通エラー（列挙防止）。
var ErrAuthenticationFailed = errors.New("authentication failed")

// LockedError はロックアウト中（E2）を表す。RetryAfter は解除までのTTL残（VAR-11）。
type LockedError struct {
	RetryAfter time.Duration
}

func (e *LockedError) Error() string { return "account locked" }

// LoginUser は認証に必要なユーザー情報（INF-01/02）。
type LoginUser struct {
	UserUUID     uuid.UUID
	PasswordHash string
	Status       string
	Roles        []string
}

// RefreshTokenRecord は永続化するリフレッシュトークン（INF-04・NFR-14: ハッシュのみ）。
type RefreshTokenRecord struct {
	TokenID   uuid.UUID
	UserUUID  uuid.UUID
	FamilyID  uuid.UUID
	TokenHash string
	ExpiresAt time.Time
}

type LoginUserRepository interface {
	// GetLoginUser は削除済みを除外して検索する。未存在は found=false（エラーにしない・E3を沈黙で扱う）。
	GetLoginUser(ctx context.Context, email string) (LoginUser, bool, error)
	SaveRefreshToken(ctx context.Context, r RefreshTokenRecord) error
}

// Lockout はログイン失敗カウント（INF-08）とロックアウト状態（INF-09）を管理する（NFR-03）。
type Lockout interface {
	Check(ctx context.Context, key string) (locked bool, retryAfter time.Duration, err error)
	RecordFailure(ctx context.Context, key string) (bool, error)
	Reset(ctx context.Context, key string) error
}

type LoginResult struct {
	AccessToken  string
	RefreshToken string
}

type LoginHandler struct {
	users      LoginUserRepository
	lockout    Lockout
	signingKey *rsa.PrivateKey
}

func NewLoginHandler(users LoginUserRepository, lockout Lockout, signingKey *rsa.PrivateKey) *LoginHandler {
	return &LoginHandler{users: users, lockout: lockout, signingKey: signingKey}
}

// Handle は UC-005 の検証順序（ロックアウト→検索→パスワード→メール確認→アカウント状態）で認証し、
// 成功時にアクセストークン（JWT RS256）とリフレッシュトークン（opaque）を発行する。
// email形式検証（E1）は呼び出し側（handler）で済ませてから渡す。
func (h *LoginHandler) Handle(ctx context.Context, email, password string) (LoginResult, error) {
	logger := applog.FromContext(ctx).With("usecase", "UC-005", "ctx", "login")
	logger.InfoContext(ctx, "usecase started")

	// E1: メールアドレス形式検証（VAR-01）。ロックアウト評価より前＝形式不正は評価順対象外
	if err := domain.ValidateEmail(email); err != nil {
		logger.WarnContext(ctx, "メールアドレス形式不正")
		return LoginResult{}, err
	}

	// フロー3: ロックアウト確認（E2・CND-05）。失敗カウントより先に手前で弾く
	locked, retryAfter, err := h.lockout.Check(ctx, email)
	if err != nil {
		return LoginResult{}, fmt.Errorf("could not check lockout: %w", err)
	}
	if locked {
		logger.WarnContext(ctx, "ロックアウト中のログイン試行")
		return LoginResult{}, &LockedError{RetryAfter: retryAfter}
	}

	// フロー4: ユーザー検索（削除済み除外）
	user, found, err := h.users.GetLoginUser(ctx, email)
	if err != nil {
		return LoginResult{}, fmt.Errorf("could not look up user: %w", err)
	}
	if !found {
		// E3: timing attack対策のダミー検証＋均一な失敗加算（ユーザー列挙防止・NFR-03）
		_ = domain.DummyPasswordVerify(password)
		if _, err := h.lockout.RecordFailure(ctx, email); err != nil {
			return LoginResult{}, fmt.Errorf("could not record login failure: %w", err)
		}
		logger.WarnContext(ctx, "ログイン失敗")
		return LoginResult{}, ErrAuthenticationFailed
	}

	// フロー5: パスワード検証（CND-02）
	if err := domain.VerifyPassword(user.PasswordHash, password); err != nil {
		// E4: user_id 付きで失敗を記録
		if _, rerr := h.lockout.RecordFailure(ctx, email); rerr != nil {
			return LoginResult{}, fmt.Errorf("could not record login failure: %w", rerr)
		}
		logger.WarnContext(ctx, "ログイン失敗", "user_id", user.UserUUID.String())
		return LoginResult{}, ErrAuthenticationFailed
	}

	// フロー6/7: メール確認済み・無効化済みでないことの確認（E5/E6・CND-03/04）
	if err := domain.CheckLoginableStatus(user.Status); err != nil {
		switch {
		case errors.Is(err, domain.ErrEmailNotVerified):
			logger.WarnContext(ctx, "メール未確認アカウントへのログイン試行", "user_id", user.UserUUID.String())
		case errors.Is(err, domain.ErrAccountDisabled):
			logger.WarnContext(ctx, "無効化済みアカウントへのログイン試行", "user_id", user.UserUUID.String())
		}
		return LoginResult{}, err
	}

	// フロー8: 失敗カウントをリセット
	if err := h.lockout.Reset(ctx, email); err != nil {
		return LoginResult{}, fmt.Errorf("could not reset failure count: %w", err)
	}

	// フロー9/10: トークン発行
	accessToken, err := domain.GenerateAccessToken(h.signingKey, user.UserUUID.String(), user.Roles, domain.AccessTokenTTL)
	if err != nil {
		return LoginResult{}, fmt.Errorf("could not issue access token: %w", err)
	}
	plainRefresh, hash, err := domain.GenerateRefreshToken()
	if err != nil {
		return LoginResult{}, fmt.Errorf("could not generate refresh token: %w", err)
	}
	if err := h.users.SaveRefreshToken(ctx, RefreshTokenRecord{
		TokenID:   uuid.New(),
		UserUUID:  user.UserUUID,
		FamilyID:  uuid.New(),
		TokenHash: hash,
		ExpiresAt: time.Now().Add(domain.RefreshTokenTTL),
	}); err != nil {
		return LoginResult{}, fmt.Errorf("could not save refresh token: %w", err)
	}

	logger.InfoContext(ctx, "usecase finished", "user_id", user.UserUUID.String())
	return LoginResult{AccessToken: accessToken, RefreshToken: plainRefresh}, nil
}
