package unit

import (
	"bytes"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apihttp "poc-app-hydra/backend/auth/api/http"
	"poc-app-hydra/backend/auth/app/command"
	"poc-app-hydra/backend/auth/domain"
	commonhttp "poc-app-hydra/backend/common/http"
	applog "poc-app-hydra/backend/common/log"
)

func newChangePasswordTestEcho(t *testing.T, repo *fakePasswordChangeRepository, key *rsa.PrivateKey) http.Handler {
	t.Helper()
	d := newTestDeps()
	e := commonhttp.NewEcho(applog.New(&bytes.Buffer{}, "auth-service"))
	apihttp.Register(e, apihttp.NewHandler(
		command.NewRegisterAccountHandler(d.repo, d.limiter, d.mailer),
		command.NewVerifyEmailHandler(&fakeTokenRepository{}, &fakeRateLimiter{blocked: map[string]bool{}}),
		command.NewResendEmailVerificationHandler(d.resendRepo, d.limiter, d.mailer),
		command.NewLoginHandler(&fakeLoginRepository{}, &fakeLockout{}, key),
		command.NewRefreshTokenHandler(&fakeRefreshRepository{}, key),
		command.NewLogoutHandler(&fakeRefreshRepository{}),
		command.NewChangePasswordHandler(repo),
		command.NewRequestPasswordResetHandler(&fakeResetRequestRepository{users: map[string]fakeResetUser{}}, &fakeRateLimiter{blocked: map[string]bool{}}, &fakeMailer{}),
		command.NewConfirmPasswordResetHandler(&fakeResetConfirmRepository{records: map[string]domain.PasswordResetTokenRecord{}}),
	), commonhttp.JWTAuth(&key.PublicKey))
	return e
}

func putPassword(t *testing.T, h http.Handler, authorization, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/auth/password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	h.ServeHTTP(rec, req)
	return rec
}

// UC-010 主成功: 有効AT＋正しい現在PWで200・bodyにrevocation_reason=password_changed（FR-10）
func TestUC010_ChangePasswordHTTP_Success_Returns200WithReason(t *testing.T) {
	key := testSigningKey(t)
	repo := &fakePasswordChangeRepository{creds: credsWith(t, domain.StatusInactive), found: true}

	res := putPassword(t, newChangePasswordTestEcho(t, repo, key),
		"Bearer "+accessTokenFor(t, key, uuid.New()),
		`{"current_password":"`+currentPW+`","new_password":"new-secret-passw0rd!"}`)

	require.Equal(t, http.StatusOK, res.Code)
	var body apihttp.PasswordChangeResponse
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))
	assert.Equal(t, command.RevocationReasonPasswordChanged, body.RevocationReason)
	assert.Len(t, repo.changed, 1)
}

// UC-010 M1: AT無しは401 invalid-token＋WWW-Authenticate（FR-19がPUTルートに適用されている）
func TestUC010_ChangePasswordHTTP_NoToken_Returns401(t *testing.T) {
	key := testSigningKey(t)
	res := putPassword(t, newChangePasswordTestEcho(t, &fakePasswordChangeRepository{}, key), "",
		`{"current_password":"x","new_password":"new-secret-passw0rd!"}`)

	require.Equal(t, http.StatusUnauthorized, res.Code)
	assert.Contains(t, problemType(t, res), "invalid-token")
	assert.Equal(t, "Bearer", res.Header().Get("WWW-Authenticate"))
}

// UC-010 E1: 新パスワード強度不足は400 validation-error
func TestUC010_ChangePasswordHTTP_WeakNewPassword_Returns400(t *testing.T) {
	key := testSigningKey(t)
	res := putPassword(t, newChangePasswordTestEcho(t, &fakePasswordChangeRepository{creds: credsWith(t, domain.StatusInactive), found: true}, key),
		"Bearer "+accessTokenFor(t, key, uuid.New()),
		`{"current_password":"`+currentPW+`","new_password":"short"}`)

	require.Equal(t, http.StatusBadRequest, res.Code)
	assert.Contains(t, problemType(t, res), "validation-error")
}

// UC-010 E2: 現在パスワード不一致は403 password-mismatch
func TestUC010_ChangePasswordHTTP_Mismatch_Returns403(t *testing.T) {
	key := testSigningKey(t)
	res := putPassword(t, newChangePasswordTestEcho(t, &fakePasswordChangeRepository{creds: credsWith(t, domain.StatusInactive), found: true}, key),
		"Bearer "+accessTokenFor(t, key, uuid.New()),
		`{"current_password":"WRONG-secret-passw0rd!","new_password":"new-secret-passw0rd!"}`)

	require.Equal(t, http.StatusForbidden, res.Code)
	assert.Contains(t, problemType(t, res), "password-mismatch")
}

// UC-010 E4/E5: 削除済み/無効化済みは401 session-revoked＋revocation_reason
func TestUC010_ChangePasswordHTTP_DeletedOrDisabled_Returns401WithReason(t *testing.T) {
	key := testSigningKey(t)
	cases := []struct {
		name   string
		repo   *fakePasswordChangeRepository
		reason string
	}{
		{"E4削除済み", &fakePasswordChangeRepository{found: false}, "account_deleted"},
		{"E5無効化済み", &fakePasswordChangeRepository{creds: credsWith(t, domain.StatusDisabled), found: true}, "account_disabled"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := putPassword(t, newChangePasswordTestEcho(t, c.repo, key),
				"Bearer "+accessTokenFor(t, key, uuid.New()),
				`{"current_password":"`+currentPW+`","new_password":"new-secret-passw0rd!"}`)

			require.Equal(t, http.StatusUnauthorized, res.Code)
			var p commonhttp.Problem
			require.NoError(t, json.Unmarshal(res.Body.Bytes(), &p))
			assert.Contains(t, p.Type, "session-revoked")
			assert.Equal(t, c.reason, p.RevocationReason)
			assert.Empty(t, res.Header().Get("WWW-Authenticate"),
				"E4/E5はhandler発の401＝WWW-Authenticateを付けない（FR-19経由のみ付与）")
		})
	}
}

// UC-010: FR-19配線が欠落してもハンドラのfail-closedガードが401を返す（UC-007先例と同型のピン留め）
func TestUC010_ChangePasswordHTTP_MissingMiddlewareWiring_FailsClosed401(t *testing.T) {
	key := testSigningKey(t)
	d := newTestDeps()
	e := commonhttp.NewEcho(applog.New(&bytes.Buffer{}, "auth-service"))
	passthrough := func(next echo.HandlerFunc) echo.HandlerFunc { return next } // 配線欠落の模擬
	apihttp.Register(e, apihttp.NewHandler(
		command.NewRegisterAccountHandler(d.repo, d.limiter, d.mailer),
		command.NewVerifyEmailHandler(&fakeTokenRepository{}, &fakeRateLimiter{blocked: map[string]bool{}}),
		command.NewResendEmailVerificationHandler(d.resendRepo, d.limiter, d.mailer),
		command.NewLoginHandler(&fakeLoginRepository{}, &fakeLockout{}, key),
		command.NewRefreshTokenHandler(&fakeRefreshRepository{}, key),
		command.NewLogoutHandler(&fakeRefreshRepository{}),
		command.NewChangePasswordHandler(&fakePasswordChangeRepository{}),
		command.NewRequestPasswordResetHandler(&fakeResetRequestRepository{users: map[string]fakeResetUser{}}, &fakeRateLimiter{blocked: map[string]bool{}}, &fakeMailer{}),
		command.NewConfirmPasswordResetHandler(&fakeResetConfirmRepository{records: map[string]domain.PasswordResetTokenRecord{}}),
	), passthrough)

	res := putPassword(t, e, "Bearer "+accessTokenFor(t, key, uuid.New()),
		`{"current_password":"x","new_password":"new-secret-passw0rd!"}`)
	require.Equal(t, http.StatusUnauthorized, res.Code, "claims欠落はfail-closedで401（安全網）")
	assert.Contains(t, problemType(t, res), "invalid-token")
}

// UC-010 E3: Tx失敗は500 internal-error（UC-010指定type）
func TestUC010_ChangePasswordHTTP_TxFailure_Returns500InternalError(t *testing.T) {
	key := testSigningKey(t)
	repo := &fakePasswordChangeRepository{creds: credsWith(t, domain.StatusInactive), found: true, chgErr: assert.AnError}

	res := putPassword(t, newChangePasswordTestEcho(t, repo, key),
		"Bearer "+accessTokenFor(t, key, uuid.New()),
		`{"current_password":"`+currentPW+`","new_password":"new-secret-passw0rd!"}`)

	require.Equal(t, http.StatusInternalServerError, res.Code)
	assert.Contains(t, problemType(t, res), "internal-error")
}
