package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/shopspring/decimal"

	"github.com/juancruzfl/paperperps/server/internal/accounts"
	"github.com/juancruzfl/paperperps/server/internal/engine"
)

// traderProfile is a public profile (/u/<handle>). It shows what the leaderboard already
// makes public plus PnL history and open positions; no balances, orders or account ID.
type traderProfile struct {
	Handle      string    `json:"handle"`
	CreatedAt   time.Time `json:"createdAt"`
	LifetimePnl string    `json:"lifetimePnl"` // leaderboard score at live marks
	Resets      int       `json:"resets"`
	// Rank on the all-time board; 0 while the account doesn't qualify yet.
	Rank int `json:"rank"`
	accounts.TraderStats
	// Performance covers closed positions (round trips), after fees.
	Performance accounts.TradePerformance `json:"performance"`
	Equity    []publicEquityPoint `json:"equity"`
	Positions []publicPosition    `json:"positions"`
}

type publicEquityPoint struct {
	Time          time.Time       `json:"t"`
	CumulativePnl decimal.Decimal `json:"cumulativePnl"`
}

type publicPosition struct {
	Coin       string          `json:"coin"`
	Size       decimal.Decimal `json:"size"` // signed: negative is short
	EntryPx    decimal.Decimal `json:"entryPx"`
	Leverage   int             `json:"leverage"`
	MarginMode string          `json:"marginMode"`
}

var errNoProfile = errors.New("no public profile")

// loadProfile builds the public profile for handle, or errNoProfile.
func loadProfile(ctx context.Context, d Deps, handle string) (traderProfile, error) {
	id, err := d.Accounts.PublicAccount(ctx, handle)
	if errors.Is(err, accounts.ErrNotFound) {
		return traderProfile{}, errNoProfile
	}
	if err != nil {
		return traderProfile{}, err
	}
	a, err := d.Accounts.Load(ctx, id)
	if err != nil {
		return traderProfile{}, err
	}
	stats, err := d.Accounts.TraderStats(ctx, id)
	if err != nil {
		return traderProfile{}, err
	}
	perf, err := d.Accounts.TradePerformance(ctx, id)
	if err != nil {
		return traderProfile{}, err
	}
	points, err := d.Accounts.Equity(ctx, id, time.Now())
	if err != nil {
		return traderProfile{}, err
	}
	sum := engine.SummarizeAccount(a, d.Market)
	p := traderProfile{
		Handle:      a.Handle,
		CreatedAt:   a.CreatedAt,
		LifetimePnl: engine.Score(a.PriorPnl, a.Balance, sum.UnrealizedPnl, a.StartingBalance).StringFixed(2),
		Resets:      a.Resets,
		TraderStats: stats,
		Performance: perf,
		Equity:      make([]publicEquityPoint, 0, len(points)),
		Positions:   []publicPosition{},
	}
	for _, pt := range points {
		p.Equity = append(p.Equity, publicEquityPoint{pt.Time, pt.CumulativePnl})
	}
	for _, pos := range a.Positions {
		if !pos.Size.IsZero() {
			p.Positions = append(p.Positions, publicPosition{pos.Coin, pos.Size, pos.EntryPx, pos.Leverage, pos.MarginMode})
		}
	}
	if d.Leaderboard != nil {
		board, err := d.Leaderboard.Get(ctx, "all", id)
		if err != nil {
			return traderProfile{}, err
		}
		if board.You != nil && board.You.Eligible {
			p.Rank = board.You.Rank
		}
	}
	return p, nil
}

func handleTrader(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := loadProfile(r.Context(), d, r.PathValue("handle"))
		if errors.Is(err, errNoProfile) {
			writeError(w, http.StatusNotFound, "trader_not_found",
				"no public profile with that handle: it doesn't exist, or the trader is hidden from the leaderboard")
			return
		}
		if err != nil {
			internalError(w, "trader profile", err)
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}
