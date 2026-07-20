package unit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend/auth/domain"
	commonhttp "poc-app-hydra/backend/common/http"
)

type fakeResendUser struct {
	uuid   uuid.UUID
	status string
}

type fakeResendRepository struct {
	users      map[string]fakeResendUser
	reissued   []uuid.UUID
	tokens     []domain.EmailConfirmationToken
	reissueErr error // 再発行Tx（メール送信前段）の失敗を模す
}

func (f *fakeResendRepository) FindUserByEmail(ctx context.Context, email string) (uuid.UUID, string, bool, error) {
	u, ok := f.users[email]
	if !ok {
		return uuid.Nil, "", false, nil
	}
	return u.uuid, u.status, true, nil
}

func (f *fakeResendRepository) ReissueEmailConfirmationToken(ctx context.Context, userUUID uuid.UUID, token domain.EmailConfirmationToken, afterInsert func(context.Context) error) error {
	if f.reissueErr != nil {
		return f.reissueErr
	}
	// NOTE: afterInsert(送信)成功後にのみ記録する＝送信失敗時は無効化・新発行を反映しない（Txロールバック相当・E2）
	if err := afterInsert(ctx); err != nil {
		return err
	}
	f.reissued = append(f.reissued, userUUID)
	f.tokens = append(f.tokens, token)
	return nil
}

func postResend(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/email-verify/resend", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec
}

func seedResendUser(d *testDeps, email, status string) {
	d.resendRepo.users[email] = fakeResendUser{uuid: uuid.New(), status: status}
}

// UC-004: 主成功シナリオ — mail_unverified ユーザーは200・新トークン再発行・メール送信
func TestUC004_Resend_Unverified_Returns200_ReissuesAndSends(t *testing.T) {
	d := newTestDeps()
	seedResendUser(d, "wait@example.com", domain.StatusMailUnverified)

	rec := postResend(t, newAuthTestEcho(t, d), `{"email":"wait@example.com"}`)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, d.resendRepo.reissued, 1)
	require.Len(t, d.resendRepo.tokens, 1)
	assert.Len(t, d.resendRepo.tokens[0].Hash, 64, "sha256 hex digest")
	assert.True(t, d.resendRepo.tokens[0].ExpiresAt.After(time.Now().Add(23*time.Hour)), "VAR-06: 24時間")
	require.Len(t, d.mailer.sent, 1)
	assert.True(t, strings.HasPrefix(d.mailer.sent[0], "wait@example.com:"))
}

// UC-004: A1 — 未登録は200・メール送信なし・再発行なし（列挙秘匿・FR-04）
func TestUC004_Resend_Unregistered_Returns200_Silent(t *testing.T) {
	d := newTestDeps()

	rec := postResend(t, newAuthTestEcho(t, d), `{"email":"ghost@example.com"}`)

	assert.Equal(t, http.StatusOK, rec.Code, "A1: 登録済みと区別しない")
	assert.Empty(t, d.resendRepo.reissued)
	assert.Empty(t, d.mailer.sent, "A1: メール送信しない")
	assert.Contains(t, d.limiter.calls, "ghost@example.com", "レート判定は未登録でも一様に行う")
}

// UC-004: A2 — mail_unverified 以外の全状態は200・メール送信なし（確認済み・無効化・削除等を包含）
func TestUC004_Resend_NonUnverifiedStatus_Returns200_Silent(t *testing.T) {
	for _, status := range []string{domain.StatusInactive, "invited", "disabled", "deleted"} {
		t.Run(status, func(t *testing.T) {
			d := newTestDeps()
			seedResendUser(d, "done@example.com", status)

			rec := postResend(t, newAuthTestEcho(t, d), `{"email":"done@example.com"}`)

			assert.Equal(t, http.StatusOK, rec.Code, "A2: 未確認と区別しない")
			assert.Empty(t, d.resendRepo.reissued, "A2: 再発行しない")
			assert.Empty(t, d.mailer.sent, "A2: メール送信しない")
		})
	}
}

// UC-004: E1 — レート超過は429 problem+json・retry_afterはTTL残秒。状態に依らず一様
func TestUC004_Resend_RateLimited_Returns429_Uniform(t *testing.T) {
	for name, seed := range map[string]func(*testDeps){
		"未登録":             func(*testDeps) {},
		"mail_unverified": func(d *testDeps) { seedResendUser(d, "limited@example.com", domain.StatusMailUnverified) },
		"確認済み":            func(d *testDeps) { seedResendUser(d, "limited@example.com", domain.StatusInactive) },
	} {
		t.Run(name, func(t *testing.T) {
			d := newTestDeps()
			seed(d)
			d.limiter.blocked["limited@example.com"] = true
			d.limiter.retryAfter = 239*time.Second + 500*time.Millisecond // 端数TTL: Ceilでないと240にならない（Floor/Round=239を検出）

			rec := postResend(t, newAuthTestEcho(t, d), `{"email":"limited@example.com"}`)

			assert.Equal(t, http.StatusTooManyRequests, rec.Code)
			var p commonhttp.Problem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			assert.Equal(t, commonhttp.ProblemTypeBase+"rate-limit-exceeded", p.Type)
			require.NotNil(t, p.RetryAfter)
			assert.Equal(t, 240, *p.RetryAfter, "retry_after はTTL残秒（切り上げ）")
			assert.Empty(t, d.resendRepo.reissued, "超過時は再発行に進まない")
		})
	}
}

// UC-004: E2 — メール送信失敗は503・再発行をロールバック（レート窓の消費はTx外なので別途）
func TestUC004_Resend_MailDeliveryFails_RollsBack_Returns503(t *testing.T) {
	d := newTestDeps()
	seedResendUser(d, "wait@example.com", domain.StatusMailUnverified)
	d.mailer.sendError = errors.New("smtp connection refused")

	rec := postResend(t, newAuthTestEcho(t, d), `{"email":"wait@example.com"}`)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"mail-delivery-error", p.Type)
	assert.Empty(t, d.resendRepo.reissued, "E2: 送信失敗時は再発行をロールバック")
}
