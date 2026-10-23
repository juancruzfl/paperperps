-- Degen mode: an opt-in that allows leverage above Hyperliquid's cap, up to 1000x.
-- degen_at marks the first fill that grew a position above the cap; the leaderboard and
-- profile show a badge from then on, across resets.

-- +goose Up
ALTER TABLE accounts
    ADD COLUMN degen_mode boolean NOT NULL DEFAULT false,
    ADD COLUMN degen_at   timestamptz;

-- +goose Down
ALTER TABLE accounts
    DROP COLUMN degen_at,
    DROP COLUMN degen_mode;
