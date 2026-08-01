package command

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"poc-app-hydra/backend/auth/domain"
	applog "poc-app-hydra/backend/common/log"
)

// ErrRefreshTokenFormatInvalid は UC-007 E1（形式不正＝400 validation-error）。
// NOTE: UC-006のE1（401 invalid-token）とは応答が異なるため別sentinel。本UCではRTは資格情報でなく
// 入力ペイロード（資格情報はアクセストークン＝FR-19で検証済み）のため入力バリデーション違反として扱う
var ErrRefreshTokenFormatInvalid = errors.New("refresh token format invalid")

// LogoutTokenRepository は UC-007 が必要とする最小の永続化操作（消費側IF）。
// 実装は RefreshTokenRepository と同じ具象（UserRepository）が満たす。
type LogoutTokenRepository interface {
	// GetRefreshTokenByHash は SHA-256ハッシュで対象トークンを検証読取する。未存在は found=false。
	GetRefreshTokenByHash(ctx context.Context, hash string) (StoredRefreshToken, bool, error)
	// RevokeRefreshToken は当該トークンのみ失効する（既失効は更新0行の冪等・reason=nilでNULL）。
	RevokeRefreshToken(ctx context.Context, tokenID uuid.UUID, reason *string) error
}

type LogoutHandler struct {
	tokens LogoutTokenRepository
}

func NewLogoutHandler(tokens LogoutTokenRepository) *LogoutHandler {
	return &LogoutHandler{tokens: tokens}
}

// Handle は UC-007（ログアウトする）: 提示されたリフレッシュトークン1本のみを冪等に失効する（FR-07）。
// 不存在（A1）・所有者不一致（E2）でもエラーを返さない（200同一応答＝存在有無・所有情報を漏洩しない）。
// いかなる状態のトークン提示でも再利用検知（NFR-10）は発火しない（失効要求は使用ではない・FR-07）。
func (h *LogoutHandler) Handle(ctx context.Context, plainToken string, authenticatedUserID string) error {
	logger := applog.FromContext(ctx).With("usecase", "UC-007", "ctx", "logout")
	logger.InfoContext(ctx, "usecase started")

	fail := func(msg string, cause error) error {
		logger.ErrorContext(ctx, msg, "error", cause)
		return fmt.Errorf("%s: %w", msg, cause)
	}

	// E1: 形式検証（空トークンは不正・判定基準はUC-006 E1と同一）
	if plainToken == "" {
		logger.WarnContext(ctx, "リフレッシュトークン形式不正")
		return ErrRefreshTokenFormatInvalid
	}

	// フロー4: 検索（不存在のみA1＝冪等成功。既失効・期限切れ・使用済みは「見つかる」ため続行）
	stored, found, err := h.tokens.GetRefreshTokenByHash(ctx, domain.HashRefreshToken(plainToken))
	if err != nil {
		return fail("could not look up refresh token", err)
	}
	if !found {
		logger.InfoContext(ctx, "usecase finished", "user_id", authenticatedUserID)
		return nil
	}

	// フロー5: 所有権確認（E2）。不一致は失効せず正常応答と同一（所有情報を漏洩しない）。
	// NOTE: subはUUID正規形へparseして比較する（表記ゆれによる正当所有者の誤判定を防ぐ。parse不能はE2扱い＝安全側）
	authUUID, parseErr := uuid.Parse(authenticatedUserID)
	if parseErr != nil || stored.UserUUID != authUUID {
		logger.WarnContext(ctx, "他ユーザーのリフレッシュトークンによるログアウト試行",
			"user_id", authenticatedUserID)
		logger.InfoContext(ctx, "usecase finished", "user_id", authenticatedUserID)
		return nil
	}

	// フロー6: 当該1本のみ冪等失効（revoked_at記録・reason NULL＝FR-07・既失効は更新0行）
	if err := h.tokens.RevokeRefreshToken(ctx, stored.TokenID, nil); err != nil {
		return fail("could not revoke refresh token", err)
	}

	logger.InfoContext(ctx, "usecase finished", "user_id", authenticatedUserID)
	return nil
}
