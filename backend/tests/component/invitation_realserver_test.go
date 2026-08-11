package tests_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	oapitypes "github.com/oapi-codegen/runtime/types"
	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
	authclient "poc-app-hydra/backend/auth/api/http/client"
	"poc-app-hydra/backend/auth/domain"
)

const invitePassword = "invited-admin-pw-01!"

// seedSuperAdmin はsuper_adminロールのログイン可能ユーザーを作成しATを返す（UC-011の操作者・ACT-02）
func seedSuperAdmin(t *testing.T, ctx context.Context) string {
	t.Helper()
	email := uniqueEmail(t)
	seedLoginUser(t, ctx, email, domain.StatusInactive)
	row, err := dbmodels.New(pool).GetUserByEmail(ctx, email)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		"INSERT INTO auth.user_roles (user_uuid, role) VALUES ($1, $2)", row.UserUuid, domain.RoleSuperAdmin)
	require.NoError(t, err)
	res := login(t, ctx, email, loginPassword)
	require.Equal(t, 200, res.StatusCode())
	return res.JSON200.AccessToken
}

func invite(t *testing.T, ctx context.Context, at, email, role string) *authclient.InviteAdminResponse {
	t.Helper()
	editors := []authclient.RequestEditorFn{}
	if at != "" {
		editors = append(editors, withBearer(at))
	}
	resp, err := client.InviteAdminWithResponse(ctx, authclient.InviteAdminJSONRequestBody{
		Email: oapitypes.Email(email),
		Role:  role,
	}, editors...)
	require.NoError(t, err)
	return resp
}

func acceptInvite(t *testing.T, ctx context.Context, token, password string) *authclient.AcceptInvitationResponse {
	t.Helper()
	resp, err := client.AcceptInvitationWithResponse(ctx, authclient.AcceptInvitationJSONRequestBody{
		Token:    token,
		Password: password,
	})
	require.NoError(t, err)
	return resp
}

// clearInviteRateWindow はレート窓（INF-12・TTL5分）を消し、同一メールでの連続招待を試験可能にする
func clearInviteRateWindow(t *testing.T, ctx context.Context, email string) {
	t.Helper()
	require.NoError(t, redisClient.Del(ctx, "invitation:"+email).Err())
}

// UC-011→UC-012 主成功E2E: super_adminが招待→メールの平文トークンで受付→
// 管理者アカウント作成（未認証・紐付けロール）→UC-005でログイン可・トークンused_at打刻
func TestUC011_UC012_Invitation_RealServer_FullFlow(t *testing.T) {
	ctx := context.Background()
	at := seedSuperAdmin(t, ctx)
	inviteeEmail := uniqueEmail(t)
	t.Cleanup(func() { clearInviteRateWindow(t, ctx, inviteeEmail) })

	before := len(mailer.Sent())
	require.Equal(t, 200, invite(t, ctx, at, inviteeEmail, "operator").StatusCode())
	plainToken := sentResetTokenFor(t, inviteeEmail, before)

	require.Equal(t, 200, acceptInvite(t, ctx, plainToken, invitePassword).StatusCode())

	// アカウント実測: 未認証（STM-01）＋紐付けロール（FR-13）
	row, err := dbmodels.New(pool).GetUserByEmail(ctx, inviteeEmail)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusInactive, row.Status, "STM-01.未認証で作成（Q-2=B案）")
	var role string
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT role FROM auth.user_roles WHERE user_uuid = $1", row.UserUuid).Scan(&role))
	assert.Equal(t, "operator", role)
	// トークン消費（CND-19・STM相当の終端）
	tokenRec, err := dbmodels.New(pool).GetInvitationTokenByHash(ctx, domain.HashInvitationToken(plainToken))
	require.NoError(t, err)
	assert.NotNil(t, tokenRec.UsedAt)
	// 受付後にUC-005でログイン可能（認証は受付では行わない）
	loginRes := login(t, ctx, inviteeEmail, invitePassword)
	assert.Equal(t, 200, loginRes.StatusCode(), "受付後はUC-005でログイン")
}

// UC-011 M1/M2: AT欠落は401一様・super_admin以外（user役）は403 forbidden（実挙動トリップワイヤ）
func TestUC011_Invitation_RealServer_AuthzEnforced(t *testing.T) {
	ctx := context.Background()
	inviteeEmail := uniqueEmail(t)
	t.Cleanup(func() { clearInviteRateWindow(t, ctx, inviteeEmail) })

	noAT := invite(t, ctx, "", inviteeEmail, "operator")
	require.Equal(t, 401, noAT.StatusCode(), "M1: 401一様")
	assert.Equal(t, "Bearer", noAT.HTTPResponse.Header.Get("WWW-Authenticate"))

	userEmail := uniqueEmail(t)
	userAT, _ := loginTokens(t, ctx, userEmail) // user役
	rec := invite(t, ctx, userAT, inviteeEmail, "operator")
	require.Equal(t, 403, rec.StatusCode(), "M2: super_admin以外は403")
	require.NotNil(t, rec.ApplicationproblemJSON403)
	assert.Contains(t, rec.ApplicationproblemJSON403.Type, "forbidden")
}

// CND-19 E2E: 再招待で旧トークンは無効化（E3合流の400）・受付後の同一トークン再利用も400
func TestUC011_UC012_Invitation_RealServer_CND19_SingleActiveToken(t *testing.T) {
	ctx := context.Background()
	at := seedSuperAdmin(t, ctx)
	inviteeEmail := uniqueEmail(t)
	t.Cleanup(func() { clearInviteRateWindow(t, ctx, inviteeEmail) })

	before1 := len(mailer.Sent())
	require.Equal(t, 200, invite(t, ctx, at, inviteeEmail, "operator").StatusCode())
	token1 := sentResetTokenFor(t, inviteeEmail, before1)
	clearInviteRateWindow(t, ctx, inviteeEmail)

	before2 := len(mailer.Sent())
	require.Equal(t, 200, invite(t, ctx, at, inviteeEmail, "system_admin").StatusCode())
	token2 := sentResetTokenFor(t, inviteeEmail, before2)
	require.NotEqual(t, token1, token2)

	reuseOld := acceptInvite(t, ctx, token1, invitePassword)
	require.Equal(t, 400, reuseOld.StatusCode(), "CND-19/FR-12: 再招待で旧トークンは無効化済み")
	assert.Contains(t, reuseOld.ApplicationproblemJSON400.Type, "invalid-token")

	require.Equal(t, 200, acceptInvite(t, ctx, token2, invitePassword).StatusCode())
	replay := acceptInvite(t, ctx, token2, "another-admin-pw-02!")
	require.Equal(t, 400, replay.StatusCode(), "受付済みトークンの再利用不可")
	assert.Contains(t, replay.ApplicationproblemJSON400.Type, "invalid-token")
	// 最終ロールは2本目（system_admin）
	row, err := dbmodels.New(pool).GetUserByEmail(ctx, inviteeEmail)
	require.NoError(t, err)
	var role string
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT role FROM auth.user_roles WHERE user_uuid = $1", row.UserUuid).Scan(&role))
	assert.Equal(t, "system_admin", role)
}

// UC-012 E6レース: 招待発行〜受付間に同一メールで自己登録されると受付は409（500にしない・トークン非消費）
func TestUC012_Invitation_RealServer_E6RegistrationRace_Returns409(t *testing.T) {
	ctx := context.Background()
	at := seedSuperAdmin(t, ctx)
	inviteeEmail := uniqueEmail(t)
	t.Cleanup(func() { clearInviteRateWindow(t, ctx, inviteeEmail) })

	before := len(mailer.Sent())
	require.Equal(t, 200, invite(t, ctx, at, inviteeEmail, "operator").StatusCode())
	token := sentResetTokenFor(t, inviteeEmail, before)

	// 招待後の自己登録レースを直接注入（UC-002相当のレコード実在）
	seedLoginUser(t, ctx, inviteeEmail, domain.StatusMailUnverified)

	res := acceptInvite(t, ctx, token, invitePassword)
	require.Equal(t, 409, res.StatusCode(), "E6: 500にしない")
	require.NotNil(t, res.ApplicationproblemJSON409)
	assert.Contains(t, res.ApplicationproblemJSON409.Type, "email-already-registered")
	tokenRec, err := dbmodels.New(pool).GetInvitationTokenByHash(ctx, domain.HashInvitationToken(token))
	require.NoError(t, err)
	assert.Nil(t, tokenRec.UsedAt, "E6: トークン非消費（Q-6）")
}

// UC-011 E3: 同一メール2回目は429（VAR-14・retry_after=TTL残秒＋Retry-Afterヘッダ）
func TestUC011_Invitation_RealServer_RateLimited_429(t *testing.T) {
	ctx := context.Background()
	at := seedSuperAdmin(t, ctx)
	inviteeEmail := uniqueEmail(t)
	t.Cleanup(func() { clearInviteRateWindow(t, ctx, inviteeEmail) })

	require.Equal(t, 200, invite(t, ctx, at, inviteeEmail, "operator").StatusCode())
	second := invite(t, ctx, at, inviteeEmail, "operator")
	require.Equal(t, 429, second.StatusCode(), "窓内2回目は429")
	require.NotNil(t, second.ApplicationproblemJSON429)
	require.NotNil(t, second.ApplicationproblemJSON429.RetryAfter)
	assert.Greater(t, *second.ApplicationproblemJSON429.RetryAfter, 0)
	assert.NotEmpty(t, second.HTTPResponse.Header.Get("Retry-After"), "Retry-Afterヘッダ（VAR-14③）")
}
