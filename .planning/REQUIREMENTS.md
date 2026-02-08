# Requirements: chassis-go

**Defined:** 2026-02-03
**Core Value:** Every service gets production-grade operational concerns without reinventing them — while keeping business logic pure and portable.

## v1 Requirements

Requirements for initial release. Each maps to roadmap phases.

### Project Setup

- [x] **SETUP-01**: Go module initialized at `github.com/ai8future/chassis-go/v5` with Go 1.22+ minimum
- [x] **SETUP-02**: CI pipeline runs tests, linting, and vet on every push
- [x] **SETUP-03**: Project has .gitignore, LICENSE, and base README

### Config

- [x] **CFG-01**: `config.MustLoad[T]()` loads env vars into typed structs via struct tags
- [x] **CFG-02**: Missing required config causes panic on startup (fail fast)
- [x] **CFG-03**: Supports string, int, bool, duration, and slice field types
- [x] **CFG-04**: Supports default values via struct tags
- [x] **CFG-05**: Supports optional fields (no panic if missing when tagged optional)

### Logging

- [x] **LOG-01**: `logz.New(level)` returns a configured `*slog.Logger` with JSON handler
- [x] **LOG-02**: Custom slog Handler extracts TraceID from `context.Context` and injects into JSON output
- [x] **LOG-03**: Supports configurable log levels (debug, info, warn, error)
- [x] **LOG-04**: Logger output is structured JSON suitable for production log aggregators

### Lifecycle

- [x] **LIFE-01**: `lifecycle.Run(ctx, components...)` orchestrates multiple `Component` functions via errgroup
- [x] **LIFE-02**: SIGTERM/SIGINT cancels root context, triggering graceful shutdown
- [x] **LIFE-03**: If any component returns error, all others are cancelled
- [x] **LIFE-04**: `Component` type is `func(ctx context.Context) error`

### Test Kit

- [x] **TEST-01**: `testkit.NewLogger(t)` returns a `*slog.Logger` that writes to `t.Log()`
- [x] **TEST-02**: `testkit.SetEnv(t, map)` sets env vars from a map and cleans up after test
- [x] **TEST-03**: `testkit.GetFreePort()` returns an available TCP port for parallel test use

### HTTP Kit

- [x] **HTTP-01**: RequestID middleware injects unique request ID into context and response headers
- [x] **HTTP-02**: Logging middleware logs request method, path, status, and duration using `*slog.Logger`
- [x] **HTTP-03**: Recovery middleware catches panics and returns 500 JSON error response
- [x] **HTTP-04**: JSON error response helper formats errors consistently

### gRPC Kit

- [x] **GRPC-01**: Unary interceptor chain: logging, recovery, metrics placeholder
- [x] **GRPC-02**: Stream interceptor chain: logging, recovery
- [x] **GRPC-03**: Helper to register `grpc.health.v1.Health` service on a gRPC server
- [x] **GRPC-04**: Recovery interceptor logs panics and returns gRPC Internal error

### Health

- [x] **HLTH-01**: Check signature is `func(ctx context.Context) error`
- [x] **HLTH-02**: `health.All(checks)` runs checks in parallel via `work.Map`
- [x] **HLTH-03**: Aggregator combines all failures with `errors.Join` (reports all, not just first)
- [x] **HLTH-04**: HTTP handler returns 200 on healthy, 503 on any failure, with body listing check results
- [x] **HLTH-05**: gRPC Health V1 adapter maps aggregated checks to gRPC health protocol

### Call (HTTP Client)

- [x] **CALL-01**: Builder pattern creates configured `*http.Client` instances
- [x] **CALL-02**: Retries with exponential backoff + jitter on 5xx responses
- [x] **CALL-03**: Never retries 4xx responses
- [x] **CALL-04**: Stops retrying immediately if request context is cancelled or timed out
- [x] **CALL-05**: Opt-in circuit breaker middleware with Open/Half-Open/Closed states
- [x] **CALL-06**: Circuit breakers are named and singleton (reused, not per-request)
- [x] **CALL-07**: Enforces timeout and context propagation on every request
- [x] **CALL-08**: Implementation-agnostic — behavior defined, concrete library chosen at build time

### Examples

- [x] **EX-01**: `examples/01-cli` — working CLI using `config` + `logz`
- [x] **EX-02**: `examples/02-service` — reference gRPC service using `lifecycle` + `grpckit` + `health`
- [x] **EX-03**: `examples/03-client` — demo of `call` with retries and circuit breaking

## v2 Requirements

Deferred to future release. Tracked but not in current roadmap.

### Metrics & Observability

- **OBS-01**: OpenTelemetry trace propagation in all middleware
- **OBS-02**: Prometheus metrics export from grpckit and httpkit
- **OBS-03**: Structured error types with stack traces

### Code Generation

- **GEN-01**: `ai8-init` scaffold tool generates boilerplate wiring code
- **GEN-02**: Template-based service scaffolding

## Out of Scope

| Feature | Reason |
|---------|--------|
| Proto definitions | Belong in separate `proto/` repo |
| Kubernetes manifests | Belong in deploy templates, not a Go library |
| ORM / database abstractions | Too opinionated per service |
| Business-logic patterns | Conventions, not library code |
| Shared interface packages | Interfaces belong to consumers, not providers |
| Cross-language ports | Get Go right with 2-3 consumers first |
| Specific third-party libraries in API signatures | Define behavior, pick implementations at build time |

## Traceability

| Requirement | Phase | Status |
|-------------|-------|--------|
| SETUP-01 | Phase 1 | Complete |
| SETUP-02 | Phase 1 | Complete |
| SETUP-03 | Phase 1 | Complete |
| CFG-01 | Phase 2 | Complete |
| CFG-02 | Phase 2 | Complete |
| CFG-03 | Phase 2 | Complete |
| CFG-04 | Phase 2 | Complete |
| CFG-05 | Phase 2 | Complete |
| LOG-01 | Phase 3 | Complete |
| LOG-02 | Phase 3 | Complete |
| LOG-03 | Phase 3 | Complete |
| LOG-04 | Phase 3 | Complete |
| LIFE-01 | Phase 4 | Complete |
| LIFE-02 | Phase 4 | Complete |
| LIFE-03 | Phase 4 | Complete |
| LIFE-04 | Phase 4 | Complete |
| TEST-01 | Phase 5 | Complete |
| TEST-02 | Phase 5 | Complete |
| TEST-03 | Phase 5 | Complete |
| HTTP-01 | Phase 6 | Complete |
| HTTP-02 | Phase 6 | Complete |
| HTTP-03 | Phase 6 | Complete |
| HTTP-04 | Phase 6 | Complete |
| HLTH-01 | Phase 7 | Complete |
| HLTH-02 | Phase 7 | Complete |
| HLTH-03 | Phase 7 | Complete |
| HLTH-04 | Phase 7 | Complete |
| HLTH-05 | Phase 7 | Complete |
| GRPC-01 | Phase 8 | Complete |
| GRPC-02 | Phase 8 | Complete |
| GRPC-03 | Phase 8 | Complete |
| GRPC-04 | Phase 8 | Complete |
| CALL-01 | Phase 9 | Complete |
| CALL-02 | Phase 9 | Complete |
| CALL-03 | Phase 9 | Complete |
| CALL-04 | Phase 9 | Complete |
| CALL-05 | Phase 9 | Complete |
| CALL-06 | Phase 9 | Complete |
| CALL-07 | Phase 9 | Complete |
| CALL-08 | Phase 9 | Complete |
| EX-01 | Phase 10 | Complete |
| EX-02 | Phase 10 | Complete |
| EX-03 | Phase 10 | Complete |

**Coverage:**
- v1 requirements: 41 total
- Mapped to phases: 41
- Unmapped: 0 ✓

---
*Requirements defined: 2026-02-03*
*Last updated: 2026-02-03 after initial definition*
