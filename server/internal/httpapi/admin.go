package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/juancruzfl/paperperps/server/internal/accounts"
	"github.com/juancruzfl/paperperps/server/internal/adminauth"
	"github.com/juancruzfl/paperperps/server/internal/engine"
	"github.com/juancruzfl/paperperps/server/internal/ratelimit"
)

// The admin API (docs/operations.md, "Admin tool") is for moderating accounts with the
// paperperps-admin CLI. It exists only when ADMIN_PUBLIC_KEY is set, and every request must
// be signed with the matching private key (package adminauth). Anything that fails the
// check gets the same 404 as an unknown /api/ path, so the routes can't be discovered.

const maxReasonLen = 500

type adminRoute struct {
	pattern string
	handler func(Deps) http.HandlerFunc
}

// adminRoutes lists every admin endpoint. They're all mounted behind requireAdmin, and the
// tests check each one rejects unsigned requests.
var adminRoutes = []adminRoute{
	{"GET /api/admin/accounts", handleAdminSearch},
	{"GET /api/admin/accounts/{id}", handleAdminShow},
	{"POST /api/admin/accounts/{id}/rename", handleAdminRename},
	{"POST /api/admin/accounts/{id}/leaderboard", handleAdminLeaderboard},
	{"POST /api/admin/accounts/{id}/suspension", handleAdminSuspension},
	{"POST /api/admin/accounts/{id}/reset", handleAdminReset},
	{"POST /api/admin/accounts/{id}/delete", handleAdminDelete},
	{"GET /api/admin/actions", handleAdminActions},
}

func mountAdmin(mux *http.ServeMux, d Deps) {
	if d.Config.AdminPublicKey == nil {
		return
	}
	for _, rt := range adminRoutes {
		mux.HandleFunc(rt.pattern, requireAdmin(d, rt.handler(d)))
	}
	slog.Warn("admin API enabled", "alert", "admin_enabled")
}

func notFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
}

// requireAdmin lets a request through only with a valid, fresh, unused signature.
func requireAdmin(d Deps, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Admin != nil {
			if ok, _ := d.Admin.Allow(ratelimit.ClientIP(r)); !ok {
				notFound(w)
				return
			}
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err != nil {
			notFound(w)
			return
		}
		v, err := adminauth.Verify(r, body, d.Config.AdminPublicKey, time.Now())
		if errors.Is(err, adminauth.ErrMissing) {
			notFound(w) // ordinary probing; not worth a log line
			return
		}
		if err != nil {
			// No IP address: the Privacy Policy says logs don't record them.
			slog.Warn("admin request rejected", "alert", "admin_auth_failed", "method", r.Method, "path", r.URL.Path, "err", err)
			notFound(w)
			return
		}
		fresh, err := d.Accounts.UseNonce(r.Context(), v.Nonce, v.Expires)
		if err != nil {
			internalError(w, "admin nonce", err)
			return
		}
		if !fresh {
			slog.Warn("admin request rejected", "alert", "admin_auth_failed", "method", r.Method, "path", r.URL.Path, "err", "replayed nonce")
			notFound(w)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next(w, r)
	}
}

// adminTarget reads the {id} path value.
func adminTarget(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "account id must be a UUID")
		return uuid.Nil, false
	}
	return id, true
}

// adminChange decodes a mutation's body into v and returns its reason, which is required.
func adminChange(w http.ResponseWriter, r *http.Request, v any, reason *string) bool {
	if !decodeBody(w, r, v) {
		return false
	}
	*reason = strings.TrimSpace(*reason)
	if *reason == "" || len(*reason) > maxReasonLen {
		writeError(w, http.StatusBadRequest, "invalid_reason", "give a reason (up to 500 characters); it goes in the audit log")
		return false
	}
	return true
}

// adminDone logs a completed admin action. The alert tag lets a log alert flag every one,
// so an action you didn't take stands out.
func adminDone(d Deps, action string, id uuid.UUID, reason string, attrs ...any) {
	slog.Warn("admin action", append([]any{"alert", "admin_action", "action", action, "account", id, "reason", reason}, attrs...)...)
	if d.Leaderboard != nil {
		d.Leaderboard.Invalidate()
	}
	if d.Hub != nil {
		d.Hub.Publish(engine.AccountTopic(id), engine.AccountEvent{Type: "moderation"})
	}
}

func adminStoreError(w http.ResponseWriter, what string, err error) {
	switch {
	case errors.Is(err, accounts.ErrNotFound):
		writeError(w, http.StatusNotFound, "account_not_found", "no such account")
	case errors.Is(err, accounts.ErrHandleInvalid):
		writeError(w, http.StatusBadRequest, "invalid_handle",
			"handles are 3–24 lowercase letters, digits, - or _, and can't contain reserved words")
	case errors.Is(err, accounts.ErrHandleTaken):
		writeError(w, http.StatusConflict, "handle_taken", "that handle is taken")
	default:
		engineError(w, what, err)
	}
}

func queryLimit(r *http.Request, def, most int) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		return def
	}
	return min(n, most)
}

func handleAdminSearch(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := d.Accounts.Search(r.Context(), r.URL.Query().Get("q"), queryLimit(r, 50, 200))
		if err != nil {
			internalError(w, "admin search", err)
			return
		}
		writeJSON(w, http.StatusOK, rows)
	}
}

func handleAdminShow(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := adminTarget(w, r)
		if !ok {
			return
		}
		a, err := d.Accounts.Load(r.Context(), id)
		if err != nil {
			adminStoreError(w, "admin show", err)
			return
		}
		actions, err := d.Accounts.Actions(r.Context(), &id, 20)
		if err != nil {
			internalError(w, "admin show actions", err)
			return
		}
		fills, err := d.Accounts.Fills(r.Context(), id, 10)
		if err != nil {
			internalError(w, "admin show fills", err)
			return
		}
		sum := engine.SummarizeAccount(a, d.Market)
		writeJSON(w, http.StatusOK, map[string]any{
			"account":     a,
			"summary":     sum,
			"lifetimePnl": engine.Score(a.PriorPnl, a.Balance, sum.UnrealizedPnl, a.StartingBalance).Round(2),
			"recentFills": fills,
			"actions":     actions,
		})
	}
}

func handleAdminRename(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := adminTarget(w, r)
		if !ok {
			return
		}
		var body struct {
			Handle string `json:"handle"` // empty: a random one
			Reason string `json:"reason"`
		}
		if !adminChange(w, r, &body, &body.Reason) {
			return
		}
		h, err := d.Accounts.AdminRename(r.Context(), id, body.Handle, body.Reason)
		if err != nil {
			adminStoreError(w, "admin rename", err)
			return
		}
		adminDone(d, "rename", id, body.Reason, "to", h)
		writeJSON(w, http.StatusOK, map[string]any{"handle": h})
	}
}

func handleAdminLeaderboard(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := adminTarget(w, r)
		if !ok {
			return
		}
		var body struct {
			Hidden *bool  `json:"hidden"`
			Reason string `json:"reason"`
		}
		if !adminChange(w, r, &body, &body.Reason) {
			return
		}
		if body.Hidden == nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "hidden must be true or false")
			return
		}
		if err := d.Accounts.SetLeaderboardHidden(r.Context(), id, *body.Hidden, body.Reason); err != nil {
			adminStoreError(w, "admin leaderboard", err)
			return
		}
		adminDone(d, map[bool]string{true: "hide", false: "unhide"}[*body.Hidden], id, body.Reason)
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleAdminSuspension(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := adminTarget(w, r)
		if !ok {
			return
		}
		var body struct {
			Suspended *bool  `json:"suspended"`
			Reason    string `json:"reason"`
		}
		if !adminChange(w, r, &body, &body.Reason) {
			return
		}
		if body.Suspended == nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "suspended must be true or false")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), engineTimeout)
		defer cancel()
		if err := d.Engine.SetSuspended(ctx, id, *body.Suspended, body.Reason); err != nil {
			engineError(w, "admin suspension", err)
			return
		}
		adminDone(d, map[bool]string{true: "suspend", false: "unsuspend"}[*body.Suspended], id, body.Reason)
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleAdminReset(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := adminTarget(w, r)
		if !ok {
			return
		}
		var body struct {
			Reason string `json:"reason"`
		}
		if !adminChange(w, r, &body, &body.Reason) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), engineTimeout)
		defer cancel()
		err := d.Engine.AdminReset(ctx, id, body.Reason)
		if errors.Is(err, accounts.ErrNotFound) {
			adminStoreError(w, "admin reset", err)
			return
		}
		if err != nil {
			engineError(w, "admin reset", err)
			return
		}
		adminDone(d, "reset", id, body.Reason)
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleAdminDelete(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := adminTarget(w, r)
		if !ok {
			return
		}
		var body struct {
			Reason string `json:"reason"`
		}
		if !adminChange(w, r, &body, &body.Reason) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), engineTimeout)
		defer cancel()
		if err := d.Engine.AdminDelete(ctx, id, body.Reason); err != nil {
			engineError(w, "admin delete", err)
			return
		}
		adminDone(d, "delete", id, body.Reason)
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleAdminActions(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var account *uuid.UUID
		if s := r.URL.Query().Get("account"); s != "" {
			id, err := uuid.Parse(s)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_id", "account must be a UUID")
				return
			}
			account = &id
		}
		rows, err := d.Accounts.Actions(r.Context(), account, queryLimit(r, 50, 500))
		if err != nil {
			internalError(w, "admin actions", err)
			return
		}
		writeJSON(w, http.StatusOK, rows)
	}
}
