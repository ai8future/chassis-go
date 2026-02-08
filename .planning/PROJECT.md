# chassis-go

## What This Is

A composable Go toolkit providing standardized, battle-tested building blocks (`config`, `logz`, `lifecycle`, `testkit`, `grpckit`, `httpkit`, `health`, `call`) that services wire together explicitly in `main.go`. This is a toolkit, not a framework — it never owns `main()`, never calls your code, and packages have zero cross-dependencies. Built for the ai8future service ecosystem.

## Core Value

Every service in the ecosystem gets production-grade operational concerns (config, logging, lifecycle, transports, health, resilient clients) without reinventing them — while keeping business logic pure and portable.

## Requirements

### Validated

(None yet — ship to validate)

### Active

**Tier 1 — Foundation (config, logz, lifecycle, testkit):**

- [ ] `chassis/config` — Load env vars into structs via tags with `MustLoad` (panic on missing required config). Handles runtime config only (ports, auth, log levels), not embedded config.
- [ ] `chassis/logz` — Structured JSON logging wrapping `log/slog`. Custom `Handler` that extracts TraceIDs from `context.Context` and injects them into JSON output.
- [ ] `chassis/lifecycle` — Orchestration and graceful shutdown wrapping `golang.org/x/sync/errgroup`. Defines `Component func(ctx context.Context) error`. Handles SIGTERM/SIGINT by cancelling root context. No registry, no hooks, no start/stop lifecycle.
- [ ] `chassis/testkit` — `NewLogger(t)` for clean test output, `LoadConfig(t)` for safe env-var setting/cleanup, `GetFreePort()` for parallel network tests.

**Tier 2 — Transports & Clients (grpckit, httpkit, health, call):**

- [ ] `chassis/grpckit` — gRPC server utilities. Standard interceptor chain: logging, recovery, metrics. Helper to wire `grpc.health.v1`.
- [ ] `chassis/httpkit` — HTTP server utilities. Standard middleware: RequestID injection, logging, recovery. JSON error response formatting.
- [ ] `chassis/health` — Standardized health protocol (HTTP 200/503, gRPC Health V1). Check signature: `func(ctx context.Context) error`. Aggregator: `health.All(checks...)` runs checks in parallel via `errgroup`, combines failures with `errors.Join`.
- [ ] `chassis/call` — Intelligent HTTP client builder. Retries with exponential backoff + jitter on 5xx. Opt-in circuit breaker (stateful, half-open support). Enforces timeouts and context propagation. No 4xx retries. Deadline propagation. Singleton breakers.

**Validation & Documentation:**

- [ ] `examples/01-cli` — Fully working CLI using `config` + `logz`.
- [ ] `examples/02-service` — Reference gRPC service using `lifecycle` + `grpckit` + `health`.
- [ ] `examples/03-client` — Demo of `call` with retries and circuit breaking.

### Out of Scope

- Proto definitions — belong in a separate `proto/` repo
- Kubernetes manifests — belong in deploy templates, not a Go library
- ORM or database abstractions — too opinionated per service
- Business-logic patterns (dual-API pattern, etc.) — conventions, not library code
- Shared interface packages — interfaces belong to consumers, not providers
- Specific third-party library choices baked into API signatures — define behavior, pick implementations at build time
- Cross-language ports (chassis-py, chassis-ts) — get Go right with 2-3 real consumers first

## Context

- Design document: `_studies/chassis-go-toolkit-design-recommendations.md` (in code_manage repo) — approved February 3, 2026
- GitHub org: `ai8future`, repo: `chassis-go`
- First planned consumers: `pricing-cli` (Tier 1 validation), `serp_svc` (full Chassis adoption)
- Architecture principle: "Pure Library" pattern — libraries never import chassis, they depend only on stdlib. Application `main.go` acts as matchmaker using functional options.
- Config philosophy: Fail fast. Missing required config = panic on startup. Binary safety — either we have config to run, or we crash.
- All packages must accept `context.Context` as first arg for blocking functions
- Consumer-owned interfaces: no shared interface packages. Libraries define what they need; Chassis provides implementations that satisfy them.
- Logging via `*slog.Logger` (standard library), not custom logger interfaces
- Visible wiring: generated boilerplate is readable and editable, no magic startup

## Constraints

- **Go version**: Go 1.22+ minimum
- **Module strategy**: Single Go module (`github.com/ai8future/chassis-go/v5`), not multi-module. Split into separate repos only if a single package needs a breaking change that doesn't affect others.
- **Versioning**: SemVer strictly. Start at `v0.x.x`.
- **Zero cross-deps**: Importing `chassis/config` must not pull in `chassis/grpc`. Packages are independent.
- **No framework behavior**: Chassis never owns `main()`. It never calls your code. You call it.

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Toolkit, not framework | Preserves "Library-First" architecture. Business logic stays pure and portable. | — Pending |
| Single Go module | Multi-module repos introduce friction in tagging/releasing. Coherent toolkit with zero cross-deps can version as a unit. | — Pending |
| `MustLoad` panic on config error | Start-up safety is binary. Crash immediately rather than run with undefined behavior. | — Pending |
| `errors.Join` for health aggregation | Reports all failing checks, not just the first. Critical for diagnosing partial outages. | — Pending |
| Consumer-owned interfaces | Avoids shared interface packages. Libraries define what they need. | — Pending |
| Go 1.22+ minimum | Access to latest language features while `log/slog` is available (added in 1.21). | — Pending |
| Implementation-agnostic circuit breaker | Define Open/Half-Open/Closed behavior, don't hardcode a library in the API signature. | — Pending |

---
*Last updated: 2026-02-03 after initialization*
