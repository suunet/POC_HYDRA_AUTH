package db

import (
	"context"

	"poc-app-hydra/backend/auth/adapters/db/dbmodels"
)

// CND-14・UC-014 E4（最後の稼働中super_admin保護）の判定に用いる。計数条件はクエリ側NOTEに記す。
func (r *UserRepository) CountActiveSuperAdmins(ctx context.Context) (int64, error) {
	return dbmodels.New(r.db).CountActiveSuperAdmins(ctx)
}
