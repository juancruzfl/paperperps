package hyperliquid

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// InfoClient calls the REST info endpoint.
type InfoClient struct {
	URL  string
	HTTP *http.Client
}

func NewInfoClient(url string) *InfoClient {
	return &InfoClient{URL: url, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

// MetaAndAssetCtxs returns every perp's metadata and live context.
func (c *InfoClient) MetaAndAssetCtxs(ctx context.Context) (MetaAndAssetCtxs, error) {
	var out MetaAndAssetCtxs
	err := c.post(ctx, map[string]any{"type": "metaAndAssetCtxs"}, &out)
	return out, err
}

// CandleSnapshot returns bars for coin between start and end (inclusive). Hyperliquid
// returns at most 5000 bars.
func (c *InfoClient) CandleSnapshot(ctx context.Context, coin, interval string, start, end time.Time) ([]Candle, error) {
	var out []Candle
	err := c.post(ctx, map[string]any{
		"type": "candleSnapshot",
		"req": map[string]any{
			"coin":      coin,
			"interval":  interval,
			"startTime": start.UnixMilli(),
			"endTime":   end.UnixMilli(),
		},
	}, &out)
	return out, err
}

func (c *InfoClient) post(ctx context.Context, body any, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("hyperliquid info: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("hyperliquid info: status %d: %s", res.StatusCode, bytes.TrimSpace(msg))
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("hyperliquid info: decode: %w", err)
	}
	return nil
}
