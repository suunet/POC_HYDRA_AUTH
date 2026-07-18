package http

import (
	"log/slog"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"poc-app-hydra/backend/common"
	applog "poc-app-hydra/backend/common/log"
)

func NewEcho(logger *slog.Logger) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	e.HTTPErrorHandler = ProblemErrorHandler
	e.Logger = common.NewEchoSlogAdapter(logger)

	e.Use(middleware.ContextTimeout(10 * time.Second))
	e.Use(middleware.RecoverWithConfig(middleware.RecoverConfig{
		LogErrorFunc: func(c echo.Context, err error, stack []byte) error {

			applog.FromContext(c.Request().Context()).Log(
				c.Request().Context(), applog.LevelCritical,
				"panic recovered", "ctx", "http", "error", err.Error(), "stack", string(stack),
			)
			return err
		},
	}))
	e.Use(contextMiddleware(logger))
	e.Use(requestLogMiddleware)

	return e
}

func contextMiddleware(logger *slog.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			req := c.Request()
			ctx := req.Context()

			reqID := req.Header.Get("X-Request-Id")
			if reqID == "" {
				reqID = applog.RequestIDFromContext(ctx)
			}

			ctx = applog.ContextWithLogger(ctx, logger)
			ctx = applog.ContextWithRequestID(ctx, reqID)
			ctx = applog.ContextWithTrace(ctx, applog.NewTraceID(), applog.NewSpanID())

			c.SetRequest(req.WithContext(ctx))
			// NOTE: クライアントがIDで問い合わせできるようレスポンスへ返す（参照元のエコーバックと同義）
			c.Response().Header().Set("X-Request-Id", applog.RequestIDFromContext(ctx))
			return next(c)
		}
	}
}
