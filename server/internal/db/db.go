// Package db opens the Postgres pool and applies schema migrations.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open connects to Postgres and verifies the connection.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}

// Migrate applies all pending migrations. A Postgres session lock makes it safe to run from
// two containers at once, which happens while Railway overlaps deploys.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	provider, sqlDB, err := newProvider(pool)
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	results, err := provider.Up(ctx)
	for _, r := range results {
		slog.Info("migration applied", "version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration)
	}
	if err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// MigrateReset rolls back every migration. Only tests use it.
func MigrateReset(ctx context.Context, pool *pgxpool.Pool) error {
	provider, sqlDB, err := newProvider(pool)
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	if _, err := provider.DownTo(ctx, 0); err != nil {
		return fmt.Errorf("migrate down: %w", err)
	}
	return nil
}

func newProvider(pool *pgxpool.Pool) (*goose.Provider, *sql.DB, error) {
	dir, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, nil, err
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, nil, err
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, dir, goose.WithSessionLocker(locker))
	if err != nil {
		sqlDB.Close()
		return nil, nil, fmt.Errorf("goose provider: %w", err)
	}
	return provider, sqlDB, nil
}
