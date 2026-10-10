// Package httpapi serves the REST API, health check and built frontend from one origin.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juancruzfl/paperperps/server/internal/accounts"
	"github.com/juancruzfl/paperperps/server/internal/config"
	"github.com/juancruzfl/paperperps/server/internal/engine"
	"github.com/juancruzfl/paperperps/server/internal/hub"
	"github.com/juancruzfl/paperperps/server/internal/leaderboard"
	"github.com/juancruzfl/paperperps/server/internal/market"
	"github.com/juancruzfl/paperperps/server/internal/ratelimit"
)

// Deps are the services the HTTP layer serves.
type Deps struct {
	Config   config.Config
	Pool     *pgxpool.Pool
	Accounts *accounts.Store
	Engine   *engine.Engine
	Market   *market.Service
	Candles  *market.Candles
	Hub      *hub.Hub

	Leaderboard *leaderboard.Service
	// NewAccounts limits account creation per IP; Trading limits trading calls per account.
	NewAccounts *ratelimit.Limiter
	Trading     *ratelimit.Limiter
	// Admin limits admin API calls per IP, before signatures are checked.
	Admin *ratelimit.Limiter
	// Profiles limits public profile lookups per IP.
	Profiles *ratelimit.Limiter
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
		engineState := "standby"
		if d.Engine.Running() {
			engineState = "leader"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"version":         cfg.Version,
			"engine":          engineState,
			"network":         cfg.HLNetwork,
			"markets":         markets,
			"startingBalance": cfg.StartingBalance.String(),
		})
	})

	mux.HandleFunc("GET /api/health", handleHealth(d))
	mux.HandleFunc("GET /api/markets", handleMarkets(d.Market))
	mux.HandleFunc("GET /api/markets/{coin}/candles", handleCandles(d.Market, d.Candles))
	mux.HandleFunc("GET /ws", handleWS(d.Hub, d.Accounts.Authenticate))

	auth := func(h http.HandlerFunc) http.HandlerFunc { return requireAccount(d.Accounts, h) }
	// trade is for calls that change trading state: authenticated and rate limited per account.
	trade := func(h http.HandlerFunc) http.HandlerFunc {
		return auth(limit(d.Trading, func(r *http.Request) string { return accountID(r).String() }, h))
	}
	mux.HandleFunc("POST /api/accounts", limit(d.NewAccounts, ratelimit.ClientIP, handleCreateAccount(d.Accounts)))
	mux.HandleFunc("GET /api/me", auth(handleMe(d.Accounts, d.Market, cfg.ResetCooldown)))
	mux.HandleFunc("PATCH /api/me", trade(handleUpdateProfile(d.Accounts, d.Leaderboard)))
	mux.HandleFunc("POST /api/me/reset", trade(handleReset(d.Engine, d.Leaderboard)))
	mux.HandleFunc("DELETE /api/me", trade(handleDeleteAccount(d.Engine, d.Leaderboard)))
	mux.HandleFunc("GET /api/me/equity", auth(handleEquity(d.Accounts)))
	mux.HandleFunc("GET /api/leaderboard", handleLeaderboard(d.Leaderboard, d.Accounts))
	mux.HandleFunc("GET /api/traders/{handle}", limit(d.Profiles, ratelimit.ClientIP, handleTrader(d)))
	mux.HandleFunc("GET /api/me/orders", auth(handleHistory("order history", d.Accounts.OrderHistory)))
	mux.HandleFunc("GET /api/me/fills", auth(handleHistory("fills", d.Accounts.Fills)))
	mux.HandleFunc("GET /api/me/funding", auth(handleHistory("funding", d.Accounts.Funding)))
	mux.HandleFunc("PUT /api/me/leverage/{coin}", trade(handleSetLeverage(d.Engine)))
	mux.HandleFunc("PUT /api/me/margin-mode/{coin}", trade(handleSetMarginMode(d.Engine)))
	mux.HandleFunc("POST /api/me/isolated-margin/{coin}", trade(handleAdjustIsolatedMargin(d.Engine)))
	mux.HandleFunc("POST /api/orders", trade(handlePlaceOrder(d.Engine)))
	mux.HandleFunc("DELETE /api/orders/{id}", trade(handleCancelOrder(d.Engine)))
	mux.HandleFunc("POST /api/orders/{id}/modify", trade(handleModifyOrder(d.Engine)))
	mux.HandleFunc("PUT /api/me/tpsl/{coin}", trade(handleSetPositionTPSL(d.Engine)))

	mountAdmin(mux, d)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { notFound(w) })

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

// handleHealth is for uptime monitors and people: 200 when the database is reachable and
// prices are live, 503 otherwise, with details either way. Unlike /healthz (which Railway
// uses to gate deploys) it fails when the Hyperliquid feed is down, because then nobody
// can trade.
func handleHealth(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		dbErr := d.Pool.Ping(ctx)
		feed := d.Market.Health()
		res := map[string]any{
			"version":  d.Config.Version,
			"database": dbErr == nil,
			"feed":     feed,
			"engine":   d.Engine.Stats(ctx),
		}
		problems := []string{}
		if dbErr != nil {
			problems = append(problems, "database unreachable")
		}
		if !feed.Healthy(market.StaleAlertAfter) {
			problems = append(problems, "market data stale")
		}
		res["ok"] = len(problems) == 0
		res["problems"] = problems
		status := http.StatusOK
		if len(problems) > 0 {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, res)
	}
}
