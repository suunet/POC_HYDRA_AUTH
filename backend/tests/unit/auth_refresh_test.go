package unit

import (
	"bytes"
	"context"
	"crypto/rsa"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend/auth/app/command"
	"poc-app-hydra/backend/auth/domain"
	applog "poc-app-hydra/backend/common/log"
)

// rotationResult はローテーション呼び出しのテスト観測用（本番コードは使わない）。
type rotationResult struct {
	OldTokenID  uuid.UUID
	NewFamilyID uuid.UUID
}

// fakeRefreshRepository は RefreshTokenRepository を模す。
type fakeRefreshRepository struct {
	// getByHash が返すレコード（見つからない場合 found=false）
	record command.StoredRefreshToken
	found  bool
	getErr error

	// GetUserForRefresh
	user    command.RefreshUser
	userOK  bool
	userErr error

	// rotateErr は RotateRefreshToken が返すエラー（並行回転競合＝ErrRefreshTokenAlreadyUsed の模擬用）
	rotateErr error

	// 記録
	rotated       []rotationResult // MarkUsed＋新規保存の呼び出し
	revokedSingle []uuid.UUID
	revokedFamily []uuid.UUID
}

func (f *fakeRefreshRepository) GetRefreshTokenByHash(ctx context.Context, hash string) (command.StoredRefreshToken, bool, error) {
	if f.getErr != nil {
		return command.StoredRefreshToken{}, false, f.getErr
	}
	return f.record, f.found, nil
}

func (f *fakeRefreshRepository) GetUserForRefresh(ctx context.Context, userUUID uuid.UUID) (command.RefreshUser, bool, error) {
	if f.userErr != nil {
		return command.RefreshUser{}, false, f.userErr
	}
	return f.user, f.userOK, nil
}

func (f *fakeRefreshRepository) RotateRefreshToken(ctx context.Context, oldTokenID uuid.UUID, newToken command.RefreshTokenRecord) error {
	if f.rotateErr != nil {
		return f.rotateErr
	}
	f.rotated = append(f.rotated, rotationResult{OldTokenID: oldTokenID, NewFamilyID: newToken.FamilyID})
	return nil
}

func (f *fakeRefreshRepository) RevokeRefreshToken(ctx context.Context, tokenID uuid.UUID, reason *string) error {
	f.revokedSingle = append(f.revokedSingle, tokenID)
	return nil
}

func (f *fakeRefreshRepository) RevokeRefreshTokenFamily(ctx context.Context, familyID uuid.UUID, reason string) error {
	f.revokedFamily = append(f.revokedFamily, familyID)
	return nil
}

func refreshCtx() (context.Context, *bytes.Buffer) {
	var buf bytes.Buffer
	return applog.ContextWithLogger(context.Background(), applog.New(&buf, "test")), &buf
}

func validStored(userUUID, familyID uuid.UUID) command.StoredRefreshToken {
	return command.StoredRefreshToken{
		TokenID:   uuid.New(),
		UserUUID:  userUUID,
		FamilyID:  familyID,
		ExpiresAt: time.Now().Add(24 * time.Hour),
		UsedAt:    nil,
		RevokedAt: nil,
	}
}

func newRefreshHandler(t *testing.T, repo *fakeRefreshRepository) (*command.RefreshTokenHandler, *rsa.PrivateKey) {
	key := testSigningKey(t)
	return command.NewRefreshTokenHandler(repo, key), key
}

// UC-006: 主成功 — 有効な未使用トークンでローテーション・新トークン発行・RS256検証可能
func TestUC006_Refresh_Success_RotatesAndIssues(t *testing.T) {
	ctx, _ := refreshCtx()
	userUUID := uuid.New()
	familyID := uuid.New()
	repo := &fakeRefreshRepository{
		record: validStored(userUUID, familyID),
		found:  true,
		user:   command.RefreshUser{UserUUID: userUUID, Status: domain.StatusInactive, Roles: []string{"user"}},
		userOK: true,
	}
	h, key := newRefreshHandler(t, repo)

	res, err := h.Handle(ctx, "some-refresh-token")
	require.NoError(t, err)
	assert.NotEmpty(t, res.AccessToken)
	assert.NotEmpty(t, res.RefreshToken)
	require.Len(t, repo.rotated, 1, "ローテーションが1回行われる")
	assert.Equal(t, familyID, repo.rotated[0].NewFamilyID, "同一familyでローテーション")

	parsed, err := jwt.Parse(res.AccessToken, func(tok *jwt.Token) (interface{}, error) {
		assert.Equal(t, "RS256", tok.Method.Alg())
		return &key.PublicKey, nil
	})
	require.NoError(t, err)
	claims := parsed.Claims.(jwt.MapClaims)
	assert.Equal(t, userUUID.String(), claims["sub"])
}

// UC-006 E2: トークンが存在しない → 401 invalid-token（ErrInvalidRefreshToken）
func TestUC006_Refresh_NotFound_ReturnsInvalidToken(t *testing.T) {
	ctx, _ := refreshCtx()
	repo := &fakeRefreshRepository{found: false}
	h, _ := newRefreshHandler(t, repo)

	_, err := h.Handle(ctx, "nope")
	assert.ErrorIs(t, err, command.ErrInvalidRefreshToken)
	assert.Empty(t, repo.rotated)
}

// UC-006 E3: 期限切れ → token-expired・当該トークンを失効
func TestUC006_Refresh_Expired_RevokesAndReturnsExpired(t *testing.T) {
	ctx, _ := refreshCtx()
	userUUID := uuid.New()
	rec := validStored(userUUID, uuid.New())
	rec.ExpiresAt = time.Now().Add(-time.Minute)
	repo := &fakeRefreshRepository{record: rec, found: true}
	h, _ := newRefreshHandler(t, repo)

	_, err := h.Handle(ctx, "expired")
	assert.ErrorIs(t, err, command.ErrRefreshTokenExpired)
	assert.Equal(t, []uuid.UUID{rec.TokenID}, repo.revokedSingle, "当該トークンのみ失効")
	assert.Empty(t, repo.revokedFamily, "familyは失効しない")
}

// UC-006 E4: 使用済みトークンの再提示 → 再利用検知・family一括失効・session-revoked
func TestUC006_Refresh_Reuse_RevokesFamily(t *testing.T) {
	ctx, buf := refreshCtx()
	userUUID := uuid.New()
	familyID := uuid.New()
	rec := validStored(userUUID, familyID)
	used := time.Now().Add(-time.Hour)
	rec.UsedAt = &used
	repo := &fakeRefreshRepository{record: rec, found: true}
	h, _ := newRefreshHandler(t, repo)

	_, err := h.Handle(ctx, "reused")
	var revoked *command.SessionRevokedError
	require.ErrorAs(t, err, &revoked)
	assert.Equal(t, "token_reuse_detected", revoked.Reason)
	assert.Equal(t, []uuid.UUID{familyID}, repo.revokedFamily, "family一括失効")
	assert.Empty(t, repo.rotated, "再利用時はローテーションしない")
	// 監査ログはCRITICAL・family_id併記（NFR-08・UC-006 §6・BJ c3#1/#5）
	assert.Contains(t, buf.String(), "CRITICAL", "再利用検知はCRITICALレベル")
	assert.Contains(t, buf.String(), familyID.String(), "family_idをlog contextに併記")
}

// UC-006 E4（並行競合）: 読取時は未使用でも Rotate の条件付きMarkUsedが0行（先を越された）→ 再利用検知・family一括失効
func TestUC006_Refresh_ConcurrentRotationConflict_RevokesFamily(t *testing.T) {
	ctx, buf := refreshCtx()
	userUUID := uuid.New()
	familyID := uuid.New()
	repo := &fakeRefreshRepository{
		record:    validStored(userUUID, familyID), // 読取時点は未使用
		found:     true,
		user:      command.RefreshUser{UserUUID: userUUID, Status: domain.StatusInactive, Roles: []string{"user"}},
		userOK:    true,
		rotateErr: command.ErrRefreshTokenAlreadyUsed, // 回転時に競合検知
	}
	h, _ := newRefreshHandler(t, repo)

	_, err := h.Handle(ctx, "raced")
	var revoked *command.SessionRevokedError
	require.ErrorAs(t, err, &revoked)
	assert.Equal(t, "token_reuse_detected", revoked.Reason)
	assert.Equal(t, []uuid.UUID{familyID}, repo.revokedFamily, "競合検知でfamily一括失効")
	assert.Contains(t, buf.String(), "CRITICAL", "並行競合もCRITICALで監査")
	assert.Contains(t, buf.String(), familyID.String())
}

// UC-006 E1: 空トークン（形式不正）は invalid-token・ローテーション/失効なし
func TestUC006_Refresh_EmptyToken_ReturnsInvalidToken(t *testing.T) {
	ctx, _ := refreshCtx()
	repo := &fakeRefreshRepository{}
	h, _ := newRefreshHandler(t, repo)

	_, err := h.Handle(ctx, "")
	assert.ErrorIs(t, err, command.ErrInvalidRefreshToken)
	assert.Empty(t, repo.rotated)
	assert.Empty(t, repo.revokedSingle)
	assert.Empty(t, repo.revokedFamily)
}

// UC-006 E5/E6: 削除済み/無効化済み → session-revoked（account_deleted/account_disabled）・当該トークン失効
func TestUC006_Refresh_DeletedOrDisabled_RevokesSingle(t *testing.T) {
	cases := []struct {
		name   string
		userOK bool
		status string
		reason string
	}{
		{"deleted", false, "", "account_deleted"},
		{"disabled", true, domain.StatusDisabled, "account_disabled"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, _ := refreshCtx()
			userUUID := uuid.New()
			rec := validStored(userUUID, uuid.New())
			repo := &fakeRefreshRepository{
				record: rec, found: true,
				user:   command.RefreshUser{UserUUID: userUUID, Status: c.status},
				userOK: c.userOK,
			}
			h, _ := newRefreshHandler(t, repo)

			_, err := h.Handle(ctx, "tok")
			var revoked *command.SessionRevokedError
			require.ErrorAs(t, err, &revoked)
			assert.Equal(t, c.reason, revoked.Reason)
			assert.Equal(t, []uuid.UUID{rec.TokenID}, repo.revokedSingle)
			assert.Empty(t, repo.revokedFamily)
		})
	}
}

// UC-006: 外部依存（DB）障害はUC層ERRORログを出しエラーを返す（fail-closed・NFR-08）
func TestUC006_Refresh_RepoFailure_LogsError(t *testing.T) {
	ctx, buf := refreshCtx()
	repo := &fakeRefreshRepository{getErr: errors.New("db down")}
	h, _ := newRefreshHandler(t, repo)

	_, err := h.Handle(ctx, "tok")
	require.Error(t, err)
	assert.Contains(t, buf.String(), "ERROR")
	assert.Contains(t, buf.String(), "token_refresh")
}
