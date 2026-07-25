BEGIN;

-- INF-04: リフレッシュトークン（NFR-14: opaque token・SHA-256ハッシュのみ保存・family_id一括失効。VAR-04: 有効期限30日）
CREATE TABLE auth.refresh_tokens
(
    token_id          uuid         NOT NULL,
    user_uuid         uuid         NOT NULL REFERENCES auth.users (user_uuid),
    family_id         uuid         NOT NULL, -- NFR-14: 同一ローテーションチェーンの識別子。再利用検知時はfamily単位で一括失効
    parent_token_id   uuid REFERENCES auth.refresh_tokens (token_id), -- NFR-14: 前トークンの参照（初回発行はnull。失効は論理のため親行の物理削除はなくFK安全）
    token_hash        varchar(255) NOT NULL, -- NOTE: 平文は保存しない（SHA-256ハッシュで照合）
    expires_at        timestamptz  NOT NULL,
    used_at           timestamptz,           -- ローテーション消費時刻（使用済みは使用時刻で表す・INF-04。消費はBUC-U05で実装）
    revoked_at        timestamptz,           -- 失効時刻（消費とは別概念）
    revocation_reason varchar(50),           -- VAR-10: セッション失効理由コード
    created_at        timestamptz  NOT NULL DEFAULT now(),
    PRIMARY KEY (token_id)
);

CREATE UNIQUE INDEX refresh_tokens_hash_unique ON auth.refresh_tokens (token_hash);
-- NOTE: family一括失効（NFR-14）・ユーザー単位失効（BUC-A03等）用
CREATE INDEX refresh_tokens_family_idx ON auth.refresh_tokens (family_id);
CREATE INDEX refresh_tokens_user_idx ON auth.refresh_tokens (user_uuid);

COMMIT;
