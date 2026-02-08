package guard_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ai8future/chassis-go/v5/guard"
)

func TestSecurityHeadersDefaults(t *testing.T) {
	mw := guard.SecurityHeaders(guard.DefaultSecurityHeaders)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	checks := map[string]string{
		"Content-Security-Policy":   "default-src 'self'",
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Referrer-Policy":           "strict-origin-when-cross-origin",
		"Cross-Origin-Opener-Policy": "same-origin",
	}
	for header, want := range checks {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}

	// Permissions-Policy
	if got := rec.Header().Get("Permissions-Policy"); got != "geolocation=(), camera=(), microphone=()" {
		t.Errorf("Permissions-Policy = %q", got)
	}
}

func TestSecurityHeadersCSPOverride(t *testing.T) {
	cfg := guard.DefaultSecurityHeaders
	cfg.ContentSecurityPolicy = "default-src 'self'; script-src cdn.example.com"

	mw := guard.SecurityHeaders(cfg)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Security-Policy"); got != "default-src 'self'; script-src cdn.example.com" {
		t.Errorf("CSP = %q", got)
	}
}

func TestSecurityHeadersHSTSFormat(t *testing.T) {
	mw := guard.SecurityHeaders(guard.DefaultSecurityHeaders)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// HSTS should only be set over HTTPS (via X-Forwarded-Proto).
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	hsts := rec.Header().Get("Strict-Transport-Security")
	if !strings.HasPrefix(hsts, "max-age=63072000") {
		t.Errorf("HSTS = %q, expected max-age=63072000 prefix", hsts)
	}
	if !strings.Contains(hsts, "includeSubDomains") {
		t.Errorf("HSTS missing includeSubDomains: %q", hsts)
	}
	if !strings.Contains(hsts, "preload") {
		t.Errorf("HSTS missing preload: %q", hsts)
	}

	// HSTS should NOT be set over plain HTTP.
	reqHTTP := httptest.NewRequest(http.MethodGet, "/", nil)
	recHTTP := httptest.NewRecorder()
	handler.ServeHTTP(recHTTP, reqHTTP)

	if got := recHTTP.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS should not be set over HTTP, got %q", got)
	}
}

func TestSecurityHeadersEmptyCSPNotSet(t *testing.T) {
	cfg := guard.SecurityHeadersConfig{
		XContentTypeOptions: "nosniff",
	}

	mw := guard.SecurityHeaders(cfg)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Security-Policy"); got != "" {
		t.Errorf("expected no CSP header, got %q", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}
