package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/juancruzfl/paperperps/server/internal/market"
)

func handleMarkets(m *market.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, m.Tickers())
	}
}

// handleCandles returns the last market.CandleBars bars: GET /api/markets/{coin}/candles?interval=1h
func handleCandles(m *market.Service, candles *market.Candles) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		coin := r.PathValue("coin")
		if !m.Has(coin) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown_market"})
			return
		}
		interval := r.URL.Query().Get("interval")
		if interval == "" {
			interval = "15m"
		}
		bars, err := candles.Get(r.Context(), coin, interval)
		switch {
		case errors.Is(err, market.ErrBadInterval):
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad_interval"})
			return
		case err != nil:
			slog.Warn("candles", "coin", coin, "interval", interval, "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "upstream_unavailable"})
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=5")
		writeJSON(w, http.StatusOK, bars)
	}
}
