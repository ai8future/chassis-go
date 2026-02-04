package errors

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc/codes"
)

func TestValidationError(t *testing.T) {
	err := ValidationError("bad input")
	if err.Message != "bad input" {
		t.Errorf("Message = %q, want %q", err.Message, "bad input")
	}
	if err.HTTPCode != http.StatusBadRequest {
		t.Errorf("HTTPCode = %d, want %d", err.HTTPCode, http.StatusBadRequest)
	}
	if err.GRPCCode != codes.InvalidArgument {
		t.Errorf("GRPCCode = %v, want %v", err.GRPCCode, codes.InvalidArgument)
	}
}

func TestNotFoundError(t *testing.T) {
	err := NotFoundError("missing")
	if err.HTTPCode != http.StatusNotFound {
		t.Errorf("HTTPCode = %d, want %d", err.HTTPCode, http.StatusNotFound)
	}
	if err.GRPCCode != codes.NotFound {
		t.Errorf("GRPCCode = %v, want %v", err.GRPCCode, codes.NotFound)
	}
}

func TestUnauthorizedError(t *testing.T) {
	err := UnauthorizedError("denied")
	if err.HTTPCode != http.StatusUnauthorized {
		t.Errorf("HTTPCode = %d, want %d", err.HTTPCode, http.StatusUnauthorized)
	}
	if err.GRPCCode != codes.Unauthenticated {
		t.Errorf("GRPCCode = %v, want %v", err.GRPCCode, codes.Unauthenticated)
	}
}

func TestTimeoutError(t *testing.T) {
	err := TimeoutError("slow")
	if err.HTTPCode != http.StatusGatewayTimeout {
		t.Errorf("HTTPCode = %d, want %d", err.HTTPCode, http.StatusGatewayTimeout)
	}
	if err.GRPCCode != codes.DeadlineExceeded {
		t.Errorf("GRPCCode = %v, want %v", err.GRPCCode, codes.DeadlineExceeded)
	}
}

func TestRateLimitError(t *testing.T) {
	err := RateLimitError("throttled")
	if err.HTTPCode != http.StatusTooManyRequests {
		t.Errorf("HTTPCode = %d, want %d", err.HTTPCode, http.StatusTooManyRequests)
	}
	if err.GRPCCode != codes.ResourceExhausted {
		t.Errorf("GRPCCode = %v, want %v", err.GRPCCode, codes.ResourceExhausted)
	}
}

func TestDependencyError(t *testing.T) {
	err := DependencyError("down")
	if err.HTTPCode != http.StatusServiceUnavailable {
		t.Errorf("HTTPCode = %d, want %d", err.HTTPCode, http.StatusServiceUnavailable)
	}
	if err.GRPCCode != codes.Unavailable {
		t.Errorf("GRPCCode = %v, want %v", err.GRPCCode, codes.Unavailable)
	}
}

func TestInternalError(t *testing.T) {
	err := InternalError("crash")
	if err.HTTPCode != http.StatusInternalServerError {
		t.Errorf("HTTPCode = %d, want %d", err.HTTPCode, http.StatusInternalServerError)
	}
	if err.GRPCCode != codes.Internal {
		t.Errorf("GRPCCode = %v, want %v", err.GRPCCode, codes.Internal)
	}
}

func TestErrorInterface(t *testing.T) {
	var err error = ValidationError("test")
	if err.Error() != "test" {
		t.Errorf("Error() = %q, want %q", err.Error(), "test")
	}
}

func TestGRPCStatus(t *testing.T) {
	err := NotFoundError("gone")
	st := err.GRPCStatus()
	if st.Code() != codes.NotFound {
		t.Errorf("status code = %v, want %v", st.Code(), codes.NotFound)
	}
	if st.Message() != "gone" {
		t.Errorf("status message = %q, want %q", st.Message(), "gone")
	}
}

func TestWithDetail(t *testing.T) {
	err := ValidationError("bad").WithDetail("field", "email").WithDetail("reason", "invalid")
	if err.Details["field"] != "email" {
		t.Errorf("Details[field] = %q, want %q", err.Details["field"], "email")
	}
	if err.Details["reason"] != "invalid" {
		t.Errorf("Details[reason] = %q, want %q", err.Details["reason"], "invalid")
	}
}

func TestUnwrap(t *testing.T) {
	cause := context.DeadlineExceeded
	err := TimeoutError("timed out").WithCause(cause)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Error("expected errors.Is to find context.DeadlineExceeded via Unwrap")
	}
}

func TestUnwrapNil(t *testing.T) {
	err := InternalError("no cause")
	if err.Unwrap() != nil {
		t.Errorf("Unwrap() = %v, want nil", err.Unwrap())
	}
}

func TestFromErrorServiceError(t *testing.T) {
	original := ValidationError("original")
	got := FromError(original)
	if got != original {
		t.Error("FromError should return the same ServiceError pointer")
	}
}

func TestFromErrorGenericError(t *testing.T) {
	original := errors.New("something broke")
	got := FromError(original)
	if got.HTTPCode != http.StatusInternalServerError {
		t.Errorf("HTTPCode = %d, want %d", got.HTTPCode, http.StatusInternalServerError)
	}
	if got.Message != "something broke" {
		t.Errorf("Message = %q, want %q", got.Message, "something broke")
	}
	if !errors.Is(got, original) {
		t.Error("FromError should chain original via Unwrap")
	}
}

func TestErrorf(t *testing.T) {
	err := Errorf(ValidationError, "field %q is invalid", "email")
	if err.Message != `field "email" is invalid` {
		t.Errorf("Message = %q", err.Message)
	}
	if err.HTTPCode != http.StatusBadRequest {
		t.Errorf("HTTPCode = %d", err.HTTPCode)
	}
}

func TestProblemDetailFromValidationError(t *testing.T) {
	err := ValidationError("name is required").
		WithDetail("field", "email").
		WithDetail("reason", "invalid format")
	req := httptest.NewRequest("PUT", "/api/users/42", nil)
	pd := err.ProblemDetail(req)

	if pd.Type != "https://chassis.ai8future.com/errors/validation" {
		t.Errorf("Type = %q, want %q", pd.Type, "https://chassis.ai8future.com/errors/validation")
	}
	if pd.Title != "Validation Error" {
		t.Errorf("Title = %q, want %q", pd.Title, "Validation Error")
	}
	if pd.Status != 400 {
		t.Errorf("Status = %d, want 400", pd.Status)
	}
	if pd.Detail != "name is required" {
		t.Errorf("Detail = %q, want %q", pd.Detail, "name is required")
	}
	if pd.Instance != "/api/users/42" {
		t.Errorf("Instance = %q, want %q", pd.Instance, "/api/users/42")
	}
	if pd.Extensions["field"] != "email" {
		t.Errorf("Extensions[field] = %q, want %q", pd.Extensions["field"], "email")
	}
	if pd.Extensions["reason"] != "invalid format" {
		t.Errorf("Extensions[reason] = %q, want %q", pd.Extensions["reason"], "invalid format")
	}
}

func TestProblemDetailJSON(t *testing.T) {
	err := NotFoundError("user not found")
	req := httptest.NewRequest("GET", "/api/users/99", nil)
	pd := err.ProblemDetail(req)
	data, marshalErr := json.Marshal(pd)
	if marshalErr != nil {
		t.Fatalf("json.Marshal failed: %v", marshalErr)
	}
	var got map[string]any
	if unmarshalErr := json.Unmarshal(data, &got); unmarshalErr != nil {
		t.Fatalf("json.Unmarshal failed: %v", unmarshalErr)
	}
	if got["type"] != "https://chassis.ai8future.com/errors/not-found" {
		t.Errorf("type = %v, want %q", got["type"], "https://chassis.ai8future.com/errors/not-found")
	}
	if got["status"].(float64) != 404 {
		t.Errorf("status = %v, want 404", got["status"])
	}
	if got["title"] != "Not Found" {
		t.Errorf("title = %v, want %q", got["title"], "Not Found")
	}
	if got["detail"] != "user not found" {
		t.Errorf("detail = %v, want %q", got["detail"], "user not found")
	}
	if got["instance"] != "/api/users/99" {
		t.Errorf("instance = %v, want %q", got["instance"], "/api/users/99")
	}
}

func TestProblemDetailWithCustomType(t *testing.T) {
	customURI := "https://example.com/errors/custom"
	err := ValidationError("custom").WithType(customURI)
	req := httptest.NewRequest("GET", "/test", nil)
	pd := err.ProblemDetail(req)
	if pd.Type != customURI {
		t.Errorf("Type = %q, want %q", pd.Type, customURI)
	}
}

func TestProblemDetailUnknownHTTPCode(t *testing.T) {
	err := &ServiceError{Message: "teapot", HTTPCode: 418, GRPCCode: 0}
	req := httptest.NewRequest("GET", "/brew", nil)
	pd := err.ProblemDetail(req)
	if pd.Type != "https://chassis.ai8future.com/errors/unknown" {
		t.Errorf("Type = %q, want unknown type URI", pd.Type)
	}
	if pd.Title != "I'm a teapot" {
		t.Errorf("Title = %q, want %q", pd.Title, "I'm a teapot")
	}
}

func TestProblemDetailNoExtensions(t *testing.T) {
	err := InternalError("boom")
	req := httptest.NewRequest("GET", "/", nil)
	pd := err.ProblemDetail(req)
	if pd.Extensions != nil {
		t.Errorf("Extensions = %v, want nil", pd.Extensions)
	}
	// Verify omitempty works in JSON
	data, _ := json.Marshal(pd)
	var got map[string]any
	json.Unmarshal(data, &got)
	if _, exists := got["extensions"]; exists {
		t.Error("extensions field should be omitted from JSON when empty")
	}
}
