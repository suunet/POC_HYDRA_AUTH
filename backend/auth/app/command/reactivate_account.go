package command

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"poc-app-hydra/backend/auth/domain"
	applog "poc-app-hydra/backend/common/log"
)

// ErrNotDisabled は UC-015 E3（対象が無効化済みでない＝409 account-not-disabled・CND-13違反）。
var ErrNotDisabled = errors.New("account is not disabled")

// ReactivateAccountRepository は UC-015 が必要とする最小の永続化操作（消費側IF）。
type ReactivateAccountRepository interface {
	// FindAccountForReactivate は user_uuid で対象（削除済み除外）のロールと状態を検証読取する。未存在は found=false。
	FindAccountForReactivate(ctx context.Context, userUUID uuid.UUID) (roles []string, status string, found bool, err error)
	// ReactivateAccount は再有効化（disabled→inactive）を遷移元ガード付きで行う。
	// 遷移0行（無効化済みでない・並行再有効化）は ErrNotDisabled を返す（E3へ合流）。
	ReactivateAccount(ctx context.Context, userUUID uuid.UUID) error
}

type ReactivateAccountHandler struct {
	repo ReactivateAccountRepository
}

func NewReactivateAccountHandler(repo ReactivateAccountRepository) *ReactivateAccountHandler {
	return &ReactivateAccountHandler{repo: repo}
}

// Handle は UC-015（管理者アカウントを再有効化する）: 対象読取（E1）→管理者ロール（E2）→
// 無効化済み（E3）→再有効化（disabled→inactive・遷移元ガード0行もE3）。セッションは作らない（FR-16）。
// M1/M2（AT検証・super_adminロール認可）はミドルウェア層（FR-19＋RequireRoles）が前段で担う。
func (h *ReactivateAccountHandler) Handle(ctx context.Context, operatorSub, targetID string) error {
	logger := applog.FromContext(ctx).With("usecase", "UC-015", "ctx", "account_reactivate")
	logger.InfoContext(ctx, "usecase started")

	targetUUID, err := uuid.Parse(targetID)
	if err != nil {
		// E1: UUID形式不正も不存在へ合流（形式差から存在有無を漏らさない・BUC-A05 E1備考）
		logger.WarnContext(ctx, "対象ユーザーが存在しない")
		return ErrUserNotFound
	}

	roles, status, found, err := h.repo.FindAccountForReactivate(ctx, targetUUID)
	if err != nil {
		// 外部依存失敗はERROR（NFR-08）。再有効化失敗と同じ観測性でinternal-error500へ倒す
		logger.ErrorContext(ctx, "アカウント再有効化の対象読取に失敗")
		return fmt.Errorf("could not look up account: %w", err)
	}
	if !found {
		logger.WarnContext(ctx, "対象ユーザーが存在しない")
		return ErrUserNotFound
	}

	isAdmin := false
	for _, r := range roles {
		if domain.IsAdminRole(r) {
			isAdmin = true
			break
		}
	}
	if !isAdmin {
		logger.WarnContext(ctx, "管理者ロール未保持のアカウントへの再有効化試行")
		return ErrNotAdminAccount
	}
	if status != domain.StatusDisabled {
		// E3: 読取チェック。読取〜遷移間の並行再有効化はTx内の遷移元ガード0行で二重に閉じる
		logger.WarnContext(ctx, "無効化されていないアカウントへの再有効化試行")
		return ErrNotDisabled
	}

	if err := h.repo.ReactivateAccount(ctx, targetUUID); err != nil {
		if errors.Is(err, ErrNotDisabled) {
			logger.WarnContext(ctx, "無効化されていないアカウントへの再有効化試行")
			return ErrNotDisabled
		}
		logger.ErrorContext(ctx, "アカウント再有効化に失敗")
		return fmt.Errorf("could not reactivate account: %w", err)
	}

	// 監査ログ（NFR-07・アカウント再有効化）。target/operatorともUUID＝NFR-09機密に非該当
	logger.InfoContext(ctx, "アカウント再有効化", "target_user_id", targetUUID.String(), "operator", operatorSub)
	logger.InfoContext(ctx, "usecase finished")
	return nil
}
