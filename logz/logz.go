// Package logz provides structured JSON logging with trace ID propagation.
package logz

import (
	"context"
	"log/slog"
	"os"
	"strings"

	chassis "github.com/ai8future/chassis-go/v10"
	"go.opentelemetry.io/otel/trace"
)

// New creates a structured JSON logger at the given level.
// Accepted levels are "debug", "info", "warn", "error" (case-insensitive).
// Unrecognized levels default to "info".
func New(level string) *slog.Logger {
	chassis.AssertVersionChecked()
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
	inner      slog.Handler // current handler with groups and attrs applied
	base       slog.Handler // base handler without groups, for top-level trace_id
	groups     []string     // accumulated group names for record reconstruction
	groupAttrs [][]slog.Attr // attrs added via WithAttrs while inside groups, per group depth
}

// Enabled delegates to the inner handler.
func (h *traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle extracts trace information from the OTel span context and, if present,
// adds "trace_id" and "span_id" attributes to the record before delegating.
//
// When groups are active, the record is reconstructed so that trace_id and
// span_id appear at the top level while other attributes remain nested.
func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	var traceID, spanID string

	sc := trace.SpanContextFromContext(ctx)
	if sc.IsValid() {
		traceID = sc.TraceID().String()
		spanID = sc.SpanID().String()
	}

	if traceID == "" {
		return h.inner.Handle(ctx, r)
	}

	if len(h.groups) == 0 {
		r.AddAttrs(slog.String("trace_id", traceID))
		r.AddAttrs(slog.String("span_id", spanID))
		return h.inner.Handle(ctx, r)
	}

	// Groups are active — reconstruct record with trace_id/span_id at top level.
	recordAttrs := make([]slog.Attr, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		recordAttrs = append(recordAttrs, a)
		return true
	})

	// Build the innermost group: merge WithAttrs for this group depth + record attrs.
	lastIdx := len(h.groups) - 1
	var innerItems []any
	if lastIdx < len(h.groupAttrs) {
		innerItems = attrsToAny(h.groupAttrs[lastIdx])
	}
	innerItems = append(innerItems, attrsToAny(recordAttrs)...)

	var grouped slog.Attr
	grouped = slog.Group(h.groups[lastIdx], innerItems...)
	for i := lastIdx - 1; i >= 0; i-- {
		var items []any
		if i < len(h.groupAttrs) {
			items = attrsToAny(h.groupAttrs[i])
		}
		items = append(items, grouped)
		grouped = slog.Group(h.groups[i], items...)
	}

	newRecord := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	newRecord.AddAttrs(slog.String("trace_id", traceID))
	newRecord.AddAttrs(slog.String("span_id", spanID))
	newRecord.AddAttrs(grouped)

	return h.base.Handle(ctx, newRecord)
}

// WithAttrs returns a new traceHandler wrapping the inner handler's WithAttrs result.
func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// If no groups yet, attrs are top-level and should also be applied to base.
	base := h.base
	groupAttrs := h.groupAttrs
	if len(h.groups) == 0 {
		base = h.base.WithAttrs(attrs)
	} else {
		// Track attrs added within groups so they can be included in the
		// reconstructed record when trace context is present.
		groupAttrs = make([][]slog.Attr, len(h.groupAttrs))
		copy(groupAttrs, h.groupAttrs)
		// Append to the current (deepest) group's attrs.
		idx := len(h.groups) - 1
		for idx >= len(groupAttrs) {
			groupAttrs = append(groupAttrs, nil)
		}
		merged := make([]slog.Attr, len(groupAttrs[idx]), len(groupAttrs[idx])+len(attrs))
		copy(merged, groupAttrs[idx])
		merged = append(merged, attrs...)
		groupAttrs[idx] = merged
	}
	return &traceHandler{
		inner:      h.inner.WithAttrs(attrs),
		base:       base,
		groups:     h.groups,
		groupAttrs: groupAttrs,
	}
}

// WithGroup returns a new traceHandler wrapping the inner handler's WithGroup result.
func (h *traceHandler) WithGroup(name string) slog.Handler {
	newGroups := make([]string, len(h.groups)+1)
	copy(newGroups, h.groups)
	newGroups[len(h.groups)] = name
	// Extend groupAttrs to have a slot for the new group depth.
	newGroupAttrs := make([][]slog.Attr, len(newGroups))
	copy(newGroupAttrs, h.groupAttrs)
	return &traceHandler{
		inner:      h.inner.WithGroup(name),
		base:       h.base,
		groups:     newGroups,
		groupAttrs: newGroupAttrs,
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
