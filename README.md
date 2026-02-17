# chassis-go

A composable Go service toolkit for building production-grade microservices. Toolkit, not framework — chassis never owns `main()`, never hides wiring behind magic, and every package is independently importable.

```
go get github.com/ai8future/chassis-go/v5
```

**Current version:** 5.0.0 &middot; **Go:** 1.25.5+ &middot; **License:** MIT

---

## Why chassis-go?

Every Go microservice needs the same foundational concerns: env-based config, structured logging, graceful shutdown, health checks, HTTP middleware, gRPC interceptors, resilient HTTP clients, observability, feature flags, and request guards. Without a shared toolkit, teams re-implement these inconsistently across services.

chassis-go provides one cohesive, OTel-native solution where you wire together only what you need.

---

## Packages

### Tier 1: Foundation

| Package | Import | Purpose |
|---------|--------|---------|
| `chassis` | `github.com/ai8future/chassis-go/v5` | Version gate: `RequireMajor(5)` must be called before any other chassis API |
| `config` | `.../v5/config` | Generic env-to-struct config loader via struct tags. Panics on missing required vars |
| `logz` | `.../v5/logz` | Structured JSON logging wrapping `log/slog` with automatic OTel `trace_id`/`span_id` injection |
| `lifecycle` | `.../v5/lifecycle` | Signal-aware graceful shutdown orchestration via `errgroup` |
| `testkit` | `.../v5/testkit` | Test helpers: `NewLogger` (writes to `t.Log`), `SetEnv` (with cleanup), `GetFreePort` |

### Tier 2: Transports and Clients

| Package | Import | Purpose |
|---------|--------|---------|
| `httpkit` | `.../v5/httpkit` | HTTP middleware: RequestID, Logging, Recovery, Tracing. JSON error responses |
| `grpckit` | `.../v5/grpckit` | gRPC interceptors: Logging, Recovery, Metrics, Tracing. Health service registration |
| `health` | `.../v5/health` | Parallel health check aggregation with HTTP handler and gRPC adapter |
| `call` | `.../v5/call` | Resilient outbound HTTP client: retry with exponential backoff, circuit breaker, OTel spans |

### Tier 3: Cross-Cutting

| Package | Import | Purpose |
|---------|--------|---------|
| `guard` | `.../v5/guard` | HTTP guards: rate limiter (LRU), CORS, IP filter, security headers, body limits, timeouts |
| `flagz` | `.../v5/flagz` | Feature flags with percentage rollouts (FNV-1a), pluggable sources, OTel span events |
| `metrics` | `.../v5/metrics` | OTel-native metrics recorder with cardinality protection (max 1000 label combos) |
| `otel` | `.../v5/otel` | OpenTelemetry bootstrap: OTLP gRPC traces + metrics, configurable samplers |
| `errors` | `.../v5/errors` | Unified error type with dual HTTP/gRPC codes and RFC 9457 Problem Details |
| `secval` | `.../v5/secval` | JSON security validation: blocks dangerous keys (`__proto__`, `eval`, etc.) and deep nesting |
| `work` | `.../v5/work` | Structured concurrency: `Map`, `All`, `Race`, `Stream` — all OTel-traced |

**Tier isolation**: If you only use Tier 1 packages, only `golang.org/x/sync` is pulled in — no gRPC, no OTel SDK.

---

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "net"
    "net/http"
    "time"

    chassis "github.com/ai8future/chassis-go/v5"
    "github.com/ai8future/chassis-go/v5/config"
    "github.com/ai8future/chassis-go/v5/guard"
    "github.com/ai8future/chassis-go/v5/health"
    "github.com/ai8future/chassis-go/v5/httpkit"
    "github.com/ai8future/chassis-go/v5/lifecycle"
    "github.com/ai8future/chassis-go/v5/logz"
)

type AppConfig struct {
    Port     int    `env:"PORT" default:"8080"`
    LogLevel string `env:"LOG_LEVEL" default:"info"`
}

func main() {
    // Version gate — must be first
    chassis.RequireMajor(5)

    cfg := config.MustLoad[AppConfig]()
    logger := logz.New(cfg.LogLevel)
    logger.Info("starting service", "version", chassis.Version)

    // Routes
    mux := http.NewServeMux()
    mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprintln(w, "hello world")
    })
    mux.Handle("GET /health", health.Handler(map[string]health.Check{
        "self": func(_ context.Context) error { return nil },
    }))

    // Middleware stack
    handler := httpkit.Recovery(logger)(
        httpkit.Tracing()(
            httpkit.RequestID(
                guard.Timeout(10*time.Second)(
                    httpkit.Logging(logger)(mux),
                ),
            ),
        ),
    )

    // Run with graceful shutdown
    lifecycle.Run(context.Background(),
        func(ctx context.Context) error {
            addr := fmt.Sprintf(":%d", cfg.Port)
            srv := &http.Server{Addr: addr, Handler: handler}
            ln, _ := net.Listen("tcp", addr)
            logger.Info("listening", "addr", ln.Addr().String())

            errCh := make(chan error, 1)
            go func() { errCh <- srv.Serve(ln) }()
            select {
            case <-ctx.Done():
                return srv.Shutdown(context.Background())
            case err := <-errCh:
                return err
            }
        },
    )
}
```

---

## Package Details

### `config` — Environment-Based Configuration

Load environment variables into typed structs using struct tags. Fail-fast: missing required config panics at startup.

```go
type AppConfig struct {
    Port        int           `env:"PORT" default:"8080"`
    DatabaseURL string        `env:"DATABASE_URL"`                // required (default)
    Debug       bool          `env:"DEBUG" required:"false"`      // optional
    Timeout     time.Duration `env:"TIMEOUT" default:"30s"`
    AllowedIPs  []string      `env:"ALLOWED_IPS" default:"127.0.0.1"`
}

cfg := config.MustLoad[AppConfig]()
```

**Supported types:** `string`, `int`, `int64`, `float64`, `bool`, `time.Duration`, `[]string` (comma-separated)

### `logz` — Structured JSON Logging

Wraps `log/slog` with automatic OpenTelemetry trace correlation. When OTel is active, every log line includes `trace_id` and `span_id` at the top level of JSON output — even inside `slog.Group` scopes.

```go
logger := logz.New("info")  // "debug", "info", "warn", "error"
logger.Info("request handled", "status", 200, "duration_ms", 42)
```

Output:
```json
{"time":"...","level":"INFO","msg":"request handled","trace_id":"abc123","span_id":"def456","status":200,"duration_ms":42}
```

### `lifecycle` — Graceful Shutdown

Signal-aware orchestrator using `errgroup`. Catches SIGTERM/SIGINT, cancels the shared context, and waits for all components to drain.

```go
lifecycle.Run(ctx,
    httpServerComponent,
    grpcServerComponent,
    workerComponent,
)
```

Each component receives a context that cancels on signal or when any peer returns an error.

### `call` — Resilient HTTP Client

Outbound HTTP with retry (exponential backoff + jitter), circuit breaker (Closed/Open/HalfOpen states), and OTel client spans.

```go
client := call.New(
    call.WithTimeout(5*time.Second),
    call.WithRetry(3, 500*time.Millisecond),
    call.WithCircuitBreaker("payments-api", 5, 30*time.Second),
)

resp, err := client.Do(req)
```

Batch concurrent requests with `client.Batch(ctx, requests)` — powered by `work.Map` under the hood.

### `errors` — Unified Error Type

Dual HTTP + gRPC error codes with RFC 9457 Problem Details. Fluent API for decorating errors.

```go
err := errors.NotFoundError("user not found").
    WithDetail("user_id", "abc123").
    WithType("https://api.example.com/errors/user-not-found").
    WithCause(dbErr)

// Factory constructors:
errors.ValidationError(msg)    // 400 / INVALID_ARGUMENT
errors.UnauthorizedError(msg)  // 401 / UNAUTHENTICATED
errors.ForbiddenError(msg)     // 403 / PERMISSION_DENIED
errors.NotFoundError(msg)      // 404 / NOT_FOUND
errors.RateLimitError(msg)     // 429 / RESOURCE_EXHAUSTED
errors.TimeoutError(msg)       // 504 / DEADLINE_EXCEEDED
errors.DependencyError(msg)    // 503 / UNAVAILABLE
errors.InternalError(msg)      // 500 / INTERNAL
```

Write RFC 9457 responses directly:
```go
errors.WriteProblem(w, r, err, requestID)
```

### `httpkit` — HTTP Middleware

Standard `func(http.Handler) http.Handler` middleware — compatible with any router.

```go
// Recommended stack order (outermost first):
handler := httpkit.Recovery(logger)(        // catch panics → 500
    httpkit.Tracing()(                      // OTel server spans + duration metric
        httpkit.RequestID(                  // UUID v4 request ID
            httpkit.Logging(logger)(mux),   // structured request logging
        ),
    ),
)

// Access request ID from context
id := httpkit.RequestIDFrom(r.Context())
```

Response helpers:
```go
httpkit.JSONError(w, r, http.StatusBadRequest, "invalid input")
httpkit.JSONProblem(w, r, serviceErr)
```

### `grpckit` — gRPC Interceptors

Unary and stream interceptors for logging, panic recovery, metrics, and tracing. Wire them with `grpc.ChainUnaryInterceptor`.

```go
srv := grpc.NewServer(
    grpc.ChainUnaryInterceptor(
        grpckit.UnaryRecovery(logger),
        grpckit.UnaryTracing(),
        grpckit.UnaryLogging(logger),
        grpckit.UnaryMetrics(),
    ),
    grpc.ChainStreamInterceptor(
        grpckit.StreamRecovery(logger),
        grpckit.StreamTracing(),
        grpckit.StreamLogging(logger),
        grpckit.StreamMetrics(),
    ),
)

// Register gRPC health service
grpckit.RegisterHealth(srv, health.CheckFunc(checks))
```

### `health` — Health Checks

Composable health checks that run in parallel. Supports both HTTP and gRPC transports.

```go
checks := map[string]health.Check{
    "database": func(ctx context.Context) error { return db.PingContext(ctx) },
    "cache":    func(ctx context.Context) error { return redis.Ping(ctx).Err() },
}

// HTTP handler: 200 {"status":"healthy",...} or 503 {"status":"unhealthy",...}
mux.Handle("GET /health", health.Handler(checks))

// gRPC adapter
grpckit.RegisterHealth(srv, health.CheckFunc(checks))
```

### `guard` — Request Guards

HTTP middleware for rate limiting, CORS, IP filtering, security headers, body limits, and timeouts.

```go
// Rate limiter with LRU eviction (O(1))
guard.RateLimit(guard.RateLimitConfig{
    Rate:    100,
    Window:  time.Minute,
    MaxKeys: 10000,
    KeyFunc: guard.XForwardedFor("10.0.0.0/8"),  // spoof-resistant
})

// CORS
guard.CORS(guard.CORSConfig{
    AllowOrigins: []string{"https://app.example.com"},
    AllowMethods: []string{"GET", "POST"},
    MaxAge:       3600,
})

// Security headers (CSP, HSTS 2yr, X-Frame-Options: DENY, etc.)
guard.SecurityHeaders(guard.DefaultSecurityHeaders)

// IP allow/deny by CIDR
guard.IPFilter(guard.IPFilterConfig{
    AllowCIDRs: []string{"10.0.0.0/8"},
    DenyAction:  "block",
})

// Body size limit
guard.MaxBody(2 * 1024 * 1024)  // 2 MB

// Request timeout with buffered response writer
guard.Timeout(10 * time.Second)
```

**Key functions** for rate limiter identification:
```go
guard.RemoteAddr()                          // r.RemoteAddr
guard.XForwardedFor("10.0.0.0/8")          // rightmost untrusted IP
guard.HeaderKey("X-API-Key")               // arbitrary header
```

### `flagz` — Feature Flags

Feature flags with boolean checks, percentage rollouts, and multi-source configuration.

```go
// Sources: env, map, JSON file, or composite
flags := flagz.New(flagz.Multi(
    flagz.FromEnv("FLAG"),       // FLAG_NEW_CHECKOUT=true
    flagz.FromJSON("flags.json"),
))

// Boolean check
if flags.Enabled("new-checkout") { ... }

// Percentage rollout (consistent per user via FNV-1a hash)
if flags.EnabledFor(ctx, "new-checkout", flagz.Context{
    UserID:  user.ID,
    Percent: 25,  // 25% of users
}) { ... }

// String variant
theme := flags.Variant("theme", "light")
```

### `metrics` — OTel Metrics with Cardinality Protection

Pre-configured metrics recorder with automatic cardinality limits. Drops new label combinations after 1000 per metric to prevent backend explosions.

```go
rec := metrics.New("ordersvc", logger)

// Pre-built request metrics
rec.RecordRequest(ctx, method, status, durationMs, contentLength)

// Custom domain counters and histograms
orders := rec.Counter("orders_placed")
orders.Add(ctx, 1, "region", "us-east", "tier", "premium")

latency := rec.Histogram("payment_duration_seconds", metrics.DurationBuckets)
latency.Observe(ctx, 0.042, "provider", "stripe")
```

### `otel` — OpenTelemetry Bootstrap

One-call OTel SDK initialization: OTLP gRPC exporters for traces and metrics, W3C propagation, configurable samplers.

```go
shutdown := otel.Init(otel.Config{
    ServiceName:    "ordersvc",
    ServiceVersion: chassis.Version,
    Endpoint:       "otel-collector:4317",   // default: localhost:4317
    Sampler:        otel.RatioSample(0.1),   // 10% sampling; default: AlwaysSample
    Insecure:       true,                    // plaintext for dev; default: TLS
})
defer shutdown(context.Background())
```

### `secval` — JSON Security Validation

Validates JSON payloads against dangerous keys and excessive nesting. Zero cross-module dependencies.

```go
if err := secval.ValidateJSON(body); err != nil {
    // errors.Is(err, secval.ErrDangerousKey)
    // errors.Is(err, secval.ErrNestingDepth)
    // errors.Is(err, secval.ErrInvalidJSON)
}
```

Blocks: `__proto__`, `constructor`, `prototype`, `eval`, `exec`, `spawn`, `import`, `require`, `system`, `shell`, `command`, `script`, `fork`, `execute`, `include`. Max nesting depth: 20.

### `work` — Structured Concurrency

Parallel execution primitives with bounded worker pools and automatic OTel tracing.

```go
// Map: transform items concurrently (preserves order)
results, err := work.Map(ctx, items, processItem, work.Workers(8))

// All: run tasks concurrently, fail on first error
err := work.All(ctx, []func(context.Context) error{task1, task2, task3})

// Race: first success wins, cancels the rest
result, err := work.Race(ctx, fetchFromPrimary, fetchFromReplica)

// Stream: process channel items concurrently
out := work.Stream(ctx, inChan, transform, work.Workers(4))
for r := range out {
    fmt.Println(r.Value, r.Err)
}
```

### `testkit` — Test Utilities

```go
func TestMyHandler(t *testing.T) {
    logger := testkit.NewLogger(t)        // writes to t.Log, hidden on pass
    testkit.SetEnv(t, map[string]string{  // auto-cleanup via t.Cleanup
        "PORT": "0",
        "DATABASE_URL": "postgres://...",
    })
    port, _ := testkit.GetFreePort()      // OS-assigned free TCP port
}
```

---

## Version Gate

chassis-go enforces a mandatory version compatibility contract. Every service must declare which major version it expects:

```go
func main() {
    chassis.RequireMajor(5)  // must be the first chassis call
    // ...
}
```

If the installed library's major version doesn't match, the process exits immediately with a clear migration message. Every chassis module calls `AssertVersionChecked()` at its entry points — importing a chassis module without calling `RequireMajor` first causes an immediate crash.

---

## Examples

The `examples/` directory contains runnable services demonstrating progressive complexity:

| Example | What It Demonstrates |
|---------|---------------------|
| `examples/01-cli` | Minimal CLI: `config` + `logz` |
| `examples/02-service` | gRPC service: `config` + `grpckit` + `health` + `lifecycle` |
| `examples/03-client` | Resilient HTTP client: `call` with retry + circuit breaker |
| `examples/04-full-service` | Full wiring: all packages combined (HTTP + admin server) |
| `cmd/demo-shutdown` | Graceful shutdown demonstration with two worker goroutines |

Run any example:
```bash
go run ./examples/04-full-service
```

Test it:
```bash
curl http://localhost:9090/health
curl -X POST http://localhost:8080/v1/demo -d '{"input":"hello"}'
curl -X POST http://localhost:8080/v1/demo -d '{"__proto__":"evil"}'  # → 400
```

---

## Design Principles

1. **Toolkit, not framework** — Chassis never owns `main()`. You call it, not the other way around.
2. **Tier isolation** — Importing `config` doesn't pull in gRPC or OTel SDK. Dependencies scale with what you use.
3. **Visible wiring** — No magic startup, no global init. All assembly happens in your code.
4. **Fail fast** — Missing config, invalid guard parameters, or wrong major version crash immediately at startup with clear messages.
5. **OTel native** — Tracing, metrics, and log correlation are built in from the ground up, not bolted on.
6. **Standard interfaces** — HTTP middleware uses `func(http.Handler) http.Handler`. gRPC uses standard interceptors. No custom types to learn.

---

## Auto-Instrumented Observability

When OTel is initialized, the following telemetry is collected automatically:

**Traces:**
- `httpkit.Tracing()` — HTTP server spans with W3C context propagation
- `grpckit.UnaryTracing()` / `StreamTracing()` — gRPC server spans from metadata
- `call.Client.Do()` — HTTP client spans with header injection
- `work.Map/All/Race/Stream` — parent + per-item child spans

**Metrics:**
- `http.server.request.duration` — HTTP server latency histogram
- `http.client.request.duration` — HTTP client latency histogram
- `rpc.server.duration` — gRPC server latency histogram

**Log correlation:**
- Every `logz` log line includes `trace_id` and `span_id` from the active span context

---

## Dependencies

Only the OTel API, `golang.org/x/sync`, and `google.golang.org/grpc` are direct dependencies:

```
go.opentelemetry.io/otel          v1.40.0
go.opentelemetry.io/otel/sdk      v1.40.0
golang.org/x/sync                 v0.19.0
google.golang.org/grpc            v1.78.0
```

---

## License

MIT — see [LICENSE](LICENSE).
