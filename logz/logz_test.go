package logz

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"

	chassis "github.com/ai8future/chassis-go/v5"
	"go.opentelemetry.io/otel/trace"
)

func TestMain(m *testing.M) {
	chassis.RequireMajor(5)
	os.Exit(m.Run())
}

// newTestLogger creates a logger that writes JSON to the provided buffer
// at the specified level, with trace ID injection via traceHandler.
func newTestLogger(buf *bytes.Buffer, level string) *slog.Logger {
	lvl := parseLevel(level)
	inner := slog.NewJSONHandler(buf, &slog.HandlerOptions{
		Level: lvl,
	})
	return slog.New(&traceHandler{inner: inner, base: inner})
}

func TestNewCreatesLoggerAtEachLevel(t *testing.T) {
	levels := []string{"debug", "info", "warn", "error"}
	for _, lvl := range levels {
		t.Run(lvl, func(t *testing.T) {
			logger := New(lvl)
			if logger == nil {
				t.Fatalf("New(%q) returned nil", lvl)
			}
		})
	}
}

func TestNewCaseInsensitive(t *testing.T) {
	for _, lvl := range []string{"DEBUG", "Info", "WARN", "Error"} {
		logger := New(lvl)
		if logger == nil {
			t.Fatalf("New(%q) returned nil", lvl)
		}
	}
}

func TestInvalidLevelDefaultsToInfo(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, "bogus")

	// Debug should NOT be logged at info level.
	logger.Debug("should not appear")
	if buf.Len() != 0 {
		t.Fatal("expected no output for debug message at info level, got:", buf.String())
	}

	// Info should be logged.
	logger.Info("hello")
	if buf.Len() == 0 {
		t.Fatal("expected output for info message at info level")
	}
}

func TestJSONOutputFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, "info")

	logger.Info("test message", "key", "value")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
	}

	// Verify expected fields.
	if msg, ok := entry["msg"].(string); !ok || msg != "test message" {
		t.Errorf("expected msg=%q, got %v", "test message", entry["msg"])
	}
	if v, ok := entry["key"].(string); !ok || v != "value" {
		t.Errorf("expected key=%q, got %v", "value", entry["key"])
	}
	if _, ok := entry["level"]; !ok {
		t.Error("expected 'level' field in JSON output")
	}
	if _, ok := entry["time"]; !ok {
		t.Error("expected 'time' field in JSON output")
	}
}

func TestTraceIDInjectionFromOTel(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, "info")

	traceIDHex, _ := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
	spanIDHex, _ := trace.SpanIDFromHex("b7ad6b7169203331")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceIDHex,
		SpanID:     spanIDHex,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	logger.InfoContext(ctx, "traced message")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
	}

	tid, ok := entry["trace_id"].(string)
	if !ok || tid != "0af7651916cd43dd8448eb211c80319c" {
		t.Errorf("expected trace_id=%q, got %v", "0af7651916cd43dd8448eb211c80319c", entry["trace_id"])
	}
	sid, ok := entry["span_id"].(string)
	if !ok || sid != "b7ad6b7169203331" {
		t.Errorf("expected span_id=%q, got %v", "b7ad6b7169203331", entry["span_id"])
	}
}

func TestTraceIDAbsent(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, "info")

	logger.Info("no trace")

	raw := buf.String()
	if strings.Contains(raw, "trace_id") {
		t.Errorf("expected no trace_id in output, got: %s", raw)
	}
}

func TestLoggerRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, "error")

	logger.Info("should not appear")
	if buf.Len() != 0 {
		t.Fatalf("info message should not appear at error level, got: %s", buf.String())
	}

	logger.Error("should appear")
	if buf.Len() == 0 {
		t.Fatal("error message should appear at error level")
	}
}

func TestWithAttrsPreservesTraceHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, "info")
	logger = logger.With("service", "test-svc")

	traceIDHex, _ := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
	spanIDHex, _ := trace.SpanIDFromHex("b7ad6b7169203331")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceIDHex,
		SpanID:     spanIDHex,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	logger.InfoContext(ctx, "with attrs")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
	}

	if v, _ := entry["service"].(string); v != "test-svc" {
		t.Errorf("expected service=%q, got %v", "test-svc", entry["service"])
	}
	if v, _ := entry["trace_id"].(string); v != "0af7651916cd43dd8448eb211c80319c" {
		t.Errorf("expected trace_id=%q, got %v", "0af7651916cd43dd8448eb211c80319c", entry["trace_id"])
	}
}

func TestWithGroupPreservesTraceHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, "info")
	logger = logger.WithGroup("grp")

	traceIDHex, _ := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
	spanIDHex, _ := trace.SpanIDFromHex("b7ad6b7169203331")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceIDHex,
		SpanID:     spanIDHex,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	logger.InfoContext(ctx, "grouped", "k", "v")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
	}

	// trace_id should be at top level (added before group delegation).
	if v, _ := entry["trace_id"].(string); v != "0af7651916cd43dd8448eb211c80319c" {
		t.Errorf("expected trace_id=%q, got %v", "0af7651916cd43dd8448eb211c80319c", entry["trace_id"])
	}

	// "k" should be nested under "grp".
	grp, ok := entry["grp"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'grp' group in output, got: %v", entry)
	}
	if v, _ := grp["k"].(string); v != "v" {
		t.Errorf("expected grp.k=%q, got %v", "v", grp["k"])
	}
}

func TestTraceHandlerReadsOTelSpanContext(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(&traceHandler{inner: inner, base: inner})

	traceIDHex, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanIDHex, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceIDHex,
		SpanID:     spanIDHex,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	logger.InfoContext(ctx, "otel traced message")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
	}

	if v, _ := entry["trace_id"].(string); v != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("expected trace_id=%q, got %v", "4bf92f3577b34da6a3ce929d0e0e4736", entry["trace_id"])
	}
	if v, _ := entry["span_id"].(string); v != "00f067aa0ba902b7" {
		t.Errorf("expected span_id=%q, got %v", "00f067aa0ba902b7", entry["span_id"])
	}
}

func TestTraceHandlerOmitsFieldsWithNoSpan(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(&traceHandler{inner: inner, base: inner})

	logger.InfoContext(context.Background(), "no span context")

	raw := buf.String()
	if strings.Contains(raw, "trace_id") {
		t.Errorf("expected no trace_id in output, got: %s", raw)
	}
	if strings.Contains(raw, "span_id") {
		t.Errorf("expected no span_id in output, got: %s", raw)
	}
}
