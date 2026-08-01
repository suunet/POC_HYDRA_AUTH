package unit

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"poc-app-hydra/backend/auth/app/command"
	"poc-app-hydra/backend/auth/domain"
)

// fakePasswordChangeRepository は PasswordChangeRepository を模す。
type fakePasswordChangeRepository struct {
	creds   command.UserCredentials
	found   bool
	getErr  error
	chgErr  error
	changed []string // ChangePassword に渡された newPasswordHash の記録
}

func (f *fakePasswordChangeRepository) GetUserCredentials(ctx context.Context, userUUID uuid.UUID) (command.UserCredentials, bool, error) {
	if f.getErr != nil {
		return command.UserCredentials{}, false, f.getErr
	}
	return f.creds, f.found, nil
}

func (f *fakePasswordChangeRepository) ChangePassword(ctx context.Context, userUUID uuid.UUID, newPasswordHash string) error {
	if f.chgErr != nil {
		return f.chgErr
	}
	f.changed = append(f.changed, newPasswordHash)
	return nil
}

const currentPW = "current-secret-passw0rd!"

func credsWith(t *testing.T, status string) command.UserCredentials {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(currentPW), bcrypt.MinCost) // テスト高速化（照合互換）
	require.NoError(t, err)
	return command.UserCredentials{PasswordHash: string(hash), Status: status}
}

func newChangePasswordHandler(repo *fakePasswordChangeRepository) *command.ChangePasswordHandler {
	return command.NewChangePasswordHandler(repo)
}

// UC-010 主成功: 検証を通過し、bcryptハッシュ化した新パスワードで単一Tx更新・監査INFO
func TestUC010_ChangePassword_Success_HashesAndChanges(t *testing.T) {
	ctx, buf := refreshCtx()
	repo := &fakePasswordChangeRepository{creds: credsWith(t, domain.StatusInactive), found: true}

	err := newChangePasswordHandler(repo).Handle(ctx, uuid.New().String(), currentPW, "new-secret-passw0rd!")
	require.NoError(t, err)
	require.Len(t, repo.changed, 1)
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(repo.changed[0]), []byte("new-secret-passw0rd!")),
		"新パスワードのbcryptハッシュが渡される")
	assert.Contains(t, buf.String(), "パスワード変更", "監査INFO（NFR-07・target=user_id）")
	assert.Contains(t, buf.String(), `"ctx":"password_change"`)
}

// UC-010 Q-2: 現在と同一の新パスワードも許容（通常フローで更新・全失効）
func TestUC010_ChangePassword_SamePassword_Allowed(t *testing.T) {
	ctx, _ := refreshCtx()
	// NOTE: 同一PWでもVAR-02を満たす必要があるため15文字以上の現在PWを使う
	repo := &fakePasswordChangeRepository{creds: credsWith(t, domain.StatusInactive), found: true}

	err := newChangePasswordHandler(repo).Handle(ctx, uuid.New().String(), currentPW, currentPW)
	require.NoError(t, err, "同一パスワードは拒否しない（Q-2・実質的な全セッション失効操作）")
	assert.Len(t, repo.changed, 1)
}

// UC-010 E1: 新パスワード強度不足（短い・72バイト超）は repo未呼び出しでエラー
func TestUC010_ChangePassword_WeakNewPassword_ReturnsInvalid(t *testing.T) {
	cases := []struct {
		name string
		pw   string
	}{
		{"15文字未満", "short-pw"},
		{"72バイト超（マルチバイト）", strings.Repeat("あ", 25)}, // 75バイト・25文字（15〜64文字は満たす）
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, buf := refreshCtx()
			repo := &fakePasswordChangeRepository{creds: credsWith(t, domain.StatusInactive), found: true}

			err := newChangePasswordHandler(repo).Handle(ctx, uuid.New().String(), currentPW, c.pw)
			assert.ErrorIs(t, err, domain.ErrInvalidPassword)
			assert.Empty(t, repo.changed, "E1では更新しない")
			assert.Contains(t, buf.String(), "パスワード強度不足")
			assert.Contains(t, buf.String(), "WARN", "E1はビジネス例外WARNING（NFR-08）")
		})
	}
}

// UC-010 E4: ユーザー不存在（削除済み）は session-revoked（account_deleted）・no-op
func TestUC010_ChangePassword_UserNotFound_SessionRevokedDeleted(t *testing.T) {
	ctx, buf := refreshCtx()
	repo := &fakePasswordChangeRepository{found: false}

	err := newChangePasswordHandler(repo).Handle(ctx, uuid.New().String(), currentPW, "new-secret-passw0rd!")
	var revoked *command.SessionRevokedError
	require.ErrorAs(t, err, &revoked)
	assert.Equal(t, "account_deleted", revoked.Reason)
	assert.Empty(t, repo.changed, "no-op（トークン状態・パスワードとも変更しない）")
	assert.Contains(t, buf.String(), "削除済みアカウントの操作試行")
	assert.Contains(t, buf.String(), "WARN", "E4はビジネス例外WARNING（NFR-08）")
}

// UC-010 E5: 無効化済みは session-revoked（account_disabled）・no-op
func TestUC010_ChangePassword_Disabled_SessionRevokedDisabled(t *testing.T) {
	ctx, buf := refreshCtx()
	repo := &fakePasswordChangeRepository{creds: credsWith(t, domain.StatusDisabled), found: true}

	err := newChangePasswordHandler(repo).Handle(ctx, uuid.New().String(), currentPW, "new-secret-passw0rd!")
	var revoked *command.SessionRevokedError
	require.ErrorAs(t, err, &revoked)
	assert.Equal(t, "account_disabled", revoked.Reason)
	assert.Empty(t, repo.changed)
	assert.Contains(t, buf.String(), "無効化済みアカウントの操作試行")
	assert.Contains(t, buf.String(), "WARN", "E5はビジネス例外WARNING（NFR-08）")
}

// UC-010 E2: 現在パスワード不一致・照合エラー（72バイト超）はいずれも password-mismatch・更新なし
func TestUC010_ChangePassword_CurrentMismatchOrOversize_ReturnsMismatch(t *testing.T) {
	cases := []struct {
		name    string
		current string
	}{
		{"不一致", "WRONG-secret-passw0rd!"},
		{"72バイト超（bcrypt照合エラー）", strings.Repeat("x", 80)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, buf := refreshCtx()
			repo := &fakePasswordChangeRepository{creds: credsWith(t, domain.StatusInactive), found: true}

			err := newChangePasswordHandler(repo).Handle(ctx, uuid.New().String(), c.current, "new-secret-passw0rd!")
			assert.ErrorIs(t, err, command.ErrPasswordMismatch, "形式検証せず照合失敗として403へ倒す（CND-16）")
			assert.Empty(t, repo.changed)
			assert.Contains(t, buf.String(), "現在のパスワード不一致")
			assert.Contains(t, buf.String(), "WARN", "E2はビジネス例外WARNING（NFR-08）")
		})
	}
}

// UC-010 E3: Tx失敗はERROR（BUC-U08指定msg）・エラー返却（fail-closed）
func TestUC010_ChangePassword_TxFailure_LogsSpecifiedError(t *testing.T) {
	ctx, buf := refreshCtx()
	repo := &fakePasswordChangeRepository{creds: credsWith(t, domain.StatusInactive), found: true, chgErr: errors.New("db down")}

	err := newChangePasswordHandler(repo).Handle(ctx, uuid.New().String(), currentPW, "new-secret-passw0rd!")
	require.Error(t, err)
	assert.Contains(t, buf.String(), "ERROR")
	assert.Contains(t, buf.String(), "パスワード変更トランザクション失敗", "E3のmsgはBUC-U08指定文言")
}

// UC-010 / NFR-08: 参照系（credentials取得）障害もERRORでfail-closed
func TestUC010_ChangePassword_LookupFailure_LogsErrorAndFails(t *testing.T) {
	ctx, buf := refreshCtx()
	repo := &fakePasswordChangeRepository{getErr: errors.New("db down")}

	err := newChangePasswordHandler(repo).Handle(ctx, uuid.New().String(), currentPW, "new-secret-passw0rd!")
	require.Error(t, err)
	assert.Contains(t, buf.String(), "ERROR")
}

// UC-010 / NFR-09: いかなる経路でも現在/新パスワード平文をログへ出さない
func TestUC010_ChangePassword_NeverLogsPlaintextPasswords(t *testing.T) {
	ctx, buf := refreshCtx()
	repo := &fakePasswordChangeRepository{creds: credsWith(t, domain.StatusInactive), found: true}

	require.NoError(t, newChangePasswordHandler(repo).Handle(ctx, uuid.New().String(), currentPW, "new-secret-passw0rd!"))
	_ = newChangePasswordHandler(&fakePasswordChangeRepository{creds: credsWith(t, domain.StatusInactive), found: true}).
		Handle(ctx, uuid.New().String(), "WRONG-secret-passw0rd!", "new-secret-passw0rd!")

	assert.NotContains(t, buf.String(), currentPW)
	assert.NotContains(t, buf.String(), "new-secret-passw0rd!")
	assert.NotContains(t, buf.String(), "WRONG-secret-passw0rd!")
}

// VAR-10: 失効理由コード password_changed はcommand層の定数を正とする（リテラル分散の防止）
func TestUC010_RevocationReasonConstant(t *testing.T) {
	assert.Equal(t, "password_changed", command.RevocationReasonPasswordChanged)
}
