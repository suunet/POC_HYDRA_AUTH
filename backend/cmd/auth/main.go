package main

import (
	"context"
	"fmt"
	"os"

	"github.com/redis/go-redis/v9"

	"poc-app-hydra/backend"
	"poc-app-hydra/backend/auth"
	"poc-app-hydra/backend/auth/adapters/mail"
	"poc-app-hydra/backend/auth/adapters/ratelimit"
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

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "redis:6379"
	}
	redisClient := redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.ErrorContext(ctx, "failed to connect redis", "ctx", "bootstrap", "error", err)
		os.Exit(1)
	}
	defer func() { _ = redisClient.Close() }()

	smtpHost := os.Getenv("SMTP_HOST")
	if smtpHost == "" {
		smtpHost = "mailpit"
	}
	smtpPort := os.Getenv("SMTP_PORT")
	if smtpPort == "" {
		smtpPort = "1025"
	}
	smtpFrom := os.Getenv("SMTP_FROM")
	if smtpFrom == "" {
		smtpFrom = "no-reply@example.com"
	}

	limiter := ratelimit.NewRegistrationLimiter(redisClient)
	mailer := mail.NewSMTPMailer(fmt.Sprintf("%s:%s", smtpHost, smtpPort), smtpFrom)

	e, err := backend.BuildAuth(ctx, logger, auth.Deps{
		PgxDb:   pool,
		Limiter: limiter,
		Mailer:  mailer,
	})
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
