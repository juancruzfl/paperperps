// Command fakehl is a tiny stand-in for Hyperliquid's public API, for browser tests: same
// endpoints and message shapes, two markets, and prices a test can move.
//
//	go run ./cmd/fakehl -addr :9999
//	curl -X POST localhost:9999/control/price -d '{"coin":"BTC","px":"82000"}'
//
// Point the server at it with HL_INFO_URL=http://localhost:9999/info and
// HL_WS_URL=ws://localhost:9999/ws.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type asset struct {
	Name        string  `json:"name"`
	SzDecimals  int     `json:"szDecimals"`
	MaxLeverage int     `json:"maxLeverage"`
	tick        float64 // price step between book levels
	prevDay     float64
}

var assets = []*asset{
	{Name: "BTC", SzDecimals: 5, MaxLeverage: 40, tick: 1, prevDay: 80000},
	{Name: "ETH", SzDecimals: 4, MaxLeverage: 25, tick: 0.1, prevDay: 2500},
}

type market struct {
	mu     sync.Mutex
	prices map[string]float64
	tid    int64
}

func (m *market) price(coin string) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.prices[coin]
}

func px(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func (m *market) ctx(a *asset) map[string]any {
	p := m.price(a.Name)
	return map[string]any{
		"funding": "0.0000125", "openInterest": "100", "prevDayPx": px(a.prevDay),
		"dayNtlVlm": "1000000", "premium": "0", "oraclePx": px(p), "markPx": px(p),
		"midPx": px(p + a.tick/2), "impactPxs": []string{px(p), px(p + a.tick)},
	}
}

// book has the best bid at the price and the best ask one tick above, 20 levels deep.
func (m *market) book(a *asset) map[string]any {
	p := m.price(a.Name)
	var bids, asks []map[string]any
	for i := range 20 {
		lvl := func(v float64) map[string]any { return map[string]any{"px": px(v), "sz": "5", "n": 3} }
		bids = append(bids, lvl(p-float64(i)*a.tick))
		asks = append(asks, lvl(p+float64(i+1)*a.tick))
	}
	return map[string]any{"coin": a.Name, "time": time.Now().UnixMilli(), "levels": [][]map[string]any{bids, asks}}
}

func (m *market) handleInfo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type string `json:"type"`
		Req  struct {
			Coin      string `json:"coin"`
			Interval  string `json:"interval"`
			StartTime int64  `json:"startTime"`
			EndTime   int64  `json:"endTime"`
		} `json:"req"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	meta := map[string]any{"universe": assets}
	var out any
	switch req.Type {
	case "meta":
		out = meta
	case "metaAndAssetCtxs":
		ctxs := []any{}
		for _, a := range assets {
			ctxs = append(ctxs, m.ctx(a))
		}
		out = []any{meta, ctxs}
	case "candleSnapshot":
		step := map[string]int64{"1m": 60e3, "5m": 300e3, "15m": 900e3, "1h": 3600e3, "4h": 14400e3, "1d": 86400e3}[req.Req.Interval]
		if step == 0 {
			step = 900e3
		}
		p := m.price(req.Req.Coin)
		bars := []any{}
		for t := req.Req.StartTime / step * step; t <= req.Req.EndTime; t += step {
			o := p * (1 + 0.002*float64((t/step)%5-2)/2)
			bars = append(bars, map[string]any{"t": t, "T": t + step - 1, "s": req.Req.Coin, "i": req.Req.Interval,
				"o": px(o), "c": px(p), "h": px(max(o, p) * 1.001), "l": px(min(o, p) * 0.999), "v": "1.5", "n": 10})
		}
		out = bars
	default:
		http.Error(w, "unsupported type "+req.Type, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// handleWS pushes books and contexts for subscribed coins every 200ms, and a trade at the
// current price every second.
func (m *market) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	var mu sync.Mutex
	subs := map[string]map[string]bool{} // channel type → coins
	send := func(channel string, data any) error {
		buf, _ := json.Marshal(map[string]any{"channel": channel, "data": data})
		wctx, c := context.WithTimeout(ctx, 5*time.Second)
		defer c()
		return conn.Write(wctx, websocket.MessageText, buf)
	}
	go func() {
		defer cancel()
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var msg struct {
				Method       string `json:"method"`
				Subscription struct {
					Type string `json:"type"`
					Coin string `json:"coin"`
				} `json:"subscription"`
			}
			json.Unmarshal(data, &msg)
			switch msg.Method {
			case "ping":
				send("pong", nil)
			case "subscribe":
				mu.Lock()
				if subs[msg.Subscription.Type] == nil {
					subs[msg.Subscription.Type] = map[string]bool{}
				}
				subs[msg.Subscription.Type][msg.Subscription.Coin] = true
				mu.Unlock()
				send("subscriptionResponse", map[string]any{"method": "subscribe", "subscription": msg.Subscription})
			}
		}
	}()

	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for n := 0; ; n++ {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		mu.Lock()
		books, ctxs, trades := subs["l2Book"], subs["activeAssetCtx"], subs["trades"]
		mu.Unlock()
		for _, a := range assets {
			if books[a.Name] {
				if send("l2Book", m.book(a)) != nil {
					return
				}
			}
			if ctxs[a.Name] {
				send("activeAssetCtx", map[string]any{"coin": a.Name, "ctx": m.ctx(a)})
			}
			if trades[a.Name] && n%5 == 0 {
				m.mu.Lock()
				m.tid++
				tid := m.tid
				m.mu.Unlock()
				send("trades", []any{map[string]any{"coin": a.Name, "side": "B", "px": px(m.price(a.Name)), "sz": "0.1",
					"time": time.Now().UnixMilli(), "tid": tid, "hash": fmt.Sprintf("0x%x", tid)}})
			}
		}
	}
}

func (m *market) handleControl(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Coin string `json:"coin"`
		Px   string `json:"px"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	v, err := strconv.ParseFloat(req.Px, 64)
	m.mu.Lock()
	_, known := m.prices[req.Coin]
	if err == nil && known && v > 0 {
		m.prices[req.Coin] = v
	}
	m.mu.Unlock()
	if err != nil || !known || v <= 0 {
		http.Error(w, "need a known coin and a positive px", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func main() {
	addr := flag.String("addr", ":9999", "listen address")
	flag.Parse()
	m := &market{prices: map[string]float64{}}
	for _, a := range assets {
		m.prices[a.Name] = a.prevDay
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /info", m.handleInfo)
	mux.HandleFunc("GET /ws", m.handleWS)
	mux.HandleFunc("POST /control/price", m.handleControl)
	log.Printf("fake Hyperliquid on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
