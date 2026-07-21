package call

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/internal/otelutil"
	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func init() { chassis.RequireMajor(11) }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type tokenSourceFunc func(context.Context) (string, error)

func (f tokenSourceFunc) Token(ctx context.Context) (string, error) { return f(ctx) }

type telemetryCapture struct {
	traceExporter *tracetest.InMemoryExporter
	traceProvider *sdktrace.TracerProvider
	metricReader  *sdkmetric.ManualReader
}

func setupTelemetryCapture(t *testing.T) *telemetryCapture {
	t.Helper()

	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	metricReader := sdkmetric.NewManualReader()
	metricProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))

	previousTraceProvider := otelapi.GetTracerProvider()
	previousMeterProvider := otelapi.GetMeterProvider()
	previousPropagator := otelapi.GetTextMapPropagator()
	previousDuration := getClientDuration

	otelapi.SetTracerProvider(traceProvider)
	otelapi.SetMeterProvider(metricProvider)
	otelapi.SetTextMapPropagator(propagation.TraceContext{})
	getClientDuration = otelutil.LazyHistogram(tracerName, "http.client.request.duration")

	t.Cleanup(func() {
		getClientDuration = previousDuration
		otelapi.SetTextMapPropagator(previousPropagator)
		otelapi.SetMeterProvider(previousMeterProvider)
		otelapi.SetTracerProvider(previousTraceProvider)
		_ = metricProvider.Shutdown(context.Background())
		_ = traceProvider.Shutdown(context.Background())
	})

	return &telemetryCapture{
		traceExporter: traceExporter,
		traceProvider: traceProvider,
		metricReader:  metricReader,
	}
}

func (c *telemetryCapture) snapshot(t *testing.T) ([]tracetest.SpanStub, metricdata.ResourceMetrics) {
	t.Helper()
	if err := c.traceProvider.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	var metrics metricdata.ResourceMetrics
	if err := c.metricReader.Collect(context.Background(), &metrics); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return c.traceExporter.GetSpans(), metrics
}

func TestTelemetryRedactionSuccessPreservesExecutionWithoutSecrets(t *testing.T) {
	capture := setupTelemetryCapture(t)

	const (
		secretHost   = "tenant-secret.internal"
		secretPath   = "/patients/secret-path"
		secretQuery  = "api_key=query-secret"
		secretHeader = "header-secret"
		secretToken  = "token-secret"
		secretReason = "retry-secret-reason"
	)

	var attempts atomic.Int32
	var traceparent string
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		traceparent = req.Header.Get("traceparent")
		if got := req.Header.Get("X-Scanner-Secret"); got != secretHeader {
			t.Fatalf("secret header changed in transport: %q", got)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer "+secretToken {
			t.Fatalf("Authorization = %q", got)
		}
		status := http.StatusServiceUnavailable
		if attempts.Add(1) == 2 {
			status = http.StatusOK
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: http.NoBody, Request: req}, nil
	})

	var allowCalls, recordCalls atomic.Int32
	breaker := &testBreaker{
		allowFn: func() error {
			allowCalls.Add(1)
			return nil
		},
		recordFn: func(success bool) {
			recordCalls.Add(1)
			if !success {
				t.Error("breaker recorded failure after successful retry")
			}
		},
	}

	client := New(
		WithHTTPClient(&http.Client{Transport: transport}),
		WithTimeout(time.Second),
		WithRetry(2, time.Nanosecond),
		WithRetryPolicy(func(rc RetryContext) RetryDecision {
			return RetryDecision{Retry: rc.Response != nil && rc.Response.StatusCode == http.StatusServiceUnavailable, Reason: secretReason}
		}),
		WithTokenSource(tokenSourceFunc(func(context.Context) (string, error) { return secretToken, nil })),
		WithBreaker(breaker),
		WithTelemetryRedaction(),
	)
	req, err := http.NewRequest(http.MethodGet, "https://"+secretHost+secretPath+"?"+secretQuery, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Scanner-Secret", secretHeader)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if attempts.Load() != 2 || allowCalls.Load() != 1 || recordCalls.Load() != 1 {
		t.Fatalf("attempts/allow/record = %d/%d/%d, want 2/1/1", attempts.Load(), allowCalls.Load(), recordCalls.Load())
	}
	if traceparent == "" {
		t.Fatal("TraceContext propagation was not injected")
	}

	spans, metrics := capture.snapshot(t)
	assertTelemetryOmits(t, spans, metrics, secretHost, secretPath, secretQuery, secretHeader, secretToken, secretReason)
	assertRedactedTelemetryAllowlist(t, spans, metrics)
	assertDurationMetricRecorded(t, metrics)
	clientSpan := findClientSpan(t, spans)
	if clientSpan.Name != http.MethodGet {
		t.Fatalf("span name = %q, want GET", clientSpan.Name)
	}
	if got := spanAttribute(clientSpan.Attributes, "http.status_code"); got != int64(http.StatusOK) {
		t.Fatalf("http.status_code = %#v", got)
	}
	for _, event := range clientSpan.Events {
		if event.Name == "retry" && spanAttribute(event.Attributes, "reason") != "retry" {
			t.Fatalf("redacted retry reason = %#v", spanAttribute(event.Attributes, "reason"))
		}
	}
}

func TestTelemetryRedactionFailureOmitsRawErrorChains(t *testing.T) {
	capture := setupTelemetryCapture(t)

	const (
		secretHost   = "failure-secret.internal"
		secretPath   = "/failure-secret-path"
		secretQuery  = "credential=failure-query-secret"
		secretHeader = "failure-header-secret"
		secretCause  = "raw-transport-cause-secret"
		secretToken  = "failure-token-secret"
	)

	var attempts atomic.Int32
	var propagated atomic.Bool
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts.Add(1)
		propagated.Store(req.Header.Get("traceparent") != "")
		return nil, &url.Error{Op: req.Method, URL: req.URL.String(), Err: errors.New(secretCause)}
	})
	var allowCalls, recordCalls atomic.Int32
	breaker := &testBreaker{
		allowFn: func() error { allowCalls.Add(1); return nil },
		recordFn: func(success bool) {
			recordCalls.Add(1)
			if success {
				t.Error("breaker recorded success for transport failure")
			}
		},
	}
	client := New(
		WithHTTPClient(&http.Client{Transport: transport}),
		WithRetry(2, time.Nanosecond),
		WithTokenSource(tokenSourceFunc(func(context.Context) (string, error) { return secretToken, nil })),
		WithBreaker(breaker),
		WithTelemetryRedaction(),
	)
	req, err := http.NewRequest(http.MethodGet, "https://"+secretHost+secretPath+"?"+secretQuery, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Scanner-Secret", secretHeader)
	_, err = client.Do(req)
	if err == nil {
		t.Fatal("expected transport error")
	}
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		t.Fatalf("returned error no longer exposes original semantics: %T", err)
	}
	if attempts.Load() != 2 || allowCalls.Load() != 1 || recordCalls.Load() != 1 || !propagated.Load() {
		t.Fatalf("attempts/allow/record/propagation = %d/%d/%d/%t", attempts.Load(), allowCalls.Load(), recordCalls.Load(), propagated.Load())
	}

	spans, metrics := capture.snapshot(t)
	assertTelemetryOmits(t, spans, metrics, secretHost, secretPath, secretQuery, secretHeader, secretCause, secretToken)
	assertRedactedTelemetryAllowlist(t, spans, metrics)
	assertDurationMetricRecorded(t, metrics)
	clientSpan := findClientSpan(t, spans)
	if clientSpan.Status.Description != telemetryErrorRequest {
		t.Fatalf("status description = %q, want %q", clientSpan.Status.Description, telemetryErrorRequest)
	}
	if got := spanAttribute(clientSpan.Attributes, "error.type"); got != telemetryErrorRequest {
		t.Fatalf("error.type = %#v, want %q", got, telemetryErrorRequest)
	}
}

func TestTelemetryRedactionSanitizesTokenAndBreakerFailures(t *testing.T) {
	tokenErr := errors.New("token-source-raw-secret")
	breakerErr := errors.New("breaker-allow-raw-secret")
	for _, tc := range []struct {
		name           string
		secret         string
		wantClass      string
		wantReturned   error
		tokenSource    TokenSource
		breakerFailure bool
	}{
		{
			name:         "token source",
			secret:       tokenErr.Error(),
			wantClass:    telemetryErrorTokenSource,
			wantReturned: tokenErr,
			tokenSource:  tokenSourceFunc(func(context.Context) (string, error) { return "", tokenErr }),
		},
		{
			name:           "breaker",
			secret:         breakerErr.Error(),
			wantClass:      telemetryErrorCircuitBreaker,
			wantReturned:   breakerErr,
			breakerFailure: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := setupTelemetryCapture(t)
			transportCalls := atomic.Int32{}
			allowCalls := atomic.Int32{}
			recordCalls := atomic.Int32{}
			breaker := &testBreaker{
				allowFn: func() error {
					allowCalls.Add(1)
					if tc.breakerFailure {
						return tc.wantReturned
					}
					return nil
				},
				recordFn: func(bool) { recordCalls.Add(1) },
			}
			client := New(
				WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					transportCalls.Add(1)
					return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
				})}),
				WithTokenSource(tc.tokenSource),
				WithBreaker(breaker),
				WithTelemetryRedaction(),
			)
			req, err := http.NewRequest(http.MethodGet, "https://safe.invalid/", nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Do(req)
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, tc.wantReturned) {
				t.Fatalf("returned error no longer preserves original cause: %v", err)
			}
			if transportCalls.Load() != 0 {
				t.Fatalf("transport calls = %d, want 0", transportCalls.Load())
			}
			wantAllowCalls := int32(0)
			if tc.breakerFailure {
				wantAllowCalls = 1
			}
			if allowCalls.Load() != wantAllowCalls || recordCalls.Load() != 0 {
				t.Fatalf("allow/record calls = %d/%d, want %d/0", allowCalls.Load(), recordCalls.Load(), wantAllowCalls)
			}
			spans, metrics := capture.snapshot(t)
			assertTelemetryOmits(t, spans, metrics, tc.secret)
			assertRedactedTelemetryAllowlist(t, spans, metrics)
			clientSpan := findClientSpan(t, spans)
			if clientSpan.Status.Description != tc.wantClass {
				t.Fatalf("status description = %q, want %q", clientSpan.Status.Description, tc.wantClass)
			}
		})
	}
}

func TestTelemetryRedactionPreservesTimeout(t *testing.T) {
	capture := setupTelemetryCapture(t)
	const secretURL = "https://timeout-secret.invalid/timeout-secret-path?token=timeout-secret-query"
	client := New(
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		})}),
		WithTimeout(20*time.Millisecond),
		WithTelemetryRedaction(),
	)
	req, err := http.NewRequest(http.MethodGet, secretURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = client.Do(req)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Do error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("redacted request ignored configured timeout: %s", elapsed)
	}

	spans, metrics := capture.snapshot(t)
	assertTelemetryOmits(t, spans, metrics, "timeout-secret.invalid", "timeout-secret-path", "timeout-secret-query")
	assertRedactedTelemetryAllowlist(t, spans, metrics)
	assertDurationMetricRecorded(t, metrics)
	clientSpan := findClientSpan(t, spans)
	if clientSpan.Status.Description != telemetryErrorDeadlineExceeded || spanAttribute(clientSpan.Attributes, "error.type") != telemetryErrorDeadlineExceeded {
		t.Fatalf("timeout classification = %q/%#v", clientSpan.Status.Description, spanAttribute(clientSpan.Attributes, "error.type"))
	}
}

func TestTelemetryRedactionClassifiesBodyRewindFailure(t *testing.T) {
	capture := setupTelemetryCapture(t)
	rewindErr := errors.New("body-rewind-raw-secret")
	var attempts atomic.Int32
	client := New(
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts.Add(1)
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: http.NoBody, Request: req}, nil
		})}),
		WithRetry(2, time.Nanosecond),
		WithTelemetryRedaction(),
	)
	req, err := http.NewRequest(http.MethodPost, "https://rewind-secret.invalid/rewind-secret-path", strings.NewReader("body-content-secret"))
	if err != nil {
		t.Fatal(err)
	}
	req.GetBody = func() (io.ReadCloser, error) { return nil, rewindErr }
	_, err = client.Do(req)
	if !errors.Is(err, ErrBodyNotRewindable) || !errors.Is(err, rewindErr) {
		t.Fatalf("returned error lost rewind semantics: %v", err)
	}
	if attempts.Load() != 1 {
		t.Fatalf("transport attempts = %d, want 1", attempts.Load())
	}

	spans, metrics := capture.snapshot(t)
	assertTelemetryOmits(t, spans, metrics, "rewind-secret.invalid", "rewind-secret-path", "body-content-secret", rewindErr.Error())
	assertRedactedTelemetryAllowlist(t, spans, metrics)
	assertDurationMetricRecorded(t, metrics)
	clientSpan := findClientSpan(t, spans)
	if clientSpan.Status.Description != telemetryErrorBodyNotRewindable || spanAttribute(clientSpan.Attributes, "error.type") != telemetryErrorBodyNotRewindable {
		t.Fatalf("body rewind classification = %q/%#v", clientSpan.Status.Description, spanAttribute(clientSpan.Attributes, "error.type"))
	}
}

func TestTelemetryRedactionPreservesRedirectsWithoutDestinationData(t *testing.T) {
	capture := setupTelemetryCapture(t)
	const (
		originHost = "redirect-origin-secret.invalid"
		originPath = "/redirect-origin-secret"
		targetHost = "redirect-target-secret.invalid"
		targetPath = "/redirect-target-secret"
	)
	var attempts atomic.Int32
	client := New(
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch attempts.Add(1) {
			case 1:
				if req.URL.Host != originHost || req.URL.Path != originPath {
					t.Fatalf("origin request = %s", req.URL)
				}
				return &http.Response{
					StatusCode: http.StatusFound,
					Header:     http.Header{"Location": {"https://" + targetHost + targetPath + "?token=redirect-query-secret"}},
					Body:       http.NoBody,
					Request:    req,
				}, nil
			case 2:
				if req.URL.Host != targetHost || req.URL.Path != targetPath {
					t.Fatalf("redirect target = %s", req.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody, Request: req}, nil
			default:
				t.Fatalf("unexpected redirect attempt %d", attempts.Load())
				return nil, errors.New("unexpected redirect attempt")
			}
		})}),
		WithTelemetryRedaction(),
	)
	req, err := http.NewRequest(http.MethodGet, "https://"+originHost+originPath+"?token=origin-query-secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if attempts.Load() != 2 || resp.Request.URL.Host != targetHost {
		t.Fatalf("redirect attempts/final host = %d/%q", attempts.Load(), resp.Request.URL.Host)
	}

	spans, metrics := capture.snapshot(t)
	assertTelemetryOmits(t, spans, metrics, originHost, originPath, targetHost, targetPath, "origin-query-secret", "redirect-query-secret")
	assertRedactedTelemetryAllowlist(t, spans, metrics)
	assertDurationMetricRecorded(t, metrics)
}

func TestTelemetryRedactionSanitizesUnknownMethodAndHTTPError(t *testing.T) {
	capture := setupTelemetryCapture(t)
	const secretMethod = "METHOD_CONTAINS_SECRET"
	client := New(
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != secretMethod {
				t.Fatalf("transport method = %q, want unchanged method", req.Method)
			}
			if req.Header.Get("traceparent") == "" {
				t.Fatal("TraceContext was not injected into an initially nil header map")
			}
			return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: http.NoBody, Request: req}, nil
		})}),
		WithTelemetryRedaction(),
	)
	req, err := http.NewRequest(secretMethod, "https://safe.invalid/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header = nil
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	spans, metrics := capture.snapshot(t)
	assertTelemetryOmits(t, spans, metrics, secretMethod)
	assertRedactedTelemetryAllowlist(t, spans, metrics)
	assertDurationMetricRecorded(t, metrics)
	clientSpan := findClientSpan(t, spans)
	if clientSpan.Name != "OTHER" || spanAttribute(clientSpan.Attributes, "http.method") != "OTHER" {
		t.Fatalf("redacted method = %q/%#v, want OTHER", clientSpan.Name, spanAttribute(clientSpan.Attributes, "http.method"))
	}
	if clientSpan.Status.Description != telemetryErrorHTTPStatus || spanAttribute(clientSpan.Attributes, "error.type") != telemetryErrorHTTPStatus {
		t.Fatalf("HTTP error classification = %q/%#v", clientSpan.Status.Description, spanAttribute(clientSpan.Attributes, "error.type"))
	}
}

func TestTelemetryRedactionIsOptIn(t *testing.T) {
	capture := setupTelemetryCapture(t)
	const host = "default-compatible.invalid"
	const path = "/default-compatible-path"
	client := New(WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody, Request: req}, nil
	})}))
	req, err := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	spans, metrics := capture.snapshot(t)
	clientSpan := findClientSpan(t, spans)
	if spanAttribute(clientSpan.Attributes, "url.path") != path || spanAttribute(clientSpan.Attributes, "server.address") != host {
		t.Fatalf("default span attributes changed: %#v", clientSpan.Attributes)
	}
	if !strings.Contains(fmt.Sprintf("%#v", metrics), host) {
		t.Fatal("default duration metric no longer includes server.address")
	}
}

func TestTelemetryRedactionOptInLeavesLegacyFailureTelemetryUnchanged(t *testing.T) {
	capture := setupTelemetryCapture(t)
	const (
		host  = "legacy-failure.invalid"
		path  = "/legacy-failure-path"
		cause = "legacy-failure-cause"
	)
	client := New(WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New(cause)
	})}))
	req, err := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(req)
	if err == nil || !strings.Contains(err.Error(), cause) {
		t.Fatalf("legacy returned error changed: %v", err)
	}
	spans, metrics := capture.snapshot(t)
	dump := fmt.Sprintf("%#v\n%#v", spans, metrics)
	for _, legacyValue := range []string{host, path, cause} {
		if !strings.Contains(dump, legacyValue) {
			t.Fatalf("legacy telemetry no longer contains %q", legacyValue)
		}
	}
}

func assertTelemetryOmits(t *testing.T, spans []tracetest.SpanStub, metrics metricdata.ResourceMetrics, secrets ...string) {
	t.Helper()
	dump := fmt.Sprintf("%#v\n%#v", spans, metrics)
	for _, secret := range secrets {
		if strings.Contains(dump, secret) {
			t.Fatalf("telemetry contains secret %q: %s", secret, dump)
		}
	}
}

func assertRedactedTelemetryAllowlist(t *testing.T, spans []tracetest.SpanStub, metrics metricdata.ResourceMetrics) {
	t.Helper()
	spanKeys := map[attribute.Key]bool{
		"attempt":          true,
		"error.type":       true,
		"from_state":       true,
		"http.method":      true,
		"http.status_code": true,
		"reason":           true,
		"success":          true,
		"to_state":         true,
	}
	for _, span := range spans {
		if span.SpanKind != trace.SpanKindClient {
			continue
		}
		for _, attr := range span.Attributes {
			if !spanKeys[attr.Key] {
				t.Fatalf("redacted span attribute is not allowlisted: %s", attr.Key)
			}
		}
		for _, event := range span.Events {
			for _, attr := range event.Attributes {
				if !spanKeys[attr.Key] {
					t.Fatalf("redacted span event attribute is not allowlisted: %s", attr.Key)
				}
			}
		}
		allowedStatusDescriptions := map[string]bool{
			"":                              true,
			telemetryErrorBodyNotRewindable: true,
			telemetryErrorCircuitBreaker:    true,
			telemetryErrorContextCanceled:   true,
			telemetryErrorDeadlineExceeded:  true,
			telemetryErrorHTTPStatus:        true,
			telemetryErrorRequest:           true,
			telemetryErrorTokenSource:       true,
		}
		if !allowedStatusDescriptions[span.Status.Description] {
			t.Fatalf("redacted span status description is not allowlisted: %q", span.Status.Description)
		}
	}
	metricKeys := map[attribute.Key]bool{
		"error.type":                true,
		"http.request.method":       true,
		"http.response.status_code": true,
	}
	for _, scope := range metrics.ScopeMetrics {
		for _, m := range scope.Metrics {
			histogram, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				continue
			}
			for _, point := range histogram.DataPoints {
				iter := point.Attributes.Iter()
				for iter.Next() {
					if attr := iter.Attribute(); !metricKeys[attr.Key] {
						t.Fatalf("redacted metric attribute is not allowlisted: %s", attr.Key)
					}
				}
			}
		}
	}
}

func assertDurationMetricRecorded(t *testing.T, metrics metricdata.ResourceMetrics) {
	t.Helper()
	for _, scope := range metrics.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "http.client.request.duration" {
				continue
			}
			if histogram, ok := m.Data.(metricdata.Histogram[float64]); ok && len(histogram.DataPoints) > 0 {
				return
			}
		}
	}
	t.Fatal("http.client.request.duration metric was not recorded")
}

func findClientSpan(t *testing.T, spans []tracetest.SpanStub) tracetest.SpanStub {
	t.Helper()
	for _, span := range spans {
		if span.SpanKind == trace.SpanKindClient {
			return span
		}
	}
	t.Fatal("client span not found")
	return tracetest.SpanStub{}
}

func spanAttribute(attrs []attribute.KeyValue, key attribute.Key) any {
	for _, attr := range attrs {
		if attr.Key != key {
			continue
		}
		switch attr.Value.Type() {
		case attribute.STRING:
			return attr.Value.AsString()
		case attribute.INT64:
			return attr.Value.AsInt64()
		case attribute.BOOL:
			return attr.Value.AsBool()
		}
	}
	return nil
}
