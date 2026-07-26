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

// UC-006 のトークン検証エラー（E1/E2＝invalid-token・E3＝token-expired）。
var (
	ErrInvalidRefreshToken = errors.New("invalid refresh token") // E1形式不正・E2不存在
	ErrRefreshTokenExpired = errors.New("refresh token expired") // E3
)

// SessionRevokedError はセッション失効（E4再利用検知・E5削除・E6無効化）を表す。
// Reason は VAR-10 の失効理由コード（token_reuse_detected/account_deleted/account_disabled）。
type SessionRevokedError struct {
	Reason string
}

func (e *SessionRevokedError) Error() string { return "session revoked: " + e.Reason }

// StoredRefreshToken は DB上のリフレッシュトークン（INF-04）。used_at/revoked_at は nullable。
type StoredRefreshToken struct {
	TokenID   uuid.UUID
	UserUUID  uuid.UUID
	FamilyID  uuid.UUID
	ExpiresAt time.Time
	UsedAt    *time.Time
	RevokedAt *time.Time
}

// RefreshUser はトークンに紐付くユーザー（INF-01/02・再発行時点のロール）。
type RefreshUser struct {
	UserUUID uuid.UUID
	Status   string
	Roles    []string
}

// RotationResult はテスト観測用（ローテーションの結果）。
type RotationResult struct {
	OldTokenID  uuid.UUID
	NewFamilyID uuid.UUID
}

// RefreshTokenRepository。ローテーション（旧used_at＋新挿入）は単一Txをrepo実装が内包する（Q-3）。
type RefreshTokenRepository interface {
	// GetRefreshTokenForUpdate は SHA-256ハッシュで検索し対象行を施錠する（Q-5）。未存在は found=false。
	GetRefreshTokenForUpdate(ctx context.Context, hash string) (StoredRefreshToken, bool, error)
	GetUserForRefresh(ctx context.Context, userUUID uuid.UUID) (RefreshUser, bool, error)
	// RotateRefreshToken は旧トークンに used_at を記録し新トークンを挿入する（単一Tx・Q-3）。
	RotateRefreshToken(ctx context.Context, oldTokenID uuid.UUID, newToken RefreshTokenRecord) error
	RevokeRefreshToken(ctx context.Context, tokenID uuid.UUID, reason *string) error
	RevokeRefreshTokenFamily(ctx context.Context, familyID uuid.UUID, reason string) error
}

type RefreshResult struct {
	AccessToken  string
	RefreshToken string
}

type RefreshTokenHandler struct {
	tokens     RefreshTokenRepository
	signingKey *rsa.PrivateKey
}

func NewRefreshTokenHandler(tokens RefreshTokenRepository, signingKey *rsa.PrivateKey) *RefreshTokenHandler {
	return &RefreshTokenHandler{tokens: tokens, signingKey: signingKey}
}

// Handle は UC-006 の検証順序（形式→検索〔施錠〕→期限→未使用〔再利用検知〕→ユーザー→status）で
// リフレッシュトークンを検証し、成功時にローテーション（新アクセス/リフレッシュトークン）を行う。
func (h *RefreshTokenHandler) Handle(ctx context.Context, plainToken string) (RefreshResult, error) {
	logger := applog.FromContext(ctx).With("usecase", "UC-006", "ctx", "token_refresh")
	logger.InfoContext(ctx, "usecase started")

	fail := func(msg string, cause error) (RefreshResult, error) {
		logger.ErrorContext(ctx, msg, "error", cause)
		return RefreshResult{}, fmt.Errorf("%s: %w", msg, cause)
	}

	// E1: 形式検証（空トークンは不正）
	if plainToken == "" {
		logger.WarnContext(ctx, "リフレッシュトークン形式不正")
		return RefreshResult{}, ErrInvalidRefreshToken
	}

	// フロー3: 検索＋施錠（Q-5）
	stored, found, err := h.tokens.GetRefreshTokenForUpdate(ctx, domain.HashRefreshToken(plainToken))
	if err != nil {
		return fail("could not look up refresh token", err)
	}
	if !found {
		// E2: 不存在
		logger.WarnContext(ctx, "不正トークンでのアクセス試行")
		return RefreshResult{}, ErrInvalidRefreshToken
	}

	// フロー4: 有効期限（E3）。BUC-U05評価順: 期限→未使用
	if stored.ExpiresAt.Before(time.Now()) {
		if rerr := h.tokens.RevokeRefreshToken(ctx, stored.TokenID, nil); rerr != nil { // E3: revocation_reasonはnull（VAR-10に期限切れコード無し）
			return fail("could not revoke expired token", rerr)
		}
		logger.WarnContext(ctx, "リフレッシュトークン有効期限切れ", "user_id", stored.UserUUID.String())
		return RefreshResult{}, ErrRefreshTokenExpired
	}

	// フロー5: 未使用確認（E4再利用検知）。UC-006 §3-5は used_at のみだが、既に revoked のトークン
	// （forced_revocation/E5/E6等で失効済み）の再提示も窃取兆候として fail-safe に family一括失効へ倒す
	// （独自判断・§4記録。安全側＝正当セッションは既に別トークンへローテーション済みで実害小）
	if stored.UsedAt != nil || stored.RevokedAt != nil {
		// E4: 同一familyを一括失効（Q-2/NFR-14）
		if rerr := h.tokens.RevokeRefreshTokenFamily(ctx, stored.FamilyID, "token_reuse_detected"); rerr != nil {
			return fail("could not revoke token family", rerr)
		}
		logger.Log(ctx, applog.LevelCritical, "リフレッシュトークン再利用検知・当該family（チェーン）のセッション無効化",
			"user_id", stored.UserUUID.String(), "family_id", stored.FamilyID.String())
		return RefreshResult{}, &SessionRevokedError{Reason: "token_reuse_detected"}
	}

	// フロー6: ユーザー取得（削除済み除外・E5）
	user, ok, err := h.tokens.GetUserForRefresh(ctx, stored.UserUUID)
	if err != nil {
		return fail("could not look up user", err)
	}
	if !ok {
		// E5: 削除済み
		if rerr := h.tokens.RevokeRefreshToken(ctx, stored.TokenID, strptr("account_deleted")); rerr != nil {
			return fail("could not revoke token", rerr)
		}
		logger.WarnContext(ctx, "削除済みアカウントのトークン再発行試行", "user_id", stored.UserUUID.String())
		return RefreshResult{}, &SessionRevokedError{Reason: "account_deleted"}
	}

	// フロー7: 無効化済みでない（E6・CND-04）
	if user.Status == domain.StatusDisabled {
		if rerr := h.tokens.RevokeRefreshToken(ctx, stored.TokenID, strptr("account_disabled")); rerr != nil {
			return fail("could not revoke token", rerr)
		}
		logger.WarnContext(ctx, "無効化済みアカウントのトークン再発行試行", "user_id", stored.UserUUID.String())
		return RefreshResult{}, &SessionRevokedError{Reason: "account_disabled"}
	}

	// フロー8-9: ローテーション（旧used_at＋新挿入を単一Tx・Q-3）
	plainRefresh, hash, err := domain.GenerateRefreshToken()
	if err != nil {
		return fail("could not generate refresh token", err)
	}
	newToken := RefreshTokenRecord{
		TokenID:       uuid.New(),
		UserUUID:      stored.UserUUID,
		FamilyID:      stored.FamilyID, // 同一family（ローテーションチェーン）
		ParentTokenID: &stored.TokenID, // NFR-14: 旧トークンを親として記録（チェーン追跡）
		TokenHash:     hash,
		ExpiresAt:     time.Now().Add(domain.RefreshTokenTTL),
	}
	if err := h.tokens.RotateRefreshToken(ctx, stored.TokenID, newToken); err != nil {
		return fail("could not rotate refresh token", err)
	}

	// フロー10: アクセストークン（再発行時点のロール）
	accessToken, err := domain.GenerateAccessToken(h.signingKey, stored.UserUUID.String(), user.Roles, domain.AccessTokenTTL)
	if err != nil {
		return fail("could not issue access token", err)
	}

	logger.InfoContext(ctx, "usecase finished", "user_id", stored.UserUUID.String())
	return RefreshResult{AccessToken: accessToken, RefreshToken: plainRefresh}, nil
}

func strptr(s string) *string { return &s }
