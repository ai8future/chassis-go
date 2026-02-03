// Package logz provides structured JSON logging with trace ID propagation.
package logz

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// traceIDKey is the unexported context key used to store trace IDs.
type traceIDKey struct{}

// WithTraceID stores a trace ID in the given context.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey{}, traceID)
}

// TraceIDFrom retrieves the trace ID from the context.
// Returns an empty string if no trace ID is present.
func TraceIDFrom(ctx context.Context) string {
	v, ok := ctx.Value(traceIDKey{}).(string)
	if !ok {
		return ""
	}
	return v
}

// New creates a structured JSON logger at the given level.
// Accepted levels are "debug", "info", "warn", "error" (case-insensitive).
// Unrecognized levels default to "info".
func New(level string) *slog.Logger {
	lvl := parseLevel(level)
	jsonHandler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: lvl,
	})
	return slog.New(&traceHandler{inner: jsonHandler, base: jsonHandler})
}

// parseLevel converts a level string to a slog.Level.
// Defaults to slog.LevelInfo for unrecognized values.
func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// traceHandler wraps an slog.Handler and injects a trace_id attribute
// from the context into every log record, when present.
//
// It maintains both the current inner handler (which may have groups/attrs applied)
// and the base handler (without groups) so that trace_id is always emitted at
// the top level of the JSON output.
type traceHandler struct {
	inner  slog.Handler // current handler with groups and attrs applied
	base   slog.Handler // base handler without groups, for top-level trace_id
	groups []string     // accumulated group names for record reconstruction
}

// Enabled delegates to the inner handler.
func (h *traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle extracts a trace ID from the context and, if present, adds it
// as a "trace_id" attribute to the record before delegating to the inner handler.
//
// When groups are active, the record is reconstructed so that trace_id appears
// at the top level while other attributes remain nested within their groups.
func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	traceID := TraceIDFrom(ctx)
	if traceID == "" {
		return h.inner.Handle(ctx, r)
	}

	if len(h.groups) == 0 {
		r.AddAttrs(slog.String("trace_id", traceID))
		return h.inner.Handle(ctx, r)
	}

	// Groups are active. Attrs added via AddAttrs on the record would be placed
	// inside the innermost group by the JSONHandler. To emit trace_id at the top
	// level, we reconstruct the record: collect the record's attrs, nest them
	// inside the group hierarchy as a slog.Group attribute, then emit trace_id
	// and the grouped attrs through the base (ungrouped) handler.
	recordAttrs := make([]slog.Attr, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		recordAttrs = append(recordAttrs, a)
		return true
	})

	// Build nested group structure from inside out.
	var grouped slog.Attr
	grouped = slog.Group(h.groups[len(h.groups)-1], attrsToAny(recordAttrs)...)
	for i := len(h.groups) - 2; i >= 0; i-- {
		grouped = slog.Group(h.groups[i], grouped)
	}

	// Create a new record with trace_id at top level and grouped attrs.
	newRecord := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	newRecord.AddAttrs(slog.String("trace_id", traceID))
	newRecord.AddAttrs(grouped)

	return h.base.Handle(ctx, newRecord)
}

// WithAttrs returns a new traceHandler wrapping the inner handler's WithAttrs result.
func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// If no groups yet, attrs are top-level and should also be applied to base.
	base := h.base
	if len(h.groups) == 0 {
		base = h.base.WithAttrs(attrs)
	}
	return &traceHandler{
		inner:  h.inner.WithAttrs(attrs),
		base:   base,
		groups: h.groups,
	}
}

// WithGroup returns a new traceHandler wrapping the inner handler's WithGroup result.
func (h *traceHandler) WithGroup(name string) slog.Handler {
	newGroups := make([]string, len(h.groups)+1)
	copy(newGroups, h.groups)
	newGroups[len(h.groups)] = name
	return &traceHandler{
		inner:  h.inner.WithGroup(name),
		base:   h.base,
		groups: newGroups,
	}
}

// attrsToAny converts a slice of slog.Attr to a slice of any for slog.Group.
func attrsToAny(attrs []slog.Attr) []any {
	result := make([]any, len(attrs))
	for i, a := range attrs {
		result[i] = a
	}
	return result
}
