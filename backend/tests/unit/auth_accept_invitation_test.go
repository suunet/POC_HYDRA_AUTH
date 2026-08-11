package unit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"poc-app-hydra/backend/auth/domain"
	commonhttp "poc-app-hydra/backend/common/http"
)

type acceptedInvitation struct {
	tokenUUID uuid.UUID
	email     string
	role      string
	hash      string
}

type fakeAcceptInvitationRepository struct {
	records   map[string]domain.InvitationTokenRecord // key: token_hash
	users     map[string][]string                     // key: email → roles（E6事前SELECT用）
	accepted  []acceptedInvitation
	acceptErr error // 受付Tx（E5・Tx内E6・consume競合）の失敗を模す
}

func (f *fakeAcceptInvitationRepository) FindUserRolesByEmail(ctx context.Context, email string) ([]string, bool, error) {
	roles, ok := f.users[email]
	return roles, ok, nil
}

func (f *fakeAcceptInvitationRepository) GetInvitationTokenByHash(ctx context.Context, hash string) (domain.InvitationTokenRecord, error) {
	rec, ok := f.records[hash]
	if !ok {
		return domain.InvitationTokenRecord{}, domain.ErrInvitationTokenNotFound
	}
	return rec, nil
}

func (f *fakeAcceptInvitationRepository) AcceptInvitation(ctx context.Context, tokenUUID uuid.UUID, email, role, passwordHash string) (uuid.UUID, error) {
	if f.acceptErr != nil {
		return uuid.Nil, f.acceptErr
	}
	f.accepted = append(f.accepted, acceptedInvitation{tokenUUID: tokenUUID, email: email, role: role, hash: passwordHash})
	return uuid.New(), nil
}

func seedAcceptToken(d *testDeps, usedAt *time.Time, expiresAt time.Time, role string) (plain string, rec domain.InvitationTokenRecord) {
	plain, token, err := domain.NewInvitationToken()
	if err != nil {
		panic(err)
	}
	rec = domain.InvitationTokenRecord{
		TokenUUID: token.TokenUUID,
		Email:     "invitee@example.com",
		Role:      role,
		TokenHash: token.Hash,
		ExpiresAt: expiresAt,
		UsedAt:    usedAt,
	}
	d.acceptRepo.records[token.Hash] = rec
	return plain, rec
}

func postAcceptInvitation(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/invitation/accept", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec
}

const acceptNewPassword = "brand-new-admin-pw-01!"

// UC-012 主成功: 有効トークン＋強度充足パスワードで200・受付Tx（当該トークン・紐付けロール・bcryptコスト12）
func TestUC012_AcceptInvitation_ValidToken_Returns200(t *testing.T) {
	d := newTestDeps()
	plain, rec := seedAcceptToken(d, nil, time.Now().UTC().Add(1*time.Hour), "operator")

	res := postAcceptInvitation(t, newAuthTestEcho(t, d), `{"token":"`+plain+`","password":"`+acceptNewPassword+`"}`)

	assert.Equal(t, http.StatusOK, res.Code)
	require.Len(t, d.acceptRepo.accepted, 1)
	got := d.acceptRepo.accepted[0]
	assert.Equal(t, rec.TokenUUID, got.tokenUUID)
	assert.Equal(t, rec.Email, got.email)
	assert.Equal(t, "operator", got.role, "FR-13: 紐付けロールを付与")
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(got.hash), []byte(acceptNewPassword)))
	cost, err := bcrypt.Cost([]byte(got.hash))
	require.NoError(t, err)
	assert.Equal(t, domain.PasswordBcryptCost, cost, "NFR-01: bcryptコスト12")
}

// UC-012 E1: パスワード強度不足は400 validation-error。トークン照会・受付Txへ進まない
func TestUC012_AcceptInvitation_WeakPassword_Returns400(t *testing.T) {
	d := newTestDeps()
	plain, _ := seedAcceptToken(d, nil, time.Now().UTC().Add(1*time.Hour), "operator")

	res := postAcceptInvitation(t, newAuthTestEcho(t, d), `{"token":"`+plain+`","password":"short"}`)

	assert.Equal(t, http.StatusBadRequest, res.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"validation-error", p.Type)
	assert.Empty(t, d.acceptRepo.accepted, "E1: 受付Txへ進まない")
}

// UC-012 E2/E3: 不存在と使用済みは応答ボディ・ステータスとも完全一致（状態を区別しない）
func TestUC012_AcceptInvitation_UnknownAndUsed_Return400_Indistinguishable(t *testing.T) {
	d1 := newTestDeps()
	resUnknown := postAcceptInvitation(t, newAuthTestEcho(t, d1),
		`{"token":"unknown-token","password":"`+acceptNewPassword+`"}`)

	d2 := newTestDeps()
	used := time.Now().UTC().Add(-1 * time.Minute)
	plainUsed, _ := seedAcceptToken(d2, &used, time.Now().UTC().Add(1*time.Hour), "operator")
	resUsed := postAcceptInvitation(t, newAuthTestEcho(t, d2),
		`{"token":"`+plainUsed+`","password":"`+acceptNewPassword+`"}`)

	assert.Equal(t, http.StatusBadRequest, resUnknown.Code)
	assert.Equal(t, http.StatusBadRequest, resUsed.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(resUnknown.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"invalid-token", p.Type)
	assert.Equal(t, resUnknown.Body.String(), resUsed.Body.String(), "E2/E3は応答同一（状態を区別しない）")
	assert.Empty(t, d2.acceptRepo.accepted, "E3: 受付Txへ進まない")
}

// UC-012 E4: 期限切れは400 token-expired・状態更新なし（no-op）
func TestUC012_AcceptInvitation_ExpiredToken_Returns400TokenExpired_NoWrite(t *testing.T) {
	d := newTestDeps()
	plain, _ := seedAcceptToken(d, nil, time.Now().UTC().Add(-1*time.Minute), "operator")

	res := postAcceptInvitation(t, newAuthTestEcho(t, d), `{"token":"`+plain+`","password":"`+acceptNewPassword+`"}`)

	assert.Equal(t, http.StatusBadRequest, res.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"token-expired", p.Type)
	assert.Empty(t, d.acceptRepo.accepted, "E4: DB書き込みなし（no-op）")
}

// UC-012 空トークンは400 validation-error（openapi minLength:1はバインダ非検証・handler必須検査）
func TestUC012_AcceptInvitation_EmptyToken_Returns400(t *testing.T) {
	d := newTestDeps()

	res := postAcceptInvitation(t, newAuthTestEcho(t, d), `{"token":"","password":"`+acceptNewPassword+`"}`)

	assert.Equal(t, http.StatusBadRequest, res.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"validation-error", p.Type)
	assert.Empty(t, d.acceptRepo.accepted)
}

// UC-012 E6: 既存アカウント実在は409 email-already-registered（500にしない・Q-6二重防御）
func TestUC012_AcceptInvitation_ExistingEmail_Returns409(t *testing.T) {
	t.Run("事前SELECT検出（外側・トークン非消費）", func(t *testing.T) {
		d := newTestDeps()
		plain, rec := seedAcceptToken(d, nil, time.Now().UTC().Add(1*time.Hour), "operator")
		d.acceptRepo.users[rec.Email] = []string{"user"}

		res := postAcceptInvitation(t, newAuthTestEcho(t, d), `{"token":"`+plain+`","password":"`+acceptNewPassword+`"}`)

		assert.Equal(t, http.StatusConflict, res.Code)
		var p commonhttp.Problem
		require.NoError(t, json.Unmarshal(res.Body.Bytes(), &p))
		assert.Equal(t, commonhttp.ProblemTypeBase+"email-already-registered", p.Type)
		assert.Empty(t, d.acceptRepo.accepted, "E6: 受付Tx未呼び出し＝トークン非消費")
	})
	t.Run("Tx内UNIQUE写像（内側・確認〜Tx間のレース窓）", func(t *testing.T) {
		d := newTestDeps()
		plain, _ := seedAcceptToken(d, nil, time.Now().UTC().Add(1*time.Hour), "operator")
		d.acceptRepo.acceptErr = domain.ErrEmailAlreadyRegistered

		res := postAcceptInvitation(t, newAuthTestEcho(t, d), `{"token":"`+plain+`","password":"`+acceptNewPassword+`"}`)

		assert.Equal(t, http.StatusConflict, res.Code)
		var p commonhttp.Problem
		require.NoError(t, json.Unmarshal(res.Body.Bytes(), &p))
		assert.Equal(t, commonhttp.ProblemTypeBase+"email-already-registered", p.Type)
	})
}

// UC-012: 検証読取〜受付Tx間の消費競合（0行）はE3相当のinvalid-tokenへ倒す
func TestUC012_AcceptInvitation_ConsumeConflictRace_Returns400InvalidToken(t *testing.T) {
	d := newTestDeps()
	plain, _ := seedAcceptToken(d, nil, time.Now().UTC().Add(1*time.Hour), "operator")
	d.acceptRepo.acceptErr = domain.ErrInvitationTokenConsumeConflict

	res := postAcceptInvitation(t, newAuthTestEcho(t, d), `{"token":"`+plain+`","password":"`+acceptNewPassword+`"}`)

	assert.Equal(t, http.StatusBadRequest, res.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"invalid-token", p.Type)
	dRef := newTestDeps()
	refUnknown := postAcceptInvitation(t, newAuthTestEcho(t, dRef), `{"token":"unknown-token","password":"`+acceptNewPassword+`"}`)
	assert.Equal(t, refUnknown.Body.String(), res.Body.String(), "consume競合はE2/E3と応答完全一致（状態を区別しない）")
}

// UC-012 E5: 受付Tx失敗は500 internal-error
func TestUC012_AcceptInvitation_TxFailure_Returns500(t *testing.T) {
	d := newTestDeps()
	plain, _ := seedAcceptToken(d, nil, time.Now().UTC().Add(1*time.Hour), "operator")
	d.acceptRepo.acceptErr = errors.New("db connection lost")

	res := postAcceptInvitation(t, newAuthTestEcho(t, d), `{"token":"`+plain+`","password":"`+acceptNewPassword+`"}`)

	assert.Equal(t, http.StatusInternalServerError, res.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"internal-error", p.Type)
}
