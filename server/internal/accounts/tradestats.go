package accounts

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// StatFill is the part of a fill that trade statistics need.
type StatFill struct {
	RunID     uuid.UUID
	Coin      string
	Side      string // buy or sell
	Sz        decimal.Decimal
	Fee       decimal.Decimal
	ClosedPnl decimal.Decimal
	At        time.Time
}

// RoundTrip is one position on one coin, from opening until it was flat again.
type RoundTrip struct {
	Coin   string
	Long   bool
	Opened time.Time
	Closed time.Time
	Pnl    decimal.Decimal // closed PnL minus fees
}

// RoundTrips rebuilds closed positions from fills, oldest fill first. A fill that flips a
// position closes one round trip and opens the next, splitting its fee by size. Positions
// still open, or wiped by a reset before closing, aren't included.
func RoundTrips(fills []StatFill) []RoundTrip {
	type key struct {
		run  uuid.UUID
		coin string
	}
	type open struct {
		size decimal.Decimal
		trip RoundTrip
	}
	state := map[key]*open{}
	var out []RoundTrip
	for _, f := range fills {
		k := key{f.RunID, f.Coin}
		st := state[k]
		if st == nil {
			st = &open{}
			state[k] = st
		}
		delta := f.Sz
		if f.Side == "sell" {
			delta = delta.Neg()
		}
		if st.size.IsZero() {
			st.trip = RoundTrip{Coin: f.Coin, Long: delta.IsPositive(), Opened: f.At}
		}
		next := st.size.Add(delta)
		flipped := !st.size.IsZero() && !next.IsZero() && st.size.Sign() != next.Sign()
		switch {
		case flipped:
			closeFee := f.Fee.Mul(st.size.Abs()).Div(f.Sz)
			st.trip.Pnl = st.trip.Pnl.Add(f.ClosedPnl).Sub(closeFee)
			st.trip.Closed = f.At
			out = append(out, st.trip)
			st.trip = RoundTrip{Coin: f.Coin, Long: next.IsPositive(), Opened: f.At, Pnl: closeFee.Sub(f.Fee)}
		case next.IsZero():
			st.trip.Pnl = st.trip.Pnl.Add(f.ClosedPnl).Sub(f.Fee)
			st.trip.Closed = f.At
			out = append(out, st.trip)
		default:
			st.trip.Pnl = st.trip.Pnl.Add(f.ClosedPnl).Sub(f.Fee)
		}
		st.size = next
	}
	return out
}

// SideStats sums the round trips on one side, or one coin.
type SideStats struct {
	Trips int             `json:"trips"`
	Wins  int             `json:"wins"`
	Pnl   decimal.Decimal `json:"pnl"`
}

type CoinStats struct {
	Coin string `json:"coin"`
	SideStats
}

// TradePerformance describes how a trader's closed positions went, after fees.
type TradePerformance struct {
	RoundTrips int `json:"roundTrips"`
	Wins       int `json:"wins"`
	Losses     int `json:"losses"`
	// AvgWin is the mean PnL of winning round trips; AvgLoss of losing ones (negative).
	AvgWin  decimal.Decimal `json:"avgWin"`
	AvgLoss decimal.Decimal `json:"avgLoss"`
	// ProfitFactor is gross wins over gross losses; null when there are no losses.
	ProfitFactor        *decimal.Decimal `json:"profitFactor"`
	AvgHoldSeconds      int64            `json:"avgHoldSeconds"`
	LongestLosingStreak int              `json:"longestLosingStreak"`
	Long                SideStats        `json:"long"`
	Short               SideStats        `json:"short"`
	// ByCoin is sorted by number of round trips, most first.
	ByCoin []CoinStats `json:"byCoin"`
}

// Summarize computes performance from round trips in the order they closed.
func Summarize(trips []RoundTrip) TradePerformance {
	p := TradePerformance{RoundTrips: len(trips), ByCoin: []CoinStats{}}
	var grossWin, grossLoss decimal.Decimal
	var held time.Duration
	streak := 0
	coins := map[string]*CoinStats{}
	for _, t := range trips {
		win := t.Pnl.IsPositive()
		switch {
		case win:
			p.Wins++
			grossWin = grossWin.Add(t.Pnl)
			streak = 0
		case t.Pnl.IsNegative():
			p.Losses++
			grossLoss = grossLoss.Add(t.Pnl.Neg())
			streak++
			p.LongestLosingStreak = max(p.LongestLosingStreak, streak)
		}
		held += t.Closed.Sub(t.Opened)

		side := &p.Short
		if t.Long {
			side = &p.Long
		}
		c := coins[t.Coin]
		if c == nil {
			c = &CoinStats{Coin: t.Coin}
			coins[t.Coin] = c
		}
		for _, s := range []*SideStats{side, &c.SideStats} {
			s.Trips++
			s.Pnl = s.Pnl.Add(t.Pnl)
			if win {
				s.Wins++
			}
		}
	}
	if p.Wins > 0 {
		p.AvgWin = grossWin.Div(decimal.NewFromInt(int64(p.Wins))).Round(2)
	}
	if p.Losses > 0 {
		p.AvgLoss = grossLoss.Div(decimal.NewFromInt(int64(p.Losses))).Neg().Round(2)
		pf := grossWin.Div(grossLoss).Round(2)
		p.ProfitFactor = &pf
	}
	if len(trips) > 0 {
		p.AvgHoldSeconds = int64(held.Seconds()) / int64(len(trips))
	}
	p.Long.Pnl = p.Long.Pnl.Round(2)
	p.Short.Pnl = p.Short.Pnl.Round(2)
	for _, c := range coins {
		c.Pnl = c.Pnl.Round(2)
		p.ByCoin = append(p.ByCoin, *c)
	}
	sort.Slice(p.ByCoin, func(i, j int) bool {
		a, b := p.ByCoin[i], p.ByCoin[j]
		if a.Trips != b.Trips {
			return a.Trips > b.Trips
		}
		return a.Coin < b.Coin
	})
	return p
}

// TradePerformance summarizes every position the account has closed, across all runs.
func (s *Store) TradePerformance(ctx context.Context, id uuid.UUID) (TradePerformance, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT run_id, coin, side, sz, fee, closed_pnl, created_at
		FROM fills WHERE account_id = $1 ORDER BY created_at, id`, id)
	if err != nil {
		return TradePerformance{}, fmt.Errorf("trade performance: %w", err)
	}
	fills, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (StatFill, error) {
		var f StatFill
		err := r.Scan(&f.RunID, &f.Coin, &f.Side, &f.Sz, &f.Fee, &f.ClosedPnl, &f.At)
		return f, err
	})
	if err != nil {
		return TradePerformance{}, fmt.Errorf("trade performance: %w", err)
	}
	return Summarize(RoundTrips(fills)), nil
}
