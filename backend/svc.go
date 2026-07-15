package backend

import (
	"context"
	"log/slog"

	"github.com/labstack/echo/v4"

	"poc-app-hydra/backend/auth"
	commonhttp "poc-app-hydra/backend/common/http"
	"poc-app-hydra/backend/common/module"
	"poc-app-hydra/backend/common/module/contracts"
	"poc-app-hydra/backend/healthcheck"
)

func BuildApp(ctx context.Context, logger *slog.Logger) (*echo.Echo, error) {
	return build(ctx, logger, []module.Module{
		healthcheck.NewModule(),
	})
}

func BuildAuth(ctx context.Context, logger *slog.Logger, deps auth.Deps) (*echo.Echo, error) {
	return build(ctx, logger, []module.Module{
		auth.NewModule(deps),
	})
}

func build(ctx context.Context, logger *slog.Logger, modules []module.Module) (*echo.Echo, error) {
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
