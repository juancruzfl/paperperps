package ratelimit

import (
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestAllow(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	l := New(rate.Every(12*time.Minute), 5) // 5 per hour
	l.now = func() time.Time { return now }

	for i := range 5 {
		if ok, _ := l.Allow("1.2.3.4"); !ok {
			t.Fatalf("request %d refused within the burst", i+1)
		}
	}
	ok, wait := l.Allow("1.2.3.4")
	if ok || wait < 11*time.Minute || wait > 12*time.Minute {
		t.Fatalf("6th request: ok=%v wait=%v, want refused with ~12m wait", ok, wait)
	}
	if ok, _ := l.Allow("5.6.7.8"); !ok {
		t.Error("another key shares the bucket")
	}

	now = now.Add(12 * time.Minute)
	if ok, _ := l.Allow("1.2.3.4"); !ok {
		t.Error("no token after the refill interval")
	}

	now = now.Add(3 * time.Hour)
	l.Allow("9.9.9.9") // triggers a sweep
	if _, kept := l.buckets["1.2.3.4"]; kept {
		t.Error("idle bucket not swept")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	if got := ClientIP(r); got != "10.0.0.1" {
		t.Errorf("no proxy: %s", got)
	}
	// The client forged the first entry; the proxy appended the real address.
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.7")
	if got := ClientIP(r); got != "203.0.113.7" {
		t.Errorf("behind proxy: %s", got)
	}
}
