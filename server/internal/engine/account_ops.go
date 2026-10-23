package engine

import (
	"context"
	"errors"
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
	return e.do(ctx, func(ctx context.Context) error { return e.resetAccount(ctx, accountID, nil) })
}

// AdminReset resets the account for a moderator, ignoring the cooldown and suspension, and
// records it in the audit log.
func (e *Engine) AdminReset(ctx context.Context, accountID uuid.UUID, reason string) error {
	return e.do(ctx, func(ctx context.Context) error { return e.resetAccount(ctx, accountID, &reason) })
}

// resetAccount resets the account. adminReason is set when a moderator does it.
func (e *Engine) resetAccount(ctx context.Context, accountID uuid.UUID, adminReason *string) error {
	a, err := e.account(ctx, accountID)
	if err != nil {
		return err
	}
	now := e.now()
	if adminReason == nil && a.suspended {
		return reject("account_suspended", "this account is suspended, so it can't be reset")
	}
	if adminReason == nil && a.lastResetAt != nil {
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
		if adminReason != nil {
			return recordAdmin(ctx, tx, "reset", a.id, *adminReason, map[string]any{"lockedUpnl": locked})
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

// --- deletion ---

// DeleteAccount permanently deletes the account and everything recorded about it: runs,
// positions, orders, fills, ledger, funding and equity snapshots (docs: Privacy Policy).
// It runs on the engine so the account's open orders leave memory in the same step.
func (e *Engine) DeleteAccount(ctx context.Context, accountID uuid.UUID) error {
	return e.do(ctx, func(ctx context.Context) error { return e.deleteAccount(ctx, accountID, nil) })
}

// AdminDelete deletes the account for a moderator and records it in the audit log. The
// log keeps the account's ID and handle, nothing else.
func (e *Engine) AdminDelete(ctx context.Context, accountID uuid.UUID, reason string) error {
	return e.do(ctx, func(ctx context.Context) error { return e.deleteAccount(ctx, accountID, &reason) })
}

func (e *Engine) deleteAccount(ctx context.Context, accountID uuid.UUID, adminReason *string) error {
	err := pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
		if adminReason != nil {
			if err := recordAdmin(ctx, tx, "delete", accountID, *adminReason, nil); err != nil {
				return err
			}
		}
		// Every table that refers to the account cascades from it.
		tag, err := tx.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, accountID)
		if err != nil {
			return fmt.Errorf("delete account: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return reject("not_found", "no such account")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if a, ok := e.accounts[accountID]; ok {
		for _, o := range a.orders {
			e.unindex(o)
		}
		delete(e.accounts, accountID)
	}
	slog.Info("account deleted", "account", accountID)
	return nil
}

// --- moderation ---

// SetSuspended suspends the account, or lifts a suspension, and records it in the audit
// log. A suspended account can't open positions: its open orders that could add to a
// position are cancelled, and it may only place reduce-only orders from then on, so it can
// still close what it holds. It also can't reset or change its handle.
func (e *Engine) SetSuspended(ctx context.Context, accountID uuid.UUID, suspended bool, reason string) error {
	return e.do(ctx, func(ctx context.Context) error { return e.setSuspended(ctx, accountID, suspended, reason) })
}

func (e *Engine) setSuspended(ctx context.Context, accountID uuid.UUID, suspended bool, reason string) error {
	a, err := e.account(ctx, accountID)
	if errors.Is(err, accounts.ErrNotFound) {
		return reject("not_found", "no such account")
	}
	if err != nil {
		return err
	}
	action := "unsuspend"
	if suspended {
		action = "suspend"
	}
	err = pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
		if suspended {
			if _, err := tx.Exec(ctx, `UPDATE accounts SET suspended_at = coalesce(suspended_at, $2), suspend_reason = $3
				WHERE id = $1`, a.id, e.now(), reason); err != nil {
				return fmt.Errorf("suspend: %w", err)
			}
		} else if _, err := tx.Exec(ctx, `UPDATE accounts SET suspended_at = NULL, suspend_reason = NULL WHERE id = $1`,
			a.id); err != nil {
			return fmt.Errorf("unsuspend: %w", err)
		}
		return recordAdmin(ctx, tx, action, a.id, reason, nil)
	})
	if err != nil {
		return err
	}
	a.suspended = suspended
	if suspended {
		var cancel []*accounts.Order
		for _, o := range a.orders {
			if !o.ReduceOnly {
				cancel = append(cancel, o)
			}
		}
		if len(cancel) > 0 {
			if err := e.closeOrders(ctx, a, cancel, "cancelled: account suspended"); err != nil {
				return fmt.Errorf("cancel orders of suspended account: %w", err)
			}
		}
	}
	e.publish(a.id, AccountEvent{Type: action})
	return nil
}

// recordAdmin writes an audit log entry for an admin action on an account, inside the
// transaction that makes the change.
func recordAdmin(ctx context.Context, tx pgx.Tx, action string, accountID uuid.UUID, reason string, detail map[string]any) error {
	var handle string
	if err := tx.QueryRow(ctx, `SELECT handle FROM accounts WHERE id = $1`, accountID).Scan(&handle); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return reject("not_found", "no such account")
		}
		return fmt.Errorf("read handle for audit: %w", err)
	}
	return accounts.RecordAction(ctx, tx, accounts.AdminAction{
		Action: action, AccountID: &accountID, Handle: &handle, Reason: reason, Detail: detail,
	})
}

// --- degen mode ---

// SetDegenMode turns degen mode on or off. On, leverage can go up to MaxDegenLeverage on
// every market. Off, leverage settings above a market's cap drop back to the cap; that's
// refused while such a coin has a position or orders that could add to one, which the
// trader has to close or move to a lower leverage first.
func (e *Engine) SetDegenMode(ctx context.Context, accountID uuid.UUID, on bool) error {
	return e.do(ctx, func(ctx context.Context) error { return e.setDegenMode(ctx, accountID, on) })
}

func (e *Engine) setDegenMode(ctx context.Context, accountID uuid.UUID, on bool) error {
	a, err := e.account(ctx, accountID)
	if err != nil {
		return err
	}
	if a.degenMode == on {
		return nil
	}
	lower := map[string]int{} // coin -> its cap, for flat positions set above it
	if !on {
		for coin, p := range a.positions {
			t, ok := e.md.Ticker(coin)
			if !ok || p.leverage <= t.MaxLeverage {
				continue
			}
			if !p.size.IsZero() || a.hasOpeningOrders(coin) {
				return reject("degen_positions_open",
					"your %s leverage is %dx, above its %dx max: close that position and its orders first", coin, p.leverage, t.MaxLeverage)
			}
			lower[coin] = t.MaxLeverage
		}
	}
	err = pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE accounts SET degen_mode = $2 WHERE id = $1`, a.id, on); err != nil {
			return fmt.Errorf("set degen mode: %w", err)
		}
		for coin, lev := range lower {
			p := *a.positions[coin]
			p.leverage = lev
			if err := upsertPosition(ctx, tx, a.id, a.runID, coin, &p, e.now()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	a.degenMode = on
	for coin, lev := range lower {
		a.positions[coin].leverage = lev
	}
	e.publish(a.id, AccountEvent{Type: "leverage"})
	return nil
}

// hasOpeningOrders reports whether the account has open orders on coin that could add to
// a position.
func (a *account) hasOpeningOrders(coin string) bool {
	for _, o := range a.orders {
		if o.Coin == coin && !o.ReduceOnly {
			return true
		}
	}
	return false
}
