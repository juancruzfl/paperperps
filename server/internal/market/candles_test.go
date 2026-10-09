package market

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestCandlesCacheAndInterval(t *testing.T) {
	info := &fakeInfo{}
	c := NewCandles(info, 10*time.Second)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	ctx := context.Background()

	if _, err := c.Get(ctx, "BTC", "3m"); !errors.Is(err, ErrBadInterval) {
		t.Errorf("bad interval err = %v", err)
	}

	bars, err := c.Get(ctx, "BTC", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if want := now.Add(-CandleBars * time.Hour).UnixMilli(); bars[0].OpenTime != want {
		t.Errorf("requested start = %d, want %d (500 bars back)", bars[0].OpenTime, want)
	}
	c.Get(ctx, "BTC", "1h")
	if n := info.candles.Load(); n != 1 {
		t.Errorf("upstream calls = %d, want 1 (cached)", n)
	}

	now = now.Add(11 * time.Second)
	c.Get(ctx, "BTC", "1h")
	if n := info.candles.Load(); n != 2 {
		t.Errorf("upstream calls = %d, want 2 after TTL", n)
	}

	// When upstream fails, stale cached bars beat an error.
	info.fail.Store(true)
	now = now.Add(11 * time.Second)
	if bars, err := c.Get(ctx, "BTC", "1h"); err != nil || len(bars) != 1 {
		t.Errorf("stale fallback = %v, %v", bars, err)
	}
	if _, err := c.Get(ctx, "ETH", "1h"); err == nil {
		t.Error("uncached failure should return an error")
	}
}

func TestCandlesConcurrentMissesShareOneRequest(t *testing.T) {
	info := &fakeInfo{release: make(chan struct{})}
	c := NewCandles(info, 10*time.Second)

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Get(context.Background(), "BTC", "1m"); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(info.release)
	wg.Wait()
	if n := info.candles.Load(); n != 1 {
		t.Errorf("upstream calls = %d, want 1", n)
	}
}
