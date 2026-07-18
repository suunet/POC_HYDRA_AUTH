package unit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	apihttp "poc-app-hydra/backend/auth/api/http"
	"poc-app-hydra/backend/auth/app/command"
	"poc-app-hydra/backend/auth/domain"
	commonhttp "poc-app-hydra/backend/common/http"
	applog "poc-app-hydra/backend/common/log"
	"poc-app-hydra/backend/common/ratelimit"
)

// NOTE: afterInsert はDBトランザクション内で呼ばれるメール送信を模し、エラー時は登録を反映しない（ロールバック相当）
type fakeUserRepository struct {
	existing    map[string]bool
	created     []domain.Registration
	tokens      []domain.EmailConfirmationToken
	insertError error // メール送信より前段（DB書き込み）の失敗を模す
}

func (f *fakeUserRepository) EmailExists(ctx context.Context, email string) (bool, error) {
	return f.existing[email], nil
}

func (f *fakeUserRepository) CreateUser(ctx context.Context, r domain.Registration, token domain.EmailConfirmationToken, afterInsert func(context.Context) error) error {
	if f.insertError != nil {
		return f.insertError
	}
	if err := afterInsert(ctx); err != nil {
		return err
	}
	f.created = append(f.created, r)
	f.tokens = append(f.tokens, token)
	return nil
}

type fakeRateLimiter struct {
	blocked    map[string]bool
	retryAfter time.Duration
	calls      []string
}

func (f *fakeRateLimiter) Allow(ctx context.Context, key string) (ratelimit.Result, error) {
	f.calls = append(f.calls, key)
	if f.blocked[key] {
		return ratelimit.NewResult(false, f.retryAfter, 0, time.Now().Add(f.retryAfter)), nil
	}
	return ratelimit.NewResult(true, 0, 0, time.Now()), nil
}

type fakeMailer struct {
	sent      []string
	sendError error
}

func (f *fakeMailer) SendConfirmationEmail(ctx context.Context, to, token string) error {
	if f.sendError != nil {
		return f.sendError
	}
	f.sent = append(f.sent, to+":"+token)
	return nil
}

type testDeps struct {
	repo    *fakeUserRepository
	limiter *fakeRateLimiter
	mailer  *fakeMailer
}

func newTestDeps() *testDeps {
	return &testDeps{
		repo:    &fakeUserRepository{existing: map[string]bool{}},
		limiter: &fakeRateLimiter{blocked: map[string]bool{}},
		mailer:  &fakeMailer{},
	}
}

func newAuthTestEcho(t *testing.T, d *testDeps) http.Handler {
	t.Helper()
	logger := applog.New(&bytes.Buffer{}, "auth-service")
	e := commonhttp.NewEcho(logger)
	apihttp.Register(e, apihttp.NewHandler(command.NewRegisterAccountHandler(d.repo, d.limiter, d.mailer), command.NewVerifyEmailHandler(&fakeTokenRepository{})))
	return e
}

func postRegister(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec
}

// UC-002: 主成功シナリオ — 201を返し、メール未確認状態＋userロール＋bcryptハッシュで登録する
func TestUC002_Register_Returns201_AndCreatesUser(t *testing.T) {
	d := newTestDeps()
	rec := postRegister(t, newAuthTestEcho(t, d), `{"email":"new@example.com","password":"secret-passw0rd!"}`)

	assert.Equal(t, http.StatusCreated, rec.Code)
	require.Len(t, d.repo.created, 1)
	created := d.repo.created[0]
	assert.Equal(t, "new@example.com", created.Email)
	assert.Equal(t, domain.StatusMailUnverified, created.Status)
	assert.Equal(t, domain.RoleUser, created.Role)
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(created.PasswordHash), []byte("secret-passw0rd!")),
		"password must be stored as a verifiable bcrypt hash")
	assert.NotEmpty(t, created.UserUUID)
}

// UC-002: E1 — メールアドレス形式エラー（VAR-01）は400 problem+json
func TestUC002_Register_InvalidEmail_Returns400ProblemJSON(t *testing.T) {
	for name, email := range map[string]string{
		"形式不正":    "not-an-email",
		"254文字超過": strings.Repeat("a", 250) + "@example.com",
	} {
		t.Run(name, func(t *testing.T) {
			d := newTestDeps()
			rec := postRegister(t, newAuthTestEcho(t, d), `{"email":"`+email+`","password":"secret-passw0rd!"}`)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
			var p commonhttp.Problem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			assert.Equal(t, commonhttp.ProblemTypeBase+"validation-error", p.Type)
			assert.Empty(t, d.repo.created)
		})
	}
}

// UC-002: E2 — パスワード強度エラー（VAR-02: 最小15・最大64・72バイト上限）は400 problem+json
func TestUC002_Register_InvalidPassword_Returns400ProblemJSON(t *testing.T) {
	for name, password := range map[string]string{
		"15文字未満": strings.Repeat("a", 14),
		"64文字超過": strings.Repeat("a", 65),
		"マルチバイト14文字（rune数）": strings.Repeat("あ", 14),
		"72バイト超過":           strings.Repeat("あ", 25), // 25文字だが75バイト
	} {
		t.Run(name, func(t *testing.T) {
			d := newTestDeps()
			rec := postRegister(t, newAuthTestEcho(t, d), `{"email":"new@example.com","password":"`+password+`"}`)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			var p commonhttp.Problem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			assert.Equal(t, commonhttp.ProblemTypeBase+"validation-error", p.Type)
			assert.Empty(t, d.repo.created)
		})
	}
}

// UC-002: VAR-02はUnicode許容・文字数はrune数（マルチバイト15文字は通る）
func TestUC002_Register_MultibytePassword_CountedInRunes(t *testing.T) {
	d := newTestDeps()
	rec := postRegister(t, newAuthTestEcho(t, d), `{"email":"mb@example.com","password":"`+strings.Repeat("あ", 15)+`"}`)
	assert.Equal(t, http.StatusCreated, rec.Code, "15文字（45バイト）はVAR-02を満たす")
	assert.Len(t, d.repo.created, 1)
}

// UC-002: ステップ8 — メール確認トークン（INF-06）を生成・保存する。VAR-06=24時間有効・平文は保存せずハッシュのみ
func TestUC002_Register_GeneratesEmailConfirmationToken(t *testing.T) {
	d := newTestDeps()
	before := time.Now().UTC()
	rec := postRegister(t, newAuthTestEcho(t, d), `{"email":"new@example.com","password":"secret-passw0rd!"}`)
	after := time.Now().UTC()

	assert.Equal(t, http.StatusCreated, rec.Code)
	require.Len(t, d.repo.tokens, 1)
	token := d.repo.tokens[0]

	assert.NotEmpty(t, token.TokenUUID)
	assert.Len(t, token.Hash, 64, "sha256 hex digest must be 64 chars")
	assert.True(t, token.ExpiresAt.After(before.Add(24*time.Hour).Add(-time.Minute)))
	assert.True(t, token.ExpiresAt.Before(after.Add(24*time.Hour).Add(time.Minute)), "VAR-06: 有効期限は24時間後")
}

// UC-002: トークンのハッシュ化はSHA-256（平文非保存。生成した平文をハッシュ照合できることを確認）
func TestUC002_EmailConfirmationToken_HashIsSHA256OfPlainToken(t *testing.T) {
	plain, token, err := domain.NewEmailConfirmationToken()
	require.NoError(t, err)

	sum := sha256.Sum256([]byte(plain))
	assert.Equal(t, hex.EncodeToString(sum[:]), token.Hash)
	assert.NotEqual(t, plain, token.Hash, "平文とハッシュが同一であってはならない")
}

// UC-002: A1 — 登録済みメールアドレスでも201（列挙攻撃対策・登録処理は行わない）。レート記録は実施済み
func TestUC002_Register_DuplicateEmail_Returns201_WithoutCreating(t *testing.T) {
	d := newTestDeps()
	d.repo.existing["taken@example.com"] = true
	rec := postRegister(t, newAuthTestEcho(t, d), `{"email":"taken@example.com","password":"secret-passw0rd!"}`)

	assert.Equal(t, http.StatusCreated, rec.Code, "A1: 未登録の場合と区別しない")
	assert.Empty(t, d.repo.created, "A1: 登録処理を行わない")
	assert.Contains(t, d.limiter.calls, "taken@example.com", "レート判定は登録済みでも一様に行う")
}

// UC-002: E4 — レートリミット超過は429 problem+json。登録済み/未登録に関わらず同一挙動
func TestUC002_Register_RateLimited_Returns429ProblemJSON(t *testing.T) {
	for name, existing := range map[string]bool{
		"未登録":  false,
		"登録済み": true,
	} {
		t.Run(name, func(t *testing.T) {
			d := newTestDeps()
			d.limiter.blocked["limited@example.com"] = true
			// 端数TTL: 切り上げ（Ceil）でないと42にならない（Floor/Round=41系を検出）
			d.limiter.retryAfter = 41*time.Second + 200*time.Millisecond
			d.repo.existing["limited@example.com"] = existing

			rec := postRegister(t, newAuthTestEcho(t, d), `{"email":"limited@example.com","password":"secret-passw0rd!"}`)

			assert.Equal(t, http.StatusTooManyRequests, rec.Code)
			var p commonhttp.Problem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			assert.Equal(t, commonhttp.ProblemTypeBase+"rate-limit-exceeded", p.Type)
			require.NotNil(t, p.RetryAfter)
			assert.Equal(t, 42, *p.RetryAfter, "retry_after はTTL残秒数（VAR-16・固定値でない）")
			assert.Empty(t, d.repo.created)
		})
	}
}

// UC-002: ステップ9 — メール確認トークンをEVT-01でメールサーバーへ送信する
func TestUC002_Register_SendsConfirmationEmail(t *testing.T) {
	d := newTestDeps()
	rec := postRegister(t, newAuthTestEcho(t, d), `{"email":"new@example.com","password":"secret-passw0rd!"}`)

	assert.Equal(t, http.StatusCreated, rec.Code)
	require.Len(t, d.mailer.sent, 1)
	assert.True(t, strings.HasPrefix(d.mailer.sent[0], "new@example.com:"), "宛先はemail")
}

// UC-002: E3 — メール送信失敗時は登録・トークン保存をロールバックし503を返す
func TestUC002_Register_MailDeliveryFails_RollsBack_Returns503(t *testing.T) {
	d := newTestDeps()
	d.mailer.sendError = errors.New("smtp connection refused")

	rec := postRegister(t, newAuthTestEcho(t, d), `{"email":"new@example.com","password":"secret-passw0rd!"}`)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"mail-delivery-error", p.Type)
	assert.Empty(t, d.repo.created, "E3: 送信失敗時は登録をロールバックする")
	assert.Empty(t, d.repo.tokens, "E3: 送信失敗時はトークン保存もロールバックする")
}

// UC-002: DB書き込み失敗（メール送信より前段）はE3ではなく未捕捉のインフラエラーとして扱う（メール未着なのに503 mail-delivery-errorを返さない）
func TestUC002_Register_DBWriteFails_IsNotMisreportedAsMailDeliveryError(t *testing.T) {
	d := newTestDeps()
	d.repo.insertError = errors.New("connection refused")

	rec := postRegister(t, newAuthTestEcho(t, d), `{"email":"new@example.com","password":"secret-passw0rd!"}`)

	assert.NotEqual(t, http.StatusServiceUnavailable, rec.Code, "DB失敗をE3(503)として誤報告してはならない")
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.NotEqual(t, commonhttp.ProblemTypeBase+"mail-delivery-error", p.Type)
	assert.Empty(t, d.mailer.sent, "メール送信は試行されない")
}
