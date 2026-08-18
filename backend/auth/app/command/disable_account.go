package command

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"poc-app-hydra/backend/auth/domain"
	applog "poc-app-hydra/backend/common/log"
)

var (
	// ErrUserNotFound は UC-014 E1（対象不存在＝404 user-not-found。UUID形式不正も合流）。
	ErrUserNotFound = errors.New("target user not found")
	// ErrNotAdminAccount は UC-014 E2（管理者ロール未保持＝400 not-admin-account）。
	ErrNotAdminAccount = errors.New("target is not an admin account")
	// ErrAlreadyDisabled は UC-014 E3（既に無効化済み＝409 account-already-disabled）。
	ErrAlreadyDisabled = errors.New("account already disabled")
	// ErrLastSuperAdmin は UC-014 E4（最後の稼働中super_admin＝409 last-super-admin・CND-14）。
	ErrLastSuperAdmin = errors.New("cannot disable the last active super_admin")
)

// DisableAccountRepository は UC-014 が必要とする最小の永続化操作（消費側IF）。
type DisableAccountRepository interface {
	// FindAccountForDisable は user_uuid で対象（削除済み除外）のロールと状態を検証読取する。未存在は found=false。
	FindAccountForDisable(ctx context.Context, userUUID uuid.UUID) (roles []string, status string, found bool, err error)
	// DisableAccount は無効化（inactive→disabled）と全RT失効（account_disabled）を単一Txで行う。
	// isSuperAdmin時はCND-14計数（稼働中super_admin行のFOR UPDATE）を同一Tx境界で評価し、
	// 1人以下なら ErrLastSuperAdmin を返す。遷移元ガード0行（並行無効化）は ErrAlreadyDisabled。
	DisableAccount(ctx context.Context, userUUID uuid.UUID, isSuperAdmin bool) error
}

type DisableAccountHandler struct {
	repo DisableAccountRepository
}

func NewDisableAccountHandler(repo DisableAccountRepository) *DisableAccountHandler {
	return &DisableAccountHandler{repo: repo}
}

// Handle は UC-014（管理者アカウントを無効化する）: 対象読取（E1）→管理者ロール（E2）→
// 未無効化（E3）→無効化Tx内でCND-14計数（E4）＋無効化＋全RT失効（E5）。
// M1/M2（AT検証・super_adminロール認可）はミドルウェア層（FR-19＋RequireRoles）が前段で担う。
func (h *DisableAccountHandler) Handle(ctx context.Context, operatorSub, targetID string) error {
	logger := applog.FromContext(ctx).With("usecase", "UC-014", "ctx", "account_disable")
	logger.InfoContext(ctx, "usecase started")

	targetUUID, err := uuid.Parse(targetID)
	if err != nil {
		// E1: UUID形式不正も不存在へ合流（形式差から存在有無を漏らさない・BUC-A04 E1備考）
		logger.WarnContext(ctx, "対象ユーザーが存在しない")
		return ErrUserNotFound
	}

	roles, status, found, err := h.repo.FindAccountForDisable(ctx, targetUUID)
	if err != nil {
		// 外部依存失敗はERROR（NFR-08）。Tx失敗と同じ観測性でinternal-error500へ倒す
		logger.ErrorContext(ctx, "アカウント無効化の対象読取に失敗")
		return fmt.Errorf("could not look up account: %w", err)
	}
	if !found {
		logger.WarnContext(ctx, "対象ユーザーが存在しない")
		return ErrUserNotFound
	}

	isAdmin, isSuperAdmin := false, false
	for _, r := range roles {
		if domain.IsAdminRole(r) {
			isAdmin = true
		}
		if r == domain.RoleSuperAdmin {
			isSuperAdmin = true
		}
	}
	if !isAdmin {
		logger.WarnContext(ctx, "管理者ロール未保持のアカウントへの無効化試行")
		return ErrNotAdminAccount
	}
	if status == domain.StatusDisabled {
		// E3: 読取チェック。読取〜遷移間の並行無効化はTx内の遷移元ガード0行で二重に閉じる
		logger.WarnContext(ctx, "既に無効化済みのアカウント")
		return ErrAlreadyDisabled
	}

	if err := h.repo.DisableAccount(ctx, targetUUID, isSuperAdmin); err != nil {
		switch {
		case errors.Is(err, ErrLastSuperAdmin):
			logger.WarnContext(ctx, "最後のsuper_adminの無効化試行")
			return ErrLastSuperAdmin
		case errors.Is(err, ErrAlreadyDisabled):
			logger.WarnContext(ctx, "既に無効化済みのアカウント")
			return ErrAlreadyDisabled
		default:
			logger.ErrorContext(ctx, "アカウント無効化トランザクション失敗")
			return fmt.Errorf("could not disable account: %w", err)
		}
	}

	// 監査ログ（NFR-07・アカウント無効化）。target/operatorともUUID＝NFR-09機密に非該当
	logger.InfoContext(ctx, "アカウント無効化", "target_user_id", targetUUID.String(), "operator", operatorSub)
	logger.InfoContext(ctx, "usecase finished")
	return nil
}
