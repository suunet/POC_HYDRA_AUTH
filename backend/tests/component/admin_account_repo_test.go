package tests_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authdb "poc-app-hydra/backend/auth/adapters/db"
	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
	"poc-app-hydra/backend/auth/app/command"
	"poc-app-hydra/backend/auth/domain"
)

// seedAdmin は指定statusのユーザーを作成しrole付与してuser_uuidを返す（UC-014のCND-14計数検証用）
func seedAdmin(t *testing.T, ctx context.Context, status, role string) uuid.UUID {
	t.Helper()
	email := uniqueEmail(t)
	seedLoginUser(t, ctx, email, status)
	row, err := dbmodels.New(pool).GetUserByEmail(ctx, email)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "INSERT INTO auth.user_roles (user_uuid, role) VALUES ($1, $2)", row.UserUuid, role)
	require.NoError(t, err)
	return row.UserUuid
}

// UC-014 CND-14: CountActiveSuperAdmins は稼働中（status='inactive'・削除除外）の super_admin のみ数える。
// disabled/deleted/非super_admin は計数対象外（これらを含めると最後の稼働中1人を無効化でき保護が破れる）
func TestUC014_CountActiveSuperAdmins_CountsOnlyInactiveSuperAdmins(t *testing.T) {
	ctx := context.Background()
	repo := authdb.NewUserRepository(pool)

	base, err := repo.CountActiveSuperAdmins(ctx)
	require.NoError(t, err)

	// 稼働中super_admin 2人（計数対象）
	seedAdmin(t, ctx, domain.StatusInactive, domain.RoleSuperAdmin)
	seedAdmin(t, ctx, domain.StatusInactive, domain.RoleSuperAdmin)
	// disabled な super_admin（対象外）
	seedAdmin(t, ctx, domain.StatusDisabled, domain.RoleSuperAdmin)
	// 稼働中だが operator（対象外）
	seedAdmin(t, ctx, domain.StatusInactive, domain.RoleOperator)

	after, err := repo.CountActiveSuperAdmins(ctx)
	require.NoError(t, err)
	assert.Equal(t, base+2, after, "稼働中(inactive)のsuper_adminのみ+2（disabled・operatorは除外）")

	// 削除済み super_admin（対象外）
	deletedID := seedAdmin(t, ctx, domain.StatusInactive, domain.RoleSuperAdmin)
	_, err = pool.Exec(ctx, "UPDATE auth.users SET deleted_at = now() WHERE user_uuid = $1", deletedID)
	require.NoError(t, err)

	afterDeleted, err := repo.CountActiveSuperAdmins(ctx)
	require.NoError(t, err)
	assert.Equal(t, base+2, afterDeleted, "削除済みsuper_adminは計数対象外（deleted_at除外）")
}

// UC-014 CND-14 TOCTOU: 2人の異なるsuper_adminを並行無効化しても、両方が成功して0人になってはならない。
// RepeatableRead＋FOR UPDATEで並行無効化は直列化失敗(40001)→UpdateInTxリトライが新スナップショットで
// 再評価し、正確に1件が成功・1件がErrLastSuperAdminへ倒れ1人が残る。
func TestUC014_DisableAccount_ConcurrentDisable_ProtectsLastSuperAdmin(t *testing.T) {
	ctx := context.Background()
	repo := authdb.NewUserRepository(pool)

	// グローバルなsuper_admin母数を確定させる（他テストのシード分を除外）: 既存の稼働中super_adminを
	// 一時的にdisabledへ倒し、本テストの2人だけを稼働中にする。計数はDB全体を見るため母数固定が要る。
	// NOTE: 全域UPDATEの副作用を残さないよう対象UUIDを捕捉しt.Cleanupでinactiveへ復元する（他テストへの非可逆汚染防止）
	rows, err := pool.Query(ctx, `SELECT u.user_uuid FROM auth.users u
		JOIN auth.user_roles ur ON ur.user_uuid = u.user_uuid
		WHERE ur.role = 'super_admin' AND u.status = 'inactive' AND u.deleted_at IS NULL`)
	require.NoError(t, err)
	var preexisting []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		require.NoError(t, rows.Scan(&id))
		preexisting = append(preexisting, id)
	}
	require.NoError(t, rows.Err())
	rows.Close()
	if len(preexisting) > 0 {
		_, err = pool.Exec(ctx, "UPDATE auth.users SET status = 'disabled' WHERE user_uuid = ANY($1)", preexisting)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), "UPDATE auth.users SET status = 'inactive' WHERE user_uuid = ANY($1)", preexisting)
		})
	}

	a := seedAdmin(t, ctx, domain.StatusInactive, domain.RoleSuperAdmin)
	b := seedAdmin(t, ctx, domain.StatusInactive, domain.RoleSuperAdmin)

	count, err := repo.CountActiveSuperAdmins(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 2, count, "母数確定: 稼働中super_adminはA/Bの2人のみ")

	// 2並行無効化（A・B）。同時に0人化してはならない（TOCTOU）
	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); errs[0] = repo.DisableAccount(ctx, a, true) }()
	go func() { defer wg.Done(); errs[1] = repo.DisableAccount(ctx, b, true) }()
	wg.Wait()

	success, blocked := 0, 0
	for _, e := range errs {
		switch {
		case e == nil:
			success++
		case errors.Is(e, command.ErrLastSuperAdmin):
			blocked++
		default:
			t.Fatalf("想定外のエラー: %v", e)
		}
	}
	assert.Equal(t, 1, success, "無効化に成功するのは1件のみ")
	assert.Equal(t, 1, blocked, "もう1件は最後のsuper_admin保護でErrLastSuperAdmin")

	remaining, err := repo.CountActiveSuperAdmins(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 1, remaining, "稼働中super_adminが1人残る（0人化を防止）")
}

// UC-015: ReactivateAccountはdisabled→inactiveへ遷移し、遷移元がdisabledでなければ0行＝ErrNotDisabled（E3）。
// 実DBで遷移元ガード（TransitionUserStatus WHERE status=disabled）が効くことを検証する。
func TestUC015_ReactivateAccount_TransitionGuard_OnlyFromDisabled(t *testing.T) {
	ctx := context.Background()
	repo := authdb.NewUserRepository(pool)

	// 無効化済みoperatorは再有効化に成功しinactiveへ遷移する
	disabled := seedAdmin(t, ctx, domain.StatusDisabled, domain.RoleOperator)
	require.NoError(t, repo.ReactivateAccount(ctx, disabled))
	row, err := dbmodels.New(pool).GetUserByUuid(ctx, disabled)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusInactive, row.Status, "disabled→inactiveへ遷移")

	// 無効化されていない（inactive）アカウントの再有効化は遷移元ガードで0行＝ErrNotDisabled
	notDisabled := seedAdmin(t, ctx, domain.StatusInactive, domain.RoleOperator)
	err = repo.ReactivateAccount(ctx, notDisabled)
	assert.ErrorIs(t, err, command.ErrNotDisabled, "遷移元がdisabledでなければErrNotDisabled（E3）")
}
