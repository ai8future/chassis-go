package guard_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ai8future/chassis-go/guard"
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
