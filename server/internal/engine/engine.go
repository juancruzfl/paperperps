package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/juancruzfl/paperperps/server/internal/accounts"
	"github.com/juancruzfl/paperperps/server/internal/hyperliquid"
	"github.com/juancruzfl/paperperps/server/internal/market"
)

// MarketData is the live market state the engine trades against.
type MarketData interface {
	Ticker(coin string) (market.Ticker, bool)
	Book(coin string) (market.Book, bool)
}

// Publisher notifies an account's websocket subscribers.
type Publisher interface {
	Publish(topic string, data any)
}

// AccountTopic is the private websocket topic for an account's order and fill events.
func AccountTopic(id uuid.UUID) string { return "account:" + id.String() }

type Config struct {
	TakerFeeRate decimal.Decimal
	MakerFeeRate decimal.Decimal
	// MaxSlippage bounds market orders: they won't fill further than this from mid.
	MaxSlippage decimal.Decimal
	// MinNotional is the smallest order value accepted, in USD.
	MinNotional decimal.Decimal
	// DefaultLeverage applies to coins the account hasn't set leverage for, capped at
	// the coin's max.
	DefaultLeverage int
	// LiquidationMode is "forfeit" (lose the remaining margin, like HL's backstop) or
	// "market" (keep it, like HL's market liquidation). See docs/plan.md §4.6.
	LiquidationMode string
	// RiskInterval is how often liquidations and funding are checked. Default 1s.
	RiskInterval time.Duration
	// Now is the clock; tests replace it. Default time.Now in UTC.
	Now func() time.Time
}

const (
	LiquidationForfeit = "forfeit"
	LiquidationMarket  = "market"

	MarginCross    = "cross"
	MarginIsolated = "isolated"
)

const (
	SideBuy  = "buy"
	SideSell = "sell"

	TypeMarket     = "market"
	TypeLimit      = "limit"
	TypeStopMarket = "stop_market"
	TypeStopLimit  = "stop_limit"
	TypeTakeMarket = "take_profit_market"
	TypeTakeLimit  = "take_profit_limit"

	TIFGTC = "gtc" // rest until filled or cancelled
	TIFIOC = "ioc" // fill what's possible now, cancel the rest
	TIFALO = "alo" // post-only: rejected if it would fill immediately

	StatusOpen      = "open"      // resting limit, or a trigger order waiting for its price
	StatusTriggered = "triggered" // a limit-type trigger order that fired and now rests
	StatusFilled    = "filled"
	StatusCancelled = "cancelled"
)

// ErrUnavailable means this instance isn't running the engine (standby, or starting up).
var ErrUnavailable = errors.New("trading engine unavailable")

// RejectError is an order or request the engine refused, with a code clients can act on.
type RejectError struct {
	Code    string `json:"error"`
	Message string `json:"message"`
}

func (e *RejectError) Error() string { return e.Code + ": " + e.Message }

func reject(code, format string, args ...any) *RejectError {
	return &RejectError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// AccountEvent is published on AccountTopic when an account's orders or fills change.
type AccountEvent struct {
	Type        string           `json:"type"` // order | fill | leverage | margin | liquidation | funding
	Order       *accounts.Order  `json:"order,omitempty"`
	Fill        *accounts.Fill   `json:"fill,omitempty"`
	Liquidation *LiquidationInfo `json:"liquidation,omitempty"`
}

// LiquidationInfo tells the user what a liquidation closed and cost.
type LiquidationInfo struct {
	Coins          []string        `json:"coins"`
	Cross          bool            `json:"cross"`
	Fills          []accounts.Fill `json:"fills"`
	LiquidationFee decimal.Decimal `json:"liquidationFee"` // remaining margin forfeited
	BadDebt        decimal.Decimal `json:"badDebt"`        // loss beyond the margin, absorbed
}

// --- in-memory state, owned by the Run goroutine ---

type position struct {
	size      decimal.Decimal
	entryPx   decimal.Decimal
	leverage  int
	isolated  bool
	isoMargin decimal.Decimal // isolated positions only
}

func (p *position) mode() string {
	if p.isolated {
		return MarginIsolated
	}
	return MarginCross
}

type account struct {
	id        uuid.UUID
	runID     uuid.UUID
	balance   decimal.Decimal
	positions map[string]*position
	orders    map[uuid.UUID]*accounts.Order // open orders
}

type event struct {
	coin   string
	trades []hyperliquid.Trade // nil for a book change
}

// Engine executes orders for every account. One goroutine (Run) owns all trading state and
// handles commands and market events in sequence, so cross-margin math never races. Every
// change is committed to Postgres before memory is updated.
type Engine struct {
	pool *pgxpool.Pool
	md   MarketData
	pub  Publisher
	cfg  Config
	now  func() time.Time

	cmds    chan func(context.Context)
	events  chan event
	running atomic.Bool
	dropped atomic.Int64

	accounts map[uuid.UUID]*account
	resting  map[string]map[uuid.UUID]*accounts.Order // coin → limit orders on the book
	triggers map[string]map[uuid.UUID]*accounts.Order // coin → trigger orders not yet fired
	owners   map[uuid.UUID]*account                   // open order id → its account

	lastFunding time.Time // the last funding hour settled
}

func New(pool *pgxpool.Pool, md MarketData, pub Publisher, cfg Config) *Engine {
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.RiskInterval == 0 {
		cfg.RiskInterval = time.Second
	}
	if cfg.LiquidationMode == "" {
		cfg.LiquidationMode = LiquidationForfeit
	}
	return &Engine{
		pool:   pool,
		md:     md,
		pub:    pub,
		cfg:    cfg,
		now:    now,
		cmds:   make(chan func(context.Context)),
		events: make(chan event, 4096),
	}
}

// Running reports whether the engine accepts commands.
func (e *Engine) Running() bool { return e.running.Load() }

// Run loads open orders and processes commands and market events until ctx is done. Only
// the instance holding the engine lock may call it.
func (e *Engine) Run(ctx context.Context) error {
	e.accounts = map[uuid.UUID]*account{}
	e.resting = map[string]map[uuid.UUID]*accounts.Order{}
	e.triggers = map[string]map[uuid.UUID]*accounts.Order{}
	e.owners = map[uuid.UUID]*account{}

	// Every account with an open order or position must be in memory: resting orders fill,
	// positions get liquidated and pay funding.
	rows, err := e.pool.Query(ctx, `
		SELECT account_id FROM orders WHERE status IN ('open', 'triggered')
		UNION SELECT account_id FROM positions WHERE size <> 0`)
	if err != nil {
		return fmt.Errorf("load active accounts: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return fmt.Errorf("load active accounts: %w", err)
	}
	for _, id := range ids {
		if _, err := e.account(ctx, id); err != nil {
			return err
		}
	}
	if e.lastFunding, err = loadLastFunding(ctx, e.pool, e.now()); err != nil {
		return err
	}
	slog.Info("engine started", "active_accounts", len(ids), "last_funding_hour", e.lastFunding)

	risk := time.NewTicker(e.cfg.RiskInterval)
	defer risk.Stop()

	e.running.Store(true)
	defer e.running.Store(false)
	for {
		select {
		case <-ctx.Done():
			return nil
		case fn := <-e.cmds:
			fn(ctx)
		case ev := <-e.events:
			e.handleEvent(ctx, ev)
		case <-risk.C:
			e.riskTick(ctx)
		}
	}
}

// do runs fn on the engine goroutine and waits for its result. fn gets the engine's
// context, not the caller's, so a client hanging up can't abort a half-applied change.
func (e *Engine) do(ctx context.Context, fn func(context.Context) error) error {
	if !e.running.Load() {
		return ErrUnavailable
	}
	done := make(chan error, 1)
	select {
	case e.cmds <- func(engineCtx context.Context) { done <- fn(engineCtx) }:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// --- market.Observer ---

func (e *Engine) BookChanged(coin string) { e.enqueue(event{coin: coin}) }

func (e *Engine) NewTrades(coin string, trades []hyperliquid.Trade) {
	e.enqueue(event{coin: coin, trades: trades})
}

func (e *Engine) enqueue(ev event) {
	if !e.running.Load() {
		return
	}
	select {
	case e.events <- ev:
	default:
		// The feed must never block. A dropped event only delays a fill until the next one.
		if n := e.dropped.Add(1); n%1000 == 1 {
			slog.Warn("engine: market event queue full, dropping events", "dropped_total", n)
		}
	}
}

// --- commands ---

// SetLeverage changes the account's leverage on coin.
func (e *Engine) SetLeverage(ctx context.Context, accountID uuid.UUID, coin string, leverage int) error {
	return e.do(ctx, func(ctx context.Context) error { return e.setLeverage(ctx, accountID, coin, leverage) })
}

func (e *Engine) setLeverage(ctx context.Context, accountID uuid.UUID, coin string, leverage int) error {
	ticker, ok := e.md.Ticker(coin)
	if !ok {
		return reject("unknown_market", "%s is not a listed market", coin)
	}
	if leverage < 1 || leverage > ticker.MaxLeverage {
		return reject("invalid_leverage", "leverage must be between 1 and %d", ticker.MaxLeverage)
	}
	a, err := e.account(ctx, accountID)
	if err != nil {
		return err
	}
	pos := a.position(coin, e.defaultLeverage(ticker))
	if pos.isolated && !pos.size.IsZero() {
		return reject("isolated_position_open", "close the isolated %s position before changing its leverage", coin)
	}
	sc := pos.scenario(coin, a.balance)
	sc.leverage = leverage
	if !e.affordable(a, sc) {
		return reject("insufficient_margin", "not enough available margin to lower leverage")
	}
	err = pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
		return upsertPosition(ctx, tx, a.id, a.runID, coin, &position{
			size: pos.size, entryPx: pos.entryPx, leverage: leverage, isolated: pos.isolated, isoMargin: pos.isoMargin,
		}, e.now())
	})
	if err != nil {
		return err
	}
	pos.leverage = leverage
	e.publish(a.id, AccountEvent{Type: "leverage"})
	return nil
}

// --- helpers ---

func (e *Engine) newFill(pos *position, orderID *uuid.UUID, coin string, buy bool,
	sz, px, fee decimal.Decimal, liquidity string, now time.Time) fillRecord {
	res := ApplyFill(pos.size, pos.entryPx, buy, sz, px)
	side := SideSell
	if buy {
		side = SideBuy
	}
	return fillRecord{
		fill: accounts.Fill{
			ID:        accounts.NewID(),
			OrderID:   orderID,
			Coin:      coin,
			Side:      side,
			Px:        px,
			Sz:        sz,
			Fee:       fee,
			Liquidity: liquidity,
			ClosedPnl: res.ClosedPnl,
			CreatedAt: now,
		},
		newSize:      res.NewSize,
		newEntry:     res.NewEntryPx,
		newIsoMargin: isoMarginAfter(pos, res.NewSize, px),
		leverage:     pos.leverage,
		isolated:     pos.isolated,
		balanceDelta: res.ClosedPnl.Sub(fee),
	}
}

func isoMarginAfter(pos *position, newSize, px decimal.Decimal) decimal.Decimal {
	if !pos.isolated {
		return zero
	}
	return IsolatedMarginAfter(pos.size, pos.isoMargin, newSize, px, pos.leverage)
}

func (e *Engine) applyFill(a *account, r fillRecord) {
	pos := a.position(r.fill.Coin, r.leverage)
	pos.size, pos.entryPx, pos.isoMargin = r.newSize, r.newEntry, r.newIsoMargin
	a.balance = a.balance.Add(r.balanceDelta)
}

func (e *Engine) account(ctx context.Context, id uuid.UUID) (*account, error) {
	if a, ok := e.accounts[id]; ok {
		return a, nil
	}
	st, err := accounts.Load(ctx, e.pool, id)
	if err != nil {
		return nil, err
	}
	a := &account{
		id:        id,
		runID:     st.RunID,
		balance:   st.Balance,
		positions: map[string]*position{},
		orders:    map[uuid.UUID]*accounts.Order{},
	}
	for _, p := range st.Positions {
		a.positions[p.Coin] = &position{
			size: p.Size, entryPx: p.EntryPx, leverage: p.Leverage,
			isolated: p.MarginMode == MarginIsolated, isoMargin: p.IsolatedMargin,
		}
	}
	for _, o := range st.OpenOrders {
		e.track(a, o)
	}
	e.accounts[id] = a
	return a, nil
}

// position returns the account's position on coin, creating a flat one if needed.
func (a *account) position(coin string, defaultLeverage int) *position {
	p, ok := a.positions[coin]
	if !ok {
		p = &position{leverage: defaultLeverage}
		a.positions[coin] = p
	}
	return p
}

func (e *Engine) defaultLeverage(t market.Ticker) int {
	return min(e.cfg.DefaultLeverage, t.MaxLeverage)
}

func (e *Engine) publish(accountID uuid.UUID, ev AccountEvent) {
	e.pub.Publish(AccountTopic(accountID), ev)
}

// scenario is an account's state on one coin after a proposed action.
type scenario struct {
	coin      string
	size      decimal.Decimal
	entryPx   decimal.Decimal
	leverage  int
	isolated  bool
	isoMargin decimal.Decimal
	balance   decimal.Decimal
	addOrder  *RestingOrder
	dropOrder uuid.UUID
}

// scenario starts a what-if from the position's current state.
func (p *position) scenario(coin string, balance decimal.Decimal) scenario {
	return scenario{
		coin: coin, size: p.size, entryPx: p.entryPx, leverage: p.leverage,
		isolated: p.isolated, isoMargin: p.isoMargin, balance: balance,
	}
}

// apply updates the what-if with a fill.
func (sc *scenario) apply(r fillRecord, balance decimal.Decimal) {
	sc.size, sc.entryPx, sc.isoMargin = r.newSize, r.newEntry, r.newIsoMargin
	sc.balance = balance.Add(r.balanceDelta)
}

// affordable reports whether the account can take the action described by sc. It must
// leave available margin non-negative, unless it doesn't add risk (closing a position on an
// underwater account is always allowed).
func (e *Engine) affordable(a *account, sc scenario) bool {
	before := Summarize(a.balance, e.coinStates(a, nil))
	after := Summarize(sc.balance, e.coinStates(a, &sc))
	if after.Available.GreaterThanOrEqual(zero) {
		return true
	}
	return after.MarginUsed.Add(after.OrderMargin).LessThanOrEqual(before.MarginUsed.Add(before.OrderMargin))
}

// coinStates builds the margin inputs for every coin the account has exposure on, with
// sc's changes applied when sc is not nil.
func (e *Engine) coinStates(a *account, sc *scenario) []CoinState {
	coins := map[string]*CoinState{}
	get := func(coin string) *CoinState {
		c, ok := coins[coin]
		if !ok {
			c = &CoinState{Coin: coin, Leverage: 1}
			if p, ok := a.positions[coin]; ok {
				c.Size, c.EntryPx, c.Leverage = p.size, p.entryPx, p.leverage
				c.Isolated, c.IsolatedMargin = p.isolated, p.isoMargin
			}
			e.mark(coin, c)
			coins[coin] = c
		}
		return c
	}
	for coin, p := range a.positions {
		if !p.size.IsZero() {
			get(coin)
		}
	}
	for _, o := range a.orders {
		if (sc != nil && o.ID == sc.dropOrder) || !reservesMargin(o) {
			continue
		}
		c := get(o.Coin)
		c.Orders = append(c.Orders, restingOrder(o))
	}
	if sc != nil {
		c := get(sc.coin)
		c.Size, c.EntryPx, c.Leverage = sc.size, sc.entryPx, sc.leverage
		c.Isolated, c.IsolatedMargin = sc.isolated, sc.isoMargin
		if sc.addOrder != nil {
			c.Orders = append(c.Orders, *sc.addOrder)
		}
	}
	out := make([]CoinState, 0, len(coins))
	for _, c := range coins {
		if c.Leverage < 1 {
			c.Leverage = 1
		}
		out = append(out, *c)
	}
	return out
}

// mark fills in a coin's mark price and maintenance rate from live data. Without a ticker
// it falls back to the entry price and the account's own leverage.
func (e *Engine) mark(coin string, c *CoinState) {
	markInto(e.md, coin, c)
}

func markInto(md MarketData, coin string, c *CoinState) {
	c.MarkPx = c.EntryPx
	c.MaintenanceRate = MaintenanceRate(c.Leverage)
	if t, ok := md.Ticker(coin); ok {
		if t.MarkPx.IsPositive() {
			c.MarkPx = t.MarkPx
		}
		c.MaintenanceRate = MaintenanceRate(t.MaxLeverage)
	}
}

// SummarizeAccount computes the margin summary of an account read from the database,
// using live marks. Any instance can call it.
func SummarizeAccount(a accounts.Account, md MarketData) Summary {
	coins := map[string]*CoinState{}
	get := func(coin string) *CoinState {
		c, ok := coins[coin]
		if !ok {
			c = &CoinState{Coin: coin, Leverage: 1}
			coins[coin] = c
		}
		return c
	}
	for _, p := range a.Positions {
		c := get(p.Coin)
		c.Size, c.EntryPx, c.Leverage = p.Size, p.EntryPx, max(p.Leverage, 1)
		c.Isolated, c.IsolatedMargin = p.MarginMode == MarginIsolated, p.IsolatedMargin
	}
	for _, o := range a.OpenOrders {
		if !reservesMargin(&o) {
			continue
		}
		c := get(o.Coin)
		c.Orders = append(c.Orders, restingOrder(&o))
	}
	out := make([]CoinState, 0, len(coins))
	for coin, c := range coins {
		markInto(md, coin, c)
		out = append(out, *c)
	}
	return Summarize(a.Balance, out)
}

func midPrice(b market.Book, t market.Ticker) decimal.Decimal {
	if len(b.Bids) > 0 && len(b.Asks) > 0 {
		return b.Bids[0].Px.Add(b.Asks[0].Px).Div(decimal.NewFromInt(2))
	}
	return t.MarkPx
}

func ptr[T any](v T) *T { return &v }
