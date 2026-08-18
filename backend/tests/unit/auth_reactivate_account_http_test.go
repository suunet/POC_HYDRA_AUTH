package unit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend/auth/app/command"
	"poc-app-hydra/backend/auth/domain"
	commonhttp "poc-app-hydra/backend/common/http"
	applog "poc-app-hydra/backend/common/log"
)

type fakeReactivateRepository struct {
	roles         []string
	status        string
	found         bool
	findErr       error // FindAccountForReactivateの読取失敗注入
	reactivateErr error
	reactivated   []uuid.UUID // ReactivateAccount呼出の対象記録
	reactCall     int
}

func (f *fakeReactivateRepository) FindAccountForReactivate(ctx context.Context, userUUID uuid.UUID) ([]string, string, bool, error) {
	if f.findErr != nil {
		return nil, "", false, f.findErr
	}
	return f.roles, f.status, f.found, nil
}

func (f *fakeReactivateRepository) ReactivateAccount(ctx context.Context, userUUID uuid.UUID) error {
	f.reactCall++
	if f.reactivateErr != nil {
		return f.reactivateErr
	}
	f.reactivated = append(f.reactivated, userUUID)
	return nil
}

func postReactivate(t *testing.T, h http.Handler, bearer, userID string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/accounts/"+userID+"/reactivate", nil)
	if bearer != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+bearer)
	}
	h.ServeHTTP(rec, req)
	return rec
}

// UC-015 M1/M2: AT欠落は401一様（FR-19）・super_admin以外のATは403 forbidden（CND-17・M2）。
// 認可不足では業務処理（ReactivateAccount）へ進まない
func TestUC015_ReactivateAccount_AuthzEnforced_401And403(t *testing.T) {
	d := newTestDeps()
	key := testSigningKey(t)
	h := newAuthTestEchoKeyed(t, d, key)
	target := uuid.New().String()

	noAT := postReactivate(t, h, "", target)
	assert.Equal(t, http.StatusUnauthorized, noAT.Code, "M1: AT欠落は401一様")
	assert.Equal(t, "Bearer", noAT.Header().Get("WWW-Authenticate"))

	userAT, err := domain.GenerateAccessToken(key, "user-1", []string{"user"}, time.Minute)
	require.NoError(t, err)
	rec := postReactivate(t, h, userAT, target)
	assert.Equal(t, http.StatusForbidden, rec.Code, "M2: super_admin以外は403")
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"forbidden", p.Type)
	assert.Zero(t, d.reactivateRepo.reactCall, "認可不足で業務処理へ進まない")
}

// UC-015 主成功: super_adminが無効化済み管理者を再有効化→200（ボディなし）・disabled→inactive遷移を委譲
func TestUC015_ReactivateAccount_Success_Returns200(t *testing.T) {
	d := newTestDeps()
	d.reactivateRepo.found = true
	d.reactivateRepo.roles = []string{domain.RoleOperator}
	d.reactivateRepo.status = domain.StatusDisabled
	key := testSigningKey(t)
	target := uuid.New()

	rec := postReactivate(t, newAuthTestEchoKeyed(t, d, key), superAdminAT(t, key), target.String())

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []uuid.UUID{target}, d.reactivateRepo.reactivated)
}

// UC-015 E1: 対象不存在・UUID形式不正はいずれも404 user-not-found。業務処理へ進まない
func TestUC015_ReactivateAccount_NotFoundOrMalformed_Returns404(t *testing.T) {
	key := testSigningKey(t)
	for name, target := range map[string]string{
		"不存在":      uuid.New().String(),
		"UUID形式不正": "not-a-uuid",
	} {
		t.Run(name, func(t *testing.T) {
			d := newTestDeps()
			d.reactivateRepo.found = false

			rec := postReactivate(t, newAuthTestEchoKeyed(t, d, key), superAdminAT(t, key), target)

			assert.Equal(t, http.StatusNotFound, rec.Code)
			var p commonhttp.Problem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			assert.Equal(t, commonhttp.ProblemTypeBase+"user-not-found", p.Type)
			assert.Zero(t, d.reactivateRepo.reactCall, "E1で業務処理へ進まない")
		})
	}
}

// UC-015 E2: 対象が管理者ロール未保持は400 not-admin-account。業務処理へ進まない
func TestUC015_ReactivateAccount_NotAdmin_Returns400(t *testing.T) {
	d := newTestDeps()
	d.reactivateRepo.found = true
	d.reactivateRepo.roles = []string{"user"}
	d.reactivateRepo.status = domain.StatusDisabled
	key := testSigningKey(t)

	rec := postReactivate(t, newAuthTestEchoKeyed(t, d, key), superAdminAT(t, key), uuid.New().String())

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"not-admin-account", p.Type)
	assert.Zero(t, d.reactivateRepo.reactCall, "E2で業務処理へ進まない")
}

// UC-015 E3: 読取時点で無効化済みでないは409 account-not-disabled（CND-13）。業務処理へ進まない
func TestUC015_ReactivateAccount_NotDisabled_Returns409(t *testing.T) {
	d := newTestDeps()
	d.reactivateRepo.found = true
	d.reactivateRepo.roles = []string{domain.RoleOperator}
	d.reactivateRepo.status = domain.StatusInactive
	key := testSigningKey(t)

	rec := postReactivate(t, newAuthTestEchoKeyed(t, d, key), superAdminAT(t, key), uuid.New().String())

	assert.Equal(t, http.StatusConflict, rec.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"account-not-disabled", p.Type)
	assert.Zero(t, d.reactivateRepo.reactCall, "E3読取チェックで業務処理へ進まない")
}

// UC-015 E3: 読取〜遷移間の並行再有効化（遷移元ガード0行）も409 account-not-disabledへ合流
func TestUC015_ReactivateAccount_TransitionGuardZeroRows_Returns409(t *testing.T) {
	d := newTestDeps()
	d.reactivateRepo.found = true
	d.reactivateRepo.roles = []string{domain.RoleOperator}
	d.reactivateRepo.status = domain.StatusDisabled
	d.reactivateRepo.reactivateErr = command.ErrNotDisabled // Tx内遷移元ガード0行
	key := testSigningKey(t)

	rec := postReactivate(t, newAuthTestEchoKeyed(t, d, key), superAdminAT(t, key), uuid.New().String())

	assert.Equal(t, http.StatusConflict, rec.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"account-not-disabled", p.Type)
}

// UC-015: 再有効化失敗（未分類エラー）は500 internal-error（ERROR記録済み・SCR-13と対称の500フォールバック）
func TestUC015_ReactivateAccount_Failure_Returns500(t *testing.T) {
	d := newTestDeps()
	d.reactivateRepo.found = true
	d.reactivateRepo.roles = []string{domain.RoleOperator}
	d.reactivateRepo.status = domain.StatusDisabled
	d.reactivateRepo.reactivateErr = errors.New("boom")
	key := testSigningKey(t)

	rec := postReactivate(t, newAuthTestEchoKeyed(t, d, key), superAdminAT(t, key), uuid.New().String())

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"internal-error", p.Type)
}

// UC-015: 再有効化失敗はERRORログ「アカウント再有効化に失敗」をctx付きで記録する（NFR-08）
func TestUC015_ReactivateAccount_Failure_LogsError(t *testing.T) {
	var buf bytes.Buffer
	ctx := applog.ContextWithLogger(context.Background(), applog.New(&buf, "auth-service"))
	repo := &fakeReactivateRepository{found: true, roles: []string{domain.RoleOperator}, status: domain.StatusDisabled, reactivateErr: errors.New("boom")}

	err := command.NewReactivateAccountHandler(repo).Handle(ctx, "operator-1", uuid.New().String())
	require.Error(t, err)
	assert.Contains(t, buf.String(), "アカウント再有効化に失敗", "外部依存失敗はERROR（NFR-08）")
	assert.Contains(t, buf.String(), `"ctx":"account_reactivate"`)
}

// UC-015: 対象読取失敗（外部依存失敗）はERRORログを記録する（NFR-08・fail()ヘルパ系UseCaseへ整合。
// HTTP写像は未分類エラー共通のdefault→500 internal-error＝Failureテストが被覆）
func TestUC015_ReactivateAccount_LookupFailure_LogsError(t *testing.T) {
	var buf bytes.Buffer
	ctx := applog.ContextWithLogger(context.Background(), applog.New(&buf, "auth-service"))
	repo := &fakeReactivateRepository{findErr: errors.New("db down")}

	err := command.NewReactivateAccountHandler(repo).Handle(ctx, "operator-1", uuid.New().String())
	require.Error(t, err)
	assert.Contains(t, buf.String(), "アカウント再有効化の対象読取に失敗", "読取失敗もERROR（NFR-08観測性の対称）")
	assert.Contains(t, buf.String(), `"ctx":"account_reactivate"`)
}

// UC-015 監査INFO: 主成功で「アカウント再有効化」をtarget/operatorのUUIDで記録する（NFR-07・BUC-A05）
func TestUC015_ReactivateAccount_Success_AuditLogsTargetAndOperator(t *testing.T) {
	var buf bytes.Buffer
	ctx := applog.ContextWithLogger(context.Background(), applog.New(&buf, "auth-service"))
	target := uuid.New()
	repo := &fakeReactivateRepository{found: true, roles: []string{domain.RoleOperator}, status: domain.StatusDisabled}

	err := command.NewReactivateAccountHandler(repo).Handle(ctx, "operator-1", target.String())
	require.NoError(t, err)

	assert.Contains(t, buf.String(), "アカウント再有効化", "監査INFO（NFR-07）")
	assert.Contains(t, buf.String(), `"ctx":"account_reactivate"`)
	assert.Contains(t, buf.String(), target.String(), "target_user_id")
	assert.Contains(t, buf.String(), "operator-1", "operator（AT sub）")
}
