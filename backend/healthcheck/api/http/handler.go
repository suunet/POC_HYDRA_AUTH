package http

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"poc-app-hydra/backend/healthcheck/app/query"
)

type Handler struct {
	health *query.HealthHandler
}

func NewHandler(health *query.HealthHandler) *Handler {
	return &Handler{health: health}
}

func (h *Handler) GetHealth(c echo.Context) error {
	res := h.health.Handle(c.Request().Context())
	return c.JSON(http.StatusOK, res)
}
