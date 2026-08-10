BEGIN;

-- INF-05: パスワードリセットトークン（NFR-15: opaque token・SHA-256ハッシュのみ保存。VAR-05: 有効期限30分。CND-18: 有効は常に最大1本）
CREATE TABLE auth.password_reset_tokens
(
    token_uuid uuid         NOT NULL,
    user_uuid  uuid         NOT NULL REFERENCES auth.users (user_uuid),
    token_hash varchar(255) NOT NULL, -- NOTE: トークンは平文で保存せずハッシュで照合する
    expires_at timestamptz  NOT NULL,
    used_at    timestamptz,           -- NOTE: 消費・一括無効化の時刻（使用済みは使用時刻で表す・INF-05）
    created_at timestamptz  NOT NULL DEFAULT now(),
    PRIMARY KEY (token_uuid)
);

CREATE UNIQUE INDEX password_reset_tokens_hash_unique ON auth.password_reset_tokens (token_hash);

COMMIT;
