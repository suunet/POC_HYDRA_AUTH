package query

import (
	"context"

	applog "poc-app-hydra/backend/common/log"
)

type HealthResult struct {
	Status string `json:"status"`
}

type HealthHandler struct{}

func NewHealthHandler() *HealthHandler {
	return &HealthHandler{}
}

func (h *HealthHandler) Handle(ctx context.Context) HealthResult {
	logger := applog.FromContext(ctx)
	logger.InfoContext(ctx, "usecase started", "ctx", "healthcheck", "usecase", "UC-001")
	defer logger.InfoContext(ctx, "usecase finished", "ctx", "healthcheck", "usecase", "UC-001")

	return HealthResult{Status: "ok"}
}
