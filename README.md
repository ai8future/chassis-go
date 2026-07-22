# chassis-go

A composable Go service toolkit for building production-grade microservices. Toolkit, not framework — chassis never owns `main()`, never hides wiring behind magic, and every package is independently importable.

```
go get github.com/ai8future/chassis-go/v11
```

**Current version:** 11.3.26 &middot; **Go:** 1.26.x (module floor 1.26.5; tested with 1.26.5) &middot; **License:** MIT

---

## Requirements

chassis-go v11 declares `go 1.26.5` in `go.mod`, so Go 1.26.5 is the minimum supported toolchain and Go 1.25 or older Go 1.26 patch releases cannot build this module without toolchain switching. Build, test, and deploy with Go 1.26.5 or later; this repository is currently verified with Go 1.26.5.

---

## Why chassis-go?

Every Go microservice needs the same foundational concerns: env-based config, structured logging, graceful shutdown, health checks, HTTP middleware, gRPC interceptors, resilient HTTP clients, observability, feature flags, and request guards. Without a shared toolkit, teams re-implement these inconsistently across services.

chassis-go provides one cohesive, OTel-native solution where you wire together only what you need.

---

## Packages

### Tier 1: Foundation

| Package | Import | Purpose |
|---------|--------|---------|
| `chassis` | `github.com/ai8future/chassis-go/v11` | Version gate (`RequireMajor(11)`) and deterministic port assignment (`Port(name, offset)` via djb2) |
| `config` | `.../v11/config` | Generic env-to-struct config loader via struct tags. Panics on missing required vars |
| `logz` | `.../v11/logz` | Structured JSON/text logging wrapping `log/slog` with automatic OTel `trace_id`/`span_id` injection |
| `clikit` | `.../v11/clikit` | Stdlib-first CLI toolkit: flat commands, env+flag binding, JSON output, exit codes, color, and opt-in registry integration |
| `lifecycle` | `.../v11/lifecycle` | Signal-aware graceful shutdown orchestration via `errgroup` |
| `registry` | `.../v11/registry` | File-based service registration at `/tmp/chassis/`. Status reporting, port declarations, custom commands, heartbeat |
| `testkit` | `.../v11/testkit` | Test helpers: `NewLogger` (writes to `t.Log`), `SetEnv` (with cleanup), `GetFreePort` |

### Tier 2: Transports and Clients

| Package | Import | Purpose |
|---------|--------|---------|
| `httpkit` | `.../v11/httpkit` | HTTP middleware: RequestID, Logging, Recovery, Tracing. JSON error responses |
| `grpckit` | `.../v11/grpckit` | gRPC interceptors: Logging, Recovery, Metrics, Tracing. Health service registration |
| `health` | `.../v11/health` | Parallel health check aggregation with HTTP handler and gRPC adapter |
| `call` | `.../v11/call` | Resilient outbound HTTP client: retry with exponential backoff, circuit breaker, OTel spans |

### Tier 3: Cross-Cutting

| Package | Import | Purpose |
|---------|--------|---------|
| `guard` | `.../v11/guard` | HTTP guards: rate limiter (LRU), CORS, IP filter, security headers, body limits, timeouts |
| `flagz` | `.../v11/flagz` | Feature flags with percentage rollouts (FNV-1a), pluggable sources, OTel span events |
| `metrics` | `.../v11/metrics` | OTel-native metrics recorder with cardinality protection (max 1000 label combos) |
| `otel` | `.../v11/otel` | OpenTelemetry bootstrap: OTLP gRPC traces + metrics, configurable samplers |
| `errors` | `.../v11/errors` | Unified error type with dual HTTP/gRPC codes, RFC 9457 Problem Details, and stable retry classes |
| `authkit` | `.../v11/authkit` | Scoped inbound bearer-token validation for Windmill-callable HTTP/gRPC endpoints |
| `idemkit` | `.../v11/idemkit` | Tenant-scoped HTTP idempotency middleware with replay, mismatch, in-flight, and 5xx release handling |
| `orchestration` | `.../v11/orchestration` | Windmill capability manifests, registry metadata, and authored OpenAPI handlers |
| `conformance` | `.../v11/conformance` | Windmill L0-L2 runtime evidence helpers plus declaration-only L3 reporting |
| `secval` | `.../v11/secval` | JSON security validation: blocks prototype pollution keys (`__proto__`, `constructor`, `prototype`) and deep nesting |
| `work` | `.../v11/work` | Structured concurrency: `Map`, `All`, `Race`, `Stream` — all OTel-traced |

### Tier 4: Utilities

| Package | Import | Purpose |
|---------|--------|---------|
| `cache` | `.../v11/cache` | Generic LRU+TTL in-memory cache with `Prune()` |
| `seal` | `.../v11/seal` | AES-256-GCM encrypt/decrypt, HMAC-SHA256 sign/verify, temporary tokens |
| `tick` | `.../v11/tick` | Periodic task components for `lifecycle.Run` (`Every` with `Immediate`/`OnError` options) |
| `webhook` | `.../v11/webhook` | HMAC-signed webhook send with retry, delivery tracking, `VerifyPayloadID` for signed delivery IDs, and legacy-compatible `VerifyPayload` wrapper |
| `deploy` | `.../v11/deploy` | Convention-based deploy directory discovery, environment detection, endpoints, dependencies, health |

### Tier 5: Integrations

| Package | Import | Purpose |
|---------|--------|---------|
| `kafkakit` | `.../v11/kafkakit` | Publish/subscribe to Redpanda with envelopes, tenant filtering, bounded DLQ, and manual-contiguous delivery. Uses `github.com/twmb/franz-go` |
| `schemakit` | `.../v11/schemakit` | Avro schema validation, registration, serialization. Confluent Schema Registry client |
| `tracekit` | `.../v11/tracekit` | Distributed trace ID propagation (`tr_` + 32 lowercase hex canonical, bounded 12-hex legacy inbound). HTTP middleware. Can be used alongside OTel/httpkit tracing |
| `heartbeatkit` | `.../v11/heartbeatkit` | Auto liveness heartbeats every 30s. Depends on `kafkakit`. Auto-activates with kafkakit |
| `announcekit` | `.../v11/announcekit` | Service/job lifecycle events. Depends on `kafkakit`. Auto-activates with kafkakit |
| `registrykit` | `.../v11/registrykit` | HTTP client to registry_svc for entity resolution. Depends on `call` |
| `lakekit` | `.../v11/lakekit` | Stdlib HTTP client to lake_svc for data lake access with tenant/trace headers and bounded response decoding |
| `phasekit` | `.../v11/phasekit` | Startup secret hydration from Phase via the `phase` CLI before `config.MustLoad` |
| `inngestkit` | `.../v11/inngestkit` | Thin setup glue for the Inngest Go SDK (config, HTTP mount, send). Functions/steps use `inngestgo` directly. Optional |

### Tier 6: External Service Clients

Most client packages below are built on `call.Client` (retry, circuit breaker, OTel spans). Exceptions are `lakekit` and `ollamakit`, which intentionally use stdlib `http.Client` for their current APIs/streaming behavior.

| Package | Import | Purpose |
|---------|--------|---------|
| `inferkit` | `.../v11/inferkit` | Provider-agnostic client for OpenAI-compatible LLM APIs: chat completions, SSE streaming, embeddings. Works with OpenAI, DeepInfra, Groq, Ollama (compat mode) |
| `ollamakit` | `.../v11/ollamakit` | Stdlib HTTP client for Ollama's native `/api/` endpoints: chat, generate, embeddings, model management; streaming/pull operations use a no-timeout client while caller context controls cancellation |
| `meilikit` | `.../v11/meilikit` | Client for Meilisearch: index and document management, search |
| `qdrantkit` | `.../v11/qdrantkit` | Client for the Qdrant vector database: collections, points, filtered search |
| `posthogkit` | `.../v11/posthogkit` | Non-blocking, batched PostHog analytics client; buffered capture flushed periodically or by size. No-op when disabled |

**Tier isolation**: Foundation packages avoid transport/runtime stacks such as gRPC and the OTel SDK unless you import packages that need them. `clikit` adds no CLI framework and reuses existing chassis/logz plumbing; its trace-aware logging path may include the already-present OTel trace API, but not the OTel SDK.

---

## Quick Start

### CLI / batch tool

```go
func main() {
    chassis.SetAppVersion(mytool.AppVersion) // app-owned VERSION embed
    chassis.RequireMajor(11)                 // owns --version and freshness

    app := clikit.New(clikit.Config{Name: "mytool"}).Command(clikit.Command{
        Name: "greet",
        Run: func(ctx context.Context, c *clikit.Context) error {
            return c.Out.Emit(map[string]string{"message": "hello"})
        },
    })
    os.Exit(app.Run(os.Args))
}
```

`clikit` does not register `--version` and does not replace `RequireMajor(11)`. Existing v11 services that do not import `clikit` keep their current startup, version, and freshness behavior.

### HTTP service

```go
package main

import (
    "context"
    "fmt"
    "net"
    "net/http"
    "time"

    chassis "github.com/ai8future/chassis-go/v11"
    "github.com/ai8future/chassis-go/v11/config"
    "github.com/ai8future/chassis-go/v11/guard"
    "github.com/ai8future/chassis-go/v11/health"
    "github.com/ai8future/chassis-go/v11/httpkit"
    "github.com/ai8future/chassis-go/v11/lifecycle"
    "github.com/ai8future/chassis-go/v11/logz"
)

type AppConfig struct {
    Port     int    `env:"PORT" default:"8080"`
    LogLevel string `env:"LOG_LEVEL" default:"info"`
}

func main() {
    // Version gate — must be first
    chassis.SetAppVersion(myapp.AppVersion) // enables --version flag and auto-rebuild
    chassis.RequireMajor(11)

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

Load environment variables into typed structs using struct tags. Fail-fast: missing required config panics at startup. As with other chassis modules, call `chassis.RequireMajor(11)` once before `config.MustLoad`.

```go
type AppConfig struct {
    Port        int           `env:"PORT" default:"8080"`
    DatabaseURL string        `env:"DATABASE_URL"`                // required (default)
    Debug       bool          `env:"DEBUG" required:"false"`      // optional
    Timeout     time.Duration `env:"TIMEOUT" default:"30s"`
    AllowedIPs  []string      `env:"ALLOWED_IPS" default:"127.0.0.1"`
}

chassis.RequireMajor(11)
cfg := config.MustLoad[AppConfig]()
```

**Supported types:** `string`, `int`, `int64`, `float64`, `bool`, `time.Duration`, `[]string` (comma-separated)

### `phasekit` - Phase Secret Hydration

Hydrate environment variables from Phase before `config.MustLoad` runs:

```go
phasekit.MustHydrate(ctx, phasekit.Config{
    ServiceToken: os.Getenv("PHASE_SERVICE_TOKEN"),
    Host:         os.Getenv("PHASE_HOST"),
    App:          "myservice",
    Env:          "Production",
    RequiredKeys: []string{"DATABASE_URL"},
})

cfg := config.MustLoad[AppConfig]()
```

Existing environment variables win by default, missing `phase` binaries fall
back to the existing process environment, dynamic secret leases are disabled in
v1, and `[REDACTED]` values fail startup. See
[`INTEGRATING_PHASE.md`](INTEGRATING_PHASE.md) for Docker and CI guidance.

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

Signal-aware orchestrator using `errgroup`. Catches SIGTERM/SIGINT, cancels the shared context, and waits for all components to drain. Automatically initializes the `registry` on startup — every service is registered at `/tmp/chassis/` with heartbeat and command polling.

```go
lifecycle.Run(ctx,
    httpServerComponent,
    grpcServerComponent,
    workerComponent,
)
```

Each component receives a context that cancels on signal or when any peer returns an error.

### `registry` — File-Based Service Registration

Every service automatically registers itself at `/tmp/chassis/<service-name>/` when `lifecycle.Run()` is called. The registry writes a JSON PID file, maintains a structured log, and provides a command interface for external tooling.

**What gets created:**
```
/tmp/chassis/<service-name>/
  <pid>.json        # Registration: name, PID, hostname, version, available commands
  <pid>.log.jsonl   # Structured event log: startup, heartbeat, status, errors, shutdown
  <pid>.cmd.json    # Command file (written by external tools, consumed by the service)
```

**Automatic behavior** (managed by `lifecycle.Run()`):
- Heartbeat event logged every 30 seconds
- Command file polled every 3 seconds
- Built-in `stop` command triggers graceful shutdown
- Built-in `restart` command sets the restart flag and triggers shutdown
- Stale PID files from dead processes are cleaned up on startup

**Module-level API** — no object to pass around:
```go
import "github.com/ai8future/chassis-go/v11/registry"

// Report status (written to the service log)
registry.Status("processing batch 42")

// Report errors
registry.Errorf("failed to connect to %s: %v", host, err)

// Register custom commands (must be called before lifecycle.Run)
registry.Handle("flush-cache", "Clear all cached data", func() error {
    // clear application cache here
    return nil
})
```

The service name is resolved from `CHASSIS_SERVICE_NAME` env var, falling back to the working directory name. The service version comes from `chassis.SetAppVersion` when set, otherwise from a `VERSION` file in the working directory.

### `call` — Resilient HTTP Client

Outbound HTTP with retry (exponential backoff + jitter), circuit breaker (Closed/Open/HalfOpen states), and OTel client spans.

```go
client := call.New(
    call.WithTimeout(5*time.Second),
    call.WithRetry(3, 500*time.Millisecond),
    call.WithCircuitBreaker("payments-api", 5, 30*time.Second),
    call.WithTextMapPropagator(propagation.TraceContext{}), // external boundary
)

resp, err := client.Do(req)
```

Batch concurrent requests with `client.Batch(ctx, requests)` — powered by `work.Map` under the hood.
`propagation` is `go.opentelemetry.io/otel/propagation`. An explicit boundary
clones request headers, removes fields declared by the active global and
selected propagators, then injects only the selected fields. Pass nil to remove
the active global propagation fields without injecting replacements.

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
errors.PayloadTooLargeError(msg) // 413 / INVALID_ARGUMENT
errors.RateLimitError(msg)     // 429 / RESOURCE_EXHAUSTED
errors.TimeoutError(msg)       // 504 / DEADLINE_EXCEEDED
errors.DependencyError(msg)    // 503 / UNAVAILABLE
errors.InternalError(msg)      // 500 / INTERNAL
```

Write RFC 9457 responses directly:
```go
errors.WriteProblem(w, r, err, requestID)
```

Windmill-compatible problem responses also expose stable retry metadata:

```go
err := errors.DependencyError("upstream unavailable").
    WithRetryAfter(2 * time.Second)

errors.WriteProblem(w, r, err, traceID) // JSON includes code, retryable, retry_after; header includes Retry-After
```

Use `errors.IsRetryable(err)` / `errors.Retryable(err)` when callers need the same classification outside HTTP.

### Windmill orchestration readiness

The Windmill readiness slice is additive v11 core functionality. Core ships the callable-service primitives, contract fixtures, and conformance helpers; durable DB-backed idempotency/outbox implementations remain service/addon responsibilities.

```go
manifest := orchestration.Manifest{
    Service:         "orders",
    Version:         "1.2.3",
    Profile:         orchestration.ProfileL2Prod,
    ContractVersion: orchestration.DefaultContractVersion,
    Capabilities:    []string{"problem-json", "authkit", "idemkit"},
    Idempotency:     &orchestration.Idempotency{Store: "postgres", Durable: true, TTLSeconds: 604800},
    Endpoints:       []orchestration.Endpoint{{Name: "submit", URL: "/v1/orders", Kind: "http"}},
    OpenAPIPath:     "/openapi.yaml",
}

mux.Handle(orchestration.WellKnownPath, orchestration.Handler(manifest))

conformance.Require(t, conformance.LevelL2, conformance.Evidence{
    Manifest:                         &manifest,
    AcceptsXTraceID:                  conformance.AcceptsTraceID("tr_0123456789abcdef0123456789abcdef"),
    EmitsProblemJSONErrors:           true,
    ErrorProblemJSONRetryClass:       true,
    ScopedBearerAuthOnMutatingRoutes: true,
    IdempotencyKeyReplayForMutating:  true,
    IdempotencyKeyTenantScoped:       true,
})
```

For production wiring, middleware order, resource provisioning, trace ID rules, idempotency behavior, and the L3 declaration-only boundary, see [`docs/windmill-orchestration.md`](docs/windmill-orchestration.md).

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
    MaxAge:       time.Hour,
})

// Security headers (CSP, HSTS 2yr, X-Frame-Options: DENY, etc.)
guard.SecurityHeaders(guard.DefaultSecurityHeaders)

// IP allow/deny by CIDR (deny takes precedence)
guard.IPFilter(guard.IPFilterConfig{
    Allow: []string{"10.0.0.0/8"},
    Deny:  []string{"10.0.0.1/32"},
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
shutdown, err := otel.InitChecked(otel.Config{
    ServiceName:    "ordersvc",
    ServiceVersion: "1.4.2",               // consuming application version
    Endpoint:       "otel-collector:4317", // host:port; default: localhost:4317
    Sampler:        otel.RatioSample(0.1),  // 10% sampling; default: AlwaysSample
    Secure:         true,                   // explicit TLS, overrides plaintext OTLP env
})
if err != nil { return err }
defer shutdown(context.Background())
```

`InitChecked` validates the local endpoint and TLS policy, constructs both
exporters, and claims the process-global telemetry slot before installing the
providers. Collector reachability and the TLS handshake remain lazy and are
proved only by export/receipt evidence. A second initialization is rejected
until the idempotent shutdown resets globals to no-op providers and drains the
owned pipelines; applications must not replace OTel globals while it is active.
Legacy `Init` retains graceful degradation for compatibility. `Secure` and
`Insecure` are mutually exclusive; use `Insecure` only for explicit plaintext
development collectors. `TLSConfig` requires `Secure`, is cloned, and cannot
weaken the TLS 1.2 minimum.

### `secval` — JSON Security Validation

Validates JSON payloads against dangerous keys and excessive nesting. Zero cross-module dependencies.

```go
if err := secval.ValidateJSON(body); err != nil {
    // errors.Is(err, secval.ErrDangerousKey)
    // errors.Is(err, secval.ErrNestingDepth)
    // errors.Is(err, secval.ErrInvalidJSON)
}
```

Blocks prototype pollution keys: `__proto__`, `constructor`, `prototype`. Common business-domain words are intentionally excluded to avoid false positives. Max nesting depth: 20.

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

### Integration & Client Kits

The integration kits are optional. Import only the ones a service needs — each
keeps its heavier dependencies out of the core import graph.

**Event bus (`kafkakit` + `schemakit`)** — publish/subscribe to Redpanda over
the Kafka protocol with a standard event envelope (event ID, ms timestamp,
source, subject, trace ID, tenant ID, entity refs), tenant-based delivery
filtering, dead-letter routing on handler error, wildcard subscriptions, and
deprecated auto-commit compatibility or partition-ordered
`manual-contiguous` delivery. Manual mode processes one bounded poll batch at
a time, preserves Kafka headers, routes poison records to a metadata-preserving
DLQ, and commits only durable contiguous partition prefixes. `AtLeastOnce`
selects manual mode for v11 compatibility; new code should set
`CommitMode: kafkakit.CommitModeManualContiguous`. Replay can still occur, so
exactly-once business effects require application-owned transactional storage
or an outbox. `schemakit` loads `.avsc` Avro schemas and
serializes/registers them in Confluent wire format against a Schema Registry.

**Liveness & lifecycle (`heartbeatkit` + `announcekit`)** — `heartbeatkit`
publishes liveness payloads to `ai8.infra.heartbeat` on a fixed interval;
`announcekit` publishes service- and job-lifecycle events to
`ai8.infra.{service}.lifecycle.{state}` and `ai8.infra.{service}.job.{state}`.
Both depend on `kafkakit` and auto-activate when it is configured.

**Platform clients (`registrykit` + `lakekit`)** — typed HTTP clients for
`registry_svc` (entity resolution, relationship/graph traversal, entity
management) and `lake_svc` (SQL queries, entity history, dataset
listing/stats). `registrykit` is `call`-backed; `lakekit` uses stdlib HTTP
with bounded response reads. Both set `X-Tenant-ID` and `X-Trace-ID` on every
request.

**Inference & search clients** — `inferkit` (OpenAI-compatible chat/stream/
embeddings), `meilikit` (Meilisearch), and `qdrantkit` (Qdrant vector DB) are
thin `call`-backed clients for common backends. `ollamakit` uses stdlib HTTP
for Ollama native APIs and long-running streams/pulls. `posthogkit` is a
non-blocking batched analytics client that no-ops when disabled.

**Durable workflows (`inngestkit`)** — thin setup glue (config, HTTP mount,
event send) for the Inngest Go SDK. Function and step definitions use
`inngestgo` directly; see [`INNGEST.md`](INNGEST.md). Optional — services
without durable-workflow needs should skip it.

---

## Version Gate

chassis-go enforces a mandatory version compatibility contract. Every service must declare which major version it expects and provide its app version:

```go
func main() {
    chassis.SetAppVersion(myapp.AppVersion) // from appversion.go at repo root
    chassis.RequireMajor(11)                // must be called before any chassis module
    // ...
}
```

`SetAppVersion` enables two automatic features:
- **`--version` flag**: `myservice --version` prints `myservice 1.2.3 (chassis-go 11.x.y)` and exits
- **Auto-rebuild**: if the binary's compiled version is older than the VERSION file on disk, it recompiles and re-execs automatically. Opt out with `CHASSIS_NO_REBUILD=1`.

See [INTEGRATING.md](INTEGRATING.md) for the full `appversion.go` setup pattern.

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
2. **Tier isolation** — Importing `config` does not pull in gRPC or the OTel SDK. Dependencies scale with what you use; `clikit` adds no CLI framework and reuses existing chassis/logz primitives.
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

The core packages keep a thin direct-dependency surface (OTel, `golang.org/x/sync`, `golang.org/x/crypto`, `google.golang.org/grpc`). Heavier dependencies are isolated to the integration kits that need them, so they are only pulled in when you import those packages:

```
go.opentelemetry.io/otel          v1.40.0   (core)
go.opentelemetry.io/otel/sdk      v1.40.0   (otel)
golang.org/x/sync                 v0.21.0   (core)
golang.org/x/crypto               v0.53.0   (seal)
google.golang.org/grpc            v1.79.3   (grpckit, otel)
github.com/twmb/franz-go          v1.20.7   (kafkakit)
github.com/hamba/avro/v2          v2.31.0   (schemakit)
github.com/inngest/inngestgo      v0.15.1   (inngestkit)
```

See `go.mod` for the full, pinned dependency list.

chassis-go core still ships no database driver; durable stores belong in service code or addons.

---

## Database Access

chassis-go ships no database driver. For Postgres, pair it with [chassis-go-addons/pgkit](https://github.com/ai8future/chassis-go-addons/tree/main/pgkit) (a `pgxpool` wrapper) plus [pressly/goose](https://github.com/pressly/goose) for ledger-based migrations.

For typed queries, [sqlc](https://github.com/sqlc-dev/sqlc) is the recommended default: real SQL in `.sql` files, generated plain-Go code, no runtime ORM, and the generated `Queries` struct accepts the same `*pgxpool.Pool` from `pgkit.Open`. **Caveat:** sqlc generates one function per query, so it shines on **stable query shapes** (CRUD-heavy services). For workloads dominated by dynamic queries — admin screens with toggleable filters, reporting, search builders — [Bun](https://github.com/uptrace/bun) or hand-rolled pgx composes more cleanly.

---

## License

MIT — see [LICENSE](LICENSE).
