Date Created: 2026-02-05T174301+0100
TOTAL_SCORE: 82/100

**Summary**
Overall unit test coverage is strong for mainline behavior, but there are consistent gaps in error paths, serialization edge cases, and internal helper branches (especially around cardinality guards, context cancellation, and encoder failures). The diffs below focus on those untested branches without changing production code.

**Score Rationale**
- Mainline behaviors are well covered across most packages.
- Multiple defensive/error branches and helper methods are still untested.
- A few failure modes are effectively untestable without injection hooks.

**Key Untested Areas Addressed**
- `config/config.go`: unexported field skip, empty `[]string` handling, unsupported type errors.
- `errors/problem.go`: reserved extension keys, unknown status/type handling, `FromError(nil)`.
- `health/handler.go`: JSON encode failure path.
- `call/breaker.go`: half-open failure transition.
- `call/retry.go`: pre-canceled context and backoff interruption path.
- `otel/otel.go`: sampler helpers.
- `grpckit/interceptors.go`: error branches in stream metrics/logging and metadata carrier helpers.
- `secval/secval.go`: non-ASCII/non-printable key stripping and deep array depth guard.
- `work/work.go`: Errors helpers, empty race, stream cancellation and error propagation.
- `httpkit/*`: responseWriter edge cases, JSONProblem nil/error paths, 5xx span status, recovery after headers written.
- `logz/logz.go`: multi-group trace reconstruction loop.
- `metrics/metrics.go`: overflow warning once.
- `guard/*`: limiter sweep, timeoutWriter idempotency, problem encoding errors.

**Patch-Ready Diffs**
`config/config_test.go`
```diff
diff --git a/config/config_test.go b/config/config_test.go
index 2f3c6d1..b5aa8c1 100644
--- a/config/config_test.go
+++ b/config/config_test.go
@@
-import (
-	"os"
-	"testing"
-	"time"
-
-	chassis "github.com/ai8future/chassis-go"
-)
+import (
+	"os"
+	"reflect"
+	"strings"
+	"testing"
+	"time"
+
+	chassis "github.com/ai8future/chassis-go"
+)
@@
 type mixedConfig struct {
 	Name    string `env:"TEST_NAME"`
 	Label   string // no env tag — should be skipped
 	Visible bool   `env:"TEST_VISIBLE" default:"true"`
 }
+
+type withUnexported struct {
+	Public string `env:"TEST_PUBLIC"`
+	secret string `env:"TEST_SECRET"`
+}
@@
 func TestMustLoad_SkipsFieldsWithoutEnvTag(t *testing.T) {
 	t.Setenv("TEST_NAME", "hello")
@@
 	if cfg.Visible != true {
 		t.Errorf("Visible = %v, want true (from default)", cfg.Visible)
 	}
 }
+
+func TestMustLoad_SkipsUnexportedFields(t *testing.T) {
+	t.Setenv("TEST_PUBLIC", "ok")
+	t.Setenv("TEST_SECRET", "hidden")
+
+	cfg := MustLoad[withUnexported]()
+
+	if cfg.Public != "ok" {
+		t.Errorf("Public = %q, want %q", cfg.Public, "ok")
+	}
+	if cfg.secret != "" {
+		t.Errorf("secret = %q, want empty", cfg.secret)
+	}
+}
@@
 func TestMustLoad_InvalidFloat(t *testing.T) {
 	defer func() {
 		r := recover()
@@
 	_ = MustLoad[cfg]()
 }
+
+func TestSetField_EmptyStringSlice(t *testing.T) {
+	var tags []string
+	if err := setField(reflect.ValueOf(&tags).Elem(), ""); err != nil {
+		t.Fatalf("setField returned error: %v", err)
+	}
+	if tags == nil || len(tags) != 0 {
+		t.Fatalf("tags = %v, want empty slice", tags)
+	}
+}
+
+func TestSetField_UnsupportedType(t *testing.T) {
+	var v struct{ A int }
+	err := setField(reflect.ValueOf(&v).Elem(), "ignored")
+	if err == nil {
+		t.Fatal("expected error for unsupported field type")
+	}
+	if !strings.Contains(err.Error(), "unsupported field type") {
+		t.Fatalf("unexpected error: %v", err)
+	}
+}
```

`errors/errors_test.go`
```diff
diff --git a/errors/errors_test.go b/errors/errors_test.go
index 69f3f48..6b0a22c 100644
--- a/errors/errors_test.go
+++ b/errors/errors_test.go
@@
 func TestFromErrorGenericError(t *testing.T) {
 	original := errors.New("something broke")
 	got := FromError(original)
@@
 	if !errors.Is(got, original) {
 		t.Error("FromError should chain original via Unwrap")
 	}
 }
+
+func TestFromErrorNil(t *testing.T) {
+	if FromError(nil) != nil {
+		t.Fatal("expected nil from FromError(nil)")
+	}
+}
@@
 func TestProblemDetailJSON(t *testing.T) {
 	err := NotFoundError("user not found")
@@
 	if got["instance"] != "/api/users/99" {
 		t.Errorf("instance = %v, want %q", got["instance"], "/api/users/99")
 	}
 }
+
+func TestProblemDetail_ReservedExtensionKeysIgnored(t *testing.T) {
+	err := ValidationError("bad input").
+		WithDetail("type", "evil").
+		WithDetail("status", "999").
+		WithDetail("instance", "/evil").
+		WithDetail("custom", "ok")
+	req := httptest.NewRequest("GET", "/safe", nil)
+	pd := err.ProblemDetail(req)
+
+	data, marshalErr := json.Marshal(pd)
+	if marshalErr != nil {
+		t.Fatalf("json.Marshal failed: %v", marshalErr)
+	}
+	var got map[string]any
+	if unmarshalErr := json.Unmarshal(data, &got); unmarshalErr != nil {
+		t.Fatalf("json.Unmarshal failed: %v", unmarshalErr)
+	}
+	if got["type"] != "https://chassis.ai8future.com/errors/validation" {
+		t.Errorf("type = %v", got["type"])
+	}
+	if got["status"].(float64) != 400 {
+		t.Errorf("status = %v, want 400", got["status"])
+	}
+	if got["instance"] != "/safe" {
+		t.Errorf("instance = %v, want %q", got["instance"], "/safe")
+	}
+	if got["custom"] != "ok" {
+		t.Errorf("custom = %v, want %q", got["custom"], "ok")
+	}
+}
+
+func TestProblemDetail_UnknownStatusAndCustomType(t *testing.T) {
+	err := &ServiceError{Message: "teapot", HTTPCode: http.StatusTeapot, GRPCCode: codes.Unknown}
+	pd := err.ProblemDetail(nil)
+	if pd.Type != typeBaseURI+"unknown" {
+		t.Errorf("Type = %q, want %q", pd.Type, typeBaseURI+"unknown")
+	}
+	if pd.Title != http.StatusText(http.StatusTeapot) {
+		t.Errorf("Title = %q, want %q", pd.Title, http.StatusText(http.StatusTeapot))
+	}
+	if pd.Instance != "" {
+		t.Errorf("Instance = %q, want empty", pd.Instance)
+	}
+
+	err.WithType("https://example.com/custom")
+	pd2 := err.ProblemDetail(httptest.NewRequest("GET", "/x", nil))
+	if pd2.Type != "https://example.com/custom" {
+		t.Errorf("Type = %q, want custom override", pd2.Type)
+	}
+}
```

`health/health_test.go`
```diff
diff --git a/health/health_test.go b/health/health_test.go
index 6b7da3b..9c7df9d 100644
--- a/health/health_test.go
+++ b/health/health_test.go
@@
 func TestMain(m *testing.M) {
 	chassis.RequireMajor(4)
 	os.Exit(m.Run())
 }
+
+type errWriter struct {
+	header http.Header
+	code   int
+}
+
+func (w *errWriter) Header() http.Header {
+	if w.header == nil {
+		w.header = make(http.Header)
+	}
+	return w.header
+}
+
+func (w *errWriter) WriteHeader(code int) {
+	w.code = code
+}
+
+func (w *errWriter) Write(p []byte) (int, error) {
+	return 0, errors.New("write failed")
+}
@@
 func TestHandler_Unhealthy(t *testing.T) {
 	checks := map[string]Check{
 		"db":    func(ctx context.Context) error { return errors.New("gone") },
@@
 	if !foundFailure {
 		t.Error("expected to find unhealthy db check with error 'gone'")
 	}
 }
+
+func TestHandler_EncodeErrorDoesNotPanic(t *testing.T) {
+	checks := map[string]Check{
+		"db": func(ctx context.Context) error { return nil },
+	}
+	w := &errWriter{}
+	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
+
+	Handler(checks).ServeHTTP(w, req)
+
+	if w.code != http.StatusOK {
+		t.Fatalf("expected status 200, got %d", w.code)
+	}
+}
```

`call/breaker_test.go`
```diff
diff --git a/call/breaker_test.go b/call/breaker_test.go
index 0d51e0a..ce6a104 100644
--- a/call/breaker_test.go
+++ b/call/breaker_test.go
@@
 func TestStateName(t *testing.T) {
 	cases := []struct {
@@
 	}
 }
+
+func TestCircuitBreaker_HalfOpenFailureReopens(t *testing.T) {
+	name := uniqueBreakerName()
+	cb := GetBreaker(name, 1, 20*time.Millisecond)
+	cb.resetForTest()
+
+	cb.Record(false)
+	if cb.State() != StateOpen {
+		t.Fatalf("expected StateOpen, got %d", cb.State())
+	}
+
+	time.Sleep(25 * time.Millisecond)
+	if err := cb.Allow(); err != nil {
+		t.Fatalf("expected probe allow, got %v", err)
+	}
+
+	cb.Record(false)
+	if cb.State() != StateOpen {
+		t.Fatalf("expected StateOpen after failed probe, got %d", cb.State())
+	}
+}
```

`call/call_test.go`
```diff
diff --git a/call/call_test.go b/call/call_test.go
index 39f9a72..38aa28b 100644
--- a/call/call_test.go
+++ b/call/call_test.go
@@
 var breakerSeq atomic.Int64
 
 func uniqueBreakerName() string {
 	return fmt.Sprintf("test-breaker-%d", breakerSeq.Add(1))
 }
+
+type testBreaker struct {
+	allowCalls  atomic.Int32
+	recordCalls atomic.Int32
+	lastSuccess atomic.Bool
+}
+
+func (b *testBreaker) Allow() error {
+	b.allowCalls.Add(1)
+	return nil
+}
+
+func (b *testBreaker) Record(success bool) {
+	b.recordCalls.Add(1)
+	b.lastSuccess.Store(success)
+}
@@
 func TestSingletonBreakers(t *testing.T) {
 	name := uniqueBreakerName()
 	b1 := GetBreaker(name, 5, time.Second)
@@
 	if b1 != b2 {
 		t.Fatal("expected same breaker instance for same name")
 	}
 }
+
+func TestWithBreakerUsesCustomBreaker(t *testing.T) {
+	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
+		w.WriteHeader(http.StatusOK)
+	}))
+	defer srv.Close()
+
+	b := &testBreaker{}
+	c := New(WithTimeout(5*time.Second), WithBreaker(b))
+	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
+
+	resp, err := c.Do(req)
+	if err != nil {
+		t.Fatalf("unexpected error: %v", err)
+	}
+	resp.Body.Close()
+
+	if b.allowCalls.Load() != 1 {
+		t.Fatalf("Allow called %d times, want 1", b.allowCalls.Load())
+	}
+	if b.recordCalls.Load() != 1 {
+		t.Fatalf("Record called %d times, want 1", b.recordCalls.Load())
+	}
+	if !b.lastSuccess.Load() {
+		t.Fatalf("expected Record to be called with success=true")
+	}
+}
```

`call/retry_test.go`
```diff
diff --git a/call/retry_test.go b/call/retry_test.go
index 36cf0f8..d2f243e 100644
--- a/call/retry_test.go
+++ b/call/retry_test.go
@@
 import (
 	"context"
 	"errors"
 	"io"
 	"net/http"
+	"strings"
 	"sync/atomic"
 	"testing"
 	"time"
 )
@@
 func TestRetrier_BackoffHonorsContextCancel(t *testing.T) {
 	r := &Retrier{BaseDelay: 200 * time.Millisecond}
 	ctx, cancel := context.WithCancel(context.Background())
@@
 	if time.Since(start) > 100*time.Millisecond {
 		t.Fatalf("backoff returned too slowly after cancel")
 	}
 }
+
+func TestRetrier_DoReturnsContextErrorBeforeAttempt(t *testing.T) {
+	ctx, cancel := context.WithCancel(context.Background())
+	cancel()
+
+	var calls atomic.Int32
+	r := &Retrier{MaxAttempts: 3, BaseDelay: 1 * time.Millisecond}
+	_, err := r.Do(ctx, func() (*http.Response, error) {
+		calls.Add(1)
+		return nil, errors.New("should not be called")
+	})
+
+	if !errors.Is(err, context.Canceled) {
+		t.Fatalf("expected context.Canceled, got %v", err)
+	}
+	if calls.Load() != 0 {
+		t.Fatalf("expected 0 attempts, got %d", calls.Load())
+	}
+}
+
+func TestRetrier_DoBackoffInterruptedByContext(t *testing.T) {
+	ctx, cancel := context.WithCancel(context.Background())
+	r := &Retrier{MaxAttempts: 2, BaseDelay: 200 * time.Millisecond}
+
+	var calls atomic.Int32
+	go func() {
+		time.Sleep(10 * time.Millisecond)
+		cancel()
+	}()
+
+	_, err := r.Do(ctx, func() (*http.Response, error) {
+		calls.Add(1)
+		return &http.Response{
+			StatusCode: http.StatusBadGateway,
+			Body:       io.NopCloser(strings.NewReader("boom")),
+		}, nil
+	})
+
+	if !errors.Is(err, context.Canceled) {
+		t.Fatalf("expected context.Canceled, got %v", err)
+	}
+	if calls.Load() != 1 {
+		t.Fatalf("expected 1 attempt before backoff cancellation, got %d", calls.Load())
+	}
+}
```

`otel/otel_test.go`
```diff
diff --git a/otel/otel_test.go b/otel/otel_test.go
index 8a9c9c4..f5d2c12 100644
--- a/otel/otel_test.go
+++ b/otel/otel_test.go
@@
 import (
 	"context"
 	"testing"
 
 	chassis "github.com/ai8future/chassis-go"
 	"github.com/ai8future/chassis-go/otel"
+	sdktrace "go.opentelemetry.io/otel/sdk/trace"
 	"go.opentelemetry.io/otel/trace"
 )
@@
 func TestDetachContextWithNoSpanReturnsBackground(t *testing.T) {
 	detached := otel.DetachContext(context.Background())
 	sc := trace.SpanContextFromContext(detached)
 	if sc.IsValid() {
 		t.Fatal("expected invalid span context from empty parent")
 	}
 }
+
+func TestAlwaysSampleSampler(t *testing.T) {
+	sampler := otel.AlwaysSample()
+	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
+	res := sampler.ShouldSample(sdktrace.SamplingParameters{TraceID: traceID})
+	if res.Decision != sdktrace.RecordAndSample {
+		t.Fatalf("expected RecordAndSample, got %v", res.Decision)
+	}
+}
+
+func TestRatioSampleSampler(t *testing.T) {
+	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
+
+	sampler := otel.RatioSample(0)
+	res := sampler.ShouldSample(sdktrace.SamplingParameters{TraceID: traceID})
+	if res.Decision != sdktrace.Drop {
+		t.Fatalf("expected Drop, got %v", res.Decision)
+	}
+
+	sampler = otel.RatioSample(1)
+	res = sampler.ShouldSample(sdktrace.SamplingParameters{TraceID: traceID})
+	if res.Decision != sdktrace.RecordAndSample {
+		t.Fatalf("expected RecordAndSample, got %v", res.Decision)
+	}
+}
```

`grpckit/grpckit_test.go`
```diff
diff --git a/grpckit/grpckit_test.go b/grpckit/grpckit_test.go
index 62bcda8..89d1b01 100644
--- a/grpckit/grpckit_test.go
+++ b/grpckit/grpckit_test.go
@@
 func TestStreamLogging(t *testing.T) {
 	var buf bytes.Buffer
 	logger := newTestLogger(&buf)
@@
 	if !strings.Contains(log, "/test.Service/StreamMethod") {
 		t.Errorf("expected log to contain method name, got: %s", log)
 	}
 }
+
+func TestStreamLogging_WithError(t *testing.T) {
+	var buf bytes.Buffer
+	logger := newTestLogger(&buf)
+
+	interceptor := StreamLogging(logger)
+
+	info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamFail"}
+	ss := &mockServerStream{ctx: context.Background()}
+	handler := func(srv any, stream grpc.ServerStream) error {
+		return status.Error(codes.NotFound, "missing")
+	}
+
+	err := interceptor(nil, ss, info, handler)
+	if err == nil {
+		t.Fatal("expected error, got nil")
+	}
+
+	log := buf.String()
+	if !strings.Contains(log, "error") {
+		t.Errorf("expected log to contain error field, got: %s", log)
+	}
+}
@@
 func TestUnaryMetrics(t *testing.T) {
 	interceptor := UnaryMetrics()
@@
 	if resp != "ok" {
 		t.Fatalf("expected resp 'ok', got %v", resp)
 	}
 	// UnaryMetrics now records an OTel histogram rather than logging.
 	// We verify it doesn't panic and returns correctly. Full metric
 	// verification requires an OTel SDK test meter.
 }
+
+func TestUnaryMetrics_WithError(t *testing.T) {
+	interceptor := UnaryMetrics()
+
+	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/MetricsFail"}
+	handler := func(ctx context.Context, req any) (any, error) {
+		return nil, status.Error(codes.DeadlineExceeded, "timeout")
+	}
+
+	_, err := interceptor(context.Background(), "req", info, handler)
+	if err == nil {
+		t.Fatal("expected error, got nil")
+	}
+	if status.Code(err) != codes.DeadlineExceeded {
+		t.Fatalf("expected DeadlineExceeded, got %v", status.Code(err))
+	}
+}
@@
 func TestStreamMetrics(t *testing.T) {
 	interceptor := StreamMetrics()
@@
 	if err != nil {
 		t.Fatalf("unexpected error: %v", err)
 	}
 	// StreamMetrics now records an OTel histogram rather than logging.
 }
+
+func TestStreamMetrics_WithError(t *testing.T) {
+	interceptor := StreamMetrics()
+
+	info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMetricsFail"}
+	ss := &mockServerStream{ctx: context.Background()}
+	handler := func(srv any, stream grpc.ServerStream) error {
+		return status.Error(codes.PermissionDenied, "denied")
+	}
+
+	err := interceptor(nil, ss, info, handler)
+	if err == nil {
+		t.Fatal("expected error, got nil")
+	}
+	if status.Code(err) != codes.PermissionDenied {
+		t.Fatalf("expected PermissionDenied, got %v", status.Code(err))
+	}
+}
```

`grpckit/interceptors_test.go`
```diff
diff --git a/grpckit/interceptors_test.go b/grpckit/interceptors_test.go
index 3c28f8f..8f8cfea 100644
--- a/grpckit/interceptors_test.go
+++ b/grpckit/interceptors_test.go
@@
 import (
 	"context"
+	"sort"
 	"testing"
@@
 	"google.golang.org/grpc/metadata"
 )
@@
 func TestStreamTracingCreatesSpan(t *testing.T) {
 	exporter := tracetest.NewInMemoryExporter()
@@
 	if v, ok := attrs["rpc.method"]; !ok || v != "/api.v1.UserService/ListUsers" {
 		t.Errorf("expected rpc.method='/api.v1.UserService/ListUsers', got %q (present=%v)", v, ok)
 	}
 }
+
+func TestMetadataCarrierKeysGetSet(t *testing.T) {
+	md := metadata.Pairs("a", "1", "b", "2")
+	c := metadataCarrier{md: md}
+	if c.Get("missing") != "" {
+		t.Fatalf("expected empty for missing key")
+	}
+	if c.Get("a") != "1" {
+		t.Fatalf("expected a=1, got %q", c.Get("a"))
+	}
+	c.Set("c", "3")
+
+	keys := c.Keys()
+	sort.Strings(keys)
+	if len(keys) != 3 {
+		t.Fatalf("expected 3 keys, got %v", keys)
+	}
+	if keys[0] != "a" || keys[1] != "b" || keys[2] != "c" {
+		t.Fatalf("unexpected keys: %v", keys)
+	}
+}
+
+func TestExtractTraceContext_NoMetadataReturnsSameContext(t *testing.T) {
+	ctx := context.Background()
+	got := extractTraceContext(ctx)
+	if got != ctx {
+		t.Fatalf("expected same context when no metadata is present")
+	}
+}
```

`secval/secval_test.go`
```diff
diff --git a/secval/secval_test.go b/secval/secval_test.go
index ffa0a50..4f5dbea 100644
--- a/secval/secval_test.go
+++ b/secval/secval_test.go
@@
 func TestNestedDangerousKey(t *testing.T) {
 	err := ValidateJSON([]byte(`{"data": {"inner": {"exec": true}}}`))
 	if !errors.Is(err, ErrDangerousKey) {
 		t.Fatalf("expected ErrDangerousKey nested, got %v", err)
 	}
 }
+
+func TestDangerousKeyStripsNonPrintable(t *testing.T) {
+	err := ValidateJSON([]byte(`{"e\u0000val": true}`))
+	if !errors.Is(err, ErrDangerousKey) {
+		t.Fatalf("expected ErrDangerousKey for stripped key, got %v", err)
+	}
+}
+
+func TestArrayDepthExceeded(t *testing.T) {
+	json := strings.Repeat("[", 21) + "0" + strings.Repeat("]", 21)
+	err := ValidateJSON([]byte(json))
+	if !errors.Is(err, ErrNestingDepth) {
+		t.Fatalf("expected ErrNestingDepth, got %v", err)
+	}
+}
```

`work/work_test.go`
```diff
diff --git a/work/work_test.go b/work/work_test.go
index 3de0d1c..0e2c2d1 100644
--- a/work/work_test.go
+++ b/work/work_test.go
@@
 func TestMap_EmptySlice(t *testing.T) {
 	results, err := Map(context.Background(), []int{}, func(_ context.Context, n int) (int, error) {
 		return n, nil
 	})
@@
 	if len(results) != 0 {
 		t.Fatalf("expected empty results, got %d", len(results))
 	}
 }
+
+func TestErrorsErrorAndUnwrap(t *testing.T) {
+	e1 := errors.New("one")
+	e2 := errors.New("two")
+	err := &Errors{Failures: []Failure{{Index: 0, Err: e1}, {Index: 2, Err: e2}}}
+
+	if err.Error() != "2 task(s) failed" {
+		t.Fatalf("Error() = %q", err.Error())
+	}
+
+	unwrapped := err.Unwrap()
+	if len(unwrapped) != 2 {
+		t.Fatalf("Unwrap len = %d, want 2", len(unwrapped))
+	}
+	if !errors.Is(unwrapped[0], e1) || !errors.Is(unwrapped[1], e2) {
+		t.Fatalf("Unwrap did not preserve errors")
+	}
+}
@@
 func TestRace_ContextCancelled(t *testing.T) {
 	var cancelled atomic.Bool
@@
 	if !cancelled.Load() {
 		t.Fatal("expected loser to observe context cancellation")
 	}
 }
+
+func TestRace_NoTasksReturnsZero(t *testing.T) {
+	result, err := Race[string](context.Background())
+	if err != nil {
+		t.Fatalf("unexpected error: %v", err)
+	}
+	if result != "" {
+		t.Fatalf("expected zero value, got %q", result)
+	}
+}
@@
 func TestStream_ClosedChannel(t *testing.T) {
 	in := make(chan int)
 	close(in)
@@
 	if count != 0 {
 		t.Fatalf("expected 0 results from closed channel, got %d", count)
 	}
 }
+
+func TestStream_ContextCancelledBeforeStart(t *testing.T) {
+	ctx, cancel := context.WithCancel(context.Background())
+	cancel()
+
+	in := make(chan int, 3)
+	in <- 1
+	in <- 2
+	in <- 3
+	close(in)
+
+	out := Stream(ctx, in, func(_ context.Context, n int) (int, error) {
+		return n, nil
+	})
+
+	count := 0
+	for range out {
+		count++
+	}
+	if count != 0 {
+		t.Fatalf("expected 0 results after early cancel, got %d", count)
+	}
+}
+
+func TestStream_ReportsErrors(t *testing.T) {
+	in := make(chan int, 1)
+	in <- 42
+	close(in)
+
+	boom := errors.New("boom")
+	out := Stream(context.Background(), in, func(_ context.Context, _ int) (int, error) {
+		return 0, boom
+	})
+
+	res := <-out
+	if !errors.Is(res.Err, boom) {
+		t.Fatalf("expected error %v, got %v", boom, res.Err)
+	}
+}
```

`httpkit/httpkit_test.go`
```diff
diff --git a/httpkit/httpkit_test.go b/httpkit/httpkit_test.go
index 414d0de..2d1c4ac 100644
--- a/httpkit/httpkit_test.go
+++ b/httpkit/httpkit_test.go
@@
 import (
 	"bytes"
 	"context"
 	"encoding/json"
+	stderrors "errors"
 	"log/slog"
 	"net/http"
 	"net/http/httptest"
 	"os"
 	"strings"
 	"testing"
 
 	chassis "github.com/ai8future/chassis-go"
 	"github.com/ai8future/chassis-go/errors"
 	otelapi "go.opentelemetry.io/otel"
+	otelcodes "go.opentelemetry.io/otel/codes"
 	"go.opentelemetry.io/otel/propagation"
 	sdktrace "go.opentelemetry.io/otel/sdk/trace"
 	"go.opentelemetry.io/otel/sdk/trace/tracetest"
 )
@@
 func TestTracingMiddlewarePropagatesIncomingTrace(t *testing.T) {
 	exporter := tracetest.NewInMemoryExporter()
 	tp := sdktrace.NewTracerProvider(
@@
 	if traceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
 		t.Fatalf("expected trace ID %q, got %q", "4bf92f3577b34da6a3ce929d0e0e4736", traceID)
 	}
 }
+
+func TestTracingMiddlewareSetsErrorStatusOn5xx(t *testing.T) {
+	exporter := tracetest.NewInMemoryExporter()
+	tp := sdktrace.NewTracerProvider(
+		sdktrace.WithSyncer(exporter),
+	)
+	defer func() { _ = tp.Shutdown(context.Background()) }()
+	otelapi.SetTracerProvider(tp)
+
+	handler := Tracing()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		w.WriteHeader(http.StatusInternalServerError)
+	}))
+
+	req := httptest.NewRequest(http.MethodGet, "/oops", nil)
+	rec := httptest.NewRecorder()
+	handler.ServeHTTP(rec, req)
+
+	spans := exporter.GetSpans()
+	if len(spans) != 1 {
+		t.Fatalf("expected 1 span, got %d", len(spans))
+	}
+	if spans[0].Status.Code != otelcodes.Error {
+		t.Fatalf("expected span status Error, got %v", spans[0].Status.Code)
+	}
+}
@@
 func TestJSONProblemWritesRFC9457(t *testing.T) {
 	req := httptest.NewRequest("POST", "/api/users", nil)
 	rec := httptest.NewRecorder()
@@
 	if pd["instance"] != "/api/users" {
 		t.Errorf("instance = %v", pd["instance"])
 	}
 }
+
+func TestJSONProblemNilError(t *testing.T) {
+	rec := httptest.NewRecorder()
+	req := httptest.NewRequest(http.MethodGet, "/err", nil)
+
+	JSONProblem(rec, req, nil)
+
+	if rec.Code != http.StatusInternalServerError {
+		t.Fatalf("expected status 500, got %d", rec.Code)
+	}
+	var pd map[string]any
+	if err := json.NewDecoder(rec.Body).Decode(&pd); err != nil {
+		t.Fatalf("failed to decode response: %v", err)
+	}
+	if pd["type"] != "https://chassis.ai8future.com/errors/internal" {
+		t.Fatalf("expected internal type URI, got %v", pd["type"])
+	}
+}
+
+func TestJSONError_UnknownStatusUsesInternal(t *testing.T) {
+	rec := httptest.NewRecorder()
+	req := httptest.NewRequest(http.MethodGet, "/err", nil)
+
+	JSONError(rec, req, http.StatusTeapot, "short and stout")
+
+	if rec.Code != http.StatusInternalServerError {
+		t.Fatalf("expected status 500, got %d", rec.Code)
+	}
+}
+
+func TestJSONProblem_EncodeError(t *testing.T) {
+	w := &errWriter{}
+	req := httptest.NewRequest(http.MethodGet, "/err", nil)
+
+	JSONProblem(w, req, errors.ValidationError("bad"))
+
+	if w.code != http.StatusBadRequest {
+		t.Fatalf("expected status 400, got %d", w.code)
+	}
+	if ct := w.header.Get("Content-Type"); ct != "application/problem+json" {
+		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
+	}
+}
+
+func TestResponseWriterWriteHeaderOnlyOnce(t *testing.T) {
+	rec := httptest.NewRecorder()
+	rw := &responseWriter{ResponseWriter: rec, statusCode: http.StatusOK}
+
+	rw.WriteHeader(http.StatusCreated)
+	rw.WriteHeader(http.StatusAccepted)
+
+	if rw.statusCode != http.StatusCreated {
+		t.Fatalf("statusCode = %d, want %d", rw.statusCode, http.StatusCreated)
+	}
+}
+
+func TestResponseWriterUnwrap(t *testing.T) {
+	rec := httptest.NewRecorder()
+	rw := &responseWriter{ResponseWriter: rec, statusCode: http.StatusOK}
+
+	if rw.Unwrap() != rec {
+		t.Fatal("expected Unwrap to return underlying ResponseWriter")
+	}
+}
+
+func TestRecovery_SkipsErrorBodyAfterHeadersWritten(t *testing.T) {
+	var buf bytes.Buffer
+	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
+
+	rec := httptest.NewRecorder()
+	rw := &responseWriter{ResponseWriter: rec, statusCode: http.StatusOK}
+	handler := Recovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		w.WriteHeader(http.StatusTeapot)
+		panic("boom")
+	}))
+
+	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
+	handler.ServeHTTP(rw, req)
+
+	if rec.Code != http.StatusTeapot {
+		t.Fatalf("expected status 418, got %d", rec.Code)
+	}
+}
+
+type errWriter struct {
+	header http.Header
+	code   int
+}
+
+func (w *errWriter) Header() http.Header {
+	if w.header == nil {
+		w.header = make(http.Header)
+	}
+	return w.header
+}
+
+func (w *errWriter) WriteHeader(code int) {
+	w.code = code
+}
+
+func (w *errWriter) Write(p []byte) (int, error) {
+	return 0, stderrors.New("write failed")
+}
```

`logz/logz_test.go`
```diff
diff --git a/logz/logz_test.go b/logz/logz_test.go
index b83c36a..153b7ef 100644
--- a/logz/logz_test.go
+++ b/logz/logz_test.go
@@
 func TestWithGroupPreservesTraceHandler(t *testing.T) {
 	var buf bytes.Buffer
 	logger := newTestLogger(&buf, "info")
@@
 	if v, _ := grp["k"].(string); v != "v" {
 		t.Errorf("expected grp.k=%q, got %v", "v", grp["k"])
 	}
 }
+
+func TestWithNestedGroupsPreservesTraceIDAtTopLevel(t *testing.T) {
+	var buf bytes.Buffer
+	logger := newTestLogger(&buf, "info")
+	logger = logger.WithGroup("outer").WithGroup("inner")
+
+	traceIDHex, _ := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
+	spanIDHex, _ := trace.SpanIDFromHex("b7ad6b7169203331")
+	sc := trace.NewSpanContext(trace.SpanContextConfig{
+		TraceID:    traceIDHex,
+		SpanID:     spanIDHex,
+		TraceFlags: trace.FlagsSampled,
+	})
+	ctx := trace.ContextWithSpanContext(context.Background(), sc)
+	logger.InfoContext(ctx, "nested", "k", "v")
+
+	var entry map[string]interface{}
+	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
+		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
+	}
+	if v, _ := entry["trace_id"].(string); v != "0af7651916cd43dd8448eb211c80319c" {
+		t.Errorf("expected trace_id=%q, got %v", "0af7651916cd43dd8448eb211c80319c", entry["trace_id"])
+	}
+	outer, ok := entry["outer"].(map[string]interface{})
+	if !ok {
+		t.Fatalf("expected 'outer' group in output, got: %v", entry)
+	}
+	inner, ok := outer["inner"].(map[string]interface{})
+	if !ok {
+		t.Fatalf("expected 'inner' group in output, got: %v", outer)
+	}
+	if v, _ := inner["k"].(string); v != "v" {
+		t.Errorf("expected inner.k=%q, got %v", "v", inner["k"])
+	}
+}
```

`metrics/metrics_test.go`
```diff
diff --git a/metrics/metrics_test.go b/metrics/metrics_test.go
index c4b42c9..f3b6a72 100644
--- a/metrics/metrics_test.go
+++ b/metrics/metrics_test.go
@@
 import (
+	"bytes"
 	"context"
 	"fmt"
 	"io"
 	"log/slog"
 	"os"
+	"strings"
 	"testing"
@@
 func TestMetricPrefix(t *testing.T) {
 	collect := setupTestMeter(t)
 	rec := New("custom_prefix", nil)
 	rec.RecordRequest(context.Background(), "GET", "200", 10, 100)
@@
 	if m := findMetric(rm, "custom_prefix_requests_total"); m == nil {
 		t.Error("expected custom_prefix_requests_total in collected metrics")
 	}
 }
+
+func TestCardinalityOverflowWarnsOnce(t *testing.T) {
+	var buf bytes.Buffer
+	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
+	rec := New("cardsvc", logger)
+
+	for i := 0; i < MaxLabelCombinations; i++ {
+		combo := fmt.Sprintf("GET\x00status_%d", i)
+		rec.checkCardinality("requests_total", combo)
+	}
+
+	rec.RecordRequest(context.Background(), "GET", "overflow1", 1, 1)
+	rec.RecordRequest(context.Background(), "GET", "overflow2", 1, 1)
+
+	count := strings.Count(buf.String(), "metrics cardinality limit reached")
+	if count != 1 {
+		t.Fatalf("expected 1 overflow warning, got %d", count)
+	}
+}
```

`guard/ratelimit_internal_test.go`
```diff
diff --git a/guard/ratelimit_internal_test.go b/guard/ratelimit_internal_test.go
new file mode 100644
index 0000000..2f8b5e9
--- /dev/null
+++ b/guard/ratelimit_internal_test.go
@@
+package guard
+
+import (
+	"testing"
+	"time"
+)
+
+func TestLimiterSweepsStaleBuckets(t *testing.T) {
+	lim := newLimiter(1, 10*time.Second)
+	now := time.Now()
+	lim.buckets["stale"] = &bucket{tokens: 0, lastFill: now.Add(-30 * time.Second)}
+	lim.lastSweep = now.Add(-20 * time.Second)
+
+	if !lim.allow("fresh") {
+		t.Fatal("expected allow for fresh key")
+	}
+	if _, ok := lim.buckets["stale"]; ok {
+		t.Fatal("expected stale bucket to be evicted")
+	}
+}
```

`guard/timeout_internal_test.go`
```diff
diff --git a/guard/timeout_internal_test.go b/guard/timeout_internal_test.go
new file mode 100644
index 0000000..f6e17bc
--- /dev/null
+++ b/guard/timeout_internal_test.go
@@
+package guard
+
+import (
+	"net/http"
+	"net/http/httptest"
+	"testing"
+)
+
+func TestTimeoutWriterWriteHeaderIdempotent(t *testing.T) {
+	rec := httptest.NewRecorder()
+	req := httptest.NewRequest(http.MethodGet, "/", nil)
+	tw := &timeoutWriter{w: rec, req: req}
+
+	tw.WriteHeader(http.StatusCreated)
+	tw.WriteHeader(http.StatusAccepted)
+
+	if tw.code != http.StatusCreated {
+		t.Fatalf("code = %d, want %d", tw.code, http.StatusCreated)
+	}
+}
+
+func TestTimeoutWriterFlushIdempotent(t *testing.T) {
+	rec := httptest.NewRecorder()
+	req := httptest.NewRequest(http.MethodGet, "/", nil)
+	tw := &timeoutWriter{w: rec, req: req}
+
+	tw.WriteHeader(http.StatusCreated)
+	_, _ = tw.Write([]byte("ok"))
+	tw.flush()
+	tw.flush()
+
+	if rec.Code != http.StatusCreated {
+		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
+	}
+	if rec.Body.String() != "ok" {
+		t.Fatalf("body = %q, want %q", rec.Body.String(), "ok")
+	}
+}
+
+func TestTimeoutWriterTimeoutAfterFlushNoop(t *testing.T) {
+	rec := httptest.NewRecorder()
+	req := httptest.NewRequest(http.MethodGet, "/", nil)
+	tw := &timeoutWriter{w: rec, req: req}
+
+	tw.WriteHeader(http.StatusAccepted)
+	tw.flush()
+	tw.timeout()
+
+	if rec.Code != http.StatusAccepted {
+		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
+	}
+}
```

`guard/problem_internal_test.go`
```diff
diff --git a/guard/problem_internal_test.go b/guard/problem_internal_test.go
new file mode 100644
index 0000000..9c6fa0b
--- /dev/null
+++ b/guard/problem_internal_test.go
@@
+package guard
+
+import (
+	stderrors "errors"
+	"net/http"
+	"net/http/httptest"
+	"testing"
+
+	chassiserr "github.com/ai8future/chassis-go/errors"
+)
+
+type errWriter struct {
+	header http.Header
+	code   int
+}
+
+func (w *errWriter) Header() http.Header {
+	if w.header == nil {
+		w.header = make(http.Header)
+	}
+	return w.header
+}
+
+func (w *errWriter) WriteHeader(code int) {
+	w.code = code
+}
+
+func (w *errWriter) Write(p []byte) (int, error) {
+	return 0, stderrors.New("write failed")
+}
+
+func TestWriteProblem_EncodeError(t *testing.T) {
+	w := &errWriter{}
+	req := httptest.NewRequest(http.MethodGet, "/", nil)
+
+	writeProblem(w, req, chassiserr.ValidationError("bad"))
+
+	if w.code != http.StatusBadRequest {
+		t.Fatalf("expected status 400, got %d", w.code)
+	}
+	if ct := w.header.Get("Content-Type"); ct != "application/problem+json" {
+		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
+	}
+}
```

**Not Easily Testable Without Refactor**
- `httpkit.generateID`: crypto/rand failure path is not injectable.
- `testkit.GetFreePort`: net.Listen failure path is not deterministic.
- `otel.Init`: resource/exporter error branches require injection hooks.
- `call.getClientDuration` and `grpckit/httpkit` histogram creation failure paths require meter provider injection that can return errors.

