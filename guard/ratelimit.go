package guard

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
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

type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    int
	window  time.Duration
}

func newLimiter(rate int, window time.Duration) *limiter {
	return &limiter{
		buckets: make(map[string]*bucket),
		rate:    rate,
		window:  window,
	}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
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
	lim := newLimiter(cfg.Rate, cfg.Window)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := cfg.KeyFunc(r)
			if !lim.allow(key) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(map[string]any{
					"error":  "rate limit exceeded",
					"status": http.StatusTooManyRequests,
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
