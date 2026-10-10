package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/juancruzfl/paperperps/server/internal/accounts"
	"github.com/juancruzfl/paperperps/server/internal/engine"
	"github.com/juancruzfl/paperperps/server/internal/leaderboard"
	"github.com/juancruzfl/paperperps/server/internal/market"
	"github.com/juancruzfl/paperperps/server/internal/ratelimit"
)

const (
	historyLimit  = 200
	engineTimeout = 5 * time.Second
	maxBodyBytes  = 4 << 10
)

type ctxKey struct{}

func accountID(r *http.Request) uuid.UUID { return r.Context().Value(ctxKey{}).(uuid.UUID) }

func bearer(r *http.Request) string {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(token)
}

// requireAccount authenticates the request's bearer secret.
func requireAccount(store *accounts.Store, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := store.Authenticate(r.Context(), bearer(r))
		if errors.Is(err, accounts.ErrUnauthorized) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or unknown account key")
			return
		}
		if err != nil {
			internalError(w, "authenticate", err)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

func internalError(w http.ResponseWriter, what string, err error) {
	slog.Error("request failed", "what", what, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "something went wrong")
}

// engineError maps engine errors to responses.
func engineError(w http.ResponseWriter, what string, err error) {
	var rej *engine.RejectError
	switch {
	case errors.As(err, &rej):
		status := http.StatusBadRequest
		if rej.Code == "not_found" {
			status = http.StatusNotFound
		}
		writeJSON(w, status, rej)
	case errors.Is(err, engine.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "engine_unavailable", "trading is restarting; try again in a few seconds")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "timeout", "the trading engine took too long to respond")
	default:
		internalError(w, what, err)
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON for this endpoint")
		return false
	}
	return true
}

func handleCreateAccount(store *accounts.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := store.Create(r.Context())
		if err != nil {
			internalError(w, "create account", err)
			return
		}
		slog.Info("account created", "account", c.ID, "handle", c.Handle)
		writeJSON(w, http.StatusCreated, c)
	}
}

// meResponse is the account's state plus a margin summary at live marks.
type meResponse struct {
	accounts.Account
	Summary engine.Summary `json:"summary"`
	// LifetimePnl is the leaderboard score: PnL across every run, at live marks.
	LifetimePnl decimal.Decimal `json:"lifetimePnl"`
	// ResetAvailableAt is when the next reset is allowed; null means now.
	ResetAvailableAt *time.Time `json:"resetAvailableAt"`
}

func handleMe(store *accounts.Store, md *market.Service, cooldown time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, err := store.Load(r.Context(), accountID(r))
		if err != nil {
			internalError(w, "load account", err)
			return
		}
		sum := engine.SummarizeAccount(a, md)
		res := meResponse{
			Account:     a,
			Summary:     sum,
			LifetimePnl: engine.Score(a.PriorPnl, a.Balance, sum.UnrealizedPnl, a.StartingBalance).Round(6),
		}
		if a.LastResetAt != nil {
			if next := a.LastResetAt.Add(cooldown); time.Now().Before(next) {
				res.ResetAvailableAt = &next
			}
		}
		writeJSON(w, http.StatusOK, res)
	}
}

// limit rejects requests over the limiter's rate for their key with 429.
func limit(l *ratelimit.Limiter, key func(*http.Request) string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if l != nil {
			if ok, wait := l.Allow(key(r)); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
				writeError(w, http.StatusTooManyRequests, "rate_limited",
					fmt.Sprintf("too many requests; try again in %s", wait.Round(time.Second)))
				return
			}
		}
		next(w, r)
	}
}

func handleReset(eng *engine.Engine, lb *leaderboard.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), engineTimeout)
		defer cancel()
		if err := eng.ResetAccount(ctx, accountID(r)); err != nil {
			engineError(w, "reset account", err)
			return
		}
		lb.Invalidate()
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleUpdateProfile(store *accounts.Store, lb *leaderboard.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var p accounts.Profile
		if !decodeBody(w, r, &p) {
			return
		}
		switch err := store.UpdateProfile(r.Context(), accountID(r), p); {
		case errors.Is(err, accounts.ErrHandleInvalid):
			writeError(w, http.StatusBadRequest, "invalid_handle",
				"handles are 3–24 lowercase letters, digits, - or _, and can't contain reserved words")
		case errors.Is(err, accounts.ErrHandleTaken):
			writeError(w, http.StatusConflict, "handle_taken", "that handle is taken")
		case err != nil:
			internalError(w, "update profile", err)
		default:
			lb.Invalidate()
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

func handleEquity(store *accounts.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		points, err := store.Equity(r.Context(), accountID(r), time.Now())
		if err != nil {
			internalError(w, "equity history", err)
			return
		}
		if points == nil {
			points = []accounts.EquityPoint{}
		}
		writeJSON(w, http.StatusOK, points)
	}
}

// handleLeaderboard serves GET /api/leaderboard?window=24h|7d|30d|all. Authentication is
// optional; with it the response includes the viewer's own standing.
func handleLeaderboard(lb *leaderboard.Service, store *accounts.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		window := r.URL.Query().Get("window")
		if window == "" {
			window = "all"
		}
		viewer := uuid.Nil
		if secret := bearer(r); secret != "" {
			if id, err := store.Authenticate(r.Context(), secret); err == nil {
				viewer = id
			}
		}
		board, err := lb.Get(r.Context(), window, viewer)
		if errors.Is(err, leaderboard.ErrBadWindow) {
			writeError(w, http.StatusBadRequest, "bad_window", "window must be 24h, 7d, 30d or all")
			return
		}
		if err != nil {
			internalError(w, "leaderboard", err)
			return
		}
		writeJSON(w, http.StatusOK, board)
	}
}

func handleHistory[T any](what string, list func(context.Context, uuid.UUID, int) ([]T, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := list(r.Context(), accountID(r), historyLimit)
		if err != nil {
			internalError(w, what, err)
			return
		}
		if rows == nil {
			rows = []T{}
		}
		writeJSON(w, http.StatusOK, rows)
	}
}

func handlePlaceOrder(eng *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req engine.OrderRequest
		if !decodeBody(w, r, &req) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), engineTimeout)
		defer cancel()
		res, err := eng.PlaceOrder(ctx, accountID(r), req)
		if err != nil {
			engineError(w, "place order", err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func handleCancelOrder(eng *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orderID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", "no open order with that id")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), engineTimeout)
		defer cancel()
		if err := eng.CancelOrder(ctx, accountID(r), orderID); err != nil {
			engineError(w, "cancel order", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleSetLeverage(eng *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Leverage int `json:"leverage"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), engineTimeout)
		defer cancel()
		if err := eng.SetLeverage(ctx, accountID(r), r.PathValue("coin"), body.Leverage); err != nil {
			engineError(w, "set leverage", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleSetMarginMode(eng *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Mode string `json:"mode"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), engineTimeout)
		defer cancel()
		if err := eng.SetMarginMode(ctx, accountID(r), r.PathValue("coin"), body.Mode); err != nil {
			engineError(w, "set margin mode", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleAdjustIsolatedMargin adds (positive amount) or removes (negative) isolated margin.
func handleAdjustIsolatedMargin(eng *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Amount decimal.Decimal `json:"amount"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), engineTimeout)
		defer cancel()
		if err := eng.AdjustIsolatedMargin(ctx, accountID(r), r.PathValue("coin"), body.Amount); err != nil {
			engineError(w, "adjust isolated margin", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleModifyOrder(eng *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orderID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", "no open order with that id")
			return
		}
		var req engine.ModifyRequest
		if !decodeBody(w, r, &req) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), engineTimeout)
		defer cancel()
		o, err := eng.ModifyOrder(ctx, accountID(r), orderID, req)
		if err != nil {
			engineError(w, "modify order", err)
			return
		}
		writeJSON(w, http.StatusOK, o)
	}
}

// handleSetPositionTPSL replaces the open position's TP/SL: {takeProfit?, stopLoss?}.
func handleSetPositionTPSL(eng *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			TakeProfit decimal.NullDecimal `json:"takeProfit"`
			StopLoss   decimal.NullDecimal `json:"stopLoss"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), engineTimeout)
		defer cancel()
		orders, err := eng.SetPositionTPSL(ctx, accountID(r), r.PathValue("coin"), body.TakeProfit, body.StopLoss)
		if err != nil {
			engineError(w, "set position tp/sl", err)
			return
		}
		if orders == nil {
			orders = []accounts.Order{}
		}
		writeJSON(w, http.StatusOK, orders)
	}
}

// handleDeleteAccount permanently deletes the caller's account. The body must be
// {"confirm": "delete"} so a stray request can't do it.
func handleDeleteAccount(eng *engine.Engine, lb *leaderboard.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Confirm string `json:"confirm"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		if body.Confirm != "delete" {
			writeError(w, http.StatusBadRequest, "confirmation_required", `send {"confirm": "delete"} to delete this account`)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), engineTimeout)
		defer cancel()
		if err := eng.DeleteAccount(ctx, accountID(r)); err != nil {
			engineError(w, "delete account", err)
			return
		}
		lb.Invalidate()
		w.WriteHeader(http.StatusNoContent)
	}
}
