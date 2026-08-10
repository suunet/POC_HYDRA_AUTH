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

type fakeResetUser struct {
	uuid   uuid.UUID
	status string
}

type fakeResetRequestRepository struct {
	users    map[string]fakeResetUser
	issued   []uuid.UUID
	tokens   []domain.PasswordResetToken
	issueErr error // 発行Tx（メール送信前段）の失敗を模す
}

func (f *fakeResetRequestRepository) FindUserByEmail(ctx context.Context, email string) (uuid.UUID, string, bool, error) {
	u, ok := f.users[email]
	if !ok {
		return uuid.Nil, "", false, nil
	}
	return u.uuid, u.status, true, nil
}

func (f *fakeResetRequestRepository) IssuePasswordResetToken(ctx context.Context, userUUID uuid.UUID, token domain.PasswordResetToken, afterInsert func(context.Context) error) error {
	if f.issueErr != nil {
		return f.issueErr
	}
	// NOTE: afterInsert(送信)成功後にのみ記録する＝送信失敗時は無効化・新発行を反映しない（Tx全ロールバック相当・E3）
	if err := afterInsert(ctx); err != nil {
		return err
	}
	f.issued = append(f.issued, userUUID)
	f.tokens = append(f.tokens, token)
	return nil
}

func postPasswordReset(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/password-reset", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec
}

func seedResetRequestUser(d *testDeps, email, status string) {
	d.resetRepo.users[email] = fakeResetUser{uuid: uuid.New(), status: status}
}

// UC-008: 主成功シナリオ — inactive（STM-01.未認証）ユーザーは200・トークン発行（VAR-05: 30分・SHA-256のみ保持）・リセットメール送信
func TestUC008_RequestReset_Inactive_Returns200_IssuesAndSends(t *testing.T) {
	d := newTestDeps()
	seedResetRequestUser(d, "forgot@example.com", domain.StatusInactive)

	rec := postPasswordReset(t, newAuthTestEcho(t, d), `{"email":"forgot@example.com"}`)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, d.resetRepo.issued, 1)
	require.Len(t, d.resetRepo.tokens, 1)
	assert.Len(t, d.resetRepo.tokens[0].Hash, 64, "sha256 hex digest")
	assert.True(t, d.resetRepo.tokens[0].ExpiresAt.After(time.Now().Add(29*time.Minute)), "VAR-05: 30分")
	require.Len(t, d.mailer.sentResets, 1)
	assert.True(t, strings.HasPrefix(d.mailer.sentResets[0], "forgot@example.com:"))
	assert.Empty(t, d.mailer.sent, "確認メールではなくリセットメールを送る")
}

// UC-008: E2 — レート超過は429・retry_afterはTTL残秒（切り上げ）。評価はレートが最初なので
// 形式不正入力・未登録・inactiveのいずれでも一様に429（VAR-12・列挙オラクル封じ）
func TestUC008_RequestReset_RateLimited_Returns429_UniformAndFirst(t *testing.T) {
	// NOTE: パース不能な形式不正はopenapi format:emailのバインド検証で全エンドポイント一律400（UC-002/UC-004と同一）。
	// UseCase内の評価順（レート→E1）は「パース可能だがVAR-01違反（254文字超）」で検証する
	longEmail := strings.Repeat("a", 245) + "@example.com"
	for name, seed := range map[string]struct {
		email string
		setup func(*testDeps)
	}{
		"inactive":         {"limited@example.com", func(d *testDeps) { seedResetRequestUser(d, "limited@example.com", domain.StatusInactive) }},
		"未登録":              {"limited@example.com", func(*testDeps) {}},
		"VAR-01違反(254超)でも": {longEmail, func(*testDeps) {}},
	} {
		t.Run(name, func(t *testing.T) {
			d := newTestDeps()
			seed.setup(d)
			d.limiter.blocked[seed.email] = true
			d.limiter.retryAfter = 239*time.Second + 500*time.Millisecond // 端数TTL: Ceilでないと240にならない（Floor/Round=239を検出）

			rec := postPasswordReset(t, newAuthTestEcho(t, d), `{"email":"`+seed.email+`"}`)

			assert.Equal(t, http.StatusTooManyRequests, rec.Code, "レートが最初＝形式検証より先に429")
			var p commonhttp.Problem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			assert.Equal(t, commonhttp.ProblemTypeBase+"rate-limit-exceeded", p.Type)
			require.NotNil(t, p.RetryAfter)
			assert.Equal(t, 240, *p.RetryAfter, "retry_after はTTL残秒（切り上げ・VAR-12③）")
			assert.Empty(t, d.resetRepo.issued, "超過時は発行に進まない")
		})
	}
}

// UC-008: E1 — メールアドレス形式不正（VAR-01・254文字超）は400。ただしレート判定・記録はその前に一様に済んでいる（評価順）。
// パース不能な入力はバインド層（openapi format:email）が全エンドポイント一律400で弾く（UC-002/UC-004と同一）
func TestUC008_RequestReset_InvalidEmail_Returns400_AfterRateRecorded(t *testing.T) {
	d := newTestDeps()
	longEmail := strings.Repeat("a", 245) + "@example.com"

	rec := postPasswordReset(t, newAuthTestEcho(t, d), `{"email":"`+longEmail+`"}`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"validation-error", p.Type)
	assert.Contains(t, d.limiter.calls, longEmail, "レート記録は形式検証より先に確定（VAR-12①）")
	assert.Empty(t, d.resetRepo.issued)
	assert.Empty(t, d.mailer.sentResets)
}

// UC-008: A1 — 未登録・不適格（inactive以外の全状態）は一律200・発行なし・メール送信なし（FR-08・列挙秘匿）。
// レート判定は存在・状態に依らず一様に行う
func TestUC008_RequestReset_UnregisteredOrIneligible_Returns200_Silent(t *testing.T) {
	cases := map[string]func(*testDeps){
		"未登録":                       func(*testDeps) {},
		domain.StatusMailUnverified: func(d *testDeps) { seedResetRequestUser(d, "silent@example.com", domain.StatusMailUnverified) },
		domain.StatusInvited:        func(d *testDeps) { seedResetRequestUser(d, "silent@example.com", domain.StatusInvited) },
		domain.StatusDisabled:       func(d *testDeps) { seedResetRequestUser(d, "silent@example.com", domain.StatusDisabled) },
		domain.StatusDeleted:        func(d *testDeps) { seedResetRequestUser(d, "silent@example.com", domain.StatusDeleted) },
	}
	for name, seed := range cases {
		t.Run(name, func(t *testing.T) {
			d := newTestDeps()
			seed(d)

			rec := postPasswordReset(t, newAuthTestEcho(t, d), `{"email":"silent@example.com"}`)

			assert.Equal(t, http.StatusOK, rec.Code, "A1: 成功時と区別しない一律200")
			assert.Empty(t, rec.Body.String(), "ボディ最小限＝成功時と同一（列挙防止）")
			assert.Empty(t, d.resetRepo.issued, "A1: 発行しない")
			assert.Empty(t, d.mailer.sentResets, "A1: メール送信しない")
			assert.Contains(t, d.limiter.calls, "silent@example.com", "レート判定は状態に依らず一様")
		})
	}
}

// UC-008: E3 — メール送信失敗は503・発行Txを全ロールバック（レート窓の消費はTx外で維持）
func TestUC008_RequestReset_MailDeliveryFails_RollsBack_Returns503(t *testing.T) {
	d := newTestDeps()
	seedResetRequestUser(d, "forgot@example.com", domain.StatusInactive)
	d.mailer.sendError = errors.New("smtp connection refused")

	rec := postPasswordReset(t, newAuthTestEcho(t, d), `{"email":"forgot@example.com"}`)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"mail-delivery-error", p.Type)
	assert.Empty(t, d.resetRepo.issued, "E3: 送信失敗時は発行をロールバック")
	assert.Contains(t, d.limiter.calls, "forgot@example.com", "レート窓は消費したまま（VAR-12①）")
}
