Date Created: 2026-02-05 17:30:35 +0100 CET
TOTAL_SCORE: 79/100

**Summary**
Quick static review across core packages and examples with a focus on security, resilience, and operational risk. Code quality is generally solid (clear package boundaries, consistent error types, OTel integration), but a few defaults and edge cases reduce safety under adversarial or high-load conditions.

**Findings**
1. **High**: Rate limiter map can grow without bound under high-cardinality keys. `guard/ratelimit.go` advertises an upper bound (`maxBuckets`) but only sweeps stale entries; if all keys stay active, the map grows indefinitely and can be memory-DoS’d. Recommendation: enforce a hard cap by evicting least-recently-used buckets when the size still exceeds `maxBuckets` after the stale sweep.
2. **Medium**: OTLP exporters are always configured with `WithInsecure()` in `otel/otel.go`. This sends telemetry in plaintext by default, which is risky outside localhost and can leak traces/metrics. Recommendation: allow TLS by default with an explicit opt-in for insecure, or provide a clear config flag for TLS vs. insecure.
3. **Low**: `httpkit` request ID generation panics if `crypto/rand.Read` fails. While rare, this can crash the server during entropy starvation or restricted environments. Recommendation: fall back to a non-crypto, best-effort ID instead of panicking.
4. **Low**: Example HTTP servers do not set read/write/idle timeouts, leaving them open to slowloris-style resource exhaustion. Recommendation: set conservative timeouts in `examples/04-full-service/main.go` so defaults are safe in reference code.

**Patch-Ready Diffs**
Diff 1: Enforce a real cap on rate limiter buckets
```diff
diff --git a/guard/ratelimit.go b/guard/ratelimit.go
--- a/guard/ratelimit.go
+++ b/guard/ratelimit.go
@@
-import (
-	"net/http"
-	"sync"
-	"time"
+import (
+	"net/http"
+	"sort"
+	"sync"
+	"time"
@@
-	// Lazy sweep: evict stale buckets every window period or when map is full.
-	if now.Sub(l.lastSweep) >= l.window || len(l.buckets) >= maxBuckets {
-		staleThreshold := now.Add(-2 * l.window)
-		for k, b := range l.buckets {
-			if b.lastFill.Before(staleThreshold) {
-				delete(l.buckets, k)
-			}
-		}
-		l.lastSweep = now
-	}
+	// Lazy sweep: evict stale buckets every window period or when map is full.
+	if now.Sub(l.lastSweep) >= l.window || len(l.buckets) >= maxBuckets {
+		staleThreshold := now.Add(-2 * l.window)
+		for k, b := range l.buckets {
+			if b.lastFill.Before(staleThreshold) {
+				delete(l.buckets, k)
+			}
+		}
+		if len(l.buckets) > maxBuckets {
+			type kv struct {
+				key      string
+				lastFill time.Time
+			}
+			candidates := make([]kv, 0, len(l.buckets))
+			for k, b := range l.buckets {
+				candidates = append(candidates, kv{key: k, lastFill: b.lastFill})
+			}
+			sort.Slice(candidates, func(i, j int) bool {
+				return candidates[i].lastFill.Before(candidates[j].lastFill)
+			})
+			over := len(l.buckets) - maxBuckets
+			for i := 0; i < over; i++ {
+				delete(l.buckets, candidates[i].key)
+			}
+		}
+		l.lastSweep = now
+	}
```

Diff 2: Allow secure OTLP by default with explicit insecure override
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
+	Insecure       *bool            // nil defaults to insecure (backward compat); set to ptr(false) to require TLS
+	TLSConfig      *tls.Config      // optional, used when Insecure is false
 }
@@
 	if cfg.Endpoint == "" {
 		cfg.Endpoint = "localhost:4317"
 	}
 	if cfg.Sampler == nil {
 		cfg.Sampler = sdktrace.AlwaysSample()
 	}
+	insecure := true
+	if cfg.Insecure != nil {
+		insecure = *cfg.Insecure
+	}
@@
-	traceExporter, err := otlptracegrpc.New(ctx,
-		otlptracegrpc.WithEndpoint(cfg.Endpoint),
-		otlptracegrpc.WithInsecure(),
-	)
+	traceOpts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.Endpoint)}
+	if insecure {
+		traceOpts = append(traceOpts, otlptracegrpc.WithInsecure())
+	} else {
+		tlsCfg := cfg.TLSConfig
+		if tlsCfg == nil {
+			tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
+		}
+		traceOpts = append(traceOpts, otlptracegrpc.WithTLSCredentials(credentials.NewTLS(tlsCfg)))
+	}
+	traceExporter, err := otlptracegrpc.New(ctx, traceOpts...)
@@
-	metricExporter, err := otlpmetricgrpc.New(ctx,
-		otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
-		otlpmetricgrpc.WithInsecure(),
-	)
+	metricOpts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(cfg.Endpoint)}
+	if insecure {
+		metricOpts = append(metricOpts, otlpmetricgrpc.WithInsecure())
+	} else {
+		tlsCfg := cfg.TLSConfig
+		if tlsCfg == nil {
+			tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
+		}
+		metricOpts = append(metricOpts, otlpmetricgrpc.WithTLSCredentials(credentials.NewTLS(tlsCfg)))
+	}
+	metricExporter, err := otlpmetricgrpc.New(ctx, metricOpts...)
```

Diff 3: Avoid panicking on request ID generation failures
```diff
diff --git a/httpkit/middleware.go b/httpkit/middleware.go
--- a/httpkit/middleware.go
+++ b/httpkit/middleware.go
@@
-import (
-	"context"
-	"crypto/rand"
-	"fmt"
-	"log/slog"
-	"net/http"
-	"runtime/debug"
-	"time"
+import (
+	"context"
+	"crypto/rand"
+	"fmt"
+	"log/slog"
+	"net/http"
+	"runtime/debug"
+	"sync/atomic"
+	"time"
@@
 type requestIDKey struct{}
+
+var fallbackCounter uint64
@@
 func generateID() string {
 	b := make([]byte, 16)
-	if _, err := rand.Read(b); err != nil {
-		panic("httpkit: crypto/rand.Read failed: " + err.Error())
-	}
-	// Set version (4) and variant (RFC 4122) bits.
-	b[6] = (b[6] & 0x0f) | 0x40
-	b[8] = (b[8] & 0x3f) | 0x80
-	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
-		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
+	if _, err := rand.Read(b); err == nil {
+		// Set version (4) and variant (RFC 4122) bits.
+		b[6] = (b[6] & 0x0f) | 0x40
+		b[8] = (b[8] & 0x3f) | 0x80
+		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
+			b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
+	}
+	// Fallback: avoid panicking if crypto/rand is unavailable.
+	n := atomic.AddUint64(&fallbackCounter, 1)
+	return fmt.Sprintf("fallback-%x-%x", time.Now().UnixNano(), n)
 }
```

Diff 4: Add safe HTTP server timeouts in the full-service example
```diff
diff --git a/examples/04-full-service/main.go b/examples/04-full-service/main.go
--- a/examples/04-full-service/main.go
+++ b/examples/04-full-service/main.go
@@
-			addr := fmt.Sprintf(":%d", cfg.HTTPPort)
-			srv := &http.Server{Addr: addr, Handler: handler}
+			addr := fmt.Sprintf(":%d", cfg.HTTPPort)
+			srv := &http.Server{
+				Addr:              addr,
+				Handler:           handler,
+				ReadHeaderTimeout: 5 * time.Second,
+				ReadTimeout:       10 * time.Second,
+				WriteTimeout:      30 * time.Second,
+				IdleTimeout:       60 * time.Second,
+			}
@@
-			addr := fmt.Sprintf(":%d", cfg.AdminPort)
-			srv := &http.Server{Addr: addr, Handler: adminMux}
+			addr := fmt.Sprintf(":%d", cfg.AdminPort)
+			srv := &http.Server{
+				Addr:              addr,
+				Handler:           adminMux,
+				ReadHeaderTimeout: 5 * time.Second,
+				ReadTimeout:       10 * time.Second,
+				WriteTimeout:      30 * time.Second,
+				IdleTimeout:       60 * time.Second,
+			}
```

**Additional Notes**
The `guard/timeout` middleware buffers response bodies to prevent partial writes after a timeout. That is safe for small responses but can use significant memory for large streaming responses; if this middleware is used on endpoints that stream or produce large bodies, consider an alternate timeout strategy (context cancellation only, or a capped buffer).

**Testing**
No tests were run for this audit-only report.
