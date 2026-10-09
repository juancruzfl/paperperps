package hyperliquid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/coder/websocket"
)

// Subscription is one websocket channel subscription.
type Subscription struct {
	Type     string `json:"type"`
	Coin     string `json:"coin,omitempty"`
	Interval string `json:"interval,omitempty"`
}

// Handler receives decoded stream messages. Calls come from a single goroutine, one at a
// time, in the order Hyperliquid sent them.
type Handler interface {
	OnConnect()
	OnDisconnect(err error)
	OnBook(L2Book)
	OnTrades([]Trade)
	OnAssetCtx(ActiveAssetCtx)
}

// Stream keeps one websocket connection to Hyperliquid open, reconnecting with backoff,
// and dispatches messages to Handler.
type Stream struct {
	URL     string
	Subs    []Subscription
	Handler Handler

	// Tunables; zero values get defaults. Tests shorten them.
	PingInterval time.Duration // Hyperliquid drops connections idle for 60s
	ReadTimeout  time.Duration
	MinBackoff   time.Duration
	MaxBackoff   time.Duration
}

// Run blocks until ctx is done, reconnecting whenever the connection fails.
func (s *Stream) Run(ctx context.Context) {
	s.setDefaults()
	backoff := s.MinBackoff
	for {
		start := time.Now()
		err := s.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		s.Handler.OnDisconnect(err)
		if time.Since(start) > time.Minute {
			backoff = s.MinBackoff // the connection was healthy for a while
		}
		wait := backoff/2 + rand.N(backoff/2+1)
		slog.Warn("hyperliquid stream disconnected", "err", err, "retry_in", wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		backoff = min(backoff*2, s.MaxBackoff)
	}
}

func (s *Stream) setDefaults() {
	if s.PingInterval == 0 {
		s.PingInterval = 30 * time.Second
	}
	if s.ReadTimeout == 0 {
		s.ReadTimeout = 60 * time.Second
	}
	if s.MinBackoff == 0 {
		s.MinBackoff = time.Second
	}
	if s.MaxBackoff == 0 {
		s.MaxBackoff = 30 * time.Second
	}
}

func (s *Stream) runOnce(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	conn, _, err := websocket.Dial(dialCtx, s.URL, nil)
	cancel()
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(8 << 20)

	for _, sub := range s.Subs {
		if err := writeJSON(ctx, conn, map[string]any{"method": "subscribe", "subscription": sub}); err != nil {
			return fmt.Errorf("subscribe %s %s: %w", sub.Type, sub.Coin, err)
		}
	}
	slog.Info("hyperliquid stream connected", "url", s.URL, "subscriptions", len(s.Subs))
	s.Handler.OnConnect()

	pingCtx, stopPing := context.WithCancel(ctx)
	defer stopPing()
	go func() {
		t := time.NewTicker(s.PingInterval)
		defer t.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-t.C:
				// A failed ping surfaces as a read error below.
				_ = writeJSON(pingCtx, conn, map[string]string{"method": "ping"})
			}
		}
	}()

	for {
		readCtx, cancel := context.WithTimeout(ctx, s.ReadTimeout)
		_, data, err := conn.Read(readCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		if err := s.dispatch(data); err != nil {
			slog.Warn("hyperliquid stream: bad message", "err", err)
		}
	}
}

type envelope struct {
	Channel string          `json:"channel"`
	Data    json.RawMessage `json:"data"`
}

func (s *Stream) dispatch(data []byte) error {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	switch env.Channel {
	case "l2Book":
		var b L2Book
		if err := json.Unmarshal(env.Data, &b); err != nil {
			return fmt.Errorf("l2Book: %w", err)
		}
		s.Handler.OnBook(b)
	case "trades":
		var t []Trade
		if err := json.Unmarshal(env.Data, &t); err != nil {
			return fmt.Errorf("trades: %w", err)
		}
		s.Handler.OnTrades(t)
	case "activeAssetCtx":
		var c ActiveAssetCtx
		if err := json.Unmarshal(env.Data, &c); err != nil {
			return fmt.Errorf("activeAssetCtx: %w", err)
		}
		s.Handler.OnAssetCtx(c)
	case "error":
		return errors.New("server error: " + string(env.Data))
	case "subscriptionResponse", "pong":
	default:
		// Ignore channels we didn't ask for.
	}
	return nil
}

func writeJSON(ctx context.Context, conn *websocket.Conn, v any) error {
	buf, err := json.Marshal(v)
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, buf)
}
