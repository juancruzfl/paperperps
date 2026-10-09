package engine

import (
	"context"
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
	leverage     int
	balanceDelta decimal.Decimal // closed PnL - fee
}

func insertOrder(ctx context.Context, tx pgx.Tx, accountID, runID uuid.UUID, o accounts.Order) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO orders (id, account_id, run_id, coin, side, type, px, sz, filled_sz, tif,
			reduce_only, status, status_reason, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		o.ID, accountID, runID, o.Coin, o.Side, o.Type, o.Px, o.Sz, o.FilledSz, o.TIF,
		o.ReduceOnly, o.Status, o.StatusReason, o.CreatedAt, o.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert order: %w", err)
	}
	return nil
}

func updateOrder(ctx context.Context, tx pgx.Tx, o accounts.Order) error {
	_, err := tx.Exec(ctx, `
		UPDATE orders SET filled_sz = $2, status = $3, status_reason = $4, updated_at = $5
		WHERE id = $1`, o.ID, o.FilledSz, o.Status, o.StatusReason, o.UpdatedAt)
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

	ledger := func(kind string, amount decimal.Decimal) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO ledger_entries (id, account_id, run_id, kind, amount, ref_id, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			accounts.NewID(), accountID, runID, kind, amount, f.ID, f.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert %s ledger entry: %w", kind, err)
		}
		return nil
	}
	if !f.ClosedPnl.IsZero() {
		if err := ledger("fill_pnl", f.ClosedPnl); err != nil {
			return err
		}
	}
	if !f.Fee.IsZero() {
		if err := ledger("fee", f.Fee.Neg()); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE runs SET balance = balance + $2 WHERE id = $1`, runID, r.balanceDelta); err != nil {
		return fmt.Errorf("update balance: %w", err)
	}
	return upsertPosition(ctx, tx, accountID, runID, f.Coin, r.newSize, r.newEntry, r.leverage, f.CreatedAt)
}

func upsertPosition(ctx context.Context, tx pgx.Tx, accountID, runID uuid.UUID, coin string,
	size, entry decimal.Decimal, leverage int, now time.Time) error {
	entryPx := decimal.NullDecimal{Decimal: entry, Valid: !size.IsZero()}
	_, err := tx.Exec(ctx, `
		INSERT INTO positions (account_id, coin, run_id, size, entry_px, margin_mode, leverage, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'cross', $6, $7)
		ON CONFLICT (account_id, coin) DO UPDATE
		SET run_id = $3, size = $4, entry_px = $5, leverage = $6, updated_at = $7`,
		accountID, coin, runID, size, entryPx, leverage, now)
	if err != nil {
		return fmt.Errorf("upsert position: %w", err)
	}
	return nil
}
