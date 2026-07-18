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

-- name: UpdateUserStatus :exec
UPDATE auth.users
SET status = $2,
	updated_at = now()
WHERE user_uuid = $1
  AND deleted_at IS NULL;
