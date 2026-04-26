# Integrating chassis-go

Practical guide for teams adopting chassis-go into an existing Go codebase.

## Before you start

**Requirements**: Go 1.26.2+ (required for security patches in crypto/tls, crypto/x509, and html/template). The `go.mod` declares `go 1.25.5` for compatibility, but running on anything older than 1.26.2 leaves known CVEs unpatched. All consumer codebases **must** build and deploy with Go 1.26.2 or later.

**What this is**: A cohesive toolkit that covers the foundational concerns of a Go service — config, logging, lifecycle, HTTP, gRPC, health checks, and resilient outbound calls. Import the whole thing. Your `main()` stays yours.

**What this is not**: An opinionated framework. Chassis doesn't own your dependency injection, routing, or service mesh. It provides building blocks that you wire together explicitly.

**Service modules vs. utility modules**: Chassis modules fall into two categories. *Service modules* (`httpkit`, `grpckit`, `lifecycle`, `registry`) require a running service with `lifecycle.Run()` and an active registry — they crash if used without it. *Utility modules* (`config`, `logz`, `errors`, `call`, `work`, `health`, `secval`, `flagz`, `metrics`, `otel`, `testkit`, `cache`, `seal`, `tick`, `webhook`, `deploy`, `tracekit`, `schemakit`, `phasekit`) work anywhere — services, libraries, CLI tools. *Service client kits* (`registrykit`, `lakekit`) are HTTP clients for internal platform services — they work anywhere but require the target service to be reachable. *Event bus kits* (`kafkakit`, `heartbeatkit`, `announcekit`) require a Kafka/Redpanda broker. A Go module that imports chassis utilities can be consumed by any application that calls `RequireMajor(11)`.

## Installation

```bash
go get github.com/ai8future/chassis-go/v11
```

The top-level package exports the library version for diagnostics:

```go
import chassis "github.com/ai8future/chassis-go/v11"

logger.Info("starting", "chassis_version", chassis.Version)
```

### Version gate and app version

Every service must declare which major version of chassis it supports, and should provide its own app version for the `--version` flag and auto-rebuild freshness check.

**Step 1: Create `appversion.go` at your repo root** (next to `VERSION` and `go.mod`):

```go
package yourpkg

import (
    _ "embed"
    "strings"
)

//go:embed VERSION
var rawAppVersion string

var AppVersion = strings.TrimSpace(rawAppVersion)
```

**Step 2: Wire it up in every `cmd/*/main.go`:**

```go
func main() {
    chassis.SetAppVersion(yourpkg.AppVersion) // enables --version and auto-rebuild
    chassis.RequireMajor(11)                  // crashes if chassis major version != 11
    // ... rest of startup
}
```

This gives you:
- **`--version` flag**: `myservice --version` prints `myservice 1.2.3 (chassis-go 11.x.y)`
- **Auto-rebuild**: if the binary's compiled version is older than the VERSION file on disk, it automatically recompiles and re-execs. Opt out with `CHASSIS_NO_REBUILD=1`.

Do NOT copy or symlink VERSION into `cmd/` directories. `go:embed` rejects symlinks, and copies get out of sync. The root-package embed + import pattern above is the correct approach.

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
    "github.com/ai8future/chassis-go/v11/call"
    "github.com/ai8future/chassis-go/v11/config"
    "github.com/ai8future/chassis-go/v11/errors"
    "github.com/ai8future/chassis-go/v11/grpckit"
    "github.com/ai8future/chassis-go/v11/health"
    "github.com/ai8future/chassis-go/v11/httpkit"
    "github.com/ai8future/chassis-go/v11/lifecycle"
    "github.com/ai8future/chassis-go/v11/work"
    "github.com/ai8future/chassis-go/v11/logz"
    "github.com/ai8future/chassis-go/v11/metrics"
    "github.com/ai8future/chassis-go/v11/registry"
    "github.com/ai8future/chassis-go/v11/secval"
)
```

And in test files:

```go
import "github.com/ai8future/chassis-go/v11/testkit"
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

### phasekit - Phase-backed environment hydration

**When to use**: Your service already uses `config.MustLoad`, but secrets live
in Phase and should be fetched at startup instead of stored in `.env` files or
copied into the runtime image.

Call `phasekit.MustHydrate` after `chassis.RequireMajor(11)` and before
`config.MustLoad`:

```go
phasekit.MustHydrate(ctx, phasekit.Config{
    ServiceToken: os.Getenv("PHASE_SERVICE_TOKEN"),
    Host:         os.Getenv("PHASE_HOST"),
    App:          "myservice",
    Env:          "Production",
    RequiredKeys: []string{"DATABASE_URL", "JWT_SIGNING_KEY"},
})

cfg := config.MustLoad[AppConfig]()
```

Important behavior:
- Existing environment variables win by default. Set `OverwriteExisting: true`
  only when Phase should replace local or orchestrator-provided values.
- If the `phase` binary is missing, phasekit returns
  `Source: phasekit.SourceEnvFallback` and leaves the existing environment
  untouched. `config.MustLoad` then decides whether required env vars are
  present.
- `Path` is exact-match. Use `AllPaths: true` to fetch every Phase path.
- Dynamic secret lease generation is disabled in v1.
- Literal `[REDACTED]` values fail startup unless `AllowRedacted` is true.
- The runtime image must include the external `phase` CLI binary to hydrate
  from Phase.

See `INTEGRATING_PHASE.md` for Docker, CI, path, and troubleshooting guidance.

---

### errors — Unified service errors

**When to use**: You need error types that carry both HTTP and gRPC status codes for consistent error handling across transport layers.

```go
import "github.com/ai8future/chassis-go/v11/errors"

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
import "github.com/ai8future/chassis-go/v11/secval"

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
import "github.com/ai8future/chassis-go/v11/metrics"

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
import otelinit "github.com/ai8future/chassis-go/v11/otel"

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
import "github.com/ai8future/chassis-go/v11/guard"

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
import "github.com/ai8future/chassis-go/v11/flagz"

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
  "chassis_version": "11.0.0",
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
import "github.com/ai8future/chassis-go/v11/registry"

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
    chassis.SetAppVersion(yourpkg.AppVersion) // enables --version and auto-rebuild
    chassis.RequireMajor(11)
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
import "github.com/ai8future/chassis-go/v11/work"

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
- All functions call `chassis.AssertVersionChecked()` internally. No separate version gate is needed per call, but `RequireMajor(11)` must have been called once at startup.
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
import "github.com/ai8future/chassis-go/v11/deploy"

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
  "chassis": "11.0",
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
spec := d.Spec() // "11.0"

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
    chassis.SetAppVersion(yourpkg.AppVersion)
    chassis.RequireMajor(11)

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

## Addon packages — don't skip these

These packages provide important functionality that most services should consider adopting. They are utility modules — they work anywhere without `lifecycle.Run()` and have no registry dependency. **If you're building a service that handles data, secrets, periodic tasks, or outbound notifications, you almost certainly need one or more of these.**

Before reaching for a third-party library or writing your own implementation, check this table:

| You need to... | Use | Why not hand-roll it? |
|---|---|---|
| Cache hot-path data in memory | `cache` | Generic `Cache[K,V]` with LRU eviction + TTL expiry. Hand-rolled maps lack eviction bounds and eventually OOM under load. |
| Encrypt data at rest | `seal` | AES-256-GCM with scrypt KDF, self-describing `Envelope`. DIY crypto gets salt/IV/tag handling wrong — seal is audited and consistent across the toolkit. |
| Sign payloads or verify signatures | `seal` | HMAC-SHA256 with constant-time comparison via `crypto/subtle`. Naive `==` comparison leaks timing info. `webhook` uses `seal.Sign` internally — one signing standard everywhere. |
| Issue short-lived internal tokens | `seal` | Signed, expiring tokens with automatic `jti` and `exp` claims. Simpler than JWT, no algorithm negotiation attack surface, no external dependency. |
| Run periodic background tasks | `tick` | Returns a `func(ctx) error` that plugs directly into `lifecycle.Run`. Hand-rolled ticker goroutines forget jitter (thundering herd), ignore context cancellation (hung shutdown), or swallow errors silently. |
| Send outbound webhooks | `webhook` | HMAC-signed delivery with retries, delivery tracking, and receive-side verification. Rolling your own means re-implementing signing, retry backoff, idempotency headers, and status tracking — `webhook` does all four. |
| Propagate trace IDs across services | `tracekit` | Context-based trace propagation with HTTP middleware. Service client kits (`registrykit`, `lakekit`) use it automatically — hand-rolling means your traces break at service boundaries. |
| Validate and serialize Avro events | `schemakit` | Schema Registry integration with Confluent wire format. Hand-rolling means getting the 5-byte header wrong, cache misses on schema lookups, and no validation before publish. |
| Resolve or manage entities | `registrykit` | 5+ identifier resolution strategies, relationship traversal, hierarchy navigation, and entity mutations. Building this from HTTP calls means hundreds of lines of boilerplate per operation. |
| Query the data lake | `lakekit` | SQL queries, entity history, dataset catalog — all with tenant and trace headers. Same boilerplate argument as above. |
| Publish liveness signals | `heartbeatkit` | Auto-activates with kafkakit. Zero-config heartbeats with publisher stats enrichment. Without it, operations has no signal that your service is alive between health checks. |
| Announce service/job lifecycle | `announcekit` | Auto-activates with kafkakit. Structured events on well-known subjects. Operations dashboards and alerting rules depend on these — skipping them makes your service invisible to ops. |
| Product analytics | `posthogkit` (planned) | Batched capture, privacy hashing, no-op when disabled |
| Full-text search | `meilikit` (planned) | Meilisearch client, replaces searchkit addon |
| LLM inference | `inferkit` (planned) | OpenAI-compatible, replaces llm addon |
| Local LLM | `ollamakit` (planned) | Ollama native API, model management |
| Vector search | `qdrantkit` (planned) | Qdrant REST, filter builder, batch upsert |

### cache — In-memory TTL/LRU cache

**When to use**: You need a fast, bounded, concurrency-safe in-memory cache. Use it for API response caching, expensive computation memoization, or any hot-path data that benefits from local caching.

```go
import "github.com/ai8future/chassis-go/v11/cache"

// Create a typed cache with TTL and size limits
userCache := cache.New[string, *User](
    cache.MaxSize(5000),             // LRU eviction after 5000 entries
    cache.TTL(10 * time.Minute),     // entries expire after 10 minutes
    cache.Name("user-cache"),        // name for metrics/debugging
)

// Simple get/set
userCache.Set(userID, user)
if u, ok := userCache.Get(userID); ok {
    return u, nil
}

// Explicit deletion
userCache.Delete(userID)

// Remove all expired entries (useful on a tick interval)
removed := userCache.Prune()

// Check current size
size := userCache.Len()
```

**Behavior**:
- Generic types: `Cache[K comparable, V any]` — no type assertions needed.
- LRU eviction: when `MaxSize` is reached, the least-recently-used entry is evicted.
- TTL expiration: expired entries are removed lazily on `Get` or eagerly via `Prune`.
- Default max size is 1000 if not specified.
- Fully concurrency-safe — all operations are guarded by `sync.RWMutex`.

**Integration notes**:
- Pair with `tick.Every` to run periodic `Prune()` calls and prevent memory from growing between access patterns.
- Follow this same pattern for any frequently-polled external data.
- For distributed caching (multi-replica), use an external store (Redis, Memcached). This package is strictly in-process.

---

### seal — Encryption, signing, and temporary tokens

**When to use**: You need to encrypt sensitive data at rest, sign payloads for integrity verification, or create short-lived signed tokens for inter-service communication. **Any service handling secrets, API keys, or user tokens should use seal instead of rolling its own crypto.**

```go
import "github.com/ai8future/chassis-go/v11/seal"

// --- Encrypt / Decrypt (AES-256-GCM with scrypt KDF) ---
env, err := seal.Encrypt([]byte("sensitive data"), "my-passphrase")
// env is a self-describing Envelope (JSON-serializable)

plaintext, err := seal.Decrypt(env, "my-passphrase")

// --- HMAC-SHA256 Sign / Verify ---
sig := seal.Sign([]byte("payload"), "secret-key")
ok := seal.Verify([]byte("payload"), sig, "secret-key")

// --- Temporary signed tokens ---
token, err := seal.NewToken(seal.Claims{
    "user_id": "u_123",
    "role":    "admin",
}, "signing-secret", 3600) // expires in 1 hour

claims, err := seal.ValidateToken(token, "signing-secret")
// err is ErrTokenExpired, ErrTokenInvalid, or ErrSignature
```

**What it provides**:
- **Encryption**: AES-256-GCM with scrypt key derivation. The `Envelope` output is self-describing (includes algorithm, salt, IV, tag) and JSON-serializable for storage.
- **Signing**: HMAC-SHA256 with constant-time comparison via `crypto/subtle`.
- **Tokens**: Signed, expiring tokens with JSON claims. Similar to JWT but simpler — no header, no algorithm negotiation. Includes automatic `jti` (unique token ID) and `exp` (expiry) claims.

**Integration notes**:
- `webhook.Sender` uses `seal.Sign` internally for HMAC webhook signatures — the signing is consistent across the toolkit.
- Tokens are not JWTs. They are simpler and intentionally non-interoperable with external JWT validators. Use them for internal service-to-service auth, not external API tokens.
- The scrypt parameters (N=16384, r=8, p=1) are tuned for server-side use. Key derivation takes ~100ms — do not use `Encrypt`/`Decrypt` in hot paths. For hot-path signing, use `Sign`/`Verify` (HMAC is fast).
- Sentinel errors (`ErrDecrypt`, `ErrTokenExpired`, `ErrTokenInvalid`, `ErrSignature`) work with `errors.Is`.

---

### tick — Periodic task scheduling

**When to use**: You have recurring work — cache pruning, metric flushing, health polling, data syncing — that runs on a fixed interval alongside your service. **Most services have at least one periodic task. Use tick instead of writing your own ticker-in-a-goroutine.**

```go
import "github.com/ai8future/chassis-go/v11/tick"

// Create a periodic task and pass it to lifecycle.Run
pruner := tick.Every(5*time.Minute, func(ctx context.Context) error {
    removed := myCache.Prune()
    logger.Info("pruned cache", "removed", removed)
    return nil
}, tick.Label("cache-pruner"))

// Run immediately on startup, then on interval
syncer := tick.Every(30*time.Second, syncFromUpstream,
    tick.Immediate(),                  // run once before first tick
    tick.Jitter(5*time.Second),        // random 0–5s delay per tick
    tick.OnError(tick.Stop),           // stop on first error (default: Skip)
    tick.Label("upstream-sync"),
)

// Wire into lifecycle
lifecycle.Run(ctx,
    httpServer,
    pruner,    // runs as a lifecycle component
    syncer,
)
```

**Options**:
- `tick.Immediate()` — execute the function once immediately before the first interval tick.
- `tick.Jitter(d)` — add a random delay (0 to `d`) before each execution. Prevents thundering herd when multiple replicas tick at the same interval.
- `tick.OnError(tick.Skip)` — silently ignore the error and continue (default). Log inside your function if you need visibility.
- `tick.OnError(tick.Stop)` — return the error, which causes `lifecycle.Run` to shut down all components.
- `tick.Label(s)` — label for logging and debugging.

**Integration notes**:
- `tick.Every` returns a `func(context.Context) error` — it is directly compatible with `lifecycle.Run` as a component. No wrapping needed.
- The function respects context cancellation — it exits cleanly when `lifecycle.Run` signals shutdown.
- Combine with `cache.Prune()` for periodic cache cleanup, or `registry.Status()` for periodic status reporting.

---

### webhook — HMAC-signed webhook delivery

**When to use**: Your service sends outbound webhook notifications to external systems. **If you're building any kind of event notification, integration callback, or partner webhook, use this package.** It handles signing, retries, and delivery tracking so you don't have to.

```go
import "github.com/ai8future/chassis-go/v11/webhook"

// --- Sending webhooks ---
sender := webhook.NewSender(
    webhook.MaxAttempts(5), // default: 3
)

deliveryID, err := sender.Send(
    "https://partner.example.com/webhooks",
    map[string]any{"event": "order.completed", "order_id": "ord_123"},
    "shared-secret-key",
)

// Check delivery status
delivery, ok := sender.Status(deliveryID)
// delivery.Status: "delivered", "failed", or "pending"
// delivery.Attempts, delivery.LastError

// --- Receiving/verifying webhooks ---
func webhookHandler(w http.ResponseWriter, r *http.Request) {
    body, _ := io.ReadAll(r.Body)
    verified, err := webhook.VerifyPayload(r.Header, body, "shared-secret-key")
    if err != nil {
        // err is webhook.ErrBadSignature
        http.Error(w, "invalid signature", 401)
        return
    }
    // verified is the raw body, safe to unmarshal
}
```

**What it does**:
- **Signing**: Every outbound webhook includes `X-Webhook-Id`, `X-Webhook-Signature` (HMAC-SHA256 via `seal.Sign`), and `X-Webhook-Timestamp` headers.
- **Retries**: Retries on 5xx and network errors with linear backoff. Never retries 4xx (client errors are permanent).
- **Delivery tracking**: Every delivery is tracked in memory by ID with status, attempt count, and error details.
- **Verification**: `VerifyPayload` validates incoming webhook signatures on the receive side using constant-time comparison.

**Integration notes**:
- Delivery tracking is in-memory. For durable delivery logs, persist `Delivery` records to your database.
- The signature format is `sha256=<hex>` over `timestamp.body`, matching common webhook conventions (GitHub, Stripe, etc.).

---

## Service client kits

These packages provide typed HTTP clients for internal platform services. **If your service interacts with any of these platform services, use the corresponding kit instead of building raw HTTP calls.** All service client kits share a consistent pattern:

- Multi-tenant via `WithTenant(id)` — sets `X-Tenant-ID` on every request
- Automatic trace propagation via `tracekit` — sets `X-Trace-ID` on every request
- Configurable timeout via `WithTimeout(d)` — default 5 seconds
- `nil, nil` return on 404 (not-found is not an error)

```go
import (
    "github.com/ai8future/chassis-go/v11/registrykit"
    "github.com/ai8future/chassis-go/v11/lakekit"
)
```

### registrykit — Entity registry client (registry_svc)

**When to use**: Your service needs to resolve, create, or traverse entities and their relationships. Provides entity resolution by multiple identifier types, relationship traversal, graph queries, ancestor/descendant navigation, and entity mutations.

```go
reg := registrykit.NewClient("http://registry-svc:8080",
    registrykit.WithTenant("tenant-123"),
)

// Resolve by various identifiers
entity, err := reg.Resolve(ctx, "company", registrykit.ByDomain("example.com"))
entity, err := reg.Resolve(ctx, "person", registrykit.ByEmail("user@example.com"))
entity, err := reg.Resolve(ctx, "company", registrykit.ByCRD("CRD-12345"))
entity, err := reg.Resolve(ctx, "product", registrykit.BySlug("my-product"))
entity, err := reg.Resolve(ctx, "user", registrykit.ByIdentifier("github", "12345"))

// Relationship traversal
rels, err := reg.Related(ctx, entityID,
    registrykit.OfType("person"),
    registrykit.Rel("employs"),
    registrykit.AsOf(time.Now().Add(-30*24*time.Hour)), // 30 days ago
)

// Hierarchy navigation
children, err := reg.Descendants(ctx, entityID, registrykit.OfType("team"))
parents, err := reg.Ancestors(ctx, entityID)

// Graph visualization
graph, err := reg.Graph(ctx, entityID, registrykit.Depth(3))

// Mutations
newEntity, err := reg.CreateEntity(ctx, registrykit.CreateEntityRequest{
    EntityTypes:   []string{"company"},
    CanonicalName: "Acme Corp",
    Metadata:      map[string]any{"industry": "tech"},
    Identifiers:   map[string]string{"domain": "acme.com"},
})

err = reg.AddIdentifier(ctx, entityID, "crunchbase", "acme-corp")

err = reg.CreateRelationship(ctx, registrykit.CreateRelationshipRequest{
    FromEntity: parentID, ToEntity: childID, Relationship: "subsidiary_of",
})

// Entity deduplication
err = reg.Merge(ctx, winnerID, loserID, "duplicate detected by matching domain")
```

**Available methods**:

| Method | Description |
|--------|-------------|
| `Resolve(ctx, type, ...opts)` | Entity resolution by identifier |
| `Related(ctx, id, ...opts)` | Relationship traversal with filters |
| `Descendants(ctx, id, ...opts)` | All descendant entities |
| `Ancestors(ctx, id)` | All ancestor entities |
| `Graph(ctx, id, ...opts)` | Graph rooted at entity |
| `CreateEntity(ctx, req)` | Create a new entity |
| `AddIdentifier(ctx, id, ns, val)` | Add identifier to entity |
| `CreateRelationship(ctx, req)` | Create directed relationship |
| `Merge(ctx, winner, loser, reason)` | Merge duplicate entities |

---

### lakekit — Data lake client (lake_svc)

**When to use**: Your service needs to query the data lake for analytics, entity history, or dataset discovery. Provides SQL query execution, entity event history, dataset listing, and dataset statistics.

```go
lake := lakekit.NewClient("http://lake-svc:8080",
    lakekit.WithTenant("tenant-123"),
    lakekit.WithTimeout(30*time.Second), // queries can be slow
)

// SQL query with parameters
result, err := lake.Query(ctx, "SELECT * FROM events WHERE entity_id = $1", entityID)
// result.Columns: ["id", "timestamp", "event_type", ...]
// result.Rows: [["ev_1", "2026-01-15T...", "created", ...], ...]
// result.RowCount: 42

// Entity event history
history, err := lake.EntityHistory(ctx, entityID)
for _, entry := range history {
    fmt.Println(entry.Timestamp, entry.EventType, entry.Data)
}

// Dataset catalog
datasets, err := lake.Datasets(ctx)
for _, ds := range datasets {
    fmt.Println(ds.Name, ds.RowCount, ds.LastUpdate)
}

// Dataset statistics and schema
stats, err := lake.DatasetStats(ctx, "events")
for _, col := range stats.Schema {
    fmt.Println(col.Name, col.Type)
}
```

**Available methods**:

| Method | Description |
|--------|-------------|
| `Query(ctx, sql, ...params)` | Execute SQL query against the data lake |
| `EntityHistory(ctx, entityID)` | Temporal event history for an entity |
| `Datasets(ctx)` | List all available datasets |
| `DatasetStats(ctx, name)` | Schema and statistics for a dataset |

**Integration notes** (all service client kits):
- All kits propagate `tracekit` trace IDs automatically. Ensure `tracekit.Middleware` or `tracekit.WithTraceID` is in your middleware chain for end-to-end tracing.
- All kits use bare `http.Client` internally. For production use with retry and circuit breaking, consider wrapping the HTTP client with `call.New()` or contributing a `WithHTTPClient` option.
- All kits return `nil, nil` for 404 responses — check for `nil` before using the result.

---

## Planned Service Client Kits

These modules are planned but **do not exist yet**. They are documented here to establish API intent and naming conventions. Do not attempt to import them — the packages have not been implemented.

### posthogkit — Product analytics event capture (planned)

**When to use**: Every service should capture analytics events. posthogkit buffers events and flushes to PostHog periodically via tick. No-op when `POSTHOG_ENABLED=false`.

```go
cfg := config.MustLoad[struct{
    PostHog posthogkit.Config
}]()
ph := posthogkit.New(cfg.PostHog)
defer ph.Close()

// Non-blocking — buffers internally, flushes every 30s or 100 events
ph.Capture(ph.HashID(userID), "api_request", map[string]any{
    "endpoint": "/search",
    "latency_ms": elapsed.Milliseconds(),
})
```

**Env vars**: `POSTHOG_API_KEY`, `POSTHOG_HOST` (default `https://us.i.posthog.com`), `POSTHOG_FLUSH_INTERVAL` (default `30s`), `POSTHOG_FLUSH_SIZE` (default `100`), `POSTHOG_ENABLED` (default `true`), `POSTHOG_HMAC_SECRET`.

**Behavior**:
- Events are buffered in memory and flushed either on interval or when the buffer reaches the configured size, whichever comes first.
- `HashID` applies HMAC-SHA256 hashing using `POSTHOG_HMAC_SECRET` for privacy-safe user identification.
- When `POSTHOG_ENABLED=false`, all capture calls are no-ops — zero overhead, no network calls.
- `Close()` flushes any remaining buffered events before returning.

---

### meilikit — Meilisearch search client (planned)

**When to use**: Any service needing full-text search. Replaces the searchkit addon.

```go
meili, _ := meilikit.New(cfg.Meili,
    meilikit.WithRetry(3, 500*time.Millisecond),
    meilikit.WithCircuitBreaker("meilisearch", 5, 30*time.Second),
)

idx, _ := meili.Index("products")
idx.Configure(ctx, meilikit.IndexConfig{
    PrimaryKey: "uuid",
    Searchable: []string{"name", "description"},
    Filterable: []string{"category", "price"},
})

result, _ := idx.Search(ctx, "wireless headphones", meilikit.SearchOptions{
    Filter: "category = electronics",
    Limit:  20,
})
```

**Env vars**: `MEILI_URL`, `MEILI_API_KEY`, `MEILI_TIMEOUT` (default `5s`).

**Behavior**:
- Index configuration is idempotent — safe to call on every startup.
- Supports retry and circuit breaker patterns consistent with `call` package conventions.
- Search results include hit count, processing time, and facet distributions when requested.

---

### inferkit — OpenAI-compatible LLM inference (planned)

**When to use**: Any service calling LLM APIs (OpenAI, DeepInfra, Groq, or any `/v1/` compatible server). Replaces the llm addon.

```go
client := inferkit.New(inferkit.Config{
    BaseURL: inferkit.DeepInfra,
    APIKey:  cfg.APIKey,
    Model:   "Qwen/Qwen3-Next-80B-A3B-Instruct",
},
    inferkit.WithRetry(4, 5*time.Second),
    inferkit.WithCircuitBreaker("llm", 5, 60*time.Second),
)

resp, _ := client.Chat(ctx, inferkit.ChatRequest{
    Messages: []inferkit.Message{
        {Role: "system", Content: systemPrompt},
        {Role: "user", Content: userQuery},
    },
    ResponseFormat: &inferkit.ResponseFormat{Type: "json_object"},
})

embeddings, _ := client.Embed(ctx, inferkit.EmbedRequest{
    Input: []string{"text to embed"},
})
```

**Env vars**: `INFER_BASE_URL` (default `https://api.openai.com/v1`), `INFER_API_KEY`, `INFER_MODEL`, `INFER_TIMEOUT` (default `120s`).

**Behavior**:
- Supports chat completions and embeddings via the standard OpenAI `/v1/` API surface.
- Provider constants (`inferkit.DeepInfra`, `inferkit.Groq`, `inferkit.OpenAI`) for common base URLs.
- Retry and circuit breaker protect against transient API failures and provider outages.
- Respects context cancellation for long-running inference requests.

---

### ollamakit — Ollama native LLM client (planned)

**When to use**: Services talking directly to a local Ollama instance. Use inferkit instead if you need to swap between cloud/local providers.

```go
ollama := ollamakit.New(ollamakit.Config{
    Host:  "http://localhost:11434",
    Model: "llama3.2",
})

// Streaming chat
stream, _ := ollama.ChatStream(ctx, ollamakit.ChatRequest{
    Messages: []ollamakit.Message{
        {Role: "user", Content: "Explain quantum computing"},
    },
})
for chunk := range stream {
    fmt.Print(chunk.Content)
}

// Embeddings
emb, _ := ollama.Embed(ctx, ollamakit.EmbedRequest{
    Input: []string{"document text"},
})

// Model management
models, _ := ollama.ListModels(ctx)
```

**Env vars**: `OLLAMA_HOST` (default `http://localhost:11434`), `OLLAMA_MODEL` (default `llama3.2`), `OLLAMA_TIMEOUT` (default `120s`), `OLLAMA_AUTO_PULL` (default `false`).

**Behavior**:
- Native Ollama REST API client — not OpenAI-compatible, uses Ollama-specific endpoints.
- Streaming chat returns a Go channel that yields chunks as they arrive from the model.
- `OLLAMA_AUTO_PULL=true` automatically pulls a model before first use if it is not already available locally.
- Model management includes listing, pulling, and checking model availability.

---

### qdrantkit — Qdrant vector database client (planned)

**When to use**: Services needing vector similarity search. Natural pair with inferkit/ollamakit embeddings.

```go
qd, _ := qdrantkit.New(cfg.Qdrant,
    qdrantkit.WithRetry(3, 500*time.Millisecond),
)

// Create collection
qd.CreateCollection(ctx, "documents", qdrantkit.CollectionConfig{
    VectorSize: 1024,
    Distance:   "Cosine",
})

// Upsert vectors
qd.Upsert(ctx, "documents", []qdrantkit.Point{{
    ID:      "doc-123",
    Vector:  embeddings.Vectors[0],
    Payload: map[string]any{"title": "My Document"},
}})

// Search with filters
results, _ := qd.Search(ctx, "documents", queryVector, qdrantkit.SearchOptions{
    Filter: qdrantkit.Filter{Must: []qdrantkit.Condition{
        qdrantkit.Match("tenant_id", tenantID),
    }},
    Limit: 10,
})
```

**Env vars**: `QDRANT_URL` (default `http://localhost:6333`), `QDRANT_API_KEY`, `QDRANT_TIMEOUT` (default `10s`).

**Behavior**:
- Qdrant REST API client with typed filter builder for constructing search queries.
- Collection creation is idempotent — safe to call on every startup.
- Batch upsert for efficient bulk vector ingestion.
- Retry support for transient network failures against the Qdrant instance.

---

## Event bus kits

These packages support publishing and subscribing to the Redpanda event bus. For the full cross-language integration guide, see **[chassis-docs/07-event-bus-integration.md](../chassis-docs/07-event-bus-integration.md)**. **If your service publishes or consumes events, these kits are mandatory — they enforce naming conventions, schema validation, and operational visibility.**

### tracekit — Cross-service trace propagation

**When to use**: You need to propagate trace IDs across HTTP calls and event bus messages for end-to-end request tracing. **Every service that handles HTTP requests or publishes/consumes events should use tracekit.** The service client kits (`registrykit`, `lakekit`) already use it automatically.

```go
import "github.com/ai8future/chassis-go/v11/tracekit"

// Generate a new trace ID (tr_ + 12 hex chars)
id := tracekit.GenerateID() // "tr_a1b2c3d4e5f6"

// Set on context
ctx = tracekit.NewTrace(ctx)            // generate + set
ctx = tracekit.WithTraceID(ctx, myID)   // set explicit ID

// Extract from context
id := tracekit.TraceID(ctx)

// HTTP middleware — extracts X-Trace-ID from request (or generates new),
// sets on context, adds to response header
mux := http.NewServeMux()
handler := tracekit.Middleware(mux)
```

**Integration notes**:
- `tracekit` operates independently of OTel. It uses a simple `X-Trace-ID` header for lightweight trace correlation. Use it alongside `httpkit.Tracing()` (OTel spans) or as a standalone alternative for services that don't need full distributed tracing.
- Service client kits (`registrykit`, `lakekit`) read `tracekit.TraceID(ctx)` and set the `X-Trace-ID` header automatically. Wire `tracekit.Middleware` at your HTTP ingress to complete the chain.

### schemakit — Avro schema management and validation

**When to use**: Your service publishes or consumes Avro-encoded events on the event bus and you need schema validation, serialization, and Schema Registry integration. **Required for any service that needs schema-enforced event contracts.**

```go
import "github.com/ai8future/chassis-go/v11/schemakit"

// Connect to Schema Registry (Redpanda or Confluent-compatible)
reg, err := schemakit.NewRegistry("http://schema-registry:8081")

// Load all .avsc files from a directory
err = reg.LoadSchemas("./schemas/")

// Get a loaded schema by subject (namespace.name from the .avsc file)
schema := reg.GetSchema("ai8.events.OrderCreated")

// Validate data against schema (catches missing/mistyped fields)
err = reg.Validate(schema, map[string]any{
    "order_id": "ord_123",
    "amount":   99.99,
})

// Serialize with Confluent wire format (magic byte + schema ID + Avro payload)
bytes, err := reg.Serialize(schema, data)

// Deserialize (looks up schema by ID from wire format header)
data, err := reg.Deserialize(rawBytes)

// Register schema with Schema Registry (assigns schema ID)
err = reg.Register(ctx, schema)
```

**Integration notes**:
- Uses the Confluent wire format (0x00 + 4-byte schema ID + Avro payload) for compatibility with Redpanda, Confluent, and any Kafka tooling that expects it.
- `LoadSchemas` walks a directory recursively — organize your `.avsc` files however you like.
- Schema subject keys are derived from `namespace.name` in the Avro schema file.
- After `Register`, the schema's `SchemaID` is updated in-place — subsequent `Serialize` calls include the correct ID.

### heartbeatkit — Automatic liveness events

**When to use**: Automatically — `heartbeatkit` auto-activates when kafkakit is configured via `lifecycle.Run(ctx, WithKafkaConfig(cfg), components...)`. It publishes periodic heartbeat payloads to `ai8.infra.heartbeat` so that operational dashboards and alerting systems can detect service liveness without polling.

```go
import "github.com/ai8future/chassis-go/v11/heartbeatkit"

// Manual usage (typically auto-activated by lifecycle)
heartbeatkit.Start(ctx, publisher, heartbeatkit.Config{
    ServiceName: "my-service",
    Version:     "2.4.1",
    Interval:    30 * time.Second, // default
})
defer heartbeatkit.Stop()
```

**Heartbeat payload** (published to `ai8.infra.heartbeat`):
```json
{
    "service": "my-service",
    "host": "prod-01",
    "pid": 48231,
    "uptime_s": 3600,
    "version": "2.4.1",
    "status": "healthy",
    "events_published_1h": 1234,
    "errors_1h": 2,
    "last_event_published": "2026-03-07T14:22:01Z"
}
```

**Integration notes**:
- If the publisher implements a `Stats()` method (kafkakit publishers do), the heartbeat payload is enriched with publishing statistics (events/errors in the last hour, last event timestamp).
- Default interval is 30 seconds. Operations uses the gap between heartbeats to detect frozen or crashed services.

### announcekit — Structured lifecycle events

**When to use**: Automatically — `announcekit` auto-activates when kafkakit is configured via `lifecycle.Run`. It publishes structured lifecycle events (started, ready, stopping, failed) and job lifecycle events (started, complete, failed) to well-known Kafka subjects. **Critical for operational visibility — operations dashboards and alerting rules depend on these events.**

```go
import "github.com/ai8future/chassis-go/v11/announcekit"

announcekit.SetServiceName("my-service")

// Service lifecycle events (typically auto-fired by lifecycle.Run)
announcekit.Started(ctx, publisher)
announcekit.Ready(ctx, publisher)
announcekit.Stopping(ctx, publisher)
announcekit.Failed(ctx, publisher, err)

// Job lifecycle events (fire manually in your job handlers)
announcekit.JobStarted(ctx, publisher, "nightly-etl", jobID)
announcekit.JobComplete(ctx, publisher, "nightly-etl", jobID, resultMap)
announcekit.JobFailed(ctx, publisher, "nightly-etl", jobID, err)
```

**Event subjects**:
- Service: `ai8.infra.{service}.lifecycle.{started|ready|stopping|failed}`
- Job: `ai8.infra.{service}.job.{started|complete|failed}`

**Integration notes**:
- Service lifecycle events are typically handled automatically. Job lifecycle events should be called explicitly in any long-running batch process.
- Job events include `job_name` and `job_id` for correlation.

---

## Common integration patterns

### Minimal HTTP service

```go
func main() {
    chassis.SetAppVersion(yourpkg.AppVersion)
    chassis.RequireMajor(11)
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

## Durable Workflows (inngestkit) — OPT-IN

`inngestkit` provides thin setup glue for self-hosted [inngest](https://www.inngest.com/). Use it when your service needs durable multi-step workflows, event-driven pipelines, webhook fanout, or code-defined scheduled tasks. **It is not required for service completion.**

```go
type Config struct {
    Port    int               `env:"PORT" default:"8080"`
    Inngest inngestkit.Config // INNGEST_BASE_URL, INNGEST_APP_ID, INNGEST_EVENT_KEY, INNGEST_SIGNING_KEY
}

func main() {
    chassis.SetAppVersion(yourpkg.AppVersion)
    chassis.RequireMajor(11)
    cfg := config.MustLoad[Config]()

    kit, err := inngestkit.New(cfg.Inngest)
    if err != nil { log.Fatal(err) }

    // Define functions with the native inngestgo SDK
    fn, _ := inngestgo.CreateFunction(kit.Client(),
        inngestgo.FunctionOpts{ID: "my-workflow"},
        inngestgo.EventTrigger("app/event", nil),
        handler,
    )
    _ = fn // functions auto-register with the client

    mux := http.NewServeMux()
    kit.Mount(mux) // registers /api/inngest serve handler

    // Send events
    kit.Send(ctx, inngestgo.Event{Name: "app/event", Data: payload})
}
```

See **INNGEST.md** for the full reference (env vars, validation rules, API surface) and the [inngestgo SDK docs](https://pkg.go.dev/github.com/inngest/inngestgo) for function definitions, steps, retries, and concurrency controls.

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

### Subscriber Concurrency

By default, the subscriber processes messages sequentially (one at a time per poll batch). Set `Concurrency` on `SubscriberConfig` to enable parallel message dispatch:

```go
cfg := kafkakit.Config{
    BootstrapServers: "localhost:9092",
    Subscriber: kafkakit.SubscriberConfig{
        Concurrency: 8, // up to 8 messages processed in parallel
    },
}

sub, err := kafkakit.NewSubscriber(cfg, "my-service-group")
```

**Behavior:**

| Concurrency value | Behavior |
|---|---|
| `0` (default) or `1` | Sequential processing -- same as before this feature |
| `>1` (e.g., `8`) | Concurrent dispatch with a semaphore limiting active goroutines |

When concurrency is `>1`, the subscriber uses a rolling semaphore model: each polled record is dispatched to a goroutine gated by a channel semaphore, and the poll loop continues immediately without waiting for the batch to drain. This keeps workers saturated continuously. `MaxPollRecords` is auto-scaled to `Concurrency * 2` (unless explicitly set higher) so each poll returns enough records to fill the worker pool. In-flight workers are drained gracefully on shutdown.

**When to use:** CPU-bound or I/O-bound handlers that can safely process multiple events simultaneously. Ensure your handler is goroutine-safe (no shared mutable state without synchronization).

**When not to use:** Handlers that depend on strict per-partition ordering. Concurrent dispatch within a batch does not guarantee ordering.

### AtLeastOnce Delivery

By default, the subscriber uses Kafka's auto-commit — offsets are committed when the next poll occurs, before handlers finish processing. If the service restarts mid-processing, those messages are lost (at-most-once delivery).

Set `AtLeastOnce: true` to switch to manual commit mode where offsets are committed only after all handlers in a batch complete:

```go
cfg := kafkakit.Config{
    BootstrapServers: "localhost:9092",
    Subscriber: kafkakit.SubscriberConfig{
        Concurrency: 96,
        AtLeastOnce: true, // commit after handlers finish, not on poll
    },
}
```

**How it works:**

| Aspect | Default (auto-commit) | AtLeastOnce |
|---|---|---|
| Offset commit timing | On next `PollRecords` call | After `wg.Wait()` + explicit `CommitUncommittedOffsets` |
| Delivery guarantee | At-most-once | At-least-once |
| Dispatch model | Rolling semaphore (no per-batch wait) | Batch-and-wait (all handlers must complete before commit) |
| Restart behavior | Messages in-flight are lost | Messages in-flight are re-delivered |
| Partition rebalance | Auto-commit handles it | `OnPartitionsRevoked` callback drains workers and commits |
| Shutdown | Workers drained, client closed | Workers drained, final commit, client closed |

**When to use AtLeastOnce:**

- Handlers take >1 second (e.g., LLM processing, external API calls). The batch-and-wait overhead is negligible compared to handler time.
- Message loss is unacceptable — you'd rather process a message twice than lose it.
- Your handlers are idempotent or your pipeline handles duplicates (e.g., entity dedup).

**When NOT to use AtLeastOnce:**

- Fast handlers (sub-second). The batch-and-wait stall between polls becomes a real throughput bottleneck.
- You already have at-most-once semantics baked into your architecture and don't need re-delivery.

**Handler errors and DLQ:** When `AtLeastOnce` is enabled, handler errors are committed after DLQ routing. A message that fails and is routed to the DLQ is considered "handled" — it will not be re-delivered in a loop. This is the correct behavior: the DLQ captures the failure for investigation.

---

## Go Best Practices for Chassis Services

See **[GO-BEST-PRACTICES.md](GO-BEST-PRACTICES.md)** for prescriptive rules on building and maintaining Go services that use chassis-go. Key highlights:

- **Cross-platform binaries**: Every deployable service must support `build-linux`, `build-darwin`, and `build-all` Makefile targets. Developing on Mac and deploying to Linux without explicit `GOOS`/`GOARCH` produces binaries that fail silently.
- **Binary naming**: Name binaries after the service (not `server`) so they're distinguishable in `ps ax`. Output to `bin/`, never the project root.
- **VERSION injection**: Pick one approach per project — either LDFLAGS (`-X main.version=$(VERSION)`) or `go:embed`. Don't use both.
- **Dockerfile builds**: Always set `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` explicitly in the build stage, even though Docker runs Linux.
- **Required Makefile targets**: `build`, `build-linux`, `build-darwin`, `build-all`, `test`, `clean`, `lint`, `deps`, `run`.

---

## Things to watch out for

**config panics are intentional.** `MustLoad` panics on missing required config. This is by design — configuration errors should crash the process at startup, not cause mysterious failures later. If you need softer error handling, validate env vars before calling `MustLoad`, or contribute a `Load` variant that returns errors.

**lifecycle.Run components must respect context.** If your component ignores `ctx.Done()`, the process will hang on shutdown. This is the most common integration mistake. Always test that your components exit cleanly when the context is cancelled.

**Circuit breakers are global singletons.** `call.GetBreaker("name", ...)` returns the same instance for the same name across your entire process. This is intentional — multiple HTTP clients hitting the same downstream should share circuit state. But it means breaker names are a global namespace. Use clear, service-specific names.

**Log the chassis version at startup.** Import the top-level `chassis` package and log `chassis.Version` during initialization. This makes it easy to correlate production issues with a specific library version and track which services have upgraded after a release.

**RequireMajor must be called first.** Every chassis module checks that `chassis.RequireMajor(N)` was called before it runs. If you skip it, you get a clear crash at startup. Place it as the first line in `main()`.

**secval errors are NOT ServiceError.** `secval.ValidateJSON` returns module-local errors (`ErrDangerousKey`, etc.), not `*errors.ServiceError`. Convert them at the handler boundary with `errors.ValidationError(err.Error())`.

**Registry enforcement is service-level, not package-level.** The registry crashes the process if `Status()` or `Errorf()` are called before `lifecycle.Run()`. The `httpkit` and `grpckit` middleware also enforce this — handling requests means you're a running service. However, utility modules (`work`, `call`, `health`, `config`, `logz`, `errors`, `secval`, `flagz`) do NOT require registry. They work fine in libraries, CLI tools, and non-service contexts. If your Go module uses chassis utilities internally, the consuming application only needs `RequireMajor(11)` — not `lifecycle.Run()` — unless it also uses httpkit/grpckit to handle requests.

**The toolkit has six direct dependencies.** `golang.org/x/sync` (for errgroup), `golang.org/x/crypto` (for seal — scrypt KDF), `google.golang.org/grpc` (for grpckit and errors), `go.opentelemetry.io/otel` (for otel, metrics, call, and work), `github.com/hamba/avro/v2` (for schemakit — Avro serialization), and `github.com/twmb/franz-go` (for kafkakit — Kafka client). If you only use core packages (config, logz, lifecycle, testkit), only `x/sync` is pulled in.

**Vendor freshness with local `replace` directives.** If your project uses `go mod vendor` and your `go.mod` has a `replace` directive pointing chassis-go to a local path (e.g., `replace github.com/ai8future/chassis-go/v11 => ../../chassis_suite/chassis-go`), the vendor directory does NOT auto-update when the local chassis source changes. You must re-run `go mod vendor` after chassis-go is updated, or your build will silently use the old vendored code — even though `go.mod` points to the latest source. This is the most common cause of "missing feature" bugs in local development. Before debugging missing chassis features, check `vendor/modules.txt` for the chassis version.

If your project uses vendoring with local `replace` directives, you **MUST** add the following to your project's `AGENTS.md` (or `CLAUDE.md`):

```
- **Vendor freshness**: This project vendors dependencies and uses a local `replace` directive
  for chassis-go. Before building, testing, or debugging, always run `go mod vendor` to ensure
  the vendor directory reflects the current local chassis-go source. A stale vendor dir will
  silently use old code even though go.mod points to the latest source.
```

This ensures that any AI agent or coding assistant working in your repo knows to sync the vendor directory before doing anything else.
