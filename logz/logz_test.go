package logz

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

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

func TestTraceIDInjection(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, "info")

	ctx := WithTraceID(context.Background(), "abc-123")
	logger.InfoContext(ctx, "traced message")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
	}

	traceID, ok := entry["trace_id"].(string)
	if !ok || traceID != "abc-123" {
		t.Errorf("expected trace_id=%q, got %v", "abc-123", entry["trace_id"])
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

func TestWithTraceIDAndTraceIDFromRoundTrip(t *testing.T) {
	ctx := context.Background()

	// No trace ID set yet.
	if got := TraceIDFrom(ctx); got != "" {
		t.Errorf("expected empty trace ID from bare context, got %q", got)
	}

	// Set and retrieve.
	ctx = WithTraceID(ctx, "xyz-789")
	if got := TraceIDFrom(ctx); got != "xyz-789" {
		t.Errorf("expected trace ID %q, got %q", "xyz-789", got)
	}

	// Overwrite.
	ctx = WithTraceID(ctx, "new-id")
	if got := TraceIDFrom(ctx); got != "new-id" {
		t.Errorf("expected trace ID %q, got %q", "new-id", got)
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

	ctx := WithTraceID(context.Background(), "attr-trace")
	logger.InfoContext(ctx, "with attrs")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
	}

	if v, _ := entry["service"].(string); v != "test-svc" {
		t.Errorf("expected service=%q, got %v", "test-svc", entry["service"])
	}
	if v, _ := entry["trace_id"].(string); v != "attr-trace" {
		t.Errorf("expected trace_id=%q, got %v", "attr-trace", entry["trace_id"])
	}
}

func TestWithGroupPreservesTraceHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, "info")
	logger = logger.WithGroup("grp")

	ctx := WithTraceID(context.Background(), "group-trace")
	logger.InfoContext(ctx, "grouped", "k", "v")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
	}

	// trace_id should be at top level (added before group delegation).
	if v, _ := entry["trace_id"].(string); v != "group-trace" {
		t.Errorf("expected trace_id=%q, got %v", "group-trace", entry["trace_id"])
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
