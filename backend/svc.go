package backend

import (
	"context"
	"log/slog"

	"github.com/labstack/echo/v4"

	commonhttp "poc-app-hydra/backend/common/http"
	"poc-app-hydra/backend/common/module"
	"poc-app-hydra/backend/common/module/contracts"
	"poc-app-hydra/backend/healthcheck"
)

func Build(ctx context.Context, logger *slog.Logger) (*echo.Echo, error) {
	modules := []module.Module{
		healthcheck.NewModule(),
	}

	c := contracts.New()
	e := commonhttp.NewEcho(logger)

	for _, m := range modules {
		if err := m.Init(ctx); err != nil {
			return nil, err
		}
		m.RegisterContracts(c)
		m.RegisterHttp(e)
	}

	return e, nil
}
