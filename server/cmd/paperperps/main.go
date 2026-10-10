// Command paperperps runs the PaperPerps server: REST API, websocket, trading engine and
// the built frontend, all in one process.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shopspring/decimal"
	"golang.org/x/time/rate"

	"github.com/juancruzfl/paperperps/server/internal/accounts"
	"github.com/juancruzfl/paperperps/server/internal/config"
	"github.com/juancruzfl/paperperps/server/internal/db"
	"github.com/juancruzfl/paperperps/server/internal/engine"
	"github.com/juancruzfl/paperperps/server/internal/httpapi"
	"github.com/juancruzfl/paperperps/server/internal/hub"
	"github.com/juancruzfl/paperperps/server/internal/hyperliquid"
	"github.com/juancruzfl/paperperps/server/internal/leader"
	"github.com/juancruzfl/paperperps/server/internal/leaderboard"
	"github.com/juancruzfl/paperperps/server/internal/market"
	"github.com/juancruzfl/paperperps/server/internal/ratelimit"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	// Coins dropped from MARKETS that people still hold stay tracked as close-only, so those
	// positions can still be valued, liquidated and closed (docs/plan.md §4.1).
	store := accounts.NewStore(pool, cfg.StartingBalance)
	held, err := store.OpenPositionCoins(ctx)
	if err != nil {
		return err
	}
	var closeOnly []string
	for _, coin := range held {
		if !slices.Contains(cfg.Markets, coin) {
			closeOnly = append(closeOnly, coin)
		}
	}
	if len(closeOnly) > 0 {
		slog.Warn("markets no longer listed but still held: close-only", "coins", closeOnly)
	}

	// Market data is read-only, so it runs on every instance, not only the engine leader.
	info := hyperliquid.NewInfoClient(cfg.HLInfoURL())
	var markets *market.Service
	h := hub.New(func(topic string) (any, bool) {
		// Account topics have no snapshot (clients fetch /api/me); the websocket handler has
		// already checked ownership.
		if strings.HasPrefix(topic, "account:") {
			return nil, true
		}
		return markets.Snapshot(topic)
	})
	markets = market.New(market.Options{Markets: cfg.Markets, CloseOnly: closeOnly, Info: info, Publisher: h})
	go markets.Run(ctx, cfg.HLWebsocketURL())

	eng := engine.New(pool, markets, h, engine.Config{
		TakerFeeRate:    cfg.TakerFeeRate,
		MakerFeeRate:    cfg.MakerFeeRate,
		MaxSlippage:     decimal.RequireFromString("0.05"),
		MinNotional:     decimal.NewFromInt(10),
		DefaultLeverage: 20,
		LiquidationMode: cfg.LiquidationMode,
		StartingBalance: cfg.StartingBalance,
		ResetCooldown:   cfg.ResetCooldown,
	})
	srv := &http.Server{
		Addr: net.JoinHostPort("", cfg.Port),
		Handler: httpapi.New(httpapi.Deps{
			Config:   cfg,
			Pool:     pool,
			Accounts: store,
			Engine:   eng,
			Market:   markets,
			Candles:  market.NewCandles(info, 10*time.Second),
			Hub:      h,

			Leaderboard: leaderboard.New(pool, markets, cfg.LeaderboardMinTrades, cfg.LeaderboardMinAge, 30*time.Second),
			// 5 new accounts per IP per hour; 10 trading calls per second per account, bursts of 30.
			NewAccounts: ratelimit.New(rate.Every(12*time.Minute), 5),
			Trading:     ratelimit.New(10, 30),
			// Admin calls, per IP: 1 per second, bursts of 20.
			Admin: ratelimit.New(1, 20),
			// Public profiles, per IP: 5 per second, bursts of 30.
			Profiles: ratelimit.New(5, 30),
		}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// Shutdown doesn't close websockets, so cancel their contexts when we stop.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	// Any of these failing stops the process; Railway restarts it.
	fatal := make(chan error, 2)
	go func() {
		slog.Info("http listening", "addr", srv.Addr, "network", cfg.HLNetwork, "version", cfg.Version)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			fatal <- err
		}
	}()

	lockReleased := make(chan struct{})
	go func() {
		defer close(lockReleased)
		slog.Info("waiting for engine lock")
		lock, err := leader.Acquire(ctx, cfg.DatabaseURL, leader.EngineLockKey, 2*time.Second)
		if err != nil {
			if ctx.Err() == nil {
				fatal <- err
			}
			return
		}
		slog.Info("engine lock acquired")

		// The engine runs only while we hold the lock, and must stop writing before the lock
		// is released. Either one failing stops both.
		engCtx, stopEngine := context.WithCancel(ctx)
		var runErr error
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer stopEngine()
			markets.SetObserver(eng)
			defer markets.SetObserver(nil)
			runErr = eng.Run(engCtx)
		}()
		watchErr := lock.Watch(engCtx, 5*time.Second)
		stopEngine()
		wg.Wait()
		if watchErr == nil && runErr == nil && ctx.Err() == nil {
			runErr = errors.New("engine stopped unexpectedly")
		}

		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := lock.Release(releaseCtx); err != nil {
			slog.Warn("release engine lock", "err", err)
		} else {
			slog.Info("engine lock released")
		}
		if err := errors.Join(watchErr, runErr); err != nil {
			fatal <- err
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutting down")
		err = nil
	case err = <-fatal:
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if shutdownErr := srv.Shutdown(shutdownCtx); shutdownErr != nil {
		slog.Warn("http shutdown", "err", shutdownErr)
	}
	<-lockReleased
	return err
}
