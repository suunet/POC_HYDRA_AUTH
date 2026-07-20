package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

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
	// NOTE: SIGINT/SIGTERM を捕捉し ctx キャンセル→backend.Run の graceful shutdown へ（参照元 cmd/main.go と同一）
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx = applog.ContextWithLogger(ctx, logger)

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
	hmacSecret, weakHMAC := ratelimit.ResolveHMACSecret(os.Getenv("RATELIMIT_HMAC_SECRET"))
	if weakHMAC {
		// WARNING: 公知の開発用既定鍵。本番でこのログが出る構成は不可（INF-14の鍵秘匿が崩れる）
		logger.WarnContext(ctx, "RATELIMIT_HMAC_SECRETが公知の開発用既定鍵です（未設定または既定鍵の明示設定）。本番では別の鍵を設定してください", "ctx", "bootstrap")
	}
	verifyLimiter := ratelimit.NewEmailVerifyLimiter(redisClient, []byte(hmacSecret))
	resendLimiter := ratelimit.NewResendEmailLimiter(redisClient)
	mailer := mail.NewSMTPMailer(fmt.Sprintf("%s:%s", smtpHost, smtpPort), smtpFrom)

	e, err := backend.BuildAuth(ctx, logger, auth.Deps{
		PgxDb:         pool,
		Limiter:       limiter,
		VerifyLimiter: verifyLimiter,
		ResendLimiter: resendLimiter,
		Mailer:        mailer,
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
	if err := backend.Run(ctx, logger, e, port); err != nil {
		logger.ErrorContext(ctx, "server stopped", "ctx", "bootstrap", "error", err)
		os.Exit(1)
	}
}
