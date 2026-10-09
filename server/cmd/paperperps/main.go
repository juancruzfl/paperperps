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
	"syscall"
	"time"

	"github.com/juancruzfl/paperperps/server/internal/config"
	"github.com/juancruzfl/paperperps/server/internal/db"
	"github.com/juancruzfl/paperperps/server/internal/httpapi"
	"github.com/juancruzfl/paperperps/server/internal/leader"
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

	engine := &httpapi.EngineState{}
	srv := &http.Server{
		Addr:              net.JoinHostPort("", cfg.Port),
		Handler:           httpapi.New(cfg, pool, engine),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
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
		engine.SetLeader(true)
		slog.Info("engine lock acquired")
		// TODO(M1): start the market data service and engine here, stopping them before
		// releasing the lock.

		watchErr := lock.Watch(ctx, 5*time.Second)
		engine.SetLeader(false)
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := lock.Release(releaseCtx); err != nil {
			slog.Warn("release engine lock", "err", err)
		} else {
			slog.Info("engine lock released")
		}
		if watchErr != nil {
			fatal <- watchErr
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
