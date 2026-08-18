package tests_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
	authclient "poc-app-hydra/backend/auth/api/http/client"
	"poc-app-hydra/backend/auth/domain"
)

// seedLoginAdmin はログイン可能な管理者（指定ロール・未認証）を作成しemailとuuidを返す（UC-014/UC-015のE2E対象）。
func seedLoginAdmin(t *testing.T, ctx context.Context, role string) (string, uuid.UUID) {
	t.Helper()
	email := uniqueEmail(t)
	seedLoginUser(t, ctx, email, domain.StatusInactive)
	row, err := dbmodels.New(pool).GetUserByEmail(ctx, email)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "INSERT INTO auth.user_roles (user_uuid, role) VALUES ($1, $2)", row.UserUuid, role)
	require.NoError(t, err)
	return email, row.UserUuid
}

func disableAccount(t *testing.T, ctx context.Context, at, userID string) *authclient.DisableAdminAccountResponse {
	t.Helper()
	editors := []authclient.RequestEditorFn{}
	if at != "" {
		editors = append(editors, withBearer(at))
	}
	resp, err := client.DisableAdminAccountWithResponse(ctx, userID, editors...)
	require.NoError(t, err)
	return resp
}

func reactivateAccount(t *testing.T, ctx context.Context, at, userID string) *authclient.ReactivateAdminAccountResponse {
	t.Helper()
	editors := []authclient.RequestEditorFn{}
	if at != "" {
		editors = append(editors, withBearer(at))
	}
	resp, err := client.ReactivateAdminAccountWithResponse(ctx, userID, editors...)
	require.NoError(t, err)
	return resp
}

// UC-014→UC-015 主成功E2E: super_adminがoperatorを無効化→対象はログイン不可（403）・既存RTは失効（account_disabled）→
// 再有効化→ログイン可（未認証へ復帰）。実サーバ・実DB・実Redisで全経路を通す。
func TestUC014_UC015_DisableReactivate_RealServer_FullFlow(t *testing.T) {
	ctx := context.Background()
	superAT := seedSuperAdmin(t, ctx)
	targetEmail, targetID := seedLoginAdmin(t, ctx, domain.RoleOperator)

	// 無効化前: 対象はログインできRTを得る
	loginRes := login(t, ctx, targetEmail, loginPassword)
	require.Equal(t, 200, loginRes.StatusCode())
	rt := loginRes.JSON200.RefreshToken

	// 無効化（FR-15）: 200・revocation_reason=account_disabled
	dis := disableAccount(t, ctx, superAT, targetID.String())
	require.Equal(t, 200, dis.StatusCode())
	require.NotNil(t, dis.JSON200)
	assert.Equal(t, "account_disabled", dis.JSON200.RevocationReason)
	row, err := dbmodels.New(pool).GetUserByUuid(ctx, targetID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusDisabled, row.Status, "STM-01.無効化済みへ遷移")

	// 対象はログイン不可（403 account-disabled）
	blocked := login(t, ctx, targetEmail, loginPassword)
	assert.Equal(t, 403, blocked.StatusCode(), "無効化済みはログイン不可")

	// 既存RTは失効（全RT失効）。DB記録の失効理由がaccount_disabledであること（FR-15・VAR-10）を主検証とする
	rtRec, err := dbmodels.New(pool).GetRefreshTokenByHash(ctx, domain.HashRefreshToken(rt))
	require.NoError(t, err)
	require.NotNil(t, rtRec.RevocationReason)
	assert.Equal(t, "account_disabled", *rtRec.RevocationReason, "無効化で全RTをaccount_disabled理由で失効")
	// 失効済みRTの再提示は401 session-revoked（当該RTは失効済みのため再利用検知が発火＝理由はtoken_reuse_detected）
	refr := refresh(t, ctx, rt)
	require.Equal(t, 401, refr.StatusCode(), "無効化で既存セッション失効")
	require.NotNil(t, refr.ApplicationproblemJSON401)
	assert.Contains(t, refr.ApplicationproblemJSON401.Type, "session-revoked")

	// 再有効化（FR-16）: 200・未認証へ復帰しログイン可能（セッションは作らず手動ログイン）
	rea := reactivateAccount(t, ctx, superAT, targetID.String())
	require.Equal(t, 200, rea.StatusCode())
	row2, err := dbmodels.New(pool).GetUserByUuid(ctx, targetID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusInactive, row2.Status, "STM-01.未認証へ復帰")

	relogin := login(t, ctx, targetEmail, loginPassword)
	assert.Equal(t, 200, relogin.StatusCode(), "再有効化後は再度ログイン可能")
}

// UC-014/UC-015 M1/M2 E2E: 無効化・再有効化とも AT欠落=401一様・super_admin以外=403 forbidden
func TestUC014_UC015_DisableReactivate_RealServer_AuthzEnforced(t *testing.T) {
	ctx := context.Background()
	_, targetID := seedLoginAdmin(t, ctx, domain.RoleOperator)
	userAT, _ := loginTokens(t, ctx, uniqueEmail(t)) // user役

	// 各呼出は (HTTPステータス, 403時のproblem type) を返す
	for name, call := range map[string]func(at string) (int, string){
		"disable": func(at string) (int, string) {
			r := disableAccount(t, ctx, at, targetID.String())
			if r.ApplicationproblemJSON403 != nil {
				return r.StatusCode(), r.ApplicationproblemJSON403.Type
			}
			return r.StatusCode(), ""
		},
		"reactivate": func(at string) (int, string) {
			r := reactivateAccount(t, ctx, at, targetID.String())
			if r.ApplicationproblemJSON403 != nil {
				return r.StatusCode(), r.ApplicationproblemJSON403.Type
			}
			return r.StatusCode(), ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			code, _ := call("")
			assert.Equal(t, 401, code, "M1: AT欠落は401一様")
			code, typ := call(userAT)
			assert.Equal(t, 403, code, "M2: super_admin以外は403")
			assert.Contains(t, typ, "forbidden", "M2: CND-17違反はforbidden型")
		})
	}
}

// UC-014 E4 E2E（CND-14・自己無効化）: 稼働中super_adminが2人なら自己無効化は許可（200）、
// その結果最後の1人になったsuper_adminの自己無効化は409 last-super-adminで阻止される。
func TestUC014_Disable_RealServer_SelfDisable_And_LastSuperAdminProtected(t *testing.T) {
	ctx := context.Background()
	isolateActiveSuperAdmins(t, ctx) // 母数を本テストのA/Bに固定（t.Cleanupで復元）

	emailA, idA := seedLoginAdmin(t, ctx, domain.RoleSuperAdmin)
	emailB, idB := seedLoginAdmin(t, ctx, domain.RoleSuperAdmin)

	// A（他にBが稼働中）は自己無効化できる（CND-14: 2人以上のため許可）
	atA := login(t, ctx, emailA, loginPassword).JSON200.AccessToken
	selfDisable := disableAccount(t, ctx, atA, idA.String())
	require.Equal(t, 200, selfDisable.StatusCode(), "他にsuper_adminがいれば自己無効化を許可")

	// 残るBは最後の稼働中super_admin＝自己無効化は409 last-super-admin（CND-14違反）
	atB := login(t, ctx, emailB, loginPassword).JSON200.AccessToken
	lastOne := disableAccount(t, ctx, atB, idB.String())
	require.Equal(t, 409, lastOne.StatusCode())
	require.NotNil(t, lastOne.ApplicationproblemJSON409)
	assert.Contains(t, lastOne.ApplicationproblemJSON409.Type, "last-super-admin")
}
