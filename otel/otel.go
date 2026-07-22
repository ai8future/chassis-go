// Package otel bootstraps OpenTelemetry trace and metric pipelines for
// chassis-go services. It is the sole SDK consumer — all other chassis
// modules depend only on OTel API packages.
package otel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc/credentials"
)

// Config configures the OpenTelemetry bootstrap.
type Config struct {
	ServiceName    string
	ServiceVersion string
	Endpoint       string           // OTLP gRPC endpoint, defaults to localhost:4317
	Sampler        sdktrace.Sampler // defaults to AlwaysSample
	Insecure       bool             // when true, disables TLS for OTLP connections
	Secure         bool             // when true, explicitly requires TLS and overrides plaintext exporter environment
	TLSConfig      *tls.Config      // optional TLS policy when Secure is true; cloned and raised to TLS 1.2 minimum
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

// Init initializes OpenTelemetry trace and metric pipelines. It shares the
// package's single-active lifecycle with InitChecked. For backward
// compatibility, configuration, construction, or already-active errors are
// logged and degrade to a no-op ShutdownFunc instead of being returned.
// Returns a ShutdownFunc that must be called on process exit.
func Init(cfg Config) ShutdownFunc {
	shutdown, err := initTelemetry(cfg, false)
	if err != nil {
		slog.Error("otel: initialization failed, telemetry globals unchanged", "error", err)
		return func(context.Context) error { return nil }
	}
	return shutdown
}

// InitChecked initializes complete trace and metric pipelines or returns an
// error without installing global providers. Only one initialization created
// through this package may be active at a time; another InitChecked call is
// rejected until its ShutdownFunc finishes. Configuration and exporter
// construction are checked synchronously, but collector reachability and the
// TLS handshake remain lazy and can first fail during export or shutdown.
// Callers that require telemetry configuration to fail closed should use this
// entry point.
func InitChecked(cfg Config) (ShutdownFunc, error) {
	return initTelemetry(cfg, true)
}

func initTelemetry(cfg Config, requireAll bool) (ShutdownFunc, error) {
	return initTelemetryWithFactories(cfg, requireAll, defaultExporterFactories)
}

var errTelemetryActive = errors.New("otel: telemetry initialization is already active")

type exporterFactories struct {
	trace  func(context.Context, ...otlptracegrpc.Option) (sdktrace.SpanExporter, error)
	metric func(context.Context, ...otlpmetricgrpc.Option) (metric.Exporter, error)
}

var defaultExporterFactories = exporterFactories{
	trace: func(ctx context.Context, opts ...otlptracegrpc.Option) (sdktrace.SpanExporter, error) {
		return otlptracegrpc.New(ctx, opts...)
	},
	metric: func(ctx context.Context, opts ...otlpmetricgrpc.Option) (metric.Exporter, error) {
		return otlpmetricgrpc.New(ctx, opts...)
	},
}

var telemetryLifecycle struct {
	sync.Mutex
	active *telemetrySession
}

type telemetrySession struct {
	tp   *sdktrace.TracerProvider
	mp   *metric.MeterProvider
	once sync.Once
	err  error
}

func initTelemetryWithFactories(cfg Config, requireAll bool, factories exporterFactories) (ShutdownFunc, error) {
	chassis.AssertVersionChecked()
	normalized, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}

	telemetryLifecycle.Lock()
	defer telemetryLifecycle.Unlock()
	if telemetryLifecycle.active != nil {
		return nil, errTelemetryActive
	}

	tp, mp, err := constructProviders(normalized, requireAll, factories)
	if err != nil {
		return nil, err
	}
	session := &telemetrySession{tp: tp, mp: mp}
	installGlobals(tp, mp)
	telemetryLifecycle.active = session
	return session.shutdown, nil
}

func constructProviders(cfg Config, requireAll bool, factories exporterFactories) (*sdktrace.TracerProvider, *metric.MeterProvider, error) {
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
	switch {
	case cfg.Insecure:
		traceOpts = append(traceOpts, otlptracegrpc.WithInsecure())
	case cfg.Secure:
		traceOpts = append(traceOpts, otlptracegrpc.WithTLSCredentials(secureCredentials(cfg.TLSConfig)))
	}
	traceExporter, err := factories.trace(ctx, traceOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("otel: trace exporter creation: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(cfg.Sampler),
	)

	// --- Metric pipeline ---
	metricOpts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
	}
	switch {
	case cfg.Insecure:
		metricOpts = append(metricOpts, otlpmetricgrpc.WithInsecure())
	case cfg.Secure:
		metricOpts = append(metricOpts, otlpmetricgrpc.WithTLSCredentials(secureCredentials(cfg.TLSConfig)))
	}
	metricExporter, err := factories.metric(ctx, metricOpts...)
	if err != nil {
		if requireAll {
			shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return nil, nil, errors.Join(fmt.Errorf("otel: metric exporter creation: %w", err), tp.Shutdown(shutdownCtx))
		}
		slog.Warn("otel: metric exporter creation failed, metrics disabled", "error", err)
		return tp, nil, nil
	}

	mp := metric.NewMeterProvider(
		metric.WithReader(metric.NewPeriodicReader(metricExporter)),
		metric.WithResource(res),
	)

	return tp, mp, nil
}

func normalizeConfig(cfg Config) (Config, error) {
	if cfg.Insecure && cfg.Secure {
		return Config{}, fmt.Errorf("otel: Insecure and Secure cannot both be true")
	}
	if cfg.TLSConfig != nil && !cfg.Secure {
		return Config{}, fmt.Errorf("otel: TLSConfig requires Secure")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = "localhost:4317"
	}
	if err := validateEndpoint(cfg.Endpoint); err != nil {
		return Config{}, err
	}
	if cfg.Sampler == nil {
		cfg.Sampler = sdktrace.AlwaysSample()
	}
	if cfg.Secure {
		tlsConfig, err := normalizeTLSConfig(cfg.TLSConfig)
		if err != nil {
			return Config{}, err
		}
		cfg.TLSConfig = tlsConfig
	}
	return cfg, nil
}

func validateEndpoint(endpoint string) error {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return fmt.Errorf("otel: Endpoint must be host:port: %w", err)
	}
	if !validEndpointHost(host) {
		return fmt.Errorf("otel: Endpoint host %q is invalid", host)
	}
	if port == "" {
		return fmt.Errorf("otel: Endpoint port is empty")
	}
	for _, char := range port {
		if char < '0' || char > '9' {
			return fmt.Errorf("otel: Endpoint port %q is not numeric", port)
		}
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNumber == 0 {
		return fmt.Errorf("otel: Endpoint port %q is outside 1..65535", port)
	}
	return nil
}

func validEndpointHost(host string) bool {
	if host == "" || strings.TrimSpace(host) != host {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if address, zone, ok := strings.Cut(host, "%"); ok {
		return zone != "" && strings.TrimSpace(zone) == zone && net.ParseIP(address) != nil
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
				(char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

func normalizeTLSConfig(config *tls.Config) (*tls.Config, error) {
	if config == nil {
		config = &tls.Config{}
	} else {
		config = config.Clone()
	}
	if !knownTLSVersion(config.MinVersion) {
		return nil, fmt.Errorf("otel: unknown TLS minimum version %#x", config.MinVersion)
	}
	if !knownTLSVersion(config.MaxVersion) {
		return nil, fmt.Errorf("otel: unknown TLS maximum version %#x", config.MaxVersion)
	}
	if config.MinVersion < tls.VersionTLS12 {
		config.MinVersion = tls.VersionTLS12
	}
	if config.MaxVersion != 0 && config.MaxVersion < config.MinVersion {
		return nil, fmt.Errorf("otel: TLS version range is empty after enforcing TLS 1.2 minimum")
	}
	return config, nil
}

func knownTLSVersion(version uint16) bool {
	switch version {
	case 0, tls.VersionTLS10, tls.VersionTLS11, tls.VersionTLS12, tls.VersionTLS13:
		return true
	default:
		return false
	}
}

func secureCredentials(config *tls.Config) credentials.TransportCredentials {
	if config == nil {
		config = &tls.Config{}
	} else {
		config = config.Clone()
	}
	if config.MinVersion < tls.VersionTLS12 {
		config.MinVersion = tls.VersionTLS12
	}
	return credentials.NewTLS(config)
}

func installGlobals(tp *sdktrace.TracerProvider, mp *metric.MeterProvider) {
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	if mp == nil {
		otel.SetMeterProvider(metricnoop.NewMeterProvider())
	} else {
		otel.SetMeterProvider(mp)
	}
}

func installNoopGlobals() {
	otel.SetTracerProvider(tracenoop.NewTracerProvider())
	otel.SetMeterProvider(metricnoop.NewMeterProvider())
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
}

func (session *telemetrySession) shutdown(ctx context.Context) error {
	session.once.Do(func() {
		if ctx == nil {
			ctx = context.Background()
		}
		telemetryLifecycle.Lock()
		// Disconnect callers from this session before draining it. This keeps
		// process globals from retaining providers after Shutdown begins. The
		// session remains active while it drains, so concurrent package
		// initialization is rejected without holding the lifecycle mutex across
		// exporter I/O.
		if telemetryLifecycle.active == session {
			installNoopGlobals()
		}
		telemetryLifecycle.Unlock()

		session.err = shutdownProviders(ctx, session.tp, session.mp)

		telemetryLifecycle.Lock()
		if telemetryLifecycle.active == session {
			telemetryLifecycle.active = nil
		}
		telemetryLifecycle.Unlock()
	})
	return session.err
}

func shutdownProviders(ctx context.Context, tp *sdktrace.TracerProvider, mp *metric.MeterProvider) error {
	tCtx, tCancel := context.WithTimeout(ctx, 5*time.Second)
	tErr := tp.Shutdown(tCtx)
	tCancel()

	var mErr error
	if mp != nil {
		mCtx, mCancel := context.WithTimeout(ctx, 5*time.Second)
		mErr = mp.Shutdown(mCtx)
		mCancel()
	}
	return errors.Join(tErr, mErr)
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
