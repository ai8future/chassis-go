// Package httpkit provides standard HTTP middleware and response utilities.
package httpkit

import (
	"net/http"

	"github.com/ai8future/chassis-go/v5/errors"
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
	requestID := RequestIDFrom(r.Context())
	errors.WriteProblem(w, r, err, requestID)
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
	case http.StatusForbidden:
		return errors.ForbiddenError(message)
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
