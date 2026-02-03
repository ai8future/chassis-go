# Requirements: chassis-go

**Defined:** 2026-02-03
**Core Value:** Every service gets production-grade operational concerns without reinventing them — while keeping business logic pure and portable.

## v1 Requirements

Requirements for initial release. Each maps to roadmap phases.

### Project Setup

- [ ] **SETUP-01**: Go module initialized at `github.com/ai8future/chassis-go` with Go 1.22+ minimum
- [ ] **SETUP-02**: CI pipeline runs tests, linting, and vet on every push
- [ ] **SETUP-03**: Project has .gitignore, LICENSE, and base README

### Config

- [ ] **CFG-01**: `config.MustLoad[T]()` loads env vars into typed structs via struct tags
- [ ] **CFG-02**: Missing required config causes panic on startup (fail fast)
- [ ] **CFG-03**: Supports string, int, bool, duration, and slice field types
- [ ] **CFG-04**: Supports default values via struct tags
- [ ] **CFG-05**: Supports optional fields (no panic if missing when tagged optional)

### Logging

- [ ] **LOG-01**: `logz.New(level)` returns a configured `*slog.Logger` with JSON handler
- [ ] **LOG-02**: Custom slog Handler extracts TraceID from `context.Context` and injects into JSON output
- [ ] **LOG-03**: Supports configurable log levels (debug, info, warn, error)
- [ ] **LOG-04**: Logger output is structured JSON suitable for production log aggregators

### Lifecycle

- [ ] **LIFE-01**: `lifecycle.Run(ctx, components...)` orchestrates multiple `Component` functions via errgroup
- [ ] **LIFE-02**: SIGTERM/SIGINT cancels root context, triggering graceful shutdown
- [ ] **LIFE-03**: If any component returns error, all others are cancelled
- [ ] **LIFE-04**: `Component` type is `func(ctx context.Context) error`

### Test Kit

- [ ] **TEST-01**: `testkit.NewLogger(t)` returns a `*slog.Logger` that writes to `t.Log()`
- [ ] **TEST-02**: `testkit.LoadConfig[T](t)` sets env vars from a map and cleans up after test
- [ ] **TEST-03**: `testkit.GetFreePort()` returns an available TCP port for parallel test use

### HTTP Kit

- [ ] **HTTP-01**: RequestID middleware injects unique request ID into context and response headers
- [ ] **HTTP-02**: Logging middleware logs request method, path, status, and duration using `*slog.Logger`
- [ ] **HTTP-03**: Recovery middleware catches panics and returns 500 JSON error response
- [ ] **HTTP-04**: JSON error response helper formats errors consistently

### gRPC Kit

- [ ] **GRPC-01**: Unary interceptor chain: logging, recovery, metrics placeholder
- [ ] **GRPC-02**: Stream interceptor chain: logging, recovery
- [ ] **GRPC-03**: Helper to register `grpc.health.v1.Health` service on a gRPC server
- [ ] **GRPC-04**: Recovery interceptor logs panics and returns gRPC Internal error

### Health

- [ ] **HLTH-01**: Check signature is `func(ctx context.Context) error`
- [ ] **HLTH-02**: `health.All(checks...)` runs checks in parallel via errgroup
- [ ] **HLTH-03**: Aggregator combines all failures with `errors.Join` (reports all, not just first)
- [ ] **HLTH-04**: HTTP handler returns 200 on healthy, 503 on any failure, with body listing check results
- [ ] **HLTH-05**: gRPC Health V1 adapter maps aggregated checks to gRPC health protocol

### Call (HTTP Client)

- [ ] **CALL-01**: Builder pattern creates configured `*http.Client` instances
- [ ] **CALL-02**: Retries with exponential backoff + jitter on 5xx responses
- [ ] **CALL-03**: Never retries 4xx responses
- [ ] **CALL-04**: Stops retrying immediately if request context is cancelled or timed out
- [ ] **CALL-05**: Opt-in circuit breaker middleware with Open/Half-Open/Closed states
- [ ] **CALL-06**: Circuit breakers are named and singleton (reused, not per-request)
- [ ] **CALL-07**: Enforces timeout and context propagation on every request
- [ ] **CALL-08**: Implementation-agnostic — behavior defined, concrete library chosen at build time

### Examples

- [ ] **EX-01**: `examples/01-cli` — working CLI using `config` + `logz`
- [ ] **EX-02**: `examples/02-service` — reference gRPC service using `lifecycle` + `grpckit` + `health`
- [ ] **EX-03**: `examples/03-client` — demo of `call` with retries and circuit breaking

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
| SETUP-01 | Phase 1 | Pending |
| SETUP-02 | Phase 1 | Pending |
| SETUP-03 | Phase 1 | Pending |
| CFG-01 | Phase 2 | Pending |
| CFG-02 | Phase 2 | Pending |
| CFG-03 | Phase 2 | Pending |
| CFG-04 | Phase 2 | Pending |
| CFG-05 | Phase 2 | Pending |
| LOG-01 | Phase 3 | Pending |
| LOG-02 | Phase 3 | Pending |
| LOG-03 | Phase 3 | Pending |
| LOG-04 | Phase 3 | Pending |
| LIFE-01 | Phase 4 | Pending |
| LIFE-02 | Phase 4 | Pending |
| LIFE-03 | Phase 4 | Pending |
| LIFE-04 | Phase 4 | Pending |
| TEST-01 | Phase 5 | Pending |
| TEST-02 | Phase 5 | Pending |
| TEST-03 | Phase 5 | Pending |
| HTTP-01 | Phase 6 | Pending |
| HTTP-02 | Phase 6 | Pending |
| HTTP-03 | Phase 6 | Pending |
| HTTP-04 | Phase 6 | Pending |
| HLTH-01 | Phase 7 | Pending |
| HLTH-02 | Phase 7 | Pending |
| HLTH-03 | Phase 7 | Pending |
| HLTH-04 | Phase 7 | Pending |
| HLTH-05 | Phase 7 | Pending |
| GRPC-01 | Phase 8 | Pending |
| GRPC-02 | Phase 8 | Pending |
| GRPC-03 | Phase 8 | Pending |
| GRPC-04 | Phase 8 | Pending |
| CALL-01 | Phase 9 | Pending |
| CALL-02 | Phase 9 | Pending |
| CALL-03 | Phase 9 | Pending |
| CALL-04 | Phase 9 | Pending |
| CALL-05 | Phase 9 | Pending |
| CALL-06 | Phase 9 | Pending |
| CALL-07 | Phase 9 | Pending |
| CALL-08 | Phase 9 | Pending |
| EX-01 | Phase 10 | Pending |
| EX-02 | Phase 10 | Pending |
| EX-03 | Phase 10 | Pending |

**Coverage:**
- v1 requirements: 41 total
- Mapped to phases: 41
- Unmapped: 0 ✓

---
*Requirements defined: 2026-02-03*
*Last updated: 2026-02-03 after initial definition*
