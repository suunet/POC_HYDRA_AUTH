package unit

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend/auth/app/command"
)

func newLogoutHandler(repo *fakeRefreshRepository) *command.LogoutHandler {
	return command.NewLogoutHandler(repo)
}

// UC-007 主成功: 所有者一致のトークンを1本のみ失効（reason NULL）し正常終了
func TestUC007_Logout_Success_RevokesSingleTokenWithNullReason(t *testing.T) {
	ctx, _ := refreshCtx()
	userUUID := uuid.New()
	rec := validStored(userUUID, uuid.New())
	repo := &fakeRefreshRepository{record: rec, found: true}

	err := newLogoutHandler(repo).Handle(ctx, "valid-token", userUUID.String())
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{rec.TokenID}, repo.revokedSingle, "当該1本のみ失効")
	require.Len(t, repo.revokedReasons, 1)
	assert.Nil(t, repo.revokedReasons[0], "revocation_reasonはNULL（FR-07・自発的操作のため理由は自明）")
	assert.Empty(t, repo.revokedFamily, "family一括失効はしない")
}

// UC-007 E1: 空トークン（形式不正）はエラー・repo呼び出しなし
func TestUC007_Logout_EmptyToken_ReturnsFormatInvalid(t *testing.T) {
	ctx, buf := refreshCtx()
	repo := &fakeRefreshRepository{}

	err := newLogoutHandler(repo).Handle(ctx, "", uuid.New().String())
	assert.ErrorIs(t, err, command.ErrRefreshTokenFormatInvalid)
	assert.Empty(t, repo.revokedSingle)
	assert.Contains(t, buf.String(), "リフレッシュトークン形式不正")
	assert.Contains(t, buf.String(), `"ctx":"logout"`)
}

// UC-007 A1: トークン不存在は正常終了（冪等200・存在有無を漏洩しない）・失効なし
func TestUC007_Logout_NotFound_SucceedsIdempotently(t *testing.T) {
	ctx, _ := refreshCtx()
	repo := &fakeRefreshRepository{found: false}

	err := newLogoutHandler(repo).Handle(ctx, "unknown", uuid.New().String())
	require.NoError(t, err)
	assert.Empty(t, repo.revokedSingle)
	assert.Empty(t, repo.revokedFamily)
}

// UC-007 E2: 所有者不一致は失効せず正常終了（情報漏洩防止・200同一応答）・WARNING監査
func TestUC007_Logout_OwnershipMismatch_NoRevoke_AuditsWarning(t *testing.T) {
	ctx, buf := refreshCtx()
	owner := uuid.New()
	attacker := uuid.New()
	rec := validStored(owner, uuid.New())
	repo := &fakeRefreshRepository{record: rec, found: true}

	err := newLogoutHandler(repo).Handle(ctx, "someones-token", attacker.String())
	require.NoError(t, err, "不一致でも正常応答と同一（E2）")
	assert.Empty(t, repo.revokedSingle, "失効は行わない")
	assert.Empty(t, repo.revokedFamily, "E2でも再利用検知（family失効）を発火しない（FR-07経路限定）")
	assert.Contains(t, buf.String(), "他ユーザーのリフレッシュトークンによるログアウト試行")
	assert.Contains(t, buf.String(), attacker.String(), "user_id=authenticated_user_id（操作主体側）")
	assert.Contains(t, buf.String(), "WARN")
	assert.NotContains(t, buf.String(), owner.String(), "所有者側UUIDはログに出さない（所有情報の漏洩防止）")
	assert.Contains(t, buf.String(), "usecase finished", "E2でも終了INFOを出す（NFR-08・開始/終了ペア）")
}

// UC-007 / NFR-08: 失効UPDATE障害もERRORログを出しエラーを返す（fail-closed）
func TestUC007_Logout_RevokeFailure_LogsErrorAndFails(t *testing.T) {
	ctx, buf := refreshCtx()
	userUUID := uuid.New()
	rec := validStored(userUUID, uuid.New())
	repo := &fakeRefreshRepository{record: rec, found: true, revokeErr: errors.New("db down")}

	err := newLogoutHandler(repo).Handle(ctx, "tok", userUUID.String())
	require.Error(t, err)
	assert.Contains(t, buf.String(), "ERROR")
	assert.Contains(t, buf.String(), `"ctx":"logout"`)
}

// UC-007 Q-2: 使用済み・期限切れ・既失効トークンも一律に冪等失効（再利用検知を発火しない）
func TestUC007_Logout_UsedExpiredRevoked_RevokeIdempotently_NoReuseDetection(t *testing.T) {
	userUUID := uuid.New()
	now := time.Now()
	used := now.Add(-time.Hour)

	cases := []struct {
		name   string
		mutate func(*command.StoredRefreshToken)
	}{
		{"使用済み（used_at非NULL）", func(r *command.StoredRefreshToken) { r.UsedAt = &used }},
		{"期限切れ", func(r *command.StoredRefreshToken) { r.ExpiresAt = now.Add(-time.Minute) }},
		{"既失効（revoked_at非NULL）", func(r *command.StoredRefreshToken) { r.RevokedAt = &used }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, _ := refreshCtx()
			rec := validStored(userUUID, uuid.New())
			c.mutate(&rec)
			repo := &fakeRefreshRepository{record: rec, found: true}

			err := newLogoutHandler(repo).Handle(ctx, "tok", userUUID.String())
			require.NoError(t, err, "状態に関わらず200（Q-2一律冪等失効）")
			assert.Equal(t, []uuid.UUID{rec.TokenID}, repo.revokedSingle, "見つかれば失効（既失効はSQL冪等ガードで0行）")
			assert.Empty(t, repo.revokedFamily, "いかなる状態でも再利用検知（family失効）を発火しない（FR-07経路限定）")
		})
	}
}

// UC-007 / NFR-08: 外部依存（DB）障害はERRORログを出しエラーを返す（fail-closed）
func TestUC007_Logout_RepoFailure_LogsErrorAndFails(t *testing.T) {
	ctx, buf := refreshCtx()
	repo := &fakeRefreshRepository{getErr: errors.New("db down")}

	err := newLogoutHandler(repo).Handle(ctx, "tok", uuid.New().String())
	require.Error(t, err)
	assert.Contains(t, buf.String(), "ERROR")
	assert.Contains(t, buf.String(), `"ctx":"logout"`)
}
