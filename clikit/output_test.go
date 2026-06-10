package clikit

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/ai8future/chassis-go/v11/logz"
	"go.opentelemetry.io/otel/trace"
)

func TestEmitterJSONModeSuppressesHumanText(t *testing.T) {
	var buf bytes.Buffer
	e := NewEmitter(&buf, true, ColorNever)
	e.Printf("hello")
	e.Println("world")
	if err := e.Emit(map[string]string{"ok": "true"}); err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	got := buf.String()
	if strings.Count(got, "\n") != 1 || !json.Valid([]byte(got)) {
		t.Fatalf("stdout = %q, want one valid JSON document", got)
	}
	if strings.Contains(got, "hello") || strings.Contains(got, "world") {
		t.Fatalf("JSON mode leaked human text: %q", got)
	}
}

func TestEmitterHumanMode(t *testing.T) {
	var buf bytes.Buffer
	e := NewEmitter(&buf, false, ColorNever)
	e.Printf("hello %s", "there")
	e.Println("!")
	if err := e.Emit("done"); err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"hello there", "!", "done"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q missing %q", got, want)
		}
	}
}

func TestTextLoggerPreservesTraceAttrs(t *testing.T) {
	traceID, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	spanID, _ := trace.SpanIDFromHex("0102030405060708")
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	var buf bytes.Buffer
	logger := logz.NewTextWithWriter("info", &buf)
	logger.InfoContext(ctx, "hello", slog.String("key", "value"))
	got := buf.String()
	if !strings.Contains(got, "trace_id=0102030405060708090a0b0c0d0e0f10") || !strings.Contains(got, "span_id=0102030405060708") {
		t.Fatalf("text log missing trace attrs: %q", got)
	}
}
