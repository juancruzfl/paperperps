package accounts

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/juancruzfl/paperperps/server/internal/db"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return NewStore(pool, decimal.NewFromInt(1000))
}

func TestCreateAuthenticateLoad(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	c, err := s.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Secret) != 43 || c.Handle == "" {
		t.Fatalf("created = %+v", c)
	}

	id, err := s.Authenticate(ctx, c.Secret)
	if err != nil || id != c.ID {
		t.Fatalf("Authenticate = %v, %v", id, err)
	}
	for _, bad := range []string{"", "nope", c.Secret + "x"} {
		if _, err := s.Authenticate(ctx, bad); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("Authenticate(%q) err = %v", bad, err)
		}
	}

	a, err := s.Load(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Balance.Equal(decimal.NewFromInt(1000)) || a.Handle != c.Handle || len(a.Positions) != 0 {
		t.Errorf("account = %+v", a)
	}

	// The cached balance must match the ledger.
	var ledger decimal.Decimal
	if err := s.pool.QueryRow(ctx, `SELECT sum(amount) FROM ledger_entries WHERE run_id = $1`, a.RunID).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	if !ledger.Equal(a.Balance) {
		t.Errorf("ledger sum %s != balance %s", ledger, a.Balance)
	}

	if _, err := s.Load(ctx, NewID()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Load(unknown) err = %v", err)
	}
}

func TestHandlesAreUnique(t *testing.T) {
	s := testStore(t)
	seen := map[string]bool{}
	for range 20 {
		c, err := s.Create(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if seen[c.Handle] {
			t.Fatalf("duplicate handle %s", c.Handle)
		}
		seen[c.Handle] = true
	}
}
