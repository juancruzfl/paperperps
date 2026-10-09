package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRedirectWWW(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := redirectWWW(ok)

	req := httptest.NewRequest(http.MethodGet, "/restore?x=1", nil)
	req.Host = "www.paperperps.lol"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", rec.Code)
	}
	if got, want := rec.Header().Get("Location"), "https://paperperps.lol/restore?x=1"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "paperperps.lol"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTeapot {
		t.Errorf("apex request status = %d, want passthrough", rec.Code)
	}
}
