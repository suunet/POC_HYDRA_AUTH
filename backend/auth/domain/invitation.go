package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
)

// VAR-07: 招待トークン有効期限
const InvitationTokenTTL = 24 * time.Hour

// VAR-14: 同一メールアドレスへの招待リクエストは5分に1回
const InvitationRateLimitWindow = 5 * time.Minute

// VAR-09: 管理者ロール（招待で指定可能なロール）
const (
	RoleSuperAdmin  = "super_admin"
	RoleOperator    = "operator"
	RoleSystemAdmin = "system_admin"
)

var ErrInvalidAdminRole = errors.New("invalid admin role")

// IsAdminRole は管理者ロール（VAR-09）かを判定する（CND-11のロール付与済み判定・UC-011 E4）。
func IsAdminRole(role string) bool {
	switch role {
	case RoleSuperAdmin, RoleOperator, RoleSystemAdmin:
		return true
	default:
		return false
	}
}

// ValidateAdminRole は招待で指定するロールがVAR-09のいずれかであることを検証する（UC-011 E2。
// `user` は自己登録のデフォルト付与＝招待対象外・BUC-A01備考）。
func ValidateAdminRole(role string) error {
	if !IsAdminRole(role) {
		return ErrInvalidAdminRole
	}
	return nil
}

var (
	ErrInvitationTokenNotFound = errors.New("invitation token not found")

	ErrInvitationTokenConsumeConflict = errors.New("invitation token consume conflict")

	// ErrEmailAlreadyRegistered は既存ユーザーと同一メールの招待/受付（UC-011 E6・UC-012 E6・CND-11）。
	// 招待受付によるパスワード上書き＝アカウント乗っ取り経路の封止
	ErrEmailAlreadyRegistered = errors.New("email already registered")
)

// NOTE: 平文は保存せずハッシュ（SHA-256）のみ持つ（NFR-15・INF-07）
type InvitationToken struct {
	TokenUUID uuid.UUID
	Hash      string
	ExpiresAt time.Time
}

func NewInvitationToken() (plain string, token InvitationToken, err error) {
	b := make([]byte, tokenPlainBytes)
	if _, err := rand.Read(b); err != nil {
		return "", InvitationToken{}, err
	}
	plain = base64.RawURLEncoding.EncodeToString(b)

	sum := sha256.Sum256([]byte(plain))
	token = InvitationToken{
		TokenUUID: uuid.New(),
		Hash:      hex.EncodeToString(sum[:]),
		ExpiresAt: time.Now().UTC().Add(InvitationTokenTTL),
	}
	return plain, token, nil
}

// HashInvitationToken は提示された平文トークンをDB照合用のSHA-256 hexへ変換する。
func HashInvitationToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// InvitationTokenRecord は保存済み招待トークンの照合用ビュー（INF-07）。
type InvitationTokenRecord struct {
	TokenUUID uuid.UUID
	Email     string
	Role      string
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
}
