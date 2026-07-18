package healthcheck

import (
	"context"

	"github.com/labstack/echo/v4"

	"poc-app-hydra/backend/common/module/contracts"
	apihttp "poc-app-hydra/backend/healthcheck/api/http"
	"poc-app-hydra/backend/healthcheck/app/query"
)

type Module struct {
	handler *apihttp.Handler
}

func NewModule() *Module {
	return &Module{handler: apihttp.NewHandler(query.NewHealthHandler())}
}

func (m *Module) Init(ctx context.Context) error {
	return nil
}

func (m *Module) RegisterContracts(c *contracts.Contracts) {
	// NOTE: 他コンテキストへ提供する契約なし
}

func (m *Module) RegisterHttp(e *echo.Echo) {
	apihttp.RegisterRoutes(e, m.handler)
}
