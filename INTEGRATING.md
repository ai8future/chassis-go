# Integrating chassis-go

Practical guide for teams adopting chassis-go into an existing Go codebase.

## Before you start

**Requirements**: Go 1.25+ (the module requires 1.25.5).

**What this is**: A cohesive toolkit that covers the foundational concerns of a Go service — config, logging, lifecycle, HTTP, gRPC, health checks, and resilient outbound calls. Import the whole thing. Your `main()` stays yours.

**What this is not**: An opinionated framework. Chassis doesn't own your dependency injection, routing, or service mesh. It provides building blocks that you wire together explicitly.

## Installation

```bash
go get github.com/ai8future/chassis-go
```

The top-level package exports the library version for diagnostics:

```go
import chassis "github.com/ai8future/chassis-go"

logger.Info("starting", "chassis_version", chassis.Version)
```

### Version gate

Every service must declare which major version of chassis it supports. This prevents silent behavior changes when chassis is upgraded without review.

```go
func main() {
    chassis.RequireMajor(3) // crashes if chassis major version != 3
    // ... rest of startup
}
```

If the version doesn't match, the process exits with a clear message telling you exactly what to do. If `RequireMajor` is not called before using any chassis module, those modules will also crash at startup.

A typical service imports all packages:

```go
import (
    "github.com/ai8future/chassis-go/call"
    "github.com/ai8future/chassis-go/config"
    "github.com/ai8future/chassis-go/errors"
    "github.com/ai8future/chassis-go/grpckit"
    "github.com/ai8future/chassis-go/health"
    "github.com/ai8future/chassis-go/httpkit"
    "github.com/ai8future/chassis-go/lifecycle"
    "github.com/ai8future/chassis-go/logz"
    "github.com/ai8future/chassis-go/metrics"
    "github.com/ai8future/chassis-go/secval"
)
```

And in test files:

```go
import "github.com/ai8future/chassis-go/testkit"
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
import "github.com/ai8future/chassis-go/errors"

// Factory constructors for common error categories
err := errors.ValidationError("name is required")         // 400 / INVALID_ARGUMENT
err := errors.NotFoundError("user not found")              // 404 / NOT_FOUND
err := errors.UnauthorizedError("invalid token")           // 401 / UNAUTHENTICATED
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
import "github.com/ai8future/chassis-go/secval"

// Validate JSON before unmarshalling
if err := secval.ValidateJSON(body); err != nil {
    // err is a secval error (ErrDangerousKey, ErrNestingDepth, ErrInvalidJSON)
    // Wrap it into a ServiceError at the handler boundary:
    return errors.ValidationError(err.Error())
}
json.Unmarshal(body, &req) // safe to unmarshal now
```

**What it checks**:
- **Dangerous keys**: `__proto__`, `constructor`, `prototype`, `execute`, `eval`, `include`, `import`, `require`, `system`, `shell`, `command`, `script`, `exec`, `spawn`, `fork`. Keys are normalized (lowercased, hyphens replaced with underscores) before checking.
- **Nesting depth**: Maximum 20 levels. Prevents stack overflow attacks from deeply nested JSON.

**Integration notes**:
- `secval` defines its own error types (`ErrDangerousKey`, `ErrNestingDepth`, `ErrInvalidJSON`), NOT `ServiceError`. This keeps the module dependency-free. Wrap secval errors into `ServiceError` at your handler boundary.
- The validation parses JSON once, then your handler parses again into a struct. This double-parse is acceptable for typical payloads (<1MB). Do not use secval on file uploads or streaming endpoints.
- Always enforce body size limits (`http.MaxBytesReader` at 1-2MB) BEFORE passing to secval.

---

### metrics — Prometheus metrics

**When to use**: You want to expose Prometheus metrics with built-in request recording and cardinality protection.

```go
import "github.com/ai8future/chassis-go/metrics"

// Create a recorder with a metric prefix
recorder := metrics.New("mysvc", logger)

// Record request metrics
recorder.RecordRequest("POST", "200", 42.5, 1024)

// Compose onto admin port alongside health checks
adminMux := http.NewServeMux()
adminMux.Handle("/metrics", recorder.Handler())
adminMux.Handle("/health", health.Handler(checks))

// Or use the convenience wrapper that serves both
srv, err := recorder.StartServer(9090, logger, healthChecks)
defer srv.Shutdown(context.Background())
```

**What it records per `RecordRequest` call**:
- `{prefix}_requests_total{method, status}` — Counter
- `{prefix}_request_duration_seconds{method}` — Histogram
- `{prefix}_content_size_bytes{method}` — Histogram

**Integration notes**:
- Cardinality protection: max 1000 label combinations per metric. On overflow, new combinations are silently dropped and a warning is logged once.
- The metric prefix is caller-supplied — use your service name.
- `Handler()` returns an `http.Handler` you can mount anywhere. `StartServer()` is a convenience wrapper that also mounts a `/health` endpoint.
- Uses `prometheus/client_golang` (new dependency in v2.0).

---

### logz — Structured JSON logging

**When to use**: You want structured JSON logging via `log/slog` with automatic trace ID injection.

```go
logger := logz.New("info") // "debug", "info", "warn", "error"

// Propagate trace IDs through context
ctx := logz.WithTraceID(ctx, "abc-123")
logger.InfoContext(ctx, "handling request", "path", "/api/users")
// Output includes: {"trace_id":"abc-123", "msg":"handling request", ...}
```

**Integration notes**:
- `logz.New` returns a standard `*slog.Logger`. Every package in your codebase that accepts `*slog.Logger` works with it unchanged.
- If you already have a logger, you can use chassis packages that accept `*slog.Logger` with your own logger instance. There is no coupling to `logz`.
- Trace ID propagation works through context. Set it once at your HTTP/gRPC ingress point and it flows through all downstream log calls that use `Context` variants.

---

### lifecycle — Graceful shutdown orchestration

**When to use**: Your service runs multiple long-lived components (HTTP server, gRPC server, background workers) that need coordinated startup and shutdown.

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

**Integration notes**:
- Every component function **must** watch `ctx.Done()`. A component that ignores the context will block shutdown indefinitely.
- `http.Server.ListenAndServe()` does not respect context cancellation — you need the goroutine + select pattern shown above. `grpc.Server.Serve()` is the same; use `GracefulStop()` on context cancellation.
- If you already have a shutdown manager, `lifecycle.Run` is just a convenience. You can use the other chassis packages without it.

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

**Utilities**:
- `httpkit.JSONError(w, r, statusCode, message)` — writes a standard `{"error": "...", "status_code": N, "request_id": "..."}` response.
- `httpkit.RequestIDFrom(ctx)` — retrieves the request ID from context (useful in your handlers).

**Integration notes**:
- These are standard `func(http.Handler) http.Handler` middleware. They compose with any router (chi, gorilla/mux, stdlib ServeMux).
- The `responseWriter` wrapper implements `Unwrap()`, so `http.NewResponseController` can still access `Flusher` and `Hijacker` on the underlying writer. SSE and WebSocket upgrades work through the middleware stack.
- Recommended middleware order (outermost first): Recovery → RequestID → Logging → your routes. Recovery should be outermost so it catches panics from all other middleware.

---

### grpckit — gRPC interceptors

**When to use**: You run a gRPC server and want logging, panic recovery, and health check wiring.

```go
srv := grpc.NewServer(
    grpc.ChainUnaryInterceptor(
        grpckit.UnaryRecovery(logger),
        grpckit.UnaryLogging(logger),
        grpckit.UnaryMetrics(logger),
    ),
    grpc.ChainStreamInterceptor(
        grpckit.StreamRecovery(logger),
        grpckit.StreamLogging(logger),
        grpckit.StreamMetrics(logger),
    ),
)

// Wire up standard gRPC health check
grpckit.RegisterHealth(srv, func(ctx context.Context) error {
    _, err := healthChecker(ctx)
    return err
})
```

**Integration notes**:
- Recovery interceptors log the panic value **and full stack trace**, then return `codes.Internal`.
- Place recovery interceptors first in the chain so they catch panics from all downstream interceptors and handlers.
- `grpckit.RegisterHealth` decouples gRPC from the `health` package. It accepts any `func(ctx context.Context) error` — you can wire in your own health logic without importing `health`.
- The metrics interceptors are placeholders that log at Debug level. Replace the function bodies with your metrics library (Prometheus, OpenTelemetry, etc.).

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
- All checks run in parallel.
- Returns 200 + `{"status":"healthy"}` when all pass.
- Returns 503 + `{"status":"unhealthy","checks":[...]}` when any fail.
- Individual check failures don't short-circuit other checks.

**Integration notes**:
- Health checks should be fast. Set timeouts on the context you pass, or use a context with deadline in your check functions.
- The `health.Check` type is just `func(ctx context.Context) error`. Wrap any existing health check function to match.

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
- `testkit.SetEnv` calls `os.Setenv` and registers cleanup via `t.Cleanup`. For parallel tests, prefer `t.Setenv` (Go 1.17+) which is parallel-safe; `testkit.SetEnv` is for when you need to set multiple vars at once.
- `testkit.GetFreePort` asks the OS for an available port. There is a small TOCTOU window between getting the port and binding to it, but it's reliable for tests.

---

## Common integration patterns

### Minimal HTTP service

```go
func main() {
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

## Things to watch out for

**config panics are intentional.** `MustLoad` panics on missing required config. This is by design — configuration errors should crash the process at startup, not cause mysterious failures later. If you need softer error handling, validate env vars before calling `MustLoad`, or contribute a `Load` variant that returns errors.

**lifecycle.Run components must respect context.** If your component ignores `ctx.Done()`, the process will hang on shutdown. This is the most common integration mistake. Always test that your components exit cleanly when the context is cancelled.

**Circuit breakers are global singletons.** `call.GetBreaker("name", ...)` returns the same instance for the same name across your entire process. This is intentional — multiple HTTP clients hitting the same downstream should share circuit state. But it means breaker names are a global namespace. Use clear, service-specific names.

**Log the chassis version at startup.** Import the top-level `chassis` package and log `chassis.Version` during initialization. This makes it easy to correlate production issues with a specific library version and track which services have upgraded after a release.

**RequireMajor must be called first.** Every chassis module checks that `chassis.RequireMajor(N)` was called before it runs. If you skip it, you get a clear crash at startup. Place it as the first line in `main()`.

**secval errors are NOT ServiceError.** `secval.ValidateJSON` returns module-local errors (`ErrDangerousKey`, etc.), not `*errors.ServiceError`. Convert them at the handler boundary with `errors.ValidationError(err.Error())`.

**The toolkit has three external dependencies.** `golang.org/x/sync` (for errgroup), `google.golang.org/grpc` (for grpckit and errors), and `prometheus/client_golang` (for metrics). If you only use Tier 1 packages (config, logz, lifecycle, testkit), only `x/sync` is pulled in.
