package auth

import (
	"context"
	"embed"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	apihttp "poc-app-hydra/backend/auth/api/http"
	"poc-app-hydra/backend/common"
	"poc-app-hydra/backend/common/module/contracts"
)

//go:embed adapters/db/migrations/*.sql
var embedMigrations embed.FS

type Module struct {
	pgxDb   *pgxpool.Pool
	handler *apihttp.Handler
}

func NewModule(pgxDb *pgxpool.Pool) *Module {
	return &Module{
		pgxDb:   pgxDb,
		handler: apihttp.NewHandler(),
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
	// 他コンテキストへ提供する契約なし
}

func (m *Module) RegisterHttp(e *echo.Echo) {
	apihttp.Register(e, m.handler)
}
