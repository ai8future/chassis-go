package otelutil

import (
	"context"
	"sync"
	"testing"

	otelapi "go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestLazyHistogramReturnsSameInstance(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otelapi.GetMeterProvider()
	otelapi.SetMeterProvider(mp)
	defer func() {
		otelapi.SetMeterProvider(prev)
		mp.Shutdown(context.Background())
	}()

	getter := LazyHistogram("test", "test_histogram")

	h1 := getter()
	h2 := getter()
	if h1 == nil {
		t.Fatal("LazyHistogram returned nil on first call")
	}
	// sync.Once guarantees the same instance.
	if h1 != h2 {
		t.Fatal("expected same histogram instance on second call")
	}
}

func TestLazyHistogramRecordsValues(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otelapi.GetMeterProvider()
	otelapi.SetMeterProvider(mp)
	defer func() {
		otelapi.SetMeterProvider(prev)
		mp.Shutdown(context.Background())
	}()

	getter := LazyHistogram("test-meter", "my_duration")
	h := getter()
	h.Record(context.Background(), 1.5)
	h.Record(context.Background(), 2.5)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "my_duration" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("expected to find my_duration metric")
	}
}

func TestLazyHistogramConcurrentFirstCall(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otelapi.GetMeterProvider()
	otelapi.SetMeterProvider(mp)
	defer func() {
		otelapi.SetMeterProvider(prev)
		mp.Shutdown(context.Background())
	}()

	getter := LazyHistogram("test", "concurrent_hist")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h := getter()
			if h != nil {
				h.Record(context.Background(), 1.0)
			}
		}()
	}
	wg.Wait()

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	if len(rm.ScopeMetrics) == 0 || len(rm.ScopeMetrics[0].Metrics) == 0 {
		t.Fatal("expected at least one metric after concurrent calls")
	}
}
