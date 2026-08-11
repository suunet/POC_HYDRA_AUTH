package auth

import (
	"context"
	"crypto/rsa"
	"embed"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	authdb "poc-app-hydra/backend/auth/adapters/db"
	apihttp "poc-app-hydra/backend/auth/api/http"
	"poc-app-hydra/backend/auth/app/command"
	"poc-app-hydra/backend/common"
	commonhttp "poc-app-hydra/backend/common/http"
	"poc-app-hydra/backend/common/module/contracts"
)

//go:embed adapters/db/migrations/*.sql
var embedMigrations embed.FS

type Module struct {
	pgxDb        *pgxpool.Pool
	handler      *apihttp.Handler
	jwtPublicKey *rsa.PublicKey
}

// NOTE: Limiter/Mailer はインターフェースで受け取る（呼び出し側が実装を選ぶ）。
// cmd/auth は実アダプタ（Redis/SMTP）、コンポーネントテストはMailerのみスタブに差し替える
type Deps struct {
	PgxDb         *pgxpool.Pool
	Limiter       command.RateLimiter
	VerifyLimiter command.RateLimiter
	ResendLimiter command.RateLimiter
	ResetLimiter  command.RateLimiter
	InviteLimiter command.RateLimiter
	LoginLockout  command.Lockout
	Mailer        command.Mailer
	JWTSigningKey *rsa.PrivateKey
}

func NewModule(deps Deps) *Module {
	users := authdb.NewUserRepository(deps.PgxDb)
	register := command.NewRegisterAccountHandler(users, deps.Limiter, deps.Mailer)
	verify := command.NewVerifyEmailHandler(users, deps.VerifyLimiter)
	resend := command.NewResendEmailVerificationHandler(users, deps.ResendLimiter, deps.Mailer)
	login := command.NewLoginHandler(users, deps.LoginLockout, deps.JWTSigningKey)
	refresh := command.NewRefreshTokenHandler(users, deps.JWTSigningKey)
	logout := command.NewLogoutHandler(users)
	changePassword := command.NewChangePasswordHandler(users)
	requestReset := command.NewRequestPasswordResetHandler(users, deps.ResetLimiter, deps.Mailer)
	confirmReset := command.NewConfirmPasswordResetHandler(users)
	inviteAdmin := command.NewInviteAdminHandler(users, deps.InviteLimiter, deps.Mailer)
	return &Module{
		pgxDb:        deps.PgxDb,
		handler:      apihttp.NewHandler(register, verify, resend, login, refresh, logout, changePassword, requestReset, confirmReset, inviteAdmin),
		jwtPublicKey: &deps.JWTSigningKey.PublicKey,
	}
}

func (m *Module) Init(ctx context.Context) error {
	return common.MigrateDatabaseUp(
		ctx,
		"auth",
		m.pgxDb,
		embedMigrations,
		"adapters/db/migrations",
	)
}

func (m *Module) RegisterContracts(c *contracts.Contracts) {
	// NOTE: 他コンテキストへ提供する契約なし
}

func (m *Module) RegisterHttp(e *echo.Echo) {
	apihttp.Register(e, m.handler, commonhttp.JWTAuth(m.jwtPublicKey))
}
