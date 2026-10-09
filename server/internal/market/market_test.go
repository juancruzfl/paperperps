package market

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/juancruzfl/paperperps/server/internal/hyperliquid"
)

type fakeInfo struct {
	meta    hyperliquid.MetaAndAssetCtxs
	candles atomic.Int32
	fail    atomic.Bool
	release chan struct{} // if set, CandleSnapshot blocks until closed
}

func (f *fakeInfo) MetaAndAssetCtxs(context.Context) (hyperliquid.MetaAndAssetCtxs, error) {
	return f.meta, nil
}

func (f *fakeInfo) CandleSnapshot(_ context.Context, coin, interval string, start, end time.Time) ([]hyperliquid.Candle, error) {
	f.candles.Add(1)
	if f.release != nil {
		<-f.release
	}
	if f.fail.Load() {
		return nil, errors.New("upstream down")
	}
	return []hyperliquid.Candle{{Coin: coin, Interval: interval, OpenTime: start.UnixMilli()}}, nil
}

type published struct {
	topic string
	data  any
}

type fakePub struct {
	mu   sync.Mutex
	msgs []published
}

func (p *fakePub) Publish(topic string, data any) {
	p.mu.Lock()
	p.msgs = append(p.msgs, published{topic, data})
	p.mu.Unlock()
}

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func newTestService(t *testing.T) (*Service, *fakePub, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	info := &fakeInfo{meta: hyperliquid.MetaAndAssetCtxs{
		Meta: hyperliquid.Meta{Universe: []hyperliquid.AssetMeta{
			{Name: "SOL", SzDecimals: 2, MaxLeverage: 10},
			{Name: "BTC", SzDecimals: 5, MaxLeverage: 40},
			{Name: "OLD", SzDecimals: 1, MaxLeverage: 3, IsDelisted: true},
		}},
		Ctxs: []hyperliquid.AssetCtx{
			{MarkPx: d("108.6"), PrevDayPx: d("110.29")},
			{MarkPx: d("82137"), PrevDayPx: d("82564"), Funding: d("-0.0005739396")},
			{MarkPx: d("1")},
		},
	}}
	pub := &fakePub{}
	s := New(Options{
		Markets:   []string{"BTC", "XRP", "SOL", "OLD"},
		Info:      info,
		Publisher: pub,
		Now:       func() time.Time { return now },
	})
	if err := s.LoadMeta(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s, pub, &now
}

func TestLoadMetaDisablesUnlistedMarkets(t *testing.T) {
	s, _, _ := newTestService(t)

	var coins []string
	for _, tk := range s.Tickers() {
		coins = append(coins, tk.Coin)
	}
	if want := []string{"BTC", "SOL"}; !slices.Equal(coins, want) {
		t.Errorf("tickers = %v, want %v (configured order, unlisted and delisted removed)", coins, want)
	}
	if s.Has("XRP") || s.Has("OLD") {
		t.Error("unlisted market still enabled")
	}
	if subs := s.subscriptions(); len(subs) != 6 {
		t.Errorf("subscriptions = %d, want 3 per market", len(subs))
	}

	btc := s.Tickers()[0]
	if btc.SzDecimals != 5 || btc.MaxLeverage != 40 || !btc.MarkPx.Equal(d("82137")) {
		t.Errorf("BTC ticker = %+v", btc)
	}
}

func TestStaleness(t *testing.T) {
	s, _, now := newTestService(t)

	if !s.Tickers()[0].Stale {
		t.Error("not stale before the stream connects")
	}
	s.OnConnect()
	if s.Tickers()[0].Stale {
		t.Error("stale right after connecting with fresh metadata")
	}
	*now = now.Add(31 * time.Second)
	if !s.Tickers()[0].Stale {
		t.Error("not stale after 31s without updates")
	}
	s.OnAssetCtx(hyperliquid.ActiveAssetCtx{Coin: "BTC", Ctx: hyperliquid.AssetCtx{MarkPx: d("82200")}})
	tk := s.Tickers()
	if tk[0].Stale || !tk[0].MarkPx.Equal(d("82200")) {
		t.Errorf("BTC after ctx update = %+v", tk[0])
	}
	if !tk[1].Stale {
		t.Error("SOL should still be stale")
	}
	s.OnDisconnect(errors.New("boom"))
	if !s.Tickers()[0].Stale {
		t.Error("not stale after disconnect")
	}
}

func TestOnBookPublishesAndSnapshots(t *testing.T) {
	s, pub, _ := newTestService(t)

	if snap, ok := s.Snapshot(BookTopic("BTC")); !ok || snap != nil {
		t.Errorf("snapshot before any book = %v, %v; want nil, true", snap, ok)
	}
	s.OnBook(hyperliquid.L2Book{Coin: "BTC", Time: 1, Levels: [2][]hyperliquid.Level{
		{{Px: d("82092"), Sz: d("0.1"), N: 1}},
		{{Px: d("82093"), Sz: d("0.3"), N: 1}},
	}})
	s.OnBook(hyperliquid.L2Book{Coin: "XRP"}) // disabled market: ignored

	if len(pub.msgs) != 1 || pub.msgs[0].topic != "book:BTC" {
		t.Fatalf("published = %+v", pub.msgs)
	}
	snap, ok := s.Snapshot(BookTopic("BTC"))
	if b, isBook := snap.(Book); !ok || !isBook || !b.Asks[0].Px.Equal(d("82093")) {
		t.Errorf("snapshot = %+v", snap)
	}
	if _, ok := s.Snapshot(BookTopic("XRP")); ok {
		t.Error("snapshot for disabled market should not exist")
	}
	if _, ok := s.Snapshot("bogus"); ok {
		t.Error("bogus topic should not exist")
	}
}

func trade(tid, ms int64) hyperliquid.Trade {
	return hyperliquid.Trade{Coin: "BTC", Tid: tid, Time: ms, Side: hyperliquid.SideBuy, Px: d("1"), Sz: d("1")}
}

func TestOnTradesDedupesAndOrdersNewestFirst(t *testing.T) {
	s, pub, _ := newTestService(t)

	// The first message after subscribing replays history, oldest first.
	s.OnTrades([]hyperliquid.Trade{trade(10, 100), trade(11, 200)})
	// After a reconnect the history is replayed again with one new trade.
	s.OnTrades([]hyperliquid.Trade{trade(10, 100), trade(11, 200), trade(12, 300)})

	snap, _ := s.Snapshot(TradesTopic("BTC"))
	var tids []int64
	for _, tr := range snap.([]hyperliquid.Trade) {
		tids = append(tids, tr.Tid)
	}
	if want := []int64{12, 11, 10}; !slices.Equal(tids, want) {
		t.Errorf("stored tids = %v, want %v", tids, want)
	}
	if len(pub.msgs) != 2 {
		t.Fatalf("published %d messages, want 2", len(pub.msgs))
	}
	if second := pub.msgs[1].data.([]hyperliquid.Trade); len(second) != 1 || second[0].Tid != 12 {
		t.Errorf("second publish = %+v, want only tid 12", second)
	}

	// A full replay of known trades publishes nothing.
	s.OnTrades([]hyperliquid.Trade{trade(12, 300)})
	if len(pub.msgs) != 2 {
		t.Error("published a duplicate-only batch")
	}
}

func TestTradesAreCapped(t *testing.T) {
	s, _, _ := newTestService(t)
	var batch []hyperliquid.Trade
	for i := range int64(seenTids + 50) {
		batch = append(batch, trade(i, i))
	}
	s.OnTrades(batch)

	snap, _ := s.Snapshot(TradesTopic("BTC"))
	if got := snap.([]hyperliquid.Trade); len(got) != MaxTrades || got[0].Tid != seenTids+49 {
		t.Errorf("kept %d trades, newest tid %d", len(got), got[0].Tid)
	}
	st := s.states["BTC"]
	if len(st.seen) != seenTids || len(st.seenLog) != seenTids {
		t.Errorf("seen set size = %d/%d, want %d", len(st.seen), len(st.seenLog), seenTids)
	}
}

func TestTradesSnapshotIsEmptyListNotNull(t *testing.T) {
	s, _, _ := newTestService(t)
	snap, ok := s.Snapshot(TradesTopic("SOL"))
	if trades, isSlice := snap.([]hyperliquid.Trade); !ok || !isSlice || trades == nil {
		t.Errorf("snapshot = %#v, want empty non-nil slice", snap)
	}
}
