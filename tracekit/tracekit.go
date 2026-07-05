// Package tracekit propagates trace IDs across events and HTTP calls
// using Go's context mechanism.
package tracekit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"

	chassis "github.com/ai8future/chassis-go/v11"
)

type contextKey struct{}

var (
	canonicalTraceID = regexp.MustCompile(`^tr_[0-9a-f]{32}$`)
	legacyTraceID    = regexp.MustCompile(`^tr_[0-9a-f]{12}$`)
)

// GenerateID creates tr_ + 32 hex random chars (16 bytes / 128-bit entropy).
func GenerateID() string {
	chassis.AssertVersionChecked()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("tracekit: crypto/rand.Read failed: " + err.Error())
	}
	return "tr_" + hex.EncodeToString(b)
}

// isValidTraceID checks that an external trace ID is either the canonical
// Windmill/chassis trace format or the bounded legacy migration format.
func isValidTraceID(id string) bool {
	return canonicalTraceID.MatchString(id) || legacyTraceID.MatchString(id)
}

// NewTrace creates a new trace ID and sets it on context.
func NewTrace(ctx context.Context) context.Context {
	chassis.AssertVersionChecked()
	return context.WithValue(ctx, contextKey{}, GenerateID())
}

// WithTraceID sets a specific trace ID on context.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, contextKey{}, traceID)
}

// TraceID extracts trace ID from context. Returns "" if not set.
func TraceID(ctx context.Context) string {
	v, _ := ctx.Value(contextKey{}).(string)
	return v
}

// Middleware extracts X-Trace-ID from request header (or generates new),
// sets on context, and adds to response header.
func Middleware(next http.Handler) http.Handler {
	chassis.AssertVersionChecked()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get("X-Trace-ID")
		if traceID == "" || !isValidTraceID(traceID) {
			traceID = GenerateID()
		}
		ctx := WithTraceID(r.Context(), traceID)
		w.Header().Set("X-Trace-ID", traceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
