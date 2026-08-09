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

type confirmedReset struct {
	userUUID  uuid.UUID
	tokenUUID uuid.UUID
	newHash   string
}

type fakeResetConfirmRepository struct {
	records    map[string]domain.PasswordResetTokenRecord // key: token_hash
	lookups    int
	confirmed  []confirmedReset
	confirmErr error // 完了Tx（E8・consume競合含む）の失敗を模す
}

func (f *fakeResetConfirmRepository) GetPasswordResetTokenByHash(ctx context.Context, hash string) (domain.PasswordResetTokenRecord, error) {
	f.lookups++
	rec, ok := f.records[hash]
	if !ok {
		return domain.PasswordResetTokenRecord{}, domain.ErrResetTokenNotFound
	}
	return rec, nil
}

func (f *fakeResetConfirmRepository) ConfirmPasswordReset(ctx context.Context, userUUID, tokenUUID uuid.UUID, newPasswordHash string) error {
	if f.confirmErr != nil {
		return f.confirmErr
	}
	f.confirmed = append(f.confirmed, confirmedReset{userUUID: userUUID, tokenUUID: tokenUUID, newHash: newPasswordHash})
	return nil
}

// seedConfirmToken は平文トークンとそのSHA-256レコードをfakeへ登録する（INF-05: DBはハッシュのみ）
func seedConfirmToken(d *testDeps, usedAt *time.Time, expiresAt time.Time) (plain string, rec domain.PasswordResetTokenRecord) {
	plain, token, err := domain.NewPasswordResetToken()
	if err != nil {
		panic(err)
	}
	rec = domain.PasswordResetTokenRecord{
		TokenUUID: token.TokenUUID,
		UserUUID:  uuid.New(),
		TokenHash: token.Hash,
		ExpiresAt: expiresAt,
		UsedAt:    usedAt,
	}
	d.resetConfirmRepo.records[token.Hash] = rec
	return plain, rec
}

func postPasswordResetConfirm(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/password-reset/confirm", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec
}

const validNewPassword = "brand-new-secret-pw!"

// UC-009: 主成功シナリオ — 有効トークン＋強度充足パスワードで200・完了Tx（当該ユーザー・当該トークン・bcryptコスト12のハッシュ）
func TestUC009_ConfirmReset_ValidToken_Returns200_ConfirmsWithBcryptHash(t *testing.T) {
	d := newTestDeps()
	plain, rec := seedConfirmToken(d, nil, time.Now().UTC().Add(20*time.Minute))

	res := postPasswordResetConfirm(t, newAuthTestEcho(t, d), `{"token":"`+plain+`","new_password":"`+validNewPassword+`"}`)

	assert.Equal(t, http.StatusOK, res.Code)
	require.Len(t, d.resetConfirmRepo.confirmed, 1)
	got := d.resetConfirmRepo.confirmed[0]
	assert.Equal(t, rec.UserUUID, got.userUUID)
	assert.Equal(t, rec.TokenUUID, got.tokenUUID)
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(got.newHash), []byte(validNewPassword)),
		"検証可能なbcryptハッシュで更新する")
	cost, err := bcrypt.Cost([]byte(got.newHash))
	require.NoError(t, err)
	assert.Equal(t, domain.PasswordBcryptCost, cost, "NFR-01: bcryptコスト12")
}

// UC-009: E4 — 新パスワード強度不足（VAR-02）は400 validation-error。トークン照会・完了Txへ進まない
func TestUC009_ConfirmReset_WeakPassword_Returns400_NoLookup(t *testing.T) {
	for name, pw := range map[string]string{
		"15文字未満":        "short-pw",
		"64文字超過":        strings.Repeat("a", 65),
		"72バイト超(UTF-8)": strings.Repeat("あ", 25),
	} {
		t.Run(name, func(t *testing.T) {
			d := newTestDeps()
			plain, _ := seedConfirmToken(d, nil, time.Now().UTC().Add(20*time.Minute))

			res := postPasswordResetConfirm(t, newAuthTestEcho(t, d), `{"token":"`+plain+`","new_password":"`+pw+`"}`)

			assert.Equal(t, http.StatusBadRequest, res.Code)
			var p commonhttp.Problem
			require.NoError(t, json.Unmarshal(res.Body.Bytes(), &p))
			assert.Equal(t, commonhttp.ProblemTypeBase+"validation-error", p.Type)
			assert.Zero(t, d.resetConfirmRepo.lookups, "E4: トークン照会へ進まない")
			assert.Empty(t, d.resetConfirmRepo.confirmed, "E4: 完了Txへ進まない")
		})
	}
}

// UC-009: 空トークンは400 validation-error（openapi minLength:1はバインダ非検証・handler必須検査＝UC-003と同型）。
// 照会・完了Txへ進まない
func TestUC009_ConfirmReset_EmptyToken_Returns400_NoLookup(t *testing.T) {
	d := newTestDeps()

	res := postPasswordResetConfirm(t, newAuthTestEcho(t, d), `{"token":"","new_password":"`+validNewPassword+`"}`)

	assert.Equal(t, http.StatusBadRequest, res.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"validation-error", p.Type)
	assert.Zero(t, d.resetConfirmRepo.lookups, "照会へ進まない")
	assert.Empty(t, d.resetConfirmRepo.confirmed, "完了Txへ進まない")
}

// UC-009: E5（不存在）と E6（使用済み）は応答ボディ・ステータスとも完全一致（トークン状態を区別させない）
func TestUC009_ConfirmReset_UnknownAndUsedToken_Return400_Indistinguishable(t *testing.T) {
	// E5: 不存在
	d1 := newTestDeps()
	resUnknown := postPasswordResetConfirm(t, newAuthTestEcho(t, d1),
		`{"token":"unknown-token","new_password":"`+validNewPassword+`"}`)

	// E6: 使用済み
	d2 := newTestDeps()
	used := time.Now().UTC().Add(-1 * time.Minute)
	plainUsed, _ := seedConfirmToken(d2, &used, time.Now().UTC().Add(20*time.Minute))
	resUsed := postPasswordResetConfirm(t, newAuthTestEcho(t, d2),
		`{"token":"`+plainUsed+`","new_password":"`+validNewPassword+`"}`)

	assert.Equal(t, http.StatusBadRequest, resUnknown.Code)
	assert.Equal(t, http.StatusBadRequest, resUsed.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(resUnknown.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"invalid-token", p.Type)
	assert.Equal(t, resUnknown.Body.String(), resUsed.Body.String(), "E5/E6は応答同一（状態を区別しない）")
	assert.Empty(t, d2.resetConfirmRepo.confirmed, "E6: 完了Txへ進まない（DB書き込みなし）")
}

// UC-009: E7 — 期限切れは400 token-expired・状態更新なし（no-op・CND-08/Q-2確定）
func TestUC009_ConfirmReset_ExpiredToken_Returns400TokenExpired_NoWrite(t *testing.T) {
	d := newTestDeps()
	plain, _ := seedConfirmToken(d, nil, time.Now().UTC().Add(-1*time.Minute))

	res := postPasswordResetConfirm(t, newAuthTestEcho(t, d), `{"token":"`+plain+`","new_password":"`+validNewPassword+`"}`)

	assert.Equal(t, http.StatusBadRequest, res.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"token-expired", p.Type)
	assert.Empty(t, d.resetConfirmRepo.confirmed, "E7: DB書き込みなし（no-op）")
}

// UC-009: 検証読取〜完了Tx間のレースで消費競合（0行）となった場合はE6相当のinvalid-tokenへ倒す
func TestUC009_ConfirmReset_ConsumeConflictRace_Returns400InvalidToken(t *testing.T) {
	d := newTestDeps()
	plain, _ := seedConfirmToken(d, nil, time.Now().UTC().Add(20*time.Minute))
	d.resetConfirmRepo.confirmErr = domain.ErrResetTokenConsumeConflict

	res := postPasswordResetConfirm(t, newAuthTestEcho(t, d), `{"token":"`+plain+`","new_password":"`+validNewPassword+`"}`)

	assert.Equal(t, http.StatusBadRequest, res.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"invalid-token", p.Type)
}

// UC-009: E8 — 完了Tx失敗は500 internal-error（全ロールバックはrepo層で担保・component検証済み）
func TestUC009_ConfirmReset_TxFailure_Returns500(t *testing.T) {
	d := newTestDeps()
	plain, _ := seedConfirmToken(d, nil, time.Now().UTC().Add(20*time.Minute))
	d.resetConfirmRepo.confirmErr = errors.New("db connection lost")

	res := postPasswordResetConfirm(t, newAuthTestEcho(t, d), `{"token":"`+plain+`","new_password":"`+validNewPassword+`"}`)

	assert.Equal(t, http.StatusInternalServerError, res.Code)
	var p commonhttp.Problem
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &p))
	assert.Equal(t, commonhttp.ProblemTypeBase+"internal-error", p.Type)
}
