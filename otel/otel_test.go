// otel/otel_test.go
package otel_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/otel"
	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	collectmetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collecttrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// shutdownWithShortTimeout calls the shutdown function with a short deadline
// to avoid long waits when no OTLP collector is available.
func shutdownWithShortTimeout(t *testing.T, shutdown otel.ShutdownFunc) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	return shutdown(ctx)
}

// isCollectorUnavailable returns true if the error indicates the OTLP collector
// is not reachable — expected in test environments without a local collector.
func isCollectorUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "code = Unavailable") ||
		strings.Contains(msg, "context deadline exceeded")
}

func TestInitReturnsShutdownFunc(t *testing.T) {
	chassis.ResetVersionCheck()
	chassis.RequireMajor(11)

	shutdown := otel.Init(otel.Config{
		ServiceName:    "test-svc",
		ServiceVersion: "1.0.0",
		Insecure:       true, // plaintext for local test
	})
	if shutdown == nil {
		t.Fatal("Init returned nil shutdown function")
	}
	if err := shutdownWithShortTimeout(t, shutdown); err != nil && !isCollectorUnavailable(err) {
		t.Fatalf("shutdown returned unexpected error: %v", err)
	}
}

func TestDetachContextPreservesSpanContext(t *testing.T) {
	chassis.ResetVersionCheck()
	chassis.RequireMajor(11)

	// Create a span context with a known trace ID.
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     false,
	})

	parentCtx, cancel := context.WithCancel(context.Background())
	parentCtx = trace.ContextWithSpanContext(parentCtx, sc)

	detached := otel.DetachContext(parentCtx)

	// Cancel the parent — detached should not be affected.
	cancel()

	select {
	case <-detached.Done():
		t.Fatal("detached context was cancelled when parent was cancelled")
	default:
		// expected
	}

	// Span context should be preserved.
	got := trace.SpanContextFromContext(detached)
	if got.TraceID() != traceID {
		t.Fatalf("trace ID not preserved: got %s, want %s", got.TraceID(), traceID)
	}
	if got.SpanID() != spanID {
		t.Fatalf("span ID not preserved: got %s, want %s", got.SpanID(), spanID)
	}
}

func TestInit_InsecureExplicitlySet(t *testing.T) {
	chassis.ResetVersionCheck()
	chassis.RequireMajor(11)

	// With Insecure=true, Init should use plaintext gRPC.
	shutdown := otel.Init(otel.Config{
		ServiceName:    "test-insecure",
		ServiceVersion: "1.0.0",
		Insecure:       true,
	})
	if shutdown == nil {
		t.Fatal("Init returned nil shutdown function")
	}
	if err := shutdownWithShortTimeout(t, shutdown); err != nil && !isCollectorUnavailable(err) {
		t.Fatalf("shutdown returned unexpected error: %v", err)
	}
}

func TestInit_DefaultTLS(t *testing.T) {
	chassis.ResetVersionCheck()
	chassis.RequireMajor(11)

	// Default (Insecure=false) should attempt TLS connection.
	// Without a TLS endpoint, Init still succeeds (lazy connection) but
	// shutdown may return errors when flushing to a non-TLS endpoint.
	shutdown := otel.Init(otel.Config{
		ServiceName:    "test-tls-default",
		ServiceVersion: "1.0.0",
	})
	if shutdown == nil {
		t.Fatal("Init returned nil shutdown function")
	}
	// Shutdown errors are expected in test (no TLS endpoint) — just verify
	// it doesn't panic.
	_ = shutdownWithShortTimeout(t, shutdown)
}

func TestInitSecureConfigOverridesPlaintextExporterEnvironment(t *testing.T) {
	chassis.ResetVersionCheck()
	chassis.RequireMajor(11)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	collector := &countingTraceCollector{}
	collecttrace.RegisterTraceServiceServer(server, collector)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(func() {
		server.Stop()
		_ = lis.Close()
	})

	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://"+lis.Addr().String())
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_INSECURE", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_INSECURE", "")
	shutdown := otel.Init(otel.Config{
		ServiceName:    "test-secure-override",
		ServiceVersion: "1.0.0",
		Endpoint:       lis.Addr().String(),
		Secure:         true,
	})
	_, span := otelapi.Tracer("secure-override-test").Start(context.Background(), "probe")
	span.End()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = shutdown(ctx)
	if calls := collector.calls.Load(); calls != 0 {
		t.Fatalf("secure config exported %d trace batch(es) to a plaintext collector", calls)
	}
}

func TestInitCheckedSecureExportsTracesAndMetricsDespitePlaintextEnvironment(t *testing.T) {
	chassis.ResetVersionCheck()
	chassis.RequireMajor(11)
	certificate, roots := testTLSCertificate(t)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
	})))
	traceCollector := &countingTraceCollector{}
	metricCollector := &countingMetricCollector{}
	collecttrace.RegisterTraceServiceServer(server, traceCollector)
	collectmetric.RegisterMetricsServiceServer(server, metricCollector)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(func() {
		server.Stop()
		_ = lis.Close()
	})

	// Every standard exporter variable points at a hostile plaintext target.
	// Explicit Config values must win for both signal pipelines.
	for _, key := range []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
	} {
		t.Setenv(key, "http://127.0.0.1:1")
	}
	for _, key := range []string{
		"OTEL_EXPORTER_OTLP_INSECURE",
		"OTEL_EXPORTER_OTLP_TRACES_INSECURE",
		"OTEL_EXPORTER_OTLP_METRICS_INSECURE",
	} {
		t.Setenv(key, "true")
	}

	shutdown, err := otel.InitChecked(otel.Config{
		ServiceName:    "test-secure-positive",
		ServiceVersion: "1.0.0",
		Endpoint:       lis.Addr().String(),
		Secure:         true,
		TLSConfig: &tls.Config{
			RootCAs:    roots,
			MinVersion: tls.VersionTLS12,
		},
	})
	if err != nil {
		t.Fatalf("InitChecked() error = %v", err)
	}
	ctx, span := otelapi.Tracer("secure-positive-test").Start(context.Background(), "secure-positive-span")
	counter, err := otelapi.Meter("secure-positive-test").Int64Counter("secure_positive_metric")
	if err != nil {
		t.Fatalf("create counter: %v", err)
	}
	counter.Add(ctx, 1)
	span.End()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown/flush telemetry: %v", err)
	}
	if calls := traceCollector.calls.Load(); calls == 0 {
		t.Fatal("explicit Secure config exported no traces to the TLS collector")
	}
	if calls := metricCollector.calls.Load(); calls == 0 {
		t.Fatal("explicit Secure config exported no metrics to the TLS collector")
	}
}

func TestInitCheckedRejectsInvalidExporterBeforeInstallingProviders(t *testing.T) {
	chassis.ResetVersionCheck()
	chassis.RequireMajor(11)
	shutdown, err := otel.InitChecked(otel.Config{
		ServiceName:    "test-checked-init",
		ServiceVersion: "1.0.0",
		Endpoint:       "dns:///%zz",
		Secure:         true,
	})
	if err == nil || shutdown != nil {
		t.Fatalf("InitChecked invalid endpoint = shutdown %v, err %v; want nil/error", shutdown, err)
	}
}

func TestInitCheckedRejectsContradictoryTransportSecurity(t *testing.T) {
	chassis.ResetVersionCheck()
	chassis.RequireMajor(11)
	shutdown, err := otel.InitChecked(otel.Config{
		ServiceName:    "test-contradictory-security",
		ServiceVersion: "1.0.0",
		Insecure:       true,
		Secure:         true,
	})
	if err == nil || shutdown != nil {
		t.Fatalf("InitChecked contradictory security = shutdown %v, err %v; want nil/error", shutdown, err)
	}
}

type countingTraceCollector struct {
	collecttrace.UnimplementedTraceServiceServer
	calls atomic.Int32
}

func (c *countingTraceCollector) Export(context.Context, *collecttrace.ExportTraceServiceRequest) (*collecttrace.ExportTraceServiceResponse, error) {
	c.calls.Add(1)
	return &collecttrace.ExportTraceServiceResponse{}, nil
}

type countingMetricCollector struct {
	collectmetric.UnimplementedMetricsServiceServer
	calls atomic.Int32
}

func (c *countingMetricCollector) Export(context.Context, *collectmetric.ExportMetricsServiceRequest) (*collectmetric.ExportMetricsServiceResponse, error) {
	c.calls.Add(1)
	return &collectmetric.ExportMetricsServiceResponse{}, nil
}

func testTLSCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate test TLS key: %v", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "chassis-go OTel test collector"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create test TLS certificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal test TLS key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("load test TLS key pair: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("append test TLS root")
	}
	return certificate, roots
}

func TestDetachContextWithNoSpanReturnsBackground(t *testing.T) {
	detached := otel.DetachContext(context.Background())
	sc := trace.SpanContextFromContext(detached)
	if sc.IsValid() {
		t.Fatal("expected invalid span context from empty parent")
	}
}

func TestAlwaysSampleReturnsNonNil(t *testing.T) {
	s := otel.AlwaysSample()
	if s == nil {
		t.Fatal("AlwaysSample() returned nil")
	}
}

func TestRatioSampleReturnsNonNil(t *testing.T) {
	s := otel.RatioSample(0.5)
	if s == nil {
		t.Fatal("RatioSample(0.5) returned nil")
	}
}

func TestInitWithCustomSampler(t *testing.T) {
	chassis.ResetVersionCheck()
	chassis.RequireMajor(11)

	shutdown := otel.Init(otel.Config{
		ServiceName:    "test-custom-sampler",
		ServiceVersion: "1.0.0",
		Insecure:       true,
		Sampler:        otel.RatioSample(0.1),
	})
	if shutdown == nil {
		t.Fatal("Init returned nil shutdown function")
	}
	if err := shutdownWithShortTimeout(t, shutdown); err != nil && !isCollectorUnavailable(err) {
		t.Fatalf("shutdown returned unexpected error: %v", err)
	}
}
