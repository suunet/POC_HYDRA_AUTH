package ratelimit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// HMACSHA256Hasher はレートキーをHMAC-SHA256で匿名化する（IP等の個人データ用）。
// NOTE: plain SHA-256はIPv4総当たりで逆引き可能なため秘密鍵付きHMACが必須（INF-14の規定の実装）。
type HMACSHA256Hasher struct {
	secret []byte
}

var _ KeyHasher = HMACSHA256Hasher{}

func NewHMACSHA256Hasher(secret []byte) HMACSHA256Hasher {
	return HMACSHA256Hasher{secret: secret}
}

func (h HMACSHA256Hasher) Hash(key string) string {
	mac := hmac.New(sha256.New, h.secret)
	mac.Write([]byte(key))
	return hex.EncodeToString(mac.Sum(nil))
}
