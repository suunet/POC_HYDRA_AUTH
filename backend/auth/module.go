package auth

import (
	"context"
	"embed"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"poc-app-hydra/backend/common"
	"poc-app-hydra/backend/common/module/contracts"
)

//go:embed adapters/db/migrations/*.sql
var embedMigrations embed.FS

type Module struct {
	pgxDb *pgxpool.Pool
}

func NewModule(pgxDb *pgxpool.Pool) *Module {
	return &Module{pgxDb: pgxDb}
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
	// TODO: T-005 サイクル3（API-first生成）で SCR-01（登録API）を登録する
}
