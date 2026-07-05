// Package errors provides a unified error type with dual HTTP and gRPC status codes.
package errors

import (
	stderrors "errors"
	"fmt"
	"net/http"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ServiceError represents an error with both HTTP and gRPC status codes.
type ServiceError struct {
	Message    string
	GRPCCode   codes.Code
	HTTPCode   int
	Details    map[string]any
	Class      string
	RetryAfter time.Duration
	cause      error
	typeURI    string // custom RFC 9457 type URI (optional)
}

const (
	ClassValidation      = "validation_failed"
	ClassNotFound        = "not_found"
	ClassUnauthorized    = "unauthorized"
	ClassForbidden       = "forbidden"
	ClassTimeout         = "timeout"
	ClassPayloadTooLarge = "payload_too_large"
	ClassRateLimit       = "rate_limited"
	ClassDependency      = "dependency_unavailable"
	ClassInternal        = "internal_error"
)

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

// WithClass returns a copy of the error with a stable machine-readable class.
func (e *ServiceError) WithClass(class string) *ServiceError {
	out := e.clone()
	out.Class = class
	return out
}

// WithRetryAfter returns a copy of the error carrying retry delay guidance.
// The value is rendered as retry_after seconds in problem JSON and as a
// Retry-After response header by WriteProblem when positive.
func (e *ServiceError) WithRetryAfter(after time.Duration) *ServiceError {
	out := e.clone()
	out.RetryAfter = after
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
	return &ServiceError{Message: msg, GRPCCode: codes.InvalidArgument, HTTPCode: http.StatusBadRequest, Class: ClassValidation}
}

// NotFoundError creates an error for missing resources (404 / NOT_FOUND).
func NotFoundError(msg string) *ServiceError {
	return &ServiceError{Message: msg, GRPCCode: codes.NotFound, HTTPCode: http.StatusNotFound, Class: ClassNotFound}
}

// UnauthorizedError creates an error for auth failures (401 / UNAUTHENTICATED).
func UnauthorizedError(msg string) *ServiceError {
	return &ServiceError{Message: msg, GRPCCode: codes.Unauthenticated, HTTPCode: http.StatusUnauthorized, Class: ClassUnauthorized}
}

// ForbiddenError creates an error for permission denials (403 / PERMISSION_DENIED).
func ForbiddenError(msg string) *ServiceError {
	return &ServiceError{Message: msg, GRPCCode: codes.PermissionDenied, HTTPCode: http.StatusForbidden, Class: ClassForbidden}
}

// TimeoutError creates an error for deadline exceeded (504 / DEADLINE_EXCEEDED).
func TimeoutError(msg string) *ServiceError {
	return &ServiceError{Message: msg, GRPCCode: codes.DeadlineExceeded, HTTPCode: http.StatusGatewayTimeout, Class: ClassTimeout}
}

// PayloadTooLargeError creates an error for oversized request bodies (413 / INVALID_ARGUMENT).
func PayloadTooLargeError(msg string) *ServiceError {
	return &ServiceError{Message: msg, GRPCCode: codes.InvalidArgument, HTTPCode: http.StatusRequestEntityTooLarge, Class: ClassPayloadTooLarge}
}

// RateLimitError creates an error for rate limiting (429 / RESOURCE_EXHAUSTED).
func RateLimitError(msg string) *ServiceError {
	return &ServiceError{Message: msg, GRPCCode: codes.ResourceExhausted, HTTPCode: http.StatusTooManyRequests, Class: ClassRateLimit}
}

// DependencyError creates an error for dependency failures (503 / UNAVAILABLE).
func DependencyError(msg string) *ServiceError {
	return &ServiceError{Message: msg, GRPCCode: codes.Unavailable, HTTPCode: http.StatusServiceUnavailable, Class: ClassDependency}
}

// InternalError creates an error for unexpected failures (500 / INTERNAL).
func InternalError(msg string) *ServiceError {
	return &ServiceError{Message: msg, GRPCCode: codes.Internal, HTTPCode: http.StatusInternalServerError, Class: ClassInternal}
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
	return InternalError("an internal error occurred").WithCause(err)
}

// Errorf creates a formatted ServiceError using the given factory.
func Errorf(factory func(string) *ServiceError, format string, args ...any) *ServiceError {
	return factory(fmt.Sprintf(format, args...))
}

// Retryable reports whether err is a ServiceError whose class/status is safe for
// retry orchestration. It uses errors.As, so wrapped ServiceErrors are honored.
func Retryable(err error) bool {
	return IsRetryable(err)
}

// IsRetryable reports whether err represents a retryable ServiceError.
func IsRetryable(err error) bool {
	var se *ServiceError
	if !stderrors.As(err, &se) || se == nil {
		return false
	}
	switch se.Class {
	case ClassTimeout, ClassRateLimit, ClassDependency:
		return true
	}
	switch se.HTTPCode {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return se.HTTPCode >= 500 && se.HTTPCode != http.StatusNotImplemented
}

func (e *ServiceError) code() string {
	if e.Class != "" {
		return e.Class
	}
	switch e.HTTPCode {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return ClassValidation
	case http.StatusNotFound:
		return ClassNotFound
	case http.StatusUnauthorized:
		return ClassUnauthorized
	case http.StatusForbidden:
		return ClassForbidden
	case http.StatusGatewayTimeout, http.StatusRequestTimeout:
		return ClassTimeout
	case http.StatusRequestEntityTooLarge:
		return ClassPayloadTooLarge
	case http.StatusTooManyRequests:
		return ClassRateLimit
	case http.StatusServiceUnavailable, http.StatusBadGateway:
		return ClassDependency
	case http.StatusInternalServerError:
		return ClassInternal
	default:
		if e.HTTPCode >= 500 {
			return ClassInternal
		}
		return "unknown_error"
	}
}
