// Package ratelimit keeps a token bucket per key (an IP or an account), in memory. Each
// instance limits on its own, which is fine for a single-replica deployment.
package ratelimit

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type entry struct {
	lim  *rate.Limiter
	seen time.Time
}

type Limiter struct {
	every rate.Limit
	burst int

	mu        sync.Mutex
	buckets   map[string]*entry
	lastSweep time.Time
	now       func() time.Time
}

// New allows burst events at once, refilling at r per second.
func New(r rate.Limit, burst int) *Limiter {
	return &Limiter{every: r, burst: burst, buckets: map[string]*entry{}, now: time.Now}
}

// Allow takes a token for key. When none is left it reports how long until one is.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	e, ok := l.buckets[key]
	if !ok {
		e = &entry{lim: rate.NewLimiter(l.every, l.burst)}
		l.buckets[key] = e
	}
	e.seen = now
	if e.lim.AllowN(now, 1) {
		return true, 0
	}
	r := e.lim.ReserveN(now, 1)
	wait := r.DelayFrom(now)
	r.CancelAt(now)
	return false, wait
}

// sweep drops buckets idle long enough to be full again, every few minutes.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < 5*time.Minute {
		return
	}
	l.lastSweep = now
	idle := time.Hour
	if l.every > 0 {
		idle = max(idle, time.Duration(float64(l.burst)/float64(l.every)*float64(time.Second)))
	}
	for k, e := range l.buckets {
		if now.Sub(e.seen) > idle {
			delete(l.buckets, k)
		}
	}
}

// ClientIP is the address of the browser that made r. Behind Railway's proxy that is the
// last X-Forwarded-For entry: the proxy appends the address it saw, so earlier entries can
// be forged by the client and are ignored.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
