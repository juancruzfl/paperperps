-- Moderation (docs/operations.md, "Admin tool"): accounts hidden from the leaderboard by
-- an admin, suspended accounts, an audit log of every admin action, and the nonces that
-- stop a signed admin request from being replayed.

-- +goose Up
ALTER TABLE accounts
    ADD COLUMN leaderboard_banned boolean NOT NULL DEFAULT false,
    ADD COLUMN suspended_at       timestamptz,
    ADD COLUMN suspend_reason     text;

-- No foreign key: the record of a deletion must outlive the account.
CREATE TABLE admin_actions (
    id         bigserial PRIMARY KEY,
    at         timestamptz NOT NULL DEFAULT now(),
    action     text NOT NULL,
    account_id uuid,
    handle     text, -- at the time of the action
    reason     text NOT NULL,
    detail     jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX admin_actions_account_idx ON admin_actions (account_id, at);

CREATE TABLE admin_nonces (
    nonce      text PRIMARY KEY,
    expires_at timestamptz NOT NULL
);
CREATE INDEX admin_nonces_expiry_idx ON admin_nonces (expires_at);

-- +goose Down
DROP TABLE admin_nonces;
DROP TABLE admin_actions;
ALTER TABLE accounts
    DROP COLUMN suspend_reason,
    DROP COLUMN suspended_at,
    DROP COLUMN leaderboard_banned;
