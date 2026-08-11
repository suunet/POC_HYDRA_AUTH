package unit

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend/auth/app/command"
	"poc-app-hydra/backend/auth/domain"
	commonhttp "poc-app-hydra/backend/common/http"
	applog "poc-app-hydra/backend/common/log"
)

type fakeInviteUser struct {
	roles []string
}

type fakeInviteRepository struct {
	users  map[string]fakeInviteUser
	issued []string // 発行先email
	tokens []domain.InvitationToken
	roles  []string // 発行時に紐付けたロール
}

func (f *fakeInviteRepository) FindUserRolesByEmail(ctx context.Context, email string) ([]string, bool, error) {
	u, ok := f.users[email]
	if !ok {
		return nil, false, nil
	}
	return u.roles, true, nil
}

func (f *fakeInviteRepository) IssueInvitationToken(ctx context.Context, email, role string, token domain.InvitationToken, afterInsert func(context.Context) error) error {
	// NOTE: afterInsert(送信)成功後にのみ記録する＝送信失敗時は無効化・新発行を反映しない（Tx全ロールバック相当・E5）
	if err := afterInsert(ctx); err != nil {
		return err
	}
	f.issued = append(f.issued, email)
	f.tokens = append(f.tokens, token)
	f.roles = append(f.roles, role)
	return nil
}

func postInvite(t *testing.T, h http.Handler, bearer, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/invitations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+bearer)
	}
	h.ServeHTTP(rec, req)
	return rec
}

func superAdminAT(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	at, err := domain.GenerateAccessToken(key, "admin-1", []string{domain.RoleSuperAdmin}, time.Minute)
	require.NoError(t, err)
	return at
}

// UC-011 M2（ロール認可トリップワイヤ・実挙動検知）: super_admin以外のATは403 forbidden。
// AT欠落は401一様（M1・FR-19）
func TestUC011_InviteAdmin_AuthzEnforced_401And403(t *testing.T) {
	d := newTestDeps()
	key := testSigningKey(t)
	h := newAuthTestEchoKeyed(t, d, key)

	noAT := postInvite(t, h, "", `{"email":"new-admin@example.com","role":"operator"}`)
	assert.Equal(t, http.StatusUnauthorized, noAT.Code, "M1: AT欠落は401一様")

	userAT, err := domain.GenerateAccessToken(key, "user-1", []string{"user"}, time.Minute)
	require.NoError(t, err)
	rec := postInvite(t, h, userAT, `{"email":"new-admin@example.com","role":"operator"}`)
	assert.Equal(t, http.StatusForbidden, rec.Code, "M2: super_admin以外は403")
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"forbidden", p.Type)
	assert.Empty(t, d.inviteRepo.issued, "認可不足で業務処理へ進まない")
	assert.Empty(t, d.limiter.calls, "M2はレート評価より前")
}

// UC-011 主成功: super_adminが未登録メールへ招待→200・発行（ロール紐付け・TTL24h・hash64）・招待メール送信
func TestUC011_InviteAdmin_Success_Returns200_IssuesAndSends(t *testing.T) {
	d := newTestDeps()
	key := testSigningKey(t)
	h := newAuthTestEchoKeyed(t, d, key)

	rec := postInvite(t, h, superAdminAT(t, key), `{"email":"new-admin@example.com","role":"operator"}`)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, d.inviteRepo.issued, 1)
	assert.Equal(t, "new-admin@example.com", d.inviteRepo.issued[0])
	assert.Equal(t, "operator", d.inviteRepo.roles[0], "FR-11: 指定ロールをDBレコード紐付けで発行")
	assert.Len(t, d.inviteRepo.tokens[0].Hash, 64, "sha256 hex digest")
	assert.True(t, d.inviteRepo.tokens[0].ExpiresAt.After(time.Now().Add(23*time.Hour)), "VAR-07: 24時間")
	require.Len(t, d.mailer.sentInvites, 1)
	assert.True(t, strings.HasPrefix(d.mailer.sentInvites[0], "new-admin@example.com:"))
}

// UC-011 監査INFO: 主成功で「管理者招待」ログをinvitation_token_idで記録し、招待先メールアドレスは含めない（NFR-09・BUC-A01備考）
func TestUC011_InviteAdmin_Success_AuditLogsTokenIdNotEmail(t *testing.T) {
	var buf bytes.Buffer
	ctx := applog.ContextWithLogger(context.Background(), applog.New(&buf, "auth-service"))
	repo := &fakeInviteRepository{users: map[string]fakeInviteUser{}}
	err := command.NewInviteAdminHandler(repo, &fakeRateLimiter{blocked: map[string]bool{}}, &fakeMailer{}).
		Handle(ctx, "new-admin@example.com", "operator")
	require.NoError(t, err)

	assert.Contains(t, buf.String(), "管理者招待", "監査INFO（NFR-07）")
	assert.Contains(t, buf.String(), `"ctx":"admin_invitation"`)
	assert.Contains(t, buf.String(), "invitation_token_id")
	assert.NotContains(t, buf.String(), "new-admin@example.com", "NFR-09: 招待先メールアドレスをログに含めない")
}

// UC-011 E3: レート超過は429・retry_after=TTL残秒（切り上げ）。存在・状態に依らず一様（VAR-14）
func TestUC011_InviteAdmin_RateLimited_Returns429_Uniform(t *testing.T) {
	for name, seed := range map[string]func(*testDeps){
		"未登録":    func(*testDeps) {},
		"既存user": func(d *testDeps) { d.inviteRepo.users["limited@example.com"] = fakeInviteUser{roles: []string{"user"}} },
	} {
		t.Run(name, func(t *testing.T) {
			d := newTestDeps()
			seed(d)
			d.limiter.blocked["limited@example.com"] = true
			d.limiter.retryAfter = 239*time.Second + 500*time.Millisecond // 端数TTL: Ceilでないと240にならない（Floor/Round=239を検出）

			key := testSigningKey(t)
			rec := postInvite(t, newAuthTestEchoKeyed(t, d, key), superAdminAT(t, key), `{"email":"limited@example.com","role":"operator"}`)

			assert.Equal(t, http.StatusTooManyRequests, rec.Code, "レートは業務評価の先頭・一様429")
			var p commonhttp.Problem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			assert.Equal(t, commonhttp.ProblemTypeBase+"rate-limit-exceeded", p.Type)
			require.NotNil(t, p.RetryAfter)
			assert.Equal(t, 240, *p.RetryAfter, "retry_after はTTL残秒（切り上げ・VAR-14③）")
			assert.Empty(t, d.inviteRepo.issued)
		})
	}
}

// UC-011 E1/E2: 形式不正（VAR-01・254超）・無効ロール（VAR-09外）は400 validation-error。
// レート記録は形式検証より先に確定（VAR-14①）
func TestUC011_InviteAdmin_InvalidInput_Returns400_AfterRateRecorded(t *testing.T) {
	longEmail := strings.Repeat("a", 245) + "@example.com"
	for name, c := range map[string]struct{ email, role string }{
		"E1_254超メール": {longEmail, "operator"},
		"E2_無効ロール":   {"new-admin@example.com", "user"},
		"E2_未知のロール":  {"new-admin@example.com", "bogus_role"},
	} {
		t.Run(name, func(t *testing.T) {
			d := newTestDeps()

			key := testSigningKey(t)
			rec := postInvite(t, newAuthTestEchoKeyed(t, d, key), superAdminAT(t, key), `{"email":"`+c.email+`","role":"`+c.role+`"}`)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			var p commonhttp.Problem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			assert.Equal(t, commonhttp.ProblemTypeBase+"validation-error", p.Type)
			assert.Contains(t, d.limiter.calls, c.email, "レート記録は形式検証より先に確定（VAR-14①）")
			assert.Empty(t, d.inviteRepo.issued)
		})
	}
}

// UC-011 E4/E6: 管理者ロール付与済みは409 role-already-assigned・既存アカウント（一般user）は409 email-already-registered（Q-3遮断）
func TestUC011_InviteAdmin_ExistingAccount_Returns409(t *testing.T) {
	t.Run("E4_管理者ロール付与済み", func(t *testing.T) {
		d := newTestDeps()
		d.inviteRepo.users["admin@example.com"] = fakeInviteUser{roles: []string{"user", "operator"}}

		key := testSigningKey(t)
		rec := postInvite(t, newAuthTestEchoKeyed(t, d, key), superAdminAT(t, key), `{"email":"admin@example.com","role":"operator"}`)

		assert.Equal(t, http.StatusConflict, rec.Code)
		var p commonhttp.Problem
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
		assert.Equal(t, commonhttp.ProblemTypeBase+"role-already-assigned", p.Type)
		assert.Empty(t, d.inviteRepo.issued)
	})
	t.Run("E6_既存エンドユーザー", func(t *testing.T) {
		d := newTestDeps()
		d.inviteRepo.users["enduser@example.com"] = fakeInviteUser{roles: []string{"user"}}

		key := testSigningKey(t)
		rec := postInvite(t, newAuthTestEchoKeyed(t, d, key), superAdminAT(t, key), `{"email":"enduser@example.com","role":"operator"}`)

		assert.Equal(t, http.StatusConflict, rec.Code)
		var p commonhttp.Problem
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
		assert.Equal(t, commonhttp.ProblemTypeBase+"email-already-registered", p.Type, "Q-3: 招待時遮断・昇格はBUC-A07経路")
		assert.Empty(t, d.inviteRepo.issued)
	})
}

// UC-011 E5: メール送信失敗は503・発行Txを全ロールバック（レート窓は消費したまま）
func TestUC011_InviteAdmin_MailDeliveryFails_RollsBack_Returns503(t *testing.T) {
	d := newTestDeps()
	d.mailer.sendError = errors.New("smtp connection refused")

	key := testSigningKey(t)
	rec := postInvite(t, newAuthTestEchoKeyed(t, d, key), superAdminAT(t, key), `{"email":"new-admin@example.com","role":"operator"}`)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"mail-delivery-error", p.Type)
	assert.Empty(t, d.inviteRepo.issued, "E5: 送信失敗時は発行をロールバック")
	assert.Contains(t, d.limiter.calls, "new-admin@example.com", "レート窓は消費したまま（VAR-14①）")
}
