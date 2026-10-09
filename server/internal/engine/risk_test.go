package engine

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Scripted price paths ("replays") with hand-calculated expectations. Taker fee 0.045%.

func (h *harness) mustOn(id uuid.UUID, coin, side, typ, sz, px string) OrderResult {
	h.t.Helper()
	res, err := h.placeOn(id, coin, side, typ, sz, px)
	if err != nil {
		h.t.Fatalf("place %s %s %s: %v", coin, side, sz, err)
	}
	return res
}

func (h *harness) ledger(id uuid.UUID, kind string) decimal.Decimal {
	h.t.Helper()
	var sum decimal.NullDecimal
	err := h.pool.QueryRow(context.Background(),
		`SELECT sum(amount) FROM ledger_entries WHERE account_id = $1 AND kind = $2`, id, kind).Scan(&sum)
	if err != nil {
		h.t.Fatal(err)
	}
	return sum.Decimal
}

func (h *harness) mustDo(err error) {
	h.t.Helper()
	if err != nil {
		h.t.Fatal(err)
	}
}

// openBigLong opens 0.4 BTC at 82,000 with 40x cross leverage.
//
//	notional 32,800 → initial margin 820, fee 14.76 → balance 985.24
//	maintenance rate 1/(2·40) = 1.25%; liquidation below mark ≈ 80,543.70
func openBigLong(h *harness) uuid.UUID {
	id := h.newAccount()
	h.mustDo(h.eng.SetLeverage(context.Background(), id, "BTC", 40))
	h.mustPlace(id, SideBuy, TypeMarket, "0.4", "")
	eq(h.t, "balance after open", h.load(id).Balance, "985.24")
	return id
}

func TestCrossLiquidationForfeit(t *testing.T) {
	h := newHarness(t)
	id := openBigLong(h)
	// A resting sell is cancelled by the liquidation.
	h.mustPlace(id, SideSell, TypeLimit, "0.1", "90000")

	// Mark 80,605: equity 985.24 - 558 = 427.24 > MM 403.03. Safe.
	h.md.setBook("BTC", "80600", "80610")
	h.runRisk()
	eq(t, "still open", positionOf(h.load(id), "BTC").Size, "0.4")

	// Mark 80,505: equity 985.24 - 598 = 387.24 < MM 402.53. Liquidated.
	h.md.setBook("BTC", "80500", "80510")
	h.runRisk()

	// Closed at the 80,500 bid: PnL -600, fee 14.49 → 370.75 left at stake, forfeited.
	a := h.load(id)
	eq(t, "position", positionOf(a, "BTC").Size, "0")
	eq(t, "fill pnl", h.ledger(id, "fill_pnl"), "-600")
	eq(t, "fees", h.ledger(id, "fee"), "-29.25") // 14.76 + 14.49
	eq(t, "liquidation fee", h.ledger(id, "liquidation_fee"), "-370.75")
	eq(t, "balance", a.Balance, "0")
	if len(a.OpenOrders) != 0 {
		t.Error("open order survived the liquidation")
	}
	fills, _ := h.store.Fills(context.Background(), id, 1)
	if !fills[0].IsLiquidation || fills[0].OrderID != nil {
		t.Errorf("liquidation fill = %+v", fills[0])
	}
	history, _ := h.store.OrderHistory(context.Background(), id, 5)
	if history[0].StatusReason == nil || *history[0].StatusReason != "cancelled: account liquidated" {
		t.Errorf("cancelled order = %+v", history[0])
	}
	h.checkLedger(id)
}

func TestCrossLiquidationMarketModeKeepsRemainder(t *testing.T) {
	h := newHarness(t)
	h.mode = LiquidationMarket
	h.start()
	id := openBigLong(h)
	h.md.setBook("BTC", "80500", "80510")
	h.runRisk()
	eq(t, "balance keeps the remainder", h.load(id).Balance, "370.75")
	eq(t, "no liquidation fee", h.ledger(id, "liquidation_fee"), "0")
	h.checkLedger(id)
}

// openIsolatedEthLong opens 4 ETH at 2,500, isolated at 25x.
//
//	notional 10,000 → isolated margin 400, fee 4.5
//	maintenance rate 1/(2·25) = 2%; liquidation below mark ≈ 2,448.98
func openIsolatedEthLong(h *harness, id uuid.UUID) {
	ctx := context.Background()
	h.mustDo(h.eng.SetMarginMode(ctx, id, "ETH", MarginIsolated))
	h.mustDo(h.eng.SetLeverage(ctx, id, "ETH", 25))
	h.mustOn(id, "ETH", SideBuy, TypeMarket, "4", "")
	p := positionOf(h.load(id), "ETH")
	if p.MarginMode != MarginIsolated {
		h.t.Fatalf("position = %+v", p)
	}
	eq(h.t, "isolated margin", p.IsolatedMargin, "400")
}

func TestIsolatedLiquidationLeavesCrossAlone(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()
	h.mustPlace(id, SideBuy, TypeMarket, "0.01", "") // cross BTC, fee 0.369
	openIsolatedEthLong(h, id)
	eq(t, "balance", h.load(id).Balance, "995.131") // 1000 - 0.369 - 4.5

	sum := SummarizeAccount(h.load(id), h.md)
	eq(t, "isolated margin", sum.IsolatedMargin, "400")
	if sum.Available.GreaterThan(d("595.131")) {
		t.Errorf("available %s should exclude the isolated margin", sum.Available)
	}

	// Mark 2,449.1: isolated equity 196.4 > MM 195.93. Safe.
	h.md.setBook("ETH", "2449", "2449.2")
	h.runRisk()
	eq(t, "ETH still open", positionOf(h.load(id), "ETH").Size, "4")

	// Mark 2,448.1: equity 192.4 < MM 195.85. Closed at 2,448: PnL -208, fee 4.4064,
	// 187.5936 of the isolated margin left → forfeited. The account loses exactly the
	// 400 of isolated margin (plus fees already paid).
	h.md.setBook("ETH", "2448", "2448.2")
	h.runRisk()
	a := h.load(id)
	eq(t, "ETH closed", positionOf(a, "ETH").Size, "0")
	eq(t, "ETH margin released", positionOf(a, "ETH").IsolatedMargin, "0")
	eq(t, "BTC untouched", positionOf(a, "BTC").Size, "0.01")
	eq(t, "liquidation fee", h.ledger(id, "liquidation_fee"), "-187.5936")
	eq(t, "balance", a.Balance, "595.131") // 995.131 - 400
	h.checkLedger(id)
}

func TestGapLiquidationAbsorbsBadDebt(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()
	openIsolatedEthLong(h, id)

	// Price gaps far through the liquidation price. Closed at 2,300: PnL -800, fee 4.14,
	// so the position lost 404.14 more than its 400 margin. That part is absorbed.
	h.md.setBook("ETH", "2300", "2300.2")
	h.runRisk()
	eq(t, "bad debt", h.ledger(id, "bad_debt"), "404.14")
	eq(t, "no liquidation fee", h.ledger(id, "liquidation_fee"), "0")
	eq(t, "balance", h.load(id).Balance, "595.5") // 1000 - 4.5 - 400
	h.checkLedger(id)
}

func TestCrossBadDebtNeverGoesNegative(t *testing.T) {
	h := newHarness(t)
	id := openBigLong(h)
	// Gap to 78,000: PnL -1,600, fee 14.04 → cross equity -628.80, absorbed.
	h.md.setBook("BTC", "78000", "78010")
	h.runRisk()
	eq(t, "bad debt", h.ledger(id, "bad_debt"), "628.8")
	eq(t, "balance", h.load(id).Balance, "0")
	h.checkLedger(id)
}

func TestThinBookClosesRemainderAtMark(t *testing.T) {
	h := newHarness(t)
	id := openBigLong(h)
	// Only 0.1 BTC bid at 80,400; the other 0.3 closes at the mark (80,450).
	h.md.setLevels("BTC", levels("80400", "0.1"), levels("80500", "1"))
	h.runRisk()
	fills, _ := h.store.Fills(context.Background(), id, 1)
	eq(t, "average close", fills[0].Px, "80437.5") // (0.1·80,400 + 0.3·80,450) / 0.4
	eq(t, "position", positionOf(h.load(id), "BTC").Size, "0")
	h.checkLedger(id)
}

func TestStaleMarketIsNotLiquidated(t *testing.T) {
	h := newHarness(t)
	id := openBigLong(h)
	h.md.setBook("BTC", "80500", "80510")
	h.md.setStale("BTC", true)
	h.runRisk()
	eq(t, "kept while stale", positionOf(h.load(id), "BTC").Size, "0.4")

	h.md.setStale("BTC", false)
	h.runRisk()
	eq(t, "liquidated once fresh", positionOf(h.load(id), "BTC").Size, "0")
}

func TestHourlyFunding(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.mustDo(func() error { _, err := h.pool.Exec(ctx, `DELETE FROM engine_state`); return err }())
	h.setClock(time.Date(2026, 10, 9, 10, 30, 0, 0, time.UTC))
	h.start() // fresh state: the first funding hour is 11:00

	id := h.newAccount()
	h.mustPlace(id, SideBuy, TypeMarket, "0.01", "") // long 0.01 BTC, cross
	h.mustDo(h.eng.SetMarginMode(ctx, id, "ETH", MarginIsolated))
	h.mustOn(id, "ETH", SideSell, TypeMarket, "1", "") // short 1 ETH at 2,499.9, isolated 20x
	before := h.load(id)
	eq(t, "ETH isolated margin", positionOf(before, "ETH").IsolatedMargin, "124.995")

	// Long BTC pays 0.01·82,100·0.01% = 0.0821. Short ETH with a negative rate pays
	// 1·2,510·0.02% = 0.502.
	h.md.setFunding("BTC", "0.0001", "82100")
	h.md.setFunding("ETH", "-0.0002", "2510")
	h.runRisk()
	eq(t, "nothing before the hour", h.ledger(id, "funding"), "0")

	h.setClock(time.Date(2026, 10, 9, 11, 0, 3, 0, time.UTC))
	h.runRisk()
	h.runRisk() // same hour again: no double charge
	eq(t, "funding paid", h.ledger(id, "funding"), "-0.5841")
	a := h.load(id)
	eq(t, "balance", a.Balance, before.Balance.Sub(d("0.5841")).String())
	eq(t, "isolated margin pays its own funding", positionOf(a, "ETH").IsolatedMargin, "124.493")
	payments, _ := h.store.Funding(ctx, id, 10)
	if len(payments) != 2 {
		t.Fatalf("payments = %+v", payments)
	}

	// A restart within the same hour doesn't pay again.
	h.start()
	h.runRisk()
	eq(t, "after restart", h.ledger(id, "funding"), "-0.5841")

	// Stale markets are skipped; the next hour pays BTC only.
	h.md.setStale("ETH", true)
	h.setClock(time.Date(2026, 10, 9, 12, 0, 1, 0, time.UTC))
	h.runRisk()
	eq(t, "next hour", h.ledger(id, "funding"), "-0.6662")
	h.checkLedger(id)
}

func TestFundingAmountSigns(t *testing.T) {
	eq(t, "long pays positive rate", FundingAmount(d("1"), d("100"), d("0.001")), "-0.1")
	eq(t, "short receives positive rate", FundingAmount(d("-1"), d("100"), d("0.001")), "0.1")
	eq(t, "long receives negative rate", FundingAmount(d("2"), d("100"), d("-0.001")), "0.2")
}

func TestMarginModeRules(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id := h.newAccount()

	wantReject(t, h.eng.SetMarginMode(ctx, id, "BTC", "portfolio"), "invalid_margin_mode")
	h.mustPlace(id, SideBuy, TypeLimit, "0.01", "70000")
	wantReject(t, h.eng.SetMarginMode(ctx, id, "BTC", MarginIsolated), "orders_open")
	h.mustDo(h.eng.CancelOrder(ctx, id, h.load(id).OpenOrders[0].ID))
	h.mustPlace(id, SideBuy, TypeMarket, "0.01", "")
	wantReject(t, h.eng.SetMarginMode(ctx, id, "BTC", MarginIsolated), "position_open")
	wantReject(t, h.eng.AdjustIsolatedMargin(ctx, id, "BTC", d("10")), "not_isolated")

	openIsolatedEthLong(h, id)
	wantReject(t, h.eng.SetLeverage(ctx, id, "ETH", 10), "isolated_position_open")

	h.mustDo(h.eng.AdjustIsolatedMargin(ctx, id, "ETH", d("50")))
	eq(t, "after add", positionOf(h.load(id), "ETH").IsolatedMargin, "450")
	wantReject(t, h.eng.AdjustIsolatedMargin(ctx, id, "ETH", d("100000")), "insufficient_margin")
	// Initial margin at mark 2,499.95 is 399.992; removing 60 would leave less.
	wantReject(t, h.eng.AdjustIsolatedMargin(ctx, id, "ETH", d("-60")), "insufficient_margin")
	h.mustDo(h.eng.AdjustIsolatedMargin(ctx, id, "ETH", d("-49.5")))
	eq(t, "after remove", positionOf(h.load(id), "ETH").IsolatedMargin, "400.5")
	wantReject(t, h.eng.AdjustIsolatedMargin(ctx, id, "ETH", d("0.0000001")), "invalid_amount")

	// Closing returns the margin to cross.
	h.mustOn(id, "ETH", SideSell, TypeMarket, "4", "")
	eq(t, "closed margin", positionOf(h.load(id), "ETH").IsolatedMargin, "0")
	h.checkLedger(id)
}
