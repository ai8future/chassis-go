package call

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	chassis "github.com/ai8future/chassis-go/v5"
	"github.com/ai8future/chassis-go/v5/internal/otelutil"
	"github.com/ai8future/chassis-go/v5/work"
	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/ai8future/chassis-go/v5/call"

var getClientDuration = otelutil.LazyHistogram(
	tracerName,
	"http.client.request.duration",
	metric.WithDescription("Duration of HTTP client requests."),
	metric.WithUnit("s"),
)

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
// exponential backoff with jitter. MaxAttempts is clamped to a minimum of 1.
//
// Note: retries re-send the same *http.Request. For requests with a non-nil
// Body, the body must be rewindable (implement GetBody) or the retry will
// send an empty/consumed body. Requests with nil Body (GET, DELETE, HEAD)
// are always safe to retry.
func WithRetry(maxAttempts int, baseDelay time.Duration) Option {
	return func(c *Client) {
		c.retrier = &Retrier{
			MaxAttempts: max(1, maxAttempts),
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
	start := time.Now()

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
			span.AddEvent("circuit_breaker_rejected")
			span.End()
			if h := getClientDuration(); h != nil {
				h.Record(ctx, time.Since(start).Seconds(),
					metric.WithAttributes(
						attribute.String("http.request.method", req.Method),
						attribute.String("server.address", req.URL.Host),
						attribute.String("error.type", fmt.Sprintf("%T", err)),
					),
				)
			}
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

		// Capture state before recording to detect transitions.
		type stater interface{ State() State }
		var prevState State
		hasPrev := false
		if s, ok := c.breaker.(stater); ok {
			prevState = s.State()
			hasPrev = true
		}

		c.breaker.Record(success)

		eventAttrs := []attribute.KeyValue{attribute.Bool("success", success)}
		if hasPrev {
			if s, ok := c.breaker.(stater); ok {
				newState := s.State()
				if newState != prevState {
					eventAttrs = append(eventAttrs,
						attribute.String("from_state", stateName(prevState)),
						attribute.String("to_state", stateName(newState)),
					)
				}
			}
		}
		span.AddEvent("circuit_breaker_record", trace.WithAttributes(eventAttrs...))
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

	// OTel: record http.client.request.duration metric.
	durationAttrs := []attribute.KeyValue{
		attribute.String("http.request.method", req.Method),
		attribute.String("server.address", req.URL.Host),
	}
	if resp != nil {
		durationAttrs = append(durationAttrs,
			attribute.Int("http.response.status_code", resp.StatusCode),
		)
	} else if err != nil {
		durationAttrs = append(durationAttrs,
			attribute.String("error.type", fmt.Sprintf("%T", err)),
		)
	}
	if h := getClientDuration(); h != nil {
		h.Record(ctx, time.Since(start).Seconds(),
			metric.WithAttributes(durationAttrs...),
		)
	}

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

// Batch executes multiple requests concurrently with bounded concurrency
// using work.Map. Results are returned in the same order as the input requests.
func (c *Client) Batch(ctx context.Context, requests []*http.Request, opts ...work.Option) ([]*http.Response, error) {
	return work.Map(ctx, requests, func(ctx context.Context, req *http.Request) (*http.Response, error) {
		req = req.WithContext(ctx)
		return c.Do(req)
	}, opts...)
}

// stateName returns a human-readable name for a circuit breaker state.
func stateName(s State) string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}
