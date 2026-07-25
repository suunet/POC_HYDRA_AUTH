package unit

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	apihttp "poc-app-hydra/backend/auth/api/http"
	"poc-app-hydra/backend/auth/app/command"
	"poc-app-hydra/backend/auth/domain"
	commonhttp "poc-app-hydra/backend/common/http"
	applog "poc-app-hydra/backend/common/log"
)

func testSigningKey(t *testing.T) *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		if t != nil {
			t.Fatal(err)
		}
		panic(err)
	}
	return key
}

type fakeLoginRepository struct {
	user  command.LoginUser
	found bool
	saved []command.RefreshTokenRecord
}

func (f *fakeLoginRepository) GetLoginUser(ctx context.Context, email string) (command.LoginUser, bool, error) {
	if !f.found {
		return command.LoginUser{}, false, nil
	}
	return f.user, true, nil
}

func (f *fakeLoginRepository) SaveRefreshToken(ctx context.Context, r command.RefreshTokenRecord) error {
	f.saved = append(f.saved, r)
	return nil
}

type fakeLockout struct {
	locked     bool
	retryAfter time.Duration
	failures   int
	resets     int
}

func (f *fakeLockout) Check(ctx context.Context, key string) (bool, time.Duration, error) {
	return f.locked, f.retryAfter, nil
}

func (f *fakeLockout) RecordFailure(ctx context.Context, key string) (bool, error) {
	f.failures++
	return false, nil
}

func (f *fakeLockout) Reset(ctx context.Context, key string) error {
	f.resets++
	return nil
}

func postLogin(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec
}

func newLoginTestEcho(t *testing.T, repo *fakeLoginRepository, lockout *fakeLockout, key *rsa.PrivateKey) http.Handler {
	t.Helper()
	d := newTestDeps()
	e := commonhttp.NewEcho(applog.New(&bytes.Buffer{}, "auth-service"))
	apihttp.Register(e, apihttp.NewHandler(
		command.NewRegisterAccountHandler(d.repo, d.limiter, d.mailer),
		command.NewVerifyEmailHandler(&fakeTokenRepository{}, &fakeRateLimiter{blocked: map[string]bool{}}),
		command.NewResendEmailVerificationHandler(d.resendRepo, d.limiter, d.mailer),
		command.NewLoginHandler(repo, lockout, key),
	))
	return e
}

func loginUserWithPassword(t *testing.T, password, status string, roles ...string) command.LoginUser {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), domain.PasswordBcryptCost)
	require.NoError(t, err)
	return command.LoginUser{UserUUID: uuid.New(), PasswordHash: string(hash), Status: status, Roles: roles}
}

// UC-005: 主成功 — inactive ユーザーは200・両トークン発行・リフレッシュ保存・失敗カウントリセット
func TestUC005_Login_Success_Returns200_IssuesTokens(t *testing.T) {
	key := testSigningKey(t)
	repo := &fakeLoginRepository{user: loginUserWithPassword(t, "secret-passw0rd!", domain.StatusInactive, "user"), found: true}
	lockout := &fakeLockout{}

	rec := postLogin(t, newLoginTestEcho(t, repo, lockout, key), `{"email":"a@example.com","password":"secret-passw0rd!"}`)

	require.Equal(t, http.StatusOK, rec.Code)
	var body apihttp.LoginResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.NotEmpty(t, body.AccessToken)
	assert.NotEmpty(t, body.RefreshToken)
	assert.Len(t, repo.saved, 1, "リフレッシュトークンがDB保存される")
	assert.NotEqual(t, body.RefreshToken, repo.saved[0].TokenHash, "保存はハッシュ・返却は平文（NFR-14）")
	assert.Equal(t, 1, lockout.resets, "成功で失敗カウントをリセット")
}

// UC-005 E1: メールアドレス形式不正は400 validation-error
func TestUC005_Login_InvalidEmail_Returns400(t *testing.T) {
	rec := postLogin(t, newLoginTestEcho(t, &fakeLoginRepository{}, &fakeLockout{}, testSigningKey(t)), `{"email":"not-an-email","password":"secret-passw0rd!"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, problemType(t, rec), "validation-error")
}

// UC-005 E2: ロックアウト中は429 account-locked・error_code・retry_after秒
func TestUC005_Login_LockedOut_Returns429(t *testing.T) {
	lockout := &fakeLockout{locked: true, retryAfter: 13*time.Minute + 500*time.Millisecond}
	rec := postLogin(t, newLoginTestEcho(t, &fakeLoginRepository{}, lockout, testSigningKey(t)), `{"email":"a@example.com","password":"secret-passw0rd!"}`)

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Contains(t, p.Type, "account-locked")
	assert.Equal(t, "account_locked", p.ErrorCode)
	require.NotNil(t, p.RetryAfter)
	assert.Equal(t, 781, *p.RetryAfter, "解除までの秒数（切り上げ・780.5→781）")
	assert.Equal(t, "781", rec.Header().Get("Retry-After"))
}

// UC-005 E3: ユーザー不存在は401・ダミー検証と失敗加算（列挙防止）
func TestUC005_Login_UserNotFound_Returns401_RecordsFailure(t *testing.T) {
	lockout := &fakeLockout{}
	rec := postLogin(t, newLoginTestEcho(t, &fakeLoginRepository{found: false}, lockout, testSigningKey(t)), `{"email":"ghost@example.com","password":"secret-passw0rd!"}`)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, problemType(t, rec), "authentication-failed")
	assert.Equal(t, 1, lockout.failures, "不存在でも失敗を加算（均一）")
}

// UC-005 E4: パスワード不一致は401（E3と同一応答）・失敗加算
func TestUC005_Login_WrongPassword_Returns401_RecordsFailure(t *testing.T) {
	repo := &fakeLoginRepository{user: loginUserWithPassword(t, "secret-passw0rd!", domain.StatusInactive, "user"), found: true}
	lockout := &fakeLockout{}
	rec := postLogin(t, newLoginTestEcho(t, repo, lockout, testSigningKey(t)), `{"email":"a@example.com","password":"WRONG-passw0rd!"}`)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, problemType(t, rec), "authentication-failed")
	assert.Equal(t, 1, lockout.failures)
	assert.Empty(t, repo.saved, "失敗時はトークンを発行しない")
}

// UC-005 E5: メール未確認（mail_unverified）は403 email-not-verified
func TestUC005_Login_EmailNotVerified_Returns403(t *testing.T) {
	repo := &fakeLoginRepository{user: loginUserWithPassword(t, "secret-passw0rd!", domain.StatusMailUnverified, "user"), found: true}
	rec := postLogin(t, newLoginTestEcho(t, repo, &fakeLockout{}, testSigningKey(t)), `{"email":"a@example.com","password":"secret-passw0rd!"}`)

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, problemType(t, rec), "email-not-verified")
}

// UC-005 E6: 無効化済み（disabled）は403 account-disabled
func TestUC005_Login_AccountDisabled_Returns403(t *testing.T) {
	repo := &fakeLoginRepository{user: loginUserWithPassword(t, "secret-passw0rd!", domain.StatusDisabled, "user"), found: true}
	rec := postLogin(t, newLoginTestEcho(t, repo, &fakeLockout{}, testSigningKey(t)), `{"email":"a@example.com","password":"secret-passw0rd!"}`)

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, problemType(t, rec), "account-disabled")
}
