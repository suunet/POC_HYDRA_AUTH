package http

import (
	"net/http"

	"github.com/labstack/echo/v4"

	applog "poc-app-hydra/backend/common/log"
)

// RequireRoles は JWTAuth（FR-19）の後段で動くロール認可ミドルウェア。
// CND-17: 要求ロールのいずれか1つを claims.Roles が含めば許可する（OR評価・NFR-16 rolesクレーム）。
// 不足は 403 forbidden（401=認証失敗と応答を区別・UC-011 M2）。
// logCtx はログの `ctx` フィールド（呼び出しルートの業務コンテキスト。例: UC-011は "admin_invitation"）。
func RequireRoles(logCtx string, roles ...string) echo.MiddlewareFunc {
	required := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		required[r] = struct{}{}
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			ctx := c.Request().Context()
			claims, ok := AuthClaimsFromContext(ctx)
			if !ok {
				// NOTE: JWTAuth未経由＝配線欠落の安全網。到達は配線バグでありM1（認証チャレンジ）ではないため
				// WWW-Authenticate は付与しない（UC-010ハンドラ安全網と同型）
				applog.FromContext(ctx).With("ctx", logCtx).WarnContext(ctx, "認証情報欠落（ミドルウェア配線異常）")
				return NewProblemError(http.StatusUnauthorized, "invalid-token", "アクセストークンが無効です")
			}
			for _, have := range claims.Roles {
				if _, match := required[have]; match {
					return next(c)
				}
			}
			applog.FromContext(ctx).With("ctx", logCtx, "user_id", claims.UserID).WarnContext(ctx, "権限不足")
			return NewProblemError(http.StatusForbidden, "forbidden", "この操作を行う権限がありません")
		}
	}
}
