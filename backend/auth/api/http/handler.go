package http

import (
	"context"
	"errors"
	"math"
	"net/http"

	"github.com/labstack/echo/v4"

	"poc-app-hydra/backend/auth/app/command"
	"poc-app-hydra/backend/auth/domain"
	commonhttp "poc-app-hydra/backend/common/http"
)

type Handler struct {
	register          *command.RegisterAccountHandler
	verify            *command.VerifyEmailHandler
	resend            *command.ResendEmailVerificationHandler
	login             *command.LoginHandler
	refresh           *command.RefreshTokenHandler
	logout            *command.LogoutHandler
	changePassword    *command.ChangePasswordHandler
	requestReset      *command.RequestPasswordResetHandler
	confirmReset      *command.ConfirmPasswordResetHandler
	inviteAdmin       *command.InviteAdminHandler
	acceptInvite      *command.AcceptInvitationHandler
	disableAccount    *command.DisableAccountHandler
	reactivateAccount *command.ReactivateAccountHandler
}

func NewHandler(register *command.RegisterAccountHandler, verify *command.VerifyEmailHandler, resend *command.ResendEmailVerificationHandler, login *command.LoginHandler, refresh *command.RefreshTokenHandler, logout *command.LogoutHandler, changePassword *command.ChangePasswordHandler, requestReset *command.RequestPasswordResetHandler, confirmReset *command.ConfirmPasswordResetHandler, inviteAdmin *command.InviteAdminHandler, acceptInvite *command.AcceptInvitationHandler, disableAccount *command.DisableAccountHandler, reactivateAccount *command.ReactivateAccountHandler) *Handler {
	return &Handler{register: register, verify: verify, resend: resend, login: login, refresh: refresh, logout: logout, changePassword: changePassword, requestReset: requestReset, confirmReset: confirmReset, inviteAdmin: inviteAdmin, acceptInvite: acceptInvite, disableAccount: disableAccount, reactivateAccount: reactivateAccount}
}

func (h *Handler) RegisterAccount(ctx context.Context, req RegisterAccountRequestObject) (RegisterAccountResponseObject, error) {
	if req.Body == nil {
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "リクエストボディが必要です")
	}

	err := h.register.Handle(ctx, string(req.Body.Email), req.Body.Password)
	var rateLimited *command.RateLimitedError
	switch {
	case err == nil:
		return RegisterAccount201Response{}, nil
	case errors.Is(err, domain.ErrInvalidEmail):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "メールアドレスの形式が不正です")
	case errors.Is(err, domain.ErrInvalidPassword):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "パスワードは15〜64文字で指定してください")
	case errors.As(err, &rateLimited):
		problem := commonhttp.NewProblemError(http.StatusTooManyRequests, "rate-limit-exceeded", "登録リクエストが多すぎます")
		// NOTE: 秒への切り上げで早すぎる再試行を防ぐ（VAR-16）
		return nil, problem.WithRetryAfter(int(math.Ceil(rateLimited.RetryAfter.Seconds())))
	case errors.Is(err, command.ErrMailDeliveryFail):
		return nil, commonhttp.NewProblemError(http.StatusServiceUnavailable, "mail-delivery-error", "確認メールの送信に失敗しました")
	default:
		return nil, err
	}
}

func (h *Handler) VerifyEmail(ctx context.Context, req VerifyEmailRequestObject) (VerifyEmailResponseObject, error) {
	if req.Body == nil {
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "リクエストボディが必要です")
	}

	// NOTE: openapiのminLength:1をバインダは検証しないため必須検査をここで行う（E4より前＝形式不正は評価順対象外）
	if req.Body.Token == "" {
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "トークンが必要です")
	}

	err := h.verify.Handle(ctx, req.Body.Token, commonhttp.ClientIPFromContext(ctx))
	var verifyRateLimited *command.RateLimitedError
	switch {
	case err == nil:
		return VerifyEmail200Response{}, nil
	case errors.Is(err, command.ErrInvalidToken):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "invalid-token", "メール確認トークンが無効です")
	case errors.Is(err, command.ErrTokenExpired):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "token-expired", "メール確認トークンの有効期限が切れています")
	case errors.As(err, &verifyRateLimited):
		problem := commonhttp.NewProblemError(http.StatusTooManyRequests, "rate-limit-exceeded", "メール確認リクエストが多すぎます")
		return nil, problem.WithRetryAfter(int(math.Ceil(verifyRateLimited.RetryAfter.Seconds())))
	default:
		return nil, err
	}
}

func (h *Handler) ResendEmailVerification(ctx context.Context, req ResendEmailVerificationRequestObject) (ResendEmailVerificationResponseObject, error) {
	if req.Body == nil {
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "リクエストボディが必要です")
	}

	err := h.resend.Handle(ctx, string(req.Body.Email))
	var rateLimited *command.RateLimitedError
	switch {
	case err == nil:
		return ResendEmailVerification200Response{}, nil
	case errors.As(err, &rateLimited):
		problem := commonhttp.NewProblemError(http.StatusTooManyRequests, "rate-limit-exceeded", "メール確認リクエストが多すぎます")
		return nil, problem.WithRetryAfter(int(math.Ceil(rateLimited.RetryAfter.Seconds())))
	case errors.Is(err, command.ErrMailDeliveryFail):
		return nil, commonhttp.NewProblemError(http.StatusServiceUnavailable, "mail-delivery-error", "確認メールの送信に失敗しました")
	default:
		return nil, err
	}
}

func (h *Handler) RequestPasswordReset(ctx context.Context, req RequestPasswordResetRequestObject) (RequestPasswordResetResponseObject, error) {
	if req.Body == nil {
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "リクエストボディが必要です")
	}

	err := h.requestReset.Handle(ctx, string(req.Body.Email))
	var rateLimited *command.RateLimitedError
	switch {
	case err == nil:
		return RequestPasswordReset200Response{}, nil
	case errors.As(err, &rateLimited):
		problem := commonhttp.NewProblemError(http.StatusTooManyRequests, "rate-limit-exceeded", "パスワードリセット要求が多すぎます")
		return nil, problem.WithRetryAfter(int(math.Ceil(rateLimited.RetryAfter.Seconds())))
	case errors.Is(err, domain.ErrInvalidEmail):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "メールアドレスの形式が不正です")
	case errors.Is(err, command.ErrMailDeliveryFail):
		return nil, commonhttp.NewProblemError(http.StatusServiceUnavailable, "mail-delivery-error", "リセットメールの送信に失敗しました")
	default:
		return nil, err
	}
}

func (h *Handler) AcceptInvitation(ctx context.Context, req AcceptInvitationRequestObject) (AcceptInvitationResponseObject, error) {
	if req.Body == nil {
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "リクエストボディが必要です")
	}
	// NOTE: openapiのminLength:1をバインダは検証しないため必須検査をここで行う（VerifyEmailと同型）
	if req.Body.Token == "" {
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "トークンが必要です")
	}

	err := h.acceptInvite.Handle(ctx, req.Body.Token, req.Body.Password)
	switch {
	case err == nil:
		return AcceptInvitation200Response{}, nil
	case errors.Is(err, domain.ErrInvalidPassword):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "パスワードは15〜64文字で指定してください")
	case errors.Is(err, command.ErrInvalidInvitationToken):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "invalid-token", "無効な招待トークン")
	case errors.Is(err, command.ErrInvitationTokenExpired):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "token-expired", "招待トークンの有効期限が切れています")
	case errors.Is(err, domain.ErrEmailAlreadyRegistered):
		return nil, commonhttp.NewProblemError(http.StatusConflict, "email-already-registered", "このメールアドレスのアカウントは既に存在します")
	default:
		// E5ほか未分類の内部失敗はUC-012指定のinternal-error（Tx失敗はUseCaseが全ロールバック・ERROR記録済み）
		return nil, commonhttp.NewProblemError(http.StatusInternalServerError, "internal-error", "サーバ内部エラーが発生しました")
	}
}

func (h *Handler) InviteAdmin(ctx context.Context, req InviteAdminRequestObject) (InviteAdminResponseObject, error) {
	if req.Body == nil {
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "リクエストボディが必要です")
	}

	err := h.inviteAdmin.Handle(ctx, string(req.Body.Email), req.Body.Role)
	var rateLimited *command.RateLimitedError
	switch {
	case err == nil:
		return InviteAdmin200Response{}, nil
	case errors.As(err, &rateLimited):
		problem := commonhttp.NewProblemError(http.StatusTooManyRequests, "rate-limit-exceeded", "招待リクエストが多すぎます")
		return nil, problem.WithRetryAfter(int(math.Ceil(rateLimited.RetryAfter.Seconds())))
	case errors.Is(err, domain.ErrInvalidEmail):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "メールアドレスの形式が不正です")
	case errors.Is(err, domain.ErrInvalidAdminRole):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "指定できるロールは管理者ロールのみです")
	case errors.Is(err, command.ErrRoleAlreadyAssigned):
		return nil, commonhttp.NewProblemError(http.StatusConflict, "role-already-assigned", "既に管理者ロールが付与されています")
	case errors.Is(err, domain.ErrEmailAlreadyRegistered):
		return nil, commonhttp.NewProblemError(http.StatusConflict, "email-already-registered", "このメールアドレスのアカウントは既に存在します")
	case errors.Is(err, command.ErrMailDeliveryFail):
		return nil, commonhttp.NewProblemError(http.StatusServiceUnavailable, "mail-delivery-error", "招待メールの送信に失敗しました")
	default:
		return nil, err
	}
}

func (h *Handler) DisableAdminAccount(ctx context.Context, req DisableAdminAccountRequestObject) (DisableAdminAccountResponseObject, error) {
	// M1/M2はミドルウェア（FR-19＋RequireRoles）が担うが、配線欠落時はfail-closedで401一様
	// （ヘッダなし＝到達はRegister配線のバグ）。操作者sub（監査のoperator）もここで取り出す
	claims, ok := commonhttp.AuthClaimsFromContext(ctx)
	if !ok {
		return nil, commonhttp.NewProblemError(http.StatusUnauthorized, "invalid-token", "アクセストークンが無効です")
	}

	err := h.disableAccount.Handle(ctx, claims.UserID, req.UserId)
	switch {
	case err == nil:
		// UC-014: 200に失効理由を含める（FR-15・VAR-10）
		return DisableAdminAccount200JSONResponse{RevocationReason: command.RevocationReasonAccountDisabled}, nil
	case errors.Is(err, command.ErrUserNotFound):
		// E1: UUID形式不正も合流（存在有無の情報漏洩回避）
		return nil, commonhttp.NewProblemError(http.StatusNotFound, "user-not-found", "対象ユーザーが存在しません")
	case errors.Is(err, command.ErrNotAdminAccount):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "not-admin-account", "対象は管理者アカウントではありません")
	case errors.Is(err, command.ErrAlreadyDisabled):
		return nil, commonhttp.NewProblemError(http.StatusConflict, "account-already-disabled", "既に無効化済みです")
	case errors.Is(err, command.ErrLastSuperAdmin):
		return nil, commonhttp.NewProblemError(http.StatusConflict, "last-super-admin", "最後のsuper_adminは無効化できません")
	default:
		// E5ほか未分類の内部失敗（Tx失敗はUseCaseが全ロールバック・ERROR記録済み）
		return nil, commonhttp.NewProblemError(http.StatusInternalServerError, "internal-error", "サーバ内部エラーが発生しました")
	}
}

func (h *Handler) ReactivateAdminAccount(ctx context.Context, req ReactivateAdminAccountRequestObject) (ReactivateAdminAccountResponseObject, error) {
	// M1/M2はミドルウェア（FR-19＋RequireRoles）が担うが配線欠落時はfail-closedで401一様。操作者sub（監査のoperator）も取り出す
	claims, ok := commonhttp.AuthClaimsFromContext(ctx)
	if !ok {
		return nil, commonhttp.NewProblemError(http.StatusUnauthorized, "invalid-token", "アクセストークンが無効です")
	}

	err := h.reactivateAccount.Handle(ctx, claims.UserID, req.UserId)
	switch {
	case err == nil:
		return ReactivateAdminAccount200Response{}, nil
	case errors.Is(err, command.ErrUserNotFound):
		// E1: UUID形式不正も合流（存在有無の情報漏洩回避）
		return nil, commonhttp.NewProblemError(http.StatusNotFound, "user-not-found", "対象ユーザーが存在しません")
	case errors.Is(err, command.ErrNotAdminAccount):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "not-admin-account", "対象は管理者アカウントではありません")
	case errors.Is(err, command.ErrNotDisabled):
		return nil, commonhttp.NewProblemError(http.StatusConflict, "account-not-disabled", "対象は無効化されていません")
	default:
		// 未分類の内部失敗（DB障害等・ERROR記録済み）。無効化API（SCR-13）と対称の500フォールバック
		return nil, commonhttp.NewProblemError(http.StatusInternalServerError, "internal-error", "サーバ内部エラーが発生しました")
	}
}

func (h *Handler) ConfirmPasswordReset(ctx context.Context, req ConfirmPasswordResetRequestObject) (ConfirmPasswordResetResponseObject, error) {
	if req.Body == nil {
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "リクエストボディが必要です")
	}

	// NOTE: openapiのminLength:1をバインダは検証しないため必須検査をここで行う（VerifyEmailと同型）
	if req.Body.Token == "" {
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "トークンが必要です")
	}

	err := h.confirmReset.Handle(ctx, req.Body.Token, req.Body.NewPassword)
	switch {
	case err == nil:
		return ConfirmPasswordReset200Response{}, nil
	case errors.Is(err, domain.ErrInvalidPassword):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "パスワードは15〜64文字で指定してください")
	case errors.Is(err, command.ErrInvalidResetToken):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "invalid-token", "無効なリセットトークン")
	case errors.Is(err, command.ErrResetTokenExpired):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "token-expired", "リセットトークンの有効期限が切れています")
	default:
		// E8ほか未分類の内部失敗（E8=Tx失敗はUseCaseが全ロールバック・ERROR記録済み。lookup障害・
		// ハッシュ化失敗も同型で500へ丸める）。typeはUC-009指定のinternal-error
		return nil, commonhttp.NewProblemError(http.StatusInternalServerError, "internal-error", "サーバ内部エラーが発生しました")
	}
}

func (h *Handler) Login(ctx context.Context, req LoginRequestObject) (LoginResponseObject, error) {
	if req.Body == nil {
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "リクエストボディが必要です")
	}

	result, err := h.login.Handle(ctx, string(req.Body.Email), req.Body.Password)
	var locked *command.LockedError
	switch {
	case err == nil:
		return Login200JSONResponse{AccessToken: result.AccessToken, RefreshToken: result.RefreshToken}, nil
	case errors.Is(err, domain.ErrInvalidEmail):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "メールアドレスの形式が不正です")
	case errors.As(err, &locked):
		// E2: retry_after は解除までの秒数（切り上げ・VAR-11）。Retry-Afterヘッダは共通実装が付与
		problem := commonhttp.NewProblemError(http.StatusTooManyRequests, "account-locked", "アカウントがロックされています")
		return nil, problem.WithRetryAfter(int(math.Ceil(locked.RetryAfter.Seconds()))).WithErrorCode("account_locked")
	case errors.Is(err, command.ErrAuthenticationFailed):
		// E3/E4: 未登録・不一致を区別しない
		return nil, commonhttp.NewProblemError(http.StatusUnauthorized, "authentication-failed", "認証に失敗しました")
	case errors.Is(err, domain.ErrEmailNotVerified):
		return nil, commonhttp.NewProblemError(http.StatusForbidden, "email-not-verified", "メールアドレスが確認されていません")
	case errors.Is(err, domain.ErrAccountDisabled):
		return nil, commonhttp.NewProblemError(http.StatusForbidden, "account-disabled", "アカウントが無効化されています")
	default:
		return nil, err
	}
}

func (h *Handler) RefreshToken(ctx context.Context, req RefreshTokenRequestObject) (RefreshTokenResponseObject, error) {
	if req.Body == nil {
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "リクエストボディが必要です")
	}

	result, err := h.refresh.Handle(ctx, req.Body.RefreshToken)
	var revoked *command.SessionRevokedError
	switch {
	case err == nil:
		return RefreshToken200JSONResponse{AccessToken: result.AccessToken, RefreshToken: result.RefreshToken}, nil
	case errors.Is(err, command.ErrInvalidRefreshToken):
		// E1形式不正・E2不存在（区別しない・列挙防止）
		return nil, commonhttp.NewProblemError(http.StatusUnauthorized, "invalid-token", "リフレッシュトークンが無効です")
	case errors.Is(err, command.ErrRefreshTokenExpired):
		// E3: 期限切れ
		return nil, commonhttp.NewProblemError(http.StatusUnauthorized, "token-expired", "リフレッシュトークンの有効期限が切れています")
	case errors.As(err, &revoked):
		// E4再利用検知・E5削除・E6無効化。失効理由コードを拡張フィールドに載せる（VAR-10）
		return nil, commonhttp.NewProblemError(http.StatusUnauthorized, "session-revoked", "セッションが失効しました").
			WithRevocationReason(revoked.Reason)
	default:
		return nil, err
	}
}

func (h *Handler) Logout(ctx context.Context, req LogoutRequestObject) (LogoutResponseObject, error) {
	// CND-06: FR-19ミドルウェア通過済みの認証情報を受け取る。未経由（配線欠落）はfail-closedで401一様。
	// NOTE: 本ガードはM1（ミドルウェアが401＋WWW-Authenticateを担う）ではなく配線欠落の安全網のため、
	// ヘッダは付与しない（到達＝Register配線のバグ）。認証ガードを入力検証より先に評価する
	claims, ok := commonhttp.AuthClaimsFromContext(ctx)
	if !ok {
		return nil, commonhttp.NewProblemError(http.StatusUnauthorized, "invalid-token", "アクセストークンが無効です")
	}

	// NOTE: strict handler経由ではBodyは常に非nil（Bind失敗は手前でエラー化）＝本ガードは到達不能。
	// 兄弟ハンドラ共通の防御慣行として残置する
	if req.Body == nil {
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "リクエストボディが必要です")
	}

	err := h.logout.Handle(ctx, req.Body.RefreshToken, claims.UserID)
	switch {
	case err == nil:
		// UC-007: A1不存在・E2所有者不一致・冪等失効いずれも200同一応答（情報漏洩防止）
		return Logout200Response{}, nil
	case errors.Is(err, command.ErrRefreshTokenFormatInvalid):
		// E1: RTは入力ペイロード（資格情報はAT）のためバリデーション違反=400
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "リフレッシュトークンが必要です")
	default:
		return nil, err
	}
}

func (h *Handler) ChangePassword(ctx context.Context, req ChangePasswordRequestObject) (ChangePasswordResponseObject, error) {
	// CND-06: FR-19ミドルウェア通過済みの認証情報を受け取る。未経由（配線欠落）はfail-closedで401一様。
	// NOTE: 本ガードはM1（ミドルウェアが401＋WWW-Authenticateを担う）ではなく配線欠落の安全網（ヘッダなし）
	claims, ok := commonhttp.AuthClaimsFromContext(ctx)
	if !ok {
		return nil, commonhttp.NewProblemError(http.StatusUnauthorized, "invalid-token", "アクセストークンが無効です")
	}

	// NOTE: strict handler経由ではBodyは常に非nil（兄弟ハンドラ共通の防御慣行として残置）
	if req.Body == nil {
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "リクエストボディが必要です")
	}

	err := h.changePassword.Handle(ctx, claims.UserID, req.Body.CurrentPassword, req.Body.NewPassword)
	var revoked *command.SessionRevokedError
	switch {
	case err == nil:
		// UC-010: 200に失効理由を含める（FR-10・再ログイン誘導）
		return ChangePassword200JSONResponse{RevocationReason: command.RevocationReasonPasswordChanged}, nil
	case errors.Is(err, domain.ErrInvalidPassword):
		// E1: VAR-02違反（文字数・72バイト）
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "パスワードは15〜64文字かつUTF-8で72バイト以下で指定してください")
	case errors.As(err, &revoked):
		// E4/E5: 削除済み・無効化済み（トークン状態は変更しない）
		return nil, commonhttp.NewProblemError(http.StatusUnauthorized, "session-revoked", "セッションが失効しました").
			WithRevocationReason(revoked.Reason)
	case errors.Is(err, command.ErrPasswordMismatch):
		// E2: 現在パスワード不一致（401はJWT認証失敗と区別・BUC-U08備考）
		return nil, commonhttp.NewProblemError(http.StatusForbidden, "password-mismatch", "現在のパスワードが一致しません")
	default:
		// E3ほか未分類の内部失敗（E3=Tx失敗はUseCaseが全ロールバック・ERROR記録済み。lookup障害・
		// ハッシュ化失敗も同型で500へ丸める）。typeはUC-010指定のinternal-error
		return nil, commonhttp.NewProblemError(http.StatusInternalServerError, "internal-error", "サーバ内部エラーが発生しました")
	}
}

// protectedRouter は生成コードの EchoRouter を満たす薄いアダプタ。保護パスの登録時のみ
// FR-19ミドルウェアをルート単位で注入する（echo全体へのUse適用＝他ルートへの副作用を避ける）。
// WARNING: 保護マップはopenapiの `security` 宣言と二重管理（乖離はunitのトリップワイヤテストが検知）。
// オーバーライドはPOST/PUTのみ＝他メソッドの保護ルートを追加する場合は該当メソッドの追加実装が必要
type protectedRouter struct {
	*echo.Echo
	protected map[string][]echo.MiddlewareFunc
}

func (r protectedRouter) POST(path string, handler echo.HandlerFunc, middleware ...echo.MiddlewareFunc) *echo.Route {
	if mws, ok := r.protected[path]; ok {
		middleware = append(middleware, mws...)
	}
	return r.Echo.POST(path, handler, middleware...)
}

func (r protectedRouter) PUT(path string, handler echo.HandlerFunc, middleware ...echo.MiddlewareFunc) *echo.Route {
	if mws, ok := r.protected[path]; ok {
		middleware = append(middleware, mws...)
	}
	return r.Echo.PUT(path, handler, middleware...)
}

// Register は認証必須ルート（openapi security: bearerAuth）へ jwtAuth を適用して全ルートを登録する。
// admin系ルートは jwtAuth の後段にロール認可（CND-17 OR評価）を連結する。
func Register(e *echo.Echo, h *Handler, jwtAuth echo.MiddlewareFunc) {
	router := protectedRouter{Echo: e, protected: map[string][]echo.MiddlewareFunc{
		"/auth/logout":   {jwtAuth}, // SCR-06（UC-007・CND-06）
		"/auth/password": {jwtAuth}, // SCR-09（UC-010・CND-06）
		// SCR-10（UC-011・CND-17）: super_admin限定（M2・403）。ログctxはUC-011規定
		"/admin/invitations": {jwtAuth, commonhttp.RequireRoles("admin_invitation", domain.RoleSuperAdmin)},
		// SCR-13（UC-014・CND-17）: super_admin限定（M2・403）。キーはecho登録パス（:userId）
		"/admin/accounts/:userId/disable": {jwtAuth, commonhttp.RequireRoles("account_disable", domain.RoleSuperAdmin)},
		// SCR-14（UC-015・CND-17）: super_admin限定（M2・403）。無効化APIと対称
		"/admin/accounts/:userId/reactivate": {jwtAuth, commonhttp.RequireRoles("account_reactivate", domain.RoleSuperAdmin)},
	}}
	RegisterHandlersWithBaseURL(router, NewStrictHandler(h, nil), "")
}
