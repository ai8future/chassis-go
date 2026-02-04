// otel/otel_test.go
package otel_test

import (
	"context"
	"testing"

	chassis "github.com/ai8future/chassis-go"
	"github.com/ai8future/chassis-go/otel"
	"go.opentelemetry.io/otel/trace"
)

func TestInitReturnsShutdownFunc(t *testing.T) {
	chassis.ResetVersionCheck()
	chassis.RequireMajor(3)

	shutdown := otel.Init(otel.Config{
		ServiceName:    "test-svc",
		ServiceVersion: "1.0.0",
	})
	if shutdown == nil {
		t.Fatal("Init returned nil shutdown function")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown returned error: %v", err)
	}
}

func TestDetachContextPreservesSpanContext(t *testing.T) {
	chassis.ResetVersionCheck()
	chassis.RequireMajor(3)

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

func TestDetachContextWithNoSpanReturnsBackground(t *testing.T) {
	detached := otel.DetachContext(context.Background())
	sc := trace.SpanContextFromContext(detached)
	if sc.IsValid() {
		t.Fatal("expected invalid span context from empty parent")
	}
}
