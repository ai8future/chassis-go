// Package otelutil provides shared OTel API helpers for chassis-go packages.
// It depends only on OTel API packages (no SDK) so that consumers remain
// decoupled from the SDK lifecycle managed by the otel package.
package otelutil

import (
	"sync"

	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// LazyHistogram returns a function that lazily initializes and returns a
// Float64Histogram. The histogram is created on first call using the global
// MeterProvider and cached via sync.Once. The meterName scopes the meter
// (typically the importing package's path).
func LazyHistogram(meterName, histName string, opts ...metric.Float64HistogramOption) func() metric.Float64Histogram {
	var (
		once sync.Once
		hist metric.Float64Histogram
	)
	return func() metric.Float64Histogram {
		once.Do(func() {
			meter := otelapi.GetMeterProvider().Meter(meterName)
			var err error
			hist, err = meter.Float64Histogram(histName, opts...)
			if err != nil {
				otelapi.Handle(err)
			}
		})
		return hist
	}
}
