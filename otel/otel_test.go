// otel/otel_test.go
package otel_test

import (
	"context"
	"strings"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v5"
	"github.com/ai8future/chassis-go/v5/otel"
	"go.opentelemetry.io/otel/trace"
)

// shutdownWithShortTimeout calls the shutdown function with a short deadline
// to avoid long waits when no OTLP collector is available.
func shutdownWithShortTimeout(t *testing.T, shutdown otel.ShutdownFunc) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	return shutdown(ctx)
}

// isCollectorUnavailable returns true if the error indicates the OTLP collector
// is not reachable — expected in test environments without a local collector.
func isCollectorUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "code = Unavailable") ||
		strings.Contains(msg, "context deadline exceeded")
}

func TestInitReturnsShutdownFunc(t *testing.T) {
	chassis.ResetVersionCheck()
	chassis.RequireMajor(5)

	shutdown := otel.Init(otel.Config{
		ServiceName:    "test-svc",
		ServiceVersion: "1.0.0",
		Insecure:       true, // plaintext for local test
	})
	if shutdown == nil {
		t.Fatal("Init returned nil shutdown function")
	}
	if err := shutdownWithShortTimeout(t, shutdown); err != nil && !isCollectorUnavailable(err) {
		t.Fatalf("shutdown returned unexpected error: %v", err)
	}
}

func TestDetachContextPreservesSpanContext(t *testing.T) {
	chassis.ResetVersionCheck()
	chassis.RequireMajor(5)

	// Create a span context with a known trace ID.
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     false,
	})

	parentCtx, cancel := context.WithCancel(context.Background())
	parentCtx = trace.ContextWithSpanContext(parentCtx, sc)

	detached := otel.DetachContext(parentCtx)

	// Cancel the parent — detached should not be affected.
	cancel()

	select {
	case <-detached.Done():
		t.Fatal("detached context was cancelled when parent was cancelled")
	default:
		// expected
	}

	// Span context should be preserved.
	got := trace.SpanContextFromContext(detached)
	if got.TraceID() != traceID {
		t.Fatalf("trace ID not preserved: got %s, want %s", got.TraceID(), traceID)
	}
	if got.SpanID() != spanID {
		t.Fatalf("span ID not preserved: got %s, want %s", got.SpanID(), spanID)
	}
}

func TestInit_InsecureExplicitlySet(t *testing.T) {
	chassis.ResetVersionCheck()
	chassis.RequireMajor(5)

	// With Insecure=true, Init should use plaintext gRPC.
	shutdown := otel.Init(otel.Config{
		ServiceName:    "test-insecure",
		ServiceVersion: "1.0.0",
		Insecure:       true,
	})
	if shutdown == nil {
		t.Fatal("Init returned nil shutdown function")
	}
	if err := shutdownWithShortTimeout(t, shutdown); err != nil && !isCollectorUnavailable(err) {
		t.Fatalf("shutdown returned unexpected error: %v", err)
	}
}

func TestInit_DefaultTLS(t *testing.T) {
	chassis.ResetVersionCheck()
	chassis.RequireMajor(5)

	// Default (Insecure=false) should attempt TLS connection.
	// Without a TLS endpoint, Init still succeeds (lazy connection) but
	// shutdown may return errors when flushing to a non-TLS endpoint.
	shutdown := otel.Init(otel.Config{
		ServiceName:    "test-tls-default",
		ServiceVersion: "1.0.0",
	})
	if shutdown == nil {
		t.Fatal("Init returned nil shutdown function")
	}
	// Shutdown errors are expected in test (no TLS endpoint) — just verify
	// it doesn't panic.
	_ = shutdownWithShortTimeout(t, shutdown)
}

func TestDetachContextWithNoSpanReturnsBackground(t *testing.T) {
	detached := otel.DetachContext(context.Background())
	sc := trace.SpanContextFromContext(detached)
	if sc.IsValid() {
		t.Fatal("expected invalid span context from empty parent")
	}
}

func TestAlwaysSampleReturnsNonNil(t *testing.T) {
	s := otel.AlwaysSample()
	if s == nil {
		t.Fatal("AlwaysSample() returned nil")
	}
}

func TestRatioSampleReturnsNonNil(t *testing.T) {
	s := otel.RatioSample(0.5)
	if s == nil {
		t.Fatal("RatioSample(0.5) returned nil")
	}
}

func TestInitWithCustomSampler(t *testing.T) {
	chassis.ResetVersionCheck()
	chassis.RequireMajor(5)

	shutdown := otel.Init(otel.Config{
		ServiceName:    "test-custom-sampler",
		ServiceVersion: "1.0.0",
		Insecure:       true,
		Sampler:        otel.RatioSample(0.1),
	})
	if shutdown == nil {
		t.Fatal("Init returned nil shutdown function")
	}
	if err := shutdownWithShortTimeout(t, shutdown); err != nil && !isCollectorUnavailable(err) {
		t.Fatalf("shutdown returned unexpected error: %v", err)
	}
}
