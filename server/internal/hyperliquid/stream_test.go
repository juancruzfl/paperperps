package hyperliquid

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Messages copied from the real testnet API (users field trimmed).
const (
	msgBook       = `{"channel":"l2Book","data":{"coin":"BTC","time":1791582363000,"levels":[[{"px":"82092.0","sz":"0.00486","n":2},{"px":"82048.0","sz":"0.3","n":1}],[{"px":"82093.0","sz":"0.3","n":1},{"px":"82138.0","sz":"0.00174","n":3}]]}}`
	msgTrades     = `{"channel":"trades","data":[{"coin":"BTC","side":"B","px":"82121.0","sz":"0.00015","time":1791582363532,"hash":"0x6ac7","tid":122200036756599},{"coin":"BTC","side":"A","px":"82112.0","sz":"0.00365","time":1791582367015,"hash":"0x32e7","tid":958026877609348}]}`
	msgCtx        = `{"channel":"activeAssetCtx","data":{"coin":"BTC","ctx":{"funding":"-0.0005739396","openInterest":"50.44296","prevDayPx":"82564.0","dayNtlVlm":"2119716.2587099997","premium":"-0.0050490119","oraclePx":"82531.0","markPx":"82137.0","midPx":"82092.5","impactPxs":["82031.5","82114.3"],"dayBaseVlm":"25.66925"}}}`
	msgCtxNullMid = `{"channel":"activeAssetCtx","data":{"coin":"PONS","ctx":{"funding":"0.0","openInterest":"0","prevDayPx":"1.0","dayNtlVlm":"0","premium":null,"oraclePx":"1.0","markPx":"1.0","midPx":null}}}`
)

type recorder struct {
	mu          sync.Mutex
	connects    int
	disconnects int
	books       []L2Book
	trades      [][]Trade
	ctxs        []ActiveAssetCtx
}

func (r *recorder) OnConnect()         { r.mu.Lock(); r.connects++; r.mu.Unlock() }
func (r *recorder) OnDisconnect(error) { r.mu.Lock(); r.disconnects++; r.mu.Unlock() }
func (r *recorder) OnBook(b L2Book)    { r.mu.Lock(); r.books = append(r.books, b); r.mu.Unlock() }
func (r *recorder) OnTrades(t []Trade) { r.mu.Lock(); r.trades = append(r.trades, t); r.mu.Unlock() }
func (r *recorder) OnAssetCtx(c ActiveAssetCtx) {
	r.mu.Lock()
	r.ctxs = append(r.ctxs, c)
	r.mu.Unlock()
}

func TestDispatch(t *testing.T) {
	rec := &recorder{}
	s := &Stream{Handler: rec}
	for _, m := range []string{msgBook, msgTrades, msgCtx, msgCtxNullMid, `{"channel":"pong"}`, `{"channel":"somethingNew","data":1}`} {
		if err := s.dispatch([]byte(m)); err != nil {
			t.Fatalf("dispatch %.40s: %v", m, err)
		}
	}

	b := rec.books[0]
	if b.Coin != "BTC" || len(b.Bids()) != 2 || len(b.Asks()) != 2 {
		t.Fatalf("book = %+v", b)
	}
	if b.Bids()[0].Px.String() != "82092" || b.Asks()[0].Sz.String() != "0.3" || b.Bids()[0].N != 2 {
		t.Errorf("top of book = %+v / %+v", b.Bids()[0], b.Asks()[0])
	}

	tr := rec.trades[0]
	if len(tr) != 2 || tr[0].Side != SideBuy || tr[1].Tid != 958026877609348 {
		t.Errorf("trades = %+v", tr)
	}

	c := rec.ctxs[0]
	if c.Coin != "BTC" || c.Ctx.MarkPx.String() != "82137" || !c.Ctx.MidPx.Valid || c.Ctx.Funding.String() != "-0.0005739396" {
		t.Errorf("ctx = %+v", c)
	}
	if rec.ctxs[1].Ctx.MidPx.Valid || rec.ctxs[1].Ctx.Premium.Valid {
		t.Errorf("null midPx/premium decoded as valid: %+v", rec.ctxs[1])
	}

	if err := s.dispatch([]byte(`{"channel":"error","data":"Invalid subscription"}`)); err == nil {
		t.Error("error channel: want error")
	}
}

// fakeHL is a websocket server that records subscriptions, sends scripted messages, then
// closes the connection so the client has to reconnect.
type fakeHL struct {
	mu    sync.Mutex
	conns int
	subs  []Subscription
	pings int
	send  []string
}

func (f *fakeHL) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	f.mu.Lock()
	f.conns++
	first := f.conns == 1
	f.mu.Unlock()

	ctx := r.Context()
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var msg struct {
				Method       string       `json:"method"`
				Subscription Subscription `json:"subscription"`
			}
			json.Unmarshal(data, &msg)
			f.mu.Lock()
			switch msg.Method {
			case "subscribe":
				f.subs = append(f.subs, msg.Subscription)
			case "ping":
				f.pings++
			}
			f.mu.Unlock()
		}
	}()

	for _, m := range f.send {
		conn.Write(ctx, websocket.MessageText, []byte(m))
	}
	if first {
		time.Sleep(100 * time.Millisecond) // let pings arrive
		conn.Close(websocket.StatusGoingAway, "bye")
		return
	}
	<-ctx.Done()
}

func TestStreamSubscribesPingsAndReconnects(t *testing.T) {
	fake := &fakeHL{send: []string{msgBook, msgTrades}}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	rec := &recorder{}
	s := &Stream{
		URL:          "ws" + strings.TrimPrefix(srv.URL, "http"),
		Subs:         []Subscription{{Type: "l2Book", Coin: "BTC"}, {Type: "trades", Coin: "BTC"}},
		Handler:      rec,
		PingInterval: 20 * time.Millisecond,
		MinBackoff:   10 * time.Millisecond,
		MaxBackoff:   20 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		rec.mu.Lock()
		reconnected := rec.connects >= 2 && len(rec.books) >= 2
		rec.mu.Unlock()
		if reconnected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no reconnect: %+v", rec)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.subs) != 4 { // 2 subscriptions × 2 connections
		t.Errorf("subscriptions = %+v, want 4", fake.subs)
	}
	if fake.pings == 0 {
		t.Error("no pings sent")
	}
	if rec.disconnects < 1 {
		t.Error("OnDisconnect not called")
	}
}
