package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"poc-app-hydra/backend"
	applog "poc-app-hydra/backend/common/log"
)

func main() {
	logger := applog.New(os.Stdout, "app-service")
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx = applog.ContextWithLogger(ctx, logger)

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
	if err := backend.Run(ctx, logger, e, port); err != nil {
		logger.ErrorContext(ctx, "server stopped", "ctx", "bootstrap", "error", err)
		os.Exit(1)
	}
}
