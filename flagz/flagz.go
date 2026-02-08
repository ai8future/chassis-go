// Package flagz provides feature flags with percentage rollouts and
// pluggable sources. It integrates with OpenTelemetry for flag evaluation
// tracing via span events.
package flagz

import (
	"context"
	"hash/fnv"

	chassis "github.com/ai8future/chassis-go/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Source provides flag values by name. Implementations may read from
// environment variables, JSON files, remote services, or in-memory maps.
type Source interface {
	Lookup(name string) (value string, ok bool)
}

// Context provides evaluation context for percentage rollouts.
type Context struct {
	UserID     string
	Percent    int               // 0-100 — percentage gate
	Attributes map[string]string // additional context for future targeting
}

// Flags wraps a Source and provides typed flag evaluation methods.
type Flags struct {
	source Source
}

// New creates a Flags instance backed by the given source.
// Panics if source is nil.
func New(source Source) *Flags {
	chassis.AssertVersionChecked()
	if source == nil {
		panic("flagz: source must not be nil")
	}
	return &Flags{source: source}
}

// Enabled returns true if the flag value is "true".
func (f *Flags) Enabled(name string) bool {
	value, ok := f.source.Lookup(name)
	return ok && value == "true"
}

// EnabledFor returns true if the flag is enabled for the given context.
// Uses consistent hashing of name+UserID mod 100 for percentage rollouts.
// If fctx.Percent is 0, the flag is always disabled.
// If fctx.Percent is 100, the flag is always enabled (assuming the source
// returns "true").
func (f *Flags) EnabledFor(ctx context.Context, name string, fctx Context) bool {
	value, ok := f.source.Lookup(name)
	if !ok || value != "true" {
		f.addSpanEvent(ctx, name, false, fctx)
		return false
	}

	if fctx.Percent <= 0 {
		f.addSpanEvent(ctx, name, false, fctx)
		return false
	}
	if fctx.Percent >= 100 {
		f.addSpanEvent(ctx, name, true, fctx)
		return true
	}

	bucket := consistentBucket(name, fctx.UserID)
	enabled := bucket < fctx.Percent
	f.addSpanEvent(ctx, name, enabled, fctx)
	return enabled
}

// Variant returns the raw flag value, or defaultVal if the flag is not set.
func (f *Flags) Variant(name string, defaultVal string) string {
	value, ok := f.source.Lookup(name)
	if !ok {
		return defaultVal
	}
	return value
}

// consistentBucket returns a deterministic bucket (0-99) for a name+userID pair.
func consistentBucket(name, userID string) int {
	h := fnv.New32a()
	h.Write([]byte(name))
	h.Write([]byte{0}) // null-byte separator prevents "ab"+"c" == "a"+"bc" collisions
	h.Write([]byte(userID))
	return int(h.Sum32() % 100)
}

// addSpanEvent records a flag evaluation as an OTel span event.
// Graceful no-op when OTel is not initialized.
func (f *Flags) addSpanEvent(ctx context.Context, name string, enabled bool, fctx Context) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("flag.name", name),
		attribute.Bool("flag.enabled", enabled),
	}
	if fctx.UserID != "" {
		attrs = append(attrs, attribute.String("flag.user_id", fctx.UserID))
	}
	if fctx.Percent > 0 {
		attrs = append(attrs, attribute.Int("flag.percent", fctx.Percent))
	}
	span.AddEvent("flag.evaluation", trace.WithAttributes(attrs...))
}
