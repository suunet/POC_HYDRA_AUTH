BEGIN;

-- STM-01: 'invited' を許可値から除去（states.md是正・CND-19: 招待済み未受付はINF-07（招待トークン）の有効未使用トークンの存在で表す）
ALTER TABLE auth.users
    DROP CONSTRAINT users_status_check;
ALTER TABLE auth.users
    ADD CONSTRAINT users_status_check
        CHECK (status IN ('mail_unverified', 'inactive', 'disabled', 'deleted'));

COMMIT;
