package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/juancruzfl/paperperps/server/internal/accounts"
	"github.com/juancruzfl/paperperps/server/internal/sharecard"
)

// Link previews for profiles. Chat apps and social sites fetch a pasted /u/<handle> link
// without running JavaScript, so the server fills in that trader's preview tags in
// index.html and renders their card as a PNG for the preview image.

var profilePath = regexp.MustCompile(`^/u/([a-z0-9_-]{1,32})/?$`)

// origin is the site's public origin as the request saw it. Railway terminates TLS and
// sets X-Forwarded-Proto.
func origin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// profileSummary is the one-line description in a profile's preview.
func profileSummary(p traderProfile) string {
	pnl, _ := strconv.ParseFloat(p.LifetimePnl, 64)
	parts := []string{sharecard.FormatUSD(pnl, true) + " lifetime PnL on paper"}
	if p.ClosedTrades > 0 {
		parts = append(parts, fmt.Sprintf("%.0f%% win rate", float64(p.Wins)/float64(p.ClosedTrades)*100))
	}
	if p.Rank > 0 {
		parts = append(parts, fmt.Sprintf("#%d all time", p.Rank))
	}
	return strings.Join(parts, " · ") + ". Paper-trade perps on live prices, no login."
}

// profilePreview rewrites index.html's preview tags for a public profile. Anything else
// (other pages, unknown or hidden handles) keeps the site's own tags.
func profilePreview(d Deps) func(r *http.Request, index []byte) []byte {
	return func(r *http.Request, index []byte) []byte {
		m := profilePath.FindStringSubmatch(strings.ToLower(r.URL.Path))
		if m == nil {
			return nil
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		p, err := loadProfile(ctx, d, m[1])
		if err != nil {
			if !errors.Is(err, errNoProfile) {
				slog.Warn("profile preview", "err", err)
			}
			return nil
		}
		base := origin(r)
		page := base + "/u/" + url.PathEscape(p.Handle)
		// The version changes hourly, so previews refresh without hammering the renderer.
		image := fmt.Sprintf("%s/api/traders/%s/card.png?v=%d", base, url.PathEscape(p.Handle), time.Now().Unix()/3600)
		title := p.Handle + " on PaperPerps"
		desc := profileSummary(p)
		out := setTitle(index, title)
		for _, t := range []struct{ attr, key, value string }{
			{"property", "og:title", title},
			{"property", "og:description", desc},
			{"property", "og:url", page},
			{"property", "og:image", image},
			{"property", "og:image:alt", title + ": " + desc},
			{"name", "twitter:title", title},
			{"name", "twitter:description", desc},
			{"name", "twitter:image", image},
			{"name", "description", desc},
		} {
			out = setMeta(out, t.attr, t.key, t.value)
		}
		return setCanonical(out, page)
	}
}

// setMeta replaces the content of <meta attr="key" content="…">.
func setMeta(page []byte, attr, key, value string) []byte {
	re := regexp.MustCompile(`(<meta\s+` + attr + `="` + regexp.QuoteMeta(key) + `"\s+content=")[^"]*(")`)
	return re.ReplaceAll(page, []byte("${1}"+escapeRepl(value)+"${2}"))
}

var (
	titleTag     = regexp.MustCompile(`<title>[^<]*</title>`)
	canonicalTag = regexp.MustCompile(`(<link\s+rel="canonical"\s+href=")[^"]*(")`)
)

func setTitle(page []byte, title string) []byte {
	return titleTag.ReplaceAll(page, []byte("<title>"+escapeRepl(title)+"</title>"))
}

func setCanonical(page []byte, href string) []byte {
	return canonicalTag.ReplaceAll(page, []byte("${1}"+escapeRepl(href)+"${2}"))
}

// escapeRepl HTML-escapes a value and escapes $ for regexp replacement templates.
func escapeRepl(s string) string {
	return strings.ReplaceAll(html.EscapeString(s), "$", "$$")
}

// --- card images ---

// cardCache keeps rendered cards briefly: a link shared in a busy channel is fetched by
// many clients at once, and rendering takes tens of milliseconds.
type cardCache struct {
	mu      sync.Mutex
	entries map[string]cardEntry
	group   singleflight.Group
}

type cardEntry struct {
	png     []byte
	expires time.Time
}

const (
	cardTTL       = 5 * time.Minute
	cardCacheSize = 500
)

func (c *cardCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.png, true
}

func (c *cardCache) put(key string, png []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil || len(c.entries) >= cardCacheSize {
		c.entries = map[string]cardEntry{} // crude, but bounded
	}
	c.entries[key] = cardEntry{png, time.Now().Add(cardTTL)}
}

func handleTraderCard(d Deps) http.HandlerFunc {
	cache := &cardCache{}
	return func(w http.ResponseWriter, r *http.Request) {
		handle := strings.ToLower(r.PathValue("handle"))
		// Check visibility on every request, cached or not: hiding takes effect at once.
		if _, err := d.Accounts.PublicAccount(r.Context(), handle); err != nil {
			if errors.Is(err, accounts.ErrNotFound) {
				writeError(w, http.StatusNotFound, "trader_not_found", "no public profile with that handle")
			} else {
				internalError(w, "trader card", err)
			}
			return
		}
		png, ok := cache.get(handle)
		if !ok {
			v, err, _ := cache.group.Do(handle, func() (any, error) {
				p, err := loadProfile(r.Context(), d, handle)
				if err != nil {
					return nil, err
				}
				pnl, _ := strconv.ParseFloat(p.LifetimePnl, 64)
				card := sharecard.Profile{Handle: p.Handle, Pnl: pnl, Trades: p.Trades, Rank: p.Rank,
					Link: r.Host + "/u/" + p.Handle}
				if p.ClosedTrades > 0 {
					wr := float64(p.Wins) / float64(p.ClosedTrades)
					card.WinRate = &wr
				}
				img, err := sharecard.RenderProfile(card)
				if err != nil {
					return nil, err
				}
				cache.put(handle, img)
				return img, nil
			})
			if errors.Is(err, errNoProfile) {
				writeError(w, http.StatusNotFound, "trader_not_found", "no public profile with that handle")
				return
			}
			if err != nil {
				internalError(w, "trader card", err)
				return
			}
			png = v.([]byte)
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=300")
		http.ServeContent(w, r, "card.png", time.Time{}, bytes.NewReader(png))
	}
}
