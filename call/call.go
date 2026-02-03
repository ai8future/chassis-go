package call

import (
	"context"
	"net/http"
	"time"
)

// Client is a resilient HTTP client that wraps the standard http.Client with
// optional retry, circuit breaker, and timeout middleware. Construct one using
// New with functional options.
type Client struct {
	httpClient *http.Client
	timeout    time.Duration
	retrier    *Retrier
	breaker    Breaker
}

// Option configures a Client.
type Option func(*Client)

// New creates a Client with the given options applied. Without options it
// behaves like a default http.Client with a 30-second timeout.
func New(opts ...Option) *Client {
	c := &Client{
		httpClient: &http.Client{},
		timeout:    30 * time.Second,
	}
	for _, o := range opts {
		o(c)
	}
	c.httpClient.Timeout = c.timeout
	return c
}

// WithTimeout sets the maximum duration for a single HTTP request attempt.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		c.timeout = d
	}
}

// WithRetry enables automatic retries for transient (5xx) errors using
// exponential backoff with jitter.
func WithRetry(maxAttempts int, baseDelay time.Duration) Option {
	return func(c *Client) {
		c.retrier = &Retrier{
			MaxAttempts: maxAttempts,
			BaseDelay:   baseDelay,
		}
	}
}

// WithCircuitBreaker protects the client with a named circuit breaker that
// opens after threshold consecutive failures and resets after resetTimeout.
func WithCircuitBreaker(name string, threshold int, resetTimeout time.Duration) Option {
	return func(c *Client) {
		c.breaker = GetBreaker(name, threshold, resetTimeout)
	}
}

// WithBreaker sets a custom circuit breaker implementation.
func WithBreaker(b Breaker) Option {
	return func(c *Client) {
		c.breaker = b
	}
}

// Do executes an HTTP request with all configured middleware applied. The
// middleware order is: circuit breaker check, retry loop, execute.
//
// If the request does not carry a context, one is created with the configured
// timeout. If a context is already present its deadline is respected.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	// Ensure the request always has a context with a deadline.
	ctx := req.Context()
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
		req = req.WithContext(ctx)
	}

	// Circuit breaker gate — reject early if open.
	if c.breaker != nil {
		if err := c.breaker.Allow(); err != nil {
			return nil, err
		}
	}

	// The core execution function.
	exec := func() (*http.Response, error) {
		return c.httpClient.Do(req)
	}

	var resp *http.Response
	var err error

	if c.retrier != nil {
		resp, err = c.retrier.Do(ctx, exec)
	} else {
		resp, err = exec()
	}

	// Record the result with the circuit breaker.
	if c.breaker != nil {
		success := err == nil && resp != nil && resp.StatusCode < 500
		c.breaker.Record(success)
	}

	return resp, err
}
