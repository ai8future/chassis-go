package call

import (
	"context"
	"io"
	"net/http"
	"time"

	chassis "github.com/ai8future/chassis-go"
	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/ai8future/chassis-go/call"

// cancelBody wraps a response body so that a context cancel function is called
// when the body is closed, rather than when Do() returns. This prevents
// premature context cancellation from interrupting callers reading the body.
type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

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
	chassis.AssertVersionChecked()
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
	var cancel context.CancelFunc
	if _, ok := ctx.Deadline(); !ok {
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		req = req.WithContext(ctx)
	}

	// OTel: create client span and inject trace headers.
	tracer := otelapi.GetTracerProvider().Tracer(tracerName)
	ctx, span := tracer.Start(ctx, req.Method+" "+req.URL.Path,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("http.method", req.Method),
			attribute.String("http.url", req.URL.String()),
			attribute.String("server.address", req.URL.Host),
		),
	)
	req = req.WithContext(ctx)
	otelapi.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))

	// Circuit breaker gate — reject early if open.
	if c.breaker != nil {
		if err := c.breaker.Allow(); err != nil {
			span.End()
			if cancel != nil {
				cancel()
			}
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

	// OTel: record result on the client span.
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else if resp != nil {
		span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))
		if resp.StatusCode >= 400 {
			span.SetStatus(codes.Error, http.StatusText(resp.StatusCode))
		}
	}
	span.End()

	// If we created a cancel func, attach it to the response body so the
	// context lives until the caller closes the body. On error, cancel now.
	if cancel != nil {
		if err != nil || resp == nil {
			cancel()
		} else {
			resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: cancel}
		}
	}

	return resp, err
}
