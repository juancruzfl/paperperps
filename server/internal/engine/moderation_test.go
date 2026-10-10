package engine

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func (h *harness) auditActions(id uuid.UUID) []string {
	h.t.Helper()
	rows, err := h.pool.Query(context.Background(), `SELECT action FROM admin_actions WHERE account_id = $1 ORDER BY id`, id)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		rows.Scan(&a)
		out = append(out, a)
	}
	return out
}

func TestSuspendedAccountCanOnlyClose(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id := h.newAccount()
	h.submit(id, req(SideBuy, TypeMarket, "0.02").tp("90000"))         // long, with a reduce-only TP
	entry := h.submit(id, req(SideBuy, TypeLimit, "0.01").px("70000")) // could add to the long
	h.submit(id, req(SideSell, TypeLimit, "0.01").px("95000").reduceOnly())

	if err := h.eng.SetSuspended(ctx, id, true, "multi-accounting"); err != nil {
		t.Fatal(err)
	}
	a := h.load(id)
	if a.Suspension == nil || a.Suspension.Reason != "multi-accounting" {
		t.Fatalf("suspension = %+v", a.Suspension)
	}
	for _, o := range a.OpenOrders {
		if !o.ReduceOnly || o.ID == entry.Order.ID {
			t.Errorf("order %s (%s) survived the suspension", o.ID, o.Type)
		}
	}
	if len(a.OpenOrders) != 2 {
		t.Errorf("open orders = %d, want the TP and the reduce-only exit", len(a.OpenOrders))
	}

	wantReject(t, h.submitErr(id, req(SideBuy, TypeMarket, "0.01")), "account_suspended")
	wantReject(t, h.submitErr(id, req(SideSell, TypeMarket, "0.05")), "account_suspended") // would flip short
	wantReject(t, h.eng.ResetAccount(ctx, id), "account_suspended")
	h.submit(id, req(SideSell, TypeMarket, "0.01").reduceOnly()) // closing is allowed

	// The suspension survives a restart.
	h.start()
	wantReject(t, h.submitErr(id, req(SideBuy, TypeMarket, "0.01")), "account_suspended")

	if err := h.eng.SetSuspended(ctx, id, false, "appeal accepted"); err != nil {
		t.Fatal(err)
	}
	if a := h.load(id); a.Suspension != nil {
		t.Errorf("still suspended: %+v", a.Suspension)
	}
	h.submit(id, req(SideBuy, TypeMarket, "0.01"))

	if got := h.auditActions(id); len(got) != 2 || got[0] != "suspend" || got[1] != "unsuspend" {
		t.Errorf("audit log = %v", got)
	}
	wantReject(t, h.eng.SetSuspended(ctx, uuid.New(), true, "x"), "not_found")
}

func TestAdminResetIgnoresCooldown(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.setClock(time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC))
	h.start()
	cfg := h.eng.cfg
	cfg.ResetCooldown = 24 * time.Hour
	h.eng.cfg = cfg

	id := h.newAccount()
	if err := h.eng.ResetAccount(ctx, id); err != nil {
		t.Fatal(err)
	}
	wantReject(t, h.eng.ResetAccount(ctx, id), "reset_cooldown")
	if err := h.eng.SetSuspended(ctx, id, true, "griefing"); err != nil {
		t.Fatal(err)
	}
	if err := h.eng.AdminReset(ctx, id, "wipe exploit gains"); err != nil {
		t.Fatalf("admin reset during the cooldown, while suspended: %v", err)
	}
	if a := h.load(id); a.Resets != 2 {
		t.Errorf("resets = %d, want 2", a.Resets)
	}
	if got := h.auditActions(id); len(got) != 2 || got[1] != "reset" {
		t.Errorf("audit log = %v", got)
	}
}

func TestAdminDeleteKeepsAuditRecord(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id := h.newAccount()
	handle := h.load(id).Handle
	h.submit(id, req(SideBuy, TypeLimit, "0.01").px("70000"))

	if err := h.eng.AdminDelete(ctx, id, "illegal content in handle"); err != nil {
		t.Fatal(err)
	}
	var n int
	h.pool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE id = $1`, id).Scan(&n)
	if n != 0 {
		t.Error("account still exists")
	}
	var action, logged, reason string
	if err := h.pool.QueryRow(ctx, `SELECT action, handle, reason FROM admin_actions WHERE account_id = $1`, id).
		Scan(&action, &logged, &reason); err != nil {
		t.Fatal(err)
	}
	if action != "delete" || logged != handle || reason != "illegal content in handle" {
		t.Errorf("audit = %s %s %q", action, logged, reason)
	}
	wantReject(t, h.eng.AdminDelete(ctx, id, "again"), "not_found")
}
