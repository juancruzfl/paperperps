-- Advanced orders (M4): triggered limit orders also count as open.

-- +goose Up
DROP INDEX orders_open_by_account_idx;
CREATE INDEX orders_open_by_account_idx ON orders (account_id) WHERE status IN ('open', 'triggered');

-- +goose Down
DROP INDEX orders_open_by_account_idx;
CREATE INDEX orders_open_by_account_idx ON orders (account_id) WHERE status = 'open';
