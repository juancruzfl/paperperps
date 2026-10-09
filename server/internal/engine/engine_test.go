package engine

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/juancruzfl/paperperps/server/internal/accounts"
	"github.com/juancruzfl/paperperps/server/internal/db"
	"github.com/juancruzfl/paperperps/server/internal/hyperliquid"
	"github.com/juancruzfl/paperperps/server/internal/market"
)

// fakeMarket is settable market data.
type fakeMarket struct {
	mu      sync.Mutex
	tickers map[string]market.Ticker
	books   map[string]market.Book
}

func newFakeMarket() *fakeMarket {
	return &fakeMarket{
		tickers: map[string]market.Ticker{
			"BTC": {Coin: "BTC", SzDecimals: 5, MaxLeverage: 40, MarkPx: d("82000")},
			"ETH": {Coin: "ETH", SzDecimals: 4, MaxLeverage: 25, MarkPx: d("2500")},
		},
		books: map[string]market.Book{},
	}
}

func (m *fakeMarket) Ticker(coin string) (market.Ticker, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tickers[coin]
	return t, ok
}

func (m *fakeMarket) Book(coin string) (market.Book, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.books[coin]
	return b, ok
}

// setBook sets coin's book (bid, ask) with plenty of size and moves the mark to mid.
func (m *fakeMarket) setBook(coin, bid, ask string) {
	m.setLevels(coin, levels(bid, "100"), levels(ask, "100"))
}

func (m *fakeMarket) setLevels(coin string, bids, asks []hyperliquid.Level) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.books[coin] = market.Book{Coin: coin, Bids: bids, Asks: asks}
	t := m.tickers[coin]
	t.MarkPx = bids[0].Px.Add(asks[0].Px).Div(decimal.NewFromInt(2))
	m.tickers[coin] = t
}

// setFunding sets coin's hourly funding rate and oracle price.
func (m *fakeMarket) setFunding(coin, rate, oracle string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tickers[coin]
	t.Funding, t.OraclePx = d(rate), d(oracle)
	m.tickers[coin] = t
}

func (m *fakeMarket) setStale(coin string, stale bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tickers[coin]
	t.Stale = stale
	m.tickers[coin] = t
}

type nopPub struct{}

func (nopPub) Publish(string, any) {}

var testConfig = Config{
	TakerFeeRate:    d("0.00045"),
	MakerFeeRate:    d("0.00015"),
	MaxSlippage:     d("0.05"),
	MinNotional:     d("10"),
	DefaultLeverage: 20,
}

type harness struct {
	clock atomic.Pointer[time.Time] // nil: real time
	mode  string                    // liquidation mode; default forfeit
	t     *testing.T
	pool  *pgxpool.Pool
	store *accounts.Store
	md    *fakeMarket
	eng   *Engine
	stop  context.CancelFunc
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, pool: pool, store: accounts.NewStore(pool, d("1000")), md: newFakeMarket()}
	h.md.setBook("BTC", "81990", "82000")
	h.md.setBook("ETH", "2499.9", "2500")
	h.start()
	return h
}

// start runs a fresh engine, as after a restart.
func (h *harness) start() {
	h.t.Helper()
	if h.stop != nil {
		h.stop()
	}
	cfg := testConfig
	cfg.LiquidationMode = h.mode
	cfg.RiskInterval = time.Hour // tests run risk checks explicitly with runRisk
	cfg.Now = func() time.Time {
		if t := h.clock.Load(); t != nil {
			return *t
		}
		return time.Now().UTC()
	}
	h.eng = New(h.pool, h.md, nopPub{}, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	h.stop = cancel
	h.t.Cleanup(cancel)
	go h.eng.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for !h.eng.Running() {
		if time.Now().After(deadline) {
			h.t.Fatal("engine did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (h *harness) setClock(t time.Time) { h.clock.Store(&t) }

// runRisk runs one liquidation and funding pass on the engine goroutine.
func (h *harness) runRisk() {
	h.t.Helper()
	err := h.eng.do(context.Background(), func(ctx context.Context) error {
		h.eng.checkRisk(ctx)
		h.eng.settleFunding(ctx)
		return nil
	})
	if err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) newAccount() uuid.UUID {
	h.t.Helper()
	c, err := h.store.Create(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	return c.ID
}

func (h *harness) place(id uuid.UUID, side, typ, sz, px string) (OrderResult, error) {
	return h.placeOn(id, "BTC", side, typ, sz, px)
}

func (h *harness) placeOn(id uuid.UUID, coin, side, typ, sz, px string) (OrderResult, error) {
	req := OrderRequest{Coin: coin, Side: side, Type: typ, Sz: d(sz)}
	if px != "" {
		req.Px = decimal.NewNullDecimal(d(px))
	}
	return h.eng.PlaceOrder(context.Background(), id, req)
}

func (h *harness) mustPlace(id uuid.UUID, side, typ, sz, px string) OrderResult {
	h.t.Helper()
	res, err := h.place(id, side, typ, sz, px)
	if err != nil {
		h.t.Fatalf("place %s %s %s@%s: %v", side, typ, sz, px, err)
	}
	return res
}

func (h *harness) load(id uuid.UUID) accounts.Account {
	h.t.Helper()
	a, err := h.store.Load(context.Background(), id)
	if err != nil {
		h.t.Fatal(err)
	}
	return a
}

// checkLedger asserts the cached balance equals the sum of the run's ledger entries.
func (h *harness) checkLedger(id uuid.UUID) {
	h.t.Helper()
	a := h.load(id)
	var sum decimal.Decimal
	if err := h.pool.QueryRow(context.Background(),
		`SELECT sum(amount) FROM ledger_entries WHERE run_id = $1`, a.RunID).Scan(&sum); err != nil {
		h.t.Fatal(err)
	}
	if !sum.Equal(a.Balance) {
		h.t.Errorf("ledger sum %s != balance %s", sum, a.Balance)
	}
}

func positionOf(a accounts.Account, coin string) accounts.Position {
	for _, p := range a.Positions {
		if p.Coin == coin {
			return p
		}
	}
	return accounts.Position{}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func wantReject(t *testing.T, err error, code string) {
	t.Helper()
	var rej *RejectError
	if !errors.As(err, &rej) || rej.Code != code {
		t.Errorf("err = %v, want reject %q", err, code)
	}
}

// The milestone's acceptance test: open and close a BTC long, with exact PnL and fees.
func TestOpenAndCloseLong(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()

	open := h.mustPlace(id, SideBuy, TypeMarket, "0.01", "")
	if open.Fill == nil || !open.Fill.Px.Equal(d("82000")) || open.Order.Status != StatusFilled {
		t.Fatalf("open = %+v", open)
	}
	eq(t, "open fee", open.Fill.Fee, "0.369") // 820 * 0.045%
	a := h.load(id)
	eq(t, "balance after open", a.Balance, "999.631")
	p := positionOf(a, "BTC")
	eq(t, "size", p.Size, "0.01")
	eq(t, "entry", p.EntryPx, "82000")
	if p.Leverage != 20 {
		t.Errorf("leverage = %d, want default 20", p.Leverage)
	}

	h.md.setBook("BTC", "83000", "83010")
	sum := SummarizeAccount(a, h.md)
	eq(t, "unrealized at mark 83005", sum.UnrealizedPnl, "10.05")

	closeRes := h.mustPlace(id, SideSell, TypeMarket, "0.01", "")
	eq(t, "close px", closeRes.Fill.Px, "83000")
	eq(t, "closed pnl", closeRes.Fill.ClosedPnl, "10")
	eq(t, "close fee", closeRes.Fill.Fee, "0.3735")

	a = h.load(id)
	eq(t, "final balance", a.Balance, "1009.2575") // 1000 + 10 - 0.369 - 0.3735
	eq(t, "final size", positionOf(a, "BTC").Size, "0")
	h.checkLedger(id)

	fills, err := h.store.Fills(context.Background(), id, 10)
	if err != nil || len(fills) != 2 {
		t.Fatalf("fills = %v, %v", fills, err)
	}
	history, err := h.store.OrderHistory(context.Background(), id, 10)
	if err != nil || len(history) != 2 {
		t.Fatalf("order history = %v, %v", history, err)
	}
}

func TestMarketOrderWalksTheBook(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()
	h.md.setLevels("BTC", levels("81990", "1"), levels("82000", "0.005", "82010", "1"))

	res := h.mustPlace(id, SideBuy, TypeMarket, "0.01", "")
	eq(t, "avg px", res.Fill.Px, "82005")
	eq(t, "fee", res.Fill.Fee, "0.369023") // 820.05 * 0.045% = 0.3690225, rounded half up

	// Liquidity ends within the 5% slippage bound: partial fill, remainder cancelled.
	h.md.setLevels("BTC", levels("81990", "1"), levels("82000", "0.001", "99000", "5"))
	res = h.mustPlace(id, SideBuy, TypeMarket, "0.01", "")
	eq(t, "partial filled", res.Order.FilledSz, "0.001")
	if res.Order.StatusReason == nil {
		t.Error("partial market fill should explain itself")
	}
	h.checkLedger(id)
}

func TestRejections(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()
	cases := []struct {
		name, side, typ, sz, px, code string
	}{
		{"too small", SideBuy, TypeMarket, "0.0001", "", "min_notional"},
		{"too many size decimals", SideBuy, TypeMarket, "0.000001", "", "invalid_size"},
		{"too much margin", SideBuy, TypeMarket, "1", "", "insufficient_margin"}, // $82k at 20x needs $4.1k
		{"limit without price", SideBuy, TypeLimit, "0.01", "", "invalid_price"},
		{"price with too many figures", SideBuy, TypeLimit, "0.01", "80000.5", "invalid_price"},
		{"bad side", "long", TypeMarket, "0.01", "", "invalid_side"},
		{"bad type", SideBuy, "stop", "0.01", "", "invalid_type"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := h.place(id, c.side, c.typ, c.sz, c.px)
			wantReject(t, err, c.code)
		})
	}

	_, err := h.eng.PlaceOrder(context.Background(), id, OrderRequest{Coin: "DOGE", Side: SideBuy, Type: TypeMarket, Sz: d("100")})
	wantReject(t, err, "unknown_market")

	h.md.setStale("BTC", true)
	_, err = h.place(id, SideBuy, TypeMarket, "0.01", "")
	wantReject(t, err, "market_stale")
	h.md.setStale("BTC", false)

	// Mid is 90495, so the 5% bound is 95020 and the only ask is beyond it.
	h.md.setLevels("BTC", levels("81990", "1"), levels("99000", "1"))
	_, err = h.place(id, SideBuy, TypeMarket, "0.01", "")
	wantReject(t, err, "no_liquidity")

	if a := h.load(id); !a.Balance.Equal(d("1000")) || len(a.OpenOrders) != 0 {
		t.Errorf("rejected orders changed the account: %+v", a)
	}
}

func TestLimitOrderRestsAndFillsAsMaker(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()

	res := h.mustPlace(id, SideBuy, TypeLimit, "0.01", "81000")
	if res.Order.Status != StatusOpen || res.Fill != nil {
		t.Fatalf("resting order = %+v", res)
	}
	a := h.load(id)
	if len(a.OpenOrders) != 1 {
		t.Fatalf("open orders = %d", len(a.OpenOrders))
	}
	eq(t, "order margin reserved", SummarizeAccount(a, h.md).OrderMargin, "40.5") // 810 / 20

	later := time.Now().Add(time.Second).UnixMilli()
	// A trade from before the order, and one exactly at the limit, don't fill it.
	h.eng.NewTrades("BTC", []hyperliquid.Trade{
		{Coin: "BTC", Px: d("80000"), Sz: d("1"), Time: time.Now().Add(-time.Hour).UnixMilli()},
		{Coin: "BTC", Px: d("81000"), Sz: d("1"), Time: later},
	})
	// A trade through the price does.
	h.eng.NewTrades("BTC", []hyperliquid.Trade{{Coin: "BTC", Px: d("80999"), Sz: d("1"), Time: later}})
	eventually(t, "resting fill", func() bool { return len(h.load(id).OpenOrders) == 0 })

	fills, _ := h.store.Fills(context.Background(), id, 10)
	if len(fills) != 1 || fills[0].Liquidity != "maker" {
		t.Fatalf("fills = %+v", fills)
	}
	eq(t, "maker fill px", fills[0].Px, "81000")
	eq(t, "maker fee", fills[0].Fee, "0.1215") // 810 * 0.015%
	eq(t, "position", positionOf(h.load(id), "BTC").Size, "0.01")
	h.checkLedger(id)
}

func TestLimitOrderFillsWhenBookCrossesIt(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()
	h.mustPlace(id, SideSell, TypeLimit, "0.01", "83000")

	h.md.setBook("BTC", "83000", "83010") // best bid at the price: not through it
	h.eng.BookChanged("BTC")
	time.Sleep(50 * time.Millisecond)
	if len(h.load(id).OpenOrders) != 1 {
		t.Fatal("filled on a touch")
	}

	h.md.setBook("BTC", "83001", "83010")
	h.eng.BookChanged("BTC")
	eventually(t, "fill on cross", func() bool { return len(h.load(id).OpenOrders) == 0 })
	eq(t, "short", positionOf(h.load(id), "BTC").Size, "-0.01")
}

func TestCrossingLimitTakesThenRests(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()
	h.md.setLevels("BTC", levels("81990", "1"), levels("82000", "0.004", "82100", "1"))

	res := h.mustPlace(id, SideBuy, TypeLimit, "0.01", "82050")
	if res.Fill == nil || res.Order.Status != StatusOpen {
		t.Fatalf("res = %+v", res)
	}
	eq(t, "taken", res.Fill.Sz, "0.004")
	if res.Fill.Liquidity != "taker" {
		t.Error("crossing part should be taker")
	}
	a := h.load(id)
	eq(t, "position", positionOf(a, "BTC").Size, "0.004")
	eq(t, "resting filled_sz", a.OpenOrders[0].FilledSz, "0.004")
}

func TestCancelOrder(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()
	other := h.newAccount()
	res := h.mustPlace(id, SideBuy, TypeLimit, "0.01", "80000")

	wantReject(t, h.eng.CancelOrder(context.Background(), other, res.Order.ID), "not_found")
	if err := h.eng.CancelOrder(context.Background(), id, res.Order.ID); err != nil {
		t.Fatal(err)
	}
	if len(h.load(id).OpenOrders) != 0 {
		t.Error("order still open")
	}
	wantReject(t, h.eng.CancelOrder(context.Background(), id, res.Order.ID), "not_found")
}

func TestLeverage(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()
	ctx := context.Background()

	wantReject(t, h.eng.SetLeverage(ctx, id, "BTC", 41), "invalid_leverage")
	wantReject(t, h.eng.SetLeverage(ctx, id, "BTC", 0), "invalid_leverage")
	if err := h.eng.SetLeverage(ctx, id, "BTC", 40); err != nil {
		t.Fatal(err)
	}
	// $30k at 40x needs $750 of margin: fine. Dropping to 2x would need $15k: refused.
	h.mustPlace(id, SideBuy, TypeMarket, "0.36", "")
	wantReject(t, h.eng.SetLeverage(ctx, id, "BTC", 2), "insufficient_margin")
	if lev := positionOf(h.load(id), "BTC").Leverage; lev != 40 {
		t.Errorf("leverage = %d, want 40", lev)
	}

	// Closing is always allowed, even when the account couldn't open it again.
	h.md.setBook("BTC", "80000", "80010")
	h.mustPlace(id, SideSell, TypeMarket, "0.36", "")
	h.checkLedger(id)
}

func TestRestartKeepsRestingOrders(t *testing.T) {
	h := newHarness(t)
	id := h.newAccount()
	h.mustPlace(id, SideBuy, TypeLimit, "0.01", "81000")

	h.start() // a new engine instance, as after a deploy
	h.eng.NewTrades("BTC", []hyperliquid.Trade{{Coin: "BTC", Px: d("80000"), Sz: d("1"), Time: time.Now().Add(time.Second).UnixMilli()}})
	eventually(t, "fill after restart", func() bool { return len(h.load(id).OpenOrders) == 0 })
}

func TestUnavailableWhenNotRunning(t *testing.T) {
	e := New(nil, newFakeMarket(), nopPub{}, testConfig)
	_, err := e.PlaceOrder(context.Background(), uuid.New(), OrderRequest{})
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}
