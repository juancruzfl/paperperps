package httpapi

import (
	"bytes"
	"context"
	"image/png"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestPreviewTagRewriting(t *testing.T) {
	index, err := os.ReadFile("../../../web/index.html")
	if err != nil {
		t.Skip("web/index.html not found")
	}
	out := string(setCanonical(setMeta(setTitle(index, `a "b" <c>`), "property", "og:title", `x "$1" & y`), "https://e.x/u/h"))
	for _, want := range []string{
		`<title>a &#34;b&#34; &lt;c&gt;</title>`,
		`<meta property="og:title" content="x &#34;$1&#34; &amp; y" />`,
		`<link rel="canonical" href="https://e.x/u/h" />`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rewritten page lacks %s", want)
		}
	}
	// Tags that weren't set are untouched.
	if !strings.Contains(out, `<meta property="og:image" content="https://paperperps.lol/og.png" />`) {
		t.Error("og:image changed")
	}
}

func TestProfileLinkPreview(t *testing.T) {
	env := newAdminEnv(t)
	ctx := context.Background()
	acct, err := env.store.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) (*http.Response, string) {
		t.Helper()
		res, err := http.Get(env.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res, string(body)
	}

	res, page := get("/u/" + acct.Handle)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("profile page status %d", res.StatusCode)
	}
	for _, want := range []string{
		`<title>` + acct.Handle + ` on PaperPerps</title>`,
		`<meta property="og:title" content="` + acct.Handle + ` on PaperPerps" />`,
		`/api/traders/` + acct.Handle + `/card.png?v=`,
		`$0.00 lifetime PnL on paper`,
		`<meta property="og:url" content="http://` + strings.TrimPrefix(env.srv.URL, "http://") + `/u/` + acct.Handle + `" />`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("profile page lacks %s", want)
		}
	}

	res, card := get("/api/traders/" + acct.Handle + "/card.png")
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("card: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	img, err := png.Decode(bytes.NewReader([]byte(card)))
	if err != nil || img.Bounds().Dx() != 1200 || img.Bounds().Dy() != 630 {
		t.Errorf("card image: %v %v", img.Bounds(), err)
	}

	// Hidden traders get the site's own preview, and no card.
	if _, err := env.pool.Exec(ctx, `UPDATE accounts SET leaderboard_opt_in = false WHERE id = $1`, acct.ID); err != nil {
		t.Fatal(err)
	}
	_, page = get("/u/" + acct.Handle)
	if strings.Contains(page, acct.Handle) || !strings.Contains(page, `content="https://paperperps.lol/og.png"`) {
		t.Error("hidden trader's page still has their preview")
	}
	if res, _ := get("/api/traders/" + acct.Handle + "/card.png"); res.StatusCode != http.StatusNotFound {
		t.Errorf("hidden trader's cached card: status %d, want 404", res.StatusCode)
	}
	if res, _ := get("/api/traders/no-such-trader/card.png"); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown card: status %d", res.StatusCode)
	}
}
