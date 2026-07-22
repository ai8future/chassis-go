package call

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/internal/otelutil"
	"github.com/ai8future/chassis-go/v11/work"
	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/ai8future/chassis-go/v11/call"

// ErrBodyNotRewindable is returned when a retry would need to resend a
// request body that cannot be recreated through Request.GetBody.
var ErrBodyNotRewindable = errors.New("call: request body is not rewindable")

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
	httpClient         *http.Client
	timeout            time.Duration
	retrier            *Retrier
	breaker            Breaker
	tokenSource        TokenSource
	telemetryRedaction bool
	propagator         propagation.TextMapPropagator
	propagatorSet      bool
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
	return c
}

// WithTimeout sets the maximum duration for a single HTTP request attempt.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		c.timeout = d
	}
}

// WithRetry enables the historical method-agnostic retries for transient (5xx)
// errors using exponential backoff with jitter. MaxAttempts is clamped to a
// minimum of 1. This option can retry mutation methods such as POST; callers
// must ensure those operations are duplicate-safe or also apply
// [WithIdempotentOnlyRetries].
//
// Note: retries re-send the same *http.Request. Requests with a non-nil Body
// must be rewindable through Request.GetBody. If a retry is required and the
// body cannot be recreated, Do returns ErrBodyNotRewindable instead of sending
// an empty or partially consumed body. Requests with nil Body (GET, DELETE,
// HEAD) are always safe to retry.
func WithRetry(maxAttempts int, baseDelay time.Duration) Option {
	return func(c *Client) {
		c.retrier = &Retrier{
			MaxAttempts: max(1, maxAttempts),
			BaseDelay:   baseDelay,
		}
	}
}

// WithRetryPolicy configures a custom retry policy. If retries were not already
// enabled, it enables a default 3-attempt retrier with the provided policy.
func WithRetryPolicy(policy RetryPolicy) Option {
	return func(c *Client) {
		if c.retrier == nil {
			c.retrier = &Retrier{MaxAttempts: 3, BaseDelay: 100 * time.Millisecond}
		}
		c.retrier.Policy = policy
	}
}

// WithIdempotentOnlyRetries suppresses retries for non-idempotent HTTP methods.
// It is opt-in so existing WithRetry behavior remains backward compatible.
func WithIdempotentOnlyRetries() Option {
	return func(c *Client) {
		if c.retrier == nil {
			c.retrier = &Retrier{MaxAttempts: 3, BaseDelay: 100 * time.Millisecond}
		}
		c.retrier.IdempotentOnly = true
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

// WithHTTPClient replaces the underlying *http.Client used by the call
// Client. This is useful when you need a custom Transport (e.g., proxy
// routing, SSRF-safe dialer) or a custom CheckRedirect policy. The
// timeout set by [WithTimeout] is applied via a per-request context and
// does not override the http.Client.Timeout field — set that to zero or
// remove it if you want the call-level timeout to be authoritative.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		c.httpClient = hc
	}
}

// WithTokenSource configures a TokenSource that provides a Bearer token
// injected into the Authorization header of every outbound request.
func WithTokenSource(source TokenSource) Option {
	return func(c *Client) {
		c.tokenSource = source
	}
}

// WithTelemetryRedaction omits destination and error details from call's
// OpenTelemetry spans and duration metrics. Redacted telemetry retains client
// spans, TraceContext injection, HTTP method and status attributes, and fixed
// error classifications. Request execution and returned errors are unchanged.
//
// Redaction is opt-in so existing consumers retain their current telemetry.
func WithTelemetryRedaction() Option {
	return func(c *Client) {
		c.telemetryRedaction = true
	}
}

// WithTextMapPropagator selects the propagator used by this client. The last
// WithTextMapPropagator option wins. Without this option, Do reads and uses the
// process-global OpenTelemetry propagator at call time for backward
// compatibility.
//
// With this option, Do clones the caller's headers, removes every field
// declared by both the active process-global propagator and the selected
// propagator, then injects only with the selected propagator. An explicit
// typed or untyped nil disables injection while still removing fields declared
// by the active global propagator. Propagators must accurately implement
// Fields. Custom propagators must also make Fields and Inject safe for
// concurrent use because a Client can execute requests concurrently.
func WithTextMapPropagator(propagator propagation.TextMapPropagator) Option {
	return func(c *Client) {
		if nilPropagator(propagator) {
			propagator = nil
		}
		c.propagator = propagator
		c.propagatorSet = true
	}
}

func nilPropagator(propagator propagation.TextMapPropagator) bool {
	if propagator == nil {
		return true
	}
	value := reflect.ValueOf(propagator)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
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
	spanName := req.Method
	spanAttrs := []attribute.KeyValue{
		attribute.String("http.method", req.Method),
		attribute.String("url.path", req.URL.Path),
		attribute.String("server.address", req.URL.Host),
	}
	if c.telemetryRedaction {
		spanName = safeHTTPMethod(req.Method)
		spanAttrs = []attribute.KeyValue{
			attribute.String("http.method", spanName),
		}
	}
	ctx, span := tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(spanAttrs...),
	)
	req = req.WithContext(ctx)
	c.injectPropagation(ctx, req)

	// Token injection — fetch a Bearer token and set the Authorization header.
	if c.tokenSource != nil {
		token, err := c.tokenSource.Token(req.Context())
		if err != nil {
			if c.telemetryRedaction {
				setRedactedSpanError(span, telemetryErrorTokenSource)
			} else {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			}
			span.End()
			if cancel != nil {
				cancel()
			}
			return nil, fmt.Errorf("call: token source: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	// Circuit breaker gate — reject early if open.
	if c.breaker != nil {
		if err := c.breaker.Allow(); err != nil {
			span.AddEvent("circuit_breaker_rejected")
			if c.telemetryRedaction {
				setRedactedSpanError(span, telemetryErrorCircuitBreaker)
			}
			span.End()
			if h := getClientDuration(); h != nil {
				durationAttrs := []attribute.KeyValue{
					attribute.String("http.request.method", req.Method),
					attribute.String("server.address", req.URL.Host),
					attribute.String("error.type", fmt.Sprintf("%T", err)),
				}
				if c.telemetryRedaction {
					durationAttrs = []attribute.KeyValue{
						attribute.String("http.request.method", safeHTTPMethod(req.Method)),
						attribute.String("error.type", telemetryErrorCircuitBreaker),
					}
				}
				h.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(durationAttrs...))
			}
			if cancel != nil {
				cancel()
			}
			return nil, err
		}
	}

	// The core execution function. When retries are configured, the closure
	// rewinds the request body via GetBody before each retry attempt. The
	// attempt counter is local to this Do call, avoiding shared state on the
	// Retrier struct which would race under concurrent use.
	var attempt int
	exec := func() (*http.Response, error) {
		if attempt > 0 {
			if req.Body != nil && req.Body != http.NoBody && req.GetBody == nil {
				return nil, ErrBodyNotRewindable
			}
			if req.GetBody != nil {
				body, err := req.GetBody()
				if err != nil {
					return nil, fmt.Errorf("%w: %w", ErrBodyNotRewindable, err)
				}
				req.Body = body
			}
		}
		attempt++
		return c.httpClient.Do(req)
	}

	var resp *http.Response
	var err error

	if c.retrier != nil {
		retrier := *c.retrier
		retrier.method = req.Method
		retrier.telemetryRedaction = c.telemetryRedaction
		resp, err = retrier.Do(ctx, exec)
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
		if c.telemetryRedaction {
			setRedactedSpanError(span, redactedErrorClassification(err))
		} else {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
	} else if resp != nil {
		span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))
		if resp.StatusCode >= 400 {
			if c.telemetryRedaction {
				setRedactedSpanError(span, telemetryErrorHTTPStatus)
			} else {
				span.SetStatus(codes.Error, http.StatusText(resp.StatusCode))
			}
		}
	}
	span.End()

	// OTel: record http.client.request.duration metric.
	durationAttrs := []attribute.KeyValue{
		attribute.String("http.request.method", req.Method),
		attribute.String("server.address", req.URL.Host),
	}
	if c.telemetryRedaction {
		durationAttrs = []attribute.KeyValue{
			attribute.String("http.request.method", safeHTTPMethod(req.Method)),
		}
	}
	if resp != nil {
		durationAttrs = append(durationAttrs,
			attribute.Int("http.response.status_code", resp.StatusCode),
		)
	} else if err != nil {
		errorType := fmt.Sprintf("%T", err)
		if c.telemetryRedaction {
			errorType = redactedErrorClassification(err)
		}
		durationAttrs = append(durationAttrs, attribute.String("error.type", errorType))
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

func (c *Client) injectPropagation(ctx context.Context, req *http.Request) {
	global := otelapi.GetTextMapPropagator()
	if !c.propagatorSet {
		if nilPropagator(global) {
			return
		}
		if req.Header == nil {
			req.Header = make(http.Header)
		}
		global.Inject(ctx, propagation.HeaderCarrier(req.Header))
		return
	}

	// Request.WithContext performs a shallow copy, so clone the header map
	// before enforcing an explicit per-client propagation boundary. This also
	// prevents token injection later in Do from mutating the caller's request.
	req.Header = req.Header.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	scrubPropagationFields(req.Header, global, c.propagator)
	if nilPropagator(c.propagator) {
		return
	}
	c.propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))
}

func scrubPropagationFields(header http.Header, propagators ...propagation.TextMapPropagator) {
	if len(header) == 0 {
		return
	}
	fields := make(map[string]struct{})
	for _, propagator := range propagators {
		if nilPropagator(propagator) {
			continue
		}
		for _, field := range propagator.Fields() {
			fields[strings.ToLower(field)] = struct{}{}
		}
	}
	for field := range header {
		if _, ok := fields[strings.ToLower(field)]; ok {
			delete(header, field)
		}
	}
}

const (
	telemetryErrorBodyNotRewindable = "body_not_rewindable"
	telemetryErrorCircuitBreaker    = "circuit_breaker_rejected"
	telemetryErrorContextCanceled   = "context_canceled"
	telemetryErrorDeadlineExceeded  = "deadline_exceeded"
	telemetryErrorHTTPStatus        = "http_error"
	telemetryErrorRequest           = "request_failed"
	telemetryErrorTokenSource       = "token_source_failed"
)

func setRedactedSpanError(span trace.Span, classification string) {
	span.SetAttributes(attribute.String("error.type", classification))
	span.SetStatus(codes.Error, classification)
}

func redactedErrorClassification(err error) string {
	switch {
	case errors.Is(err, ErrBodyNotRewindable):
		return telemetryErrorBodyNotRewindable
	case errors.Is(err, context.Canceled):
		return telemetryErrorContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return telemetryErrorDeadlineExceeded
	default:
		return telemetryErrorRequest
	}
}

func safeHTTPMethod(method string) string {
	switch method {
	case http.MethodConnect, http.MethodDelete, http.MethodGet, http.MethodHead,
		http.MethodOptions, http.MethodPatch, http.MethodPost, http.MethodPut,
		http.MethodTrace:
		return method
	default:
		return "OTHER"
	}
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
