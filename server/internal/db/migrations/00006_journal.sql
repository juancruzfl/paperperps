-- Trade journal: a private note per fill, written by the account that made it.

-- +goose Up
CREATE TABLE fill_notes (
    fill_id    uuid PRIMARY KEY REFERENCES fills (id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    note       text NOT NULL CHECK (char_length(note) BETWEEN 1 AND 1000),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE fill_notes;
