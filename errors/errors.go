// Package errors provides a unified error type with dual HTTP and gRPC status codes.
package errors

import (
	stderrors "errors"
	"fmt"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ServiceError represents an error with both HTTP and gRPC status codes.
type ServiceError struct {
	Message  string
	GRPCCode codes.Code
	HTTPCode int
	Details  map[string]any
	cause    error
	typeURI  string // custom RFC 9457 type URI (optional)
}

// Error implements the error interface.
func (e *ServiceError) Error() string {
	return e.Message
}

// Unwrap returns the underlying cause, supporting errors.Is/As chains.
func (e *ServiceError) Unwrap() error {
	return e.cause
}

// GRPCStatus returns a gRPC status for this error.
func (e *ServiceError) GRPCStatus() *status.Status {
	return status.New(e.GRPCCode, e.Message)
}

// WithDetail returns a copy of the error with the given detail key-value pair added.
// The receiver is not modified, making it safe to decorate errors across goroutines.
func (e *ServiceError) WithDetail(key string, value any) *ServiceError {
	out := e.clone()
	if out.Details == nil {
		out.Details = make(map[string]any)
	}
	out.Details[key] = value
	return out
}

// WithDetails returns a copy of the error with the given detail key-value pairs added.
// The receiver is not modified, making it safe to decorate errors across goroutines.
func (e *ServiceError) WithDetails(details map[string]any) *ServiceError {
	out := e.clone()
	if out.Details == nil {
		out.Details = make(map[string]any, len(details))
	}
	for k, v := range details {
		out.Details[k] = v
	}
	return out
}

// WithType returns a copy of the error with a custom RFC 9457 type URI, overriding the default.
func (e *ServiceError) WithType(uri string) *ServiceError {
	out := e.clone()
	out.typeURI = uri
	return out
}

// WithCause returns a copy of the error with the underlying error cause set for Unwrap chaining.
func (e *ServiceError) WithCause(err error) *ServiceError {
	out := e.clone()
	out.cause = err
	return out
}

// clone returns a shallow copy of the ServiceError with a deep-copied Details map.
func (e *ServiceError) clone() *ServiceError {
	out := *e
	if e.Details != nil {
		out.Details = make(map[string]any, len(e.Details))
		for k, v := range e.Details {
			out.Details[k] = v
		}
	}
	return &out
}

// --- Factory constructors ---

// ValidationError creates an error for invalid input (400 / INVALID_ARGUMENT).
func ValidationError(msg string) *ServiceError {
	return &ServiceError{Message: msg, GRPCCode: codes.InvalidArgument, HTTPCode: http.StatusBadRequest}
}

// NotFoundError creates an error for missing resources (404 / NOT_FOUND).
func NotFoundError(msg string) *ServiceError {
	return &ServiceError{Message: msg, GRPCCode: codes.NotFound, HTTPCode: http.StatusNotFound}
}

// UnauthorizedError creates an error for auth failures (401 / UNAUTHENTICATED).
func UnauthorizedError(msg string) *ServiceError {
	return &ServiceError{Message: msg, GRPCCode: codes.Unauthenticated, HTTPCode: http.StatusUnauthorized}
}

// ForbiddenError creates an error for permission denials (403 / PERMISSION_DENIED).
func ForbiddenError(msg string) *ServiceError {
	return &ServiceError{Message: msg, GRPCCode: codes.PermissionDenied, HTTPCode: http.StatusForbidden}
}

// TimeoutError creates an error for deadline exceeded (504 / DEADLINE_EXCEEDED).
func TimeoutError(msg string) *ServiceError {
	return &ServiceError{Message: msg, GRPCCode: codes.DeadlineExceeded, HTTPCode: http.StatusGatewayTimeout}
}

// PayloadTooLargeError creates an error for oversized request bodies (413 / INVALID_ARGUMENT).
func PayloadTooLargeError(msg string) *ServiceError {
	return &ServiceError{Message: msg, GRPCCode: codes.InvalidArgument, HTTPCode: http.StatusRequestEntityTooLarge}
}

// RateLimitError creates an error for rate limiting (429 / RESOURCE_EXHAUSTED).
func RateLimitError(msg string) *ServiceError {
	return &ServiceError{Message: msg, GRPCCode: codes.ResourceExhausted, HTTPCode: http.StatusTooManyRequests}
}

// DependencyError creates an error for dependency failures (503 / UNAVAILABLE).
func DependencyError(msg string) *ServiceError {
	return &ServiceError{Message: msg, GRPCCode: codes.Unavailable, HTTPCode: http.StatusServiceUnavailable}
}

// InternalError creates an error for unexpected failures (500 / INTERNAL).
func InternalError(msg string) *ServiceError {
	return &ServiceError{Message: msg, GRPCCode: codes.Internal, HTTPCode: http.StatusInternalServerError}
}

// --- Helpers ---

// FromError converts any error to a ServiceError. If the error is already
// a ServiceError it is returned as-is; otherwise it is wrapped as internal.
func FromError(err error) *ServiceError {
	if err == nil {
		return nil
	}
	var se *ServiceError
	if stderrors.As(err, &se) {
		return se
	}
	return InternalError(err.Error()).WithCause(err)
}

// Errorf creates a formatted ServiceError using the given factory.
func Errorf(factory func(string) *ServiceError, format string, args ...any) *ServiceError {
	return factory(fmt.Sprintf(format, args...))
}
