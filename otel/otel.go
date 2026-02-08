// Package otel bootstraps OpenTelemetry trace and metric pipelines for
// chassis-go services. It is the sole SDK consumer — all other chassis
// modules depend only on OTel API packages.
package otel

import (
	"context"
	"errors"
	"log/slog"
	"time"

	chassis "github.com/ai8future/chassis-go/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// Config configures the OpenTelemetry bootstrap.
type Config struct {
	ServiceName    string
	ServiceVersion string
	Endpoint       string           // OTLP gRPC endpoint, defaults to localhost:4317
	Sampler        sdktrace.Sampler // defaults to AlwaysSample
	Insecure       bool             // when true, disables TLS for OTLP connections
}

// ShutdownFunc drains and closes all OTel providers.
type ShutdownFunc func(ctx context.Context) error

// AlwaysSample returns a sampler that samples every trace.
func AlwaysSample() sdktrace.Sampler {
	return sdktrace.AlwaysSample()
}

// RatioSample returns a sampler that samples a fraction of traces.
func RatioSample(fraction float64) sdktrace.Sampler {
	return sdktrace.TraceIDRatioBased(fraction)
}

// Init initializes OpenTelemetry trace and metric pipelines.
// Returns a ShutdownFunc that must be called on process exit.
func Init(cfg Config) ShutdownFunc {
	chassis.AssertVersionChecked()

	if cfg.Endpoint == "" {
		cfg.Endpoint = "localhost:4317"
	}
	if cfg.Sampler == nil {
		cfg.Sampler = sdktrace.AlwaysSample()
	}

	ctx := context.Background()

	res, resErr := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
	)
	if resErr != nil {
		slog.Warn("otel: resource creation failed, using default", "error", resErr)
		res = resource.Default()
	}

	// --- Trace pipeline ---
	traceOpts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(cfg.Endpoint),
	}
	if cfg.Insecure {
		traceOpts = append(traceOpts, otlptracegrpc.WithInsecure())
	}
	traceExporter, err := otlptracegrpc.New(ctx, traceOpts...)
	if err != nil {
		slog.Error("otel: trace exporter creation failed, all telemetry disabled", "error", err)
		return func(ctx context.Context) error { return nil }
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(cfg.Sampler),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// --- Metric pipeline ---
	metricOpts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
	}
	if cfg.Insecure {
		metricOpts = append(metricOpts, otlpmetricgrpc.WithInsecure())
	}
	metricExporter, err := otlpmetricgrpc.New(ctx, metricOpts...)
	if err != nil {
		slog.Warn("otel: metric exporter creation failed, metrics disabled", "error", err)
		return func(ctx context.Context) error {
			shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return tp.Shutdown(shutdownCtx)
		}
	}

	mp := metric.NewMeterProvider(
		metric.WithReader(metric.NewPeriodicReader(metricExporter)),
		metric.WithResource(res),
	)

	otel.SetMeterProvider(mp)

	return func(ctx context.Context) error {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		tErr := tp.Shutdown(shutdownCtx)
		mErr := mp.Shutdown(shutdownCtx)
		return errors.Join(tErr, mErr)
	}
}

// DetachContext returns a new context.Background() populated with the OTel
// SpanContext from the original context. Cancellation is detached; trace
// correlation is preserved. Use this when spawning goroutines from request
// handlers.
func DetachContext(ctx context.Context) context.Context {
	spanCtx := trace.SpanContextFromContext(ctx)
	if !spanCtx.IsValid() {
		return context.Background()
	}
	return trace.ContextWithSpanContext(context.Background(), spanCtx)
}
