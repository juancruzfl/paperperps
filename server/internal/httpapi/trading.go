package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/juancruzfl/paperperps/server/internal/accounts"
	"github.com/juancruzfl/paperperps/server/internal/engine"
	"github.com/juancruzfl/paperperps/server/internal/market"
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
}

func handleMe(store *accounts.Store, md *market.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, err := store.Load(r.Context(), accountID(r))
		if err != nil {
			internalError(w, "load account", err)
			return
		}
		writeJSON(w, http.StatusOK, meResponse{Account: a, Summary: engine.SummarizeAccount(a, md)})
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
