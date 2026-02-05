package guard

import (
	"net/http"
	"sync"
	"time"

	chassis "github.com/ai8future/chassis-go"
	"github.com/ai8future/chassis-go/errors"
)

// RateLimitConfig configures the rate limiter.
type RateLimitConfig struct {
	Rate    int
	Window  time.Duration
	KeyFunc KeyFunc // REQUIRED
}

type bucket struct {
	tokens   float64
	lastFill time.Time
}

// maxBuckets is the upper bound on tracked keys. When exceeded, a sweep is
// forced and the oldest idle buckets are evicted.
const maxBuckets = 100_000

type limiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	rate      int
	window    time.Duration
	lastSweep time.Time
}

func newLimiter(rate int, window time.Duration) *limiter {
	return &limiter{
		buckets:   make(map[string]*bucket),
		rate:      rate,
		window:    window,
		lastSweep: time.Now(),
	}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()

	// Lazy sweep: evict stale buckets every window period or when map is full.
	if now.Sub(l.lastSweep) >= l.window || len(l.buckets) >= maxBuckets {
		staleThreshold := now.Add(-2 * l.window)
		for k, b := range l.buckets {
			if b.lastFill.Before(staleThreshold) {
				delete(l.buckets, k)
			}
		}
		l.lastSweep = now
	}

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: float64(l.rate), lastFill: now}
		l.buckets[key] = b
	}
	elapsed := now.Sub(b.lastFill)
	refill := elapsed.Seconds() / l.window.Seconds() * float64(l.rate)
	b.tokens += refill
	if b.tokens > float64(l.rate) {
		b.tokens = float64(l.rate)
	}
	b.lastFill = now
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// RateLimit returns middleware enforcing per-key rate limiting with token bucket.
func RateLimit(cfg RateLimitConfig) func(http.Handler) http.Handler {
	chassis.AssertVersionChecked()
	lim := newLimiter(cfg.Rate, cfg.Window)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := cfg.KeyFunc(r)
			if !lim.allow(key) {
				writeProblem(w, r, errors.RateLimitError("rate limit exceeded"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
