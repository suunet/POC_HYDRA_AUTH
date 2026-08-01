package unit

import (
	"bytes"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

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

func newLogoutTestEcho(t *testing.T, repo *fakeRefreshRepository, key *rsa.PrivateKey) http.Handler {
	t.Helper()
	d := newTestDeps()
	e := commonhttp.NewEcho(applog.New(&bytes.Buffer{}, "auth-service"))
	apihttp.Register(e, apihttp.NewHandler(
		command.NewRegisterAccountHandler(d.repo, d.limiter, d.mailer),
		command.NewVerifyEmailHandler(&fakeTokenRepository{}, &fakeRateLimiter{blocked: map[string]bool{}}),
		command.NewResendEmailVerificationHandler(d.resendRepo, d.limiter, d.mailer),
		command.NewLoginHandler(&fakeLoginRepository{}, &fakeLockout{}, key),
		command.NewRefreshTokenHandler(&fakeRefreshRepository{}, key),
		command.NewLogoutHandler(repo),
	), commonhttp.JWTAuth(&key.PublicKey))
	return e
}

func postLogout(t *testing.T, h http.Handler, authorization, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	h.ServeHTTP(rec, req)
	return rec
}

func accessTokenFor(t *testing.T, key *rsa.PrivateKey, userUUID uuid.UUID) string {
	t.Helper()
	token, err := domain.GenerateAccessToken(key, userUUID.String(), []string{"user"}, time.Minute)
	require.NoError(t, err)
	return token
}

// UC-007 主成功: 有効AT＋自分のRTで200・当該1本失効（reason NULL）
func TestUC007_LogoutHTTP_Success_Returns200_Revokes(t *testing.T) {
	key := testSigningKey(t)
	userUUID := uuid.New()
	rec := validStored(userUUID, uuid.New())
	repo := &fakeRefreshRepository{record: rec, found: true}

	res := postLogout(t, newLogoutTestEcho(t, repo, key),
		"Bearer "+accessTokenFor(t, key, userUUID), `{"refresh_token":"my-token"}`)

	require.Equal(t, http.StatusOK, res.Code)
	require.Equal(t, []uuid.UUID{rec.TokenID}, repo.revokedSingle)
	assert.Nil(t, repo.revokedReasons[0], "reason NULL（FR-07）")
}

// UC-007 M1: AT無しは401 invalid-token（FR-19ミドルウェアがルート適用されている）
func TestUC007_LogoutHTTP_NoToken_Returns401(t *testing.T) {
	key := testSigningKey(t)
	res := postLogout(t, newLogoutTestEcho(t, &fakeRefreshRepository{}, key), "", `{"refresh_token":"x"}`)

	require.Equal(t, http.StatusUnauthorized, res.Code)
	assert.Contains(t, problemType(t, res), "invalid-token")
	assert.Equal(t, "Bearer", res.Header().Get("WWW-Authenticate"))
}

// FR-19 ルート限定: ミドルウェアは /auth/logout のみに適用され、他ルート（login）はAT無しで通る
func TestUC007_LogoutHTTP_MiddlewareScopedToLogoutRoute(t *testing.T) {
	key := testSigningKey(t)
	h := newLogoutTestEcho(t, &fakeRefreshRepository{}, key)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login",
		strings.NewReader(`{"email":"a@example.com","password":"secret-passw0rd!"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	// NOTE: fakeLoginRepositoryは不存在ユーザー＝401 authentication-failed（loginの正当な応答）。
	// ミドルウェア誤適用なら type=invalid-token＋WWW-Authenticate が付くため、そこで判別する
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, problemType(t, rec), "authentication-failed", "loginの401はlogin自身の応答（FR-19の401ではない）")
	assert.NotContains(t, problemType(t, rec), "invalid-token", "FR-19はlogoutルート限定")
	assert.Empty(t, rec.Header().Get("WWW-Authenticate"), "FR-19はlogoutルート限定")
}

// UC-007 E1: 有効AT＋空RTは400 validation-error
func TestUC007_LogoutHTTP_EmptyRefreshToken_Returns400(t *testing.T) {
	key := testSigningKey(t)
	res := postLogout(t, newLogoutTestEcho(t, &fakeRefreshRepository{}, key),
		"Bearer "+accessTokenFor(t, key, uuid.New()), `{"refresh_token":""}`)

	require.Equal(t, http.StatusBadRequest, res.Code)
	assert.Contains(t, problemType(t, res), "validation-error")
}

// UC-007 A1: 不存在トークンでも200（冪等・存在有無を漏洩しない）
func TestUC007_LogoutHTTP_UnknownToken_Returns200(t *testing.T) {
	key := testSigningKey(t)
	repo := &fakeRefreshRepository{found: false}
	res := postLogout(t, newLogoutTestEcho(t, repo, key),
		"Bearer "+accessTokenFor(t, key, uuid.New()), `{"refresh_token":"unknown"}`)

	require.Equal(t, http.StatusOK, res.Code)
	assert.Empty(t, repo.revokedSingle)
}

// FR-19 トリップワイヤ: openapiで `security` 宣言された全操作は、AT無しリクエストで401 invalid-tokenになる
// （protectedRouterの保護マップとopenapi契約の二重管理の乖離＝黙って非保護になる事故を実挙動で検知する）
func TestUC007_LogoutHTTP_OpenAPISecurityDeclarations_AreEnforced(t *testing.T) {
	data, err := os.ReadFile("../../auth/api/http/openapi.yaml")
	require.NoError(t, err)

	// NOTE: 本リポジトリのopenapi整形（paths直下=2スペース・操作直下=6スペース）前提の行パース。
	// yaml.v3の直接依存昇格を避けた構成のため、下の自己検査（/auth/logout検出必須）でパーサ破れを検知する
	pathRe := regexp.MustCompile(`^  (/[^:]+):$`)
	var protected []string
	current := ""
	for _, line := range strings.Split(string(data), "\n") {
		if m := pathRe.FindStringSubmatch(line); m != nil {
			current = m[1]
		}
		if strings.HasPrefix(line, "      security:") && current != "" {
			protected = append(protected, current)
		}
	}
	require.Contains(t, protected, "/auth/logout", "パーサ自己検査: security宣言済みのlogoutを検出できない場合はパース前提が破れている")

	h := newLogoutTestEcho(t, &fakeRefreshRepository{}, testSigningKey(t))
	for _, p := range protected {
		t.Run(p, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, p, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			h.ServeHTTP(rec, req)
			require.Equal(t, http.StatusUnauthorized, rec.Code, "security宣言済み操作はAT無しで401（保護マップの乖離検知）")
			assert.Contains(t, problemType(t, rec), "invalid-token")
			assert.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"))
		})
	}
}

// UC-007: FR-19配線が欠落してもハンドラのfail-closedガードが401を返す（ミドルウェア素通し状態のピン留め）
func TestUC007_LogoutHTTP_MissingMiddlewareWiring_FailsClosed401(t *testing.T) {
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
	), passthrough)

	res := postLogout(t, e, "Bearer "+accessTokenFor(t, key, uuid.New()), `{"refresh_token":"x"}`)
	require.Equal(t, http.StatusUnauthorized, res.Code, "claims欠落はfail-closedで401（安全網）")
	assert.Contains(t, problemType(t, res), "invalid-token")
}

// UC-007 E2: 他ユーザーのRTでも200・失効なし（正常応答と同一＝所有情報を漏洩しない）
func TestUC007_LogoutHTTP_OthersToken_Returns200_NoRevoke(t *testing.T) {
	key := testSigningKey(t)
	owner := uuid.New()
	rec := validStored(owner, uuid.New())
	repo := &fakeRefreshRepository{record: rec, found: true}

	res := postLogout(t, newLogoutTestEcho(t, repo, key),
		"Bearer "+accessTokenFor(t, key, uuid.New()), `{"refresh_token":"someones"}`)

	require.Equal(t, http.StatusOK, res.Code, "E2も正常応答と同一")
	assert.Empty(t, repo.revokedSingle, "失効は行わない")
	assert.Empty(t, repo.revokedFamily)
}
