// Package metrics provides Prometheus metrics with cardinality protection.
// It exposes both a composable HTTP handler (NewHandler) and a convenience
// server (StartServer) that also serves /health.
package metrics

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
	requestsTotal   *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	contentSize     *prometheus.HistogramVec
	registry        *prometheus.Registry

	// cardinality tracking
	mu              sync.RWMutex
	seenCombos      map[string]map[string]struct{} // metric name → set of label combos
	overflowWarned  map[string]bool
	logger          *slog.Logger
}

// New creates a Recorder with the given metric prefix and optional logger.
// The prefix is prepended to all metric names (e.g., "mysvc" → "mysvc_requests_total").
func New(prefix string, logger *slog.Logger) *Recorder {
	reg := prometheus.NewRegistry()

	requestsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: prefix + "_requests_total",
			Help: "Total number of requests.",
		},
		[]string{"method", "status"},
	)

	requestDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    prefix + "_request_duration_seconds",
			Help:    "Request duration in seconds.",
			Buckets: DurationBuckets,
		},
		[]string{"method"},
	)

	contentSize := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    prefix + "_content_size_bytes",
			Help:    "Content size in bytes.",
			Buckets: ContentBuckets,
		},
		[]string{"method"},
	)

	reg.MustRegister(requestsTotal, requestDuration, contentSize)

	return &Recorder{
		prefix:          prefix,
		requestsTotal:   requestsTotal,
		requestDuration: requestDuration,
		contentSize:     contentSize,
		registry:        reg,
		seenCombos:      make(map[string]map[string]struct{}),
		overflowWarned:  make(map[string]bool),
		logger:          logger,
	}
}

// RecordRequest increments request metrics with cardinality protection.
func (r *Recorder) RecordRequest(method, status string, durationMs float64, contentLength float64) {
	// Check cardinality for requests_total (method+status)
	comboKey := method + "\x00" + status
	if r.checkCardinality("requests_total", comboKey) {
		r.requestsTotal.WithLabelValues(method, status).Inc()
	}

	// Duration and content use method only
	if r.checkCardinality("request_duration_seconds", method) {
		r.requestDuration.WithLabelValues(method).Observe(durationMs / 1000)
	}

	if r.checkCardinality("content_size_bytes", method) {
		r.contentSize.WithLabelValues(method).Observe(contentLength)
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

var overflowWarnedGlobal atomic.Bool

func (r *Recorder) warnOnceOverflow(metricName string) {
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

// CounterVec wraps a prometheus.CounterVec with cardinality protection.
type CounterVec struct {
	inner    *prometheus.CounterVec
	name     string
	recorder *Recorder
}

// Add increments the counter with the given label pairs (key, value, key, value, ...).
func (c *CounterVec) Add(val float64, labelPairs ...string) {
	labels := pairsToValues(labelPairs)
	combo := strings.Join(labels, "\x00")
	if c.recorder.checkCardinality(c.name, combo) {
		c.inner.WithLabelValues(labels...).Add(val)
	}
}

// HistogramVec wraps a prometheus.HistogramVec with cardinality protection.
type HistogramVec struct {
	inner    *prometheus.HistogramVec
	name     string
	recorder *Recorder
}

// Observe records a value in the histogram with the given label pairs.
func (h *HistogramVec) Observe(val float64, labelPairs ...string) {
	labels := pairsToValues(labelPairs)
	combo := strings.Join(labels, "\x00")
	if h.recorder.checkCardinality(h.name, combo) {
		h.inner.WithLabelValues(labels...).Observe(val)
	}
}

// Counter creates and registers a new counter with the given name and label names.
func (r *Recorder) Counter(name string, labelNames ...string) *CounterVec {
	fullName := r.prefix + "_" + name
	cv := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: fullName,
		Help: "Custom counter: " + name,
	}, labelNames)
	r.registry.MustRegister(cv)
	return &CounterVec{inner: cv, name: name, recorder: r}
}

// Histogram creates and registers a new histogram with the given name, buckets, and label names.
func (r *Recorder) Histogram(name string, buckets []float64, labelNames ...string) *HistogramVec {
	fullName := r.prefix + "_" + name
	hv := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    fullName,
		Help:    "Custom histogram: " + name,
		Buckets: buckets,
	}, labelNames)
	r.registry.MustRegister(hv)
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

// Handler returns an http.Handler that serves GET /metrics in Prometheus text format.
func (r *Recorder) Handler() http.Handler {
	return promhttp.HandlerFor(r.registry, promhttp.HandlerOpts{})
}

// HealthCheck is a function that returns an error if unhealthy.
type HealthCheck func(ctx context.Context) error

// StartServer starts an HTTP server serving /metrics and /health on the given port.
// Returns a shutdown function.
func (r *Recorder) StartServer(port int, logger *slog.Logger, checks map[string]HealthCheck) (*http.Server, error) {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", r.Handler())
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, req *http.Request) {
		healthy := true
		for name, check := range checks {
			if err := check(req.Context()); err != nil {
				healthy = false
				if logger != nil {
					logger.Warn("health check failed", "check", name, "error", err)
				}
			}
		}
		if healthy {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"status":"healthy"}`)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"status":"unhealthy"}`)
		}
	})

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			if logger != nil {
				logger.Error("metrics server error", "error", err)
			}
		}
	}()

	return srv, nil
}
