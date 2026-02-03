package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// All tests
// ---------------------------------------------------------------------------

func TestAll_AllHealthy(t *testing.T) {
	checks := map[string]Check{
		"db":    func(ctx context.Context) error { return nil },
		"cache": func(ctx context.Context) error { return nil },
	}

	results, err := All(checks)(context.Background())
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	for _, r := range results {
		if !r.Healthy {
			t.Errorf("check %q should be healthy", r.Name)
		}
		if r.Error != "" {
			t.Errorf("check %q should have empty error, got %q", r.Name, r.Error)
		}
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestAll_OneFailure(t *testing.T) {
	dbErr := errors.New("connection refused")
	checks := map[string]Check{
		"db":    func(ctx context.Context) error { return dbErr },
		"cache": func(ctx context.Context) error { return nil },
	}

	results, err := All(checks)(context.Background())
	if err == nil {
		t.Fatal("expected non-nil error")
	}

	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("combined error should contain 'connection refused', got %q", err.Error())
	}

	var foundDB, foundCache bool
	for _, r := range results {
		switch r.Name {
		case "db":
			foundDB = true
			if r.Healthy {
				t.Error("db check should be unhealthy")
			}
			if r.Error != "connection refused" {
				t.Errorf("db error = %q, want %q", r.Error, "connection refused")
			}
		case "cache":
			foundCache = true
			if !r.Healthy {
				t.Error("cache check should be healthy")
			}
		}
	}
	if !foundDB || !foundCache {
		t.Fatal("missing expected result entries")
	}
}

func TestAll_MultipleFailures(t *testing.T) {
	checks := map[string]Check{
		"db":    func(ctx context.Context) error { return errors.New("db down") },
		"cache": func(ctx context.Context) error { return errors.New("cache timeout") },
		"queue": func(ctx context.Context) error { return nil },
	}

	results, err := All(checks)(context.Background())
	if err == nil {
		t.Fatal("expected combined error")
	}

	// errors.Join produces a string with newline-separated messages.
	msg := err.Error()
	if !strings.Contains(msg, "db down") {
		t.Errorf("combined error missing 'db down': %q", msg)
	}
	if !strings.Contains(msg, "cache timeout") {
		t.Errorf("combined error missing 'cache timeout': %q", msg)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
}

func TestAll_ParallelExecution(t *testing.T) {
	const checkDuration = 50 * time.Millisecond

	var started atomic.Int32

	slowCheck := func(ctx context.Context) error {
		started.Add(1)
		time.Sleep(checkDuration)
		return nil
	}

	checks := map[string]Check{
		"a": slowCheck,
		"b": slowCheck,
		"c": slowCheck,
	}

	start := time.Now()
	results, err := All(checks)(context.Background())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// If run sequentially this would take >= 3 * checkDuration (150ms).
	// Parallel execution should complete in roughly 1 * checkDuration.
	limit := 2 * checkDuration
	if elapsed > limit {
		t.Errorf("checks appear sequential: elapsed %v exceeds %v", elapsed, limit)
	}
}

func TestAll_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	var sawCancelled atomic.Bool

	checks := map[string]Check{
		"slow": func(ctx context.Context) error {
			if ctx.Err() != nil {
				sawCancelled.Store(true)
				return ctx.Err()
			}
			// Simulate work that respects the context.
			select {
			case <-ctx.Done():
				sawCancelled.Store(true)
				return ctx.Err()
			case <-time.After(5 * time.Second):
				return nil
			}
		},
	}

	results, _ := All(checks)(ctx)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	if !sawCancelled.Load() {
		t.Error("check did not observe context cancellation")
	}
}

// ---------------------------------------------------------------------------
// Handler tests
// ---------------------------------------------------------------------------

func TestHandler_Healthy(t *testing.T) {
	checks := map[string]Check{
		"db": func(ctx context.Context) error { return nil },
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	Handler(checks).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body response
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if body.Status != "healthy" {
		t.Errorf("status = %q, want %q", body.Status, "healthy")
	}
	if len(body.Checks) != 1 {
		t.Fatalf("expected 1 check result, got %d", len(body.Checks))
	}
	if !body.Checks[0].Healthy {
		t.Error("check should be healthy")
	}
}

func TestHandler_Unhealthy(t *testing.T) {
	checks := map[string]Check{
		"db":    func(ctx context.Context) error { return errors.New("gone") },
		"cache": func(ctx context.Context) error { return nil },
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	Handler(checks).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}

	var body response
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if body.Status != "unhealthy" {
		t.Errorf("status = %q, want %q", body.Status, "unhealthy")
	}
	if len(body.Checks) != 2 {
		t.Fatalf("expected 2 check results, got %d", len(body.Checks))
	}

	var foundFailure bool
	for _, c := range body.Checks {
		if c.Name == "db" && !c.Healthy && c.Error == "gone" {
			foundFailure = true
		}
	}
	if !foundFailure {
		t.Error("expected to find unhealthy db check with error 'gone'")
	}
}
