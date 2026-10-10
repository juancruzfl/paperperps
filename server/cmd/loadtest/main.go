// Command loadtest drives a running PaperPerps server the way many visitors would, and
// reports latencies and errors. Point it at a throwaway instance: it creates accounts and
// leaves resting orders behind.
//
//	go run ./cmd/loadtest -url http://localhost:8099 -accounts 300 -resting 20 -ws 300 -duration 30s
//
// Account creation is rate limited per IP. Without a proxy in front, the server trusts the
// last X-Forwarded-For hop, so the tool sends a distinct fake address per account. Behind
// Railway's proxy that header can't be forged this way, which is the point of the limit.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

type ticker struct {
	Coin        string `json:"coin"`
	SzDecimals  int    `json:"szDecimals"`
	MaxLeverage int    `json:"maxLeverage"`
	MarkPx      string `json:"markPx"`
}

type account struct {
	secret string
	mu     sync.Mutex
	long   map[string]string // coin → size bought at market, to sell back
}

// recorder collects latencies and error codes per operation.
type recorder struct {
	mu   sync.Mutex
	lat  map[string][]time.Duration
	errs map[string]map[string]int
}

func newRecorder() *recorder {
	return &recorder{lat: map[string][]time.Duration{}, errs: map[string]map[string]int{}}
}

func (r *recorder) add(op string, d time.Duration, errCode string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lat[op] = append(r.lat[op], d)
	if errCode != "" {
		if r.errs[op] == nil {
			r.errs[op] = map[string]int{}
		}
		r.errs[op][errCode]++
	}
}

func (r *recorder) report() {
	r.mu.Lock()
	defer r.mu.Unlock()
	ops := make([]string, 0, len(r.lat))
	for op := range r.lat {
		ops = append(ops, op)
	}
	slices.Sort(ops)
	fmt.Printf("\n%-18s %7s %8s %8s %8s %8s  %s\n", "operation", "count", "p50", "p95", "p99", "max", "errors")
	for _, op := range ops {
		l := r.lat[op]
		slices.Sort(l)
		pct := func(p float64) time.Duration { return l[min(len(l)-1, int(math.Ceil(p*float64(len(l))))-1)] }
		errs := "-"
		if e := r.errs[op]; len(e) > 0 {
			parts := []string{}
			for code, n := range e {
				parts = append(parts, fmt.Sprintf("%s×%d", code, n))
			}
			slices.Sort(parts)
			errs = strings.Join(parts, " ")
		}
		fmt.Printf("%-18s %7d %8s %8s %8s %8s  %s\n", op, len(l), ms(pct(0.5)), ms(pct(0.95)), ms(pct(0.99)), ms(l[len(l)-1]), errs)
	}
}

func ms(d time.Duration) string { return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000) }

type client struct {
	base string
	http *http.Client
	rec  *recorder
}

// call does one API request and records it under op. It returns the decoded body.
func (c *client) call(ctx context.Context, op, method, path, secret, ip string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	if ip != "" {
		req.Header.Set("X-Forwarded-For", ip)
	}
	start := time.Now()
	res, err := c.http.Do(req)
	if err != nil {
		c.rec.add(op, time.Since(start), "network")
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	d := time.Since(start)
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(data, &e)
		code := e.Error
		if code == "" {
			code = fmt.Sprint(res.StatusCode)
		}
		c.rec.add(op, d, code)
		return errors.New(code)
	}
	c.rec.add(op, d, "")
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// restingBuy returns a limit price well below the mark and a size worth just over $10,
// both valid under Hyperliquid's price and size rules.
func restingBuy(t ticker) (px, sz string) {
	mark := mustFloat(t.MarkPx)
	p := mark * (0.6 + rand.Float64()*0.3)
	dec := min(6-t.SzDecimals, max(0, 5-int(math.Floor(math.Log10(p)))-1))
	p = math.Round(p*math.Pow10(dec)) / math.Pow10(dec)
	step := math.Pow10(t.SzDecimals)
	s := math.Ceil(10.5/p*step) / step
	return trim(p, dec), trim(s, t.SzDecimals)
}

func marketSize(t ticker) string {
	step := math.Pow10(t.SzDecimals)
	return trim(math.Ceil(11/mustFloat(t.MarkPx)*step)/step, t.SzDecimals)
}

func trim(v float64, dec int) string { return fmt.Sprintf("%.*f", dec, v) }

func mustFloat(s string) float64 {
	var f float64
	fmt.Sscan(s, &f)
	return f
}

func main() {
	base := flag.String("url", "http://localhost:8099", "server to load")
	nAccounts := flag.Int("accounts", 300, "accounts to create")
	resting := flag.Int("resting", 20, "resting limit orders per account")
	nWS := flag.Int("ws", 300, "websocket clients")
	duration := flag.Duration("duration", 30*time.Second, "steady-state phase length")
	marketRate := flag.Int("market-rate", 20, "market orders per second during the steady phase")
	flag.Parse()

	rec := newRecorder()
	c := &client{base: strings.TrimRight(*base, "/"), http: &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 256}}, rec: rec}
	ctx := context.Background()

	var tickers []ticker
	if err := c.call(ctx, "markets", "GET", "/api/markets", "", "", nil, &tickers); err != nil || len(tickers) == 0 {
		fmt.Fprintln(os.Stderr, "can't load markets:", err)
		os.Exit(1)
	}
	byCoin := map[string]ticker{}
	for _, t := range tickers {
		byCoin[t.Coin] = t
	}
	fmt.Printf("server %s: %d markets\n", c.base, len(tickers))

	// Websocket clients first, so they see the whole run.
	var wsMsgs, wsDropped, wsErrors atomic.Int64
	wsCtx, stopWS := context.WithCancel(ctx)
	var wsWG sync.WaitGroup
	wsURL := "ws" + strings.TrimPrefix(c.base, "http") + "/ws"
	for i := range *nWS {
		wsWG.Add(1)
		go func() {
			defer wsWG.Done()
			coin := tickers[i%len(tickers)].Coin
			conn, _, err := websocket.Dial(wsCtx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {c.base}}})
			if err != nil {
				wsErrors.Add(1)
				return
			}
			defer conn.CloseNow()
			conn.SetReadLimit(1 << 20)
			for _, topic := range []string{"markets", "book:" + coin, "trades:" + coin} {
				buf, _ := json.Marshal(map[string]string{"op": "subscribe", "topic": topic})
				conn.Write(wsCtx, websocket.MessageText, buf)
			}
			for {
				_, _, err := conn.Read(wsCtx)
				if err != nil {
					if wsCtx.Err() == nil {
						if websocket.CloseStatus(err) == websocket.StatusPolicyViolation {
							wsDropped.Add(1) // the server dropped us as too slow
						} else {
							wsErrors.Add(1)
						}
					}
					return
				}
				wsMsgs.Add(1)
			}
		}()
	}

	// Accounts, 20 at a time.
	fmt.Printf("creating %d accounts…\n", *nAccounts)
	accts := make([]*account, *nAccounts)
	sem := make(chan struct{}, 20)
	var wg sync.WaitGroup
	for i := range *nAccounts {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			var created struct {
				Secret string `json:"secret"`
			}
			ip := fmt.Sprintf("10.%d.%d.%d", i/65536%256, i/256%256, i%256)
			if err := c.call(ctx, "create account", "POST", "/api/accounts", "", ip, nil, &created); err == nil {
				accts[i] = &account{secret: created.Secret, long: map[string]string{}}
			}
		}()
	}
	wg.Wait()
	accts = slices.DeleteFunc(accts, func(a *account) bool { return a == nil })

	// Resting orders.
	fmt.Printf("placing %d resting limit orders…\n", len(accts)**resting)
	phase := time.Now()
	for _, a := range accts {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			for range *resting {
				t := tickers[rand.N(len(tickers))]
				px, sz := restingBuy(t)
				c.call(ctx, "place limit", "POST", "/api/orders", a.secret, "", map[string]any{
					"coin": t.Coin, "side": "buy", "type": "limit", "sz": sz, "px": px,
				}, nil)
			}
		}()
	}
	wg.Wait()
	fmt.Printf("  took %s\n", time.Since(phase).Round(time.Millisecond))

	// Steady state: market orders round-tripping BTC/ETH, account reads, leaderboard reads.
	fmt.Printf("steady state for %s: %d market orders/s, %d websocket clients…\n", *duration, *marketRate, *nWS)
	msgsBefore := wsMsgs.Load()
	steadyCtx, stop := context.WithTimeout(ctx, *duration)
	defer stop()
	tick := time.NewTicker(time.Second / time.Duration(max(1, *marketRate)))
	defer tick.Stop()
	go func() {
		for steadyCtx.Err() == nil {
			c.call(ctx, "leaderboard", "GET", "/api/leaderboard?window=all", "", "", nil, nil)
			a := accts[rand.N(len(accts))]
			c.call(ctx, "get /api/me", "GET", "/api/me", a.secret, "", nil, nil)
			time.Sleep(500 * time.Millisecond)
		}
	}()
loop:
	for {
		select {
		case <-steadyCtx.Done():
			break loop
		case <-tick.C:
			a := accts[rand.N(len(accts))]
			coin := []string{"BTC", "ETH"}[rand.N(2)]
			wg.Add(1)
			go func() {
				defer wg.Done()
				a.mu.Lock()
				defer a.mu.Unlock()
				if sz, ok := a.long[coin]; ok {
					if c.call(ctx, "market order", "POST", "/api/orders", a.secret, "", map[string]any{
						"coin": coin, "side": "sell", "type": "market", "sz": sz, "reduceOnly": true,
					}, nil) == nil {
						delete(a.long, coin)
					}
					return
				}
				sz := marketSize(byCoin[coin])
				if c.call(ctx, "market order", "POST", "/api/orders", a.secret, "", map[string]any{
					"coin": coin, "side": "buy", "type": "market", "sz": sz,
				}, nil) == nil {
					a.long[coin] = sz
				}
			}()
		}
	}
	wg.Wait()
	elapsed := *duration
	msgs := wsMsgs.Load() - msgsBefore
	stopWS()
	wsWG.Wait()

	rec.report()
	fmt.Printf("\nwebsocket: %d clients, %d messages in the steady phase (%.0f/s), %d dropped as slow, %d other errors\n",
		*nWS, msgs, float64(msgs)/elapsed.Seconds(), wsDropped.Load(), wsErrors.Load())

	var health map[string]any
	if err := c.call(ctx, "health", "GET", "/api/health", "", "", nil, &health); err == nil || health != nil {
		buf, _ := json.MarshalIndent(health["engine"], "", "  ")
		fmt.Printf("engine after the run: %s\n", buf)
	}
}
