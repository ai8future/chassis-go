# Chassis-Go Toolkit Design Recommendations

**Date:** February 3, 2026
**Status:** Approved for Implementation (Final Revision)

## Executive Summary

`chassis-go` is a **composable toolkit**, not a framework. It provides standardized, battle-tested building blocks (`config`, `logging`, `lifecycle`, `http-client`) that services wire together explicitly in `main.go`.

This design preserves the "Library-First" architecture. Core business logic remains pure and portable, while applications (CLIs, Services) import the Chassis to handle operational concerns like observability and shutdown.

---

## 1. Core Principles

1. **Toolkit, Not Framework:** The Chassis never owns `main()`. It never calls your code. You call it.
2. **Zero Cross-Dependencies:** Packages within Chassis are independent. Importing `chassis/config` does not pull in `chassis/grpc`.
3. **Consumer-Owned Interfaces:** We do not define shared interface packages. Libraries define what they need; Chassis provides implementations that satisfy them. Use `*slog.Logger` (standard library) for logging. Use `context.Context` for tracing and timeouts. For other dependencies, the consumer defines the interface it needs.
4. **Visible Wiring:** `ai8-init` generates boilerplate code that developers can read and edit. No "magic" startup functions.
5. **Fail Fast Configuration:** Missing required configuration causes a panic on startup (`MustLoad`). It is better to crash immediately than to run with undefined behavior.

---

## 2. Package Structure & Tiers

### Tier 1: The Essentials
*High reuse, low risk. Used by CLIs, Tools, and Services.*

| Package | Purpose | Implementation Notes |
| :--- | :--- | :--- |
| `chassis/config` | Runtime config loading | Loads env vars into structs via tags. **Panic on error**: start-up safety is binary; either we have the config to run, or we crash. Handles *Runtime* config only (ports, auth), not *Embedded* config (rules, data). |
| `chassis/logz` | Structured JSON logging | Wraps standard library `log/slog`. Includes a custom `Handler` to extract TraceIDs from Context and inject them into JSON logs. |
| `chassis/lifecycle` | Orchestration & Shutdown | Wraps `golang.org/x/sync/errgroup`. Defines `Component func(ctx context.Context) error`. Handles SIGTERM/SIGINT by cancelling the root context. This is the entire abstraction — no start/stop lifecycle, no registry, no hooks. |
| `chassis/testkit` | Testing Utilities | `NewLogger(t)` for clean test output. `LoadConfig(t)` for safe env-var setting/cleanup. `GetFreePort()` for parallel network tests. |

### Tier 2: The Transports & Clients
*Used by Service entry points to talk to the world.*

| Package | Purpose | Implementation Notes |
| :--- | :--- | :--- |
| `chassis/grpckit` | gRPC Server Utilities | Standard interceptor chain: logging, recovery, metrics. Helper to wire `grpc.health.v1`. |
| `chassis/httpkit` | HTTP Server Utilities | Standard middleware: RequestID injection, logging, recovery. JSON error response formatting. |
| `chassis/health` | Health Protocol | Standardizes protocol (HTTP 200/503, gRPC Health V1). Check signature: `func(ctx context.Context) error`. **Aggregator**: `health.All(checks...)` runs checks in parallel via `errgroup` and combines failures with `errors.Join` (reports all failing checks, not just the first). Probe returns 503 on any failure. |
| `chassis/call` | Intelligent HTTP Client | A builder for `*http.Client`. **Retries:** Exponential backoff + jitter on 5xx. **Circuit Breaker:** Opt-in middleware (stateful, half-open support) — implementation-agnostic, define the behavior not the library. **Safety:** Enforces timeouts and context propagation. |

---

## 3. The "Pure Library" Pattern

Libraries **never** import `chassis`. They depend only on the Standard Library. The Application (`cmd/api/main.go`) acts as the matchmaker using Functional Options.

```go
func main() {
    // 1. Chassis handles Runtime Config (panics if missing)
    envCfg := config.MustLoad[EnvConfig]()

    // 2. Chassis handles Observability
    logger := logz.New(envCfg.LogLevel)

    // 3. Library instantiated via Functional Options
    // Library defaults to embedded config, overridden here by runtime env vars
    pricer := pricing.New(
        pricing.WithLogger(logger),
        pricing.WithCostRules(envCfg.Rules),
    )
}
```

For cross-library dependencies, the consumer defines the interface:

```go
// pricing_db defines only what it needs
type Sealer interface {
    Seal(plaintext []byte) ([]byte, error)
}

// encryptedcol satisfies this interface without knowing about it
// main.go wires them together:
pricer := pricing.New(
    pricing.WithSealer(encryptedcol.NewCipher(...)),
)
```

---

## 4. Operational Safety Guidelines

### Circuit Breakers & Retries

To prevent retry storms (DDoS-ing ourselves), `chassis/call` must implement:

1. **Deadline Propagation:** Stop retrying immediately if the request Context is cancelled or timed out.
2. **No 4xx Retries:** Never retry Bad Requests.
3. **Singleton Breakers:** Circuit breakers must be named and reused. Creating a new breaker per request is a bug.
4. **Implementation Agnostic:** Define the behavior (Open/Half-Open/Closed), but do not hardcode a specific library in the API signature. Use an adapter internally. Pick the concrete implementation when you build, not now.

### Health Aggregation

Services often depend on DB + Cache + Upstream.

- **Signature:** `func(ctx context.Context) error`
- **Aggregation:** `health.All(db.Check, cache.Check)` runs checks in parallel and combines all failures via `errors.Join`. A readiness probe returns 503 on any failure, but the response body and logs report *which* checks failed — critical for diagnosing partial outages.

### Context Usage

- **Mandatory:** Every blocking function in Chassis must accept `context.Context` as the first argument.
- **Cancellation:** `lifecycle` relies entirely on Context cancellation to signal shutdown.

### Embedded vs Runtime Config

Two distinct configuration domains that must not be conflated:

- **Embedded configuration:** Library-owned data bundled via `go:embed` (e.g., pricing_db's JSON files). The library controls this.
- **Runtime configuration:** Env vars for ports, credentials, log levels loaded by `chassis/config`. The service entry point controls this.

Libraries own embedded config. Chassis owns runtime config. When both apply, use the overlay pattern in `main.go` via functional options.

---

## 5. Versioning Strategy

**Decision:** `chassis-go` will be a **single Go module** initially.

- **Rationale:** Multi-module repositories (tagging `config/v1.0.0`, `logz/v1.0.0`) introduce significant friction in tagging and releasing. Since `chassis-go` is a coherent toolkit with zero cross-dependencies between packages, versioning it as a unit is acceptable.
- **Versioning:** Follow SemVer strictly. Start with `v0.x.x`.
- **Split trigger:** If a single package needs a breaking change that doesn't affect other packages, that is the signal to split it into a separate repo (e.g., `chassis-grpc`). Do not use multi-module subdirectories — split into separate repos entirely. Note the migration cost: every consumer updates import paths. Acceptable at 3-5 services, painful at 20+. Plan splits before you hit that scale.

---

## 6. Documentation Strategy

Ship **living documentation** inside the repo, not a wiki:

- `examples/01-cli`: A fully working CLI tool using `config` + `logz`.
- `examples/02-service`: A reference gRPC service using `lifecycle` + `grpckit` + `health`.
- `examples/03-client`: A demo of `call` making requests with retries and circuit breaking.

---

## 7. Implementation Roadmap

1. **Initialize Repo:** Create `github.com/ai8future/chassis-go`.
2. **Step 1 (Foundation):** Implement `config`, `logz`, and `testkit`.
3. **Step 2 (Foundation Validation):** Port `pricing-cli` to use Step 1.
4. **Step 3 (Orchestration):** Implement `lifecycle`.
5. **Step 4 (Lifecycle Validation):** Create a standalone `cmd/demo-shutdown` inside the repo. Verify it handles SIGTERM correctly, cancels Contexts, and drains gracefully before moving to transports.
6. **Step 5 (Transports):** Implement `grpckit`, `httpkit`, `health`, and `call`.
7. **Step 6 (Adoption):** Scaffold `serp_svc` using the full Chassis toolkit.

---

## What Does NOT Belong in Chassis

- **Proto definitions** — separate `proto/` repo
- **Kubernetes manifests** — belong in templates, not a Go library
- **ORM or database abstractions** — too opinionated per service
- **Business-logic patterns** — the dual-API pattern is a convention, not library code
- **Shared interface packages** — interfaces belong to consumers, not providers
- **Specific third-party library choices** — define behavior, pick implementations at build time

---

## Cross-Language Note

Resist building `chassis-py` and `chassis-ts` simultaneously. Get `chassis-go` right with 2-3 real consumers first, then port proven abstractions. Premature cross-language parity leads to lowest-common-denominator APIs.
