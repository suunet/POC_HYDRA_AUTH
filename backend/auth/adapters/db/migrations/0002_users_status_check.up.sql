BEGIN;

-- STM-01: auth.users.status を states.md 状態図の5状態に制限する
ALTER TABLE auth.users
    ADD CONSTRAINT users_status_check
        CHECK (status IN ('mail_unverified', 'invited', 'inactive', 'disabled', 'deleted'));

COMMIT;
