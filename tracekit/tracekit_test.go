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
	if len(id) != 35 {
		t.Fatalf("expected length 35, got %d (%q)", len(id), id)
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
	if len(id) != 35 {
		t.Fatalf("expected length 35, got %d", len(id))
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
	req.Header.Set("X-Trace-ID", "tr_0123456789abcdef0123456789abcdef")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if captured != "tr_0123456789abcdef0123456789abcdef" {
		t.Fatalf("expected canonical trace id, got %q", captured)
	}
	if rr.Header().Get("X-Trace-ID") != "tr_0123456789abcdef0123456789abcdef" {
		t.Fatalf("response header: expected canonical trace id, got %q", rr.Header().Get("X-Trace-ID"))
	}
}

func TestMiddleware_AcceptsBoundedLegacyHeader(t *testing.T) {
	var captured string
	handler := tracekit.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = tracekit.TraceID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Trace-ID", "tr_0123456789ab")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if captured != "tr_0123456789ab" {
		t.Fatalf("expected bounded legacy trace id, got %q", captured)
	}
}

func TestMiddleware_RegeneratesInvalidHeader(t *testing.T) {
	var captured string
	handler := tracekit.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = tracekit.TraceID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Trace-ID", "tr_fromrequest1")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if captured == "tr_fromrequest1" {
		t.Fatal("invalid trace id should be regenerated")
	}
	if !strings.HasPrefix(captured, "tr_") || len(captured) != 35 {
		t.Fatalf("expected generated canonical trace ID, got %q", captured)
	}
}

func TestMiddleware_AcceptsLegacyShortHeader(t *testing.T) {
	const legacyTraceID = "tr_a1b2c3d4e5f6"
	var captured string
	handler := tracekit.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = tracekit.TraceID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Trace-ID", legacyTraceID)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if captured != legacyTraceID {
		t.Fatalf("expected %q, got %q", legacyTraceID, captured)
	}
	if rr.Header().Get("X-Trace-ID") != legacyTraceID {
		t.Fatalf("response header: expected %q, got %q", legacyTraceID, rr.Header().Get("X-Trace-ID"))
	}
}

func TestMiddleware_AcceptsCanonicalHeader(t *testing.T) {
	const canonicalTraceID = "tr_0123456789abcdef0123456789abcdef"
	var captured string
	handler := tracekit.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = tracekit.TraceID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Trace-ID", canonicalTraceID)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if captured != canonicalTraceID {
		t.Fatalf("expected %q, got %q", canonicalTraceID, captured)
	}
	if rr.Header().Get("X-Trace-ID") != canonicalTraceID {
		t.Fatalf("response header: expected %q, got %q", canonicalTraceID, rr.Header().Get("X-Trace-ID"))
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
	if len(captured) != 35 {
		t.Fatalf("expected length 35, got %d", len(captured))
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
