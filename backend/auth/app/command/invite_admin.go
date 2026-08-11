package command

import (
	"context"
	"errors"
	"fmt"

	"poc-app-hydra/backend/auth/domain"
	applog "poc-app-hydra/backend/common/log"
)

// ErrRoleAlreadyAssigned は UC-011 E4（管理者ロール付与済み・CND-11違反＝409 role-already-assigned）。
var ErrRoleAlreadyAssigned = errors.New("admin role already assigned")

// AdminInvitationRepository は UC-011 が必要とする最小の永続化操作（消費側IF）。
type AdminInvitationRepository interface {
	// FindUserRolesByEmail はメールアドレスでユーザー（削除済み除外）とロール一覧を検証読取する。未存在は found=false。
	FindUserRolesByEmail(ctx context.Context, email string) (roles []string, found bool, err error)
	// NOTE: 既存有効トークンの一括無効化・新発行・afterInsert(送信)は単一Tx。afterInsertがエラーを返すと
	// 無効化を含む全体をロールバックする（BUC-A01 E5・CND-19）
	IssueInvitationToken(ctx context.Context, email, role string, token domain.InvitationToken, afterInsert func(context.Context) error) error
}

type InviteAdminHandler struct {
	repo    AdminInvitationRepository
	limiter RateLimiter
	mailer  Mailer
}

func NewInviteAdminHandler(repo AdminInvitationRepository, limiter RateLimiter, mailer Mailer) *InviteAdminHandler {
	return &InviteAdminHandler{repo: repo, limiter: limiter, mailer: mailer}
}

// Handle は UC-011（管理者を招待する）: レート（E3・業務評価の先頭）→形式（E1）→ロール（E2）→
// CND-11判定（E4=ロール付与済み・E6=既存アカウント）→発行Tx内送信（A1/E5）。
// M1/M2（AT検証・super_adminロール認可）はミドルウェア層（FR-19＋RequireRoles）が前段で担う。
func (h *InviteAdminHandler) Handle(ctx context.Context, email, role string) error {
	logger := applog.FromContext(ctx).With("usecase", "UC-011", "ctx", "admin_invitation")
	logger.InfoContext(ctx, "usecase started")

	// NOTE: レート判定（E3）は形式検証・存在確認より先（UC-011 §3・VAR-14。一様適用で挙動差から状態を漏らさない）
	res, err := h.limiter.Allow(ctx, email)
	if err != nil {
		return fmt.Errorf("could not check rate limit: %w", err)
	}
	if !res.Allowed {
		logger.WarnContext(ctx, "招待リクエストレートリミット超過")
		return &RateLimitedError{RetryAfter: res.RetryAfter}
	}

	if err := domain.ValidateEmail(email); err != nil {
		logger.WarnContext(ctx, "メールアドレス形式不正")
		return err
	}
	if err := domain.ValidateAdminRole(role); err != nil {
		logger.WarnContext(ctx, "無効なロール指定")
		return err
	}

	roles, found, err := h.repo.FindUserRolesByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("could not look up user: %w", err)
	}
	if found {
		// CND-11: 管理者ロール付与済み（E4）を既存アカウント（E6）より先に判定（付与済み⊂実在のため専用応答を優先）
		for _, r := range roles {
			if domain.IsAdminRole(r) {
				logger.WarnContext(ctx, "管理者ロール付与済み")
				return ErrRoleAlreadyAssigned
			}
		}
		logger.WarnContext(ctx, "既存アカウントへの招待")
		return domain.ErrEmailAlreadyRegistered
	}

	plainToken, token, err := domain.NewInvitationToken()
	if err != nil {
		return fmt.Errorf("could not generate invitation token: %w", err)
	}

	err = h.repo.IssueInvitationToken(ctx, email, role, token, func(ctx context.Context) error {
		if sendErr := h.mailer.SendInvitationEmail(ctx, email, plainToken); sendErr != nil {
			return fmt.Errorf("%w: %v", ErrMailDeliveryFail, sendErr)
		}
		return nil
	})
	if errors.Is(err, ErrMailDeliveryFail) {
		// WARNING: メールアドレスを含みうるためエラー詳細はログに出さない
		logger.ErrorContext(ctx, "招待メール送信失敗")
		return ErrMailDeliveryFail
	}
	if err != nil {
		return fmt.Errorf("could not issue invitation token: %w", err)
	}

	// 監査ログ（NFR-07・管理者招待）。NOTE: メールアドレスは含めずトークンIDで記録する（NFR-09・BUC-A01備考）
	logger.InfoContext(ctx, "管理者招待", "invitation_token_id", token.TokenUUID.String())
	logger.InfoContext(ctx, "usecase finished")
	return nil
}
