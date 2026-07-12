package module

import (
	"context"

	"github.com/labstack/echo/v4"

	"poc-app-hydra/backend/common/module/contracts"
)

type Module interface {
	Init(ctx context.Context) error
	RegisterContracts(c *contracts.Contracts)
	RegisterHttp(e *echo.Echo)
}
