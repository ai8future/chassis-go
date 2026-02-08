package guard

import (
	"container/list"
	"net/http"
	"sync"
	"time"

	chassis "github.com/ai8future/chassis-go/v5"
	"github.com/ai8future/chassis-go/v5/errors"
)

// RateLimitConfig configures the rate limiter.
type RateLimitConfig struct {
	Rate    int
	Window  time.Duration
	KeyFunc KeyFunc // REQUIRED
	MaxKeys int     // REQUIRED: upper bound on tracked keys
}

type bucket struct {
	tokens   float64
	lastFill time.Time
}

// lruEntry holds a bucket and its position in the LRU list.
type lruEntry struct {
	key    string
	bucket *bucket
	elem   *list.Element
}

type limiter struct {
	mu      sync.Mutex
	entries map[string]*lruEntry
	order   *list.List // front=MRU, back=LRU
	rate    int
	window  time.Duration
	maxKeys int
}

func newLimiter(rate int, window time.Duration, maxKeys int) *limiter {
	return &limiter{
		entries: make(map[string]*lruEntry),
		order:   list.New(),
		rate:    rate,
		window:  window,
		maxKeys: maxKeys,
	}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()

	entry, ok := l.entries[key]
	if ok {
		// Move to front (MRU).
		l.order.MoveToFront(entry.elem)
	} else {
		// Evict LRU if at capacity.
		for len(l.entries) >= l.maxKeys {
			l.evictLRU()
		}
		b := &bucket{tokens: float64(l.rate), lastFill: now}
		elem := l.order.PushFront(key)
		entry = &lruEntry{key: key, bucket: b, elem: elem}
		l.entries[key] = entry
	}

	b := entry.bucket
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

// evictLRU removes the least recently used entry. Must be called with mu held.
func (l *limiter) evictLRU() {
	back := l.order.Back()
	if back == nil {
		return
	}
	key := back.Value.(string)
	l.order.Remove(back)
	delete(l.entries, key)
}

// RateLimit returns middleware enforcing per-key rate limiting with token bucket.
// Panics if Rate, Window, KeyFunc, or MaxKeys are invalid.
func RateLimit(cfg RateLimitConfig) func(http.Handler) http.Handler {
	chassis.AssertVersionChecked()
	if cfg.Rate <= 0 {
		panic("guard: RateLimitConfig.Rate must be > 0")
	}
	if cfg.Window <= 0 {
		panic("guard: RateLimitConfig.Window must be > 0")
	}
	if cfg.KeyFunc == nil {
		panic("guard: RateLimitConfig.KeyFunc must not be nil")
	}
	if cfg.MaxKeys <= 0 {
		panic("guard: RateLimitConfig.MaxKeys must be > 0")
	}
	lim := newLimiter(cfg.Rate, cfg.Window, cfg.MaxKeys)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := cfg.KeyFunc(r)
			if !lim.allow(key) {
				w.Header().Set("Retry-After", "1")
				writeProblem(w, r, errors.RateLimitError("rate limit exceeded"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
