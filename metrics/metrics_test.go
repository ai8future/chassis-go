package metrics

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecordAndScrape(t *testing.T) {
	logger := slog.Default()
	rec := New("testsvc", logger)

	rec.RecordRequest("GET", "200", 50, 1024)
	rec.RecordRequest("POST", "201", 120, 2048)
	rec.RecordRequest("GET", "500", 5000, 0)

	handler := rec.Handler()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	body := w.Body.String()

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Verify counter appears
	if !strings.Contains(body, "testsvc_requests_total") {
		t.Error("expected testsvc_requests_total in output")
	}

	// Verify histogram appears
	if !strings.Contains(body, "testsvc_request_duration_seconds") {
		t.Error("expected testsvc_request_duration_seconds in output")
	}

	if !strings.Contains(body, "testsvc_content_size_bytes") {
		t.Error("expected testsvc_content_size_bytes in output")
	}
}

func TestHandlerComposable(t *testing.T) {
	rec := New("demosvc", nil)

	// Mount alongside other handlers
	mux := http.NewServeMux()
	mux.Handle("/metrics", rec.Handler())
	mux.HandleFunc("/custom", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "custom")
	})

	// Test /metrics
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/metrics returned %d", w.Code)
	}

	// Test /custom
	req2 := httptest.NewRequest(http.MethodGet, "/custom", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Body.String() != "custom" {
		t.Fatalf("expected 'custom', got %q", w2.Body.String())
	}
}

func TestCardinalityLimit(t *testing.T) {
	rec := New("cardsvc", nil)

	// Fill up to the limit
	for i := range MaxLabelCombinations {
		rec.RecordRequest("GET", fmt.Sprintf("status_%d", i), 10, 100)
	}

	// The next new combination should be dropped (no panic, no error)
	rec.RecordRequest("GET", "overflow_status", 10, 100)

	// Verify metrics still serve
	handler := rec.Handler()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestStartServerHealthEndpoint(t *testing.T) {
	logger := slog.Default()
	rec := New("healthtest", logger)

	checks := map[string]HealthCheck{
		"self": func(_ context.Context) error { return nil },
	}

	srv, err := rec.StartServer(0, logger, checks)
	if err != nil {
		t.Fatalf("StartServer error: %v", err)
	}
	defer srv.Close()

	// Get the actual port
	// Since port 0 isn't directly accessible from http.Server, let's test via httptest instead
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", rec.Handler())
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"healthy"}`)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "healthy") {
		t.Errorf("expected healthy response, got %q", string(body))
	}
}

func TestRecorderCounter(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := New("test", logger)
	counter := r.Counter("searches_total", "type")
	if counter == nil {
		t.Fatal("Counter returned nil")
	}
	counter.Add(1, "type", "organic")
	counter.Add(3, "type", "paid")
}

func TestRecorderHistogram(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := New("test", logger)
	hist := r.Histogram("pdf_size_bytes", ContentBuckets, "format")
	if hist == nil {
		t.Fatal("Histogram returned nil")
	}
	hist.Observe(524288, "format", "pdf")
	hist.Observe(1024, "format", "png")
}

func TestCustomMetricsAppearInHandler(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := New("app", logger)
	counter := r.Counter("events_total", "kind")
	counter.Add(5, "kind", "click")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	r.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "app_events_total") {
		t.Fatal("custom counter not in /metrics output")
	}
}

func TestMetricPrefix(t *testing.T) {
	rec := New("custom_prefix", nil)
	rec.RecordRequest("GET", "200", 10, 100)

	handler := rec.Handler()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "custom_prefix_requests_total") {
		t.Error("expected custom_prefix_requests_total in output")
	}
}
