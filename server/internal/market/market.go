// Package market keeps the live state of every enabled perp (book, trades, prices, funding)
// from Hyperliquid's feed, and publishes changes to websocket clients.
//
// The feed is read-only market data, so it runs on every instance, leader or not. During a
// deploy, clients connected to the standby instance still get live prices.
package market

import (
	"cmp"
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shopspring/decimal"

	"github.com/juancruzfl/paperperps/server/internal/hyperliquid"
)

const (
	TopicMarkets = "markets"
	topicBook    = "book:"
	topicTrades  = "trades:"

	// MaxTrades is how many recent trades are kept per market.
	MaxTrades = 100
	// seenTids bounds the memory used to de-duplicate trades across reconnects.
	seenTids = 1000
)

func BookTopic(coin string) string   { return topicBook + coin }
func TradesTopic(coin string) string { return topicTrades + coin }

// Info is the subset of the Hyperliquid REST API the service uses.
type Info interface {
	MetaAndAssetCtxs(ctx context.Context) (hyperliquid.MetaAndAssetCtxs, error)
	CandleSnapshot(ctx context.Context, coin, interval string, start, end time.Time) ([]hyperliquid.Candle, error)
}

// Publisher delivers updates to subscribers of a topic.
type Publisher interface {
	Publish(topic string, data any)
}

// Observer is told about market changes, e.g. so the engine can fill resting orders. Calls
// are made from the feed goroutine and must not block.
type Observer interface {
	BookChanged(coin string)
	NewTrades(coin string, trades []hyperliquid.Trade)
}

// Ticker is a market's summary row: what the market list and stats bar show.
type Ticker struct {
	Coin         string              `json:"coin"`
	SzDecimals   int                 `json:"szDecimals"`
	MaxLeverage  int                 `json:"maxLeverage"`
	MarkPx       decimal.Decimal     `json:"markPx"`
	MidPx        decimal.NullDecimal `json:"midPx"`
	OraclePx     decimal.Decimal     `json:"oraclePx"`
	PrevDayPx    decimal.Decimal     `json:"prevDayPx"`
	DayNtlVlm    decimal.Decimal     `json:"dayNtlVlm"`
	Funding      decimal.Decimal     `json:"funding"`      // hourly rate
	OpenInterest decimal.Decimal     `json:"openInterest"` // in coin units
	// Stale is true when the feed is down or this market hasn't updated recently. The
	// engine must not fill or liquidate against stale prices.
	Stale bool `json:"stale"`
}

// Book is an order book snapshot as sent to clients.
type Book struct {
	Coin string              `json:"coin"`
	Time int64               `json:"time"`
	Bids []hyperliquid.Level `json:"bids"`
	Asks []hyperliquid.Level `json:"asks"`
}

type state struct {
	meta    hyperliquid.AssetMeta
	hasMeta bool
	ctx     hyperliquid.AssetCtx
	ctxAt   time.Time
	book    *Book
	trades  []hyperliquid.Trade // newest first
	seen    map[int64]struct{}
	seenLog []int64 // insertion order, to evict from seen
}

type Options struct {
	Markets    []string
	Info       Info
	Publisher  Publisher
	StaleAfter time.Duration    // default 30s
	Now        func() time.Time // default time.Now
}

type Service struct {
	info       Info
	pub        Publisher
	staleAfter time.Duration
	now        func() time.Time

	observer atomic.Pointer[Observer]

	mu        sync.RWMutex
	connected bool
	order     []string // enabled markets, in configured order
	states    map[string]*state
}

func New(o Options) *Service {
	s := &Service{
		info:       o.Info,
		pub:        o.Publisher,
		staleAfter: o.StaleAfter,
		now:        o.Now,
		states:     map[string]*state{},
	}
	if s.staleAfter == 0 {
		s.staleAfter = 30 * time.Second
	}
	if s.now == nil {
		s.now = time.Now
	}
	for _, coin := range o.Markets {
		s.order = append(s.order, coin)
		s.states[coin] = &state{seen: map[int64]struct{}{}}
	}
	return s
}

// Run loads market metadata, then streams from wsURL until ctx is done.
func (s *Service) Run(ctx context.Context, wsURL string) {
	for attempt := 0; ; attempt++ {
		err := s.LoadMeta(ctx)
		if err == nil {
			break
		}
		wait := min(time.Duration(1<<min(attempt, 5))*time.Second, 30*time.Second)
		slog.Error("load market metadata", "err", err, "retry_in", wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}

	stream := &hyperliquid.Stream{URL: wsURL, Subs: s.subscriptions(), Handler: s}
	go stream.Run(ctx)

	tickers := time.NewTicker(time.Second)
	defer tickers.Stop()
	refresh := time.NewTicker(10 * time.Minute)
	defer refresh.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tickers.C:
			s.pub.Publish(TopicMarkets, s.Tickers())
		case <-refresh.C:
			if err := s.LoadMeta(ctx); err != nil {
				slog.Warn("refresh market metadata", "err", err)
			}
		}
	}
}

// LoadMeta fetches metadata and contexts for the enabled markets. On the first load, any
// configured coin the network doesn't list is disabled.
func (s *Service) LoadMeta(ctx context.Context) error {
	res, err := s.info.MetaAndAssetCtxs(ctx)
	if err != nil {
		return err
	}
	listed := map[string]int{}
	for i, a := range res.Meta.Universe {
		if !a.IsDelisted {
			listed[a.Name] = i
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var missing []string
	s.order = slices.DeleteFunc(s.order, func(coin string) bool {
		i, ok := listed[coin]
		st := s.states[coin]
		if !ok {
			if st.hasMeta {
				return false // delisted after startup: keep serving what we have
			}
			missing = append(missing, coin)
			delete(s.states, coin)
			return true
		}
		st.meta, st.hasMeta = res.Meta.Universe[i], true
		st.ctx, st.ctxAt = res.Ctxs[i], now
		return false
	})
	if len(missing) > 0 {
		slog.Warn("markets not listed on this network; disabled", "coins", strings.Join(missing, ","))
	}
	slog.Info("market metadata loaded", "markets", len(s.order))
	return nil
}

func (s *Service) subscriptions() []hyperliquid.Subscription {
	s.mu.RLock()
	defer s.mu.RUnlock()
	subs := make([]hyperliquid.Subscription, 0, 3*len(s.order))
	for _, coin := range s.order {
		subs = append(subs,
			hyperliquid.Subscription{Type: "l2Book", Coin: coin},
			hyperliquid.Subscription{Type: "trades", Coin: coin},
			hyperliquid.Subscription{Type: "activeAssetCtx", Coin: coin},
		)
	}
	return subs
}

// SetObserver registers o for market changes; nil removes it.
func (s *Service) SetObserver(o Observer) {
	if o == nil {
		s.observer.Store(nil)
		return
	}
	s.observer.Store(&o)
}

func (s *Service) notify(fn func(Observer)) {
	if o := s.observer.Load(); o != nil {
		fn(*o)
	}
}

// Ticker returns one market's summary.
func (s *Service) Ticker(coin string) (Ticker, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.states[coin]
	if !ok || !st.hasMeta {
		return Ticker{}, false
	}
	return s.ticker(coin, st, s.now()), true
}

// Book returns a market's latest order book.
func (s *Service) Book(coin string) (Book, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.states[coin]
	if !ok || st.book == nil {
		return Book{}, false
	}
	return *st.book, true
}

// Has reports whether coin is an enabled market.
func (s *Service) Has(coin string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.states[coin]
	return ok
}

// Tickers returns every enabled market that has metadata, in configured order.
func (s *Service) Tickers() []Ticker {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := s.now()
	out := make([]Ticker, 0, len(s.order))
	for _, coin := range s.order {
		st := s.states[coin]
		if !st.hasMeta {
			continue
		}
		out = append(out, s.ticker(coin, st, now))
	}
	return out
}

// ticker must be called with s.mu held.
func (s *Service) ticker(coin string, st *state, now time.Time) Ticker {
	return Ticker{
		Coin:         coin,
		SzDecimals:   st.meta.SzDecimals,
		MaxLeverage:  st.meta.MaxLeverage,
		MarkPx:       st.ctx.MarkPx,
		MidPx:        st.ctx.MidPx,
		OraclePx:     st.ctx.OraclePx,
		PrevDayPx:    st.ctx.PrevDayPx,
		DayNtlVlm:    st.ctx.DayNtlVlm,
		Funding:      st.ctx.Funding,
		OpenInterest: st.ctx.OpenInterest,
		Stale:        !s.connected || now.Sub(st.ctxAt) > s.staleAfter,
	}
}

// Snapshot returns the current state of a topic, for clients that just subscribed.
func (s *Service) Snapshot(topic string) (any, bool) {
	if topic == TopicMarkets {
		return s.Tickers(), true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if coin, ok := strings.CutPrefix(topic, topicBook); ok {
		st, ok := s.states[coin]
		if !ok {
			return nil, false
		}
		if st.book == nil {
			return nil, true
		}
		return *st.book, true
	}
	if coin, ok := strings.CutPrefix(topic, topicTrades); ok {
		st, ok := s.states[coin]
		if !ok {
			return nil, false
		}
		return append([]hyperliquid.Trade{}, st.trades...), true
	}
	return nil, false
}

// --- hyperliquid.Handler ---

func (s *Service) OnConnect() {
	s.mu.Lock()
	s.connected = true
	s.mu.Unlock()
}

func (s *Service) OnDisconnect(error) {
	s.mu.Lock()
	s.connected = false
	s.mu.Unlock()
}

func (s *Service) OnBook(b hyperliquid.L2Book) {
	book := Book{Coin: b.Coin, Time: b.Time, Bids: b.Bids(), Asks: b.Asks()}
	s.mu.Lock()
	st, ok := s.states[b.Coin]
	if ok {
		st.book = &book
	}
	s.mu.Unlock()
	if ok {
		s.pub.Publish(BookTopic(b.Coin), book)
		s.notify(func(o Observer) { o.BookChanged(b.Coin) })
	}
}

// OnTrades stores and publishes trades not seen before. Hyperliquid replays recent trades
// after every (re)subscribe, so duplicates are expected.
func (s *Service) OnTrades(trades []hyperliquid.Trade) {
	if len(trades) == 0 {
		return
	}
	coin := trades[0].Coin
	s.mu.Lock()
	st, ok := s.states[coin]
	if !ok {
		s.mu.Unlock()
		return
	}
	fresh := make([]hyperliquid.Trade, 0, len(trades))
	for _, t := range trades {
		if _, dup := st.seen[t.Tid]; dup {
			continue
		}
		st.seen[t.Tid] = struct{}{}
		st.seenLog = append(st.seenLog, t.Tid)
		fresh = append(fresh, t)
	}
	if over := len(st.seenLog) - seenTids; over > 0 {
		for _, tid := range st.seenLog[:over] {
			delete(st.seen, tid)
		}
		st.seenLog = slices.Delete(st.seenLog, 0, over)
	}
	// Hyperliquid sends oldest first; clients want newest first.
	slices.SortStableFunc(fresh, func(a, b hyperliquid.Trade) int { return cmp.Compare(b.Time, a.Time) })
	st.trades = append(fresh, st.trades...)
	if len(st.trades) > MaxTrades {
		st.trades = st.trades[:MaxTrades]
	}
	s.mu.Unlock()

	if len(fresh) > 0 {
		s.pub.Publish(TradesTopic(coin), fresh)
		s.notify(func(o Observer) { o.NewTrades(coin, fresh) })
	}
}

func (s *Service) OnAssetCtx(c hyperliquid.ActiveAssetCtx) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.states[c.Coin]; ok {
		st.ctx, st.ctxAt = c.Ctx, s.now()
	}
}
