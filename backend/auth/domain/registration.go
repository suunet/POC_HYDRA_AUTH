package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/mail"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	StatusMailUnverified = "mail_unverified"
)

const RoleUser = "user"

const (
	emailMaxLength    = 254
	passwordMinLength = 15
	passwordMaxLength = 64
	passwordMaxBytes  = 72 // NOTE: bcryptの入力上限（超過は最大文字数違反と同じ扱い）
)

var (
	ErrInvalidEmail    = errors.New("invalid email format")
	ErrInvalidPassword = errors.New("invalid password length")
)

type Registration struct {
	UserUUID     uuid.UUID
	Email        string
	PasswordHash string
	Status       string
	Role         string
}

func ValidateEmail(email string) error {
	if email == "" || len(email) > emailMaxLength {
		return ErrInvalidEmail
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return ErrInvalidEmail
	}
	return nil
}

func ValidatePassword(password string) error {
	runes := utf8.RuneCountInString(password)
	if runes < passwordMinLength || runes > passwordMaxLength {
		return ErrInvalidPassword
	}
	if len(password) > passwordMaxBytes {
		return ErrInvalidPassword
	}
	return nil
}

const EmailConfirmationTokenTTL = 24 * time.Hour

const RegistrationRateLimitWindow = 5 * time.Minute

// VAR-13: 同一メールアドレスの再送は5分に1回
const ResendEmailRateLimitWindow = 5 * time.Minute

const tokenPlainBytes = 32 // NOTE: 256bit・crypto/randで生成

// NOTE: 平文は保存せずハッシュ（SHA-256）のみ持つ（漏洩対策）
type EmailConfirmationToken struct {
	TokenUUID uuid.UUID
	Hash      string
	ExpiresAt time.Time
}

func NewEmailConfirmationToken() (plain string, token EmailConfirmationToken, err error) {
	b := make([]byte, tokenPlainBytes)
	if _, err := rand.Read(b); err != nil {
		return "", EmailConfirmationToken{}, err
	}
	plain = base64.RawURLEncoding.EncodeToString(b)

	sum := sha256.Sum256([]byte(plain))
	token = EmailConfirmationToken{
		TokenUUID: uuid.New(),
		Hash:      hex.EncodeToString(sum[:]),
		ExpiresAt: time.Now().UTC().Add(EmailConfirmationTokenTTL),
	}
	return plain, token, nil
}
