package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
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
}

const (
	SideBuy  = "buy"
	SideSell = "sell"

	TypeMarket = "market"
	TypeLimit  = "limit"

	StatusOpen      = "open"
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

// OrderRequest is a new order from a client.
type OrderRequest struct {
	Coin string              `json:"coin"`
	Side string              `json:"side"` // buy | sell
	Type string              `json:"type"` // market | limit
	Sz   decimal.Decimal     `json:"sz"`
	Px   decimal.NullDecimal `json:"px"` // limit orders only
}

// OrderResult is the order as placed and the fill it got immediately, if any.
type OrderResult struct {
	Order accounts.Order `json:"order"`
	Fill  *accounts.Fill `json:"fill"`
}

// AccountEvent is published on AccountTopic when an account's orders or fills change.
type AccountEvent struct {
	Type  string          `json:"type"` // order | fill | leverage
	Order *accounts.Order `json:"order,omitempty"`
	Fill  *accounts.Fill  `json:"fill,omitempty"`
}

// --- in-memory state, owned by the Run goroutine ---

type position struct {
	size     decimal.Decimal
	entryPx  decimal.Decimal
	leverage int
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
	resting  map[string]map[uuid.UUID]*accounts.Order // coin → open limit orders
	owners   map[uuid.UUID]*account                   // open order id → its account
}

func New(pool *pgxpool.Pool, md MarketData, pub Publisher, cfg Config) *Engine {
	return &Engine{
		pool:   pool,
		md:     md,
		pub:    pub,
		cfg:    cfg,
		now:    func() time.Time { return time.Now().UTC() },
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
	e.owners = map[uuid.UUID]*account{}

	rows, err := e.pool.Query(ctx, `SELECT DISTINCT account_id FROM orders WHERE status = 'open'`)
	if err != nil {
		return fmt.Errorf("load open orders: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return fmt.Errorf("load open orders: %w", err)
	}
	for _, id := range ids {
		if _, err := e.account(ctx, id); err != nil {
			return err
		}
	}
	slog.Info("engine started", "accounts_with_open_orders", len(ids))

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

// PlaceOrder validates, executes and records a new order.
func (e *Engine) PlaceOrder(ctx context.Context, accountID uuid.UUID, req OrderRequest) (OrderResult, error) {
	var res OrderResult
	err := e.do(ctx, func(ctx context.Context) error {
		var err error
		res, err = e.placeOrder(ctx, accountID, req)
		return err
	})
	return res, err
}

// CancelOrder cancels one of the account's open orders.
func (e *Engine) CancelOrder(ctx context.Context, accountID, orderID uuid.UUID) error {
	return e.do(ctx, func(ctx context.Context) error { return e.cancelOrder(ctx, accountID, orderID) })
}

// SetLeverage changes the account's leverage on coin.
func (e *Engine) SetLeverage(ctx context.Context, accountID uuid.UUID, coin string, leverage int) error {
	return e.do(ctx, func(ctx context.Context) error { return e.setLeverage(ctx, accountID, coin, leverage) })
}

func (e *Engine) placeOrder(ctx context.Context, accountID uuid.UUID, req OrderRequest) (OrderResult, error) {
	if req.Side != SideBuy && req.Side != SideSell {
		return OrderResult{}, reject("invalid_side", "side must be buy or sell")
	}
	if req.Type != TypeMarket && req.Type != TypeLimit {
		return OrderResult{}, reject("invalid_type", "type must be market or limit")
	}
	ticker, ok := e.md.Ticker(req.Coin)
	if !ok {
		return OrderResult{}, reject("unknown_market", "%s is not a listed market", req.Coin)
	}
	book, hasBook := e.md.Book(req.Coin)
	if ticker.Stale || !hasBook {
		return OrderResult{}, reject("market_stale", "%s market data is not live right now; try again shortly", req.Coin)
	}
	if err := ValidateSize(req.Sz, ticker.SzDecimals); err != nil {
		return OrderResult{}, reject("invalid_size", "size %s", err)
	}
	if req.Type == TypeLimit {
		if !req.Px.Valid {
			return OrderResult{}, reject("invalid_price", "limit orders need a price")
		}
		if err := ValidatePrice(req.Px.Decimal, ticker.SzDecimals); err != nil {
			return OrderResult{}, reject("invalid_price", "price %s", err)
		}
	}

	a, err := e.account(ctx, accountID)
	if err != nil {
		return OrderResult{}, err
	}

	buy := req.Side == SideBuy
	mid := midPrice(book, ticker)
	refPx := mid
	if req.Type == TypeLimit {
		refPx = req.Px.Decimal
	}
	if req.Sz.Mul(refPx).LessThan(e.cfg.MinNotional) {
		return OrderResult{}, reject("min_notional", "order value must be at least $%s", e.cfg.MinNotional)
	}

	// Take liquidity first: market orders up to the slippage bound, limit orders up to their
	// price. Whatever a limit order can't fill rests on the book.
	var limitPx decimal.Decimal
	if req.Type == TypeLimit {
		limitPx = req.Px.Decimal
	} else if buy {
		limitPx = mid.Mul(one.Add(e.cfg.MaxSlippage))
	} else {
		limitPx = mid.Mul(one.Sub(e.cfg.MaxSlippage))
	}
	levels := book.Bids
	if buy {
		levels = book.Asks
	}
	filled, notional := WalkBook(levels, buy, req.Sz, limitPx)
	if req.Type == TypeMarket && filled.IsZero() {
		return OrderResult{}, reject("no_liquidity", "no liquidity within %s%% of the mid price", e.cfg.MaxSlippage.Shift(2))
	}
	remaining := req.Sz.Sub(filled)

	now := e.now()
	order := accounts.Order{
		ID:        accounts.NewID(),
		Coin:      req.Coin,
		Side:      req.Side,
		Type:      req.Type,
		Sz:        req.Sz,
		FilledSz:  filled,
		Status:    StatusFilled,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if req.Type == TypeLimit {
		order.Px = req.Px
		order.TIF = ptr("gtc")
		if remaining.IsPositive() {
			order.Status = StatusOpen
		}
	} else {
		order.TIF = ptr("ioc")
		if remaining.IsPositive() {
			order.StatusReason = ptr(fmt.Sprintf("partially filled: no more liquidity within %s%% of mid", e.cfg.MaxSlippage.Shift(2)))
		}
	}

	pos := a.position(req.Coin, e.defaultLeverage(ticker))
	var rec *fillRecord
	sc := scenario{coin: req.Coin, size: pos.size, entryPx: pos.entryPx, leverage: pos.leverage, balance: a.balance}
	if filled.IsPositive() {
		// The fee is charged on the exact notional walked, not on size × average price.
		fee := notional.Mul(e.cfg.TakerFeeRate).Round(usdDecimals)
		r := e.newFill(pos, &order.ID, req.Coin, buy, filled, notional.Div(filled), fee, "taker", now)
		rec = &r
		sc.size, sc.entryPx, sc.balance = r.newSize, r.newEntry, a.balance.Add(r.balanceDelta)
	}
	if order.Status == StatusOpen {
		sc.addOrder = &RestingOrder{Buy: buy, Remaining: remaining, Px: req.Px.Decimal}
	}
	if !e.affordable(a, sc) {
		return OrderResult{}, reject("insufficient_margin", "not enough available margin for this order")
	}

	err = pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
		if err := insertOrder(ctx, tx, a.id, a.runID, order); err != nil {
			return err
		}
		if rec != nil {
			return recordFill(ctx, tx, a.id, a.runID, *rec)
		}
		// Persist the leverage the resting order's margin is reserved at, so readers of the
		// database compute the same margin as the engine.
		return upsertPosition(ctx, tx, a.id, a.runID, req.Coin, pos.size, pos.entryPx, pos.leverage, now)
	})
	if err != nil {
		return OrderResult{}, err
	}

	res := OrderResult{Order: order}
	if rec != nil {
		e.applyFill(a, *rec)
		res.Fill = &rec.fill
		e.publish(a.id, AccountEvent{Type: "fill", Order: &order, Fill: &rec.fill})
	} else {
		e.publish(a.id, AccountEvent{Type: "order", Order: &order})
	}
	if order.Status == StatusOpen {
		o := order
		a.orders[o.ID] = &o
		e.index(a, &o)
	}
	return res, nil
}

func (e *Engine) cancelOrder(ctx context.Context, accountID, orderID uuid.UUID) error {
	a, err := e.account(ctx, accountID)
	if err != nil {
		return err
	}
	o, ok := a.orders[orderID]
	if !ok {
		return reject("not_found", "no open order with that id")
	}
	return e.closeOrder(ctx, a, o, "cancelled by user")
}

// closeOrder cancels an open order with a reason.
func (e *Engine) closeOrder(ctx context.Context, a *account, o *accounts.Order, reason string) error {
	updated := *o
	updated.Status = StatusCancelled
	updated.StatusReason = ptr(reason)
	updated.UpdatedAt = e.now()
	if err := pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error { return updateOrder(ctx, tx, updated) }); err != nil {
		return err
	}
	delete(a.orders, o.ID)
	e.unindex(o)
	e.publish(a.id, AccountEvent{Type: "order", Order: &updated})
	return nil
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
	sc := scenario{coin: coin, size: pos.size, entryPx: pos.entryPx, leverage: leverage, balance: a.balance}
	if !e.affordable(a, sc) {
		return reject("insufficient_margin", "not enough available margin to lower leverage")
	}
	err = pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
		return upsertPosition(ctx, tx, a.id, a.runID, coin, pos.size, pos.entryPx, leverage, e.now())
	})
	if err != nil {
		return err
	}
	pos.leverage = leverage
	e.publish(a.id, AccountEvent{Type: "leverage"})
	return nil
}

// --- resting order matching ---

// handleEvent fills resting orders that the market traded through. A buy resting at P fills
// when a trade prints below P or the best ask drops below P; queue position is unknown, so a
// trade exactly at P doesn't count (docs/plan.md §4.3).
func (e *Engine) handleEvent(ctx context.Context, ev event) {
	orders := e.resting[ev.coin]
	if len(orders) == 0 {
		return
	}
	ticker, ok := e.md.Ticker(ev.coin)
	if !ok || ticker.Stale {
		return
	}

	var bestBid, bestAsk decimal.Decimal
	if ev.trades == nil {
		book, ok := e.md.Book(ev.coin)
		if !ok {
			return
		}
		if len(book.Bids) > 0 {
			bestBid = book.Bids[0].Px
		}
		if len(book.Asks) > 0 {
			bestAsk = book.Asks[0].Px
		}
	}

	var matched []*accounts.Order
	for _, o := range orders {
		px := o.Px.Decimal
		buy := o.Side == SideBuy
		crossed := false
		if ev.trades != nil {
			for _, t := range ev.trades {
				// Trades from before the order existed (e.g. replayed after a reconnect)
				// don't count.
				if t.Time <= o.CreatedAt.UnixMilli() {
					continue
				}
				if (buy && t.Px.LessThan(px)) || (!buy && t.Px.GreaterThan(px)) {
					crossed = true
					break
				}
			}
		} else {
			crossed = (buy && !bestAsk.IsZero() && bestAsk.LessThan(px)) ||
				(!buy && !bestBid.IsZero() && bestBid.GreaterThan(px))
		}
		if crossed {
			matched = append(matched, o)
		}
	}
	slices.SortFunc(matched, func(a, b *accounts.Order) int { return a.CreatedAt.Compare(b.CreatedAt) })
	for _, o := range matched {
		if err := e.fillResting(ctx, o, ticker); err != nil {
			slog.Error("engine: fill resting order", "order", o.ID, "err", err)
		}
	}
}

// fillResting fills the rest of a resting limit order at its limit price, as a maker.
func (e *Engine) fillResting(ctx context.Context, o *accounts.Order, ticker market.Ticker) error {
	a := e.owners[o.ID]
	if a == nil {
		return fmt.Errorf("order %s has no loaded account", o.ID)
	}
	buy := o.Side == SideBuy
	sz := o.Sz.Sub(o.FilledSz)
	px := o.Px.Decimal
	now := e.now()
	pos := a.position(o.Coin, e.defaultLeverage(ticker))

	r := e.newFill(pos, &o.ID, o.Coin, buy, sz, px, Fee(sz, px, e.cfg.MakerFeeRate), "maker", now)
	sc := scenario{
		coin: o.Coin, size: r.newSize, entryPx: r.newEntry, leverage: pos.leverage,
		balance: a.balance.Add(r.balanceDelta), dropOrder: o.ID,
	}
	if !e.affordable(a, sc) {
		return e.closeOrder(ctx, a, o, "cancelled: insufficient margin when it would have filled")
	}

	updated := *o
	updated.FilledSz = o.Sz
	updated.Status = StatusFilled
	updated.UpdatedAt = now
	err := pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
		if err := updateOrder(ctx, tx, updated); err != nil {
			return err
		}
		return recordFill(ctx, tx, a.id, a.runID, r)
	})
	if err != nil {
		return err
	}
	delete(a.orders, o.ID)
	e.unindex(o)
	e.applyFill(a, r)
	e.publish(a.id, AccountEvent{Type: "fill", Order: &updated, Fill: &r.fill})
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
		leverage:     pos.leverage,
		balanceDelta: res.ClosedPnl.Sub(fee),
	}
}

func (e *Engine) applyFill(a *account, r fillRecord) {
	pos := a.position(r.fill.Coin, r.leverage)
	pos.size, pos.entryPx = r.newSize, r.newEntry
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
		a.positions[p.Coin] = &position{size: p.Size, entryPx: p.EntryPx, leverage: p.Leverage}
	}
	for _, o := range st.OpenOrders {
		a.orders[o.ID] = &o
		e.index(a, &o)
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

func (e *Engine) index(a *account, o *accounts.Order) {
	if e.resting[o.Coin] == nil {
		e.resting[o.Coin] = map[uuid.UUID]*accounts.Order{}
	}
	e.resting[o.Coin][o.ID] = o
	e.owners[o.ID] = a
}

func (e *Engine) unindex(o *accounts.Order) {
	delete(e.resting[o.Coin], o.ID)
	delete(e.owners, o.ID)
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
	balance   decimal.Decimal
	addOrder  *RestingOrder
	dropOrder uuid.UUID
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
			c = &CoinState{Leverage: 1}
			if p, ok := a.positions[coin]; ok {
				c.Size, c.EntryPx, c.Leverage = p.size, p.entryPx, p.leverage
			}
			c.MarkPx = c.EntryPx
			if t, ok := e.md.Ticker(coin); ok && t.MarkPx.IsPositive() {
				c.MarkPx = t.MarkPx
			}
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
		if sc != nil && o.ID == sc.dropOrder {
			continue
		}
		c := get(o.Coin)
		c.Orders = append(c.Orders, RestingOrder{Buy: o.Side == SideBuy, Remaining: o.Sz.Sub(o.FilledSz), Px: o.Px.Decimal})
	}
	if sc != nil {
		c := get(sc.coin)
		c.Size, c.EntryPx, c.Leverage = sc.size, sc.entryPx, sc.leverage
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

// SummarizeAccount computes the margin summary of an account read from the database,
// using live marks. Any instance can call it.
func SummarizeAccount(a accounts.Account, md MarketData) Summary {
	coins := map[string]*CoinState{}
	get := func(coin string) *CoinState {
		c, ok := coins[coin]
		if !ok {
			c = &CoinState{Leverage: 1}
			coins[coin] = c
		}
		return c
	}
	for _, p := range a.Positions {
		c := get(p.Coin)
		c.Size, c.EntryPx, c.Leverage = p.Size, p.EntryPx, max(p.Leverage, 1)
	}
	for _, o := range a.OpenOrders {
		c := get(o.Coin)
		c.Orders = append(c.Orders, RestingOrder{Buy: o.Side == SideBuy, Remaining: o.Sz.Sub(o.FilledSz), Px: o.Px.Decimal})
	}
	out := make([]CoinState, 0, len(coins))
	for coin, c := range coins {
		c.MarkPx = c.EntryPx
		if t, ok := md.Ticker(coin); ok && t.MarkPx.IsPositive() {
			c.MarkPx = t.MarkPx
		}
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
