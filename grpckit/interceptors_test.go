package grpckit

import (
	"context"
	"testing"

	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestUnaryTracingCreatesSpan(t *testing.T) {
	// Set up in-memory span exporter.
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otelapi.SetTracerProvider(tp)

	interceptor := UnaryTracing()

	info := &grpc.UnaryServerInfo{FullMethod: "/api.v1.UserService/GetUser"}
	handler := func(ctx context.Context, req any) (any, error) {
		return "ok", nil
	}

	resp, err := interceptor(context.Background(), "req", info, handler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "ok" {
		t.Fatalf("expected resp 'ok', got %v", resp)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	span := spans[0]
	if span.Name != "/api.v1.UserService/GetUser" {
		t.Errorf("expected span name '/api.v1.UserService/GetUser', got %q", span.Name)
	}

	// Verify rpc.system and rpc.method attributes.
	attrs := make(map[string]string)
	for _, a := range span.Attributes {
		attrs[string(a.Key)] = a.Value.Emit()
	}

	if v, ok := attrs["rpc.system"]; !ok || v != "grpc" {
		t.Errorf("expected rpc.system=grpc, got %q (present=%v)", v, ok)
	}
	if v, ok := attrs["rpc.method"]; !ok || v != "/api.v1.UserService/GetUser" {
		t.Errorf("expected rpc.method='/api.v1.UserService/GetUser', got %q (present=%v)", v, ok)
	}
}

func TestUnaryTracingPropagatesIncomingTrace(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otelapi.SetTracerProvider(tp)
	otelapi.SetTextMapPropagator(propagation.TraceContext{})

	interceptor := UnaryTracing()

	info := &grpc.UnaryServerInfo{FullMethod: "/api.v1.UserService/GetUser"}
	handler := func(ctx context.Context, req any) (any, error) {
		return "ok", nil
	}

	// Simulate incoming gRPC metadata with W3C traceparent.
	md := metadata.Pairs("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	ctx := metadata.NewIncomingContext(context.Background(), md)

	_, err := interceptor(ctx, "req", info, handler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	traceID := spans[0].SpanContext.TraceID().String()
	if traceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("expected trace ID %q, got %q", "4bf92f3577b34da6a3ce929d0e0e4736", traceID)
	}
}

func TestStreamTracingCreatesSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otelapi.SetTracerProvider(tp)

	interceptor := StreamTracing()

	info := &grpc.StreamServerInfo{FullMethod: "/api.v1.UserService/ListUsers"}
	ss := &mockServerStream{ctx: context.Background()}
	handler := func(srv any, stream grpc.ServerStream) error {
		return nil
	}

	err := interceptor(nil, ss, info, handler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	span := spans[0]
	if span.Name != "/api.v1.UserService/ListUsers" {
		t.Errorf("expected span name '/api.v1.UserService/ListUsers', got %q", span.Name)
	}

	attrs := make(map[string]string)
	for _, a := range span.Attributes {
		attrs[string(a.Key)] = a.Value.Emit()
	}

	if v, ok := attrs["rpc.system"]; !ok || v != "grpc" {
		t.Errorf("expected rpc.system=grpc, got %q (present=%v)", v, ok)
	}
	if v, ok := attrs["rpc.method"]; !ok || v != "/api.v1.UserService/ListUsers" {
		t.Errorf("expected rpc.method='/api.v1.UserService/ListUsers', got %q (present=%v)", v, ok)
	}
}
