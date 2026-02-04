// Package httpkit provides standard HTTP middleware and response utilities.
package httpkit

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/ai8future/chassis-go/errors"
)

// ErrorResponse is the standard JSON error body returned by JSONError.
type ErrorResponse struct {
	Error      string `json:"error"`
	StatusCode int    `json:"status_code"`
	RequestID  string `json:"request_id,omitempty"`
}

// JSONError writes a JSON error response with the given status code and message.
// If a request ID is present in the request context, it is included in the response body.
func JSONError(w http.ResponseWriter, r *http.Request, statusCode int, message string) {
	resp := ErrorResponse{
		Error:      message,
		StatusCode: statusCode,
		RequestID:  RequestIDFrom(r.Context()),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.ErrorContext(r.Context(), "httpkit: failed to encode error response", "error", err)
	}
}

// JSONProblem writes an RFC 9457 Problem Details JSON response from a ServiceError.
func JSONProblem(w http.ResponseWriter, r *http.Request, err *errors.ServiceError) {
	pd := err.ProblemDetail(r)

	if id := RequestIDFrom(r.Context()); id != "" {
		if pd.Extensions == nil {
			pd.Extensions = make(map[string]string)
		}
		pd.Extensions["request_id"] = id
	}

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(err.HTTPCode)

	if encErr := json.NewEncoder(w).Encode(pd); encErr != nil {
		slog.ErrorContext(r.Context(), "httpkit: failed to encode problem detail", "error", encErr)
	}
}
