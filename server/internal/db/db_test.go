package db

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"
)

// TestMigrateUpDownUp runs in its own database: rolling migrations back would break other
// packages' tests, which share TEST_DATABASE_URL and run in parallel.
func TestMigrateUpDownUp(t *testing.T) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := Open(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("paperperps_migrate_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")

	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("up: %v", err)
	}
	if err := MigrateReset(ctx, pool); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("up again: %v", err)
	}

	// The pool must still work after goose's database/sql wrapper is closed.
	var n int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name IN
		('accounts','runs','positions','orders','fills','ledger_entries','funding_payments','equity_snapshots')`).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 8 {
		t.Errorf("found %d tables, want 8", n)
	}
}
