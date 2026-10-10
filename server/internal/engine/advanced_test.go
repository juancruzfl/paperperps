package engine

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/juancruzfl/paperperps/server/internal/accounts"
)

func TestTriggered(t *testing.T) {
	cases := []struct {
		typ          string
		buy          bool
		trigger      string
		fireAt, hold string
	}{
		{TypeStopMarket, false, "80000", "79999", "80001"}, // stop-loss on a long: price falls
		{TypeStopMarket, true, "85000", "85001", "84999"},  // stop-loss on a short: price rises
		{TypeTakeMarket, false, "85000", "85000", "84999"}, // take-profit on a long: price rises
		{TypeTakeLimit, true, "80000", "80000", "80001"},   // take-profit on a short: price falls
	}
	for _, c := range cases {
		if !Triggered(c.typ, c.buy, d(c.trigger), d(c.fireAt)) {
			t.Errorf("%s buy=%v trigger %s should fire at %s", c.typ, c.buy, c.trigger, c.fireAt)
		}
		if Triggered(c.typ, c.buy, d(c.trigger), d(c.hold)) {
			t.Errorf("%s buy=%v trigger %s should not fire at %s", c.typ, c.buy, c.trigger, c.hold)
		}
	}
}

// req builds a BTC order request.
func req(side, typ, sz string) OrderRequest {
	return OrderRequest{Coin: "BTC", Side: side, Type: typ, Sz: d(sz)}
}

func (r OrderRequest) px(p string) OrderRequest { r.Px = decimal.NewNullDecimal(d(p)); return r }
func (r OrderRequest) trigger(p string) OrderRequest {
	r.TriggerPx = decimal.NewNullDecimal(d(p))
	return r
}
func (r OrderRequest) tif(t string) OrderRequest { r.TIF = t; return r }
func (r OrderRequest) reduceOnly() OrderRequest  { r.ReduceOnly = true; return r }
func (r OrderRequest) tp(p string) OrderRequest {
	r.TakeProfit = decimal.NewNullDecimal(d(p))
	return r
}
func (r OrderRequest) sl(p string) OrderRequest { r.StopLoss = decimal.NewNullDecimal(d(p)); return r }

func (h *harness) submit(id uuid.UUID, r OrderRequest) OrderResult {
	h.t.Helper()
	res, err := h.eng.PlaceOrder(context.Background(), id, r)
	if err != nil {
		h.t.Fatalf("place %+v: %v", r, err)
	}
	return res
}

func (h *harness) submitErr(id uuid.UUID, r OrderRequest) error {
	_, err := h.eng.PlaceOrder(context.Background(), id, r)
	return err
}

func (h *harness) order(id, orderID uuid.UUID) accounts.Order {
	h.t.Helper()
	a := h.load(id)
	for _, o := range a.OpenOrders {
		if o.ID == orderID {
			return o
		}
	}
	hist, _ := h.store.OrderHistory(context.Background(), id, 50)
	for _, o := range hist {
		if o.ID == orderID {
			return o
		}
	}
	h.t.Fatalf("order %s not found", orderID)
	return accounts.Order{}
}

func TestStopLossMarketFires(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()
	h.submit(id, req(SideBuy, TypeMarket, "0.01")) // long at 82,000
	stop := h.submit(id, req(SideSell, TypeStopMarket, "0.01").trigger("81000").reduceOnly()).Order
	if stop.Status != StatusOpen || h.load(id).OpenOrders[0].ID != stop.ID {
		t.Fatalf("stop = %+v", stop)
	}

	h.md.setBook("BTC", "81500", "81510")
	h.runRisk()
	if h.order(id, stop.ID).Status != StatusOpen {
		t.Fatal("fired above its trigger")
	}

	h.md.setBook("BTC", "80990", "81000") // mark 80,995 ≤ 81,000
	h.runRisk()
	if o := h.order(id, stop.ID); o.Status != StatusFilled {
		t.Fatalf("stop after trigger = %+v", o)
	}
	fills, _ := h.store.Fills(context.Background(), id, 1)
	eq(t, "sold at the bid", fills[0].Px, "80990")
	eq(t, "loss", fills[0].ClosedPnl, "-10.1")
	eq(t, "position", positionOf(h.load(id), "BTC").Size, "0")
	h.checkLedger(id)
}

func TestTriggerValidation(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()
	h.submit(id, req(SideBuy, TypeMarket, "0.01"))
	wantReject(t, h.submitErr(id, req(SideSell, TypeStopMarket, "0.01").reduceOnly()), "invalid_trigger")
	// A stop sell above the mark would fire immediately.
	wantReject(t, h.submitErr(id, req(SideSell, TypeStopMarket, "0.01").trigger("83000").reduceOnly()), "invalid_trigger")
	wantReject(t, h.submitErr(id, req(SideSell, TypeStopLimit, "0.01").trigger("81000").reduceOnly()), "invalid_price")
	wantReject(t, h.submitErr(id, req(SideSell, TypeStopMarket, "0.01").trigger("81000").tif(TIFALO)), "invalid_tif")
}

func TestTakeProfitLimitRestsAfterTrigger(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()
	h.submit(id, req(SideBuy, TypeMarket, "0.01"))
	tp := h.submit(id, req(SideSell, TypeTakeLimit, "0.01").trigger("83000").px("83500").reduceOnly()).Order

	// Mark 83,005 fires it, but no bid reaches 83,500: it rests as a triggered limit.
	h.md.setBook("BTC", "83000", "83010")
	h.runRisk()
	if o := h.order(id, tp.ID); o.Status != StatusTriggered {
		t.Fatalf("after trigger = %+v", o)
	}

	h.md.setBook("BTC", "83600", "83610")
	h.eng.BookChanged("BTC")
	eventually(t, "resting take-profit fill", func() bool { return h.order(id, tp.ID).Status == StatusFilled })
	fills, _ := h.store.Fills(context.Background(), id, 1)
	eq(t, "filled at its limit", fills[0].Px, "83500")
	if fills[0].Liquidity != "maker" {
		t.Error("a resting fill is a maker fill")
	}
	eq(t, "position", positionOf(h.load(id), "BTC").Size, "0")
}

func TestReduceOnly(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()
	wantReject(t, h.submitErr(id, req(SideSell, TypeMarket, "0.01").reduceOnly()), "reduce_only")

	h.submit(id, req(SideBuy, TypeMarket, "0.01"))
	wantReject(t, h.submitErr(id, req(SideBuy, TypeMarket, "0.01").reduceOnly()), "reduce_only")

	// Bigger than the position: clamped, never flips to short.
	res := h.submit(id, req(SideSell, TypeMarket, "0.05").reduceOnly())
	eq(t, "filled", res.Fill.Sz, "0.01")
	if res.Order.StatusReason == nil {
		t.Error("clamped order should say why")
	}
	eq(t, "flat, not short", positionOf(h.load(id), "BTC").Size, "0")

	// Reduce-only can close dust below the $10 minimum.
	h.submit(id, req(SideBuy, TypeMarket, "0.0002")) // $16.40
	h.submit(id, req(SideSell, TypeMarket, "0.0001").reduceOnly())
	eq(t, "dust closed", positionOf(h.load(id), "BTC").Size, "0.0001")
	h.checkLedger(id)
}

func TestIOCAndPostOnly(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()
	h.md.setLevels("BTC", levels("81990", "1"), levels("82000", "0.004", "82100", "1"))

	res := h.submit(id, req(SideBuy, TypeLimit, "0.01").px("82050").tif(TIFIOC))
	eq(t, "IOC filled what it could", res.Fill.Sz, "0.004")
	if res.Order.Status != StatusFilled || len(h.load(id).OpenOrders) != 0 {
		t.Errorf("IOC remainder must not rest: %+v", res.Order)
	}
	wantReject(t, h.submitErr(id, req(SideBuy, TypeLimit, "0.01").px("81000").tif(TIFIOC)), "ioc_unfilled")

	wantReject(t, h.submitErr(id, req(SideBuy, TypeLimit, "0.01").px("82000").tif(TIFALO)), "would_cross")
	alo := h.submit(id, req(SideBuy, TypeLimit, "0.01").px("81000").tif(TIFALO))
	if alo.Order.Status != StatusOpen || alo.Fill != nil {
		t.Errorf("post-only away from the book should rest: %+v", alo)
	}
}

func TestAttachedTPSL(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()
	wantReject(t, h.submitErr(id, req(SideBuy, TypeMarket, "0.01").tp("80000")), "invalid_tpsl") // TP below a long's entry
	wantReject(t, h.submitErr(id, req(SideBuy, TypeMarket, "0.01").sl("83000")), "invalid_tpsl") // SL above it

	res := h.submit(id, req(SideBuy, TypeMarket, "0.01").tp("85000").sl("80000"))
	if len(res.Children) != 2 || h.load(id).OpenOrders[0].ParentOrderID == nil {
		t.Fatalf("children = %+v", res.Children)
	}
	tp, sl := res.Children[0], res.Children[1]
	if !tp.ReduceOnly || tp.OcoGroup == nil || *tp.OcoGroup != *sl.OcoGroup {
		t.Fatalf("tp/sl not linked: %+v / %+v", tp, sl)
	}

	h.md.setBook("BTC", "85000", "85010")
	h.runRisk()
	if h.order(id, tp.ID).Status != StatusFilled {
		t.Fatal("take-profit didn't fire")
	}
	if o := h.order(id, sl.ID); o.Status != StatusCancelled {
		t.Fatalf("stop-loss should be cancelled with its pair: %+v", o)
	}
	eq(t, "profit", h.ledger(id, "fill_pnl"), "30") // 0.01 × (85,000 - 82,000)
	eq(t, "flat", positionOf(h.load(id), "BTC").Size, "0")
	h.checkLedger(id)
}

func TestAttachedTPSLWaitsForParentFill(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()
	res := h.submit(id, req(SideBuy, TypeLimit, "0.01").px("81000").sl("79000"))
	sl := res.Children[0]

	// The price runs through both the entry and the stop before the engine sees the book:
	// the stop must not fire while its entry hasn't filled.
	h.md.setBook("BTC", "78990", "79000")
	h.runRisk()
	if h.order(id, sl.ID).Status != StatusOpen {
		t.Fatal("stop-loss fired before its entry filled")
	}

	h.eng.BookChanged("BTC") // the entry fills at 81,000
	eventually(t, "entry fill", func() bool { return h.order(id, res.Order.ID).Status == StatusFilled })
	h.runRisk() // now the stop is live and the mark is below it
	if h.order(id, sl.ID).Status != StatusFilled {
		t.Fatal("stop-loss didn't fire after its entry filled")
	}
	eq(t, "flat", positionOf(h.load(id), "BTC").Size, "0")
}

func TestCancellingUnfilledEntryCancelsItsTPSL(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()
	res := h.submit(id, req(SideBuy, TypeLimit, "0.01").px("81000").tp("90000").sl("79000"))
	if err := h.eng.CancelOrder(context.Background(), id, res.Order.ID); err != nil {
		t.Fatal(err)
	}
	if n := len(h.load(id).OpenOrders); n != 0 {
		t.Errorf("%d orders left open", n)
	}
}

func TestPositionTPSL(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id := h.newAccount()
	_, err := h.eng.SetPositionTPSL(ctx, id, "BTC", decimal.NewNullDecimal(d("90000")), decimal.NullDecimal{})
	wantReject(t, err, "no_position")

	h.submit(id, req(SideBuy, TypeMarket, "0.02"))
	set, err := h.eng.SetPositionTPSL(ctx, id, "BTC", decimal.NewNullDecimal(d("84000")), decimal.NewNullDecimal(d("80000")))
	if err != nil || len(set) != 2 {
		t.Fatalf("set = %v, %v", set, err)
	}
	eq(t, "sized to the position", set[0].Sz, "0.02")

	_, err = h.eng.SetPositionTPSL(ctx, id, "BTC", decimal.NullDecimal{}, decimal.NewNullDecimal(d("83000")))
	wantReject(t, err, "invalid_tpsl") // a stop above the mark would fire at once

	// Replacing keeps one live set.
	if _, err := h.eng.SetPositionTPSL(ctx, id, "BTC", decimal.NullDecimal{}, decimal.NewNullDecimal(d("81000"))); err != nil {
		t.Fatal(err)
	}
	if open := h.load(id).OpenOrders; len(open) != 1 || !open[0].TriggerPx.Decimal.Equal(d("81000")) {
		t.Fatalf("open after replace = %+v", open)
	}

	// Closing the position by hand cancels its now-pointless stop.
	h.submit(id, req(SideSell, TypeMarket, "0.02"))
	if n := len(h.load(id).OpenOrders); n != 0 {
		t.Errorf("%d reduce-only orders survived the close", n)
	}
}

func TestPositionTPSLReplacesAttachedOnes(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id := h.newAccount()
	h.submit(id, req(SideBuy, TypeMarket, "0.01").tp("85000").sl("80000"))
	if _, err := h.eng.SetPositionTPSL(ctx, id, "BTC", decimal.NullDecimal{}, decimal.NewNullDecimal(d("81000"))); err != nil {
		t.Fatal(err)
	}
	open := h.load(id).OpenOrders
	if len(open) != 1 || !open[0].TriggerPx.Decimal.Equal(d("81000")) {
		t.Fatalf("after replacing, open = %+v", open)
	}

	// TP/SL still waiting on an unfilled entry are not the position's, and stay.
	h.submit(id, req(SideBuy, TypeLimit, "0.01").px("80000").sl("78000"))
	if _, err := h.eng.SetPositionTPSL(ctx, id, "BTC", decimal.NullDecimal{}, decimal.NewNullDecimal(d("81500"))); err != nil {
		t.Fatal(err)
	}
	if n := len(h.load(id).OpenOrders); n != 3 { // entry limit, its pending SL, new position SL
		t.Errorf("open orders = %d, want 3", n)
	}
}

func TestModifyOrder(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id := h.newAccount()
	o := h.submit(id, req(SideBuy, TypeLimit, "0.01").px("80000")).Order

	m, err := h.eng.ModifyOrder(ctx, id, o.ID, ModifyRequest{Px: decimal.NewNullDecimal(d("80500")), Sz: decimal.NewNullDecimal(d("0.02"))})
	if err != nil || m.ID != o.ID {
		t.Fatalf("modify = %+v, %v", m, err)
	}
	got := h.order(id, o.ID)
	eq(t, "new price", got.Px.Decimal, "80500")
	eq(t, "new size", got.Sz, "0.02")
	eq(t, "margin follows", SummarizeAccount(h.load(id), h.md).OrderMargin, "80.5") // 1,610 / 20

	_, err = h.eng.ModifyOrder(ctx, id, o.ID, ModifyRequest{Px: decimal.NewNullDecimal(d("82500"))})
	wantReject(t, err, "would_cross")
	_, err = h.eng.ModifyOrder(ctx, id, o.ID, ModifyRequest{Sz: decimal.NewNullDecimal(d("1"))})
	wantReject(t, err, "insufficient_margin")
	_, err = h.eng.ModifyOrder(ctx, id, o.ID, ModifyRequest{})
	wantReject(t, err, "nothing_to_modify")
	_, err = h.eng.ModifyOrder(ctx, id, o.ID, ModifyRequest{TriggerPx: decimal.NewNullDecimal(d("80000"))})
	wantReject(t, err, "invalid_trigger")

	h.submit(id, req(SideBuy, TypeMarket, "0.01"))
	stop := h.submit(id, req(SideSell, TypeStopMarket, "0.01").trigger("80000").reduceOnly()).Order
	if _, err := h.eng.ModifyOrder(ctx, id, stop.ID, ModifyRequest{TriggerPx: decimal.NewNullDecimal(d("81000"))}); err != nil {
		t.Fatal(err)
	}
	eq(t, "new trigger", h.order(id, stop.ID).TriggerPx.Decimal, "81000")
	_, err = h.eng.ModifyOrder(ctx, id, stop.ID, ModifyRequest{TriggerPx: decimal.NewNullDecimal(d("82500"))})
	wantReject(t, err, "invalid_trigger")

	// The modified price applies to matching.
	h.md.setBook("BTC", "80000", "80400")
	h.eng.BookChanged("BTC")
	eventually(t, "modified order fills", func() bool { return h.order(id, o.ID).Status == StatusFilled })
}

func TestTriggerAndReduceOnlyOrdersReserveNoMargin(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()
	h.submit(id, req(SideBuy, TypeStopMarket, "0.1").trigger("90000")) // breakout entry, $9k
	if m := SummarizeAccount(h.load(id), h.md).OrderMargin; !m.IsZero() {
		t.Errorf("untriggered stop reserves %s", m)
	}
	h.submit(id, req(SideBuy, TypeMarket, "0.01"))
	h.submit(id, req(SideSell, TypeLimit, "0.01").px("90000").reduceOnly())
	if m := SummarizeAccount(h.load(id), h.md).OrderMargin; !m.IsZero() {
		t.Errorf("reduce-only limit reserves %s", m)
	}

	// When the breakout stop fires it needs margin like any order: $9k at 20x is $450, fine.
	h.md.setBook("BTC", "90000", "90010")
	h.runRisk()
	eq(t, "breakout filled", positionOf(h.load(id), "BTC").Size, "0.11")
}

func TestCloseOnlyMarketAcceptsOnlyReduceOnly(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()
	h.submit(id, req(SideBuy, TypeMarket, "0.01"))

	// BTC is dropped from the listed markets while the account still holds it.
	h.md.mu.Lock()
	tk := h.md.tickers["BTC"]
	tk.CloseOnly = true
	h.md.tickers["BTC"] = tk
	h.md.mu.Unlock()

	wantReject(t, h.submitErr(id, req(SideBuy, TypeMarket, "0.01")), "close_only")
	wantReject(t, h.submitErr(id, req(SideSell, TypeLimit, "0.01").px("90000")), "close_only")
	if _, err := h.eng.SetPositionTPSL(context.Background(), id, "BTC", decimal.NullDecimal{}, decimal.NewNullDecimal(d("80000"))); err != nil {
		t.Errorf("a stop-loss on a close-only position should be allowed: %v", err)
	}
	h.submit(id, req(SideSell, TypeMarket, "0.01").reduceOnly())
	eq(t, "closed", positionOf(h.load(id), "BTC").Size, "0")
}

func TestOrderCaps(t *testing.T) {
	h := newHarness(t)
	cfg := h.eng.cfg
	cfg.MaxOpenOrders, cfg.MaxTriggerOrders = 2, 2
	h.eng.cfg = cfg
	id := h.newAccount()

	h.submit(id, req(SideBuy, TypeLimit, "0.01").px("70000"))
	h.submit(id, req(SideBuy, TypeLimit, "0.01").px("71000"))
	wantReject(t, h.submitErr(id, req(SideBuy, TypeLimit, "0.01").px("72000")), "too_many_orders")
	// Orders that can't rest don't count against the cap.
	h.submit(id, req(SideBuy, TypeMarket, "0.01"))

	h.submit(id, req(SideBuy, TypeStopMarket, "0.01").trigger("90000"))
	wantReject(t, h.submitErr(id, req(SideBuy, TypeMarket, "0.01").tp("90000").sl("80000")), "too_many_orders")

	// Replacing position TP/SL frees what it replaces.
	ctx := context.Background()
	if _, err := h.eng.SetPositionTPSL(ctx, id, "BTC", decimal.NullDecimal{}, decimal.NewNullDecimal(d("80000"))); err != nil {
		t.Fatal(err)
	}
	if _, err := h.eng.SetPositionTPSL(ctx, id, "BTC", decimal.NullDecimal{}, decimal.NewNullDecimal(d("79000"))); err != nil {
		t.Errorf("replacing at the cap should work: %v", err)
	}
}
