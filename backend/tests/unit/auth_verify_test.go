package unit

import (
	"bytes"
	"context"
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

type fakeTokenRepository struct {
	record   domain.EmailConfirmationTokenRecord
	found    bool
	consumed []uuid.UUID
	// NOTE: Tx内競合（used_at更新0行等）を模す
	consumeErr error
}

func (f *fakeTokenRepository) GetEmailConfirmationTokenByHash(ctx context.Context, hash string) (domain.EmailConfirmationTokenRecord, error) {
	if !f.found {
		return domain.EmailConfirmationTokenRecord{}, domain.ErrTokenNotFound
	}
	return f.record, nil
}

func (f *fakeTokenRepository) ConsumeEmailConfirmationToken(ctx context.Context, tokenUUID, userUUID uuid.UUID) error {
	if f.consumeErr != nil {
		return f.consumeErr
	}
	f.consumed = append(f.consumed, tokenUUID)
	return nil
}

func newVerifyTestEcho(t *testing.T, repo *fakeTokenRepository) http.Handler {
	t.Helper()
	d := newTestDeps()
	e := commonhttp.NewEcho(applog.New(&bytes.Buffer{}, "auth-service"))
	apihttp.Register(e, apihttp.NewHandler(
		command.NewRegisterAccountHandler(d.repo, d.limiter, d.mailer),
		command.NewVerifyEmailHandler(repo),
	))
	return e
}

func postVerify(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/email-verify", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec
}

func validTokenRecord(plain string) domain.EmailConfirmationTokenRecord {
	return domain.EmailConfirmationTokenRecord{
		TokenUUID: uuid.New(),
		UserUUID:  uuid.New(),
		TokenHash: domain.HashEmailConfirmationToken(plain),
		ExpiresAt: time.Now().Add(time.Hour),
	}
}

func problemType(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	return p.Type
}

// UC-003: 主成功シナリオ — 有効トークンで200。トークン使い切り＋mail_unverified→inactive（単一Tx＝Consume）
func TestUC003_Verify_ValidToken_Returns200_AndConsumes(t *testing.T) {
	repo := &fakeTokenRepository{record: validTokenRecord("plain-token"), found: true}

	rec := postVerify(t, newVerifyTestEcho(t, repo), `{"token":"plain-token"}`)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, repo.consumed, 1)
	assert.Equal(t, repo.record.TokenUUID, repo.consumed[0])
}

// UC-003: E1a — 不在トークンは400 invalid-token
func TestUC003_Verify_UnknownToken_Returns400InvalidToken(t *testing.T) {
	repo := &fakeTokenRepository{found: false}

	rec := postVerify(t, newVerifyTestEcho(t, repo), `{"token":"unknown"}`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, commonhttp.ProblemTypeBase+"invalid-token", problemType(t, rec))
}

// UC-003: E1b — 使用済みはE1aと同一の400 invalid-token（状態を区別しない）。Consumeは呼ばれない
func TestUC003_Verify_UsedToken_Returns400InvalidToken(t *testing.T) {
	used := time.Now().Add(-time.Minute)
	record := validTokenRecord("plain-token")
	record.UsedAt = &used
	repo := &fakeTokenRepository{record: record, found: true}

	rec := postVerify(t, newVerifyTestEcho(t, repo), `{"token":"plain-token"}`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, commonhttp.ProblemTypeBase+"invalid-token", problemType(t, rec))
	assert.Empty(t, repo.consumed)
}

// UC-003: E2 — 期限切れは400 token-expired
func TestUC003_Verify_ExpiredToken_Returns400TokenExpired(t *testing.T) {
	record := validTokenRecord("plain-token")
	record.ExpiresAt = time.Now().Add(-time.Minute)
	repo := &fakeTokenRepository{record: record, found: true}

	rec := postVerify(t, newVerifyTestEcho(t, repo), `{"token":"plain-token"}`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, commonhttp.ProblemTypeBase+"token-expired", problemType(t, rec))
}

// UC-003: 評価順序 — 使用済みかつ期限切れは invalid-token（E1bをE2より先に判定＝実在を漏らさない）
func TestUC003_Verify_UsedAndExpired_PrefersInvalidToken(t *testing.T) {
	used := time.Now().Add(-time.Hour)
	record := validTokenRecord("plain-token")
	record.UsedAt = &used
	record.ExpiresAt = time.Now().Add(-time.Minute)
	repo := &fakeTokenRepository{record: record, found: true}

	rec := postVerify(t, newVerifyTestEcho(t, repo), `{"token":"plain-token"}`)

	assert.Equal(t, commonhttp.ProblemTypeBase+"invalid-token", problemType(t, rec))
}

// UC-003: Tx内競合（used_at更新0行＝Q-8含む）は invalid-token に倒す
func TestUC003_Verify_ConsumeConflict_Returns400InvalidToken(t *testing.T) {
	repo := &fakeTokenRepository{record: validTokenRecord("plain-token"), found: true, consumeErr: domain.ErrTokenConsumeConflict}

	rec := postVerify(t, newVerifyTestEcho(t, repo), `{"token":"plain-token"}`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, commonhttp.ProblemTypeBase+"invalid-token", problemType(t, rec))
}
