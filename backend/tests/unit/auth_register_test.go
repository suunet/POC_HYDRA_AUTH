package unit

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	apihttp "poc-app-hydra/backend/auth/api/http"
	"poc-app-hydra/backend/auth/app/command"
	"poc-app-hydra/backend/auth/domain"
	commonhttp "poc-app-hydra/backend/common/http"
	applog "poc-app-hydra/backend/common/log"
)

// fakeUserRepository は command.UserRepository の公開インターフェースを満たすテストダブル
type fakeUserRepository struct {
	existing map[string]bool
	created  []domain.Registration
}

func (f *fakeUserRepository) EmailExists(ctx context.Context, email string) (bool, error) {
	return f.existing[email], nil
}

func (f *fakeUserRepository) CreateUser(ctx context.Context, r domain.Registration) error {
	f.created = append(f.created, r)
	return nil
}

func newAuthTestEcho(t *testing.T, repo command.UserRepository) http.Handler {
	t.Helper()
	logger := applog.New(&bytes.Buffer{}, "auth-service")
	e := commonhttp.NewEcho(logger)
	apihttp.Register(e, apihttp.NewHandler(command.NewRegisterAccountHandler(repo)))
	return e
}

func postRegister(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec
}

// UC-002: 主成功シナリオ — 201を返し、メール未確認状態＋userロール＋bcryptハッシュで登録する
func TestUC002_Register_Returns201_AndCreatesUser(t *testing.T) {
	repo := &fakeUserRepository{existing: map[string]bool{}}
	rec := postRegister(t, newAuthTestEcho(t, repo), `{"email":"new@example.com","password":"secret-passw0rd!"}`)

	assert.Equal(t, http.StatusCreated, rec.Code)
	require.Len(t, repo.created, 1)
	created := repo.created[0]
	assert.Equal(t, "new@example.com", created.Email)
	assert.Equal(t, domain.StatusMailUnverified, created.Status)
	assert.Equal(t, domain.RoleUser, created.Role)
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(created.PasswordHash), []byte("secret-passw0rd!")),
		"password must be stored as a verifiable bcrypt hash")
	assert.NotEmpty(t, created.UserUUID)
}

// UC-002: E1 — メールアドレス形式エラー（VAR-01）は400 problem+json
func TestUC002_Register_InvalidEmail_Returns400ProblemJSON(t *testing.T) {
	for name, email := range map[string]string{
		"形式不正":    "not-an-email",
		"254文字超過": strings.Repeat("a", 250) + "@example.com",
	} {
		t.Run(name, func(t *testing.T) {
			repo := &fakeUserRepository{existing: map[string]bool{}}
			rec := postRegister(t, newAuthTestEcho(t, repo), `{"email":"`+email+`","password":"secret-passw0rd!"}`)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
			var p commonhttp.Problem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			assert.Equal(t, commonhttp.ProblemTypeBase+"validation-error", p.Type)
			assert.Empty(t, repo.created)
		})
	}
}

// UC-002: E2 — パスワード強度エラー（VAR-02: 最小15・最大64）は400 problem+json
func TestUC002_Register_InvalidPassword_Returns400ProblemJSON(t *testing.T) {
	for name, password := range map[string]string{
		"15文字未満": strings.Repeat("a", 14),
		"64文字超過": strings.Repeat("a", 65),
		"マルチバイト14文字（rune数）": strings.Repeat("あ", 14),
		"72バイト超過（Q-13暫定）":   strings.Repeat("あ", 25), // 25文字だが75バイト
	} {
		t.Run(name, func(t *testing.T) {
			repo := &fakeUserRepository{existing: map[string]bool{}}
			rec := postRegister(t, newAuthTestEcho(t, repo), `{"email":"new@example.com","password":"`+password+`"}`)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			var p commonhttp.Problem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			assert.Equal(t, commonhttp.ProblemTypeBase+"validation-error", p.Type)
			assert.Empty(t, repo.created)
		})
	}
}

// UC-002: VAR-02はUnicode許容・文字数はrune数（マルチバイト15文字は通る）
func TestUC002_Register_MultibytePassword_CountedInRunes(t *testing.T) {
	repo := &fakeUserRepository{existing: map[string]bool{}}
	rec := postRegister(t, newAuthTestEcho(t, repo), `{"email":"mb@example.com","password":"`+strings.Repeat("あ", 15)+`"}`)
	assert.Equal(t, http.StatusCreated, rec.Code, "15文字（45バイト）はVAR-02を満たす")
	assert.Len(t, repo.created, 1)
}

// UC-002: A1 — 登録済みメールアドレスでも201（列挙攻撃対策・登録処理は行わない）
func TestUC002_Register_DuplicateEmail_Returns201_WithoutCreating(t *testing.T) {
	repo := &fakeUserRepository{existing: map[string]bool{"taken@example.com": true}}
	rec := postRegister(t, newAuthTestEcho(t, repo), `{"email":"taken@example.com","password":"secret-passw0rd!"}`)

	assert.Equal(t, http.StatusCreated, rec.Code, "A1: 未登録の場合と区別しない")
	assert.Empty(t, repo.created, "A1: 登録処理を行わない")
}
