// Package errors provides a unified error type with dual HTTP and gRPC status codes.
package errors

import (
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
	Details  map[string]string
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

// WithDetail adds a single detail key-value pair (fluent).
func (e *ServiceError) WithDetail(key, value string) *ServiceError {
	if e.Details == nil {
		e.Details = make(map[string]string)
	}
	e.Details[key] = value
	return e
}

// WithDetails adds multiple detail key-value pairs at once (fluent).
func (e *ServiceError) WithDetails(details map[string]string) *ServiceError {
	if e.Details == nil {
		e.Details = make(map[string]string, len(details))
	}
	for k, v := range details {
		e.Details[k] = v
	}
	return e
}

// WithType sets a custom RFC 9457 type URI, overriding the default.
func (e *ServiceError) WithType(uri string) *ServiceError {
	e.typeURI = uri
	return e
}

// WithCause sets the underlying error cause for Unwrap chaining.
func (e *ServiceError) WithCause(err error) *ServiceError {
	e.cause = err
	return e
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

// TimeoutError creates an error for deadline exceeded (504 / DEADLINE_EXCEEDED).
func TimeoutError(msg string) *ServiceError {
	return &ServiceError{Message: msg, GRPCCode: codes.DeadlineExceeded, HTTPCode: http.StatusGatewayTimeout}
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
	if se, ok := err.(*ServiceError); ok {
		return se
	}
	return InternalError(err.Error()).WithCause(err)
}

// Errorf creates a formatted ServiceError using the given factory.
func Errorf(factory func(string) *ServiceError, format string, args ...any) *ServiceError {
	return factory(fmt.Sprintf(format, args...))
}
