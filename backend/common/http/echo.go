package http

import (
	"log/slog"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	applog "poc-app-hydra/backend/common/log"
)

func NewEcho(logger *slog.Logger) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	e.Use(middleware.Recover())
	e.Use(contextMiddleware(logger))

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
			return next(c)
		}
	}
}
