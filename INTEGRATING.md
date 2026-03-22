# Integrating chassis-go

Practical guide for teams adopting chassis-go into an existing Go codebase.

## Before you start

**Requirements**: Go 1.25+ (the module requires 1.25.5).

**What this is**: A cohesive toolkit that covers the foundational concerns of a Go service — config, logging, lifecycle, HTTP, gRPC, health checks, and resilient outbound calls. Import the whole thing. Your `main()` stays yours.

**What this is not**: An opinionated framework. Chassis doesn't own your dependency injection, routing, or service mesh. It provides building blocks that you wire together explicitly.

**Service modules vs. utility modules**: Chassis modules fall into two categories. *Service modules* (`httpkit`, `grpckit`, `lifecycle`, `registry`) require a running service with `lifecycle.Run()` and an active registry — they crash if used without it. *Utility modules* (`config`, `logz`, `errors`, `call`, `work`, `health`, `secval`, `flagz`, `metrics`, `otel`, `testkit`, `cache`, `seal`, `tick`, `webhook`, `deploy`) work anywhere — services, libraries, CLI tools. A Go module that imports chassis utilities can be consumed by any application that calls `RequireMajor(10)`.

## Installation

```bash
go get github.com/ai8future/chassis-go/v10
```

The top-level package exports the library version for diagnostics:

```go
import chassis "github.com/ai8future/chassis-go/v10"

logger.Info("starting", "chassis_version", chassis.Version)
```

### Version gate

Every service must declare which major version of chassis it supports. This prevents silent behavior changes when chassis is upgraded without review.

```go
func main() {
    chassis.RequireMajor(10) // crashes if chassis major version != 10
    // ... rest of startup
}
```

If the version doesn't match, the process exits with a clear message telling you exactly what to do. If `RequireMajor` is not called before using any chassis module, those modules will also crash at startup.

### Deterministic ports

The root package also provides `chassis.Port()` for deriving stable, deterministic ports from a service name using djb2 hashing. The result is in the range 5000–48000, safely below the OS ephemeral range.

```go
// Derive ports from service name (same name always gives the same ports)
httpPort := chassis.Port("serp_svc", chassis.PortHTTP)       // base port (offset 0)
grpcPort := chassis.Port("serp_svc", chassis.PortGRPC)       // base + 1
metricsPort := chassis.Port("serp_svc", chassis.PortMetrics) // base + 2

// Zero-arg defaults to offset 0 (HTTP)
httpPort := chassis.Port("serp_svc") // equivalent to chassis.Port("serp_svc", chassis.PortHTTP)
```

Standard offset constants:

| Constant | Value | Purpose |
|----------|-------|---------|
| `chassis.PortHTTP` | 0 | Primary HTTP/REST API |
| `chassis.PortGRPC` | 1 | gRPC transport |
| `chassis.PortMetrics` | 2 | Admin, Prometheus metrics, health |

Services that need additional ports use raw offsets (3, 4, ...). For test isolation, continue using `testkit.GetFreePort()` — it serves a different purpose (random OS-assigned port).

A typical service imports all packages:

```go
import (
    "github.com/ai8future/chassis-go/v10/call"
    "github.com/ai8future/chassis-go/v10/config"
    "github.com/ai8future/chassis-go/v10/errors"
    "github.com/ai8future/chassis-go/v10/grpckit"
    "github.com/ai8future/chassis-go/v10/health"
    "github.com/ai8future/chassis-go/v10/httpkit"
    "github.com/ai8future/chassis-go/v10/lifecycle"
    "github.com/ai8future/chassis-go/v10/work"
    "github.com/ai8future/chassis-go/v10/logz"
    "github.com/ai8future/chassis-go/v10/metrics"
    "github.com/ai8future/chassis-go/v10/registry"
    "github.com/ai8future/chassis-go/v10/secval"
)
```

And in test files:

```go
import "github.com/ai8future/chassis-go/v10/testkit"
```

The packages are designed to work together. While you *can* import selectively (a CLI tool might only need `config` + `logz`), the standard path for any service is to use the full toolkit.

---

## Package-by-package integration

### config — Environment-based configuration

**When to use**: You have structs representing service configuration and want to populate them from environment variables without a config file parser.

```go
type AppConfig struct {
    Port        int           `env:"PORT" default:"8080"`
    DatabaseURL string        `env:"DATABASE_URL"`                    // required by default
    LogLevel    string        `env:"LOG_LEVEL" default:"info"`
    Debug       bool          `env:"DEBUG" required:"false"`          // optional, zero value if unset
    Timeout     time.Duration `env:"REQUEST_TIMEOUT" default:"30s"`
    AllowedIPs  []string      `env:"ALLOWED_IPS" default:"127.0.0.1"`
}

cfg := config.MustLoad[AppConfig]()
```

**Behavior**:
- Fields without an `env` tag are ignored.
- Missing required values panic at startup (fail-fast by design).
- Supported types: `string`, `int`, `int64`, `float64`, `bool`, `time.Duration`, `[]string` (comma-separated).

**Integration notes**:
- Call `MustLoad` early in `main()`, before any goroutines. The panic-on-missing design means configuration errors surface immediately at startup, not minutes later under load.
- If you already have a config library (viper, envconfig, etc.), you don't need to migrate all at once. Chassis config is a standalone function — use it alongside your existing setup.
- For testing, use `testkit.SetEnv(t, map[string]string{...})` to set env vars with automatic cleanup, then call `config.MustLoad[T]()` as usual.

---

### errors — Unified service errors

**When to use**: You need error types that carry both HTTP and gRPC status codes for consistent error handling across transport layers.

```go
import "github.com/ai8future/chassis-go/v10/errors"

// Factory constructors for common error categories
err := errors.ValidationError("name is required")         // 400 / INVALID_ARGUMENT
err := errors.NotFoundError("user not found")              // 404 / NOT_FOUND
err := errors.UnauthorizedError("invalid token")           // 401 / UNAUTHENTICATED
err := errors.ForbiddenError("access denied")              // 403 / PERMISSION_DENIED
err := errors.PayloadTooLargeError("body exceeds limit")   // 413 / INVALID_ARGUMENT
err := errors.TimeoutError("request timed out")            // 504 / DEADLINE_EXCEEDED
err := errors.RateLimitError("too many requests")          // 429 / RESOURCE_EXHAUSTED
err := errors.DependencyError("database unavailable")      // 503 / UNAVAILABLE
err := errors.InternalError("unexpected failure")          // 500 / INTERNAL

// Formatted errors
err := errors.Errorf(errors.ValidationError, "%s must be between %d and %d", "age", 0, 150)

// Wrap unknown errors as internal
svcErr := errors.FromError(err) // returns *ServiceError

// Fluent detail attachment
err := errors.ValidationError("invalid input").
    WithDetail("field", "email").
    WithDetail("reason", "invalid format")

// Convert to gRPC status
grpcErr := svcErr.GRPCStatus().Err()

// Error cause chaining (works with errors.Is/errors.As)
svcErr := errors.InternalError("db failed").WithCause(originalErr)
```

**Integration notes**:
- `ServiceError` implements `error`, `Unwrap() error`, and `GRPCStatus() *status.Status`.
- In HTTP handlers, use `svcErr.HTTPCode` for the response status. In gRPC handlers, use `svcErr.GRPCStatus().Err()`.
- `FromError` is the boundary converter — use it when catching errors from business logic to ensure they become `ServiceError` for the transport layer.
- `WithCause` preserves the original error for `errors.Is`/`errors.As` chains while still providing a clean message to clients.

---

### secval — JSON security validation

**When to use**: You want to reject JSON payloads containing dangerous keys (prototype pollution, injection patterns) before processing them.

```go
import "github.com/ai8future/chassis-go/v10/secval"

// Validate JSON before unmarshalling
if err := secval.ValidateJSON(body); err != nil {
    // err is a secval error (ErrDangerousKey, ErrNestingDepth, ErrInvalidJSON)
    // Wrap it into a ServiceError at the handler boundary:
    return errors.ValidationError(err.Error())
}
json.Unmarshal(body, &req) // safe to unmarshal now
```

**What it checks**:
- **Dangerous keys**: `__proto__`, `constructor`, `prototype`. Only keys that indicate prototype pollution or direct code execution vectors are blocked. Common business-domain words (command, system, import, etc.) are intentionally excluded to avoid false positives. Keys are normalized (lowercased, hyphens replaced with underscores) before checking.
- **Nesting depth**: Maximum 20 levels. Prevents stack overflow attacks from deeply nested JSON.

**Integration notes**:
- `secval` defines its own error types (`ErrDangerousKey`, `ErrNestingDepth`, `ErrInvalidJSON`), NOT `ServiceError`. This keeps the module dependency-free. Wrap secval errors into `ServiceError` at your handler boundary.
- The validation parses JSON once, then your handler parses again into a struct. This double-parse is acceptable for typical payloads (<1MB). Do not use secval on file uploads or streaming endpoints.
- Always enforce body size limits (`http.MaxBytesReader` at 1-2MB) BEFORE passing to secval.

---

### metrics — OTel metrics with cardinality protection

**When to use**: You want structured metrics with built-in request recording and cardinality protection. Metrics flow out via OTLP push — no scrape endpoint required.

```go
import "github.com/ai8future/chassis-go/v10/metrics"

// Create a recorder with a metric prefix
recorder := metrics.New("mysvc", logger)

// Record request metrics
recorder.RecordRequest(ctx, "POST", "200", 42.5, 1024)

// Create custom counters and histograms
counter := recorder.Counter("searches_total")
counter.Add(ctx, 1, "type", "organic")

hist := recorder.Histogram("pdf_size_bytes", metrics.ContentBuckets)
hist.Observe(ctx, 524288, "format", "pdf")
```

**What it records per `RecordRequest` call**:
- `{prefix}_requests_total{method, status}` — Counter
- `{prefix}_request_duration_seconds{method}` — Histogram
- `{prefix}_content_size_bytes{method}` — Histogram

**Integration notes**:
- Requires `otel.Init()` to be called first to configure the OTLP metric exporter. Without it, metrics are recorded to the no-op global meter.
- Cardinality protection: max 1000 label combinations per metric. On overflow, new combinations are silently dropped and a warning is logged once.
- The metric prefix is caller-supplied — use your service name.
- Uses OpenTelemetry metric API — no Prometheus dependency.

---

### otel — OpenTelemetry bootstrap

**When to use**: You want distributed tracing and metrics export via OTLP. This is the single SDK consumer — all other chassis modules depend only on OTel API packages.

```go
import otelinit "github.com/ai8future/chassis-go/v10/otel"

shutdown := otelinit.Init(otelinit.Config{
    ServiceName:    "mysvc",
    ServiceVersion: chassis.Version,
    Endpoint:       "otel-collector:4317", // defaults to localhost:4317
    Sampler:        otelinit.RatioSample(0.1), // defaults to AlwaysSample
    Insecure:       false, // default: uses TLS. Set true for plaintext (dev/test)
})
defer shutdown(context.Background())
```

**Behavior**:
- Configures OTLP gRPC trace and metric exporters.
- Sets the global `TracerProvider`, `MeterProvider`, and `TextMapPropagator` (W3C TraceContext + Baggage).
- Degrades gracefully — if the exporter can't connect, tracing and metrics become no-ops rather than crashing.
- Returns a `ShutdownFunc` that drains pending spans/metrics on process exit.

**Utilities**:
- `otel.DetachContext(ctx)` — returns a new `context.Background()` that preserves the OTel span context but detaches cancellation. Use when spawning background goroutines from request handlers.
- `otel.AlwaysSample()` — samples every trace (default).
- `otel.RatioSample(fraction)` — samples a fraction of traces by trace ID.

**Integration notes**:
- Call `otel.Init()` early in `main()`, after `config.MustLoad` and `logz.New`, but before creating middleware or metrics recorders.
- Without `otel.Init()`, all tracing and metrics across chassis are no-ops — `httpkit.Tracing()`, `grpckit.UnaryTracing()`, `call.Do`, `work.Map`, and `metrics.RecordRequest` all degrade gracefully.
- The shutdown function should be deferred in `main()` to ensure spans and metrics are flushed before exit.

---

### guard — Request guards

**When to use**: You need request-level protection — enforcing timeouts, rate limits, CORS, security headers, IP filtering, or body size limits as HTTP middleware.

```go
import "github.com/ai8future/chassis-go/v10/guard"

// Timeout — returns 504 if handler doesn't complete in time
handler = guard.Timeout(10 * time.Second)(handler)

// Rate limiting — per-key token bucket with LRU eviction
handler = guard.RateLimit(guard.RateLimitConfig{
    Rate:    100,
    Window:  time.Minute,
    KeyFunc: guard.RemoteAddr(),
    MaxKeys: 10000, // LRU eviction when exceeded
})(handler)

// Body size limit — rejects oversized payloads
handler = guard.MaxBody(2 * 1024 * 1024)(handler) // 2MB max

// CORS — Cross-Origin Resource Sharing
handler = guard.CORS(guard.CORSConfig{
    AllowOrigins:     []string{"https://app.example.com"},
    AllowMethods:     []string{"GET", "POST", "PUT", "DELETE"},
    AllowHeaders:     []string{"Authorization", "Content-Type"},
    MaxAge:           24 * time.Hour,
    AllowCredentials: true,
})(handler)

// Security headers — sets secure defaults
handler = guard.SecurityHeaders(guard.DefaultSecurityHeaders)(handler)

// IP filtering — allow/deny by CIDR
handler = guard.IPFilter(guard.IPFilterConfig{
    Allow: []string{"10.0.0.0/8", "172.16.0.0/12"},
    Deny:  []string{"10.0.0.1/32"}, // deny takes precedence
})(handler)
```

**Available middleware**:
- `guard.Timeout(d)` — sets context deadline, buffers response, returns 504 Gateway Timeout with RFC 9457 Problem Details if deadline fires.
- `guard.RateLimit(cfg)` — per-key token bucket rate limiting with LRU eviction. Returns 429 Too Many Requests with Problem Details on limit exceeded. `MaxKeys` controls the LRU capacity.
- `guard.MaxBody(maxBytes)` — rejects requests with `Content-Length` exceeding the limit with 413 Payload Too Large. Wraps the body with `http.MaxBytesReader` as a safety net.
- `guard.CORS(cfg)` — handles CORS preflight (204) and sets Access-Control headers on matching origins. Panics if `AllowCredentials` is used with wildcard origin.
- `guard.SecurityHeaders(cfg)` — sets security headers (CSP, HSTS, X-Frame-Options, etc.). Use `guard.DefaultSecurityHeaders` for secure defaults.
- `guard.IPFilter(cfg)` — filters requests by client IP using CIDR allow/deny lists. Deny rules are evaluated first and take precedence. Returns 403 Forbidden on rejection.

**Key extraction functions**:
- `guard.RemoteAddr()` — uses the request's remote address (without port).
- `guard.XForwardedFor(trustedCIDRs...)` — reads client IP from `X-Forwarded-For`, falling back to `RemoteAddr` if the immediate peer is not in a trusted CIDR range.
- `guard.HeaderKey(header)` — uses the value of any request header as the rate limit key.

**Integration notes**:
- Place `Timeout` inside `Recovery` but outside `Logging` so timeouts are caught and logged properly.
- Rate limit state is in-memory per process. In multi-replica deployments, each replica enforces its own limit.
- All guard middleware returns RFC 9457 Problem Details JSON on rejection for consistency with `httpkit.JSONError`.
- All guard constructors validate their config at construction time and panic on invalid values (e.g., zero rate, nil KeyFunc).

---

### flagz — Feature flags

**When to use**: You need feature flags with percentage rollouts, multiple sources, and OTel tracing integration.

```go
import "github.com/ai8future/chassis-go/v10/flagz"

// Create flags from environment variables (FLAG_NEW_UI=true → "new-ui")
src := flagz.FromEnv("FLAG")

// Or from a JSON file
src := flagz.FromJSON("/etc/flags.json")

// Or from multiple sources (later overrides earlier)
src := flagz.Multi(
    flagz.FromJSON("/etc/defaults.json"),
    flagz.FromEnv("FLAG"),
)

flags := flagz.New(src)

// Simple boolean check
if flags.Enabled("new-ui") {
    renderNewUI()
}

// Percentage rollout with consistent bucketing
if flags.EnabledFor(ctx, "experiment", flagz.Context{
    UserID:  userID,
    Percent: 25, // 25% of users
}) {
    showExperiment()
}

// Variant (raw string value)
theme := flags.Variant("theme", "light") // "light" is the default
```

**Sources**:
- `flagz.FromEnv(prefix)` — reads env vars at construction. `FLAG_NEW_THING=true` maps to flag `"new-thing"`.
- `flagz.FromMap(m)` — in-memory map, useful for testing.
- `flagz.FromJSON(path)` — reads a flat JSON object from a file.
- `flagz.Multi(sources...)` — layers sources; later sources override earlier.

**Integration notes**:
- `EnabledFor` uses FNV-1a hashing of flag name + user ID for consistent percentage bucketing. The same user always gets the same result for the same flag.
- When OTel is initialized, `EnabledFor` records `flag.evaluation` span events with flag name, enabled status, and user ID.
- Flag sources are read at construction time (not on each lookup). For dynamic flags, create a new `Flags` instance when the source changes.

---

### logz — Structured JSON logging

**When to use**: You want structured JSON logging via `log/slog` with automatic trace ID injection.

```go
logger := logz.New("info") // "debug", "info", "warn", "error"

// Trace IDs are injected automatically from OTel span context.
// Use httpkit.Tracing() or grpckit.UnaryTracing() to set up spans at ingress.
logger.InfoContext(ctx, "handling request", "path", "/api/users")
// Output includes: {"trace_id":"...", "span_id":"...", "msg":"handling request", ...}
```

**Integration notes**:
- `logz.New` returns a standard `*slog.Logger`. Every package in your codebase that accepts `*slog.Logger` works with it unchanged.
- If you already have a logger, you can use chassis packages that accept `*slog.Logger` with your own logger instance. There is no coupling to `logz`.
- Trace IDs are read automatically from the OTel span context. Use `httpkit.Tracing()` or `grpckit.UnaryTracing()` middleware at your ingress point — downstream log calls that use `InfoContext`/`ErrorContext` will include `trace_id` and `span_id` automatically.

---

### lifecycle — Graceful shutdown orchestration

**When to use**: Your service runs multiple long-lived components (HTTP server, gRPC server, background workers) that need coordinated startup and shutdown.

`lifecycle.Run()` automatically initializes the `registry` module — every service is registered at `/tmp/chassis/` on startup, with automatic heartbeat every 30s and command polling every 3s. Register custom commands via `registry.Handle()` before calling `Run`.

```go
err := lifecycle.Run(context.Background(),
    func(ctx context.Context) error {
        // HTTP server — must respect ctx.Done()
        errCh := make(chan error, 1)
        go func() { errCh <- httpServer.ListenAndServe() }()
        select {
        case <-ctx.Done():
            return httpServer.Shutdown(context.Background())
        case err := <-errCh:
            return err
        }
    },
    func(ctx context.Context) error {
        // Background worker
        for {
            select {
            case <-ctx.Done():
                return nil
            case job := <-jobCh:
                process(job)
            }
        }
    },
)
```

**Behavior**:
- Catches SIGTERM and SIGINT, cancels the shared context.
- If any component returns an error, all others are signalled to stop.
- Uses `errgroup` under the hood — returns the first non-nil error.
- `Run` accepts `Component` values or bare `func(ctx context.Context) error` functions. Unsupported argument types cause a panic at startup.

**Integration notes**:
- Every component function **must** watch `ctx.Done()`. A component that ignores the context will block shutdown indefinitely.
- `http.Server.ListenAndServe()` does not respect context cancellation — you need the goroutine + select pattern shown above. `grpc.Server.Serve()` is the same; use `GracefulStop()` on context cancellation.
- If you already have a shutdown manager, `lifecycle.Run` is just a convenience. You can use the other chassis packages without it.

---

### registry — File-based service registration

**When to use**: Automatically — `lifecycle.Run()` initializes the registry for you. Use the module-level API to report status, log errors, and register custom commands. Registry initialization is mandatory. Calling `Status()`, `Errorf()`, or any post-lifecycle chassis module without an active registry will crash the process.

The registry creates a directory per service under `/tmp/chassis/<service-name>/` containing:
- `<pid>.json` — Registration file (written atomically on startup, removed on clean shutdown)
- `<pid>.log.jsonl` — Structured event log (startup, heartbeat, status, error, command, shutdown events)
- `<pid>.cmd.json` — Command file consumed by the service (written by external tools)

Registration file structure:
```json
{
  "name": "serp_svc",
  "pid": 48231,
  "hostname": "z1",
  "started_at": "2026-03-07T14:22:01Z",
  "version": "2.4.1",
  "language": "go",
  "chassis_version": "10.0.1",
  "args": ["./serp_svc"],
  "base_port": 12847,
  "ports": [
    {"port": 12847, "role": "http", "proto": "http", "label": "REST API"},
    {"port": 12848, "role": "grpc", "proto": "h2c", "label": "gRPC API"},
    {"port": 12849, "role": "metrics", "proto": "http", "label": "Prometheus metrics"}
  ],
  "commands": [
    {"name": "stop", "description": "Graceful shutdown", "builtin": true},
    {"name": "restart", "description": "Restart with same arguments", "builtin": true},
    {"name": "flush-cache", "description": "Clear all cached data"}
  ]
}
```

- `base_port` — the djb2-derived deterministic port for this service name (always present)
- `ports` — declared via `registry.Port()` before `lifecycle.Run()`
- `args` — captures `os.Args` for restart support

```go
import "github.com/ai8future/chassis-go/v10/registry"

// Report status events (written to the service log)
registry.Status("batch processing started")

// Report errors
registry.Errorf("connection to %s failed: %v", host, err)

// Declare open ports for operational visibility (call before lifecycle.Run)
registry.Port(chassis.PortHTTP, httpPort, "REST API")
registry.Port(chassis.PortGRPC, grpcPort, "gRPC API")
registry.Port(chassis.PortMetrics, metricsPort, "Prometheus metrics")

// Override protocol if needed (defaults: http→"http", grpc→"h2c", metrics→"http")
registry.Port(chassis.PortGRPC, grpcPort, "gRPC API", registry.Proto("h2"))

// Register custom commands (call before lifecycle.Run)
registry.Handle("flush-cache", "Clear all cached data", func() error {
    return cache.Flush()
})

// Check if a restart was requested (useful after lifecycle.Run returns)
if registry.RestartRequested() {
    // re-exec the process
}
```

**Built-in commands** (always available):
- `stop` — triggers graceful shutdown via context cancellation
- `restart` — sets the restart flag and triggers shutdown

To send a command to a running service, write a JSON file to `<pid>.cmd.json`:
```json
{"command": "flush-cache", "issued_at": "2026-03-07T12:00:00Z"}
```

**Automatic behavior** (managed by `lifecycle.Run()`):
- Heartbeat event logged every 30 seconds (`DefaultHeartbeatInterval`)
- Command file polled every 3 seconds (`DefaultCmdPollInterval`)
- Stale PID files from dead processes cleaned up on startup
- Shutdown event logged with uptime when the service exits

**Integration notes**:
- The registry uses only the stdlib — zero chassis dependencies. It is safe to import anywhere.
- The service name comes from `CHASSIS_SERVICE_NAME` env var, falling back to `filepath.Base(os.Getwd())`.
- The service version is read from a `VERSION` file in the working directory.
- `Status()` and `Errorf()` crash the process if called before `Init()` (i.e., before `lifecycle.Run()`). Registry is mandatory for all chassis services.
- Custom command handlers registered via `Handle()` must be registered before `lifecycle.Run()` is called.

---

### CLI/batch mode

For CLI tools and batch processes that aren't long-running services but still need visibility:

```go
func main() {
    chassis.RequireMajor(10)
    cfg := config.MustLoad[BatchConfig]()

    if err := registry.InitCLI(chassis.Version); err != nil {
        log.Fatal(err)
    }
    defer registry.ShutdownCLI(0)

    registry.Status("starting import")

    for i, record := range records {
        if registry.StopRequested() {
            registry.ShutdownCLI(1)
            os.Exit(1)
        }
        if err := process(record); err != nil {
            failures++
            registry.Errorf("record %d failed: %v", i, err)
        }
        registry.Progress(i+1, len(records), failures)
    }

    registry.Status("import complete")
}
```

CLI mode differs from service mode:
- **No heartbeat** — frozen detection via stale progress timestamps
- **PID file persists** — rewritten with completion status on exit, kept for 24h
- **Flags captured** — CLI arguments parsed and stored for viewer visibility
- **Stop support** — command polling runs; check `StopRequested()` in your loop

---

### httpkit — HTTP middleware

**When to use**: You need request ID generation, structured request logging, panic recovery, or standard JSON error responses for an HTTP service.

```go
mux := http.NewServeMux()
mux.HandleFunc("GET /healthz", healthHandler)
mux.HandleFunc("GET /api/users", listUsers)

// Stack middleware: outermost wraps innermost
handler := httpkit.Recovery(logger)(
    httpkit.RequestID(
        httpkit.Logging(logger)(mux),
    ),
)

http.ListenAndServe(":8080", handler)
```

**Available middleware**:
- `httpkit.RequestID` — generates a UUID v4 request ID, sets `X-Request-ID` header, stores in context.
- `httpkit.Logging(logger)` — logs method, path, status, duration per request.
- `httpkit.Recovery(logger)` — catches panics, logs with stack trace, returns 500 JSON error.
- `httpkit.Tracing()` — creates OTel spans for each request, extracting W3C TraceContext from incoming headers. Requires `otel.Init()` for real spans; no-op otherwise.

**Utilities**:
- `httpkit.JSONError(w, r, statusCode, message)` — writes an RFC 9457 Problem Details JSON response (`{"type": "...", "title": "...", "status": N, "detail": "...", "instance": "/path"}`). When a request ID is present in context, it appears as a top-level `request_id` extension member per RFC 9457.
- `httpkit.JSONProblem(w, r, serviceError)` — writes a `ServiceError` directly as RFC 9457 Problem Details.
- `httpkit.RequestIDFrom(ctx)` — retrieves the request ID from context (useful in your handlers).

**Integration notes**:
- These are standard `func(http.Handler) http.Handler` middleware. They compose with any router (chi, gorilla/mux, stdlib ServeMux).
- The `responseWriter` wrapper implements `Unwrap()`, so `http.NewResponseController` can still access `Flusher` and `Hijacker` on the underlying writer. SSE and WebSocket upgrades work through the middleware stack.
- Recommended middleware order (outermost first): Recovery → Tracing → RequestID → Logging → your routes. Recovery should be outermost so it catches panics from all other middleware.

---

### grpckit — gRPC interceptors

**When to use**: You run a gRPC server and want logging, panic recovery, metrics, tracing, and health check wiring.

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

// Wire up standard gRPC health check
grpckit.RegisterHealth(srv, health.CheckFunc(checks))
```

**Integration notes**:
- Recovery interceptors log the panic value **and full stack trace**, then return `codes.Internal`.
- Place recovery interceptors first in the chain so they catch panics from all downstream interceptors and handlers.
- `grpckit.RegisterHealth` decouples gRPC from the `health` package. It accepts any `func(ctx context.Context) error` — you can wire in your own health logic without importing `health`.
- The metrics interceptors record per-RPC OTel histograms (`rpc.server.duration`) using the configured `otel.MeterProvider`.

---

### health — Health checks

**When to use**: You need a `/healthz` endpoint or gRPC health check that aggregates multiple dependency checks.

```go
checks := map[string]health.Check{
    "postgres": func(ctx context.Context) error {
        return db.PingContext(ctx)
    },
    "redis": func(ctx context.Context) error {
        return redisClient.Ping(ctx).Err()
    },
}

// HTTP handler — returns JSON with per-check results
mux.Handle("GET /healthz", health.Handler(checks))

// Or use programmatically
runAll := health.All(checks)
results, err := runAll(ctx)
```

**Behavior**:
- All checks run in parallel via `work.Map`.
- Returns 200 + `{"status":"healthy"}` when all pass.
- Returns 503 + `{"status":"unhealthy","checks":[...]}` when any fail.
- Individual check failures don't short-circuit other checks.

**Integration notes**:
- Health checks should be fast. Set timeouts on the context you pass, or use a context with deadline in your check functions.
- The `health.Check` type is just `func(ctx context.Context) error`. Wrap any existing health check function to match.
- Use `health.CheckFunc(checks)` to get a simple `func(ctx) error` suitable for passing directly to `grpckit.RegisterHealth`:
  ```go
  grpckit.RegisterHealth(srv, health.CheckFunc(checks))
  ```

---

### work — Structured concurrency

**When to use**: You have fan-out/fan-in workloads — batch processing, parallel dependency checks, racing fallback strategies, or streaming pipelines — and need bounded concurrency with OTel tracing.

```go
import "github.com/ai8future/chassis-go/v10/work"

// Map: apply a function to each item with bounded concurrency
results, err := work.Map(ctx, userIDs, func(ctx context.Context, id string) (*User, error) {
    return fetchUser(ctx, id)
}, work.Workers(10))
// results is []User in input order; err is *work.Errors if any failed

// All: run heterogeneous tasks concurrently
err := work.All(ctx, []func(context.Context) error{
    func(ctx context.Context) error { return migrateDB(ctx) },
    func(ctx context.Context) error { return warmCache(ctx) },
}, work.Workers(3))

// Race: first success wins, remaining tasks cancelled
result, err := work.Race(ctx,
    func(ctx context.Context) (string, error) { return fetchFromPrimary(ctx) },
    func(ctx context.Context) (string, error) { return fetchFromReplica(ctx) },
)

// Stream: process channel items with bounded concurrency
out := work.Stream(ctx, inputCh, func(ctx context.Context, item Job) (Result, error) {
    return process(ctx, item)
}, work.Workers(5))
for r := range out {
    if r.Err != nil { log.Error("failed", "index", r.Index, "err", r.Err) }
}
```

**Patterns**:
- `Map[T, R]` — ordered fan-out/fan-in. Returns `[]R` in input order. On partial failure returns both results and `*work.Errors`.
- `All` — heterogeneous tasks (no input slice). Returns `*work.Errors` if any fail.
- `Race[R]` — first success wins, context cancelled for losers. All fail → `*work.Errors`.
- `Stream[T, R]` — channel-based pipeline. Sends `Result[R]` to output channel as items complete. Output channel closes when input closes and all workers finish.

**Integration notes**:
- Default worker count is `runtime.NumCPU()`. Override with `work.Workers(n)`.
- Every function creates an OTel parent span (`work.Map`, `work.All`, `work.Race`, `work.Stream`) with per-item child spans. Span attributes include `work.total`, `work.succeeded`, `work.failed`, and `work.pattern`.
- If no `TracerProvider` is configured, spans are no-ops — graceful degradation.
- All functions call `chassis.AssertVersionChecked()` internally. No separate version gate is needed per call, but `RequireMajor(10)` must have been called once at startup.
- `*work.Errors` implements `Unwrap() []error` for use with `errors.Is`/`errors.As`.

---

### call — Resilient HTTP client

**When to use**: Your service makes outbound HTTP calls and you want retries, circuit breaking, and timeout enforcement.

```go
client := call.New(
    call.WithTimeout(5 * time.Second),
    call.WithRetry(3, 500 * time.Millisecond),
    call.WithCircuitBreaker("user-service", 5, 30 * time.Second),
)

req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
resp, err := client.Do(req)
```

**Behavior**:
- Retries on 5xx responses and network errors. Never retries 4xx.
- Exponential backoff with random jitter between retries.
- Circuit breaker opens after N consecutive failures, rejects immediately for the reset duration, then allows a single probe request to test recovery.
- Respects context deadlines and cancellation.

**Integration notes**:
- Circuit breakers are singletons keyed by name. If multiple `call.Client` instances use the same breaker name, they share state. Use distinct names for distinct downstream services.
- If you have a custom circuit breaker (e.g., wrapping sony/gobreaker), implement the `call.Breaker` interface and use `call.WithBreaker(yourBreaker)`.
- The client returns the raw `*http.Response` — you are responsible for closing the body.
- **Retry body constraint**: Retries re-send the same `*http.Request`. For requests with a non-nil body, the body must be rewindable (implement `GetBody`) or the retry will send an empty/consumed body. Bodiless requests (GET, DELETE, HEAD) are always safe to retry.

**Batch requests**:

```go
reqs := []*http.Request{req1, req2, req3}
responses, err := client.Batch(ctx, reqs, work.Workers(5))
// responses is []*http.Response in input order
// err is *work.Errors if any requests failed
```

`Batch` uses `work.Map` internally — each request goes through the full client pipeline (tracing, circuit breaker, retry).

---

### testkit — Test helpers

**When to use**: Writing tests for code that uses chassis packages.

```go
func TestMyHandler(t *testing.T) {
    // Logger that writes to t.Log (suppressed unless -v)
    logger := testkit.NewLogger(t)

    // Set env vars with automatic cleanup
    testkit.SetEnv(t, map[string]string{
        "PORT":     "0",
        "LOG_LEVEL": "debug",
    })
    cfg := config.MustLoad[AppConfig]()

    // Get a free port for parallel test isolation
    port, _ := testkit.GetFreePort()
}
```

**Integration notes**:
- `testkit.NewLogger` writes to `t.Log`, so output is captured per-test and only shown on failure (or with `-v`).
- `testkit.SetEnv` calls `t.Setenv` for each key-value pair, providing automatic cleanup. It is a convenience for setting multiple vars at once.
- `testkit.GetFreePort` asks the OS for an available port. There is a small TOCTOU window between getting the port and binding to it, but it's reliable for tests.

---

### deploy — Convention-based deploy directory

**When to use**: Your service needs to discover deploy-time configuration, environment files, TLS certificates, or feature flags from a convention-based directory layout. Also provides runtime environment detection, endpoint declarations, dependency topology, and structured health payloads.

**Discovery**:

```go
import "github.com/ai8future/chassis-go/v10/deploy"

d := deploy.Discover("my-service")
if d.Found() {
    fmt.Println("deploy dir:", d.Dir())
}
```

Search order (first match wins):
1. `$CHASSIS_DEPLOY_DIR` (env var override)
2. `/app/deploy/<name>/` (K8s convention)
3. `~/deploy/<name>/` (developer workstation)
4. `/deploy/<name>/` (system-level)

**deploy.json format** (with chassis spec version):

```json
{
  "chassis": "10.0",
  "version": "2.4.1",
  "environment": {
    "env": "production",
    "provider": "aws",
    "region": "us-east-1",
    "cluster": "prod-01"
  },
  "endpoints": {
    "api": {"port": 8080, "protocol": "http", "path": "/v1"},
    "grpc": {"port": 9090, "protocol": "h2c"}
  },
  "dependencies": [
    {"service": "postgres", "port": 5432, "protocol": "tcp"},
    {"service": "redis", "port": 6379, "required": false}
  ]
}
```

**Key methods**:

```go
// Spec version from deploy.json ("chassis" field, defaults to "8.0" for pre-v9 files)
spec := d.Spec() // "10.0"

// Runtime environment detection + deploy.json env block + env var overrides
env := d.Environment()
// env.Runtime: "kubernetes", "container", "vm", or "bare-metal"
// env.Env, env.Provider, env.Region, env.Cluster (overridden by CHASSIS_ENV, etc.)
// env.Namespace, env.PodName (auto-detected in Kubernetes)

// Typed endpoint objects from deploy.json
endpoints := d.Endpoints()          // map[string]Endpoint
api, ok := d.Endpoint("api")       // single lookup
// api.Port, api.Protocol (default "http"), api.Path

// Service dependency topology
deps := d.Dependencies()
// deps[i].Service, deps[i].Required (*bool, defaults to true)

// Structured health payload
status := d.Health(map[string]string{
    "database": "ok",
    "cache":    "ok",
})
// status.Service, status.Version, status.ChassisSpec, status.Runtime,
// status.Uptime (float64 seconds), status.Endpoints, status.Components
```

**Typical usage**:

```go
func main() {
    chassis.RequireMajor(10)

    d := deploy.Discover("my-service")
    d.LoadEnv() // load config.env and secrets.env into os env

    cfg := config.MustLoad[AppConfig]()
    logger := logz.New(cfg.LogLevel)

    env := d.Environment()
    logger.Info("starting", "runtime", env.Runtime, "env", env.Env)

    // Use endpoints for service registration
    endpoints := d.Endpoints()
    logger.Info("endpoints", "count", len(endpoints))

    // Health endpoint returns structured payload
    mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
        status := d.Health(map[string]string{"self": "ok"})
        json.NewEncoder(w).Encode(status)
    })
}
```

**Integration notes**:
- `deploy` is a utility module — it works without `lifecycle.Run()` or the registry.
- No deploy.json caching — each method re-reads the file. This keeps behavior simple and consistent.
- `LoadEnv()` never overwrites existing env vars — explicit env vars always take precedence.
- Environment variable overrides (`CHASSIS_ENV`, `CHASSIS_PROVIDER`, `CHASSIS_REGION`, `CHASSIS_CLUSTER`) always take highest priority, above deploy.json values.
- `Dependency.Required` uses `*bool` in Go — `nil` defaults to `true`. Set explicitly to `false` for optional dependencies.

---

## Common integration patterns

### Minimal HTTP service

```go
func main() {
    chassis.RequireMajor(10)
    cfg := config.MustLoad[ServiceConfig]()
    logger := logz.New(cfg.LogLevel)

    mux := http.NewServeMux()
    mux.Handle("GET /healthz", health.Handler(map[string]health.Check{
        "self": func(_ context.Context) error { return nil },
    }))
    mux.HandleFunc("GET /api/data", handleData)

    handler := httpkit.Recovery(logger)(
        httpkit.RequestID(
            httpkit.Logging(logger)(mux),
        ),
    )

    srv := &http.Server{Addr: fmt.Sprintf(":%d", cfg.Port), Handler: handler}

    lifecycle.Run(context.Background(), func(ctx context.Context) error {
        errCh := make(chan error, 1)
        go func() { errCh <- srv.ListenAndServe() }()
        select {
        case <-ctx.Done():
            return srv.Shutdown(context.Background())
        case err := <-errCh:
            return err
        }
    })
}
```

### Service calling another service

```go
// Build one client per downstream service, share across handlers.
var userClient = call.New(
    call.WithTimeout(3 * time.Second),
    call.WithRetry(2, 200 * time.Millisecond),
    call.WithCircuitBreaker("user-svc", 5, 30 * time.Second),
)

func getUser(ctx context.Context, id string) (*User, error) {
    req, err := http.NewRequestWithContext(ctx, http.MethodGet,
        fmt.Sprintf("http://user-svc:8080/users/%s", id), nil)
    if err != nil {
        return nil, err
    }
    resp, err := userClient.Do(req)
    if err != nil {
        return nil, err // includes ErrCircuitOpen
    }
    defer resp.Body.Close()
    // ...
}
```

### Adoption for new services vs. existing codebases

**New services**: Import the full toolkit from the start. The packages are designed as a coherent set — config, logging, lifecycle, transports, health, and resilient clients cover the foundational layer of any service. Start with the "Minimal HTTP service" pattern above and add gRPC and outbound clients as needed.

**Existing codebases migrating incrementally**: If you're retrofitting chassis into a service that already has its own config loader, logger, etc., a reasonable migration order is:

1. **config + logz** — Replace scattered `os.Getenv` calls and `log.Printf` with structured alternatives.
2. **httpkit + health** — Drop middleware into your existing handler chain and add a health endpoint.
3. **lifecycle** — Replace your shutdown logic with coordinated orchestration.
4. **call** — Swap raw `http.Client` usage for resilient outbound calls.
5. **grpckit** — Wire up interceptors and health when adding or migrating to gRPC.

But the goal is to get to full adoption. Each package is designed with the assumption that the others are present.

---

## XYOps integration (STRONGLY RECOMMENDED)

**All services and CLI tools that use chassis SHOULD integrate with xyops.** This is not optional boilerplate — xyops provides centralized job management, monitoring, alerting, and operational visibility across the entire infrastructure. A service that uses chassis but not xyops is invisible to operations. Integrate it.

There are two integration patterns: the **client** (all services and CLI tools) and the **worker** (services that execute jobs dispatched by xyops).

### xyops client — Every service should have this

The xyops client gives your service the ability to trigger events, check job status, acknowledge alerts, and — critically — push health and application metrics to xyops for monitoring and alerting.

**Config env vars** (add to your config struct):

```go
type Config struct {
    // ... your existing fields ...
    Xyops xyops.Config // embedded xyops config
}
```

Required env vars:
- `XYOPS_BASE_URL` — xyops server URL (e.g. `https://xyops.example.com:5522`)
- `XYOPS_API_KEY` — API key for authentication

Optional env vars:
- `XYOPS_SERVICE_NAME` — service name for monitoring (defaults to chassis service name)
- `XYOPS_MONITOR_ENABLED` — enable the monitoring bridge (default `false`)
- `XYOPS_MONITOR_INTERVAL` — seconds between metric pushes (default `30`)

**Minimal integration** (connect to xyops, enable monitoring):

```go
import (
    "github.com/ai8future/chassis-go/v10/xyops"
)

func main() {
    chassis.RequireMajor(10)
    cfg := config.MustLoad[Config]()
    logger := logz.New(cfg.LogLevel)

    ops := xyops.New(cfg.Xyops,
        xyops.WithMonitoring(cfg.Xyops.MonitorInterval),
    )

    // Add ops.Run as a lifecycle component — it pushes metrics to xyops
    lifecycle.Run(ctx,
        httpServer,
        ops.Run,
    )
}
```

**With bridged application metrics** (recommended):

```go
ops := xyops.New(cfg.Xyops,
    xyops.WithMonitoring(30),
    xyops.BridgeMetric("request_latency_p99", latencyGauge),
    xyops.BridgeMetric("queue_depth", queueGauge),
    xyops.BridgeMetric("error_rate", errorCounter),
)
```

Bridged metrics become xyops custom monitors and can trigger alert expressions. This is how operations knows your service is healthy.

**Using the curated API** in handlers:

```go
// Trigger an event
jobID, err := ops.RunEvent(ctx, "deploy-prod", map[string]string{
    "version": r.URL.Query().Get("v"),
})

// Check job status (cached — safe to poll frequently)
status, err := ops.GetJobStatus(ctx, jobID)

// Cancel a job
err := ops.CancelJob(ctx, jobID)

// List active alerts
alerts, err := ops.ListActiveAlerts(ctx)

// Acknowledge an alert
err := ops.AckAlert(ctx, alertID)

// Escape hatch for any xyops API endpoint
resp, err := ops.Raw(ctx, "GET", "/api/custom/endpoint", nil)
```

### xyops worker — Services that execute jobs

If your service receives and executes jobs from xyops (deployments, migrations, batch processing, etc.), add the worker module.

**Config env vars:**

```go
type Config struct {
    // ... your existing fields ...
    Worker xyopsworker.Config
}
```

Required env vars:
- `XYOPS_WORKER_MASTER_URL` — WebSocket URL (e.g. `wss://xyops.example.com:5523`)
- `XYOPS_WORKER_SECRET_KEY` — satellite authentication key

Optional env vars:
- `XYOPS_WORKER_HOSTNAME` — worker hostname (defaults to OS hostname)
- `XYOPS_WORKER_GROUPS` — comma-separated xyops server groups to join
- `XYOPS_WORKER_SHELL_ENABLED` — allow shell execution fallback (default `false`)

```go
import (
    "github.com/ai8future/chassis-go/v10/xyopsworker"
)

func main() {
    chassis.RequireMajor(10)
    cfg := config.MustLoad[Config]()

    worker := xyopsworker.New(cfg.Worker)

    worker.Handle("deploy", func(ctx context.Context, job xyopsworker.Job) error {
        job.Log("Starting deployment for " + job.Params["environment"])
        job.Progress(50, "Building image...")
        // ... do the work ...
        job.SetOutput("deployed version 2.4.1")
        return nil
    })

    worker.Handle("db-migrate", migrateHandler)

    lifecycle.Run(ctx, worker.Run)
}
```

### CLI tools and batch processes

CLI tools and batch processes should also integrate the xyops client. Even without the monitoring bridge, having the ability to trigger events and check job status from batch tools is valuable for operational workflows.

```go
func main() {
    chassis.RequireMajor(10)
    cfg := config.MustLoad[BatchConfig]()

    if err := registry.InitCLI(chassis.Version); err != nil {
        log.Fatal(err)
    }
    defer registry.ShutdownCLI(0)

    // Create xyops client (no monitoring bridge needed for CLI)
    ops := xyops.New(cfg.Xyops)

    // Trigger a job and wait for it
    jobID, _ := ops.RunEvent(ctx, "nightly-cleanup", nil)
    for {
        status, _ := ops.GetJobStatus(ctx, jobID)
        registry.Progress(status.Progress, 100, 0)
        if status.State == "completed" || status.State == "failed" {
            break
        }
        time.Sleep(5 * time.Second)
    }
}
```

### Integration notes

- The xyops client uses `call` under the hood — you get retry, circuit breaking, and OTel tracing on all API calls automatically.
- `GetJobStatus` results are cached (5 min TTL, 500 entries) to avoid redundant API calls when polling.
- `FireWebhook` delegates to `webhook.Sender` — HMAC signed, retried, delivery tracked.
- `ops.Run` is safe to include in `lifecycle.Run` even when monitoring is disabled — it blocks until context cancellation.
- The worker's `Dispatch` method can be called directly in tests without a WebSocket connection.

---

## Event Bus Integration

For publishing and subscribing to the Redpanda event bus using kafkakit, schemakit, tracekit, heartbeatkit, and announcekit, see the cross-language integration guide:

- **[chassis-docs/07-event-bus-integration.md](../chassis-docs/07-event-bus-integration.md)** — kafkakit API patterns, error handling, entity refs, automatic behaviors, schema formalization
- **[chassis-docs/docs/conventions/event-naming.md](../chassis-docs/docs/conventions/event-naming.md)** — subject naming rules, tenant IDs, payload design guidelines

### Quick Reference (Go)

```go
// Publisher
pub, err := kafkakit.NewPublisher(cfg.Kafkakit)
defer pub.Close()

if err := pub.Publish(ctx, "ai8.suite.service.noun.verb", data); err != nil {
    slog.Warn("failed to publish event", "error", err)
}

// Subscriber
sub, err := kafkakit.NewSubscriber(cfg.Kafkakit, "my-service-group")
sub.Subscribe("ai8.suite.service.noun.verb", func(ctx context.Context, event kafkakit.Event) error {
    return nil
})
sub.Start(ctx)
```

heartbeatkit and announcekit auto-activate when kafkakit is configured via `lifecycle.Run(ctx, WithKafkaConfig(cfg), components...)`.

---

## Things to watch out for

**config panics are intentional.** `MustLoad` panics on missing required config. This is by design — configuration errors should crash the process at startup, not cause mysterious failures later. If you need softer error handling, validate env vars before calling `MustLoad`, or contribute a `Load` variant that returns errors.

**lifecycle.Run components must respect context.** If your component ignores `ctx.Done()`, the process will hang on shutdown. This is the most common integration mistake. Always test that your components exit cleanly when the context is cancelled.

**Circuit breakers are global singletons.** `call.GetBreaker("name", ...)` returns the same instance for the same name across your entire process. This is intentional — multiple HTTP clients hitting the same downstream should share circuit state. But it means breaker names are a global namespace. Use clear, service-specific names.

**Log the chassis version at startup.** Import the top-level `chassis` package and log `chassis.Version` during initialization. This makes it easy to correlate production issues with a specific library version and track which services have upgraded after a release.

**RequireMajor must be called first.** Every chassis module checks that `chassis.RequireMajor(N)` was called before it runs. If you skip it, you get a clear crash at startup. Place it as the first line in `main()`.

**secval errors are NOT ServiceError.** `secval.ValidateJSON` returns module-local errors (`ErrDangerousKey`, etc.), not `*errors.ServiceError`. Convert them at the handler boundary with `errors.ValidationError(err.Error())`.

**Registry enforcement is service-level, not package-level.** The registry crashes the process if `Status()` or `Errorf()` are called before `lifecycle.Run()`. The `httpkit` and `grpckit` middleware also enforce this — handling requests means you're a running service. However, utility modules (`work`, `call`, `health`, `config`, `logz`, `errors`, `secval`, `flagz`) do NOT require registry. They work fine in libraries, CLI tools, and non-service contexts. If your Go module uses chassis utilities internally, the consuming application only needs `RequireMajor(10)` — not `lifecycle.Run()` — unless it also uses httpkit/grpckit to handle requests.

**The toolkit has four external dependencies.** `golang.org/x/sync` (for errgroup), `golang.org/x/crypto` (for seal — scrypt KDF), `google.golang.org/grpc` (for grpckit and errors), and `go.opentelemetry.io/otel` (for otel, metrics, call, and work). If you only use Tier 1 packages (config, logz, lifecycle, testkit), only `x/sync` is pulled in.
