// Package httpapi serves the REST API, health check and built frontend from one origin.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juancruzfl/paperperps/server/internal/config"
	"github.com/juancruzfl/paperperps/server/internal/hub"
	"github.com/juancruzfl/paperperps/server/internal/market"
)

// EngineState reports whether this process holds the engine lock.
type EngineState struct {
	leader atomic.Bool
}

func (s *EngineState) SetLeader(v bool) { s.leader.Store(v) }
func (s *EngineState) IsLeader() bool   { return s.leader.Load() }

func (s *EngineState) String() string {
	if s.IsLeader() {
		return "leader"
	}
	return "standby"
}

// Deps are the services the HTTP layer serves.
type Deps struct {
	Config  config.Config
	Pool    *pgxpool.Pool
	Engine  *EngineState
	Market  *market.Service
	Candles *market.Candles
	Hub     *hub.Hub
}

// New builds the HTTP handler.
func New(d Deps) http.Handler {
	cfg, pool := d.Config, d.Pool
	mux := http.NewServeMux()

	// Health passes once HTTP is up and the DB is reachable. It must not wait for the engine
	// lock, or Railway deploys deadlock (plan §8.1).
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			slog.Warn("health check: database unreachable", "err", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "database_unreachable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		var markets []string
		for _, t := range d.Market.Tickers() {
			markets = append(markets, t.Coin)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"version":         cfg.Version,
			"engine":          d.Engine.String(),
			"network":         cfg.HLNetwork,
			"markets":         markets,
			"startingBalance": cfg.StartingBalance.String(),
		})
	})

	mux.HandleFunc("GET /api/markets", handleMarkets(d.Market))
	mux.HandleFunc("GET /api/markets/{coin}/candles", handleCandles(d.Market, d.Candles))
	mux.HandleFunc("GET /ws", handleWS(d.Hub))

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
	})

	mux.Handle("/", spaHandler(cfg.WebDir))
	return logRequests(redirectWWW(mux))
}

// redirectWWW sends www.paperperps.lol to paperperps.lol so there is one canonical origin.
func redirectWWW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if apex, ok := strings.CutPrefix(r.Host, "www."); ok {
			http.Redirect(w, r, "https://"+apex+r.URL.RequestURI(), http.StatusMovedPermanently)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("write json response", "err", err)
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Unwrap lets http.ResponseController reach the underlying writer (needed for websockets).
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/healthz" {
			return // Railway polls this; logging it is noise.
		}
		slog.Info("http request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration", time.Since(start))
	})
}
