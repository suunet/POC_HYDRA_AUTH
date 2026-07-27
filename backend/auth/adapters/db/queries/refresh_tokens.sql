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

-- name: MarkRefreshTokenUsed :execrows
-- NOTE: UC-006 ローテーション: 使用済みは使用時刻で表す（INF-04・used_at＝消費）。
-- used_at IS NULL 条件付きの check-and-set で二重消費を直列化する（更新0行＝並行リクエストに先を越された
-- ＝再利用相当。呼出側は family一括失効へ倒す・NFR-14・BJ c4#1）
UPDATE auth.refresh_tokens
SET used_at = now()
WHERE token_id = $1
  AND used_at IS NULL;

-- name: RevokeRefreshToken :exec
-- NOTE: UC-006 E3/E5/E6: 当該トークンのみ失効（revoked_at＝失効・used_atと別概念）。
-- family失効と対称に既失効は上書きしない（先行失効の理由コード保護・BJ c2#1）
UPDATE auth.refresh_tokens
SET revoked_at = now(),
    revocation_reason = $2
WHERE token_id = $1
  AND revoked_at IS NULL;

-- name: RevokeRefreshTokenFamily :exec
-- NOTE: UC-006 E4: 同一family（ローテーションチェーン）を一括失効（NFR-14・他familyは生かす）。既失効は上書きしない
UPDATE auth.refresh_tokens
SET revoked_at = now(),
    revocation_reason = $2
WHERE family_id = $1
  AND revoked_at IS NULL;
