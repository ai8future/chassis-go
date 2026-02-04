package httpkit

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ai8future/chassis-go/errors"
	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestRequestID_SetsHeader(t *testing.T) {
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	id := rec.Header().Get("X-Request-ID")
	if id == "" {
		t.Fatal("expected X-Request-ID header to be set")
	}
	// Verify it looks like a UUID (contains hyphens, correct length).
	if len(id) != 36 {
		t.Fatalf("expected request ID length 36, got %d: %q", len(id), id)
	}
	if strings.Count(id, "-") != 4 {
		t.Fatalf("expected 4 hyphens in request ID, got %d: %q", strings.Count(id, "-"), id)
	}
}

func TestRequestID_InContext(t *testing.T) {
	var captured string
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = RequestIDFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/ctx", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if captured == "" {
		t.Fatal("expected request ID in context, got empty string")
	}
	// The context value should match the header.
	if got := rec.Header().Get("X-Request-ID"); got != captured {
		t.Fatalf("header %q != context %q", got, captured)
	}
}

func TestRequestIDFrom_Empty(t *testing.T) {
	id := RequestIDFrom(context.Background())
	if id != "" {
		t.Fatalf("expected empty string from bare context, got %q", id)
	}
}

func TestLogging_LogsRequestDetails(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/items", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	output := buf.String()
	for _, want := range []string{"POST", "/items", "201"} {
		if !strings.Contains(output, want) {
			t.Errorf("log output missing %q:\n%s", want, output)
		}
	}
}

func TestLogging_IncludesRequestID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Chain RequestID -> Logging so the logger can see the ID.
	handler := RequestID(Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest(http.MethodGet, "/with-id", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	output := buf.String()
	if !strings.Contains(output, "request_id") {
		t.Errorf("expected request_id in log output:\n%s", output)
	}
}

func TestRecovery_CatchesPanic(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := Recovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something went wrong")
	}))

	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	rec := httptest.NewRecorder()

	// Should not panic.
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rec.Code)
	}

	// Verify JSON error body.
	var errResp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if errResp.StatusCode != 500 {
		t.Fatalf("expected status_code 500 in body, got %d", errResp.StatusCode)
	}

	// Verify the panic was logged.
	output := buf.String()
	if !strings.Contains(output, "panic recovered") {
		t.Errorf("expected panic log entry:\n%s", output)
	}
}

func TestJSONError_Format(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/err", nil)

	// Add a request ID to context so it appears in the response.
	ctx := context.WithValue(req.Context(), requestIDKey{}, "test-req-123")
	req = req.WithContext(ctx)

	JSONError(rec, req, http.StatusNotFound, "not found")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}

	var errResp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if errResp.Error != "not found" {
		t.Fatalf("expected error %q, got %q", "not found", errResp.Error)
	}
	if errResp.StatusCode != 404 {
		t.Fatalf("expected status_code 404, got %d", errResp.StatusCode)
	}
	if errResp.RequestID != "test-req-123" {
		t.Fatalf("expected request_id %q, got %q", "test-req-123", errResp.RequestID)
	}
}

func TestJSONError_OmitsEmptyRequestID(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/err", nil)

	JSONError(rec, req, http.StatusBadRequest, "bad request")

	body := rec.Body.String()
	if strings.Contains(body, "request_id") {
		t.Fatalf("expected request_id to be omitted, got: %s", body)
	}
}

func TestMiddlewareChain(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Build chain: Recovery -> RequestID -> Logging -> handler
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := RequestIDFrom(r.Context())
		if id == "" {
			t.Error("expected request ID in context within chained handler")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	chain := Recovery(logger)(RequestID(Logging(logger)(inner)))

	req := httptest.NewRequest(http.MethodGet, "/chain", nil)
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Fatal("expected X-Request-ID header in chained response")
	}
	output := buf.String()
	if !strings.Contains(output, "/chain") {
		t.Errorf("expected /chain in log output:\n%s", output)
	}
}

func TestTracingMiddlewareCreatesSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otelapi.SetTracerProvider(tp)

	handler := Tracing()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Name != "GET /hello" {
		t.Fatalf("expected span name %q, got %q", "GET /hello", spans[0].Name)
	}
}

func TestTracingMiddlewarePropagatesIncomingTrace(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otelapi.SetTracerProvider(tp)
	otelapi.SetTextMapPropagator(propagation.TraceContext{})

	handler := Tracing()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/trace", nil)
	// W3C traceparent: version-traceID-spanID-flags
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	traceID := spans[0].SpanContext.TraceID().String()
	if traceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("expected trace ID %q, got %q", "4bf92f3577b34da6a3ce929d0e0e4736", traceID)
	}
}

func TestJSONProblemWritesRFC9457(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/users", nil)
	rec := httptest.NewRecorder()

	svcErr := errors.ValidationError("email is invalid").
		WithDetail("field", "email")
	JSONProblem(rec, req, svcErr)

	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}

	var pd map[string]any
	json.NewDecoder(rec.Body).Decode(&pd)
	if pd["type"] != "https://chassis.ai8future.com/errors/validation" {
		t.Errorf("type = %v", pd["type"])
	}
	if pd["detail"] != "email is invalid" {
		t.Errorf("detail = %v", pd["detail"])
	}
	if pd["instance"] != "/api/users" {
		t.Errorf("instance = %v", pd["instance"])
	}
}
