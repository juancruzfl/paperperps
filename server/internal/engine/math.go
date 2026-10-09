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
	Size     decimal.Decimal // signed
	EntryPx  decimal.Decimal
	Leverage int
	MarkPx   decimal.Decimal
	Orders   []RestingOrder
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

// Summary is an account's cross-margin state.
type Summary struct {
	Balance       decimal.Decimal `json:"balance"`       // cash: deposits + realized PnL - fees
	UnrealizedPnl decimal.Decimal `json:"unrealizedPnl"` // at mark
	AccountValue  decimal.Decimal `json:"accountValue"`  // balance + unrealized PnL
	MarginUsed    decimal.Decimal `json:"marginUsed"`    // by open positions
	OrderMargin   decimal.Decimal `json:"orderMargin"`   // reserved by resting orders
	Available     decimal.Decimal `json:"available"`     // free to open new exposure
}

// Summarize computes the cross-margin summary of balance plus coins.
func Summarize(balance decimal.Decimal, coins []CoinState) Summary {
	s := Summary{Balance: balance}
	for _, c := range coins {
		s.UnrealizedPnl = s.UnrealizedPnl.Add(c.UnrealizedPnl())
		s.MarginUsed = s.MarginUsed.Add(c.PositionMargin())
		s.OrderMargin = s.OrderMargin.Add(c.OrderMargin())
	}
	s.AccountValue = balance.Add(s.UnrealizedPnl)
	s.Available = s.AccountValue.Sub(s.MarginUsed).Sub(s.OrderMargin)
	return s
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
