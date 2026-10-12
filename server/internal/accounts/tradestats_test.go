package accounts

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestRoundTripsAndSummary(t *testing.T) {
	run, nextRun := uuid.New(), uuid.New()
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	d := decimal.RequireFromString
	fill := func(run uuid.UUID, mins int, coin, side, sz, fee, pnl string) StatFill {
		return StatFill{run, coin, side, d(sz), d(fee), d(pnl), t0.Add(time.Duration(mins) * time.Minute)}
	}
	fills := []StatFill{
		// ETH long in two buys, closed in two sells: +50 - 4 fees = +46, held 60 min.
		fill(run, 0, "ETH", "buy", "1", "1", "0"),
		fill(run, 10, "ETH", "buy", "1", "1", "0"),
		fill(run, 30, "ETH", "sell", "1", "1", "30"),
		fill(run, 60, "ETH", "sell", "1", "1", "20"),
		// BTC short, flipped long by one fill of 3 (fee 3: 1 to the close, 2 to the new long).
		fill(run, 0, "BTC", "sell", "1", "1", "0"),
		fill(run, 20, "BTC", "buy", "3", "3", "-10"), // short: -10 - 1 - 1 = -12, held 20 min
		fill(run, 50, "BTC", "sell", "2", "2", "-5"), // long: -2 - 5 - 2 = -9, held 30 min
		// SOL left open in a run that was reset: not counted.
		fill(run, 70, "SOL", "buy", "5", "1", "0"),
		fill(nextRun, 80, "SOL", "sell", "5", "1", "0"), // a fresh short in the new run, still open
	}

	trips := RoundTrips(fills)
	want := []struct {
		coin string
		long bool
		pnl  string
		held time.Duration
	}{
		{"BTC", false, "-12", 20 * time.Minute},
		{"BTC", true, "-9", 30 * time.Minute},
		{"ETH", true, "46", 60 * time.Minute},
	}
	if len(trips) != len(want) {
		t.Fatalf("got %d round trips, want %d: %+v", len(trips), len(want), trips)
	}
	byKey := map[string]RoundTrip{}
	for _, tr := range trips {
		byKey[tr.Coin+map[bool]string{true: "L", false: "S"}[tr.Long]] = tr
	}
	for _, w := range want {
		tr := byKey[w.coin+map[bool]string{true: "L", false: "S"}[w.long]]
		if !tr.Pnl.Equal(d(w.pnl)) || tr.Closed.Sub(tr.Opened) != w.held {
			t.Errorf("%s long=%v: pnl %s held %s, want %s %s", w.coin, w.long, tr.Pnl, tr.Closed.Sub(tr.Opened), w.pnl, w.held)
		}
	}

	p := Summarize(trips)
	if p.RoundTrips != 3 || p.Wins != 1 || p.Losses != 2 || p.LongestLosingStreak != 2 {
		t.Errorf("counts = %+v", p)
	}
	if !p.AvgWin.Equal(d("46")) || !p.AvgLoss.Equal(d("-10.5")) || p.ProfitFactor == nil || !p.ProfitFactor.Equal(d("2.19")) {
		t.Errorf("averages: win %s loss %s pf %v", p.AvgWin, p.AvgLoss, p.ProfitFactor)
	}
	if p.AvgHoldSeconds != 36*60+40 {
		t.Errorf("avg hold = %ds, want 2200", p.AvgHoldSeconds)
	}
	if p.Long.Trips != 2 || !p.Long.Pnl.Equal(d("37")) || p.Short.Trips != 1 || !p.Short.Pnl.Equal(d("-12")) {
		t.Errorf("sides: long %+v short %+v", p.Long, p.Short)
	}
	if len(p.ByCoin) != 2 || p.ByCoin[0].Coin != "BTC" || p.ByCoin[0].Trips != 2 || !p.ByCoin[1].Pnl.Equal(d("46")) {
		t.Errorf("by coin = %+v", p.ByCoin)
	}
}

func TestSummarizeNothing(t *testing.T) {
	p := Summarize(nil)
	if p.RoundTrips != 0 || p.ProfitFactor != nil || p.ByCoin == nil {
		t.Errorf("empty summary = %+v", p)
	}
}
