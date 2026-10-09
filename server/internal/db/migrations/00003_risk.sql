-- Risk (M3): bad-debt ledger entries and engine bookkeeping.

-- +goose Up
-- bad_debt credits the part of a liquidation loss beyond the margin at risk, so an account
-- can never owe money (the "backstop" absorbs it).
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check CHECK (kind IN (
    'deposit', 'fill_pnl', 'fee', 'funding', 'liquidation_fee', 'bad_debt', 'reset'));

-- Small key/value store for engine progress, e.g. the last funding hour settled.
CREATE TABLE engine_state (
    key        text PRIMARY KEY,
    value      timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX positions_open_idx ON positions (account_id) WHERE size <> 0;

-- +goose Down
DROP INDEX positions_open_idx;
DROP TABLE engine_state;
DELETE FROM ledger_entries WHERE kind = 'bad_debt';
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check CHECK (kind IN (
    'deposit', 'fill_pnl', 'fee', 'funding', 'liquidation_fee', 'reset'));
