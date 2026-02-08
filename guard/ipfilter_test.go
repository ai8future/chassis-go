package guard_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ai8future/chassis-go/v5/guard"
)

func TestIPFilterAllowOnly(t *testing.T) {
	mw := guard.IPFilter(guard.IPFilterConfig{
		Allow: []string{"192.168.1.0/24"},
	})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Allowed IP
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.50:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("allowed IP: expected 200, got %d", rec.Code)
	}

	// Denied IP
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("denied IP: expected 403, got %d", rec.Code)
	}
}

func TestIPFilterDenyOnly(t *testing.T) {
	mw := guard.IPFilter(guard.IPFilterConfig{
		Deny: []string{"10.0.0.0/8"},
	})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Denied IP
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.1.2.3:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("denied IP: expected 403, got %d", rec.Code)
	}

	// Allowed IP (not in deny list)
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("allowed IP: expected 200, got %d", rec.Code)
	}
}

func TestIPFilterDenyTakesPrecedence(t *testing.T) {
	mw := guard.IPFilter(guard.IPFilterConfig{
		Allow: []string{"10.0.0.0/8"},
		Deny:  []string{"10.0.0.1/32"},
	})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Denied despite being in allow range
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (deny takes precedence), got %d", rec.Code)
	}

	// Allowed (in allow range, not in deny)
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:12345"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestIPFilterInvalidCIDRPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for invalid CIDR")
		}
	}()
	guard.IPFilter(guard.IPFilterConfig{
		Allow: []string{"not-a-cidr"},
	})
}

func TestIPFilterEmptyConfigPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for empty Allow and Deny")
		}
	}()
	guard.IPFilter(guard.IPFilterConfig{})
}

func TestIPFilter403IsProblemJSON(t *testing.T) {
	mw := guard.IPFilter(guard.IPFilterConfig{
		Allow: []string{"192.168.1.0/24"},
	})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/secret", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}

	var pd map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&pd); err != nil {
		t.Fatalf("failed to decode problem detail: %v", err)
	}
	if pd["type"] != "https://chassis.ai8future.com/errors/forbidden" {
		t.Errorf("type = %v", pd["type"])
	}
	if pd["title"] != "Forbidden" {
		t.Errorf("title = %v", pd["title"])
	}
	if int(pd["status"].(float64)) != 403 {
		t.Errorf("status = %v", pd["status"])
	}
}
