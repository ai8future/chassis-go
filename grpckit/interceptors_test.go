package grpckit

import (
	"context"
	"log/slog"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	otelapi "go.opentelemetry.io/otel"
	"google.golang.org/grpc"
)

func TestUnaryTracingCreatesSpan(t *testing.T) {
	// Set up in-memory span exporter.
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otelapi.SetTracerProvider(tp)

	logger := slog.Default()
	interceptor := UnaryTracing(logger)

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

func TestStreamTracingCreatesSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otelapi.SetTracerProvider(tp)

	logger := slog.Default()
	interceptor := StreamTracing(logger)

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
