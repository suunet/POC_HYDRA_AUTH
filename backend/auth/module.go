package auth

import (
	"context"
	"embed"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	authdb "poc-app-hydra/backend/auth/adapters/db"
	apihttp "poc-app-hydra/backend/auth/api/http"
	"poc-app-hydra/backend/auth/app/command"
	"poc-app-hydra/backend/common"
	"poc-app-hydra/backend/common/module/contracts"
)

//go:embed adapters/db/migrations/*.sql
var embedMigrations embed.FS

type Module struct {
	pgxDb   *pgxpool.Pool
	handler *apihttp.Handler
}

// NOTE: Limiter/Mailer はインターフェースで受け取る（呼び出し側が実装を選ぶ）。
// cmd/auth は実アダプタ（Redis/SMTP）、コンポーネントテストはMailerのみスタブに差し替える
type Deps struct {
	PgxDb         *pgxpool.Pool
	Limiter       command.RateLimiter
	VerifyLimiter command.RateLimiter
	Mailer        command.Mailer
}

func NewModule(deps Deps) *Module {
	users := authdb.NewUserRepository(deps.PgxDb)
	register := command.NewRegisterAccountHandler(users, deps.Limiter, deps.Mailer)
	verify := command.NewVerifyEmailHandler(users, deps.VerifyLimiter)
	return &Module{
		pgxDb:   deps.PgxDb,
		handler: apihttp.NewHandler(register, verify),
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
	apihttp.Register(e, m.handler)
}
