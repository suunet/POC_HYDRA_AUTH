-- name: InsertUser :exec
INSERT INTO auth.users (
	user_uuid,
	email,
	password_hash,
	status
)
VALUES
	($1, $2, $3, $4);

-- name: GetUserByEmail :one
SELECT
	user_uuid,
	email,
	password_hash,
	status,
	created_at,
	updated_at
FROM auth.users
WHERE email = $1
  AND deleted_at IS NULL;

-- name: InsertUserRole :exec
INSERT INTO auth.user_roles (
	user_uuid,
	role
)
VALUES
	($1, $2);

-- name: TransitionUserStatus :execrows
UPDATE auth.users
SET status = $2,
	updated_at = now()
WHERE user_uuid = $1
  AND status = $3
  AND deleted_at IS NULL;

-- name: GetUserRoles :many
SELECT
	role
FROM auth.user_roles
WHERE user_uuid = $1
ORDER BY role;

-- name: GetUserByUuid :one
-- NOTE: UC-006: token→user 取得（削除済み除外・E5）
SELECT
	user_uuid,
	email,
	password_hash,
	status,
	created_at,
	updated_at
FROM auth.users
WHERE user_uuid = $1
  AND deleted_at IS NULL;

-- name: UpdateUserPassword :execrows
-- NOTE: UC-010: パスワード更新（削除済み除外）。0行=ユーザー不存在（削除レース）＝呼出側がTx全体を失敗させる
UPDATE auth.users
SET password_hash = $2,
    updated_at = now()
WHERE user_uuid = $1
  AND deleted_at IS NULL;

-- name: CountActiveSuperAdmins :one
-- NOTE: CND-14: 稼働中（status='inactive'・削除除外）の super_admin 数。無効化済み/削除済みは含めない
-- （含めると最後の稼働中1人を無効化でき保護が破れる）。UC-014のE4判定に用いる
SELECT count(*)
FROM auth.users u
JOIN auth.user_roles ur ON ur.user_uuid = u.user_uuid
WHERE ur.role = 'super_admin'
  AND u.status = 'inactive'
  AND u.deleted_at IS NULL;

-- name: LockActiveSuperAdmins :many
-- NOTE: CND-14 TOCTOU: 稼働中super_admin行をFOR UPDATEでロックし無効化Tx内で件数評価する（最後の1人保護・UC-014 E4）。
-- Tx分離はRepeatableRead（common.UpdateInTx）。並行無効化が先にコミット済みだと本FOR UPDATEは直列化失敗
-- （40001）となり、UpdateInTxのリトライが新スナップショットで再評価する＝相手の無効化後の件数で判定される。
-- ORDER BYはロック取得順を全Txで一意にしデッドロックを防ぐ（異なる2人の並行無効化が逆順ロックで詰まらない）
SELECT u.user_uuid
FROM auth.users u
JOIN auth.user_roles ur ON ur.user_uuid = u.user_uuid
WHERE ur.role = 'super_admin'
  AND u.status = 'inactive'
  AND u.deleted_at IS NULL
ORDER BY u.user_uuid
FOR UPDATE OF u;
