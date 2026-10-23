package engine

import (
	"context"
	"testing"
)

func TestDegenMode(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()
	ctx := context.Background()

	// BTC's real cap is 40x.
	wantReject(t, h.eng.SetLeverage(ctx, id, "BTC", 41), "invalid_leverage")

	h.mustDo(h.eng.SetDegenMode(ctx, id, true))
	wantReject(t, h.eng.SetLeverage(ctx, id, "BTC", MaxDegenLeverage+1), "invalid_leverage")
	h.mustDo(h.eng.SetLeverage(ctx, id, "BTC", MaxDegenLeverage))
	if a := h.load(id); !a.DegenMode || a.DegenAt != nil {
		t.Fatalf("after turning on: degenMode %v, degenAt %v; want true, nil (no trade yet)", a.DegenMode, a.DegenAt)
	}

	// 0.01 BTC at 82,000: margin 0.82. Maintenance follows the position's own 1000x,
	// 1/2000 of notional at the 81,995 mark, so it doesn't open already liquidatable.
	h.mustPlace(id, SideBuy, TypeMarket, "0.01", "")
	a := h.load(id)
	if a.DegenAt == nil {
		t.Fatal("degenAt not set by a fill above the cap")
	}
	eq(t, "cross maintenance", SummarizeAccount(a, h.md).CrossMaintenance, "0.409975")

	// Turning it off waits until nothing is open above the cap.
	wantReject(t, h.eng.SetDegenMode(ctx, id, false), "degen_positions_open")
	h.mustPlace(id, SideSell, TypeMarket, "0.01", "")
	h.mustDo(h.eng.SetDegenMode(ctx, id, false))
	a = h.load(id)
	if a.DegenMode || a.DegenAt == nil {
		t.Fatalf("after turning off: degenMode %v, degenAt %v; want false, still set", a.DegenMode, a.DegenAt)
	}
	if lev := positionOf(a, "BTC").Leverage; lev != 40 {
		t.Fatalf("BTC leverage after turning off = %d, want the 40x cap", lev)
	}
	wantReject(t, h.eng.SetLeverage(ctx, id, "BTC", 41), "invalid_leverage")

	// The setting outlives a restart.
	h.mustDo(h.eng.SetDegenMode(ctx, id, true))
	h.stop()
	h.start()
	h.mustDo(h.eng.SetLeverage(ctx, id, "BTC", 500))
}
