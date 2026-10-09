// Package hyperliquid is a read-only client for Hyperliquid's public info and websocket API.
// Nothing here signs or places orders.
package hyperliquid

import (
	"encoding/json"
	"fmt"

	"github.com/shopspring/decimal"
)

// Level is one price level of an order book.
type Level struct {
	Px decimal.Decimal `json:"px"`
	Sz decimal.Decimal `json:"sz"`
	N  int             `json:"n"` // number of orders at this level
}

// L2Book is a full order book snapshot. Hyperliquid sends full snapshots, not diffs.
type L2Book struct {
	Coin   string     `json:"coin"`
	Time   int64      `json:"time"`   // unix ms
	Levels [2][]Level `json:"levels"` // [bids, asks], best first
}

func (b L2Book) Bids() []Level { return b.Levels[0] }
func (b L2Book) Asks() []Level { return b.Levels[1] }

const (
	SideBuy  = "B" // the taker bought
	SideSell = "A" // the taker sold
)

// Trade is one public trade.
type Trade struct {
	Coin string          `json:"coin"`
	Side string          `json:"side"`
	Px   decimal.Decimal `json:"px"`
	Sz   decimal.Decimal `json:"sz"`
	Time int64           `json:"time"` // unix ms
	Tid  int64           `json:"tid"`
	Hash string          `json:"hash"`
}

// AssetCtx is the live context of a perp: prices, funding, open interest and volume.
type AssetCtx struct {
	Funding      decimal.Decimal     `json:"funding"` // hourly rate
	OpenInterest decimal.Decimal     `json:"openInterest"`
	PrevDayPx    decimal.Decimal     `json:"prevDayPx"`
	DayNtlVlm    decimal.Decimal     `json:"dayNtlVlm"`
	Premium      decimal.NullDecimal `json:"premium"`
	OraclePx     decimal.Decimal     `json:"oraclePx"`
	MarkPx       decimal.Decimal     `json:"markPx"`
	MidPx        decimal.NullDecimal `json:"midPx"` // null when a side of the book is empty
}

// ActiveAssetCtx is the payload of the activeAssetCtx websocket channel.
type ActiveAssetCtx struct {
	Coin string   `json:"coin"`
	Ctx  AssetCtx `json:"ctx"`
}

// Candle is one OHLCV bar.
type Candle struct {
	OpenTime  int64           `json:"t"` // unix ms
	CloseTime int64           `json:"T"` // unix ms
	Coin      string          `json:"s"`
	Interval  string          `json:"i"`
	Open      decimal.Decimal `json:"o"`
	High      decimal.Decimal `json:"h"`
	Low       decimal.Decimal `json:"l"`
	Close     decimal.Decimal `json:"c"`
	Volume    decimal.Decimal `json:"v"` // base units
	Trades    int             `json:"n"`
}

// AssetMeta is a perp's static configuration.
type AssetMeta struct {
	Name          string `json:"name"`
	SzDecimals    int    `json:"szDecimals"`
	MaxLeverage   int    `json:"maxLeverage"`
	MarginTableID int    `json:"marginTableId"`
	OnlyIsolated  bool   `json:"onlyIsolated"`
	IsDelisted    bool   `json:"isDelisted"`
}

// Meta lists every perp. Universe order defines asset indexes.
type Meta struct {
	Universe []AssetMeta `json:"universe"`
}

// MetaAndAssetCtxs pairs each perp's metadata with its current context.
type MetaAndAssetCtxs struct {
	Meta Meta
	Ctxs []AssetCtx // same order as Meta.Universe
}

func (m *MetaAndAssetCtxs) UnmarshalJSON(data []byte) error {
	var raw [2]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("metaAndAssetCtxs: %w", err)
	}
	if err := json.Unmarshal(raw[0], &m.Meta); err != nil {
		return fmt.Errorf("metaAndAssetCtxs meta: %w", err)
	}
	if err := json.Unmarshal(raw[1], &m.Ctxs); err != nil {
		return fmt.Errorf("metaAndAssetCtxs ctxs: %w", err)
	}
	if len(m.Ctxs) != len(m.Meta.Universe) {
		return fmt.Errorf("metaAndAssetCtxs: %d assets but %d contexts", len(m.Meta.Universe), len(m.Ctxs))
	}
	return nil
}
