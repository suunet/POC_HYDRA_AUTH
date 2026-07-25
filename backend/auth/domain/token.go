package domain

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// NFR-14: リフレッシュトークンのランダム値バイト数（crypto/rand）
const refreshTokenBytes = 32

// ParseRSAPrivateKeyPEM は PEM（PKCS#1 または PKCS#8）から RSA 秘密鍵を読む（Q-2: JWT_PRIVATE_KEY_PATH 経由で注入）。
func ParseRSAPrivateKeyPEM(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in JWT private key")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("could not parse RSA private key (PKCS1/PKCS8): %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("JWT private key is not RSA")
	}
	return key, nil
}

// GenerateAccessToken は RS256 で署名したアクセストークンを返す（NFR-02・INF-03）。
// クレーム: sub（ユーザーID）・roles（文字列配列・NFR-16）・exp（VAR-03）・iat。
func GenerateAccessToken(key *rsa.PrivateKey, userID string, roles []string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":   userID,
		"roles": roles,
		"iat":   now.Unix(),
		"exp":   now.Add(ttl).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("could not sign access token: %w", err)
	}
	return signed, nil
}

// GenerateRefreshToken は opaque token を生成し、クライアント返却用の平文（base64url）と
// DB保存用のSHA-256ハッシュ（hex）を返す（NFR-14: 平文は永続化しない）。
func GenerateRefreshToken() (plain, hash string, err error) {
	buf := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("could not generate refresh token: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(buf)
	return plain, HashRefreshToken(plain), nil
}

// HashRefreshToken は照合用にリフレッシュトークン平文のSHA-256ハッシュ（hex）を返す。
func HashRefreshToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// VerifyPassword は bcrypt ハッシュと平文を照合する（CND-02）。不一致・不正ハッシュはエラー。
func VerifyPassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

// DummyPasswordVerify はユーザー不存在時（E3）に実行する timing attack 対策のダミー検証（NFR-03）。
// 実在ユーザーの検証と同等の計算コストを費やし、常に ErrMismatchedHashAndPassword を返す
// （dummyBcryptHash の元平文は生成時に破棄済みのため、いかなる入力とも一致しない＝契約を構造的に保証）。
func DummyPasswordVerify(password string) error {
	return bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
}

// bcryptコスト12（NFR-01）の固定ダミーハッシュ。照合コストを実在ユーザーと揃えるためのもの。
// 生成方法: 推測不能なランダム32バイト（base64url）を平文として `bcrypt.GenerateFromPassword(pw, 12)` で生成し、
// 平文は破棄した（元平文が存在しないため CompareHashAndPassword は常に不一致で完全比較まで到達する）。
var dummyBcryptHash = []byte("$2a$12$/KPcFQoQrZnRME3ehYSpZOEALvYljQpop1F4uYX0oSdYWvaD77YVS")
