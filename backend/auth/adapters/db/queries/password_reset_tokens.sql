-- name: InsertPasswordResetToken :exec
INSERT INTO auth.password_reset_tokens (
	token_uuid,
	user_uuid,
	token_hash,
	expires_at
)
VALUES
	($1, $2, $3, $4);

-- name: GetPasswordResetTokenByHash :one
SELECT
	token_uuid,
	user_uuid,
	token_hash,
	expires_at,
	used_at
FROM auth.password_reset_tokens
WHERE token_hash = $1;

-- name: MarkPasswordResetTokenUsed :execrows
UPDATE auth.password_reset_tokens
SET used_at = now()
WHERE token_uuid = $1
  AND used_at IS NULL;

-- name: InvalidateActivePasswordResetTokensByUser :execrows
UPDATE auth.password_reset_tokens
SET used_at = now()
WHERE user_uuid = $1
  AND used_at IS NULL;
