# chassis-go

A composable Go toolkit providing standardized building blocks that services wire together explicitly. Toolkit, not framework — it never owns `main()`.

## Packages

### Tier 1: Foundation
| Package | Purpose |
|---------|---------|
| `config` | Load env vars into structs via tags. Panic on missing required config. |
| `logz` | Structured JSON logging wrapping `log/slog` with TraceID extraction. |
| `lifecycle` | Graceful shutdown orchestration via `errgroup`. |
| `testkit` | Testing utilities: `NewLogger`, `SetEnv`, `GetFreePort`. |

### Tier 2: Transports & Clients
| Package | Purpose |
|---------|---------|
| `httpkit` | HTTP server middleware: RequestID, logging, recovery, JSON errors. |
| `grpckit` | gRPC server interceptors: logging, recovery, metrics. Health wiring. |
| `health` | Health check protocol with parallel aggregation. HTTP + gRPC. |
| `call` | Intelligent HTTP client: retries, circuit breaking, deadline propagation. |

### Tier 3: Cross-Cutting
| Package | Purpose |
|---------|---------|
| `guard` | Request guards: rate limiting (LRU), CORS, security headers, IP filtering, timeouts, body limits. |
| `flagz` | Feature flags with percentage rollouts and pluggable sources. |
| `metrics` | OTel metrics with cardinality protection. |
| `otel` | OpenTelemetry bootstrap (traces + metrics). |
| `errors` | Unified error type with HTTP + gRPC codes and RFC 9457 Problem Details. |
| `secval` | JSON security validation (dangerous keys, nesting depth). |

## Usage

```go
func main() {
    chassis.RequireMajor(5)
    cfg := config.MustLoad[AppConfig]()
    logger := logz.New(cfg.LogLevel)

    svc := yourservice.New(
        yourservice.WithLogger(logger),
    )

    lifecycle.Run(context.Background(),
        func(ctx context.Context) error {
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
            errCh := make(chan error, 1)
            go func() { errCh <- grpcServer.Serve(lis) }()
            select {
            case <-ctx.Done():
                grpcServer.GracefulStop()
                return nil
            case err := <-errCh:
                return err
            }
        },
    )
}
```

## Principles

1. **Toolkit, not framework** — Chassis never owns `main()`. You call it.
2. **Zero cross-dependencies** — Importing `config` doesn't pull in `grpc`.
3. **Consumer-owned interfaces** — Libraries define what they need.
4. **Visible wiring** — No magic startup.
5. **Fail fast** — Missing config = panic on startup.

## Install

```bash
go get github.com/ai8future/chassis-go/v5
```

## License

MIT
