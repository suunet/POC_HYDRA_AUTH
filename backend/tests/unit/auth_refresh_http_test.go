package unit

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apihttp "poc-app-hydra/backend/auth/api/http"
	"poc-app-hydra/backend/auth/app/command"
	"poc-app-hydra/backend/auth/domain"
	commonhttp "poc-app-hydra/backend/common/http"
	applog "poc-app-hydra/backend/common/log"
)

func postRefresh(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/token/refresh", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec
}

func newRefreshTestEcho(t *testing.T, repo *fakeRefreshRepository) http.Handler {
	t.Helper()
	d := newTestDeps()
	e := commonhttp.NewEcho(applog.New(&bytes.Buffer{}, "auth-service"))
	key := testSigningKey(t)
	apihttp.Register(e, apihttp.NewHandler(
		command.NewRegisterAccountHandler(d.repo, d.limiter, d.mailer),
		command.NewVerifyEmailHandler(&fakeTokenRepository{}, &fakeRateLimiter{blocked: map[string]bool{}}),
		command.NewResendEmailVerificationHandler(d.resendRepo, d.limiter, d.mailer),
		command.NewLoginHandler(&fakeLoginRepository{}, &fakeLockout{}, key),
		command.NewRefreshTokenHandler(repo, key),
		command.NewLogoutHandler(&fakeRefreshRepository{}),
		command.NewChangePasswordHandler(&fakePasswordChangeRepository{}),
		command.NewRequestPasswordResetHandler(&fakeResetRequestRepository{users: map[string]fakeResetUser{}}, &fakeRateLimiter{blocked: map[string]bool{}}, &fakeMailer{}),
		command.NewConfirmPasswordResetHandler(&fakeResetConfirmRepository{records: map[string]domain.PasswordResetTokenRecord{}}),
		command.NewInviteAdminHandler(&fakeInviteRepository{users: map[string]fakeInviteUser{}}, &fakeRateLimiter{blocked: map[string]bool{}}, &fakeMailer{}),
		command.NewAcceptInvitationHandler(&fakeAcceptInvitationRepository{records: map[string]domain.InvitationTokenRecord{}, users: map[string][]string{}}),
		command.NewDisableAccountHandler(&fakeDisableRepository{}),
		command.NewReactivateAccountHandler(&fakeReactivateRepository{}),
	), commonhttp.JWTAuth(&key.PublicKey))
	return e
}

// UC-006: 主成功 — 有効トークンで200・両トークンをJSONで返す
func TestUC006_RefreshHTTP_Success_Returns200(t *testing.T) {
	userUUID := uuid.New()
	repo := &fakeRefreshRepository{
		record: validStored(userUUID, uuid.New()),
		found:  true,
		user:   command.RefreshUser{UserUUID: userUUID, Status: domain.StatusInactive, Roles: []string{"user"}},
		userOK: true,
	}
	rec := postRefresh(t, newRefreshTestEcho(t, repo), `{"refresh_token":"valid-token"}`)

	require.Equal(t, http.StatusOK, rec.Code)
	var body apihttp.LoginTokens
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.NotEmpty(t, body.AccessToken)
	assert.NotEmpty(t, body.RefreshToken)
}

// UC-006 E1: 空トークンは401 invalid-token
func TestUC006_RefreshHTTP_EmptyToken_Returns401InvalidToken(t *testing.T) {
	rec := postRefresh(t, newRefreshTestEcho(t, &fakeRefreshRepository{}), `{"refresh_token":""}`)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, problemType(t, rec), "invalid-token")
}

// UC-006 E2: 不存在トークンは401 invalid-token（E1と同一応答・列挙防止）
func TestUC006_RefreshHTTP_NotFound_Returns401InvalidToken(t *testing.T) {
	rec := postRefresh(t, newRefreshTestEcho(t, &fakeRefreshRepository{found: false}), `{"refresh_token":"nope"}`)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, problemType(t, rec), "invalid-token")
}

// UC-006 E3: 期限切れは401 token-expired
func TestUC006_RefreshHTTP_Expired_Returns401TokenExpired(t *testing.T) {
	rec := &fakeRefreshRepository{found: true, record: validStored(uuid.New(), uuid.New())}
	rec.record.ExpiresAt = time.Now().Add(-time.Minute)
	res := postRefresh(t, newRefreshTestEcho(t, rec), `{"refresh_token":"expired"}`)
	require.Equal(t, http.StatusUnauthorized, res.Code)
	assert.Contains(t, problemType(t, res), "token-expired")
}

// UC-006 E4: 再利用検知は401 session-revoked・revocation_reason=token_reuse_detected
func TestUC006_RefreshHTTP_Reuse_Returns401SessionRevoked(t *testing.T) {
	rec := &fakeRefreshRepository{found: true, record: validStored(uuid.New(), uuid.New())}
	used := time.Now().Add(-time.Hour)
	rec.record.UsedAt = &used
	res := postRefresh(t, newRefreshTestEcho(t, rec), `{"refresh_token":"reused"}`)

	require.Equal(t, http.StatusUnauthorized, res.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &p))
	assert.Contains(t, p.Type, "session-revoked")
	assert.Equal(t, "token_reuse_detected", p.RevocationReason)
}

// UC-006 E5/E6: 削除済み/無効化済みは401 session-revoked・失効理由コードを載せる
func TestUC006_RefreshHTTP_DeletedOrDisabled_Returns401WithReason(t *testing.T) {
	cases := []struct {
		name   string
		userOK bool
		status string
		reason string
	}{
		{"deleted", false, "", "account_deleted"},
		{"disabled", true, domain.StatusDisabled, "account_disabled"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			userUUID := uuid.New()
			repo := &fakeRefreshRepository{
				found:  true,
				record: validStored(userUUID, uuid.New()),
				user:   command.RefreshUser{UserUUID: userUUID, Status: c.status},
				userOK: c.userOK,
			}
			res := postRefresh(t, newRefreshTestEcho(t, repo), `{"refresh_token":"tok"}`)

			require.Equal(t, http.StatusUnauthorized, res.Code)
			var p commonhttp.Problem
			require.NoError(t, json.Unmarshal(res.Body.Bytes(), &p))
			assert.Contains(t, p.Type, "session-revoked")
			assert.Equal(t, c.reason, p.RevocationReason)
		})
	}
}
