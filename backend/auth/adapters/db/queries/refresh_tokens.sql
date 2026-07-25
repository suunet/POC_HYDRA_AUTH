-- name: InsertRefreshToken :exec
INSERT INTO auth.refresh_tokens (
	token_id,
	user_uuid,
	family_id,
	parent_token_id,
	token_hash,
	expires_at
)
VALUES
	($1, $2, $3, $4, $5, $6);

-- name: GetRefreshTokenByHash :one
SELECT
	token_id,
	user_uuid,
	family_id,
	parent_token_id,
	token_hash,
	expires_at,
	used_at,
	revoked_at,
	revocation_reason,
	created_at
FROM auth.refresh_tokens
WHERE token_hash = $1;
