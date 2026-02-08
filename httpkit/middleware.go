package httpkit

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync/atomic"
	"time"

	chassis "github.com/ai8future/chassis-go/v5"
)

// idCounter is a fallback counter used when crypto/rand fails.
var idCounter uint64

// requestIDKey is the unexported context key used to store request IDs.
type requestIDKey struct{}

// RequestIDFrom retrieves the request ID from the context.
// Returns an empty string if no request ID is present.
func RequestIDFrom(ctx context.Context) string {
	v, ok := ctx.Value(requestIDKey{}).(string)
	if !ok {
		return ""
	}
	return v
}

// generateID produces a UUID-v4-like random identifier using crypto/rand.
// Falls back to a timestamp+counter scheme if crypto/rand is unavailable.
func generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x-%d", time.Now().UnixNano(), atomic.AddUint64(&idCounter, 1))
	}
	// Set version (4) and variant (RFC 4122) bits.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// RequestID is middleware that generates a unique request ID, stores it in the
// request context, and sets it as the X-Request-ID response header.
func RequestID(next http.Handler) http.Handler {
	chassis.AssertVersionChecked()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := generateID()
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// responseWriter wraps http.ResponseWriter to capture the status code
// and track whether headers have been sent.
type responseWriter struct {
	http.ResponseWriter
	statusCode    int
	headerWritten bool
}

// WriteHeader captures the status code and delegates to the underlying writer.
// Only the first call takes effect; subsequent calls are suppressed to prevent
// "superfluous WriteHeader" warnings from net/http.
func (rw *responseWriter) WriteHeader(code int) {
	if rw.headerWritten {
		return
	}
	rw.statusCode = code
	rw.headerWritten = true
	rw.ResponseWriter.WriteHeader(code)
}

// Unwrap returns the underlying http.ResponseWriter so that
// http.NewResponseController can access optional interfaces like
// http.Flusher and http.Hijacker.
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// Logging returns middleware that logs each request's method, path, status code,
// and duration using the provided structured logger. If a request ID is present
// in the context, it is included in the log entry.
func Logging(logger *slog.Logger) func(http.Handler) http.Handler {
	chassis.AssertVersionChecked()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			next.ServeHTTP(rw, r)

			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rw.statusCode),
				slog.Duration("duration", time.Since(start)),
			}
			if id := RequestIDFrom(r.Context()); id != "" {
				attrs = append(attrs, slog.String("request_id", id))
			}

			logger.LogAttrs(r.Context(), slog.LevelInfo, "request completed", attrs...)
		})
	}
}

// Recovery returns middleware that catches panics in downstream handlers,
// logs them at Error level with stack information, and returns a 500 JSON error.
// If the handler has already started writing the response, the error body is
// skipped to avoid corrupting the response.
func Recovery(logger *slog.Logger) func(http.Handler) http.Handler {
	chassis.AssertVersionChecked()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Ensure we have a responseWriter to track headerWritten state,
			// whether or not Logging/Tracing middleware has already wrapped w.
			rw, ok := w.(*responseWriter)
			if !ok {
				rw = &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
				w = rw
			}
			defer func() {
				if err := recover(); err != nil {
					stack := debug.Stack()
					logger.Error("panic recovered",
						"error", fmt.Sprint(err),
						"stack", string(stack),
					)
					if rw.headerWritten {
						return // headers already sent — cannot write error response
					}
					JSONError(w, r, http.StatusInternalServerError, "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
