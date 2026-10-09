package market

import (
	"context"
	"errors"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/juancruzfl/paperperps/server/internal/hyperliquid"
)

// CandleBars is how many bars a candle request returns.
const CandleBars = 500

// Intervals are the chart intervals clients may request.
var Intervals = map[string]time.Duration{
	"1m":  time.Minute,
	"5m":  5 * time.Minute,
	"15m": 15 * time.Minute,
	"1h":  time.Hour,
	"4h":  4 * time.Hour,
	"1d":  24 * time.Hour,
}

var ErrBadInterval = errors.New("unsupported interval")

// Candles serves chart history from Hyperliquid, cached briefly. Every visitor's chart
// loads history, so without the cache each page view would cost an upstream request and
// eat into Hyperliquid's per-IP rate limit. Clients extend the last bar from the live
// trade stream, so a few seconds of staleness doesn't show.
type Candles struct {
	info Info
	ttl  time.Duration
	now  func() time.Time

	group singleflight.Group
	mu    sync.Mutex
	cache map[string]candleEntry
}

type candleEntry struct {
	bars    []hyperliquid.Candle
	fetched time.Time
}

func NewCandles(info Info, ttl time.Duration) *Candles {
	return &Candles{info: info, ttl: ttl, now: time.Now, cache: map[string]candleEntry{}}
}

// Get returns the last CandleBars bars for coin, oldest first.
func (c *Candles) Get(ctx context.Context, coin, interval string) ([]hyperliquid.Candle, error) {
	step, ok := Intervals[interval]
	if !ok {
		return nil, ErrBadInterval
	}
	key := coin + "|" + interval

	c.mu.Lock()
	e, hit := c.cache[key]
	c.mu.Unlock()
	if hit && c.now().Sub(e.fetched) < c.ttl {
		return e.bars, nil
	}

	// Concurrent misses for the same key share one upstream request. It runs detached from
	// the first caller's context so that caller disconnecting doesn't fail the others.
	ch := c.group.DoChan(key, func() (any, error) {
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		end := c.now()
		bars, err := c.info.CandleSnapshot(fetchCtx, coin, interval, end.Add(-step*CandleBars), end)
		if err != nil {
			return nil, err
		}
		if bars == nil {
			bars = []hyperliquid.Candle{}
		}
		c.mu.Lock()
		c.cache[key] = candleEntry{bars: bars, fetched: end}
		c.mu.Unlock()
		return bars, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.Err != nil {
			if hit {
				return e.bars, nil // serve stale history rather than an empty chart
			}
			return nil, r.Err
		}
		return r.Val.([]hyperliquid.Candle), nil
	}
}
