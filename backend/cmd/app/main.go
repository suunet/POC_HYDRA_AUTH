package main

import (
	"context"
	"os"

	"poc-app-hydra/backend"
	applog "poc-app-hydra/backend/common/log"
)

func main() {
	logger := applog.New(os.Stdout, "app-service")
	ctx := applog.ContextWithLogger(context.Background(), logger)

	e, err := backend.BuildApp(ctx, logger)
	if err != nil {
		logger.ErrorContext(ctx, "failed to build service", "ctx", "bootstrap", "error", err)
		os.Exit(1)
	}

	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "8080"
	}

	logger.InfoContext(ctx, "app-service starting", "ctx", "bootstrap", "port", port)
	if err := e.Start(":" + port); err != nil {
		logger.ErrorContext(ctx, "server stopped", "ctx", "bootstrap", "error", err)
		os.Exit(1)
	}
}
