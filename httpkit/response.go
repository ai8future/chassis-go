// Package httpkit provides standard HTTP middleware and response utilities.
package httpkit

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/ai8future/chassis-go/errors"
)

// JSONError writes an RFC 9457 Problem Details JSON response for the given
// status code and message. It constructs a ServiceError internally to derive
// the type URI and title. For richer error responses, use JSONProblem with
// an existing ServiceError directly.
func JSONError(w http.ResponseWriter, r *http.Request, statusCode int, message string) {
	svcErr := errorForStatus(statusCode, message)
	JSONProblem(w, r, svcErr)
}

// JSONProblem writes an RFC 9457 Problem Details JSON response from a ServiceError.
func JSONProblem(w http.ResponseWriter, r *http.Request, err *errors.ServiceError) {
	if err == nil {
		err = errors.InternalError("unknown error")
	}
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

// errorForStatus maps an HTTP status code to an appropriate ServiceError factory.
func errorForStatus(code int, message string) *errors.ServiceError {
	switch code {
	case http.StatusBadRequest:
		return errors.ValidationError(message)
	case http.StatusNotFound:
		return errors.NotFoundError(message)
	case http.StatusUnauthorized:
		return errors.UnauthorizedError(message)
	case http.StatusGatewayTimeout:
		return errors.TimeoutError(message)
	case http.StatusRequestEntityTooLarge:
		return errors.PayloadTooLargeError(message)
	case http.StatusTooManyRequests:
		return errors.RateLimitError(message)
	case http.StatusServiceUnavailable:
		return errors.DependencyError(message)
	default:
		return errors.InternalError(message)
	}
}
