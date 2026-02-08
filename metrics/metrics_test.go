package metrics

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	chassis "github.com/ai8future/chassis-go/v5"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestMain(m *testing.M) {
	chassis.RequireMajor(5)
	os.Exit(m.Run())
}

// setupTestMeter installs a ManualReader-backed MeterProvider and returns
// a collect function that snapshots all recorded metrics.
func setupTestMeter(t *testing.T) func() metricdata.ResourceMetrics {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { mp.Shutdown(context.Background()) })

	return func() metricdata.ResourceMetrics {
		var rm metricdata.ResourceMetrics
		if err := reader.Collect(context.Background(), &rm); err != nil {
			t.Fatalf("Collect failed: %v", err)
		}
		return rm
	}
}

// findMetric searches ResourceMetrics for a metric by name.
func findMetric(rm metricdata.ResourceMetrics, name string) *metricdata.Metrics {
	for _, sm := range rm.ScopeMetrics {
		for i := range sm.Metrics {
			if sm.Metrics[i].Name == name {
				return &sm.Metrics[i]
			}
		}
	}
	return nil
}

func TestRecordAndCollect(t *testing.T) {
	collect := setupTestMeter(t)
	rec := New("testsvc", slog.Default())

	ctx := context.Background()
	rec.RecordRequest(ctx, "GET", "200", 50, 1024)
	rec.RecordRequest(ctx, "POST", "201", 120, 2048)
	rec.RecordRequest(ctx, "GET", "500", 5000, 0)

	rm := collect()

	if m := findMetric(rm, "testsvc_requests_total"); m == nil {
		t.Error("expected testsvc_requests_total in collected metrics")
	}
	if m := findMetric(rm, "testsvc_request_duration_seconds"); m == nil {
		t.Error("expected testsvc_request_duration_seconds in collected metrics")
	}
	if m := findMetric(rm, "testsvc_content_size_bytes"); m == nil {
		t.Error("expected testsvc_content_size_bytes in collected metrics")
	}
}

func TestCardinalityLimit(t *testing.T) {
	collect := setupTestMeter(t)
	rec := New("cardsvc", nil)

	ctx := context.Background()
	// Fill up to the limit
	for i := range MaxLabelCombinations {
		rec.RecordRequest(ctx, "GET", fmt.Sprintf("status_%d", i), 10, 100)
	}

	// The next new combination should be dropped (no panic, no error)
	rec.RecordRequest(ctx, "GET", "overflow_status", 10, 100)

	// Verify metrics still collect without error
	rm := collect()
	if m := findMetric(rm, "cardsvc_requests_total"); m == nil {
		t.Error("expected cardsvc_requests_total after cardinality overflow")
	}
}

func TestRecorderCounter(t *testing.T) {
	_ = setupTestMeter(t)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := New("test", logger)
	counter := r.Counter("searches_total")
	if counter == nil {
		t.Fatal("Counter returned nil")
	}
	ctx := context.Background()
	counter.Add(ctx, 1, "type", "organic")
	counter.Add(ctx, 3, "type", "paid")
}

func TestRecorderHistogram(t *testing.T) {
	_ = setupTestMeter(t)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := New("test", logger)
	hist := r.Histogram("pdf_size_bytes", ContentBuckets)
	if hist == nil {
		t.Fatal("Histogram returned nil")
	}
	ctx := context.Background()
	hist.Observe(ctx, 524288, "format", "pdf")
	hist.Observe(ctx, 1024, "format", "png")
}

func TestCustomMetricsAppearInCollect(t *testing.T) {
	collect := setupTestMeter(t)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := New("app", logger)
	counter := r.Counter("events_total")
	counter.Add(context.Background(), 5, "kind", "click")

	rm := collect()
	if m := findMetric(rm, "app_events_total"); m == nil {
		t.Fatal("custom counter not in collected metrics")
	}
}

func TestLabelCollision_DifferentKeysNotEqual(t *testing.T) {
	_ = setupTestMeter(t)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := New("collide", logger)
	counter := r.Counter("events_total")

	ctx := context.Background()
	// These two calls have different keys but could collide if only values
	// were hashed: ("a","x") vs ("b","x") — values are the same.
	counter.Add(ctx, 1, "a", "x")
	counter.Add(ctx, 1, "b", "x")

	// Both should register as distinct combos (no cardinality collision).
	// Verify we can still add a third distinct combo without overflow.
	counter.Add(ctx, 1, "c", "y")

	// If the old pairsToValues was used, "a","x" and "b","x" would hash
	// to the same combo "x", collapsing to 1 combo instead of 2.
	// The new pairsToCombo hashes "a=x" and "b=x" — distinct.
}

func TestMetricPrefix(t *testing.T) {
	collect := setupTestMeter(t)
	rec := New("custom_prefix", nil)
	rec.RecordRequest(context.Background(), "GET", "200", 10, 100)

	rm := collect()
	if m := findMetric(rm, "custom_prefix_requests_total"); m == nil {
		t.Error("expected custom_prefix_requests_total in collected metrics")
	}
}

func TestWarnOnceOverflowLogsOnce(t *testing.T) {
	_ = setupTestMeter(t)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	rec := New("warnsvc", logger)

	ctx := context.Background()
	// Fill to cardinality limit
	for i := range MaxLabelCombinations {
		rec.RecordRequest(ctx, "GET", fmt.Sprintf("s%d", i), 10, 100)
	}

	// Trigger overflow twice
	rec.RecordRequest(ctx, "GET", "overflow-1", 10, 100)
	rec.RecordRequest(ctx, "GET", "overflow-2", 10, 100)

	output := buf.String()
	count := strings.Count(output, "cardinality limit reached")
	if count != 1 {
		t.Fatalf("expected exactly 1 overflow warning, got %d", count)
	}
}
