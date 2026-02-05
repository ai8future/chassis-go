package call

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go"
	"github.com/ai8future/chassis-go/work"
	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestMain(m *testing.M) {
	chassis.RequireMajor(4)
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
