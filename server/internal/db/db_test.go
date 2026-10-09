package db

import (
	"context"
	"os"
	"testing"
)

func TestMigrateUpDownUp(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
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
