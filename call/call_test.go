package call

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/work"
	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestMain(m *testing.M) {
	chassis.RequireMajor(11)
	os.Exit(m.Run())
}

// ---------- helpers ----------

// counterServer returns an httptest.Server that responds with the given status
// codes in order. Once all codes are exhausted it responds with finalStatus.
func counterServer(codes ...int) (*httptest.Server, *atomic.Int32) {
	var idx atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		i := int(idx.Add(1)) - 1
		if i < len(codes) {
			w.WriteHeader(codes[i])
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	return srv, &idx
}

// uniqueBreaker returns a unique breaker name so parallel tests don't collide.
var breakerSeq atomic.Int64

func uniqueBreakerName() string {
	return fmt.Sprintf("test-breaker-%d", breakerSeq.Add(1))
}

// ---------- tests ----------

func TestBasicRequestSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := New(WithTimeout(5 * time.Second))
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestRetryOn5xx(t *testing.T) {
	// Return 500 twice, then 200.
	srv, hits := counterServer(500, 500)
	defer srv.Close()

	c := New(
		WithTimeout(5*time.Second),
		WithRetry(3, 10*time.Millisecond),
	)
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	if n := int(hits.Load()); n != 3 {
		t.Fatalf("expected 3 attempts, got %d", n)
	}
}

func TestWithRetryPreservesExplicitUnsafePostRetries(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		attempt := attempts.Add(1)
		if attempt == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := New(WithRetry(2, time.Millisecond))
	req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader("duplicate-safe payload"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if attempts.Load() != 2 {
		t.Fatalf("POST attempts = %d, want 2 for explicit historical WithRetry", attempts.Load())
	}
}

func TestRetryReturnsBodyNotRewindableWhenGetBodyFails(t *testing.T) {
	srv, hits := counterServer(http.StatusServiceUnavailable)
	defer srv.Close()

	c := New(
		WithTimeout(5*time.Second),
		WithRetry(3, time.Millisecond),
	)
	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader("payload"))
	rewindErr := errors.New("rewind failed")
	req.GetBody = func() (io.ReadCloser, error) {
		return nil, rewindErr
	}

	_, err := c.Do(req)
	if !errors.Is(err, ErrBodyNotRewindable) {
		t.Fatalf("expected ErrBodyNotRewindable, got %v", err)
	}
	if !errors.Is(err, rewindErr) {
		t.Fatalf("expected wrapped rewind error, got %v", err)
	}
	if n := int(hits.Load()); n != 1 {
		t.Fatalf("expected 1 attempt before rewind failure, got %d", n)
	}
}

func TestRetryReturnsBodyNotRewindableForNonRewindableBody(t *testing.T) {
	srv, hits := counterServer(http.StatusServiceUnavailable)
	defer srv.Close()

	c := New(
		WithTimeout(5*time.Second),
		WithRetry(3, time.Millisecond),
	)
	req, _ := http.NewRequest(http.MethodPost, srv.URL, io.NopCloser(strings.NewReader("payload")))
	req.GetBody = nil

	_, err := c.Do(req)
	if !errors.Is(err, ErrBodyNotRewindable) {
		t.Fatalf("expected ErrBodyNotRewindable, got %v", err)
	}
	if n := int(hits.Load()); n != 1 {
		t.Fatalf("expected 1 attempt before non-rewindable retry, got %d", n)
	}
}

func TestNoRetryOn4xx(t *testing.T) {
	srv, hits := counterServer(http.StatusBadRequest)
	defer srv.Close()

	c := New(
		WithTimeout(5*time.Second),
		WithRetry(3, 10*time.Millisecond),
	)
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}

	if n := int(hits.Load()); n != 1 {
		t.Fatalf("expected 1 attempt (no retries on 4xx), got %d", n)
	}
}

func TestContextCancellationStopsRetries(t *testing.T) {
	srv, hits := counterServer(500, 500, 500, 500, 500)
	defer srv.Close()

	c := New(
		WithTimeout(5*time.Second),
		WithRetry(5, 50*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)

	// Cancel the context after a short delay so the retry loop is interrupted.
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	_, err := c.Do(req)
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}

	// Should NOT have completed all 5 attempts.
	if n := int(hits.Load()); n >= 5 {
		t.Fatalf("expected fewer than 5 attempts due to cancellation, got %d", n)
	}
}

func TestCircuitBreakerOpensAfterThreshold(t *testing.T) {
	srv, _ := counterServer(500, 500, 500, 500, 500)
	defer srv.Close()

	name := uniqueBreakerName()
	c := New(
		WithTimeout(5*time.Second),
		WithCircuitBreaker(name, 3, 1*time.Second),
	)

	// Fire 3 requests that all fail — breaker should open.
	for range 3 {
		req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
		c.Do(req)
	}

	// The fourth request should be rejected by the breaker.
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	_, err := c.Do(req)
	if err != ErrCircuitOpen {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreakerHalfOpenAllowsOneRequest(t *testing.T) {
	srv, hits := counterServer(200)
	defer srv.Close()

	name := uniqueBreakerName()
	cb := GetBreaker(name, 2, 50*time.Millisecond)

	// Force breaker open.
	cb.Record(false)
	cb.Record(false)

	if cb.State() != StateOpen {
		t.Fatalf("expected StateOpen, got %d", cb.State())
	}

	// Wait for reset timeout to elapse.
	time.Sleep(60 * time.Millisecond)

	c := New(
		WithTimeout(5*time.Second),
		WithCircuitBreaker(name, 2, 50*time.Millisecond),
	)

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("unexpected error in half-open state: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	if n := int(hits.Load()); n != 1 {
		t.Fatalf("expected exactly 1 request in half-open, got %d", n)
	}
}

func TestCircuitBreakerResetsOnSuccessInHalfOpen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	name := uniqueBreakerName()
	cb := GetBreaker(name, 2, 50*time.Millisecond)

	// Force breaker open.
	cb.Record(false)
	cb.Record(false)

	if cb.State() != StateOpen {
		t.Fatalf("expected StateOpen, got %d", cb.State())
	}

	// Wait for reset timeout so it transitions to half-open on next Allow.
	time.Sleep(60 * time.Millisecond)

	c := New(
		WithTimeout(5*time.Second),
		WithCircuitBreaker(name, 2, 50*time.Millisecond),
	)

	// Successful request in half-open should close the breaker.
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	if cb.State() != StateClosed {
		t.Fatalf("expected StateClosed after half-open success, got %d", cb.State())
	}

	// Subsequent requests should also pass through.
	req, _ = http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err = c.Do(req)
	if err != nil {
		t.Fatalf("unexpected error after breaker reset: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestSingletonBreakers(t *testing.T) {
	name := uniqueBreakerName()
	b1 := GetBreaker(name, 5, time.Second)
	b2 := GetBreaker(name, 10, 2*time.Second) // different params, same name

	if b1 != b2 {
		t.Fatal("expected same breaker instance for same name")
	}
}

func TestResponseBodyReadableAfterDo(t *testing.T) {
	// Regression: Do() used defer cancel() on its internal context, which
	// cancelled the response body's context before the caller could read it.
	// The server streams the body slowly so it cannot be fully buffered before
	// Do() returns.

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", 500)
			return
		}
		w.WriteHeader(http.StatusOK)
		// Write chunks with delays so the body is still streaming after Do() returns.
		for i := range 5 {
			fmt.Fprintf(w, "chunk-%d\n", i)
			flusher.Flush()
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer srv.Close()

	c := New(WithTimeout(5 * time.Second))
	// Do NOT set a deadline on the request — this triggers the buggy code path
	// where Do() creates its own context.WithTimeout and defers cancel().
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	// This read fails with context.Canceled if defer cancel() fires too early.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body after Do() returned: %v", err)
	}

	expected := "chunk-0\nchunk-1\nchunk-2\nchunk-3\nchunk-4\n"
	if string(body) != expected {
		t.Fatalf("expected body %q, got %q", expected, string(body))
	}
}

func TestTimeoutEnforcement(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Sleep longer than the client timeout.
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(WithTimeout(50 * time.Millisecond))
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)

	start := time.Now()
	_, err := c.Do(req)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}

	// Should have taken roughly the timeout duration, not the full 500ms.
	if elapsed > 300*time.Millisecond {
		t.Fatalf("request took too long (%v), timeout not enforced", elapsed)
	}
}

func TestRetrySpanEvents(t *testing.T) {
	// Set up in-memory span exporter.
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())

	prevTP := otelapi.GetTracerProvider()
	otelapi.SetTracerProvider(tp)
	defer otelapi.SetTracerProvider(prevTP)

	// Server returns 500 twice, then 200.
	srv, _ := counterServer(500, 500)
	defer srv.Close()

	c := New(
		WithTimeout(5*time.Second),
		WithRetry(3, 10*time.Millisecond),
	)
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	tp.ForceFlush(context.Background())

	spans := exporter.GetSpans()
	var retryEvents int
	for _, s := range spans {
		for _, e := range s.Events {
			if e.Name == "retry" {
				retryEvents++
			}
		}
	}
	if retryEvents != 2 {
		t.Fatalf("expected 2 retry span events, got %d", retryEvents)
	}
}

func TestClientSpanNameUsesMethodOnly(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())

	prevTP := otelapi.GetTracerProvider()
	otelapi.SetTracerProvider(tp)
	defer otelapi.SetTracerProvider(prevTP)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(WithTimeout(5 * time.Second))
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/users/123?verbose=true", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	tp.ForceFlush(context.Background())

	for _, s := range exporter.GetSpans() {
		if s.SpanKind != trace.SpanKindClient {
			continue
		}
		if s.Name != http.MethodGet {
			t.Fatalf("client span name = %q, want %q", s.Name, http.MethodGet)
		}
		for _, attr := range s.Attributes {
			if string(attr.Key) == "url.path" && attr.Value.AsString() == "/users/123" {
				return
			}
		}
		t.Fatal("expected client span to include url.path attribute")
	}
	t.Fatal("expected a client span")
}

type countingReadCloser struct {
	remaining int
	read      int
	closed    bool
}

func (b *countingReadCloser) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > b.remaining {
		n = b.remaining
	}
	b.remaining -= n
	b.read += n
	return n, nil
}

func (b *countingReadCloser) Close() error {
	b.closed = true
	return nil
}

func TestRetrierCapsRetryBodyDrain(t *testing.T) {
	firstBody := &countingReadCloser{remaining: 2 << 20}
	attempts := 0
	retrier := &Retrier{MaxAttempts: 2, BaseDelay: time.Nanosecond}

	resp, err := retrier.Do(context.Background(), func() (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: firstBody}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	if firstBody.read > 1<<20 {
		t.Fatalf("retry drain read %d bytes, want at most 1MiB", firstBody.read)
	}
	if !firstBody.closed {
		t.Fatal("expected retry response body to be closed")
	}
}

func TestCircuitBreakerSpanEvents(t *testing.T) {
	// Set up in-memory span exporter.
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())

	prevTP := otelapi.GetTracerProvider()
	otelapi.SetTracerProvider(tp)
	defer otelapi.SetTracerProvider(prevTP)

	srv, _ := counterServer(500, 500, 500, 500)
	defer srv.Close()

	name := uniqueBreakerName()
	c := New(
		WithTimeout(5*time.Second),
		WithCircuitBreaker(name, 3, 1*time.Second),
	)

	// Fire 3 requests that all fail — breaker opens.
	for range 3 {
		req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
		c.Do(req)
	}

	// Fourth request should be rejected by the breaker.
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	_, err := c.Do(req)
	if err != ErrCircuitOpen {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}

	tp.ForceFlush(context.Background())

	spans := exporter.GetSpans()
	var rejectedEvents, recordEvents int
	for _, s := range spans {
		for _, e := range s.Events {
			if e.Name == "circuit_breaker_rejected" {
				rejectedEvents++
			}
			if e.Name == "circuit_breaker_record" {
				recordEvents++
			}
		}
	}
	if rejectedEvents != 1 {
		t.Fatalf("expected 1 circuit_breaker_rejected event, got %d", rejectedEvents)
	}
	if recordEvents < 3 {
		t.Fatalf("expected at least 3 circuit_breaker_record events, got %d", recordEvents)
	}
}

func TestDoPropagatestraceparentHeader(t *testing.T) {
	// Set up in-memory span exporter with TracerProvider.
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	// Set global provider and propagator.
	prevTP := otelapi.GetTracerProvider()
	prevProp := otelapi.GetTextMapPropagator()
	otelapi.SetTracerProvider(tp)
	otelapi.SetTextMapPropagator(propagation.TraceContext{})
	defer func() {
		otelapi.SetTracerProvider(prevTP)
		otelapi.SetTextMapPropagator(prevProp)
	}()

	// Create a test HTTP server that captures the traceparent header.
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Create a parent span context to propagate.
	tracer := tp.Tracer("test")
	ctx, parentSpan := tracer.Start(context.Background(), "parent-op")
	defer parentSpan.End()

	c := New(WithTimeout(5 * time.Second))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/test-path", nil)

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	// Verify traceparent header was set on the outbound request.
	if captured == "" {
		t.Fatal("expected traceparent header to be set on outbound request")
	}
	// traceparent format: version-traceID-spanID-flags
	parts := strings.Split(captured, "-")
	if len(parts) != 4 {
		t.Fatalf("expected 4 parts in traceparent, got %d: %s", len(parts), captured)
	}

	// Force flush to ensure spans are exported.
	tp.ForceFlush(context.Background())

	// Verify a client span was created (SpanKindClient).
	spans := exporter.GetSpans()
	var found bool
	for _, s := range spans {
		if s.SpanKind == trace.SpanKindClient {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected a SpanKindClient span to be created")
	}
}

func TestWithBreakerCustomImplementation(t *testing.T) {
	// Verify that WithBreaker accepts a custom Breaker implementation.
	var allowCalled, recordCalled bool
	custom := &testBreaker{
		allowFn: func() error {
			allowCalled = true
			return nil
		},
		recordFn: func(success bool) {
			recordCalled = true
			if !success {
				t.Error("expected success=true for 200 response")
			}
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(WithTimeout(5*time.Second), WithBreaker(custom))
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	if !allowCalled {
		t.Error("custom breaker Allow() was not called")
	}
	if !recordCalled {
		t.Error("custom breaker Record() was not called")
	}
}

func TestWithBreakerRejectsWhenOpen(t *testing.T) {
	custom := &testBreaker{
		allowFn: func() error {
			return ErrCircuitOpen
		},
		recordFn: func(_ bool) {},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("request should not reach server when breaker is open")
	}))
	defer srv.Close()

	c := New(WithTimeout(5*time.Second), WithBreaker(custom))
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	_, err := c.Do(req)
	if err != ErrCircuitOpen {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
}

// testBreaker is a mock Breaker for testing WithBreaker.
type testBreaker struct {
	allowFn  func() error
	recordFn func(bool)
}

func (b *testBreaker) Allow() error        { return b.allowFn() }
func (b *testBreaker) Record(success bool) { b.recordFn(success) }

func TestBatch(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := New(WithTimeout(5 * time.Second))

	requests := make([]*http.Request, 5)
	for i := range requests {
		requests[i], _ = http.NewRequest(http.MethodGet, srv.URL, nil)
	}

	responses, err := c.Batch(context.Background(), requests, work.Workers(2))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(responses) != 5 {
		t.Fatalf("expected 5 responses, got %d", len(responses))
	}

	for i, resp := range responses {
		if resp.StatusCode != http.StatusOK {
			t.Errorf("response %d: expected 200, got %d", i, resp.StatusCode)
		}
		resp.Body.Close()
	}

	if n := int(hits.Load()); n != 5 {
		t.Fatalf("expected 5 server hits, got %d", n)
	}
}
