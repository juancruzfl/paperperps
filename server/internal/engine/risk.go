package engine

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/juancruzfl/paperperps/server/internal/accounts"
)

// accountIDs returns the loaded accounts in a stable order, so risk and funding runs are
// deterministic.
func (e *Engine) accountIDs() []uuid.UUID {
	return slices.SortedFunc(maps.Keys(e.accounts), compareIDs)
}

func compareIDs(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) }

// riskTick runs the once-a-second checks, in order: fire trigger orders (a stop-loss
// should get its chance before a liquidation), liquidate, then settle funding.
func (e *Engine) riskTick(ctx context.Context) {
	e.checkTriggers(ctx)
	e.checkRisk(ctx)
	e.settleFunding(ctx)
}

// --- liquidation ---

// checkRisk liquidates positions that fell below maintenance margin. Markets whose data is
// stale are skipped: liquidating on a frozen price would be wrong (docs/plan.md §3.1).
func (e *Engine) checkRisk(ctx context.Context) {
	for _, id := range e.accountIDs() {
		if err := e.checkAccount(ctx, e.accounts[id]); err != nil {
			slog.Error("engine: risk check", "account", id, "err", err)
		}
	}
}

func (e *Engine) fresh(coin string) bool {
	t, ok := e.md.Ticker(coin)
	return ok && !t.Stale
}

func (e *Engine) checkAccount(ctx context.Context, a *account) error {
	// Isolated positions stand alone, so they're checked (and liquidated) one by one first.
	for _, c := range e.coinStates(a, nil) {
		if c.IsolatedLiquidatable() && e.fresh(c.Coin) {
			if err := e.liquidate(ctx, a, []string{c.Coin}, false); err != nil {
				return err
			}
		}
	}

	var cross []string
	for coin, p := range a.positions {
		if !p.isolated && !p.size.IsZero() {
			if !e.fresh(coin) {
				return nil // one stale price makes the whole cross account's value unknown
			}
			cross = append(cross, coin)
		}
	}
	if len(cross) == 0 || !Summarize(a.balance, e.coinStates(a, nil)).CrossLiquidatable() {
		return nil
	}
	slices.Sort(cross)
	return e.liquidate(ctx, a, cross, true)
}

// liquidate closes the positions on coins at market. Cross liquidation closes every cross
// position; isolated liquidation closes one isolated position.
//
// What the user keeps afterwards depends on LiquidationMode (docs/plan.md §4.6):
//   - forfeit: the margin left at stake is taken as a liquidation fee, like HL's backstop.
//     Isolated: the position's margin. Cross: the cross equity. Isolated positions and
//     their margin are never touched by a cross liquidation.
//   - market: the remainder stays with the user, like HL's market liquidation.
//
// Either way, a loss beyond the margin at stake (prices gapped through the liquidation
// price) is absorbed as bad debt, so an account never goes below zero.
func (e *Engine) liquidate(ctx context.Context, a *account, coins []string, cross bool) error {
	now := e.now()
	recs := make([]fillRecord, 0, len(coins))
	delta := zero
	for _, coin := range coins {
		p := a.positions[coin]
		ticker, _ := e.md.Ticker(coin)
		book, _ := e.md.Book(coin)
		buy := p.size.IsNegative() // closing a short buys
		sz := p.size.Abs()
		levels := book.Bids
		if buy {
			levels = book.Asks
		}
		filled, notional := WalkBook(levels, buy, sz, zero)
		// If the book is too thin, the rest closes at the mark price.
		notional = notional.Add(sz.Sub(filled).Mul(ticker.MarkPx))
		fee := notional.Mul(e.cfg.TakerFeeRate).Round(usdDecimals)
		r := e.newFill(p, nil, coin, buy, sz, notional.Div(sz), fee, "taker", now)
		r.fill.IsLiquidation = true
		recs = append(recs, r)
		delta = delta.Add(r.balanceDelta)
	}

	// The margin still at stake once the positions are closed.
	var atStake decimal.Decimal
	if cross {
		isolated := zero
		for _, p := range a.positions {
			if p.isolated {
				isolated = isolated.Add(p.isoMargin)
			}
		}
		atStake = a.balance.Add(delta).Sub(isolated)
	} else {
		atStake = a.positions[coins[0]].isoMargin.Add(delta)
	}
	liqFee, badDebt := zero, zero
	switch {
	case atStake.IsNegative():
		badDebt = atStake.Neg()
	case e.cfg.LiquidationMode == LiquidationForfeit:
		liqFee = atStake
	}

	var cancel []*accounts.Order
	for _, o := range a.orders {
		if cross || o.Coin == coins[0] {
			cancel = append(cancel, o)
		}
	}
	cancelled := make([]accounts.Order, len(cancel))
	for i, o := range cancel {
		cancelled[i] = *o
		cancelled[i].Status = StatusCancelled
		cancelled[i].StatusReason = ptr("cancelled: account liquidated")
		cancelled[i].UpdatedAt = now
	}

	ref := recs[0].fill.ID
	err := pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
		for _, r := range recs {
			if err := recordFill(ctx, tx, a.id, a.runID, r); err != nil {
				return err
			}
		}
		if err := addLedger(ctx, tx, a.id, a.runID, "liquidation_fee", liqFee.Neg(), ref, now); err != nil {
			return err
		}
		if err := addLedger(ctx, tx, a.id, a.runID, "bad_debt", badDebt, ref, now); err != nil {
			return err
		}
		if err := addBalance(ctx, tx, a.runID, badDebt.Sub(liqFee)); err != nil {
			return err
		}
		for _, o := range cancelled {
			if err := updateOrder(ctx, tx, o); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("liquidate %v: %w", coins, err)
	}

	info := &LiquidationInfo{Coins: coins, Cross: cross, LiquidationFee: liqFee, BadDebt: badDebt}
	for _, r := range recs {
		e.applyFill(a, r)
		info.Fills = append(info.Fills, r.fill)
	}
	a.balance = a.balance.Add(badDebt).Sub(liqFee)
	a.dirty = true
	for _, o := range cancel {
		delete(a.orders, o.ID)
		e.unindex(o)
	}
	slog.Info("account liquidated", "account", a.id, "coins", coins, "cross", cross,
		"liquidation_fee", liqFee, "bad_debt", badDebt, "balance", a.balance)
	e.publish(a.id, AccountEvent{Type: "liquidation", Liquidation: info})
	return nil
}

// --- funding ---

// settleFunding pays funding once per hour, at the first risk tick after the hour starts.
// Payments are keyed by (account, coin, hour), so retrying after an error or a restart
// never charges twice. Missed hours (the engine was down) are not back-filled.
func (e *Engine) settleFunding(ctx context.Context) {
	hour := e.now().Truncate(time.Hour)
	if !hour.After(e.lastFunding) {
		return
	}
	failed := false
	for _, id := range e.accountIDs() {
		if err := e.fundAccount(ctx, e.accounts[id], hour); err != nil {
			slog.Error("engine: funding", "account", id, "hour", hour, "err", err)
			failed = true
		}
	}
	if failed {
		return // retried next tick; accounts already paid are skipped by the unique key
	}
	if err := pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error { return saveLastFunding(ctx, tx, hour) }); err != nil {
		slog.Error("engine: save funding hour", "err", err)
		return
	}
	e.lastFunding = hour
}

type fundingPayment struct {
	coin     string
	size     decimal.Decimal
	rate     decimal.Decimal
	oraclePx decimal.Decimal
	amount   decimal.Decimal // positive: the account receives
}

// FundingAmount is what a position receives (negative: pays) for one funding interval.
// With a positive rate longs pay shorts.
func FundingAmount(size, oraclePx, rate decimal.Decimal) decimal.Decimal {
	return size.Mul(oraclePx).Mul(rate).Neg().Round(usdDecimals)
}

func (e *Engine) fundAccount(ctx context.Context, a *account, hour time.Time) error {
	var due []fundingPayment
	for _, coin := range slices.Sorted(maps.Keys(a.positions)) {
		p := a.positions[coin]
		if p.size.IsZero() {
			continue
		}
		t, ok := e.md.Ticker(coin)
		if !ok || t.Stale {
			continue // no trustworthy rate this hour
		}
		amount := FundingAmount(p.size, t.OraclePx, t.Funding)
		if amount.IsZero() {
			continue
		}
		due = append(due, fundingPayment{coin: coin, size: p.size, rate: t.Funding, oraclePx: t.OraclePx, amount: amount})
	}
	if len(due) == 0 {
		return nil
	}

	var paid []fundingPayment
	err := pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
		paid = paid[:0]
		total := zero
		for _, f := range due {
			id := accounts.NewID()
			tag, err := tx.Exec(ctx, `
				INSERT INTO funding_payments (id, account_id, run_id, coin, funding_hour, size, rate, oracle_px, amount)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
				ON CONFLICT (account_id, coin, funding_hour) DO NOTHING`,
				id, a.id, a.runID, f.coin, hour, f.size, f.rate, f.oraclePx, f.amount)
			if err != nil {
				return fmt.Errorf("insert funding payment: %w", err)
			}
			if tag.RowsAffected() == 0 {
				continue // already paid this hour
			}
			if err := addLedger(ctx, tx, a.id, a.runID, "funding", f.amount, id, hour); err != nil {
				return err
			}
			if p := a.positions[f.coin]; p.isolated {
				// Isolated funding comes out of (or goes into) the position's own margin.
				if _, err := tx.Exec(ctx, `UPDATE positions SET isolated_margin = isolated_margin + $3
					WHERE account_id = $1 AND coin = $2`, a.id, f.coin, f.amount); err != nil {
					return fmt.Errorf("update isolated margin: %w", err)
				}
			}
			total = total.Add(f.amount)
			paid = append(paid, f)
		}
		return addBalance(ctx, tx, a.runID, total)
	})
	if err != nil {
		return err
	}
	for _, f := range paid {
		a.dirty = true
		a.balance = a.balance.Add(f.amount)
		if p := a.positions[f.coin]; p.isolated {
			p.isoMargin = p.isoMargin.Add(f.amount)
		}
	}
	if len(paid) > 0 {
		e.publish(a.id, AccountEvent{Type: "funding"})
	}
	return nil
}

// --- margin mode and isolated margin ---

// SetMarginMode switches coin between cross and isolated. Like Hyperliquid, only allowed
// with no position and no open orders on the coin.
func (e *Engine) SetMarginMode(ctx context.Context, accountID uuid.UUID, coin, mode string) error {
	return e.do(ctx, func(ctx context.Context) error { return e.setMarginMode(ctx, accountID, coin, mode) })
}

func (e *Engine) setMarginMode(ctx context.Context, accountID uuid.UUID, coin, mode string) error {
	if mode != MarginCross && mode != MarginIsolated {
		return reject("invalid_margin_mode", "margin mode must be cross or isolated")
	}
	ticker, ok := e.md.Ticker(coin)
	if !ok {
		return reject("unknown_market", "%s is not a listed market", coin)
	}
	a, err := e.account(ctx, accountID)
	if err != nil {
		return err
	}
	p := a.position(coin, e.defaultLeverage(ticker))
	if p.mode() == mode {
		return nil
	}
	if !p.size.IsZero() {
		return reject("position_open", "close your %s position before switching margin mode", coin)
	}
	for _, o := range a.orders {
		if o.Coin == coin {
			return reject("orders_open", "cancel your open %s orders before switching margin mode", coin)
		}
	}
	next := *p
	next.isolated = mode == MarginIsolated
	next.isoMargin = zero
	if err := pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
		return upsertPosition(ctx, tx, a.id, a.runID, coin, &next, e.now())
	}); err != nil {
		return err
	}
	*p = next
	e.publish(a.id, AccountEvent{Type: "margin"})
	return nil
}

// AdjustIsolatedMargin moves amount of margin into (positive) or out of (negative) an
// open isolated position.
func (e *Engine) AdjustIsolatedMargin(ctx context.Context, accountID uuid.UUID, coin string, amount decimal.Decimal) error {
	return e.do(ctx, func(ctx context.Context) error { return e.adjustIsolatedMargin(ctx, accountID, coin, amount) })
}

func (e *Engine) adjustIsolatedMargin(ctx context.Context, accountID uuid.UUID, coin string, amount decimal.Decimal) error {
	if amount.IsZero() || decimalPlaces(amount) > usdDecimals {
		return reject("invalid_amount", "amount must be non-zero with at most %d decimals", usdDecimals)
	}
	a, err := e.account(ctx, accountID)
	if err != nil {
		return err
	}
	p, ok := a.positions[coin]
	if !ok || !p.isolated || p.size.IsZero() {
		return reject("not_isolated", "you have no open isolated %s position", coin)
	}

	next := *p
	next.isoMargin = p.isoMargin.Add(amount)
	if amount.IsPositive() {
		if Summarize(a.balance, e.coinStates(a, nil)).Available.LessThan(amount) {
			return reject("insufficient_margin", "not enough available margin to add")
		}
	} else {
		// Removing margin may not leave less than the position's initial margin.
		var c CoinState
		for _, s := range e.coinStates(a, nil) {
			if s.Coin == coin {
				c = s
			}
		}
		c.IsolatedMargin = next.isoMargin
		if !next.isoMargin.IsPositive() || c.IsolatedEquity().LessThan(c.PositionMargin()) {
			return reject("insufficient_margin", "removing that much would leave less than the position's initial margin")
		}
	}
	if err := pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
		return upsertPosition(ctx, tx, a.id, a.runID, coin, &next, e.now())
	}); err != nil {
		return err
	}
	*p = next
	e.publish(a.id, AccountEvent{Type: "margin"})
	return nil
}
