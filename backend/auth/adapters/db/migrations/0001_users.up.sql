BEGIN;

CREATE SCHEMA IF NOT EXISTS auth;

-- INF-01: ユーザー情報（STM-01 アカウント状態を status で保持・GDPR対応の論理削除カラム）
CREATE TABLE auth.users
(
    user_uuid     uuid         NOT NULL,
    email         varchar(254) NOT NULL, -- VAR-01: RFC5322準拠・最大254文字
    password_hash varchar(255) NOT NULL, -- bcrypt
    status        varchar(30)  NOT NULL, -- STM-01の英語ID（states.md 状態図エイリアス）: mail_unverified / invited / inactive / disabled / deleted
    created_at    timestamptz  NOT NULL DEFAULT now(),
    updated_at    timestamptz  NOT NULL DEFAULT now(),
    deleted_at    timestamptz,
    PRIMARY KEY (user_uuid)
);

-- NOTE: 論理削除済みを除いて一意（CND-01。削除後の同一メールアドレス再登録を許す）
CREATE UNIQUE INDEX users_email_unique ON auth.users (email) WHERE deleted_at IS NULL;

-- INF-02: ロール情報（1ユーザーに複数ロール付与可能・VAR-08/VAR-09）
CREATE TABLE auth.user_roles
(
    user_uuid  uuid        NOT NULL REFERENCES auth.users (user_uuid),
    role       varchar(30) NOT NULL,
    granted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_uuid, role)
);

-- INF-06: メール確認トークン（VAR-06: 24時間・使い切り）
CREATE TABLE auth.email_confirmation_tokens
(
    token_uuid uuid         NOT NULL,
    user_uuid  uuid         NOT NULL REFERENCES auth.users (user_uuid),
    token_hash varchar(255) NOT NULL, -- NOTE: トークンは平文で保存せずハッシュで照合する
    expires_at timestamptz  NOT NULL,
    used_at    timestamptz,
    created_at timestamptz  NOT NULL DEFAULT now(),
    PRIMARY KEY (token_uuid)
);

CREATE UNIQUE INDEX email_confirmation_tokens_hash_unique ON auth.email_confirmation_tokens (token_hash);

COMMIT;
