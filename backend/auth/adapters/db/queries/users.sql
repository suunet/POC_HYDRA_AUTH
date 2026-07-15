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

-- name: InsertEmailConfirmationToken :exec
INSERT INTO auth.email_confirmation_tokens (
	token_uuid,
	user_uuid,
	token_hash,
	expires_at
)
VALUES
	($1, $2, $3, $4);
