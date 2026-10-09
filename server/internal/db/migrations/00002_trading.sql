-- Trading state for M2: a cached balance per run, and order status reasons.

-- +goose Up
-- Running balance of the run. Always equals the sum of the run's ledger entries; both are
-- written in the same transaction.
ALTER TABLE runs ADD COLUMN balance numeric NOT NULL DEFAULT 0;

-- Why an order was cancelled (by the user, IOC remainder, insufficient margin...).
ALTER TABLE orders RENAME COLUMN reject_reason TO status_reason;

CREATE INDEX orders_open_by_account_idx ON orders (account_id) WHERE status = 'open';

-- +goose Down
DROP INDEX orders_open_by_account_idx;
ALTER TABLE orders RENAME COLUMN status_reason TO reject_reason;
ALTER TABLE runs DROP COLUMN balance;
