package auth

import (
	"context"
	"embed"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"

	authdb "poc-app-hydra/backend/auth/adapters/db"
	"poc-app-hydra/backend/auth/adapters/mail"
	"poc-app-hydra/backend/auth/adapters/ratelimit"
	apihttp "poc-app-hydra/backend/auth/api/http"
	"poc-app-hydra/backend/auth/app/command"
	"poc-app-hydra/backend/auth/domain"
	"poc-app-hydra/backend/common"
	"poc-app-hydra/backend/common/module/contracts"
)

//go:embed adapters/db/migrations/*.sql
var embedMigrations embed.FS

type Module struct {
	pgxDb   *pgxpool.Pool
	handler *apihttp.Handler
}

type Deps struct {
	PgxDb        *pgxpool.Pool
	Redis        *redis.Client
	SMTPAddr     string
	SMTPFromAddr string
}

func NewModule(deps Deps) *Module {
	users := authdb.NewUserRepository(deps.PgxDb)
	limiter := ratelimit.NewRegistrationLimiter(deps.Redis, domain.RegistrationRateLimitWindow)
	mailer := mail.NewSMTPMailer(deps.SMTPAddr, deps.SMTPFromAddr)
	register := command.NewRegisterAccountHandler(users, limiter, mailer)
	return &Module{
		pgxDb:   deps.PgxDb,
		handler: apihttp.NewHandler(register),
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
