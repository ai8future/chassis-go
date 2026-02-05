Date Created: 2026-02-05 18:02:13 +0100
TOTAL_SCORE: 88/100

**AUDIT**
- OTLP exporters are hardcoded to plaintext (`WithInsecure()`), which risks leaking telemetry outside localhost. Add TLS support and make insecure explicit so production can default to TLS.

```diff
diff --git a/otel/otel.go b/otel/otel.go
--- a/otel/otel.go
+++ b/otel/otel.go
@@
-import (
-	"context"
-	"errors"
-	"log/slog"
-	"time"
+import (
+	"context"
+	"crypto/tls"
+	"errors"
+	"log/slog"
+	"time"
@@
 	"go.opentelemetry.io/otel/sdk/metric"
 	"go.opentelemetry.io/otel/sdk/resource"
 	sdktrace "go.opentelemetry.io/otel/sdk/trace"
 	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
 	"go.opentelemetry.io/otel/trace"
+	"google.golang.org/grpc/credentials"
 )
@@
 type Config struct {
 	ServiceName    string
 	ServiceVersion string
 	Endpoint       string           // OTLP gRPC endpoint, defaults to localhost:4317
 	Sampler        sdktrace.Sampler // defaults to AlwaysSample
+	Insecure       bool             // set true to disable TLS (plaintext)
+	TLSConfig      *tls.Config      // optional TLS config when Insecure is false
 }
@@
 	if cfg.Sampler == nil {
 		cfg.Sampler = sdktrace.AlwaysSample()
 	}
+	if !cfg.Insecure && cfg.TLSConfig == nil {
+		cfg.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
+	}
@@
-	traceExporter, err := otlptracegrpc.New(ctx,
-		otlptracegrpc.WithEndpoint(cfg.Endpoint),
-		otlptracegrpc.WithInsecure(),
-	)
+	traceOpts := []otlptracegrpc.Option{
+		otlptracegrpc.WithEndpoint(cfg.Endpoint),
+	}
+	if cfg.Insecure {
+		traceOpts = append(traceOpts, otlptracegrpc.WithInsecure())
+	} else {
+		traceOpts = append(traceOpts, otlptracegrpc.WithTLSCredentials(credentials.NewTLS(cfg.TLSConfig)))
+	}
+
+	traceExporter, err := otlptracegrpc.New(ctx, traceOpts...)
@@
-	metricExporter, err := otlpmetricgrpc.New(ctx,
-		otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
-		otlpmetricgrpc.WithInsecure(),
-	)
+	metricOpts := []otlpmetricgrpc.Option{
+		otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
+	}
+	if cfg.Insecure {
+		metricOpts = append(metricOpts, otlpmetricgrpc.WithInsecure())
+	} else {
+		metricOpts = append(metricOpts, otlpmetricgrpc.WithTLSCredentials(credentials.NewTLS(cfg.TLSConfig)))
+	}
+
+	metricExporter, err := otlpmetricgrpc.New(ctx, metricOpts...)
```

- Rate limiting config accepts zero/negative values and nil `KeyFunc`, leading to divide-by-zero panics or nil deref under load. Fail fast with clear config validation to prevent runtime crashes.

```diff
diff --git a/guard/ratelimit.go b/guard/ratelimit.go
--- a/guard/ratelimit.go
+++ b/guard/ratelimit.go
@@
 func RateLimit(cfg RateLimitConfig) func(http.Handler) http.Handler {
 	chassis.AssertVersionChecked()
+	if cfg.KeyFunc == nil {
+		panic("guard: RateLimit requires KeyFunc")
+	}
+	if cfg.Rate <= 0 {
+		panic("guard: RateLimit Rate must be > 0")
+	}
+	if cfg.Window <= 0 {
+		panic("guard: RateLimit Window must be > 0")
+	}
 	lim := newLimiter(cfg.Rate, cfg.Window)
 	return func(next http.Handler) http.Handler {
 		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
```

**TESTS**
- Add unit tests for new config validation and timeout late-write handling.

```diff
diff --git a/guard/ratelimit_test.go b/guard/ratelimit_test.go
--- a/guard/ratelimit_test.go
+++ b/guard/ratelimit_test.go
@@
 func TestXForwardedForIgnoresUntrustedProxy(t *testing.T) {
@@
 	if rec2.Code != http.StatusTooManyRequests {
 		t.Fatalf("second request: expected 429, got %d (XFF should be ignored for untrusted proxy)", rec2.Code)
 	}
 }
+
+func TestRateLimitInvalidConfigPanics(t *testing.T) {
+	cases := []struct {
+		name string
+		cfg  guard.RateLimitConfig
+	}{
+		{
+			name: "nil key func",
+			cfg: guard.RateLimitConfig{Rate: 1, Window: time.Second, KeyFunc: nil},
+		},
+		{
+			name: "zero rate",
+			cfg: guard.RateLimitConfig{Rate: 0, Window: time.Second, KeyFunc: guard.RemoteAddr()},
+		},
+		{
+			name: "zero window",
+			cfg: guard.RateLimitConfig{Rate: 1, Window: 0, KeyFunc: guard.RemoteAddr()},
+		},
+	}
+
+	for _, tc := range cases {
+		t.Run(tc.name, func(t *testing.T) {
+			defer func() {
+				if r := recover(); r == nil {
+					t.Fatal("expected panic for invalid rate limit config")
+				}
+			}()
+			_ = guard.RateLimit(tc.cfg)
+		})
+	}
+}
```

```diff
diff --git a/guard/timeout_test.go b/guard/timeout_test.go
--- a/guard/timeout_test.go
+++ b/guard/timeout_test.go
@@
-import (
-	"context"
-	"net/http"
-	"net/http/httptest"
-	"testing"
-	"time"
-
-	"github.com/ai8future/chassis-go/guard"
-)
+import (
+	"context"
+	"errors"
+	"net/http"
+	"net/http/httptest"
+	"testing"
+	"time"
+
+	"github.com/ai8future/chassis-go/guard"
+)
@@
 func TestTimeoutFlushesOnSuccess(t *testing.T) {
@@
 	if rec.Body.String() != "ok" {
 		t.Fatalf("expected body 'ok', got %q", rec.Body.String())
 	}
 }
+
+func TestTimeoutRejectsLateWrites(t *testing.T) {
+	errCh := make(chan error, 1)
+	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		<-r.Context().Done()
+		_, err := w.Write([]byte("late"))
+		errCh <- err
+	})
+
+	handler := guard.Timeout(20 * time.Millisecond)(inner)
+	req := httptest.NewRequest("GET", "/slow", nil)
+	rec := httptest.NewRecorder()
+	handler.ServeHTTP(rec, req)
+
+	select {
+	case err := <-errCh:
+		if !errors.Is(err, http.ErrHandlerTimeout) {
+			t.Fatalf("expected ErrHandlerTimeout, got %v", err)
+		}
+	case <-time.After(1 * time.Second):
+		t.Fatal("timed out waiting for handler write")
+	}
+}
```

```diff
diff --git a/call/retry_test.go b/call/retry_test.go
--- a/call/retry_test.go
+++ b/call/retry_test.go
@@
 func TestRetrier_BackoffHonorsContextCancel(t *testing.T) {
 	r := &Retrier{BaseDelay: 200 * time.Millisecond}
 	ctx, cancel := context.WithCancel(context.Background())
 	cancel()
@@
 	if time.Since(start) > 100*time.Millisecond {
 		t.Fatalf("backoff returned too slowly after cancel")
 	}
 }
+
+func TestRetrier_InvalidConfig(t *testing.T) {
+	r := &Retrier{MaxAttempts: 0, BaseDelay: 0}
+	_, err := r.Do(context.Background(), func() (*http.Response, error) {
+		return nil, errors.New("boom")
+	})
+	if !errors.Is(err, ErrInvalidRetrierConfig) {
+		t.Fatalf("expected ErrInvalidRetrierConfig, got %v", err)
+	}
+}
+
+func TestRetrier_BackoffZeroDelay(t *testing.T) {
+	r := &Retrier{BaseDelay: 0}
+	if err := r.backoff(context.Background(), 1); err != nil {
+		t.Fatalf("expected nil error for zero delay backoff, got %v", err)
+	}
+}
```

**FIXES**
- Retrier config edge cases can panic or return (nil, nil). Guard invalid configs and make backoff safe when delay is zero.

```diff
diff --git a/call/retry.go b/call/retry.go
--- a/call/retry.go
+++ b/call/retry.go
@@
-import (
-	"context"
-	"io"
-	"math/rand/v2"
-	"net/http"
-	"time"
+import (
+	"context"
+	"errors"
+	"fmt"
+	"io"
+	"math/rand/v2"
+	"net/http"
+	"time"
@@
 )
+
+// ErrInvalidRetrierConfig indicates an invalid Retrier configuration.
+var ErrInvalidRetrierConfig = errors.New("call: invalid retrier configuration")
@@
 func (r *Retrier) Do(ctx context.Context, fn func() (*http.Response, error)) (*http.Response, error) {
+	if r.MaxAttempts <= 0 {
+		return nil, fmt.Errorf("%w: MaxAttempts must be >= 1", ErrInvalidRetrierConfig)
+	}
+	if r.BaseDelay < 0 {
+		return nil, fmt.Errorf("%w: BaseDelay must be >= 0", ErrInvalidRetrierConfig)
+	}
 	var (
 		resp *http.Response
 		err  error
 	)
@@
 func (r *Retrier) backoff(ctx context.Context, attempt int) error {
 	delay := r.BaseDelay
 	for range attempt {
 		delay *= 2
 	}
-
-	// Add jitter: random duration in [0, delay/2).
-	jitter := time.Duration(rand.Int64N(int64(delay / 2)))
-	delay += jitter
+	if delay <= 0 {
+		return nil
+	}
+
+	// Add jitter: random duration in [0, delay/2).
+	if jitterBound := delay / 2; jitterBound > 0 {
+		jitter := time.Duration(rand.Int64N(int64(jitterBound)))
+		delay += jitter
+	}
```

- Timeout middleware buffers responses; after a timeout, late writes keep appending to memory. Track timed-out state and reject post-timeout writes to prevent unbounded buffering.

```diff
diff --git a/guard/timeout.go b/guard/timeout.go
--- a/guard/timeout.go
+++ b/guard/timeout.go
@@
 type timeoutWriter struct {
 	w   http.ResponseWriter
 	req *http.Request
@@
 	buf     []byte
 	written bool // true once flush() or timeout() has been called
 	started bool // true once handler called WriteHeader or Write
+	timedOut bool // true if timeout() finalized the response
 }
@@
 func (tw *timeoutWriter) WriteHeader(code int) {
 	tw.mu.Lock()
 	defer tw.mu.Unlock()
+	if tw.written {
+		return
+	}
 	if tw.started {
 		return
 	}
 	tw.started = true
 	tw.code = code
 }
@@
 func (tw *timeoutWriter) Write(b []byte) (int, error) {
 	tw.mu.Lock()
 	defer tw.mu.Unlock()
+	if tw.written {
+		if tw.timedOut {
+			return 0, http.ErrHandlerTimeout
+		}
+		return 0, nil
+	}
 	if !tw.started {
 		tw.started = true
 		tw.code = http.StatusOK
 	}
 	tw.buf = append(tw.buf, b...)
 	return len(b), nil
 }
@@
 func (tw *timeoutWriter) timeout() {
 	tw.mu.Lock()
 	defer tw.mu.Unlock()
 	if tw.written {
 		return
 	}
 	tw.written = true
+	tw.timedOut = true
+	tw.buf = nil
 	writeProblem(tw.w, tw.req, errors.TimeoutError("request timed out"))
 }
```

**REFACTOR**
- Centralize guard config validation (RateLimit, MaxBody, Timeout) into a small internal helper to keep middleware constructors consistent.
- Consider a shared OTel helper for client/server spans to reduce repeated attribute wiring across `call`, `httpkit`, and `grpckit`.
- Split `work` tracing concerns into optional wrappers so core concurrency primitives stay lightweight for non-OTel users.
