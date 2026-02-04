// Package otel bootstraps OpenTelemetry trace and metric pipelines for
// chassis-go services. It is the sole SDK consumer — all other chassis
// modules depend only on OTel API packages.
package otel

import (
	"context"
	"time"

	chassis "github.com/ai8future/chassis-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
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

	res, _ := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
	)

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.Endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		// Degrade gracefully — no tracing but no crash.
		return func(ctx context.Context) error { return nil }
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(cfg.Sampler),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return func(ctx context.Context) error {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return tp.Shutdown(shutdownCtx)
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
