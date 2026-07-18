-- name: InsertEmailConfirmationToken :exec
INSERT INTO auth.email_confirmation_tokens (
	token_uuid,
	user_uuid,
	token_hash,
	expires_at
)
VALUES
	($1, $2, $3, $4);

-- name: GetEmailConfirmationTokenByHash :one
SELECT
	token_uuid,
	user_uuid,
	token_hash,
	expires_at,
	used_at
FROM auth.email_confirmation_tokens
WHERE token_hash = $1;

-- name: MarkEmailConfirmationTokenUsed :exec
UPDATE auth.email_confirmation_tokens
SET used_at = now()
WHERE token_uuid = $1;
