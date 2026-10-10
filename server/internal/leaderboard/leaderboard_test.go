package leaderboard

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/juancruzfl/paperperps/server/internal/accounts"
	"github.com/juancruzfl/paperperps/server/internal/db"
	"github.com/juancruzfl/paperperps/server/internal/dbtest"
	"github.com/juancruzfl/paperperps/server/internal/market"
)

type marks map[string]string

func (m marks) Ticker(coin string) (market.Ticker, bool) {
	px, ok := m[coin]
	if !ok {
		return market.Ticker{}, false
	}
	return market.Ticker{Coin: coin, MarkPx: decimal.RequireFromString(px), MaxLeverage: 40}, true
}

func (m marks) Book(string) (market.Book, bool) { return market.Book{}, false }

// freshDB gives the test its own database: the board ranks every account in it.
func freshDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, drop, ok, err := dbtest.Create("paperperps_lb")
	if !ok {
		t.Skip("TEST_DATABASE_URL not set")
	}
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		drop()
	})
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

type seed struct {
	t     *testing.T
	pool  *pgxpool.Pool
	store *accounts.Store
	now   time.Time
}

// account creates an account aged ageHours with trades fills and its current run's
// balance set (the deposit was 1000).
func (s seed) account(handle string, ageHours, trades int, balance string) uuid.UUID {
	s.t.Helper()
	ctx := context.Background()
	c, err := s.store.Create(ctx)
	if err != nil {
		s.t.Fatal(err)
	}
	s.exec(`UPDATE accounts SET handle = $2, created_at = $3 WHERE id = $1`, c.ID, handle, s.now.Add(-time.Duration(ageHours)*time.Hour))
	s.exec(`UPDATE runs SET balance = $2 WHERE account_id = $1`, c.ID, balance)
	for i := range trades {
		s.exec(`INSERT INTO fills (id, account_id, run_id, coin, side, px, sz, fee, liquidity, created_at)
			SELECT $2, a.id, a.current_run_id, 'BTC', 'buy', 80000, 0.01, 0, 'taker', $3 FROM accounts a WHERE a.id = $1`,
			c.ID, accounts.NewID(), s.now.Add(-time.Duration(i)*time.Hour))
	}
	return c.ID
}

func (s seed) exec(sql string, args ...any) {
	s.t.Helper()
	if _, err := s.pool.Exec(context.Background(), sql, args...); err != nil {
		s.t.Fatalf("%s: %v", sql, err)
	}
}

func TestLeaderboard(t *testing.T) {
	pool := freshDB(t)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	s := seed{t: t, pool: pool, store: accounts.NewStore(pool, decimal.NewFromInt(1000)), now: now}

	// alice: +100 realized and a 0.01 BTC long from 80,000 marked at 82,000 (+20) → 120.
	alice := s.account("alice", 48, 5, "1100")
	s.exec(`INSERT INTO positions (account_id, coin, run_id, size, entry_px, leverage)
		SELECT id, 'BTC', current_run_id, 0.01, 80000, 20 FROM accounts WHERE id = $1`, alice)
	// alice was at +50 thirty hours ago, so her 24h score is +70.
	s.exec(`INSERT INTO equity_snapshots (account_id, run_id, ts, equity, cumulative_pnl)
		SELECT id, current_run_id, $2, 1050, 50 FROM accounts WHERE id = $1`, alice, now.Add(-30*time.Hour))

	// bob reset once: the old run ended at 900 with -50 locked (-150); the new run is at
	// 1,030 (+30). Lifetime: -120. A reset doesn't wipe the loss.
	bob := s.account("bob", 48, 5, "1030")
	s.exec(`INSERT INTO runs (id, account_id, starting_balance, balance, locked_upnl, started_at, ended_at)
		VALUES ($1, $2, 1000, 900, -50, $3, $3)`, accounts.NewID(), bob, now.Add(-40*time.Hour))

	carol := s.account("carol", 48, 3, "2000") // +1,000 but only 3 trades
	dave := s.account("dave", 48, 5, "1500")   // +500 but opted out
	s.exec(`UPDATE accounts SET leaderboard_opt_in = false WHERE id = $1`, dave)
	erin := s.account("erin", 2, 5, "1300") // +300 but 2 hours old
	// frank and gina would lead, but a moderator hid one and suspended the other.
	frank := s.account("frank", 48, 5, "3000")
	s.exec(`UPDATE accounts SET leaderboard_banned = true WHERE id = $1`, frank)
	gina := s.account("gina", 48, 5, "4000")
	s.exec(`UPDATE accounts SET suspended_at = now(), suspend_reason = 'x' WHERE id = $1`, gina)
	_, _ = carol, erin

	lb := New(pool, marks{"BTC": "82000"}, 5, 24*time.Hour, time.Minute)
	lb.now = func() time.Time { return now }
	ctx := context.Background()

	b, err := lb.Get(ctx, "all", alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Entries) != 2 || b.Entries[0].Handle != "alice" || b.Entries[1].Handle != "bob" {
		t.Fatalf("all-time entries = %+v", b.Entries)
	}
	if !b.Entries[0].Pnl.Equal(decimal.NewFromInt(120)) || !b.Entries[1].Pnl.Equal(decimal.NewFromInt(-120)) {
		t.Errorf("scores = %s, %s; want 120, -120", b.Entries[0].Pnl, b.Entries[1].Pnl)
	}
	if b.Entries[1].Resets != 1 || b.Entries[0].Trades != 5 || !b.Entries[0].You {
		t.Errorf("entry details = %+v", b.Entries)
	}
	if b.You == nil || !b.You.Eligible || b.You.Rank != 1 {
		t.Errorf("viewer = %+v", b.You)
	}

	day, err := lb.Get(ctx, "24h", uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	if !day.Entries[0].Pnl.Equal(decimal.NewFromInt(70)) {
		t.Errorf("alice 24h = %s, want 70 (120 now - 50 at the window start)", day.Entries[0].Pnl)
	}
	if day.You != nil {
		t.Error("anonymous viewers get no standing")
	}

	for viewer, want := range map[uuid.UUID]string{
		carol:            "make 2 more trade(s) to appear",
		dave:             "you've hidden yourself from the leaderboard",
		erin:             "accounts appear once they're 24 hours old",
		frank:            "a moderator has hidden this account from the leaderboard",
		gina:             "a moderator has hidden this account from the leaderboard",
		accounts.NewID(): "make at least 5 trades to appear",
	} {
		b, _ := lb.Get(ctx, "all", viewer)
		if b.You == nil || b.You.Eligible || b.You.Reason != want {
			t.Errorf("viewer reason = %+v, want %q", b.You, want)
		}
	}

	if _, err := lb.Get(ctx, "1y", uuid.Nil); err != ErrBadWindow {
		t.Errorf("bad window err = %v", err)
	}
}
