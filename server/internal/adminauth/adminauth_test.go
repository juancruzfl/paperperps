package adminauth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// signed builds a client request, signs it, and returns the request the server would see.
func signed(t *testing.T, method, url, body string, now time.Time) *http.Request {
	t.Helper()
	priv, err := ParsePrivateKey(testPriv)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := Sign(req, []byte(body), priv, now); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewRequest(method, url, strings.NewReader(body))
	srv.Header = req.Header.Clone()
	srv.RequestURI = req.URL.RequestURI() // a real server sees the path, not the absolute URL
	return srv
}

var testPriv = func() string {
	priv, _, err := GenerateKey()
	if err != nil {
		panic(err)
	}
	return priv
}()

func TestSignVerify(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	priv, err := ParsePrivateKey(testPriv)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ParsePublicKey(PublicKeyOf(priv))
	if err != nil {
		t.Fatal(err)
	}
	const url = "https://paperperps.lol/api/admin/accounts/abc/suspend?x=1"
	const body = `{"reason":"spam"}`

	r := signed(t, "POST", url, body, now)
	v, err := Verify(r, []byte(body), pub, now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("valid request: %v", err)
	}
	if v.Nonce == "" || !v.Expires.Equal(now.Add(MaxSkew)) {
		t.Errorf("verified = %+v", v)
	}

	tests := []struct {
		name   string
		mutate func(r *http.Request) (*http.Request, []byte)
		at     time.Time
		want   error
	}{
		{"body changed", func(r *http.Request) (*http.Request, []byte) { return r, []byte(`{"reason":"other"}`) }, now, ErrSignature},
		{"method changed", func(r *http.Request) (*http.Request, []byte) { r.Method = "DELETE"; return r, []byte(body) }, now, ErrSignature},
		{"path changed", func(r *http.Request) (*http.Request, []byte) {
			r.RequestURI = "/api/admin/accounts/def/suspend?x=1"
			return r, []byte(body)
		}, now, ErrSignature},
		{"host changed", func(r *http.Request) (*http.Request, []byte) { r.Host = "localhost:8080"; return r, []byte(body) }, now, ErrSignature},
		{"nonce changed", func(r *http.Request) (*http.Request, []byte) {
			r.Header.Set(HeaderNonce, strings.Repeat("0", 32))
			return r, []byte(body)
		}, now, ErrSignature},
		{"timestamp changed", func(r *http.Request) (*http.Request, []byte) {
			r.Header.Set(HeaderTimestamp, "1791633601")
			return r, []byte(body)
		}, now, ErrSignature},
		{"too old", func(r *http.Request) (*http.Request, []byte) { return r, []byte(body) }, now.Add(61 * time.Second), ErrExpired},
		{"from the future", func(r *http.Request) (*http.Request, []byte) { return r, []byte(body) }, now.Add(-61 * time.Second), ErrExpired},
		{"unsigned", func(r *http.Request) (*http.Request, []byte) {
			r.Header.Del(HeaderSignature)
			return r, []byte(body)
		}, now, ErrMissing},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, b := tc.mutate(signed(t, "POST", url, body, now))
			if _, err := Verify(r, b, pub, tc.at); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}

	// A different key's signature doesn't verify.
	_, otherPub, _ := GenerateKey()
	other, _ := ParsePublicKey(otherPub)
	if _, err := Verify(signed(t, "POST", url, body, now), []byte(body), other, now); !errors.Is(err, ErrSignature) {
		t.Errorf("other key: err = %v", err)
	}
}

func TestParseKeys(t *testing.T) {
	for _, bad := range []string{"", "not base64!", "AAAA"} {
		if _, err := ParsePrivateKey(bad); err == nil {
			t.Errorf("ParsePrivateKey(%q) accepted", bad)
		}
		if _, err := ParsePublicKey(bad); err == nil {
			t.Errorf("ParsePublicKey(%q) accepted", bad)
		}
	}
}
