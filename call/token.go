package call

import (
	"context"
	"sync"
	"time"
)

// TokenSource provides Bearer tokens for HTTP requests.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// CachedToken caches a token and refreshes it when within Leeway of expiry.
type CachedToken struct {
	fetch   func(ctx context.Context) (token string, expiresAt time.Time, err error)
	leeway  time.Duration
	mu      sync.Mutex
	token   string
	expires time.Time
}

// NewCachedToken creates a TokenSource that caches tokens from fetchFn.
func NewCachedToken(fetchFn func(ctx context.Context) (string, time.Time, error), opts ...TokenOption) *CachedToken {
	ct := &CachedToken{
		fetch:  fetchFn,
		leeway: 5 * time.Minute,
	}
	for _, o := range opts {
		o(ct)
	}
	return ct
}

// TokenOption configures a CachedToken.
type TokenOption func(*CachedToken)

// Leeway sets the pre-expiry refresh window.
func Leeway(d time.Duration) TokenOption {
	return func(ct *CachedToken) { ct.leeway = d }
}

// Token returns a cached token, refreshing if needed.
func (ct *CachedToken) Token(ctx context.Context) (string, error) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	if ct.token != "" && time.Now().Add(ct.leeway).Before(ct.expires) {
		return ct.token, nil
	}

	token, expires, err := ct.fetch(ctx)
	if err != nil {
		return "", err
	}
	ct.token = token
	ct.expires = expires
	return token, nil
}
