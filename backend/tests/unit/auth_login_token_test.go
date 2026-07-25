package unit

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"poc-app-hydra/backend/auth/domain"
)

// UC-005: アクセストークンはRS256で署名され sub/roles/exp クレームを持つ（NFR-02/16・VAR-03）
func TestUC005_GenerateAccessToken_RS256_WithClaims(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	userID := "user-123"
	roles := []string{"user", "operator"}
	signed, err := domain.GenerateAccessToken(key, userID, roles, 15*time.Minute)
	require.NoError(t, err)

	parsed, err := jwt.Parse(signed, func(tok *jwt.Token) (interface{}, error) {
		require.Equal(t, "RS256", tok.Method.Alg(), "NFR-02: 署名アルゴリズムはRS256")
		return &key.PublicKey, nil
	})
	require.NoError(t, err)
	require.True(t, parsed.Valid)

	claims, ok := parsed.Claims.(jwt.MapClaims)
	require.True(t, ok)
	assert.Equal(t, userID, claims["sub"])
	rawRoles, ok := claims["roles"].([]interface{})
	require.True(t, ok, "NFR-16: roles は配列")
	assert.ElementsMatch(t, []interface{}{"user", "operator"}, rawRoles)
	exp, err := claims.GetExpirationTime()
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(15*time.Minute), exp.Time, 5*time.Second)
}

// UC-005: リフレッシュトークンはcrypto/rand 32バイト・base64url、返却は平文・保存はSHA-256ハッシュ（NFR-14）
func TestUC005_GenerateRefreshToken_OpaqueAndHashed(t *testing.T) {
	plain, hash, err := domain.GenerateRefreshToken()
	require.NoError(t, err)

	raw, err := base64.RawURLEncoding.DecodeString(plain)
	require.NoError(t, err, "base64urlでデコードできる")
	assert.Len(t, raw, 32, "NFR-14: 32バイトのランダム値")

	sum := sha256.Sum256([]byte(plain))
	assert.Equal(t, hex.EncodeToString(sum[:]), hash, "保存値は平文のSHA-256")
	assert.NotEqual(t, plain, hash, "平文とハッシュは異なる")

	plain2, _, err := domain.GenerateRefreshToken()
	require.NoError(t, err)
	assert.NotEqual(t, plain, plain2, "毎回異なる")
}

// UC-005: PEM（PKCS#1/PKCS#8）から RSA 秘密鍵をロードできる（Q-2）
func TestUC005_ParseRSAPrivateKeyPEM(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pkcs1 := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	got1, err := domain.ParseRSAPrivateKeyPEM(pkcs1)
	require.NoError(t, err)
	assert.Equal(t, key.D, got1.D)

	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	pkcs8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	got8, err := domain.ParseRSAPrivateKeyPEM(pkcs8)
	require.NoError(t, err)
	assert.Equal(t, key.D, got8.D)

	_, err = domain.ParseRSAPrivateKeyPEM([]byte("not a pem"))
	assert.Error(t, err)
}

// UC-005: パスワード検証（CND-02）とダミー検証（E3・NFR-03 timing attack対策）
func TestUC005_VerifyPassword_AndDummy(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret-passw0rd!"), 12)
	require.NoError(t, err)

	assert.NoError(t, domain.VerifyPassword(string(hash), "secret-passw0rd!"), "一致で成功")
	assert.Error(t, domain.VerifyPassword(string(hash), "wrong"), "不一致でエラー")

	// ダミー検証は完全比較まで到達して不一致を返す（早期リターンでtiming対策が無効化する退行を検知・NFR-03）
	assert.ErrorIs(t, domain.DummyPasswordVerify("any-password"), bcrypt.ErrMismatchedHashAndPassword)
}

// UC-005 / NFR-03: ダミーハッシュのコストは register の PasswordBcryptCost と一致する
// （片方だけ変更されるとE3/E4のtiming差が無言で再発するため構造的に結束する）
func TestUC005_DummyHashCost_MatchesRegisterCost(t *testing.T) {
	cost := domain.DummyBcryptHashCost()
	assert.Equal(t, domain.PasswordBcryptCost, cost, "ダミーと登録のbcryptコストは一致（timing整合）")
}
