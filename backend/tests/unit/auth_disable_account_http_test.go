package unit

import (
	"bytes"
	"context"
	"encoding/json"
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

type fakeDisableRepository struct {
	roles       []string
	status      string
	found       bool
	disableErr  error
	disabled    []uuid.UUID // DisableAccount呼出の対象記録
	gotSuper    bool        // 最後のDisableAccount呼出のisSuperAdmin
	disableCall int
}

func (f *fakeDisableRepository) FindAccountForDisable(ctx context.Context, userUUID uuid.UUID) ([]string, string, bool, error) {
	return f.roles, f.status, f.found, nil
}

func (f *fakeDisableRepository) DisableAccount(ctx context.Context, userUUID uuid.UUID, isSuperAdmin bool) error {
	f.disableCall++
	f.gotSuper = isSuperAdmin
	if f.disableErr != nil {
		return f.disableErr
	}
	f.disabled = append(f.disabled, userUUID)
	return nil
}

func postDisable(t *testing.T, h http.Handler, bearer, userID string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/accounts/"+userID+"/disable", nil)
	if bearer != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+bearer)
	}
	h.ServeHTTP(rec, req)
	return rec
}

// UC-014 M1/M2: AT欠落は401一様（FR-19）・super_admin以外のATは403 forbidden（CND-17・M2）。
// 認可不足では業務処理（DisableAccount）へ進まない
func TestUC014_DisableAccount_AuthzEnforced_401And403(t *testing.T) {
	d := newTestDeps()
	key := testSigningKey(t)
	h := newAuthTestEchoKeyed(t, d, key)
	target := uuid.New().String()

	noAT := postDisable(t, h, "", target)
	assert.Equal(t, http.StatusUnauthorized, noAT.Code, "M1: AT欠落は401一様")
	assert.Equal(t, "Bearer", noAT.Header().Get("WWW-Authenticate"))

	userAT, err := domain.GenerateAccessToken(key, "user-1", []string{"user"}, time.Minute)
	require.NoError(t, err)
	rec := postDisable(t, h, userAT, target)
	assert.Equal(t, http.StatusForbidden, rec.Code, "M2: super_admin以外は403")
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"forbidden", p.Type)
	assert.Zero(t, d.disableRepo.disableCall, "認可不足で業務処理へ進まない")
}

// UC-014 主成功: super_adminがoperatorを無効化→200・revocation_reason=account_disabled・
// CND-14計数はsuper_admin対象時のみ（operator対象はisSuperAdmin=false）
func TestUC014_DisableAccount_Success_Returns200_Operator(t *testing.T) {
	d := newTestDeps()
	d.disableRepo.found = true
	d.disableRepo.roles = []string{domain.RoleOperator}
	d.disableRepo.status = domain.StatusInactive
	key := testSigningKey(t)
	target := uuid.New()

	rec := postDisable(t, newAuthTestEchoKeyed(t, d, key), superAdminAT(t, key), target.String())

	assert.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		RevocationReason string `json:"revocation_reason"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "account_disabled", body.RevocationReason, "FR-15・VAR-10")
	require.Equal(t, []uuid.UUID{target}, d.disableRepo.disabled)
	assert.False(t, d.disableRepo.gotSuper, "operator対象はCND-14計数対象外（isSuperAdmin=false）")
}

// UC-014: super_admin対象の無効化はisSuperAdmin=trueで委譲する（Tx内でCND-14計数を発火させる）
func TestUC014_DisableAccount_SuperAdminTarget_PassesSuperFlag(t *testing.T) {
	d := newTestDeps()
	d.disableRepo.found = true
	d.disableRepo.roles = []string{domain.RoleSuperAdmin}
	d.disableRepo.status = domain.StatusInactive
	key := testSigningKey(t)

	rec := postDisable(t, newAuthTestEchoKeyed(t, d, key), superAdminAT(t, key), uuid.New().String())

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, d.disableRepo.gotSuper, "super_admin対象はCND-14計数のためisSuperAdmin=trueで委譲")
}

// UC-014 E1: 対象不存在・UUID形式不正はいずれも404 user-not-found（存在有無の情報漏洩回避）。業務Txへ進まない
func TestUC014_DisableAccount_NotFoundOrMalformed_Returns404(t *testing.T) {
	key := testSigningKey(t)
	for name, target := range map[string]string{
		"不存在":      uuid.New().String(),
		"UUID形式不正": "not-a-uuid",
	} {
		t.Run(name, func(t *testing.T) {
			d := newTestDeps()
			d.disableRepo.found = false // 不存在（形式不正はParse段で合流）

			rec := postDisable(t, newAuthTestEchoKeyed(t, d, key), superAdminAT(t, key), target)

			assert.Equal(t, http.StatusNotFound, rec.Code)
			var p commonhttp.Problem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			assert.Equal(t, commonhttp.ProblemTypeBase+"user-not-found", p.Type)
			assert.Zero(t, d.disableRepo.disableCall, "E1で業務Txへ進まない")
		})
	}
}

// UC-014 E2: 対象が管理者ロール未保持は400 not-admin-account。業務Txへ進まない
func TestUC014_DisableAccount_NotAdmin_Returns400(t *testing.T) {
	d := newTestDeps()
	d.disableRepo.found = true
	d.disableRepo.roles = []string{"user"}
	d.disableRepo.status = domain.StatusInactive
	key := testSigningKey(t)

	rec := postDisable(t, newAuthTestEchoKeyed(t, d, key), superAdminAT(t, key), uuid.New().String())

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"not-admin-account", p.Type)
	assert.Zero(t, d.disableRepo.disableCall, "E2で業務Txへ進まない")
}

// UC-014 E3: 読取時点で既に無効化済みは409 account-already-disabled。業務Txへ進まない
func TestUC014_DisableAccount_AlreadyDisabled_Returns409(t *testing.T) {
	d := newTestDeps()
	d.disableRepo.found = true
	d.disableRepo.roles = []string{domain.RoleOperator}
	d.disableRepo.status = domain.StatusDisabled
	key := testSigningKey(t)

	rec := postDisable(t, newAuthTestEchoKeyed(t, d, key), superAdminAT(t, key), uuid.New().String())

	assert.Equal(t, http.StatusConflict, rec.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"account-already-disabled", p.Type)
	assert.Zero(t, d.disableRepo.disableCall, "E3読取チェックで業務Txへ進まない")
}

// UC-014 E4: 最後の稼働中super_adminは409 last-super-admin（Tx内CND-14計数がErrLastSuperAdminを返す）
func TestUC014_DisableAccount_LastSuperAdmin_Returns409(t *testing.T) {
	d := newTestDeps()
	d.disableRepo.found = true
	d.disableRepo.roles = []string{domain.RoleSuperAdmin}
	d.disableRepo.status = domain.StatusInactive
	d.disableRepo.disableErr = command.ErrLastSuperAdmin
	key := testSigningKey(t)

	rec := postDisable(t, newAuthTestEchoKeyed(t, d, key), superAdminAT(t, key), uuid.New().String())

	assert.Equal(t, http.StatusConflict, rec.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"last-super-admin", p.Type)
}

// UC-014 監査INFO: 主成功で「アカウント無効化」をtarget/operatorのUUIDで記録する（NFR-07・BUC-A04）
func TestUC014_DisableAccount_Success_AuditLogsTargetAndOperator(t *testing.T) {
	var buf bytes.Buffer
	ctx := applog.ContextWithLogger(context.Background(), applog.New(&buf, "auth-service"))
	target := uuid.New()
	repo := &fakeDisableRepository{found: true, roles: []string{domain.RoleOperator}, status: domain.StatusInactive}

	err := command.NewDisableAccountHandler(repo).Handle(ctx, "operator-1", target.String())
	require.NoError(t, err)

	assert.Contains(t, buf.String(), "アカウント無効化", "監査INFO（NFR-07）")
	assert.Contains(t, buf.String(), `"ctx":"account_disable"`)
	assert.Contains(t, buf.String(), target.String(), "target_user_id")
	assert.Contains(t, buf.String(), "operator-1", "operator（AT sub）")
}
