package db

import (
	"context"
	"testing"

	"github.com/juancruzfl/paperperps/server/internal/dbtest"
)

// TestMigrateUpDownUp runs in its own database: rolling migrations back would break other
// packages' tests, which share TEST_DATABASE_URL and run in parallel.
func TestMigrateUpDownUp(t *testing.T) {
	url, drop, ok, err := dbtest.Create("paperperps_migrate")
	if !ok {
		t.Skip("TEST_DATABASE_URL not set")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer drop()
	ctx := context.Background()
	pool, err := Open(ctx, url)
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
