package backend

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

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

// Run は e を起動し、ctx のキャンセル（シグナル捕捉）で graceful shutdown する。
// NOTE: 参照元 svc.Run の翻案。Svc型でなく関数形なのは2バイナリ分離・Deps注入（記録済み逸脱）のため。
// タイムアウト値・ErrServerClosed を正常終了扱いにする挙動は参照元と同一。
func Run(ctx context.Context, logger *slog.Logger, e *echo.Echo, port string) error {
	go func() {
		<-ctx.Done()
		if err := e.Shutdown(context.Background()); err != nil {
			logger.Error("shutting down http server failed", "ctx", "bootstrap", "error", err)
		}
	}()

	e.Server.WriteTimeout = 15 * time.Second
	e.Server.ReadHeaderTimeout = 5 * time.Second

	if err := e.Start(":" + port); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("starting http server failed: %w", err)
	}
	return nil
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
