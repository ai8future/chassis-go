// Package metrics provides OpenTelemetry metrics with cardinality protection.
// Metrics flow out via OTLP push — there is no scrape endpoint.
package metrics

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	chassis "github.com/ai8future/chassis-go/v5"
	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Pre-configured histogram buckets.
var (
	DurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}
	ContentBuckets  = []float64{100, 500, 1000, 5000, 10000, 50000, 100000, 500000, 1000000}
)

// MaxLabelCombinations is the cardinality cap per metric.
const MaxLabelCombinations = 1000

// Recorder holds pre-registered metrics for a service.
type Recorder struct {
	prefix          string
	meter           metric.Meter
	requestsTotal   metric.Float64Counter
	requestDuration metric.Float64Histogram
	contentSize     metric.Float64Histogram

	// cardinality tracking
	mu             sync.RWMutex
	seenCombos     map[string]map[string]struct{} // metric name → set of label combos
	overflowWarned map[string]bool
	logger         *slog.Logger
}

// New creates a Recorder with the given metric prefix and optional logger.
// The prefix is used as the OTel meter name and prepended to metric names.
func New(prefix string, logger *slog.Logger) *Recorder {
	chassis.AssertVersionChecked()
	meter := otelapi.GetMeterProvider().Meter(prefix)

	requestsTotal, err := meter.Float64Counter(
		prefix+"_requests_total",
		metric.WithDescription("Total number of requests."),
	)
	if err != nil && logger != nil {
		logger.Warn("metrics: failed to create requests_total counter", "error", err)
	}

	requestDuration, err := meter.Float64Histogram(
		prefix+"_request_duration_seconds",
		metric.WithDescription("Request duration in seconds."),
		metric.WithExplicitBucketBoundaries(DurationBuckets...),
	)
	if err != nil && logger != nil {
		logger.Warn("metrics: failed to create request_duration histogram", "error", err)
	}

	contentSize, err := meter.Float64Histogram(
		prefix+"_content_size_bytes",
		metric.WithDescription("Content size in bytes."),
		metric.WithExplicitBucketBoundaries(ContentBuckets...),
	)
	if err != nil && logger != nil {
		logger.Warn("metrics: failed to create content_size histogram", "error", err)
	}

	return &Recorder{
		prefix:          prefix,
		meter:           meter,
		requestsTotal:   requestsTotal,
		requestDuration: requestDuration,
		contentSize:     contentSize,
		seenCombos:      make(map[string]map[string]struct{}),
		overflowWarned:  make(map[string]bool),
		logger:          logger,
	}
}

// RecordRequest increments request metrics with cardinality protection.
// The context is used for trace-metric correlation via OTel exemplars.
func (r *Recorder) RecordRequest(ctx context.Context, method, status string, durationMs float64, contentLength float64) {

	// Check cardinality for requests_total (method+status)
	comboKey := method + "\x00" + status
	if r.checkCardinality("requests_total", comboKey) {
		r.requestsTotal.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("method", method),
				attribute.String("status", status),
			),
		)
	}

	// Duration and content use method only
	if r.checkCardinality("request_duration_seconds", method) {
		r.requestDuration.Record(ctx, durationMs/1000,
			metric.WithAttributes(attribute.String("method", method)),
		)
	}

	if r.checkCardinality("content_size_bytes", method) {
		r.contentSize.Record(ctx, contentLength,
			metric.WithAttributes(attribute.String("method", method)),
		)
	}
}

// checkCardinality returns true if the combo is allowed (under limit).
func (r *Recorder) checkCardinality(metricName, combo string) bool {
	// Fast path: check under read lock if the combo is already known.
	r.mu.RLock()
	if combos, exists := r.seenCombos[metricName]; exists {
		if _, seen := combos[combo]; seen {
			r.mu.RUnlock()
			return true
		}
	}
	r.mu.RUnlock()

	// Slow path: acquire write lock and re-check everything.
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seenCombos[metricName] == nil {
		r.seenCombos[metricName] = make(map[string]struct{})
	}
	// Re-check: another goroutine may have added this combo while we waited.
	if _, seen := r.seenCombos[metricName][combo]; seen {
		return true
	}
	if len(r.seenCombos[metricName]) >= MaxLabelCombinations {
		r.warnOnceOverflowLocked(metricName)
		return false
	}
	r.seenCombos[metricName][combo] = struct{}{}
	return true
}

// warnOnceOverflowLocked logs a cardinality overflow warning once per metric.
// Must be called with r.mu held.
func (r *Recorder) warnOnceOverflowLocked(metricName string) {
	if r.overflowWarned[metricName] {
		return
	}
	r.overflowWarned[metricName] = true
	if r.logger != nil {
		r.logger.Warn("metrics cardinality limit reached, dropping new label combinations",
			"metric", metricName,
			"limit", MaxLabelCombinations,
		)
	}
}

// CounterVec wraps an OTel Float64Counter with cardinality protection.
type CounterVec struct {
	inner    metric.Float64Counter
	name     string
	recorder *Recorder
}

// Add increments the counter with the given label pairs (key, value, key, value, ...).
func (c *CounterVec) Add(ctx context.Context, val float64, labelPairs ...string) {
	combo := pairsToCombo(labelPairs)
	if c.recorder.checkCardinality(c.name, combo) {
		c.inner.Add(ctx, val, metric.WithAttributes(pairsToAttributes(labelPairs)...))
	}
}

// HistogramVec wraps an OTel Float64Histogram with cardinality protection.
type HistogramVec struct {
	inner    metric.Float64Histogram
	name     string
	recorder *Recorder
}

// Observe records a value in the histogram with the given label pairs.
func (h *HistogramVec) Observe(ctx context.Context, val float64, labelPairs ...string) {
	combo := pairsToCombo(labelPairs)
	if h.recorder.checkCardinality(h.name, combo) {
		h.inner.Record(ctx, val, metric.WithAttributes(pairsToAttributes(labelPairs)...))
	}
}

// Counter creates and registers a new counter with the given name.
func (r *Recorder) Counter(name string) *CounterVec {
	fullName := r.prefix + "_" + name
	cv, _ := r.meter.Float64Counter(
		fullName,
		metric.WithDescription("Custom counter: "+name),
	)
	return &CounterVec{inner: cv, name: name, recorder: r}
}

// Histogram creates and registers a new histogram with the given name and buckets.
func (r *Recorder) Histogram(name string, buckets []float64) *HistogramVec {
	fullName := r.prefix + "_" + name
	hv, _ := r.meter.Float64Histogram(
		fullName,
		metric.WithDescription("Custom histogram: "+name),
		metric.WithExplicitBucketBoundaries(buckets...),
	)
	return &HistogramVec{inner: hv, name: name, recorder: r}
}

// pairsToCombo joins key=value pairs with \x00 as a unique combo key.
// Including both keys and values prevents collisions where different
// label names happen to share the same values.
func pairsToCombo(pairs []string) string {
	parts := make([]string, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, pairs[i]+"="+pairs[i+1])
	}
	return strings.Join(parts, "\x00")
}

// pairsToAttributes converts key-value pairs to OTel attributes.
func pairsToAttributes(pairs []string) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		attrs = append(attrs, attribute.String(pairs[i], pairs[i+1]))
	}
	return attrs
}
