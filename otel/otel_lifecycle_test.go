package otel

import (
	"context"
	"crypto/tls"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	metricapi "go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	traceapi "go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestInitCheckedValidationLeavesGlobalsUnchanged(t *testing.T) {
	requireOTelVersion(t)
	tracer := &markedTracerProvider{TracerProvider: tracenoop.NewTracerProvider()}
	meter := &markedMeterProvider{MeterProvider: metricnoop.NewMeterProvider()}
	propagator := markedPropagator{}
	otelapi.SetTracerProvider(tracer)
	otelapi.SetMeterProvider(meter)
	otelapi.SetTextMapPropagator(propagator)
	t.Cleanup(installNoopGlobals)

	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "missing port", cfg: Config{Endpoint: "collector", Insecure: true}},
		{name: "empty host", cfg: Config{Endpoint: ":4317", Insecure: true}},
		{name: "non numeric port", cfg: Config{Endpoint: "collector:grpc", Insecure: true}},
		{name: "zero port", cfg: Config{Endpoint: "collector:0", Insecure: true}},
		{name: "out of range port", cfg: Config{Endpoint: "collector:65536", Insecure: true}},
		{name: "URL is not host port", cfg: Config{Endpoint: "https://collector:4317", Secure: true}},
		{name: "contradictory security", cfg: Config{Endpoint: "collector:4317", Insecure: true, Secure: true}},
		{name: "TLS config without secure", cfg: Config{Endpoint: "collector:4317", TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}},
		{name: "unknown minimum TLS version", cfg: Config{Endpoint: "collector:4317", Secure: true, TLSConfig: &tls.Config{MinVersion: 0x9999}}},
		{name: "unknown maximum TLS version", cfg: Config{Endpoint: "collector:4317", Secure: true, TLSConfig: &tls.Config{MaxVersion: 0x9999}}},
		{name: "inverted TLS range", cfg: Config{Endpoint: "collector:4317", Secure: true, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS12}}},
		{name: "maximum below effective minimum", cfg: Config{Endpoint: "collector:4317", Secure: true, TLSConfig: &tls.Config{MaxVersion: tls.VersionTLS11}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shutdown, err := InitChecked(tt.cfg)
			if err == nil || shutdown != nil {
				t.Fatalf("InitChecked() = (%v, %v), want (nil, error)", shutdown, err)
			}
			if got := otelapi.GetTracerProvider(); got != tracer {
				t.Fatalf("tracer provider changed on validation failure: got %T", got)
			}
			if got := otelapi.GetMeterProvider(); got != meter {
				t.Fatalf("meter provider changed on validation failure: got %T", got)
			}
			if got := otelapi.GetTextMapPropagator(); !reflect.DeepEqual(got, propagator) {
				t.Fatalf("propagator changed on validation failure: got %T", got)
			}
		})
	}
}

func TestInitCheckedClonesCallerTLSConfig(t *testing.T) {
	requireOTelVersion(t)
	nextProtos := []string{"caller-owned"}
	caller := &tls.Config{
		MinVersion: tls.VersionTLS10,
		MaxVersion: tls.VersionTLS13,
		ServerName: "collector.example",
		NextProtos: nextProtos,
	}

	cfg := Config{
		Endpoint:  "127.0.0.1:1",
		Secure:    true,
		TLSConfig: caller,
	}
	normalized, err := normalizeConfig(cfg)
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	if normalized.TLSConfig == caller {
		t.Fatal("normalizeConfig retained the caller TLS config pointer")
	}
	if normalized.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("effective TLS minimum = %#x, want TLS 1.2", normalized.TLSConfig.MinVersion)
	}
	shutdown, err := initTelemetryWithFactories(cfg, true, successfulExporterFactories())
	if err != nil {
		t.Fatalf("initTelemetryWithFactories() error = %v", err)
	}
	if caller.MinVersion != tls.VersionTLS10 || caller.MaxVersion != tls.VersionTLS13 || caller.ServerName != "collector.example" {
		t.Fatalf("caller TLS config mutated: %+v", caller)
	}
	if !reflect.DeepEqual(caller.NextProtos, []string{"caller-owned"}) || !reflect.DeepEqual(nextProtos, []string{"caller-owned"}) {
		t.Fatalf("caller TLS protocol slice mutated: config=%v original=%v", caller.NextProtos, nextProtos)
	}
	_ = shutdown(context.Background())
}

func TestInvalidConfigurationSkipsExporterConstruction(t *testing.T) {
	requireOTelVersion(t)
	var traceCalls atomic.Int32
	var metricCalls atomic.Int32
	factories := exporterFactories{
		trace: func(context.Context, ...otlptracegrpc.Option) (sdktrace.SpanExporter, error) {
			traceCalls.Add(1)
			return &countingSpanExporter{}, nil
		},
		metric: func(context.Context, ...otlpmetricgrpc.Option) (sdkmetric.Exporter, error) {
			metricCalls.Add(1)
			return &countingMetricExporter{}, nil
		},
	}
	for _, cfg := range []Config{
		{Endpoint: ":4317", Insecure: true},
		{Endpoint: "collector:not-a-port", Insecure: true},
		{Endpoint: "collector:4317", Secure: true, TLSConfig: &tls.Config{MinVersion: 0x9999}},
		{Endpoint: "collector:4317", Secure: true, TLSConfig: &tls.Config{MaxVersion: tls.VersionTLS11}},
	} {
		shutdown, err := initTelemetryWithFactories(cfg, true, factories)
		if err == nil || shutdown != nil {
			t.Fatalf("invalid config %+v = (%v, %v), want (nil, error)", cfg, shutdown, err)
		}
	}
	if got := traceCalls.Load(); got != 0 {
		t.Fatalf("trace constructor called %d time(s) for invalid configuration", got)
	}
	if got := metricCalls.Load(); got != 0 {
		t.Fatalf("metric constructor called %d time(s) for invalid configuration", got)
	}
}

func TestInitCheckedSingleActiveLifecycleUnderConcurrency(t *testing.T) {
	requireOTelVersion(t)
	var resetViolations atomic.Int32
	assertGlobalsReset := func() {
		if _, ok := otelapi.GetTracerProvider().(tracenoop.TracerProvider); !ok {
			resetViolations.Add(1)
		}
		if _, ok := otelapi.GetMeterProvider().(metricnoop.MeterProvider); !ok {
			resetViolations.Add(1)
		}
		if len(otelapi.GetTextMapPropagator().Fields()) != 0 {
			resetViolations.Add(1)
		}
	}
	traceExporter := &countingSpanExporter{onShutdown: assertGlobalsReset}
	metricExporter := &countingMetricExporter{onShutdown: assertGlobalsReset}
	factories := exporterFactories{
		trace: func(context.Context, ...otlptracegrpc.Option) (sdktrace.SpanExporter, error) {
			return traceExporter, nil
		},
		metric: func(context.Context, ...otlpmetricgrpc.Option) (sdkmetric.Exporter, error) {
			return metricExporter, nil
		},
	}

	const callers = 32
	start := make(chan struct{})
	results := make(chan initResult, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			shutdown, err := initTelemetryWithFactories(Config{Endpoint: "127.0.0.1:4317", Insecure: true}, true, factories)
			results <- initResult{shutdown: shutdown, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var winner ShutdownFunc
	for result := range results {
		if result.err == nil {
			if winner != nil || result.shutdown == nil {
				t.Fatalf("unexpected additional successful initialization: %+v", result)
			}
			winner = result.shutdown
			continue
		}
		if result.shutdown != nil || !errors.Is(result.err, errTelemetryActive) {
			t.Fatalf("losing initialization = (%v, %v), want (nil, errTelemetryActive)", result.shutdown, result.err)
		}
	}
	if winner == nil {
		t.Fatal("no initialization succeeded")
	}

	installedTracer := otelapi.GetTracerProvider()
	installedMeter := otelapi.GetMeterProvider()
	repeatedShutdown, err := InitChecked(Config{Endpoint: "127.0.0.1:4317", Insecure: true})
	if !errors.Is(err, errTelemetryActive) || repeatedShutdown != nil {
		t.Fatalf("repeated InitChecked() = (%v, %v), want (nil, errTelemetryActive)", repeatedShutdown, err)
	}
	if got := otelapi.GetTracerProvider(); got != installedTracer {
		t.Fatal("repeated initialization replaced the active tracer provider")
	}
	if got := otelapi.GetMeterProvider(); got != installedMeter {
		t.Fatal("repeated initialization replaced the active meter provider")
	}

	legacyShutdown := Init(Config{Endpoint: "127.0.0.1:4317", Insecure: true})
	if legacyShutdown == nil {
		t.Fatal("legacy Init returned a nil graceful-degradation shutdown")
	}
	if err := legacyShutdown(context.Background()); err != nil {
		t.Fatalf("legacy repeated-init shutdown error = %v", err)
	}
	if traceExporter.shutdowns.Load() != 0 || metricExporter.shutdowns.Load() != 0 {
		t.Fatal("legacy repeated initialization shut down the active lifecycle")
	}

	if err := winner(context.Background()); err != nil {
		t.Fatalf("shutdown error = %v", err)
	}
	if err := winner(context.Background()); err != nil {
		t.Fatalf("idempotent shutdown error = %v", err)
	}
	if got := traceExporter.shutdowns.Load(); got != 1 {
		t.Fatalf("trace exporter shutdown count = %d, want 1", got)
	}
	if got := metricExporter.shutdowns.Load(); got != 1 {
		t.Fatalf("metric exporter shutdown count = %d, want 1", got)
	}
	if got := resetViolations.Load(); got != 0 {
		t.Fatalf("provider shutdown observed %d global reset violation(s)", got)
	}
	if _, ok := otelapi.GetTracerProvider().(tracenoop.TracerProvider); !ok {
		t.Fatalf("tracer provider after shutdown = %T, want explicit no-op", otelapi.GetTracerProvider())
	}
	if _, ok := otelapi.GetMeterProvider().(metricnoop.MeterProvider); !ok {
		t.Fatalf("meter provider after shutdown = %T, want explicit no-op", otelapi.GetMeterProvider())
	}
	if fields := otelapi.GetTextMapPropagator().Fields(); len(fields) != 0 {
		t.Fatalf("propagator fields after shutdown = %v, want explicit no-op", fields)
	}
}

func TestInitCheckedCleansTraceProviderAfterMetricConstructionFailure(t *testing.T) {
	requireOTelVersion(t)
	tracer := &markedTracerProvider{TracerProvider: tracenoop.NewTracerProvider()}
	meter := &markedMeterProvider{MeterProvider: metricnoop.NewMeterProvider()}
	propagator := markedPropagator{}
	otelapi.SetTracerProvider(tracer)
	otelapi.SetMeterProvider(meter)
	otelapi.SetTextMapPropagator(propagator)
	t.Cleanup(installNoopGlobals)

	traceExporter := &countingSpanExporter{}
	wantErr := errors.New("metric constructor failed")
	shutdown, err := initTelemetryWithFactories(
		Config{Endpoint: "127.0.0.1:4317", Insecure: true},
		true,
		exporterFactories{
			trace: func(context.Context, ...otlptracegrpc.Option) (sdktrace.SpanExporter, error) {
				return traceExporter, nil
			},
			metric: func(context.Context, ...otlpmetricgrpc.Option) (sdkmetric.Exporter, error) {
				return nil, wantErr
			},
		},
	)
	if !errors.Is(err, wantErr) || shutdown != nil {
		t.Fatalf("initTelemetryWithFactories() = (%v, %v), want (nil, wrapped constructor error)", shutdown, err)
	}
	if got := traceExporter.shutdowns.Load(); got != 1 {
		t.Fatalf("partially constructed trace exporter shutdown count = %d, want 1", got)
	}
	if got := otelapi.GetTracerProvider(); got != tracer {
		t.Fatal("partial construction failure changed the tracer global")
	}
	if got := otelapi.GetMeterProvider(); got != meter {
		t.Fatal("partial construction failure changed the meter global")
	}
	if got := otelapi.GetTextMapPropagator(); !reflect.DeepEqual(got, propagator) {
		t.Fatal("partial construction failure changed the propagator global")
	}
}

func TestInitializationIsRejectedWhileShutdownDrains(t *testing.T) {
	requireOTelVersion(t)
	shutdownEntered := make(chan struct{})
	releaseShutdown := make(chan struct{})
	traceExporter := &countingSpanExporter{onShutdown: func() {
		close(shutdownEntered)
		<-releaseShutdown
	}}
	factories := exporterFactories{
		trace: func(context.Context, ...otlptracegrpc.Option) (sdktrace.SpanExporter, error) {
			return traceExporter, nil
		},
		metric: func(context.Context, ...otlpmetricgrpc.Option) (sdkmetric.Exporter, error) {
			return &countingMetricExporter{}, nil
		},
	}
	shutdown, err := initTelemetryWithFactories(Config{Endpoint: "127.0.0.1:4317", Insecure: true}, true, factories)
	if err != nil {
		t.Fatalf("initialization error = %v", err)
	}
	shutdownResult := make(chan error, 1)
	go func() { shutdownResult <- shutdown(context.Background()) }()
	select {
	case <-shutdownEntered:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not reach exporter")
	}

	repeated, err := initTelemetryWithFactories(Config{Endpoint: "127.0.0.1:4317", Insecure: true}, true, successfulExporterFactories())
	if !errors.Is(err, errTelemetryActive) || repeated != nil {
		t.Fatalf("initialization during shutdown = (%v, %v), want (nil, errTelemetryActive)", repeated, err)
	}
	if _, ok := otelapi.GetTracerProvider().(tracenoop.TracerProvider); !ok {
		t.Fatalf("tracer global during shutdown = %T, want explicit no-op", otelapi.GetTracerProvider())
	}
	if _, ok := otelapi.GetMeterProvider().(metricnoop.MeterProvider); !ok {
		t.Fatalf("meter global during shutdown = %T, want explicit no-op", otelapi.GetMeterProvider())
	}
	if fields := otelapi.GetTextMapPropagator().Fields(); len(fields) != 0 {
		t.Fatalf("propagator fields during shutdown = %v, want explicit no-op", fields)
	}

	close(releaseShutdown)
	if err := <-shutdownResult; err != nil {
		t.Fatalf("shutdown error = %v", err)
	}
	second, err := initTelemetryWithFactories(Config{Endpoint: "127.0.0.1:4317", Insecure: true}, true, successfulExporterFactories())
	if err != nil {
		t.Fatalf("initialization after shutdown error = %v", err)
	}
	if err := second(context.Background()); err != nil {
		t.Fatalf("second shutdown error = %v", err)
	}
}

type initResult struct {
	shutdown ShutdownFunc
	err      error
}

type markedTracerProvider struct{ traceapi.TracerProvider }
type markedMeterProvider struct{ metricapi.MeterProvider }

type markedPropagator struct{}

func (markedPropagator) Inject(context.Context, propagation.TextMapCarrier) {}
func (markedPropagator) Extract(ctx context.Context, _ propagation.TextMapCarrier) context.Context {
	return ctx
}
func (markedPropagator) Fields() []string { return []string{"marked"} }

type countingSpanExporter struct {
	shutdowns  atomic.Int32
	onShutdown func()
}

func (*countingSpanExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error { return nil }
func (e *countingSpanExporter) Shutdown(context.Context) error {
	if e.onShutdown != nil {
		e.onShutdown()
	}
	e.shutdowns.Add(1)
	return nil
}

type countingMetricExporter struct {
	shutdowns  atomic.Int32
	onShutdown func()
}

func (*countingMetricExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(kind)
}
func (*countingMetricExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(kind)
}
func (*countingMetricExporter) Export(context.Context, *metricdata.ResourceMetrics) error { return nil }
func (*countingMetricExporter) ForceFlush(context.Context) error                          { return nil }
func (e *countingMetricExporter) Shutdown(context.Context) error {
	if e.onShutdown != nil {
		e.onShutdown()
	}
	e.shutdowns.Add(1)
	return nil
}

func successfulExporterFactories() exporterFactories {
	return exporterFactories{
		trace: func(context.Context, ...otlptracegrpc.Option) (sdktrace.SpanExporter, error) {
			return &countingSpanExporter{}, nil
		},
		metric: func(context.Context, ...otlpmetricgrpc.Option) (sdkmetric.Exporter, error) {
			return &countingMetricExporter{}, nil
		},
	}
}

func requireOTelVersion(t *testing.T) {
	t.Helper()
	chassis.ResetVersionCheck()
	chassis.RequireMajor(11)
}
