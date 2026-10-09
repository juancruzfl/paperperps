// Package leader makes sure only one process runs the trading engine at a time.
//
// Railway can briefly run an old and a new container side by side during a deploy. Both
// would otherwise match orders, charge funding and liquidate. The leader holds a Postgres
// session advisory lock on its own dedicated connection. If the process dies, the
// connection closes and Postgres releases the lock. See docs/plan.md §8.1.
package leader

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// EngineLockKey is the advisory lock key for the trading engine. Any constant works as long
// as nothing else in the database uses it.
const EngineLockKey int64 = 7_270_013_001

// Lock is a held advisory lock.
type Lock struct {
	conn *pgx.Conn
	key  int64
}

// Acquire opens a dedicated connection and blocks until it holds the lock for key, retrying
// every interval, or until ctx is done.
func Acquire(ctx context.Context, databaseURL string, key int64, interval time.Duration) (*Lock, error) {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		var ok bool
		if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&ok); err != nil {
			conn.Close(context.Background())
			return nil, fmt.Errorf("try advisory lock: %w", err)
		}
		if ok {
			return &Lock{conn: conn, key: key}, nil
		}
		select {
		case <-ctx.Done():
			conn.Close(context.Background())
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// Watch pings the lock's connection every interval. It returns nil when ctx is done, or an
// error as soon as the connection fails, after which the lock must be considered lost.
func (l *Lock) Watch(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, interval)
			err := l.conn.Ping(pingCtx)
			cancel()
			if err != nil && ctx.Err() == nil {
				return fmt.Errorf("lock connection lost: %w", err)
			}
		}
	}
}

// Release unlocks and closes the connection. Closing alone would also release the lock;
// unlocking first just makes it explicit.
func (l *Lock) Release(ctx context.Context) error {
	_, err := l.conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", l.key)
	if closeErr := l.conn.Close(ctx); err == nil {
		err = closeErr
	}
	return err
}
