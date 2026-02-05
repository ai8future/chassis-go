package grpckit

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	chassis "github.com/ai8future/chassis-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMain(m *testing.M) {
	chassis.RequireMajor(4)
	os.Exit(m.Run())
}

// newTestLogger returns a logger that writes JSON to the provided buffer.
func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// ---------- Unary interceptor tests ----------

func TestUnaryLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	interceptor := UnaryLogging(logger)

	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/TestMethod"}
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

	log := buf.String()
	if !strings.Contains(log, "unary RPC") {
		t.Errorf("expected log to contain 'unary RPC', got: %s", log)
	}
	if !strings.Contains(log, "/test.Service/TestMethod") {
		t.Errorf("expected log to contain method name, got: %s", log)
	}
	if !strings.Contains(log, "duration") {
		t.Errorf("expected log to contain duration, got: %s", log)
	}
}

func TestUnaryLogging_WithError(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	interceptor := UnaryLogging(logger)

	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Fail"}
	handler := func(ctx context.Context, req any) (any, error) {
		return nil, status.Error(codes.NotFound, "not found")
	}

	_, err := interceptor(context.Background(), "req", info, handler)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	log := buf.String()
	if !strings.Contains(log, "error") {
		t.Errorf("expected log to contain error field, got: %s", log)
	}
}

func TestUnaryRecovery(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	interceptor := UnaryRecovery(logger)

	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Panic"}
	handler := func(ctx context.Context, req any) (any, error) {
		panic("something went wrong")
	}

	_, err := interceptor(context.Background(), "req", info, handler)
	if err == nil {
		t.Fatal("expected error after panic, got nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("expected codes.Internal, got %v", st.Code())
	}

	log := buf.String()
	if !strings.Contains(log, "panic recovered") {
		t.Errorf("expected log to contain 'panic recovered', got: %s", log)
	}
	if !strings.Contains(log, "something went wrong") {
		t.Errorf("expected log to contain panic value, got: %s", log)
	}
}

// ---------- Stream interceptor tests ----------

// mockServerStream is a minimal grpc.ServerStream implementation for testing.
type mockServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (m *mockServerStream) Context() context.Context { return m.ctx }

func TestStreamLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	interceptor := StreamLogging(logger)

	info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}
	ss := &mockServerStream{ctx: context.Background()}
	handler := func(srv any, stream grpc.ServerStream) error {
		return nil
	}

	err := interceptor(nil, ss, info, handler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	log := buf.String()
	if !strings.Contains(log, "stream RPC") {
		t.Errorf("expected log to contain 'stream RPC', got: %s", log)
	}
	if !strings.Contains(log, "/test.Service/StreamMethod") {
		t.Errorf("expected log to contain method name, got: %s", log)
	}
}

func TestStreamRecovery(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	interceptor := StreamRecovery(logger)

	info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamPanic"}
	ss := &mockServerStream{ctx: context.Background()}
	handler := func(srv any, stream grpc.ServerStream) error {
		panic("stream panic")
	}

	err := interceptor(nil, ss, info, handler)
	if err == nil {
		t.Fatal("expected error after panic, got nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("expected codes.Internal, got %v", st.Code())
	}

	log := buf.String()
	if !strings.Contains(log, "panic recovered") {
		t.Errorf("expected log to contain 'panic recovered', got: %s", log)
	}
	if !strings.Contains(log, "stream panic") {
		t.Errorf("expected log to contain panic value, got: %s", log)
	}
}

// ---------- Metrics interceptor tests ----------

func TestUnaryMetrics(t *testing.T) {
	interceptor := UnaryMetrics()

	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/MetricsMethod"}
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
	// UnaryMetrics now records an OTel histogram rather than logging.
	// We verify it doesn't panic and returns correctly. Full metric
	// verification requires an OTel SDK test meter.
}

func TestStreamMetrics(t *testing.T) {
	interceptor := StreamMetrics()

	info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMetrics"}
	ss := &mockServerStream{ctx: context.Background()}
	handler := func(srv any, stream grpc.ServerStream) error {
		return nil
	}

	err := interceptor(nil, ss, info, handler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// StreamMetrics now records an OTel histogram rather than logging.
}
