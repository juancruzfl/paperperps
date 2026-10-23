package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/juancruzfl/paperperps/server/internal/accounts"
)

// fillRecord is everything one fill writes. It is computed before the transaction and
// applied to memory only after the commit succeeds.
type fillRecord struct {
	fill         accounts.Fill
	newSize      decimal.Decimal
	newEntry     decimal.Decimal
	newIsoMargin decimal.Decimal
	leverage     int
	isolated     bool
	balanceDelta decimal.Decimal // closed PnL - fee
	aboveCap     bool            // grew a position at leverage above the market's cap
}

func insertOrder(ctx context.Context, tx pgx.Tx, accountID, runID uuid.UUID, o accounts.Order) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO orders (id, account_id, run_id, coin, side, type, px, trigger_px, sz, filled_sz, tif,
			reduce_only, parent_order_id, oco_group, status, status_reason, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`,
		o.ID, accountID, runID, o.Coin, o.Side, o.Type, o.Px, o.TriggerPx, o.Sz, o.FilledSz, o.TIF,
		o.ReduceOnly, o.ParentOrderID, o.OcoGroup, o.Status, o.StatusReason, o.CreatedAt, o.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert order: %w", err)
	}
	return nil
}

func updateOrder(ctx context.Context, tx pgx.Tx, o accounts.Order) error {
	_, err := tx.Exec(ctx, `
		UPDATE orders SET filled_sz = $2, status = $3, status_reason = $4, updated_at = $5,
			sz = $6, px = $7, trigger_px = $8
		WHERE id = $1`, o.ID, o.FilledSz, o.Status, o.StatusReason, o.UpdatedAt, o.Sz, o.Px, o.TriggerPx)
	if err != nil {
		return fmt.Errorf("update order: %w", err)
	}
	return nil
}

// recordFill writes a fill, its ledger entries, the new run balance and the new position.
func recordFill(ctx context.Context, tx pgx.Tx, accountID, runID uuid.UUID, r fillRecord) error {
	f := r.fill
	if _, err := tx.Exec(ctx, `
		INSERT INTO fills (id, order_id, account_id, run_id, coin, side, px, sz, fee, liquidity,
			closed_pnl, is_liquidation, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		f.ID, f.OrderID, accountID, runID, f.Coin, f.Side, f.Px, f.Sz, f.Fee, f.Liquidity,
		f.ClosedPnl, f.IsLiquidation, f.CreatedAt); err != nil {
		return fmt.Errorf("insert fill: %w", err)
	}

	if err := addLedger(ctx, tx, accountID, runID, "fill_pnl", f.ClosedPnl, f.ID, f.CreatedAt); err != nil {
		return err
	}
	if err := addLedger(ctx, tx, accountID, runID, "fee", f.Fee.Neg(), f.ID, f.CreatedAt); err != nil {
		return err
	}
	if err := addBalance(ctx, tx, runID, r.balanceDelta); err != nil {
		return err
	}
	if r.aboveCap {
		if _, err := tx.Exec(ctx, `UPDATE accounts SET degen_at = coalesce(degen_at, $2) WHERE id = $1`,
			accountID, f.CreatedAt); err != nil {
			return fmt.Errorf("mark degen: %w", err)
		}
	}
	return upsertPosition(ctx, tx, accountID, runID, f.Coin, &position{
		size: r.newSize, entryPx: r.newEntry, leverage: r.leverage, isolated: r.isolated, isoMargin: r.newIsoMargin,
	}, f.CreatedAt)
}

// addLedger records a balance change. Zero amounts are skipped.
func addLedger(ctx context.Context, tx pgx.Tx, accountID, runID uuid.UUID, kind string,
	amount decimal.Decimal, refID uuid.UUID, at time.Time) error {
	if amount.IsZero() {
		return nil
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO ledger_entries (id, account_id, run_id, kind, amount, ref_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		accounts.NewID(), accountID, runID, kind, amount, refID, at)
	if err != nil {
		return fmt.Errorf("insert %s ledger entry: %w", kind, err)
	}
	return nil
}

// addBalance moves the run's cached balance. Callers add matching ledger entries in the
// same transaction.
func addBalance(ctx context.Context, tx pgx.Tx, runID uuid.UUID, delta decimal.Decimal) error {
	if _, err := tx.Exec(ctx, `UPDATE runs SET balance = balance + $2 WHERE id = $1`, runID, delta); err != nil {
		return fmt.Errorf("update balance: %w", err)
	}
	return nil
}

func upsertPosition(ctx context.Context, tx pgx.Tx, accountID, runID uuid.UUID, coin string, p *position, now time.Time) error {
	entryPx := decimal.NullDecimal{Decimal: p.entryPx, Valid: !p.size.IsZero()}
	_, err := tx.Exec(ctx, `
		INSERT INTO positions (account_id, coin, run_id, size, entry_px, margin_mode, leverage, isolated_margin, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (account_id, coin) DO UPDATE
		SET run_id = $3, size = $4, entry_px = $5, margin_mode = $6, leverage = $7, isolated_margin = $8, updated_at = $9`,
		accountID, coin, runID, p.size, entryPx, p.mode(), p.leverage, p.isoMargin, now)
	if err != nil {
		return fmt.Errorf("upsert position: %w", err)
	}
	return nil
}

const lastFundingKey = "last_funding_hour"

// loadLastFunding returns the last funding hour settled. On a fresh database it is the
// current hour, so the first payment happens at the next hour boundary.
func loadLastFunding(ctx context.Context, q accounts.Querier, now time.Time) (time.Time, error) {
	var t time.Time
	err := q.QueryRow(ctx, `SELECT value FROM engine_state WHERE key = $1`, lastFundingKey).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return now.Truncate(time.Hour), nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("load last funding hour: %w", err)
	}
	return t.UTC(), nil
}

func saveLastFunding(ctx context.Context, tx pgx.Tx, hour time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO engine_state (key, value, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = $2, updated_at = now()`, lastFundingKey, hour)
	if err != nil {
		return fmt.Errorf("save last funding hour: %w", err)
	}
	return nil
}
