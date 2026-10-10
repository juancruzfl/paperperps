package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestTraderProfile(t *testing.T) {
	env := newAdminEnv(t)
	ctx := context.Background()
	acct, err := env.store.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := env.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Three fills: an open, a winning close (+50) and a losing close (-20).
	for _, f := range []struct{ side, px, sz, pnl string }{
		{"buy", "100", "2", "0"}, {"sell", "125", "2", "50"}, {"sell", "90", "1", "-20"},
	} {
		exec(`INSERT INTO fills (id, account_id, run_id, coin, side, px, sz, fee, liquidity, closed_pnl)
			SELECT gen_random_uuid(), id, current_run_id, 'ETH', $2, $3, $4, 0, 'taker', $5 FROM accounts WHERE id = $1`,
			acct.ID, f.side, f.px, f.sz, f.pnl)
	}
	exec(`INSERT INTO positions (account_id, coin, run_id, size, entry_px, leverage)
		SELECT id, 'BTC', current_run_id, -0.5, 80000, 10 FROM accounts WHERE id = $1`, acct.ID)
	exec(`INSERT INTO positions (account_id, coin, run_id, size, entry_px, leverage)
		SELECT id, 'SOL', current_run_id, 0, NULL, 5 FROM accounts WHERE id = $1`, acct.ID) // flat: not shown

	get := func(handle string) (int, map[string]any) {
		t.Helper()
		res, err := http.Get(env.srv.URL + "/api/traders/" + handle)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var body map[string]any
		json.NewDecoder(res.Body).Decode(&body)
		return res.StatusCode, body
	}

	status, p := get(strings.ToUpper(acct.Handle)) // handles are case-insensitive
	if status != http.StatusOK {
		t.Fatalf("status %d: %v", status, p)
	}
	if p["handle"] != acct.Handle || p["trades"] != 3.0 || p["closedTrades"] != 2.0 || p["wins"] != 1.0 ||
		p["bestTrade"] != "50" || p["volume"] != "540" || p["rank"] != 0.0 {
		t.Errorf("profile = %v", p)
	}
	positions := p["positions"].([]any)
	if len(positions) != 1 || positions[0].(map[string]any)["coin"] != "BTC" {
		t.Errorf("positions = %v, want only the open BTC short", positions)
	}
	for _, private := range []string{"accountId", "balance", "secret", "openOrders"} {
		if _, ok := p[private]; ok {
			t.Errorf("profile exposes %q", private)
		}
	}

	// Hidden from the leaderboard, by the trader or a moderator, or suspended: no profile.
	for _, sql := range []string{
		`UPDATE accounts SET leaderboard_opt_in = false WHERE id = $1`,
		`UPDATE accounts SET leaderboard_opt_in = true, leaderboard_banned = true WHERE id = $1`,
		`UPDATE accounts SET leaderboard_banned = false, suspended_at = now() WHERE id = $1`,
	} {
		exec(sql, acct.ID)
		if status, body := get(acct.Handle); status != http.StatusNotFound || body["error"] != "trader_not_found" {
			t.Errorf("after %q: %d %v, want 404 trader_not_found", sql, status, body)
		}
	}
	if status, _ := get("no-such-trader"); status != http.StatusNotFound {
		t.Errorf("unknown handle: status %d", status)
	}
}
