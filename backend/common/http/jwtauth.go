package http

import (
	"context"
	"crypto/rsa"
	"errors"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"

	applog "poc-app-hydra/backend/common/log"
)

// AuthClaims は FR-19（JWT検証ミドルウェア）が検証済みトークンから取り出す認証情報。
// Roles は NFR-16（文字列配列）。本人確認は UserID（sub）で行う。
type AuthClaims struct {
	UserID string
	Roles  []string
}

type authClaimsKey struct{}

// AuthClaimsFromContext は JWTAuth 通過後の認証情報を返す（未経由は ok=false）。
func AuthClaimsFromContext(ctx context.Context) (AuthClaims, bool) {
	claims, ok := ctx.Value(authClaimsKey{}).(AuthClaims)
	return claims, ok
}

// VerifyAccessToken は RS256 公開鍵でアクセストークン（INF-03）を検証し sub/roles を返す。
// NOTE: alg は RS256 のみ許可（alg none / HS256 混同攻撃の拒否）。exp は必須（欠落トークンを
// 発行側の実装に依存せずミドルウェア層でも拒否する＝多層防御・FR-19）
func VerifyAccessToken(pub *rsa.PublicKey, tokenString string) (AuthClaims, error) {
	parsed, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		return pub, nil
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithExpirationRequired())
	if err != nil {
		return AuthClaims{}, err
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return AuthClaims{}, jwt.ErrTokenInvalidClaims
	}
	sub, err := claims.GetSubject()
	if err != nil || sub == "" {
		return AuthClaims{}, jwt.ErrTokenInvalidClaims
	}
	rawRoles, _ := claims["roles"].([]interface{})
	roles := make([]string, 0, len(rawRoles))
	for _, r := range rawRoles {
		if s, ok := r.(string); ok {
			roles = append(roles, s)
		}
	}
	return AuthClaims{UserID: sub, Roles: roles}, nil
}

// JWTAuth は FR-19（JWTを検証するミドルウェア機能）。Bearerトークンを検証し、
// sub/roles を context へ格納する。検証失敗は種別を問わず 401 invalid-token で一様に返す
// （失敗種別を応答で区別しない＝情報漏洩防止・FR-19）。UC-007 M1。
func JWTAuth(pub *rsa.PublicKey) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			ctx := c.Request().Context()
			logger := applog.FromContext(ctx).With("ctx", "jwt_verify")
			unauthorized := func() error {
				c.Response().Header().Set("WWW-Authenticate", "Bearer")
				return NewProblemError(http.StatusUnauthorized, "invalid-token", "アクセストークンが無効です")
			}

			// NOTE: authスキームは大文字小文字非区別（RFC 6750）。トークン空（"Bearer "のみ）は
			// クライアント実装バグの典型のため改ざんシグナル（監査）でなく欠落側に分類する
			const prefix = "Bearer "
			header := c.Request().Header.Get(echo.HeaderAuthorization)
			token := ""
			if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
				token = strings.TrimSpace(header[len(prefix):])
			}
			if token == "" {
				// AT欠落（Bearer形式でない・トークン空を含む）: 監査対象外のビジネス例外WARNING（UC-007 M1）
				logger.WarnContext(ctx, "アクセストークン欠落")
				return unauthorized()
			}

			claims, err := VerifyAccessToken(pub, token)
			if err != nil {
				switch {
				case errors.Is(err, jwt.ErrTokenExpired):
					// 期限切れ: 正常なクライアント遷移（→UC-006）でも発生。監査対象外のビジネス例外WARNING
					logger.WarnContext(ctx, "アクセストークン期限切れ")
				case errors.Is(err, jwt.ErrTokenMalformed):
					logger.WarnContext(ctx, "JWTフォーマット不正") // NFR-07監査
				default:
					// 署名不正・alg不一致・claims不正（exp/sub欠落）等の残余は安全側で改ざん検知に分類（NFR-07監査）
					logger.WarnContext(ctx, "JWT署名不正・改ざん検知")
				}
				return unauthorized()
			}

			c.SetRequest(c.Request().WithContext(context.WithValue(ctx, authClaimsKey{}, claims)))
			return next(c)
		}
	}
}
