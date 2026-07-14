package clikit

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/logz"
	"go.opentelemetry.io/otel/trace"
)

type outputStringer string

func (s outputStringer) String() string { return string(s) }

func init() {
	chassis.RequireMajor(11)
}

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

func TestEmitterHumanModeFormatsSupportedValues(t *testing.T) {
	var buf bytes.Buffer
	e := NewEmitter(&buf, false, ColorAlways)
	for _, value := range []any{[]byte("bytes"), outputStringer("stringer"), 42} {
		if err := e.Emit(value); err != nil {
			t.Fatal(err)
		}
	}
	if got := buf.String(); got != "bytes\nstringer\n42\n" {
		t.Fatalf("output = %q", got)
	}
	if e.Color() != ColorAlways {
		t.Fatalf("Color = %v", e.Color())
	}
}

func TestEmitterJSONWritesRegardlessOfMode(t *testing.T) {
	var buf bytes.Buffer
	e := NewEmitter(&buf, false, ColorNever)
	if err := e.JSON(map[string]int{"answer": 42}); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(buf.Bytes()) || !strings.Contains(buf.String(), "42") {
		t.Fatalf("output = %q", buf.String())
	}
}

func TestNilEmitterMethodsAreNoops(t *testing.T) {
	var e *Emitter
	e.Printf("ignored")
	e.Println("ignored")
	if err := e.Emit("ignored"); err != nil {
		t.Fatal(err)
	}
	if err := e.JSON("ignored"); err != nil {
		t.Fatal(err)
	}
	if e.Color() != ColorNever {
		t.Fatalf("Color = %v", e.Color())
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
