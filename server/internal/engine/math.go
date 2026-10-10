// Package engine simulates order execution and cross margin against live market data.
//
// math.go holds the pure trading math. The web client mirrors it in web/src/lib/margin.ts;
// keep the two in sync.
package engine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/juancruzfl/paperperps/server/internal/hyperliquid"
)

// usdDecimals is USDC's precision. Fees and realized PnL are rounded to it.
const usdDecimals = 6

var (
	zero = decimal.Zero
	one  = decimal.NewFromInt(1)
)

// FillResult is how one fill changes an account's position on a coin.
type FillResult struct {
	NewSize    decimal.Decimal // signed
	NewEntryPx decimal.Decimal // zero when flat
	ClosedPnl  decimal.Decimal // realized on the part that reduced the position
}

// ApplyFill returns the position after buying (buy=true) or selling sz at px, starting from
// a position of size (signed) entered at entryPx.
func ApplyFill(size, entryPx decimal.Decimal, buy bool, sz, px decimal.Decimal) FillResult {
	signed := sz
	if !buy {
		signed = sz.Neg()
	}
	newSize := size.Add(signed)

	// Opening or adding: the entry price becomes the size-weighted average.
	if size.IsZero() || size.Sign() == signed.Sign() {
		entry := size.Abs().Mul(entryPx).Add(sz.Mul(px)).Div(newSize.Abs())
		return FillResult{NewSize: newSize, NewEntryPx: entry, ClosedPnl: zero}
	}

	// Reducing, closing or flipping: PnL is realized on the closed quantity.
	closing := decimal.Min(sz, size.Abs())
	pnl := closing.Mul(px.Sub(entryPx))
	if size.IsNegative() {
		pnl = pnl.Neg()
	}
	r := FillResult{NewSize: newSize, NewEntryPx: entryPx, ClosedPnl: pnl.Round(usdDecimals)}
	switch {
	case newSize.IsZero():
		r.NewEntryPx = zero
	case newSize.Sign() == signed.Sign(): // flipped: the remainder opens at px
		r.NewEntryPx = px
	}
	return r
}

// Fee for trading sz at px.
func Fee(sz, px, rate decimal.Decimal) decimal.Decimal {
	return sz.Mul(px).Mul(rate).Round(usdDecimals)
}

// WalkBook fills up to sz against levels (best first), not going past limitPx (zero means
// no limit). buy=true takes asks. It returns the filled size and its notional.
func WalkBook(levels []hyperliquid.Level, buy bool, sz, limitPx decimal.Decimal) (filled, notional decimal.Decimal) {
	remaining := sz
	for _, l := range levels {
		if remaining.IsZero() {
			break
		}
		if !limitPx.IsZero() && ((buy && l.Px.GreaterThan(limitPx)) || (!buy && l.Px.LessThan(limitPx))) {
			break
		}
		take := decimal.Min(remaining, l.Sz)
		filled = filled.Add(take)
		notional = notional.Add(take.Mul(l.Px))
		remaining = remaining.Sub(take)
	}
	return filled, notional
}

// --- margin ---

// RestingOrder is the part of an open order that matters for margin.
type RestingOrder struct {
	Buy       bool
	Remaining decimal.Decimal
	Px        decimal.Decimal
}

// CoinState is an account's exposure on one coin.
type CoinState struct {
	Coin     string
	Size     decimal.Decimal // signed
	EntryPx  decimal.Decimal
	Leverage int
	MarkPx   decimal.Decimal
	Orders   []RestingOrder

	// Isolated positions have their own margin, carved out of the cash balance.
	Isolated       bool
	IsolatedMargin decimal.Decimal
	// MaintenanceRate is the fraction of notional the position must keep as equity.
	MaintenanceRate decimal.Decimal
}

// MaintenanceRate is Hyperliquid's maintenance margin rule: half the initial margin at max
// leverage. Margin tiers only start at millions of dollars of notional, far beyond what a
// paper account reaches, so the first tier is the only one modeled.
func MaintenanceRate(maxLeverage int) decimal.Decimal {
	return one.Div(decimal.NewFromInt(int64(2 * max(maxLeverage, 1))))
}

// MaintenanceMargin is the equity the position needs to avoid liquidation.
func (c CoinState) MaintenanceMargin() decimal.Decimal {
	return c.Size.Abs().Mul(c.MarkPx).Mul(c.MaintenanceRate)
}

// IsolatedEquity is an isolated position's margin plus its unrealized PnL.
func (c CoinState) IsolatedEquity() decimal.Decimal {
	return c.IsolatedMargin.Add(c.UnrealizedPnl())
}

// IsolatedLiquidatable reports whether an isolated position is below maintenance.
func (c CoinState) IsolatedLiquidatable() bool {
	return c.Isolated && !c.Size.IsZero() && c.IsolatedEquity().LessThan(c.MaintenanceMargin())
}

// UnrealizedPnl at the mark price.
func (c CoinState) UnrealizedPnl() decimal.Decimal {
	if c.Size.IsZero() {
		return zero
	}
	return c.Size.Mul(c.MarkPx.Sub(c.EntryPx))
}

// PositionMargin is the initial margin the position uses: notional at mark / leverage.
func (c CoinState) PositionMargin() decimal.Decimal {
	return c.Size.Abs().Mul(c.MarkPx).Div(decimal.NewFromInt(int64(c.Leverage)))
}

// OrderMargin reserves margin for resting orders, but only for the part that would grow
// the position: buys first close a short, sells first close a long, and that part is free.
func (c CoinState) OrderMargin() decimal.Decimal {
	var buyQty, buyNtl, sellQty, sellNtl decimal.Decimal
	for _, o := range c.Orders {
		if o.Buy {
			buyQty = buyQty.Add(o.Remaining)
			buyNtl = buyNtl.Add(o.Remaining.Mul(o.Px))
		} else {
			sellQty = sellQty.Add(o.Remaining)
			sellNtl = sellNtl.Add(o.Remaining.Mul(o.Px))
		}
	}
	lev := decimal.NewFromInt(int64(c.Leverage))
	short := decimal.Max(zero, c.Size.Neg())
	long := decimal.Max(zero, c.Size)
	margin := zero
	if buyQty.IsPositive() {
		growing := decimal.Max(zero, buyQty.Sub(short))
		margin = margin.Add(growing.Mul(buyNtl.Div(buyQty)).Div(lev))
	}
	if sellQty.IsPositive() {
		growing := decimal.Max(zero, sellQty.Sub(long))
		margin = margin.Add(growing.Mul(sellNtl.Div(sellQty)).Div(lev))
	}
	return margin
}

// Summary is an account's margin state.
type Summary struct {
	Balance       decimal.Decimal `json:"balance"`       // cash: deposits + realized PnL - fees ± funding
	UnrealizedPnl decimal.Decimal `json:"unrealizedPnl"` // all positions, at mark
	AccountValue  decimal.Decimal `json:"accountValue"`  // balance + unrealized PnL

	// Cross margin: everything except isolated positions and their margin.
	CrossAccountValue decimal.Decimal `json:"crossAccountValue"` // balance - isolated margin + cross unrealized PnL
	MarginUsed        decimal.Decimal `json:"marginUsed"`        // initial margin of cross positions
	CrossMaintenance  decimal.Decimal `json:"crossMaintenance"`  // cross liquidates below this
	IsolatedMargin    decimal.Decimal `json:"isolatedMargin"`    // allocated to isolated positions
	OrderMargin       decimal.Decimal `json:"orderMargin"`       // reserved by resting orders
	Available         decimal.Decimal `json:"available"`         // free to open new exposure
}

// CrossLiquidatable reports whether the cross positions are below maintenance.
func (s Summary) CrossLiquidatable() bool {
	return s.CrossMaintenance.IsPositive() && s.CrossAccountValue.LessThan(s.CrossMaintenance)
}

// Summarize computes the margin summary of balance plus coins.
func Summarize(balance decimal.Decimal, coins []CoinState) Summary {
	s := Summary{Balance: balance}
	crossUpnl := zero
	for _, c := range coins {
		upnl := c.UnrealizedPnl()
		s.UnrealizedPnl = s.UnrealizedPnl.Add(upnl)
		s.OrderMargin = s.OrderMargin.Add(c.OrderMargin())
		if c.Isolated {
			s.IsolatedMargin = s.IsolatedMargin.Add(c.IsolatedMargin)
			continue
		}
		crossUpnl = crossUpnl.Add(upnl)
		s.MarginUsed = s.MarginUsed.Add(c.PositionMargin())
		s.CrossMaintenance = s.CrossMaintenance.Add(c.MaintenanceMargin())
	}
	s.AccountValue = balance.Add(s.UnrealizedPnl)
	s.CrossAccountValue = balance.Sub(s.IsolatedMargin).Add(crossUpnl)
	s.Available = s.CrossAccountValue.Sub(s.MarginUsed).Sub(s.OrderMargin)
	return s
}

// Score is an account's lifetime PnL, what the leaderboard ranks: PnL from runs ended by
// resets plus the current run's (balance and unrealized PnL over what it started with).
// A reset gives a fresh bankroll, not a fresh score (docs/plan.md §5.3).
func Score(priorPnl, balance, unrealizedPnl, startingBalance decimal.Decimal) decimal.Decimal {
	return priorPnl.Add(balance).Add(unrealizedPnl).Sub(startingBalance)
}

// IsolatedMarginAfter returns an isolated position's margin after it changes from oldSize
// to newSize at px. Opening or adding allocates initial margin for the added size;
// reducing releases margin in proportion; flipping starts over for the new side.
func IsolatedMarginAfter(oldSize, oldMargin, newSize, px decimal.Decimal, leverage int) decimal.Decimal {
	lev := decimal.NewFromInt(int64(leverage))
	switch {
	case newSize.IsZero():
		return zero
	case oldSize.IsZero() || newSize.Sign() != oldSize.Sign():
		return newSize.Abs().Mul(px).Div(lev).Round(usdDecimals)
	case newSize.Abs().GreaterThan(oldSize.Abs()):
		added := newSize.Abs().Sub(oldSize.Abs())
		return oldMargin.Add(added.Mul(px).Div(lev)).Round(usdDecimals)
	default:
		return oldMargin.Mul(newSize.Abs()).Div(oldSize.Abs()).Round(usdDecimals)
	}
}

// --- validation ---

var (
	errNotPositive = errors.New("must be greater than zero")
)

// ValidateSize checks sz against the asset's size decimals.
func ValidateSize(sz decimal.Decimal, szDecimals int) error {
	if !sz.IsPositive() {
		return errNotPositive
	}
	if decimalPlaces(sz) > szDecimals {
		return fmt.Errorf("at most %d decimals", szDecimals)
	}
	return nil
}

// ValidatePrice applies Hyperliquid's perp price rules: at most 5 significant figures
// (integers are always allowed) and at most 6 - szDecimals decimals.
func ValidatePrice(px decimal.Decimal, szDecimals int) error {
	if !px.IsPositive() {
		return errNotPositive
	}
	if max := 6 - szDecimals; decimalPlaces(px) > max {
		return fmt.Errorf("at most %d decimals", max)
	}
	if !px.IsInteger() && significantFigures(px) > 5 {
		return errors.New("at most 5 significant figures")
	}
	return nil
}

func decimalPlaces(d decimal.Decimal) int {
	s := stripTrailingZeros(d)
	if s.Exponent() >= 0 {
		return 0
	}
	return int(-s.Exponent())
}

func significantFigures(d decimal.Decimal) int {
	return len(strings.TrimPrefix(stripTrailingZeros(d).Coefficient().String(), "-"))
}

func stripTrailingZeros(d decimal.Decimal) decimal.Decimal {
	// decimal.Decimal keeps the exponent it was parsed with ("1.50" has exponent -2);
	// re-parsing its canonical string drops trailing zeros.
	return decimal.RequireFromString(d.String())
}
