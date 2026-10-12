package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestFillNotes(t *testing.T) {
	env := newAdminEnv(t)
	ctx := context.Background()
	me, err := env.store.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	other, err := env.store.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fill := func(account uuid.UUID) uuid.UUID {
		t.Helper()
		id := uuid.New()
		if _, err := env.pool.Exec(ctx, `INSERT INTO fills (id, account_id, run_id, coin, side, px, sz, fee, liquidity)
			SELECT $1, id, current_run_id, 'ETH', 'buy', 100, 1, 0, 'taker' FROM accounts WHERE id = $2`, id, account); err != nil {
			t.Fatal(err)
		}
		return id
	}
	mine, theirs := fill(me.ID), fill(other.ID)

	do := func(method, path, secret, body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, env.srv.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+secret)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { res.Body.Close() })
		return res
	}
	note := func() string {
		t.Helper()
		var fills []struct {
			ID   uuid.UUID `json:"id"`
			Note string    `json:"note"`
		}
		json.NewDecoder(do("GET", "/api/me/fills", me.Secret, "").Body).Decode(&fills)
		if len(fills) != 1 || fills[0].ID != mine {
			t.Fatalf("fills = %+v", fills)
		}
		return fills[0].Note
	}

	put := func(id uuid.UUID, secret, text string) int {
		b, _ := json.Marshal(map[string]string{"note": text})
		return do("PUT", "/api/me/fills/"+id.String()+"/note", secret, string(b)).StatusCode
	}
	if s := put(mine, me.Secret, "  chased the breakout  "); s != http.StatusNoContent {
		t.Fatalf("save: status %d", s)
	}
	if n := note(); n != "chased the breakout" {
		t.Errorf("note = %q", n)
	}
	if s := put(mine, me.Secret, "waited for the retest"); s != http.StatusNoContent || note() != "waited for the retest" {
		t.Errorf("edit: status %d, note %q", s, note())
	}
	if s := put(mine, me.Secret, strings.Repeat("é", 1001)); s != http.StatusBadRequest {
		t.Errorf("too long: status %d", s)
	}
	if s := put(theirs, me.Secret, "not mine"); s != http.StatusNotFound {
		t.Errorf("someone else's fill: status %d", s)
	}
	if s := put(mine, me.Secret, ""); s != http.StatusNoContent || note() != "" {
		t.Errorf("clear: status %d, note %q", s, note())
	}
}
