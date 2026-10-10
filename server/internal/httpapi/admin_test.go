package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/juancruzfl/paperperps/server/internal/accounts"
	"github.com/juancruzfl/paperperps/server/internal/adminauth"
	"github.com/juancruzfl/paperperps/server/internal/config"
	"github.com/juancruzfl/paperperps/server/internal/db"
	"github.com/juancruzfl/paperperps/server/internal/dbtest"
	"github.com/juancruzfl/paperperps/server/internal/engine"
	"github.com/juancruzfl/paperperps/server/internal/hub"
	"github.com/juancruzfl/paperperps/server/internal/leaderboard"
	"github.com/juancruzfl/paperperps/server/internal/market"
)

func adminKeys(t *testing.T) (ed25519.PrivateKey, ed25519.PublicKey) {
	t.Helper()
	priv, pub, err := adminauth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	k, _ := adminauth.ParsePrivateKey(priv)
	p, _ := adminauth.ParsePublicKey(pub)
	return k, p
}

// concretePath fills a route pattern's wildcards: "POST /a/{id}" → "POST", "/a/<uuid>".
func concretePath(pattern string) (string, string) {
	method, path, _ := strings.Cut(pattern, " ")
	return method, strings.ReplaceAll(path, "{id}", uuid.NewString())
}

func send(t *testing.T, h http.Handler, req *http.Request) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// TestAdminRoutesAreHidden checks every admin route answers exactly like an unknown /api/
// path unless the request carries a valid signature from the configured key, and that
// none exist when the admin API is off. It covers routes added later automatically.
func TestAdminRoutesAreHidden(t *testing.T) {
	priv, pub := adminKeys(t)
	otherPriv, _ := adminKeys(t)
	on := New(Deps{Config: config.Config{AdminPublicKey: pub}})
	off := New(Deps{Config: config.Config{}})

	wantStatus, wantBody := send(t, on, httptest.NewRequest("GET", "/api/no-such-route", nil))
	if wantStatus != http.StatusNotFound {
		t.Fatalf("unknown route status = %d", wantStatus)
	}
	signed := func(key ed25519.PrivateKey, method, path string, at time.Time) *http.Request {
		r := httptest.NewRequest(method, path, strings.NewReader(`{"reason":"x"}`))
		if err := adminauth.Sign(r, []byte(`{"reason":"x"}`), key, at); err != nil {
			t.Fatal(err)
		}
		return r
	}

	if len(adminRoutes) == 0 {
		t.Fatal("no admin routes")
	}
	for _, rt := range adminRoutes {
		method, path := concretePath(rt.pattern)
		if !strings.HasPrefix(path, "/api/admin/") {
			t.Errorf("%s isn't under /api/admin/", rt.pattern)
		}
		cases := map[string]struct {
			h   http.Handler
			req *http.Request
		}{
			"unsigned":           {on, httptest.NewRequest(method, path, strings.NewReader(`{"reason":"x"}`))},
			"wrong key":          {on, signed(otherPriv, method, path, time.Now())},
			"expired":            {on, signed(priv, method, path, time.Now().Add(-2*time.Minute))},
			"wrong method":       {on, signed(priv, "PUT", path, time.Now())},
			"api off, valid sig": {off, signed(priv, method, path, time.Now())},
			"api off, unsigned":  {off, httptest.NewRequest(method, path, nil)},
		}
		bad := httptest.NewRequest(method, path, nil)
		bad.Header.Set(adminauth.HeaderTimestamp, "1")
		bad.Header.Set(adminauth.HeaderNonce, "zz")
		bad.Header.Set(adminauth.HeaderSignature, "garbage")
		cases["garbage signature"] = struct {
			h   http.Handler
			req *http.Request
		}{on, bad}

		for name, c := range cases {
			status, body := send(t, c.h, c.req)
			if status != wantStatus || body != wantBody {
				t.Errorf("%s %s (%s): %d %s, want the unknown-route 404", method, path, name, status, body)
			}
		}
	}
}

// adminEnv is a server with the admin API on, a database, and a running engine.
type adminEnv struct {
	srv    *httptest.Server
	client *adminauth.Client
	store  *accounts.Store
}

func newAdminEnv(t *testing.T) *adminEnv {
	t.Helper()
	url, drop, ok, err := dbtest.Create("paperperps_httpapi")
	if !ok {
		t.Skip("TEST_DATABASE_URL not set")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(drop)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	priv, pub := adminKeys(t)
	store := accounts.NewStore(pool, decimal.NewFromInt(1000))
	md := market.New(market.Options{})
	h := hub.New(nil)
	eng := engine.New(pool, md, h, engine.Config{
		TakerFeeRate: decimal.RequireFromString("0.00045"), MakerFeeRate: decimal.RequireFromString("0.00015"),
		MaxSlippage: decimal.RequireFromString("0.05"), MinNotional: decimal.NewFromInt(10), DefaultLeverage: 20,
		StartingBalance: decimal.NewFromInt(1000), ResetCooldown: 24 * time.Hour,
	})
	go eng.Run(ctx)
	for deadline := time.Now().Add(5 * time.Second); !eng.Running(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("engine did not start")
		}
	}
	srv := httptest.NewServer(New(Deps{
		Config: config.Config{AdminPublicKey: pub, ResetCooldown: 24 * time.Hour}, Pool: pool, Accounts: store,
		Engine: eng, Market: md, Hub: h, Leaderboard: leaderboard.New(pool, md, 5, 24*time.Hour, 30*time.Second),
	}))
	t.Cleanup(srv.Close)
	return &adminEnv{srv: srv, client: &adminauth.Client{BaseURL: srv.URL, Key: priv}, store: store}
}

func wantAPIError(t *testing.T, err error, status int, code string) {
	t.Helper()
	var apiErr *adminauth.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != status || apiErr.Code != code {
		t.Errorf("err = %v, want %d %s", err, status, code)
	}
}

func TestAdminAPI(t *testing.T) {
	env := newAdminEnv(t)
	ctx := context.Background()
	c := env.client
	acct, err := env.store.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/admin/accounts/" + acct.ID.String()

	var found []accounts.AccountSummary
	if err := c.Do(ctx, "GET", "/api/admin/accounts?q="+acct.Handle[:5], nil, &found); err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != acct.ID {
		t.Errorf("search = %+v", found)
	}

	// Every change needs a reason.
	wantAPIError(t, c.Do(ctx, "POST", base+"/rename", map[string]any{}, nil), 400, "invalid_reason")

	var renamed struct{ Handle string }
	if err := c.Do(ctx, "POST", base+"/rename", map[string]any{"handle": "trader42", "reason": "offensive handle"}, &renamed); err != nil {
		t.Fatal(err)
	}
	if renamed.Handle != "trader42" {
		t.Errorf("handle = %q", renamed.Handle)
	}
	if err := c.Do(ctx, "POST", base+"/rename", map[string]any{"reason": "random"}, &renamed); err != nil || renamed.Handle == "trader42" {
		t.Errorf("random rename: %q, %v", renamed.Handle, err)
	}

	if err := c.Do(ctx, "POST", base+"/leaderboard", map[string]any{"hidden": true, "reason": "wash trading"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Do(ctx, "POST", base+"/suspension", map[string]any{"suspended": true, "reason": "multi-accounting"}, nil); err != nil {
		t.Fatal(err)
	}
	a, err := env.store.Load(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !a.LeaderboardHidden || a.Suspension == nil || a.Suspension.Reason != "multi-accounting" {
		t.Errorf("account after moderation = hidden %v, suspension %+v", a.LeaderboardHidden, a.Suspension)
	}

	// The suspended account can't rename itself back.
	req, _ := http.NewRequest("PATCH", env.srv.URL+"/api/me", strings.NewReader(`{"handle":"badname"}`))
	req.Header.Set("Authorization", "Bearer "+acct.Secret)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("suspended handle change: status %d, want 403", res.StatusCode)
	}

	var shown struct {
		Account accounts.Account       `json:"account"`
		Actions []accounts.AdminAction `json:"actions"`
	}
	if err := c.Do(ctx, "GET", base, nil, &shown); err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, x := range shown.Actions {
		actions = append(actions, x.Action)
	}
	if got := strings.Join(actions, ","); got != "suspend,hide,rename,rename" {
		t.Errorf("audit log = %s", got)
	}

	if err := c.Do(ctx, "POST", base+"/reset", map[string]any{"reason": "exploit"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Do(ctx, "POST", base+"/delete", map[string]any{"reason": "spam"}, nil); err != nil {
		t.Fatal(err)
	}
	wantAPIError(t, c.Do(ctx, "GET", base, nil, nil), 404, "account_not_found")
	wantAPIError(t, c.Do(ctx, "POST", "/api/admin/accounts/"+uuid.NewString()+"/suspension",
		map[string]any{"suspended": true, "reason": "x"}, nil), 404, "not_found")

	var log []accounts.AdminAction
	if err := c.Do(ctx, "GET", "/api/admin/actions?account="+acct.ID.String(), nil, &log); err != nil {
		t.Fatal(err)
	}
	if len(log) != 6 || log[0].Action != "delete" || log[0].Reason != "spam" {
		t.Errorf("log after delete = %+v", log)
	}
}

func TestAdminReplayIsRejected(t *testing.T) {
	env := newAdminEnv(t)
	ctx := context.Background()
	req, err := env.client.NewRequest(ctx, "GET", "/api/admin/actions", nil)
	if err != nil {
		t.Fatal(err)
	}
	again := req.Clone(ctx)
	again.Body = io.NopCloser(bytes.NewReader(nil))

	for i, r := range []*http.Request{req, again} {
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		want := http.StatusOK
		if i == 1 {
			want = http.StatusNotFound
		}
		if res.StatusCode != want {
			t.Errorf("send %d: status %d, want %d", i+1, res.StatusCode, want)
		}
	}
}
