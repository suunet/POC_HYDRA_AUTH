BEGIN;

-- INF-07: 招待トークン（NFR-15: opaque token・SHA-256ハッシュのみ保存。VAR-07: 有効期限24時間。CND-19: 有効は常に最大1本。付与ロールはDBレコード紐付け=FR-11）
CREATE TABLE auth.invitation_tokens
(
    token_uuid uuid         NOT NULL,
    email      varchar(254) NOT NULL, -- NOTE: 受付前はユーザーが存在しないためFKでなくメールアドレスで紐付ける（VAR-01）
    role       varchar(30)  NOT NULL, -- VAR-09（管理者ロール）
    token_hash varchar(255) NOT NULL, -- NOTE: トークンは平文で保存せずハッシュで照合する
    expires_at timestamptz  NOT NULL,
    used_at    timestamptz,           -- NOTE: 消費・一括無効化の時刻（使用済みは使用時刻で表す・INF-07）
    created_at timestamptz  NOT NULL DEFAULT now(),
    PRIMARY KEY (token_uuid)
);

CREATE UNIQUE INDEX invitation_tokens_hash_unique ON auth.invitation_tokens (token_hash);

COMMIT;
