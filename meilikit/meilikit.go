// Package meilikit provides an HTTP client for Meilisearch built on raw HTTP
// via chassis call. Zero external dependencies -- just chassis internals and stdlib.
package meilikit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/call"
	"github.com/ai8future/chassis-go/v11/tracekit"
)

// --------------------------------------------------------------------------
// Client
// --------------------------------------------------------------------------

// Client is an HTTP client for Meilisearch backed by call.Client.
type Client struct {
	baseURL  string
	tenantID string
	http     *call.Client
}

// options collects configuration before building the call.Client.
type options struct {
	tenantID    string
	callOptions []call.Option
}

// Option configures a Client.
type Option func(*options)

// WithTenant sets the tenant ID propagated on all requests.
func WithTenant(id string) Option {
	return func(o *options) { o.tenantID = id }
}

// WithTimeout overrides the default per-request timeout.
func WithTimeout(d time.Duration) Option {
	return func(o *options) { o.callOptions = append(o.callOptions, call.WithTimeout(d)) }
}

// WithRetry enables automatic retries for transient (5xx) errors.
func WithRetry(maxAttempts int, baseDelay time.Duration) Option {
	return func(o *options) { o.callOptions = append(o.callOptions, call.WithRetry(maxAttempts, baseDelay)) }
}

// WithCircuitBreaker protects the client with a named circuit breaker.
func WithCircuitBreaker(name string, threshold int, cooldown time.Duration) Option {
	return func(o *options) {
		o.callOptions = append(o.callOptions, call.WithCircuitBreaker(name, threshold, cooldown))
	}
}

// staticToken is a TokenSource that returns a fixed API key.
type staticToken string

func (s staticToken) Token(_ context.Context) (string, error) { return string(s), nil }

// New creates a new Meilisearch client. Returns error if BaseURL is empty.
func New(cfg Config, opts ...Option) (*Client, error) {
	chassis.AssertVersionChecked()
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("meilikit: base URL must not be empty")
	}

	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	// Build call.Client options: config defaults first, then user overrides.
	var callOpts []call.Option
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	callOpts = append(callOpts, call.WithTimeout(timeout))
	if cfg.APIKey != "" {
		callOpts = append(callOpts, call.WithTokenSource(staticToken(cfg.APIKey)))
	}
	callOpts = append(callOpts, o.callOptions...)

	return &Client{
		baseURL:  strings.TrimRight(cfg.BaseURL, "/"),
		tenantID: o.tenantID,
		http:     call.New(callOpts...),
	}, nil
}

// Ping checks that Meilisearch is reachable. GET /health.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("meilikit: build request: %w", err)
	}
	c.setHeaders(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("meilikit: ping: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return decodeMeiliError(resp)
	}
	return nil
}

// Index returns a handle for the named index. No HTTP call is made.
func (c *Client) Index(name string) (*Index, error) {
	if err := validateIndexName(name); err != nil {
		return nil, err
	}
	return &Index{name: name, client: c}, nil
}

// MultiSearch runs multiple queries in a single request. POST /multi-search.
func (c *Client) MultiSearch(ctx context.Context, queries []SearchQuery) (*MultiSearchResult, error) {
	body, err := json.Marshal(buildMultiSearchRequest(queries))
	if err != nil {
		return nil, fmt.Errorf("meilikit: marshal multi-search: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/multi-search", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("meilikit: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setHeaders(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("meilikit: multi-search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, decodeMeiliError(resp)
	}

	var result MultiSearchResult
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&result); err != nil {
		return nil, fmt.Errorf("meilikit: decode multi-search: %w", err)
	}
	return &result, nil
}

// --------------------------------------------------------------------------
// Internal helpers
// --------------------------------------------------------------------------

// setHeaders adds X-Tenant-ID and X-Trace-ID headers.
// Authorization is handled by call.Client via WithTokenSource.
func (c *Client) setHeaders(ctx context.Context, req *http.Request) {
	if c.tenantID != "" {
		req.Header.Set("X-Tenant-ID", c.tenantID)
	}
	if tid := tracekit.TraceID(ctx); tid != "" {
		req.Header.Set("X-Trace-ID", tid)
	}
}
