package main

import (
	"context"
	"os"

	"poc-app-hydra/backend"
	"poc-app-hydra/backend/common"
	applog "poc-app-hydra/backend/common/log"
)

func main() {
	logger := applog.New(os.Stdout, "auth-service")
	ctx := applog.ContextWithLogger(context.Background(), logger)

	dsn := os.Getenv("POSTGRES_URL")
	if dsn == "" {
		logger.ErrorContext(ctx, "POSTGRES_URL is not set", "ctx", "bootstrap")
		os.Exit(1)
	}

	pool, err := common.NewPgxPool(ctx, dsn)
	if err != nil {
		logger.ErrorContext(ctx, "failed to connect database", "ctx", "bootstrap", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	e, err := backend.BuildAuth(ctx, logger, pool)
	if err != nil {
		logger.ErrorContext(ctx, "failed to build service", "ctx", "bootstrap", "error", err)
		os.Exit(1)
	}

	port := os.Getenv("AUTH_PORT")
	if port == "" {
		port = "8081"
	}

	logger.InfoContext(ctx, "auth-service starting", "ctx", "bootstrap", "port", port)
	if err := e.Start(":" + port); err != nil {
		logger.ErrorContext(ctx, "server stopped", "ctx", "bootstrap", "error", err)
		os.Exit(1)
	}
}
