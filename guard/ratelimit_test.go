package guard_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v5"
	"github.com/ai8future/chassis-go/v5/guard"
)

func TestMain(m *testing.M) {
	chassis.RequireMajor(5)
	os.Exit(m.Run())
}

func TestRateLimitAllowsWithinLimit(t *testing.T) {
	mw := guard.RateLimit(guard.RateLimitConfig{
		Rate:    5,
		Window:  time.Minute,
		KeyFunc: guard.RemoteAddr(),
		MaxKeys: 1000,
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rec.Code)
		}
	}
}

func TestRateLimitRejectsOverLimit(t *testing.T) {
	mw := guard.RateLimit(guard.RateLimitConfig{
		Rate:    2,
		Window:  time.Hour, // long window so no refill happens
		KeyFunc: guard.RemoteAddr(),
		MaxKeys: 1000,
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "10.0.0.1:9999"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if i < 2 && rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rec.Code)
		}
		if i == 2 {
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("request %d: expected 429, got %d", i+1, rec.Code)
			}
			ct := rec.Header().Get("Content-Type")
			if ct != "application/problem+json" {
				t.Fatalf("Content-Type = %q, want application/problem+json", ct)
			}
			var pd map[string]any
			if err := json.NewDecoder(rec.Body).Decode(&pd); err != nil {
				t.Fatalf("failed to decode problem detail: %v", err)
			}
			if pd["type"] != "https://chassis.ai8future.com/errors/rate-limit" {
				t.Errorf("type = %v", pd["type"])
			}
			if pd["title"] != "Rate Limit Exceeded" {
				t.Errorf("title = %v", pd["title"])
			}
			if int(pd["status"].(float64)) != 429 {
				t.Errorf("status = %v", pd["status"])
			}
		}
	}
}

func TestXForwardedForExtractor(t *testing.T) {
	mw := guard.RateLimit(guard.RateLimitConfig{
		Rate:    1,
		Window:  time.Hour,
		KeyFunc: guard.XForwardedFor("10.0.0.0/8"),
		MaxKeys: 1000,
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request from XFF client IP — should pass
	req1 := httptest.NewRequest("GET", "/", nil)
	req1.RemoteAddr = "10.0.0.1:8080"
	req1.Header.Set("X-Forwarded-For", "203.0.113.50")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", rec1.Code)
	}

	// Second request from same XFF client IP — should be rejected
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.RemoteAddr = "10.0.0.1:8080"
	req2.Header.Set("X-Forwarded-For", "203.0.113.50")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: expected 429, got %d", rec2.Code)
	}
}

func TestXForwardedForIgnoresUntrustedProxy(t *testing.T) {
	mw := guard.RateLimit(guard.RateLimitConfig{
		Rate:    1,
		Window:  time.Hour,
		KeyFunc: guard.XForwardedFor("172.16.0.0/12"), // only trust 172.16.x.x
		MaxKeys: 1000,
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Request from untrusted proxy — XFF should be ignored, key = RemoteAddr
	req1 := httptest.NewRequest("GET", "/", nil)
	req1.RemoteAddr = "192.168.1.1:5555"
	req1.Header.Set("X-Forwarded-For", "203.0.113.99")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", rec1.Code)
	}

	// Second request from same RemoteAddr but different XFF — should be 429
	// because key is RemoteAddr (192.168.1.1), not the XFF value
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.RemoteAddr = "192.168.1.1:5555"
	req2.Header.Set("X-Forwarded-For", "203.0.113.100")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: expected 429, got %d (XFF should be ignored for untrusted proxy)", rec2.Code)
	}
}

func TestRateLimit_LRUEviction(t *testing.T) {
	mw := guard.RateLimit(guard.RateLimitConfig{
		Rate:    1,
		Window:  time.Hour,
		KeyFunc: guard.RemoteAddr(),
		MaxKeys: 2,
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Use up the token for IP-A.
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.1.1.1:1111"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("IP-A first: expected 200, got %d", rec.Code)
	}

	// Use up the token for IP-B.
	req = httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "2.2.2.2:2222"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("IP-B first: expected 200, got %d", rec.Code)
	}

	// Add IP-C — this should evict IP-A (LRU).
	req = httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "3.3.3.3:3333"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("IP-C first: expected 200, got %d", rec.Code)
	}

	// IP-A should be evicted and get a fresh bucket (1 token).
	req = httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.1.1.1:1111"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("IP-A after eviction: expected 200 (fresh bucket), got %d", rec.Code)
	}
}

func TestRateLimit_PanicsOnZeroRate(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for zero Rate")
		}
	}()
	guard.RateLimit(guard.RateLimitConfig{
		Rate:    0,
		Window:  time.Minute,
		KeyFunc: guard.RemoteAddr(),
		MaxKeys: 100,
	})
}

func TestRateLimit_PanicsOnZeroWindow(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for zero Window")
		}
	}()
	guard.RateLimit(guard.RateLimitConfig{
		Rate:    10,
		Window:  0,
		KeyFunc: guard.RemoteAddr(),
		MaxKeys: 100,
	})
}

func TestRateLimit_PanicsOnNilKeyFunc(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil KeyFunc")
		}
	}()
	guard.RateLimit(guard.RateLimitConfig{
		Rate:    10,
		Window:  time.Minute,
		KeyFunc: nil,
		MaxKeys: 100,
	})
}

func TestRateLimit_PanicsOnZeroMaxKeys(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for zero MaxKeys")
		}
	}()
	guard.RateLimit(guard.RateLimitConfig{
		Rate:    10,
		Window:  time.Minute,
		KeyFunc: guard.RemoteAddr(),
		MaxKeys: 0,
	})
}
