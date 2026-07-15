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

// STM-01の英語ID（正本: states.md）
const (
	StatusMailUnverified = "mail_unverified"
)

const RoleUser = "user"

const (
	emailMaxLength    = 254
	passwordMinLength = 15
	passwordMaxLength = 64
	passwordMaxBytes  = 72 // bcryptの入力上限（超過は最大文字数違反と同じ扱い）
)

var (
	ErrInvalidEmail    = errors.New("invalid email format")
	ErrInvalidPassword = errors.New("invalid password length")
)

// Registration は新規登録されるアカウント
type Registration struct {
	UserUUID     uuid.UUID
	Email        string
	PasswordHash string
	Status       string
	Role         string
}

// ValidateEmail はRFC5322準拠・最大254文字を検証する
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

// ValidatePassword は最小15文字・最大64文字（Unicode許容）を検証する。文字数はrune数で数える
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

const tokenPlainBytes = 32 // 256bit・crypto/randで生成

// EmailConfirmationToken はメール確認トークンの永続化表現。平文は保存せずハッシュ（SHA-256）のみ持つ
type EmailConfirmationToken struct {
	TokenUUID uuid.UUID
	Hash      string
	ExpiresAt time.Time
}

// NewEmailConfirmationToken は平文トークン（メール送信用）とその永続化表現を生成する
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
