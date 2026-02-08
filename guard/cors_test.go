package guard_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ai8future/chassis-go/v5/guard"
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

func TestCORSEmptyOriginsPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for empty AllowOrigins")
		}
	}()
	guard.CORS(guard.CORSConfig{})
}
