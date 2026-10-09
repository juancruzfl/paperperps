package engine

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/juancruzfl/paperperps/server/internal/accounts"
	"github.com/juancruzfl/paperperps/server/internal/market"
)

// OrderRequest is a new order from a client.
type OrderRequest struct {
	Coin string `json:"coin"`
	Side string `json:"side"` // buy | sell
	// Type is market, limit, stop_market, stop_limit, take_profit_market or
	// take_profit_limit.
	Type string          `json:"type"`
	Sz   decimal.Decimal `json:"sz"`
	// Px is the limit price, for limit and *_limit orders.
	Px decimal.NullDecimal `json:"px"`
	// TriggerPx is the mark price that fires stop and take-profit orders.
	TriggerPx decimal.NullDecimal `json:"triggerPx"`
	// TIF applies to limit orders: gtc (default), ioc or alo (post-only).
	TIF string `json:"tif"`
	// ReduceOnly orders can only shrink a position, never grow or flip it.
	ReduceOnly bool `json:"reduceOnly"`
	// TakeProfit and StopLoss attach reduce-only trigger orders to an entry (market or limit)
	// order. They activate once the entry fills, and filling one cancels the other.
	TakeProfit decimal.NullDecimal `json:"takeProfit"`
	StopLoss   decimal.NullDecimal `json:"stopLoss"`
}

// OrderResult is the order as placed, the fill it got immediately (if any), and any TP/SL
// orders attached to it.
type OrderResult struct {
	Order    accounts.Order   `json:"order"`
	Fill     *accounts.Fill   `json:"fill"`
	Children []accounts.Order `json:"children,omitempty"`
}

// ModifyRequest changes an open order in place. Omitted fields are unchanged.
type ModifyRequest struct {
	Sz        decimal.NullDecimal `json:"sz"`
	Px        decimal.NullDecimal `json:"px"`
	TriggerPx decimal.NullDecimal `json:"triggerPx"`
}

// --- order kinds ---

// IsTrigger reports whether an order type waits for a trigger price.
func IsTrigger(typ string) bool {
	switch typ {
	case TypeStopMarket, TypeStopLimit, TypeTakeMarket, TypeTakeLimit:
		return true
	}
	return false
}

// limitPriced reports whether an order type executes with a limit price.
func limitPriced(typ string) bool {
	return typ == TypeLimit || typ == TypeStopLimit || typ == TypeTakeLimit
}

// Triggered reports whether a trigger order fires at mark. Stops fire when price moves
// against the position they protect (a stop sell when the price falls to the trigger);
// take-profits when it moves in favor (a take-profit sell when it rises to the trigger).
// Hyperliquid triggers on mark price.
func Triggered(typ string, buy bool, triggerPx, mark decimal.Decimal) bool {
	stop := strings.HasPrefix(typ, "stop")
	if stop == buy { // stop buy or take-profit sell: price rising
		return mark.GreaterThanOrEqual(triggerPx)
	}
	return mark.LessThanOrEqual(triggerPx)
}

// onBook reports whether an open order rests on the order book as a limit.
func onBook(o *accounts.Order) bool {
	return o.Status == StatusTriggered || (o.Status == StatusOpen && o.Type == TypeLimit)
}

// pendingTrigger reports whether an order is waiting for its trigger price.
func pendingTrigger(o *accounts.Order) bool {
	return o.Status == StatusOpen && IsTrigger(o.Type)
}

// reservesMargin reports whether an order holds margin: limits on the book that can grow
// the position. Untriggered and reduce-only orders don't.
func reservesMargin(o *accounts.Order) bool {
	return onBook(o) && !o.ReduceOnly
}

func restingOrder(o *accounts.Order) RestingOrder {
	return RestingOrder{Buy: o.Side == SideBuy, Remaining: o.Sz.Sub(o.FilledSz), Px: o.Px.Decimal}
}

// reducible is how much of the position an order on this side can close.
func reducible(p *position, buy bool) decimal.Decimal {
	if buy {
		return decimal.Max(zero, p.size.Neg())
	}
	return decimal.Max(zero, p.size)
}

// active reports whether a trigger order may fire: TP/SL attached to an entry order wait
// until the entry is no longer open.
func (a *account) active(o *accounts.Order) bool {
	if o.ParentOrderID == nil {
		return true
	}
	_, parentOpen := a.orders[*o.ParentOrderID]
	return !parentOpen
}

// --- placing ---

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

// liveMarket returns coin's ticker and book, or a rejection if trading on it isn't possible.
func (e *Engine) liveMarket(coin string) (market.Ticker, market.Book, error) {
	ticker, ok := e.md.Ticker(coin)
	if !ok {
		return market.Ticker{}, market.Book{}, reject("unknown_market", "%s is not a listed market", coin)
	}
	book, hasBook := e.md.Book(coin)
	if ticker.Stale || !hasBook {
		return market.Ticker{}, market.Book{}, reject("market_stale", "%s market data is not live right now; try again shortly", coin)
	}
	return ticker, book, nil
}

func (e *Engine) placeOrder(ctx context.Context, accountID uuid.UUID, req OrderRequest) (OrderResult, error) {
	if req.Side != SideBuy && req.Side != SideSell {
		return OrderResult{}, reject("invalid_side", "side must be buy or sell")
	}
	if req.Type != TypeMarket && !limitPriced(req.Type) && !IsTrigger(req.Type) {
		return OrderResult{}, reject("invalid_type", "unknown order type %q", req.Type)
	}
	ticker, book, err := e.liveMarket(req.Coin)
	if err != nil {
		return OrderResult{}, err
	}
	if err := ValidateSize(req.Sz, ticker.SzDecimals); err != nil {
		return OrderResult{}, reject("invalid_size", "size %s", err)
	}
	if limitPriced(req.Type) {
		if !req.Px.Valid {
			return OrderResult{}, reject("invalid_price", "%s orders need a limit price", req.Type)
		}
		if err := ValidatePrice(req.Px.Decimal, ticker.SzDecimals); err != nil {
			return OrderResult{}, reject("invalid_price", "price %s", err)
		}
	}
	if IsTrigger(req.Type) {
		if !req.TriggerPx.Valid {
			return OrderResult{}, reject("invalid_trigger", "%s orders need a trigger price", req.Type)
		}
		if err := ValidatePrice(req.TriggerPx.Decimal, ticker.SzDecimals); err != nil {
			return OrderResult{}, reject("invalid_trigger", "trigger price %s", err)
		}
	}
	tif := req.TIF
	switch {
	case req.Type == TypeMarket:
		tif = TIFIOC
	case req.Type != TypeLimit:
		if tif != "" {
			return OrderResult{}, reject("invalid_tif", "time in force only applies to limit orders")
		}
		tif = TIFGTC
		if req.Type == TypeStopMarket || req.Type == TypeTakeMarket {
			tif = TIFIOC
		}
	case tif == "":
		tif = TIFGTC
	case tif != TIFGTC && tif != TIFIOC && tif != TIFALO:
		return OrderResult{}, reject("invalid_tif", "tif must be gtc, ioc or alo")
	}
	attaching := req.TakeProfit.Valid || req.StopLoss.Valid
	if attaching && (IsTrigger(req.Type) || req.ReduceOnly) {
		return OrderResult{}, reject("invalid_tpsl", "TP/SL can only be attached to a market or limit entry order")
	}

	a, err := e.account(ctx, accountID)
	if err != nil {
		return OrderResult{}, err
	}
	pos := a.position(req.Coin, e.defaultLeverage(ticker))
	buy := req.Side == SideBuy
	if req.ReduceOnly && reducible(pos, buy).IsZero() {
		return OrderResult{}, reject("reduce_only", "a reduce-only %s needs an open %s position to reduce", req.Side, opposite(req.Side))
	}

	refPx := midPrice(book, ticker)
	switch {
	case limitPriced(req.Type):
		refPx = req.Px.Decimal
	case IsTrigger(req.Type):
		refPx = req.TriggerPx.Decimal
	}
	// Reduce-only orders may be small: closing a dust position must always be possible.
	if !req.ReduceOnly && req.Sz.Mul(refPx).LessThan(e.cfg.MinNotional) {
		return OrderResult{}, reject("min_notional", "order value must be at least $%s", e.cfg.MinNotional)
	}

	now := e.now()
	order := accounts.Order{
		ID:         accounts.NewID(),
		Coin:       req.Coin,
		Side:       req.Side,
		Type:       req.Type,
		Sz:         req.Sz,
		TIF:        &tif,
		ReduceOnly: req.ReduceOnly,
		Status:     StatusOpen,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if limitPriced(req.Type) {
		order.Px = req.Px
	}
	if IsTrigger(req.Type) {
		order.TriggerPx = req.TriggerPx
		if Triggered(req.Type, buy, req.TriggerPx.Decimal, ticker.MarkPx) {
			return OrderResult{}, reject("invalid_trigger", "the mark price (%s) has already reached that trigger; use a market or limit order", ticker.MarkPx)
		}
	}

	var children []accounts.Order
	if attaching {
		children, err = e.tpsl(order.ID, req.Coin, opposite(req.Side), req.Sz, req.TakeProfit, req.StopLoss, refPx, ticker, now)
		if err != nil {
			return OrderResult{}, err
		}
	}

	// Trigger orders just wait. They hold no margin; it's checked when they fire.
	if IsTrigger(req.Type) {
		err := pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
			if err := insertOrder(ctx, tx, a.id, a.runID, order); err != nil {
				return err
			}
			return upsertPosition(ctx, tx, a.id, a.runID, req.Coin, pos, now)
		})
		if err != nil {
			return OrderResult{}, err
		}
		e.track(a, order)
		e.publish(a.id, AccountEvent{Type: "order", Order: &order})
		return OrderResult{Order: order}, nil
	}

	rec, rests, rej := e.take(pos, &order, ticker, book, now)
	if rej != nil {
		return OrderResult{}, rej
	}
	if order.Status != StatusOpen && order.FilledSz.IsZero() {
		children = nil // the entry never filled, so there's nothing to protect
	}
	if !order.ReduceOnly && !e.affordableAfter(a, pos, &order, rec, rests) {
		return OrderResult{}, reject("insufficient_margin", "not enough available margin for this order")
	}

	err = pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
		if err := insertOrder(ctx, tx, a.id, a.runID, order); err != nil {
			return err
		}
		for _, c := range children {
			if err := insertOrder(ctx, tx, a.id, a.runID, c); err != nil {
				return err
			}
		}
		if rec != nil {
			return recordFill(ctx, tx, a.id, a.runID, *rec)
		}
		// Persist the leverage the resting order's margin is reserved at, so readers of the
		// database compute the same margin as the engine.
		return upsertPosition(ctx, tx, a.id, a.runID, req.Coin, pos, now)
	})
	if err != nil {
		return OrderResult{}, err
	}

	res := OrderResult{Order: order, Children: children}
	if rests {
		e.track(a, order)
	}
	for _, c := range children {
		e.track(a, c)
	}
	if rec != nil {
		e.applyFill(a, *rec)
		res.Fill = &rec.fill
		e.publish(a.id, AccountEvent{Type: "fill", Order: &order, Fill: &rec.fill})
		e.afterFill(ctx, a, &order)
	} else {
		e.publish(a.id, AccountEvent{Type: "order", Order: &order})
	}
	return res, nil
}

// tpsl builds reduce-only trigger-market orders that take profit and stop losses on a
// position of size sz, closed by side. parent (if not nil) is the entry order they wait
// for. refPx is the expected entry: a take-profit must be on the profitable side of it
// and a stop-loss on the losing side.
func (e *Engine) tpsl(parent uuid.UUID, coin, side string, sz decimal.Decimal, tp, sl decimal.NullDecimal,
	refPx decimal.Decimal, ticker market.Ticker, now time.Time) ([]accounts.Order, error) {
	closingLong := side == SideSell
	group := accounts.NewID()
	var out []accounts.Order
	add := func(typ string, trigger decimal.Decimal) error {
		if err := ValidatePrice(trigger, ticker.SzDecimals); err != nil {
			return reject("invalid_tpsl", "%s price %s", label(typ), err)
		}
		profitable := trigger.GreaterThan(refPx) == closingLong
		if (typ == TypeTakeMarket) != profitable {
			dir := "above"
			if (typ == TypeTakeMarket) != closingLong {
				dir = "below"
			}
			return reject("invalid_tpsl", "%s for a %s must be %s %s", label(typ), positionWord(closingLong), dir, refPx)
		}
		if Triggered(typ, side == SideBuy, trigger, ticker.MarkPx) {
			return reject("invalid_tpsl", "%s at %s would trigger immediately (mark %s)", label(typ), trigger, ticker.MarkPx)
		}
		o := accounts.Order{
			ID: accounts.NewID(), Coin: coin, Side: side, Type: typ, Sz: sz, TIF: ptr(TIFIOC),
			TriggerPx: decimal.NewNullDecimal(trigger), ReduceOnly: true, OcoGroup: &group,
			Status: StatusOpen, CreatedAt: now, UpdatedAt: now,
		}
		if parent != uuid.Nil {
			o.ParentOrderID = &parent
		}
		out = append(out, o)
		return nil
	}
	if tp.Valid {
		if err := add(TypeTakeMarket, tp.Decimal); err != nil {
			return nil, err
		}
	}
	if sl.Valid {
		if err := add(TypeStopMarket, sl.Decimal); err != nil {
			return nil, err
		}
	}
	if len(out) == 1 {
		out[0].OcoGroup = nil
	}
	return out, nil
}

func label(typ string) string {
	if typ == TypeTakeMarket {
		return "take-profit"
	}
	return "stop-loss"
}

func positionWord(long bool) string {
	if long {
		return "long"
	}
	return "short"
}

func opposite(side string) string {
	if side == SideBuy {
		return SideSell
	}
	return SideBuy
}

// take executes o against the current book: market-type orders as IOC within the slippage
// bound of mid, limit-type up to their price. It updates o's filled size, status and
// reason, and returns the fill (nil if nothing filled) and whether the rest stays on the
// book. A rejection means the order can't execute at all.
func (e *Engine) take(pos *position, o *accounts.Order, ticker market.Ticker, book market.Book, now time.Time) (*fillRecord, bool, *RejectError) {
	buy := o.Side == SideBuy
	want := o.Sz.Sub(o.FilledSz)
	clamped := false
	if o.ReduceOnly {
		if r := reducible(pos, buy); r.LessThan(want) {
			want, clamped = r, true
		}
		if want.IsZero() {
			return nil, false, reject("reduce_only", "the position it would reduce is already closed")
		}
	}

	mid := midPrice(book, ticker)
	marketType := !limitPriced(o.Type)
	limitPx := o.Px.Decimal
	if marketType {
		limitPx = mid.Mul(one.Add(e.cfg.MaxSlippage))
		if !buy {
			limitPx = mid.Mul(one.Sub(e.cfg.MaxSlippage))
		}
	}
	levels := book.Bids
	if buy {
		levels = book.Asks
	}
	filled, notional := WalkBook(levels, buy, want, limitPx)
	tif := TIFGTC
	if o.TIF != nil {
		tif = *o.TIF
	}

	switch {
	case !marketType && tif == TIFALO && filled.IsPositive():
		return nil, false, reject("would_cross", "post-only order would have filled immediately; it was not placed")
	case marketType && filled.IsZero():
		return nil, false, reject("no_liquidity", "no liquidity within %s%% of the mid price", e.cfg.MaxSlippage.Shift(2))
	case tif == TIFIOC && filled.IsZero():
		return nil, false, reject("ioc_unfilled", "IOC order found nothing to fill at that price")
	}

	o.FilledSz = o.FilledSz.Add(filled)
	o.UpdatedAt = now
	left := o.Sz.Sub(o.FilledSz)
	rests := false
	switch {
	case !left.IsPositive():
		o.Status = StatusFilled
	case clamped && filled.Equal(want):
		o.Status = StatusFilled
		o.StatusReason = ptr("reduce-only: size reduced to the open position")
	case marketType:
		o.Status = StatusFilled
		o.StatusReason = ptr(fmt.Sprintf("partially filled: no more liquidity within %s%% of mid", e.cfg.MaxSlippage.Shift(2)))
	case tif == TIFIOC:
		o.Status = StatusFilled
		o.StatusReason = ptr("IOC: unfilled remainder cancelled")
	default:
		rests = true
		if IsTrigger(o.Type) {
			o.Status = StatusTriggered
		}
	}

	if filled.IsZero() {
		return nil, rests, nil
	}
	// The fee is charged on the exact notional walked, not on size × average price.
	fee := notional.Mul(e.cfg.TakerFeeRate).Round(usdDecimals)
	r := e.newFill(pos, &o.ID, o.Coin, buy, filled, notional.Div(filled), fee, "taker", now)
	return &r, rests, nil
}

// affordableAfter checks margin once o has executed as rec (may be nil) and, if rests,
// left its remainder on the book.
func (e *Engine) affordableAfter(a *account, pos *position, o *accounts.Order, rec *fillRecord, rests bool) bool {
	sc := pos.scenario(o.Coin, a.balance)
	sc.dropOrder = o.ID
	if rec != nil {
		sc.apply(*rec, a.balance)
	}
	if rests && !o.ReduceOnly {
		ro := restingOrder(o)
		sc.addOrder = &ro
	}
	return e.affordable(a, sc)
}

// track adds an open order to memory and the right index.
func (e *Engine) track(a *account, o accounts.Order) {
	if o.Status != StatusOpen && o.Status != StatusTriggered {
		return
	}
	stored := o
	a.orders[o.ID] = &stored
	e.owners[o.ID] = a
	switch {
	case onBook(&stored):
		addTo(e.resting, &stored)
	case pendingTrigger(&stored):
		addTo(e.triggers, &stored)
	}
}

func addTo(index map[string]map[uuid.UUID]*accounts.Order, o *accounts.Order) {
	if index[o.Coin] == nil {
		index[o.Coin] = map[uuid.UUID]*accounts.Order{}
	}
	index[o.Coin][o.ID] = o
}

func (e *Engine) unindex(o *accounts.Order) {
	delete(e.resting[o.Coin], o.ID)
	delete(e.triggers[o.Coin], o.ID)
	delete(e.owners, o.ID)
}

// forget removes a no-longer-open order from memory.
func (e *Engine) forget(a *account, o *accounts.Order) {
	delete(a.orders, o.ID)
	e.unindex(o)
}

// --- cancelling ---

// CancelOrder cancels one of the account's open orders.
func (e *Engine) CancelOrder(ctx context.Context, accountID, orderID uuid.UUID) error {
	return e.do(ctx, func(ctx context.Context) error { return e.cancelOrder(ctx, accountID, orderID) })
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
	return e.closeOrders(ctx, a, []*accounts.Order{o}, "cancelled by user")
}

// closeOrders cancels open orders with a reason, in one transaction. TP/SL attached to an
// entry that is cancelled before any of it filled go with it.
func (e *Engine) closeOrders(ctx context.Context, a *account, orders []*accounts.Order, reason string) error {
	byID := map[uuid.UUID]*accounts.Order{}
	for _, o := range orders {
		byID[o.ID] = o
	}
	for _, o := range orders {
		if !o.FilledSz.IsZero() {
			continue
		}
		for _, c := range a.orders {
			if c.ParentOrderID != nil && *c.ParentOrderID == o.ID {
				byID[c.ID] = c
			}
		}
	}
	now := e.now()
	updated := make([]accounts.Order, 0, len(byID))
	for _, id := range slices.SortedFunc(maps.Keys(byID), compareIDs) {
		u := *byID[id]
		u.Status = StatusCancelled
		u.StatusReason = ptr(reason)
		u.UpdatedAt = now
		updated = append(updated, u)
	}
	err := pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
		for _, u := range updated {
			if err := updateOrder(ctx, tx, u); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := range updated {
		e.forget(a, byID[updated[i].ID])
		e.publish(a.id, AccountEvent{Type: "order", Order: &updated[i]})
	}
	return nil
}

// afterFill tidies up once o has (partly) filled: the other side of its TP/SL pair is
// cancelled, and when the position is now flat, reduce-only orders that can no longer do
// anything are cancelled.
func (e *Engine) afterFill(ctx context.Context, a *account, o *accounts.Order) {
	var cancel []*accounts.Order
	if o.OcoGroup != nil {
		for _, other := range a.orders {
			if other.ID != o.ID && other.OcoGroup != nil && *other.OcoGroup == *o.OcoGroup {
				cancel = append(cancel, other)
			}
		}
	}
	if err := e.closeOrders(ctx, a, cancel, "cancelled: the other side of its TP/SL filled"); err != nil {
		slog.Error("engine: cancel OCO sibling", "order", o.ID, "err", err)
	}
	if p := a.positions[o.Coin]; p == nil || p.size.IsZero() {
		cancel = cancel[:0]
		for _, other := range a.orders {
			if other.Coin == o.Coin && other.ReduceOnly && a.active(other) {
				cancel = append(cancel, other)
			}
		}
		if err := e.closeOrders(ctx, a, cancel, "cancelled: position closed"); err != nil {
			slog.Error("engine: cancel reduce-only orders", "coin", o.Coin, "err", err)
		}
	}
}

// --- market events: resting fills and triggers ---

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
				// Trades from before the order rested (e.g. replayed after a reconnect)
				// don't count.
				if t.Time <= o.UpdatedAt.UnixMilli() {
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
		if _, still := e.owners[o.ID]; !still {
			continue // cancelled by an earlier fill's OCO or reduce-only cleanup
		}
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

	updated := *o
	updated.Status = StatusFilled
	updated.UpdatedAt = now
	if o.ReduceOnly {
		r := reducible(pos, buy)
		if r.IsZero() {
			return e.closeOrders(ctx, a, []*accounts.Order{o}, "cancelled: reduce-only and the position is already closed")
		}
		if r.LessThan(sz) {
			sz = r
			updated.StatusReason = ptr("reduce-only: size reduced to the open position")
		}
	}
	updated.FilledSz = o.FilledSz.Add(sz)

	rec := e.newFill(pos, &o.ID, o.Coin, buy, sz, px, Fee(sz, px, e.cfg.MakerFeeRate), "maker", now)
	if !o.ReduceOnly && !e.affordableAfter(a, pos, &updated, &rec, false) {
		return e.closeOrders(ctx, a, []*accounts.Order{o}, "cancelled: insufficient margin when it would have filled")
	}

	err := pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
		if err := updateOrder(ctx, tx, updated); err != nil {
			return err
		}
		return recordFill(ctx, tx, a.id, a.runID, rec)
	})
	if err != nil {
		return err
	}
	e.forget(a, o)
	e.applyFill(a, rec)
	e.publish(a.id, AccountEvent{Type: "fill", Order: &updated, Fill: &rec.fill})
	e.afterFill(ctx, a, &updated)
	return nil
}

// checkTriggers fires trigger orders whose trigger price the mark has reached.
func (e *Engine) checkTriggers(ctx context.Context) {
	for _, coin := range slices.Sorted(maps.Keys(e.triggers)) {
		orders := e.triggers[coin]
		if len(orders) == 0 {
			continue
		}
		ticker, book, err := e.liveMarket(coin)
		if err != nil {
			continue // never trigger on stale prices
		}
		var fire []*accounts.Order
		for _, o := range orders {
			a := e.owners[o.ID]
			if a != nil && a.active(o) && Triggered(o.Type, o.Side == SideBuy, o.TriggerPx.Decimal, ticker.MarkPx) {
				fire = append(fire, o)
			}
		}
		slices.SortFunc(fire, func(a, b *accounts.Order) int { return a.CreatedAt.Compare(b.CreatedAt) })
		for _, o := range fire {
			if _, still := e.owners[o.ID]; !still {
				continue // cancelled as the other side of a TP/SL that just filled
			}
			if err := e.fireTrigger(ctx, o, ticker, book); err != nil {
				slog.Error("engine: fire trigger", "order", o.ID, "err", err)
			}
		}
	}
}

// fireTrigger executes a trigger order: market types at market, limit types up to their
// limit price, resting the remainder.
func (e *Engine) fireTrigger(ctx context.Context, o *accounts.Order, ticker market.Ticker, book market.Book) error {
	a := e.owners[o.ID]
	now := e.now()
	pos := a.position(o.Coin, e.defaultLeverage(ticker))
	updated := *o
	rec, rests, rej := e.take(pos, &updated, ticker, book, now)
	if rej != nil {
		return e.closeOrders(ctx, a, []*accounts.Order{o}, "triggered but cancelled: "+rej.Message)
	}
	if !o.ReduceOnly && !e.affordableAfter(a, pos, &updated, rec, rests) {
		return e.closeOrders(ctx, a, []*accounts.Order{o}, "triggered but cancelled: insufficient margin")
	}
	err := pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
		if err := updateOrder(ctx, tx, updated); err != nil {
			return err
		}
		if rec != nil {
			return recordFill(ctx, tx, a.id, a.runID, *rec)
		}
		return nil
	})
	if err != nil {
		return err
	}
	e.forget(a, o)
	if rests {
		e.track(a, updated)
	}
	slog.Info("trigger order fired", "order", o.ID, "type", o.Type, "coin", o.Coin, "trigger", o.TriggerPx.Decimal, "mark", ticker.MarkPx)
	if rec != nil {
		e.applyFill(a, *rec)
		e.publish(a.id, AccountEvent{Type: "fill", Order: &updated, Fill: &rec.fill})
		e.afterFill(ctx, a, &updated)
	} else {
		e.publish(a.id, AccountEvent{Type: "order", Order: &updated})
	}
	return nil
}

// --- modify and position TP/SL ---

// ModifyOrder changes an open order's size, limit price or trigger price in place.
func (e *Engine) ModifyOrder(ctx context.Context, accountID, orderID uuid.UUID, req ModifyRequest) (accounts.Order, error) {
	var out accounts.Order
	err := e.do(ctx, func(ctx context.Context) error {
		var err error
		out, err = e.modifyOrder(ctx, accountID, orderID, req)
		return err
	})
	return out, err
}

func (e *Engine) modifyOrder(ctx context.Context, accountID, orderID uuid.UUID, req ModifyRequest) (accounts.Order, error) {
	a, err := e.account(ctx, accountID)
	if err != nil {
		return accounts.Order{}, err
	}
	o, ok := a.orders[orderID]
	if !ok {
		return accounts.Order{}, reject("not_found", "no open order with that id")
	}
	ticker, book, err := e.liveMarket(o.Coin)
	if err != nil {
		return accounts.Order{}, err
	}
	if !req.Sz.Valid && !req.Px.Valid && !req.TriggerPx.Valid {
		return accounts.Order{}, reject("nothing_to_modify", "give a new size, price or trigger price")
	}
	buy := o.Side == SideBuy
	u := *o
	u.UpdatedAt = e.now()
	if req.Sz.Valid {
		if err := ValidateSize(req.Sz.Decimal, ticker.SzDecimals); err != nil {
			return accounts.Order{}, reject("invalid_size", "size %s", err)
		}
		if !req.Sz.Decimal.GreaterThan(o.FilledSz) {
			return accounts.Order{}, reject("invalid_size", "size must be more than the %s already filled", o.FilledSz)
		}
		u.Sz = req.Sz.Decimal
	}
	if req.Px.Valid {
		if !limitPriced(o.Type) {
			return accounts.Order{}, reject("invalid_price", "%s orders have no limit price", o.Type)
		}
		if err := ValidatePrice(req.Px.Decimal, ticker.SzDecimals); err != nil {
			return accounts.Order{}, reject("invalid_price", "price %s", err)
		}
		u.Px = req.Px
	}
	if req.TriggerPx.Valid {
		if !pendingTrigger(o) {
			return accounts.Order{}, reject("invalid_trigger", "only untriggered stop and take-profit orders have a trigger price")
		}
		if err := ValidatePrice(req.TriggerPx.Decimal, ticker.SzDecimals); err != nil {
			return accounts.Order{}, reject("invalid_trigger", "trigger price %s", err)
		}
		if Triggered(o.Type, buy, req.TriggerPx.Decimal, ticker.MarkPx) {
			return accounts.Order{}, reject("invalid_trigger", "the mark price (%s) has already reached that trigger", ticker.MarkPx)
		}
		u.TriggerPx = req.TriggerPx
	}
	if onBook(&u) {
		// A modified limit keeps resting; one that would now cross is a new order.
		if (buy && len(book.Asks) > 0 && u.Px.Decimal.GreaterThanOrEqual(book.Asks[0].Px)) ||
			(!buy && len(book.Bids) > 0 && u.Px.Decimal.LessThanOrEqual(book.Bids[0].Px)) {
			return accounts.Order{}, reject("would_cross", "that price would fill immediately; cancel and place a market or limit order instead")
		}
		if !u.ReduceOnly {
			if u.Sz.Sub(u.FilledSz).Mul(u.Px.Decimal).LessThan(e.cfg.MinNotional) {
				return accounts.Order{}, reject("min_notional", "order value must be at least $%s", e.cfg.MinNotional)
			}
			pos := a.position(o.Coin, e.defaultLeverage(ticker))
			if !e.affordableAfter(a, pos, &u, nil, true) {
				return accounts.Order{}, reject("insufficient_margin", "not enough available margin for this order")
			}
		}
	}
	if err := pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error { return updateOrder(ctx, tx, u) }); err != nil {
		return accounts.Order{}, err
	}
	*o = u // the indexes point at o
	e.publish(a.id, AccountEvent{Type: "order", Order: &u})
	return u, nil
}

// SetPositionTPSL replaces the take-profit and stop-loss on the account's open position on
// coin. Either may be omitted; omitting both removes them.
func (e *Engine) SetPositionTPSL(ctx context.Context, accountID uuid.UUID, coin string, tp, sl decimal.NullDecimal) ([]accounts.Order, error) {
	var out []accounts.Order
	err := e.do(ctx, func(ctx context.Context) error {
		var err error
		out, err = e.setPositionTPSL(ctx, accountID, coin, tp, sl)
		return err
	})
	return out, err
}

func (e *Engine) setPositionTPSL(ctx context.Context, accountID uuid.UUID, coin string, tp, sl decimal.NullDecimal) ([]accounts.Order, error) {
	ticker, _, err := e.liveMarket(coin)
	if err != nil {
		return nil, err
	}
	a, err := e.account(ctx, accountID)
	if err != nil {
		return nil, err
	}
	p, ok := a.positions[coin]
	if !ok || p.size.IsZero() {
		return nil, reject("no_position", "you have no open %s position", coin)
	}
	side := SideSell
	if p.size.IsNegative() {
		side = SideBuy
	}
	// The mark, not the entry, is the reference: a stop-loss above entry (locking in profit)
	// is fine as long as the price hasn't already passed it.
	orders, err := e.tpsl(uuid.Nil, coin, side, p.size.Abs(), tp, sl, ticker.MarkPx, ticker, e.now())
	if err != nil {
		return nil, err
	}

	// Everything currently protecting the position is replaced, including TP/SL that were
	// attached to an entry order which has since filled.
	var replace []*accounts.Order
	for _, o := range a.orders {
		if o.Coin == coin && o.ReduceOnly && pendingTrigger(o) && !limitPriced(o.Type) && a.active(o) {
			replace = append(replace, o)
		}
	}
	if err := e.closeOrders(ctx, a, replace, "replaced by new position TP/SL"); err != nil {
		return nil, err
	}
	err = pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
		for _, o := range orders {
			if err := insertOrder(ctx, tx, a.id, a.runID, o); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, o := range orders {
		e.track(a, o)
		e.publish(a.id, AccountEvent{Type: "order", Order: &o})
	}
	return orders, nil
}
