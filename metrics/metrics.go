// Package metrics provides OpenTelemetry metrics with cardinality protection.
// Metrics flow out via OTLP push — there is no scrape endpoint.
package metrics

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	chassis "github.com/ai8future/chassis-go"
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

	requestsTotal, _ := meter.Float64Counter(
		prefix+"_requests_total",
		metric.WithDescription("Total number of requests."),
	)

	requestDuration, _ := meter.Float64Histogram(
		prefix+"_request_duration_seconds",
		metric.WithDescription("Request duration in seconds."),
		metric.WithExplicitBucketBoundaries(DurationBuckets...),
	)

	contentSize, _ := meter.Float64Histogram(
		prefix+"_content_size_bytes",
		metric.WithDescription("Content size in bytes."),
		metric.WithExplicitBucketBoundaries(ContentBuckets...),
	)

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
	r.mu.RLock()
	combos, exists := r.seenCombos[metricName]
	if exists {
		if _, seen := combos[combo]; seen {
			r.mu.RUnlock()
			return true
		}
		if len(combos) >= MaxLabelCombinations {
			r.mu.RUnlock()
			r.warnOnceOverflow(metricName)
			return false
		}
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seenCombos[metricName] == nil {
		r.seenCombos[metricName] = make(map[string]struct{})
	}
	if len(r.seenCombos[metricName]) >= MaxLabelCombinations {
		return false
	}
	r.seenCombos[metricName][combo] = struct{}{}
	return true
}

func (r *Recorder) warnOnceOverflow(metricName string) {
	r.mu.RLock()
	if r.overflowWarned[metricName] {
		r.mu.RUnlock()
		return
	}
	r.mu.RUnlock()

	r.mu.Lock()
	if r.overflowWarned[metricName] {
		r.mu.Unlock()
		return
	}
	r.overflowWarned[metricName] = true
	r.mu.Unlock()

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
	labels := pairsToValues(labelPairs)
	combo := strings.Join(labels, "\x00")
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
	labels := pairsToValues(labelPairs)
	combo := strings.Join(labels, "\x00")
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

// pairsToValues extracts values from key-value pairs (skipping keys).
func pairsToValues(pairs []string) []string {
	values := make([]string, 0, len(pairs)/2)
	for i := 1; i < len(pairs); i += 2 {
		values = append(values, pairs[i])
	}
	return values
}

// pairsToAttributes converts key-value pairs to OTel attributes.
func pairsToAttributes(pairs []string) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		attrs = append(attrs, attribute.String(pairs[i], pairs[i+1]))
	}
	return attrs
}
