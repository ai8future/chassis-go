package guard_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ai8future/chassis-go/v11/guard"
)

func TestCORSPreflightReturns204(t *testing.T) {
	mw := guard.CORS(guard.CORSConfig{
		AllowOrigins: []string{"https://example.com"},
		MaxAge:       24 * time.Hour,
	})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for preflight")
	}))

	req := httptest.NewRequest(http.MethodOptions, "/api/data", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Fatalf("Allow-Origin = %q, want %q", got, "https://example.com")
	}
	if got := rec.Header().Get("Access-Control-Max-Age"); got != "86400" {
		t.Fatalf("Max-Age = %q, want %q", got, "86400")
	}
}

func TestCORSMatchingOriginSetsHeader(t *testing.T) {
	mw := guard.CORS(guard.CORSConfig{
		AllowOrigins: []string{"https://example.com", "https://other.com"},
	})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://other.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://other.com" {
		t.Fatalf("Allow-Origin = %q, want %q", got, "https://other.com")
	}
}

func TestCORSNonMatchingOriginNoHeader(t *testing.T) {
	mw := guard.CORS(guard.CORSConfig{
		AllowOrigins: []string{"https://example.com"},
	})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no Allow-Origin header, got %q", got)
	}
}

func TestCORSWildcard(t *testing.T) {
	mw := guard.CORS(guard.CORSConfig{
		AllowOrigins: []string{"*"},
	})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://anything.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Allow-Origin = %q, want %q", got, "*")
	}
}

func TestCORSCredentialsWithWildcardPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for credentials with wildcard")
		}
	}()
	guard.CORS(guard.CORSConfig{
		AllowOrigins:     []string{"*"},
		AllowCredentials: true,
	})
}

func TestCORSPassthroughWithoutOrigin(t *testing.T) {
	called := false
	mw := guard.CORS(guard.CORSConfig{
		AllowOrigins: []string{"https://example.com"},
	})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// No Origin header.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("handler should be called without Origin header")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no Allow-Origin header, got %q", got)
	}
}

func TestCORSAllowCredentials(t *testing.T) {
	cfg := guard.CORSConfig{
		AllowOrigins:     []string{"https://example.com"},
		AllowCredentials: true,
		AllowMethods:     []string{"GET", "POST"},
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	mw := guard.CORS(cfg)(handler)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://example.com")
	mw.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Allow-Credentials = %q, want %q", got, "true")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("Allow-Origin = %q, want %q (must echo origin, not *)", got, "https://example.com")
	}
}

func TestCORSPreflightCustomMethods(t *testing.T) {
	cfg := guard.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{"GET", "DELETE", "PATCH"},
		AllowHeaders: []string{"X-Custom-Header"},
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	mw := guard.CORS(cfg)(handler)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/", nil)
	req.Header.Set("Origin", "https://test.com")
	req.Header.Set("Access-Control-Request-Method", "DELETE")
	mw.ServeHTTP(rec, req)

	methods := rec.Header().Get("Access-Control-Allow-Methods")
	if !strings.Contains(methods, "DELETE") {
		t.Errorf("Allow-Methods = %q, should contain DELETE", methods)
	}

	headers := rec.Header().Get("Access-Control-Allow-Headers")
	if !strings.Contains(headers, "X-Custom-Header") {
		t.Errorf("Allow-Headers = %q, should contain X-Custom-Header", headers)
	}
}

func TestCORSEmptyOriginsPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for empty AllowOrigins")
		}
	}()
	guard.CORS(guard.CORSConfig{})
}
