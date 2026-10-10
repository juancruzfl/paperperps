package engine

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestResetLocksPnLAndHasCooldown(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	start := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	h.setClock(start)
	h.start()
	cfg := h.eng.cfg
	cfg.ResetCooldown = 24 * time.Hour
	h.eng.cfg = cfg

	id := h.newAccount()
	h.submit(id, req(SideBuy, TypeMarket, "0.01")) // fee 0.369 → balance 999.631
	h.submit(id, req(SideBuy, TypeLimit, "0.01").px("70000"))

	// Mark 80,995: the open long is down 10.05.
	h.md.setBook("BTC", "80990", "81000")
	h.md.setStale("BTC", true)
	wantReject(t, h.eng.ResetAccount(ctx, id), "market_stale")
	h.md.setStale("BTC", false)
	if err := h.eng.ResetAccount(ctx, id); err != nil {
		t.Fatal(err)
	}

	a := h.load(id)
	eq(t, "fresh balance", a.Balance, "1000")
	if len(a.Positions) != 0 || len(a.OpenOrders) != 0 || a.Resets != 1 || a.LastResetAt == nil {
		t.Fatalf("after reset = %+v", a)
	}
	// The ended run lost the fee and the locked 10.05: 999.631 - 10.05 - 1000.
	eq(t, "prior pnl", a.PriorPnl, "-10.419")
	eq(t, "lifetime score", Score(a.PriorPnl, a.Balance, zero, a.StartingBalance), "-10.419")
	h.checkLedger(id) // the new run's ledger is just its deposit

	var locked decimal.Decimal
	if err := h.pool.QueryRow(ctx, `SELECT locked_upnl FROM runs WHERE account_id = $1 AND ended_at IS NOT NULL`, id).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	eq(t, "locked upnl", locked, "-10.05")

	wantReject(t, h.eng.ResetAccount(ctx, id), "reset_cooldown")
	h.setClock(start.Add(24*time.Hour + time.Second))
	if err := h.eng.ResetAccount(ctx, id); err != nil {
		t.Fatalf("reset after the cooldown: %v", err)
	}
	if a := h.load(id); a.Resets != 2 {
		t.Errorf("resets = %d", a.Resets)
	}
	eq(t, "score survives a second reset", h.load(id).PriorPnl, "-10.419")
}

func TestEquitySnapshots(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.setClock(time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))
	h.start()
	id := h.newAccount()
	h.submit(id, req(SideBuy, TypeMarket, "0.01"))

	snap := func() {
		if err := h.eng.do(ctx, func(ctx context.Context) error { h.eng.snapshot(ctx, false); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	count := func() int {
		var n int
		h.pool.QueryRow(ctx, `SELECT count(*) FROM equity_snapshots WHERE account_id = $1`, id).Scan(&n)
		return n
	}

	h.md.setBook("BTC", "82990", "83010") // mark 83,000: +10
	snap()
	points, err := h.store.Equity(ctx, id, time.Date(2026, 10, 9, 12, 1, 0, 0, time.UTC))
	if err != nil || len(points) != 1 {
		t.Fatalf("points = %v, %v", points, err)
	}
	eq(t, "equity", points[0].Equity, "1009.631")             // 999.631 + 10
	eq(t, "cumulative pnl", points[0].CumulativePnl, "9.631") // +10 - 0.369 fee

	// Open positions are snapshotted every interval; flat, unchanged accounts aren't.
	h.setClock(time.Date(2026, 10, 9, 12, 5, 0, 0, time.UTC))
	snap()
	h.submit(id, req(SideSell, TypeMarket, "0.01"))
	h.setClock(time.Date(2026, 10, 9, 12, 10, 0, 0, time.UTC))
	snap() // dirty after the close
	h.setClock(time.Date(2026, 10, 9, 12, 15, 0, 0, time.UTC))
	snap() // flat and unchanged: skipped
	if n := count(); n != 3 {
		t.Errorf("snapshots = %d, want 3", n)
	}
}

func TestPruneSnapshotsKeepsBaselines(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	h.setClock(now)
	h.start()
	active, flat := h.newAccount(), h.newAccount()
	insert := func(id any, daysAgo int) {
		_, err := h.pool.Exec(ctx, `INSERT INTO equity_snapshots (account_id, run_id, ts, equity, cumulative_pnl)
			SELECT id, current_run_id, $2, 1000, 0 FROM accounts WHERE id = $1`, id, now.Add(-time.Duration(daysAgo)*24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []int{60, 50, 40, 10, 1} {
		insert(active, d)
	}
	insert(flat, 90) // flat since: its only snapshot is the baseline for every window
	insert(flat, 80)

	if err := h.eng.do(ctx, func(ctx context.Context) error { h.eng.pruneSnapshots(ctx); return nil }); err != nil {
		t.Fatal(err)
	}
	days := func(id any) []int {
		rows, _ := h.pool.Query(ctx, `SELECT ts FROM equity_snapshots WHERE account_id = $1 ORDER BY ts DESC`, id)
		var out []int
		for rows.Next() {
			var ts time.Time
			rows.Scan(&ts)
			out = append(out, int(now.Sub(ts).Hours()/24))
		}
		return out
	}
	if got := days(active); !slices.Equal(got, []int{1, 10, 40}) {
		t.Errorf("active account keeps %v, want [1 10 40]", got)
	}
	if got := days(flat); !slices.Equal(got, []int{80}) {
		t.Errorf("flat account keeps %v, want its latest baseline [80]", got)
	}
}
