package tracekit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/tracekit"
)

func init() { chassis.RequireMajor(11) }

// ---------------------------------------------------------------------------
// GenerateID
// ---------------------------------------------------------------------------

func TestGenerateID_Format(t *testing.T) {
	id := tracekit.GenerateID()
	if !strings.HasPrefix(id, "tr_") {
		t.Fatalf("expected tr_ prefix, got %q", id)
	}
	if len(id) != 15 {
		t.Fatalf("expected length 15, got %d (%q)", len(id), id)
	}
	// Verify hex portion is valid hex
	hexPart := id[3:]
	for _, c := range hexPart {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("non-hex char %c in %q", c, id)
		}
	}
}

func TestGenerateID_Uniqueness(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := tracekit.GenerateID()
		if seen[id] {
			t.Fatalf("duplicate ID: %s", id)
		}
		seen[id] = true
	}
}

// ---------------------------------------------------------------------------
// NewTrace / WithTraceID / TraceID
// ---------------------------------------------------------------------------

func TestNewTrace(t *testing.T) {
	ctx := tracekit.NewTrace(context.Background())
	id := tracekit.TraceID(ctx)
	if !strings.HasPrefix(id, "tr_") {
		t.Fatalf("expected tr_ prefix, got %q", id)
	}
	if len(id) != 15 {
		t.Fatalf("expected length 15, got %d", len(id))
	}
}

func TestWithTraceID(t *testing.T) {
	ctx := tracekit.WithTraceID(context.Background(), "tr_custom123456")
	id := tracekit.TraceID(ctx)
	if id != "tr_custom123456" {
		t.Fatalf("expected tr_custom123456, got %q", id)
	}
}

func TestTraceID_EmptyContext(t *testing.T) {
	id := tracekit.TraceID(context.Background())
	if id != "" {
		t.Fatalf("expected empty string, got %q", id)
	}
}

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

func TestMiddleware_ExtractsHeader(t *testing.T) {
	var captured string
	handler := tracekit.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = tracekit.TraceID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Trace-ID", "tr_fromrequest1")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if captured != "tr_fromrequest1" {
		t.Fatalf("expected tr_fromrequest1, got %q", captured)
	}
	if rr.Header().Get("X-Trace-ID") != "tr_fromrequest1" {
		t.Fatalf("response header: expected tr_fromrequest1, got %q", rr.Header().Get("X-Trace-ID"))
	}
}

func TestMiddleware_GeneratesIfMissing(t *testing.T) {
	var captured string
	handler := tracekit.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = tracekit.TraceID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if !strings.HasPrefix(captured, "tr_") {
		t.Fatalf("expected generated trace ID with tr_ prefix, got %q", captured)
	}
	if len(captured) != 15 {
		t.Fatalf("expected length 15, got %d", len(captured))
	}
}

func TestMiddleware_SetsResponseHeader(t *testing.T) {
	handler := tracekit.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	respTrace := rr.Header().Get("X-Trace-ID")
	if !strings.HasPrefix(respTrace, "tr_") {
		t.Fatalf("expected tr_ prefix in response header, got %q", respTrace)
	}
}
