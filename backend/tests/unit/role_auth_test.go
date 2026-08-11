package unit

import (
	"bytes"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend/auth/domain"
	commonhttp "poc-app-hydra/backend/common/http"
	applog "poc-app-hydra/backend/common/log"
)

// roleAuthTestEcho は FR-19（JWTAuth）→ロール認可（RequireRoles）の順で保護したダミールートを組む
func roleAuthTestEcho(t *testing.T, key *rsa.PrivateKey, requiredRoles ...string) http.Handler {
	t.Helper()
	e := commonhttp.NewEcho(applog.New(&bytes.Buffer{}, "auth-service"))
	e.POST("/protected", func(c echo.Context) error { return c.NoContent(http.StatusOK) },
		commonhttp.JWTAuth(&key.PublicKey), commonhttp.RequireRoles("role_auth_test", requiredRoles...))
	return e
}

func postRoleProtected(t *testing.T, h http.Handler, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/protected", nil)
	if bearer != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+bearer)
	}
	h.ServeHTTP(rec, req)
	return rec
}

// CND-17/FR-19（UC-011 M2の基盤）: ロール認可はOR評価——要求ロールのいずれか1つを持てば許可する
func TestFR19_RequireRoles_OrEvaluation_AllowsAnyMatch(t *testing.T) {
	key := testSigningKey(t)
	h := roleAuthTestEcho(t, key, "super_admin", "operator")
	at, err := domain.GenerateAccessToken(key, "user-1", []string{"user", "operator"}, time.Minute)
	require.NoError(t, err)

	rec := postRoleProtected(t, h, at)

	assert.Equal(t, http.StatusOK, rec.Code, "CND-17: いずれか1つ（operator）の一致で許可")
}

// CND-17/FR-19（UC-011 M2）: 要求ロールを1つも持たない場合は403 forbidden（401=認証失敗と区別）
func TestFR19_RequireRoles_InsufficientRole_Returns403Forbidden(t *testing.T) {
	key := testSigningKey(t)
	h := roleAuthTestEcho(t, key, "super_admin")
	at, err := domain.GenerateAccessToken(key, "user-1", []string{"user", "operator"}, time.Minute)
	require.NoError(t, err)

	rec := postRoleProtected(t, h, at)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"forbidden", p.Type)
	assert.Empty(t, rec.Header().Get("WWW-Authenticate"), "認証は成立済み＝チャレンジヘッダは付けない")
}

// FR-19: JWTAuth未経由（claims欠落）はfail-closedで401 invalid-token（配線欠落の安全網・UC-010先例と同型）
func TestFR19_RequireRoles_MissingClaims_FailsClosed401(t *testing.T) {
	key := testSigningKey(t)
	e := commonhttp.NewEcho(applog.New(&bytes.Buffer{}, "auth-service"))
	// NOTE: JWTAuthを意図的に配線しない＝ミドルウェア順序の事故を模す
	e.POST("/protected", func(c echo.Context) error { return c.NoContent(http.StatusOK) },
		commonhttp.RequireRoles("role_auth_test", "super_admin"))
	at, err := domain.GenerateAccessToken(key, "user-1", []string{"super_admin"}, time.Minute)
	require.NoError(t, err)

	rec := postRoleProtected(t, e, at)

	assert.Equal(t, http.StatusUnauthorized, rec.Code, "claims欠落はfail-closedで401")
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"invalid-token", p.Type)
	assert.Empty(t, rec.Header().Get("WWW-Authenticate"), "安全網はM1でない＝チャレンジヘッダを付けない（UC-010先例同型）")
}
