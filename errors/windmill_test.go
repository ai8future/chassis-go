package errors

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWindmillProblemRetryableContract(t *testing.T) {
	fixturePath := filepath.Join("..", "testdata", "windmill", "contracts", "fixtures", "problem.retryable.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read pinned fixture: %v", err)
	}
	var fixture map[string]any
	if err := json.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/jobs", nil)
	svcErr := DependencyError("upstream timeout").
		WithClass("dependency_unavailable").
		WithType("https://ai8.dev/problems/dependency_unavailable").
		WithRetryAfter(1500*time.Millisecond).
		WithDetail("trace_id", fixture["trace_id"])

	WriteProblem(rec, req, svcErr, "")

	if got := rec.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After header = %q, want 2", got)
	}
	var got map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	for _, key := range []string{"type", "status", "code", "retryable", "retry_after", "trace_id"} {
		if got[key] != fixture[key] {
			t.Fatalf("%s = %#v, want fixture %#v", key, got[key], fixture[key])
		}
	}
}

func TestRetryableHelpers(t *testing.T) {
	if !IsRetryable(DependencyError("down")) {
		t.Fatal("dependency errors should be retryable")
	}
	if Retryable(ValidationError("bad")) {
		t.Fatal("validation errors should be terminal")
	}
}

func TestFactoryClasses(t *testing.T) {
	cases := []struct {
		name string
		err  *ServiceError
		want string
	}{
		{"validation", ValidationError("bad"), ClassValidation},
		{"not_found", NotFoundError("missing"), ClassNotFound},
		{"unauthorized", UnauthorizedError("denied"), ClassUnauthorized},
		{"forbidden", ForbiddenError("blocked"), ClassForbidden},
		{"timeout", TimeoutError("slow"), ClassTimeout},
		{"payload", PayloadTooLargeError("large"), ClassPayloadTooLarge},
		{"rate_limit", RateLimitError("throttle"), ClassRateLimit},
		{"dependency", DependencyError("down"), ClassDependency},
		{"internal", InternalError("boom"), ClassInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err.Class != tc.want || tc.err.code() != tc.want {
				t.Fatalf("class/code = %q/%q, want %q", tc.err.Class, tc.err.code(), tc.want)
			}
		})
	}
}
