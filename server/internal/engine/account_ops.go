package engine

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/juancruzfl/paperperps/server/internal/accounts"
)

// --- reset ---

// ResetAccount starts the account over with a fresh bankroll (docs/plan.md §5.3). Open
// orders are cancelled and positions are wiped without trading. The positions' unrealized
// PnL at the moment of reset is locked into the leaderboard score, so a reset can't erase a
// losing trade.
func (e *Engine) ResetAccount(ctx context.Context, accountID uuid.UUID) error {
	return e.do(ctx, func(ctx context.Context) error { return e.resetAccount(ctx, accountID) })
}

func (e *Engine) resetAccount(ctx context.Context, accountID uuid.UUID) error {
	a, err := e.account(ctx, accountID)
	if err != nil {
		return err
	}
	now := e.now()
	if a.lastResetAt != nil {
		if next := a.lastResetAt.Add(e.cfg.ResetCooldown); now.Before(next) {
			return reject("reset_cooldown", "you can reset again in %s", roundDuration(next.Sub(now)))
		}
	}

	locked := zero
	for coin, p := range a.positions {
		if p.size.IsZero() {
			continue
		}
		t, ok := e.md.Ticker(coin)
		if !ok || t.Stale {
			return reject("market_stale", "%s prices aren't live right now, so your open position can't be valued; try again shortly", coin)
		}
		locked = locked.Add(p.size.Mul(t.MarkPx.Sub(p.entryPx)))
	}
	locked = locked.Round(usdDecimals)

	var cancelled []accounts.Order
	for _, o := range a.orders {
		c := *o
		c.Status, c.StatusReason, c.UpdatedAt = StatusCancelled, ptr("cancelled: account reset"), now
		cancelled = append(cancelled, c)
	}
	newRun := accounts.NewID()
	start := e.cfg.StartingBalance
	err = pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
		for _, o := range cancelled {
			if err := updateOrder(ctx, tx, o); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE runs SET ended_at = $2, locked_upnl = $3 WHERE id = $1`, a.runID, now, locked); err != nil {
			return fmt.Errorf("end run: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO runs (id, account_id, starting_balance, balance, started_at)
			VALUES ($1, $2, $3, $3, $4)`, newRun, a.id, start, now); err != nil {
			return fmt.Errorf("start run: %w", err)
		}
		if err := addLedger(ctx, tx, a.id, newRun, "deposit", start, newRun, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM positions WHERE account_id = $1`, a.id); err != nil {
			return fmt.Errorf("clear positions: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE accounts SET current_run_id = $2, last_reset_at = $3 WHERE id = $1`,
			a.id, newRun, now); err != nil {
			return fmt.Errorf("switch run: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	for _, o := range a.orders {
		e.unindex(o)
	}
	ended := Score(zero, a.balance, locked, a.startingBalance)
	a.priorPnl = a.priorPnl.Add(ended)
	a.runID, a.balance, a.startingBalance = newRun, start, start
	a.positions = map[string]*position{}
	a.orders = map[uuid.UUID]*accounts.Order{}
	a.lastResetAt = &now
	a.dirty = true
	slog.Info("account reset", "account", a.id, "locked_upnl", locked, "ended_run_pnl", ended, "lifetime_pnl", a.priorPnl)
	e.publish(a.id, AccountEvent{Type: "reset"})
	return nil
}

func roundDuration(d time.Duration) string {
	if d > time.Hour {
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", max(1, int(d.Minutes())))
}

// --- equity snapshots ---

// snapshot records equity and lifetime PnL for accounts with open positions (their value
// moves with prices) and accounts that changed since the last snapshot. These feed the PnL
// chart and the leaderboard's 24h/7d/30d baselines. dirtyOnly is for the final flush at
// shutdown.
func (e *Engine) snapshot(ctx context.Context, dirtyOnly bool) {
	ts := e.now().Truncate(time.Minute)
	type row struct {
		a             *account
		equity, score decimal.Decimal
	}
	var rows []row
	for _, id := range e.accountIDs() {
		a := e.accounts[id]
		open := false
		for _, p := range a.positions {
			open = open || !p.size.IsZero()
		}
		if !a.dirty && (dirtyOnly || !open) {
			continue
		}
		upnl := Summarize(a.balance, e.coinStates(a, nil)).UnrealizedPnl
		rows = append(rows, row{a, a.balance.Add(upnl), Score(a.priorPnl, a.balance, upnl, a.startingBalance)})
	}
	if len(rows) == 0 {
		return
	}
	err := pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
		for _, r := range rows {
			if _, err := tx.Exec(ctx, `
				INSERT INTO equity_snapshots (account_id, run_id, ts, equity, cumulative_pnl)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (account_id, ts) DO UPDATE SET run_id = $2, equity = $4, cumulative_pnl = $5`,
				r.a.id, r.a.runID, ts, r.equity.Round(usdDecimals), r.score.Round(usdDecimals)); err != nil {
				return fmt.Errorf("insert snapshot: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		slog.Error("engine: equity snapshots", "err", err)
		return
	}
	for _, r := range rows {
		r.a.dirty = false
	}
}

// SnapshotRetention is how long equity snapshots are kept: the PnL chart shows 30 days and
// the longest leaderboard window is 30 days.
const SnapshotRetention = 35 * 24 * time.Hour

// pruneSnapshots deletes snapshots past the retention period, except each account's
// latest one before the cutoff. That one is still the baseline of an account that has been
// flat (and so not snapshotted) since then.
func (e *Engine) pruneSnapshots(ctx context.Context) {
	cutoff := e.now().Add(-SnapshotRetention)
	tag, err := e.pool.Exec(ctx, `
		DELETE FROM equity_snapshots s
		WHERE s.ts < $1 AND s.ts < (
			SELECT max(k.ts) FROM equity_snapshots k WHERE k.account_id = s.account_id AND k.ts < $1)`, cutoff)
	if err != nil {
		slog.Error("engine: prune equity snapshots", "err", err)
		return
	}
	e.lastPrune = e.now()
	slog.Info("pruned equity snapshots", "deleted", tag.RowsAffected(), "older_than", cutoff)
}
