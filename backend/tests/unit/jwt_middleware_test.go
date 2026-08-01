package unit

import (
	"bytes"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend/auth/domain"
	commonhttp "poc-app-hydra/backend/common/http"
	applog "poc-app-hydra/backend/common/log"
)

// newJWTAuthTestEcho は FR-19 ミドルウェアをルート適用した検証用サーバを組む。
// 保護ルートは context から取り出した sub/roles をそのまま返す（格納の観測用）。
func newJWTAuthTestEcho(t *testing.T, pub *rsa.PublicKey) (http.Handler, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	e := commonhttp.NewEcho(applog.New(&buf, "test"))
	e.POST("/protected", func(c echo.Context) error {
		claims, ok := commonhttp.AuthClaimsFromContext(c.Request().Context())
		require.True(t, ok, "検証成功時はclaimsがcontextに格納される")
		return c.JSON(http.StatusOK, map[string]any{"sub": claims.UserID, "roles": claims.Roles})
	}, commonhttp.JWTAuth(pub))
	return e, &buf
}

func postProtected(t *testing.T, h http.Handler, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/protected", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	h.ServeHTTP(rec, req)
	return rec
}

// FR-19 / UC-007 主成功2: 有効なRS256トークンで通過し、sub/roles（NFR-16）がcontextへ格納される
func TestFR19_JWTAuth_ValidToken_PassesAndStoresClaims(t *testing.T) {
	key := testSigningKey(t)
	token, err := domain.GenerateAccessToken(key, "user-uuid-1", []string{"user", "operator"}, time.Minute)
	require.NoError(t, err)

	h, _ := newJWTAuthTestEcho(t, &key.PublicKey)
	rec := postProtected(t, h, "Bearer "+token)

	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Sub   string   `json:"sub"`
		Roles []string `json:"roles"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "user-uuid-1", body.Sub)
	assert.Equal(t, []string{"user", "operator"}, body.Roles, "rolesは文字列配列（NFR-16）")

	// RFC 6750: authスキームは大文字小文字非区別（"bearer" でも通過する）
	lower := postProtected(t, h, "bearer "+token)
	assert.Equal(t, http.StatusOK, lower.Code, "小文字schemeも受理（RFC 6750）")
}

// FR-19 / UC-007 M1: 検証失敗4種はすべて401 invalid-tokenで一様（種別を応答で区別しない）＋WWW-Authenticate
func TestFR19_JWTAuth_Failures_Return401Uniformly(t *testing.T) {
	key := testSigningKey(t)
	otherKey := testSigningKey(t)

	expired, err := domain.GenerateAccessToken(key, "user-uuid-1", []string{"user"}, -time.Minute)
	require.NoError(t, err)
	wrongKey, err := domain.GenerateAccessToken(otherKey, "user-uuid-1", []string{"user"}, time.Minute)
	require.NoError(t, err)
	// alg混同攻撃の模擬: HS256で署名したトークン（RS256強制で拒否されるべき）
	hs256, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-uuid-1", "roles": []string{"user"}, "exp": time.Now().Add(time.Minute).Unix(),
	}).SignedString([]byte("hmac-secret"))
	require.NoError(t, err)
	// claims不正の模擬（正鍵署名）: exp欠落／sub欠落 — sentinel外は安全側で改ざん検知に分類される
	noExp, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": "user-uuid-1", "roles": []string{"user"},
	}).SignedString(key)
	require.NoError(t, err)
	noSub, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"roles": []string{"user"}, "exp": time.Now().Add(time.Minute).Unix(),
	}).SignedString(key)
	require.NoError(t, err)

	cases := []struct {
		name          string
		authorization string
		wantLogMsg    string
	}{
		{"AT欠落", "", "アクセストークン欠落"},
		{"Bearer形式でない", "Basic abc", "アクセストークン欠落"},
		{"トークン空（Bearerのみ）", "Bearer ", "アクセストークン欠落"},
		{"フォーマット不正", "Bearer not-a-jwt", "JWTフォーマット不正"},
		{"署名不正（別鍵）", "Bearer " + wrongKey, "JWT署名不正・改ざん検知"},
		{"alg混同（HS256）", "Bearer " + hs256, "JWT署名不正・改ざん検知"},
		{"exp欠落（正鍵）", "Bearer " + noExp, "JWT署名不正・改ざん検知"},
		{"sub欠落（正鍵）", "Bearer " + noSub, "JWT署名不正・改ざん検知"},
		{"期限切れ", "Bearer " + expired, "アクセストークン期限切れ"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, buf := newJWTAuthTestEcho(t, &key.PublicKey)
			rec := postProtected(t, h, c.authorization)

			require.Equal(t, http.StatusUnauthorized, rec.Code)
			assert.Contains(t, problemType(t, rec), "invalid-token", "失敗種別を問わず一様typeで返す")
			assert.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"))
			// ログ種別: 署名不正/フォーマット不正=NFR-07監査WARNING・欠落/期限切れ=ビジネス例外WARNING（UC-007 M1）
			assert.Contains(t, buf.String(), c.wantLogMsg)
			assert.Contains(t, buf.String(), "jwt_verify", "ctx=jwt_verify（UC-007 M1）")
			assert.Contains(t, buf.String(), "WARN")
		})
	}
}

// FR-19: 検証失敗時にトークン本体をログへ含めない（NFR-09・秘匿）
func TestFR19_JWTAuth_Failure_DoesNotLogTokenBody(t *testing.T) {
	key := testSigningKey(t)
	otherKey := testSigningKey(t)
	wrongKey, err := domain.GenerateAccessToken(otherKey, "user-uuid-1", []string{"user"}, time.Minute)
	require.NoError(t, err)

	h, buf := newJWTAuthTestEcho(t, &key.PublicKey)
	rec := postProtected(t, h, "Bearer "+wrongKey)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.NotContains(t, buf.String(), wrongKey, "アクセストークン本体をログに含めない")
}
