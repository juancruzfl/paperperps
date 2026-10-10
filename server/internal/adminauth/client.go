package adminauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client calls the admin API with signed requests.
type Client struct {
	BaseURL string // e.g. https://paperperps.lol
	Key     ed25519.PrivateKey
	HTTP    *http.Client
}

// APIError is a non-2xx response.
type APIError struct {
	Status  int
	Code    string `json:"error"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	if e.Status == http.StatusNotFound && e.Code == "not_found" && e.Message == "" {
		return "404 not found: the admin API is off (ADMIN_PUBLIC_KEY unset), or the server rejected the " +
			"signature (wrong key, or this machine's clock is more than a minute off)"
	}
	if e.Message != "" {
		return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("%d %s", e.Status, e.Code)
}

// Do sends a signed request. in, if not nil, is sent as JSON; a JSON response is decoded
// into out, if not nil.
func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	req, err := c.NewRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		apiErr := &APIError{Status: res.StatusCode}
		json.Unmarshal(data, apiErr)
		return apiErr
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// NewRequest builds a signed request. Its body can be sent once; the signature's nonce
// makes a second send a rejected replay.
func (c *Client) NewRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if err := Sign(req, body, c.Key, time.Now()); err != nil {
		return nil, err
	}
	return req, nil
}
