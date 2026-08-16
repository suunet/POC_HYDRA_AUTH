package tests_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authdb "poc-app-hydra/backend/auth/adapters/db"
	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
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
