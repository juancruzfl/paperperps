// Package dbtest creates throwaway Postgres databases for tests, so suites don't see each
// other's rows (or rows left over from earlier runs).
package dbtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
)

var seq atomic.Int64

// Create makes an empty database next to the one in TEST_DATABASE_URL and returns its URL
// and a function that drops it. ok is false when TEST_DATABASE_URL isn't set.
func Create(prefix string) (dbURL string, drop func(), ok bool, err error) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		return "", func() {}, false, nil
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		return "", nil, true, err
	}
	name := fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano(), seq.Add(1))
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close(ctx)
		return "", nil, true, err
	}
	u, err := url.Parse(base)
	if err != nil {
		admin.Close(ctx)
		return "", nil, true, err
	}
	u.Path = "/" + name
	drop = func() {
		admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		admin.Close(context.Background())
	}
	return u.String(), drop, true, nil
}
