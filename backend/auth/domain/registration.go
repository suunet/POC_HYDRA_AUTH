package domain

import (
	"errors"
	"net/mail"
	"unicode/utf8"

	"github.com/google/uuid"
)

// STM-01（アカウント状態）の英語ID（states.md 状態図エイリアスが正本 — 指示書T-005 §4.1）
const (
	StatusMailUnverified = "mail_unverified"
)

// VAR-08（一般ユーザーロール）
const RoleUser = "user"

const (
	emailMaxLength    = 254 // VAR-01
	passwordMinLength = 15  // VAR-02（文字数=rune数）
	passwordMaxLength = 64  // VAR-02（文字数=rune数）
	passwordMaxBytes  = 72  // bcryptの入力上限。暫定でE2扱い（カタログ反映はQ-13で確定）
)

var (
	ErrInvalidEmail    = errors.New("invalid email format")    // E1（VAR-01違反）
	ErrInvalidPassword = errors.New("invalid password length") // E2（VAR-02違反）
)

// Registration は UC-002 で新規登録されるアカウント
type Registration struct {
	UserUUID     uuid.UUID
	Email        string
	PasswordHash string
	Status       string
	Role         string
}

// ValidateEmail は VAR-01（RFC5322準拠・最大254文字）を検証する
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

// ValidatePassword は VAR-02（最小15文字・最大64文字・Unicode許容）を検証する。文字数はrune数で数える
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
