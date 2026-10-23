// Package leaderboard ranks accounts by lifetime PnL in USD (docs/plan.md §5.4).
//
// The score is engine.Score: PnL across every run, so resets don't erase losses. Windowed
// boards (24h, 7d, 30d) rank the change in score since the window began, using the latest
// equity snapshot from before then as the baseline. Standings are recomputed at most every
// TTL and shared by all viewers.
package leaderboard

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"golang.org/x/sync/singleflight"

	"github.com/juancruzfl/paperperps/server/internal/engine"
)

// Windows are the boards on offer; zero means all time.
var Windows = map[string]time.Duration{
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
	"all": 0,
}

var ErrBadWindow = errors.New("unknown leaderboard window")

const topN = 100

type Entry struct {
	Rank   int             `json:"rank"`
	Handle string          `json:"handle"`
	Pnl    decimal.Decimal `json:"pnl"`
	Volume decimal.Decimal `json:"volume"` // traded notional in the window
	Trades int             `json:"trades"` // fills, all time
	Resets int             `json:"resets"`
	// Degen: has traded above a market's real leverage cap (degen mode).
	Degen bool `json:"degen,omitempty"`
	You   bool `json:"you,omitempty"`
}

// Viewer is the requesting account's own standing, ranked or not.
type Viewer struct {
	Entry
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason,omitempty"` // why not eligible
}

type Board struct {
	Window      string    `json:"window"`
	UpdatedAt   time.Time `json:"updatedAt"`
	MinTrades   int       `json:"minTrades"`
	MinAgeHours int       `json:"minAgeHours"`
	Entries     []Entry   `json:"entries"`
	You         *Viewer   `json:"you,omitempty"`
}

type standing struct {
	id        uuid.UUID
	entry     Entry
	createdAt time.Time
	optIn     bool
	hidden    bool // by a moderator, or suspended
}

type snapshot struct {
	at   time.Time
	rows []standing // sorted, best first
}

type Service struct {
	pool      *pgxpool.Pool
	md        engine.MarketData
	minTrades int
	minAge    time.Duration
	ttl       time.Duration
	now       func() time.Time

	group singleflight.Group
	mu    sync.Mutex
	cache map[string]snapshot
}

func New(pool *pgxpool.Pool, md engine.MarketData, minTrades int, minAge, ttl time.Duration) *Service {
	return &Service{
		pool: pool, md: md, minTrades: minTrades, minAge: minAge, ttl: ttl,
		now: func() time.Time { return time.Now().UTC() }, cache: map[string]snapshot{},
	}
}

// Invalidate drops cached standings, e.g. after someone changes their handle or hides
// themselves, so they see the change on their next request.
func (s *Service) Invalidate() {
	s.mu.Lock()
	clear(s.cache)
	s.mu.Unlock()
}

// Get returns the board for window. viewer (uuid.Nil for anonymous requests) gets their own
// standing in You.
func (s *Service) Get(ctx context.Context, window string, viewer uuid.UUID) (Board, error) {
	if _, ok := Windows[window]; !ok {
		return Board{}, ErrBadWindow
	}
	snap, err := s.standings(ctx, window)
	if err != nil {
		return Board{}, err
	}
	b := Board{
		Window: window, UpdatedAt: snap.at, MinTrades: s.minTrades,
		MinAgeHours: int(s.minAge.Hours()), Entries: []Entry{},
	}
	rank := 0
	for _, st := range snap.rows {
		reason := s.ineligible(st, snap.at)
		if reason == "" {
			rank++
		}
		e := st.entry
		e.Rank = rank
		if st.id == viewer && viewer != uuid.Nil {
			e.You = true
			v := &Viewer{Entry: e, Eligible: reason == "", Reason: reason}
			if !v.Eligible {
				v.Rank = 0
			}
			b.You = v
		}
		if reason == "" && rank <= topN {
			b.Entries = append(b.Entries, e)
		}
	}
	if b.You == nil && viewer != uuid.Nil {
		b.You = &Viewer{Reason: fmt.Sprintf("make at least %d trades to appear", s.minTrades)}
	}
	return b, nil
}

func (s *Service) ineligible(st standing, now time.Time) string {
	switch {
	case st.hidden:
		return "a moderator has hidden this account from the leaderboard"
	case !st.optIn:
		return "you've hidden yourself from the leaderboard"
	case st.entry.Trades < s.minTrades:
		return fmt.Sprintf("make %d more trade(s) to appear", s.minTrades-st.entry.Trades)
	case now.Sub(st.createdAt) < s.minAge:
		return fmt.Sprintf("accounts appear once they're %d hours old", int(s.minAge.Hours()))
	}
	return ""
}

func (s *Service) standings(ctx context.Context, window string) (snapshot, error) {
	s.mu.Lock()
	c, ok := s.cache[window]
	s.mu.Unlock()
	if ok && s.now().Sub(c.at) < s.ttl {
		return c, nil
	}
	v, err, _ := s.group.Do(window, func() (any, error) {
		computeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		snap, err := s.compute(computeCtx, window)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.cache[window] = snap
		s.mu.Unlock()
		return snap, nil
	})
	if err != nil {
		return snapshot{}, err
	}
	return v.(snapshot), nil
}

// Compute builds fresh standings, ignoring the cache. Exposed for tests.
func (s *Service) Compute(ctx context.Context, window string) ([]Entry, error) {
	snap, err := s.compute(ctx, window)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, len(snap.rows))
	for i, r := range snap.rows {
		out[i] = r.entry
	}
	return out, nil
}

func (s *Service) compute(ctx context.Context, window string) (snapshot, error) {
	now := s.now()
	var since time.Time
	if d := Windows[window]; d > 0 {
		since = now.Add(-d)
	}

	// Realized lifetime PnL per account that has traded: every run's balance plus PnL
	// locked at its reset, over what it started with.
	byID := map[uuid.UUID]*standing{}
	var order []uuid.UUID
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.handle, a.created_at, a.leaderboard_opt_in,
			a.leaderboard_banned OR a.suspended_at IS NOT NULL, a.degen_at IS NOT NULL,
			(SELECT sum(r.balance + r.locked_upnl - r.starting_balance) FROM runs r WHERE r.account_id = a.id),
			(SELECT count(*) - 1 FROM runs r WHERE r.account_id = a.id),
			f.trades, f.volume
		FROM accounts a
		JOIN (SELECT account_id, count(*) AS trades,
				coalesce(sum(px * sz) FILTER (WHERE created_at >= $1), 0) AS volume
			FROM fills GROUP BY account_id) f ON f.account_id = a.id`, since)
	if err != nil {
		return snapshot{}, fmt.Errorf("leaderboard accounts: %w", err)
	}
	for rows.Next() {
		st := &standing{}
		var realized decimal.Decimal
		if err := rows.Scan(&st.id, &st.entry.Handle, &st.createdAt, &st.optIn, &st.hidden, &st.entry.Degen, &realized,
			&st.entry.Resets, &st.entry.Trades, &st.entry.Volume); err != nil {
			rows.Close()
			return snapshot{}, fmt.Errorf("leaderboard accounts: %w", err)
		}
		st.entry.Pnl = realized
		st.entry.Volume = st.entry.Volume.Round(2)
		byID[st.id] = st
		order = append(order, st.id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return snapshot{}, fmt.Errorf("leaderboard accounts: %w", err)
	}

	// Unrealized PnL of open positions, at live marks.
	rows, err = s.pool.Query(ctx, `
		SELECT p.account_id, p.coin, p.size, p.entry_px FROM positions p
		JOIN accounts a ON a.id = p.account_id AND a.current_run_id = p.run_id
		WHERE p.size <> 0`)
	if err != nil {
		return snapshot{}, fmt.Errorf("leaderboard positions: %w", err)
	}
	for rows.Next() {
		var id uuid.UUID
		var coin string
		var size, entry decimal.Decimal
		if err := rows.Scan(&id, &coin, &size, &entry); err != nil {
			rows.Close()
			return snapshot{}, fmt.Errorf("leaderboard positions: %w", err)
		}
		st, ok := byID[id]
		if !ok {
			continue
		}
		mark := entry
		if t, ok := s.md.Ticker(coin); ok && t.MarkPx.IsPositive() {
			mark = t.MarkPx
		}
		st.entry.Pnl = st.entry.Pnl.Add(size.Mul(mark.Sub(entry)))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return snapshot{}, fmt.Errorf("leaderboard positions: %w", err)
	}

	// Windowed boards subtract the score at the window's start.
	if !since.IsZero() {
		rows, err = s.pool.Query(ctx, `
			SELECT DISTINCT ON (account_id) account_id, cumulative_pnl FROM equity_snapshots
			WHERE ts <= $1 ORDER BY account_id, ts DESC`, since)
		if err != nil {
			return snapshot{}, fmt.Errorf("leaderboard baselines: %w", err)
		}
		for rows.Next() {
			var id uuid.UUID
			var base decimal.Decimal
			if err := rows.Scan(&id, &base); err != nil {
				rows.Close()
				return snapshot{}, fmt.Errorf("leaderboard baselines: %w", err)
			}
			if st, ok := byID[id]; ok {
				st.entry.Pnl = st.entry.Pnl.Sub(base)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return snapshot{}, fmt.Errorf("leaderboard baselines: %w", err)
		}
	}

	out := make([]standing, 0, len(order))
	for _, id := range order {
		st := byID[id]
		st.entry.Pnl = st.entry.Pnl.Round(2)
		out = append(out, *st)
	}
	slices.SortFunc(out, func(a, b standing) int {
		if c := b.entry.Pnl.Cmp(a.entry.Pnl); c != 0 {
			return c
		}
		return cmp.Compare(a.entry.Handle, b.entry.Handle)
	})
	return snapshot{at: now, rows: out}, nil
}
