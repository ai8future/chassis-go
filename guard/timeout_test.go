package guard_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ai8future/chassis-go/v5/guard"
)

func TestTimeoutSetsDeadline(t *testing.T) {
	var gotDeadline bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, gotDeadline = r.Context().Deadline()
		w.WriteHeader(http.StatusOK)
	})

	handler := guard.Timeout(5 * time.Second)(inner)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !gotDeadline {
		t.Fatal("expected deadline to be set on context")
	}
}

func TestTimeoutRespectsExistingTighterDeadline(t *testing.T) {
	var gotDeadline time.Time
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotDeadline, _ = r.Context().Deadline()
		w.WriteHeader(http.StatusOK)
	})

	handler := guard.Timeout(30 * time.Second)(inner)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, "GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if time.Until(gotDeadline) > 3*time.Second {
		t.Fatal("expected existing tighter deadline to be preserved")
	}
}

func TestTimeoutReturns504WhenExceeded(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a slow handler that blocks until context is cancelled.
		<-r.Context().Done()
	})

	handler := guard.Timeout(50 * time.Millisecond)(inner)
	req := httptest.NewRequest("GET", "/slow", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected status 504, got %d", rec.Code)
	}
}

func TestTimeoutFlushesOnSuccess(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "yes")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("ok"))
	})

	handler := guard.Timeout(5 * time.Second)(inner)
	req := httptest.NewRequest("GET", "/fast", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", rec.Code)
	}
	if rec.Header().Get("X-Custom") != "yes" {
		t.Fatal("expected X-Custom header to be flushed")
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("expected body 'ok', got %q", rec.Body.String())
	}
}

func TestTimeoutPanicsOnZero(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on Timeout(0)")
		}
	}()
	guard.Timeout(0)
}

func TestTimeoutPanicsOnNegative(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on Timeout(-1)")
		}
	}()
	guard.Timeout(-1)
}
