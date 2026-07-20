package tests_test

import (
	"context"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"poc-app-hydra/backend"
	"poc-app-hydra/backend/auth"
	"poc-app-hydra/backend/auth/adapters/ratelimit"
	authclient "poc-app-hydra/backend/auth/api/http/client"
	"poc-app-hydra/backend/common"
	applog "poc-app-hydra/backend/common/log"
)

// NOTE: DB・Redisは実インフラを使う一方、メール送信のみMailpitへの実送信を避けるためスタブに差し替える
type stubMailer struct {
	mu        sync.Mutex
	sent      []sentMail
	sendError error
}

type sentMail struct {
	To    string
	Token string
}

func (m *stubMailer) SendConfirmationEmail(ctx context.Context, to, plainToken string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sendError != nil {
		return m.sendError
	}
	m.sent = append(m.sent, sentMail{To: to, Token: plainToken})
	return nil
}

func (m *stubMailer) FailWith(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendError = err
}

func (m *stubMailer) Sent() []sentMail {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]sentMail, len(m.sent))
	copy(out, m.sent)
	return out
}

var (
	mailer      *stubMailer
	client      *authclient.ClientWithResponses
	pool        *pgxpool.Pool
	redisClient *redis.Client
)

func TestMain(m *testing.M) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := applog.New(os.Stdout, "auth-service-component-test")
	ctx = applog.ContextWithLogger(ctx, logger)

	dsn := os.Getenv("POSTGRES_URL")
	if dsn == "" {
		panic("POSTGRES_URL environment variable is not set")
	}
	var err error
	pool, err = common.NewPgxPool(ctx, dsn)
	if err != nil {
		panic(err)
	}
	defer pool.Close()

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "redis:6379"
	}
	redisClient = redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := redisClient.Ping(ctx).Err(); err != nil {
		panic(err)
	}
	defer func() { _ = redisClient.Close() }()

	mailer = &stubMailer{}
	limiter := ratelimit.NewRegistrationLimiter(redisClient)
	verifyLimiter := ratelimit.NewEmailVerifyLimiter(redisClient, []byte("component-test-secret"))
	resendLimiter := ratelimit.NewResendEmailLimiter(redisClient)

	e, err := backend.BuildAuth(ctx, logger, auth.Deps{
		PgxDb:         pool,
		Limiter:       limiter,
		VerifyLimiter: verifyLimiter,
		ResendLimiter: resendLimiter,
		Mailer:        mailer,
	})
	if err != nil {
		panic(err)
	}

	server := httptest.NewServer(e)
	defer server.Close()

	client, err = authclient.NewClientWithResponses(server.URL)
	if err != nil {
		panic(err)
	}

	os.Exit(m.Run())
}
