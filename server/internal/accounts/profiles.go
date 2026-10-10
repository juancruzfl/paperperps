package accounts

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// PublicAccount returns the account behind a public profile (/u/<handle>). Only accounts
// shown on the leaderboard have one: opted in, and not hidden or suspended by a moderator.
// Hiding yourself from the leaderboard hides your profile too (Privacy Policy).
func (s *Store) PublicAccount(ctx context.Context, handle string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT id FROM accounts
		WHERE handle = $1 AND leaderboard_opt_in AND NOT leaderboard_banned AND suspended_at IS NULL`,
		strings.ToLower(strings.TrimSpace(handle))).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return id, err
}

// TraderStats summarizes an account's trading across all its runs.
type TraderStats struct {
	Trades int `json:"trades"` // fills
	// ClosedTrades are fills that closed some of a position; Wins are those that made money.
	ClosedTrades int             `json:"closedTrades"`
	Wins         int             `json:"wins"`
	BestTrade    decimal.Decimal `json:"bestTrade"` // largest closed PnL of one fill; 0 if none
	Volume       decimal.Decimal `json:"volume"`    // traded notional
}

func (s *Store) TraderStats(ctx context.Context, id uuid.UUID) (TraderStats, error) {
	var t TraderStats
	err := s.pool.QueryRow(ctx, `
		SELECT count(*),
			count(*) FILTER (WHERE closed_pnl <> 0),
			count(*) FILTER (WHERE closed_pnl > 0),
			coalesce(max(closed_pnl) FILTER (WHERE closed_pnl > 0), 0),
			coalesce(sum(px * sz), 0)
		FROM fills WHERE account_id = $1`, id).Scan(&t.Trades, &t.ClosedTrades, &t.Wins, &t.BestTrade, &t.Volume)
	if err != nil {
		return TraderStats{}, fmt.Errorf("trader stats: %w", err)
	}
	t.Volume = t.Volume.Round(2)
	return t, nil
}
