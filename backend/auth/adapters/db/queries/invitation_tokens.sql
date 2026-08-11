-- name: InsertInvitationToken :exec
INSERT INTO auth.invitation_tokens (
	token_uuid,
	email,
	role,
	token_hash,
	expires_at
)
VALUES
	($1, $2, $3, $4, $5);

-- name: GetInvitationTokenByHash :one
SELECT
	token_uuid,
	email,
	role,
	token_hash,
	expires_at,
	used_at
FROM auth.invitation_tokens
WHERE token_hash = $1;

-- name: MarkInvitationTokenUsed :execrows
UPDATE auth.invitation_tokens
SET used_at = now()
WHERE token_uuid = $1
  AND used_at IS NULL;

-- name: InvalidateActiveInvitationTokensByEmail :execrows
UPDATE auth.invitation_tokens
SET used_at = now()
WHERE email = $1
  AND used_at IS NULL;
