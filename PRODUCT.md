> **What is PRODUCT.md?** This file is the product bible for this codebase. It helps
> someone understand what this software does, why it exists, who it serves, and what
> business capabilities it provides. It gives context needed to make informed decisions
> about code changes.

# What Is chassis-go?

chassis-go is the standardized Go microservice **toolkit** for the ai8future / `chassis_suite` ecosystem. It is a curated, pre-integrated set of production-grade building blocks — configuration, structured logging, lifecycle/shutdown, HTTP and gRPC transports, resilient service-to-service calls, observability, security guards, an event bus, operational visibility, and typed clients for shared platform services. It is the shared substrate on which Go microservices in the suite are built, the Go-language sibling of the suite's other language chassis (TypeScript, Python). It is explicitly a **toolkit, not a framework**: chassis never owns `main()`, never calls application code, and never hides wiring behind magic. The developer imports exactly the packages they need and assembles them in their own `main()`.

# Why Does It Exist?

### The Core Problem

Every Go microservice needs the same foundational concerns: env-based config, structured JSON logging with trace correlation, graceful multi-component shutdown, health checks, HTTP middleware (request IDs, recovery, logging), gRPC interceptors, resilient outbound calls with retry and circuit breaking, rate limiting, feature flags, security headers/CORS, distributed tracing, metrics, webhooks, and event publishing. Without a shared toolkit, each team re-implements these inconsistently. The results are predictable: divergent error formats between services, observability present in some services and absent in others, different retry strategies, no standard health protocol, and no operational view of what is running and in what state. Operating the fleet gets harder with every new service.

### The Business Goal / Business Case

**Reduce time-to-production for a new microservice from weeks to hours while guaranteeing every service in the fleet meets a single, consistent production-readiness bar.** Concretely:

- **Consistency** — every service logs, errors, exposes health, and shuts down the same way, so the fleet is operable as one coherent system instead of a pile of snowflakes.
- **Faster onboarding** — a developer gets a production-grade service by importing chassis packages and wiring them; no need to hand-roll OpenTelemetry, a circuit breaker, or a rate limiter.
- **Operational visibility** — every chassis service self-registers, heartbeats, reports status, and accepts operational commands. There are no invisible services.
- **Fail-fast safety** — config errors, version mismatches, and init-ordering mistakes crash at startup with clear messages, before any traffic is served.
- **Platform connectivity** — services connect to the suite's entity registry, data lake, event bus, and common backends (LLM inference, vector/search, analytics) without writing boilerplate clients.

# Who Does It Serve?

chassis-go is consumed by **other code, not end users**:

- **Service developers** building Go microservices for the suite. They import chassis, wire it in `main()`, and get consistent observability, error handling, and operability.
- **Platform engineers** maintaining shared infrastructure (entity registry, data lake, event bus). They use the integration kits to keep cross-service communication uniform.
- **CLI / batch tool authors** who use `clikit` + the registry to ship visible, version-gated command-line tools that fit the same operational fabric.
- **Operations** who monitor and manage the fleet via the file registry, heartbeats, and lifecycle events, and can issue commands (stop, restart, custom) to running services.

# Business Capabilities

### 1. Version Compatibility Contract
Every service declares the chassis major it expects (`chassis.RequireMajor(11)`) at the top of `main()`; a mismatch exits immediately, and every chassis package asserts the gate was called before it runs. `SetAppVersion` additionally powers a free `--version` flag and stale-binary auto-rebuild. **Business value:** prevents silent behavioral drift on upgrade, turns version skew into an actionable message instead of subtle bugs, and forces a conscious upgrade checkpoint.

### 2. Fail-Fast Environment Configuration
`config` loads env vars into typed structs via struct tags (`string`, `int`, `int64`, `float64`, `bool`, `time.Duration`, `[]string`); required fields without defaults panic at startup. **Business value:** misconfiguration — the most common preventable production incident — is caught before the service accepts traffic, never leaving a service that looks healthy but is wrong.

### 3. Structured Logging with Trace Correlation
`logz` wraps `log/slog` to emit JSON with automatic OTel `trace_id`/`span_id` injection on every line in a traced context. **Business value:** logs are aggregation-ready (Datadog/Loki/ELK) and engineers can jump from a log line straight to its distributed trace, cutting mean-time-to-diagnose.

### 4. Graceful Shutdown Orchestration
`lifecycle.Run` catches SIGTERM/SIGINT, cancels a shared context, drains all components via `errgroup`, and fails the group if any peer errors. It also initializes the registry on startup. **Business value:** Kubernetes sends SIGTERM before killing pods; standardized drain prevents dropped in-flight requests, data corruption, and orphaned resources without each team re-solving it.

### 5. Operational Visibility via File Registry
On startup each service registers under `/tmp/chassis/<service>/` with a JSON PID file, a JSONL event log, and a polled command file; it heartbeats every 30s, polls commands every 3s, honors built-in `stop`/`restart`, and cleans up stale PIDs. **Business value:** answers "what is running here, in what state?" for dev/staging without a service mesh, and lets external tooling issue operational commands (including custom ones like cache flush).

### 6. Deterministic Port Assignment
`chassis.Port(name, offset)` derives a stable port (djb2 hash, range 5000–48000) with conventional role offsets (HTTP +0, gRPC +1, metrics +2). **Business value:** eliminates port-collision friction across developer machines and environments and gives tooling a predictable convention.

### 7. Resilient Outbound HTTP
`call` provides retry (exponential backoff + jitter on 5xx, never on 4xx), circuit breakers (Closed/Open/HalfOpen, global singletons keyed by name), timeouts, Bearer-token injection, OTel client spans, and batch concurrency via `work.Map`. **Business value:** service-to-service calls are the dominant microservice failure mode; backoff absorbs transient faults and breakers prevent cascade failures.

### 8. Unified Error Model
`errors.ServiceError` carries both an HTTP status and a gRPC code, supports fluent detail/cause chaining (`errors.Is/As`-compatible) and RFC 9457 Problem Details, with factory constructors for the standard categories (400/401/403/404/413/429/504/503/500). **Business value:** in a dual HTTP+gRPC fleet, one error type that knows both representations prevents mistranslation, and clients get machine-readable structured errors.

### 9. HTTP & gRPC Middleware
`httpkit` supplies `func(http.Handler) http.Handler` middleware (RequestID, Logging, Recovery, Tracing); `grpckit` supplies matching unary/stream interceptors plus health registration. Both use standard signatures. **Business value:** every transport gets consistent observability and panic safety while composing with any router or gRPC server — no proprietary framework to learn.

### 10. Parallel Health Aggregation
`health` runs dependency checks concurrently, reports every result (no short-circuit), and serves 200/503 over HTTP or healthy/unhealthy over gRPC Health V1. **Business value:** low-latency readiness probes that surface *all* failing dependencies, which is what diagnosing partial outages requires.

### 11. Request Guards (Defense-in-Depth)
`guard` provides LRU-bounded rate limiting (spoof-resistant key extraction), CORS, CIDR IP filtering (deny wins), security headers (CSP, HSTS, frame/MIME/referrer/permissions), body-size limits (413), and request timeouts (504). **Business value:** application-layer protection against abuse, cross-origin attacks, common web vulns, and resource-exhaustion — uniformly, per service.

### 12. Feature Flags with Stable Rollouts
`flagz` offers boolean checks, percentage rollouts via consistent FNV-1a hashing (same user → same result), string variants, and pluggable/composable sources (env, JSON, map), recorded as OTel span events. **Business value:** gradual rollouts, A/B tests, and kill switches without redeploys, with stable per-user behavior.

### 13. OTel-Native Observability
`otel` boots the SDK in one call (OTLP gRPC traces+metrics, W3C propagation, configurable samplers); `metrics` is a recorder with cardinality protection (max 1000 label combos/metric). All packages emit telemetry automatically. **Business value:** observability is the default rather than an afterthought, and cardinality limits keep high-cardinality labels from overwhelming the metrics backend.

### 14. Security & Crypto Primitives
`secval` blocks prototype-pollution keys (`__proto__`, `constructor`, `prototype`) and >20-level nesting; `seal` provides AES-256-GCM, HMAC-SHA256, and temporary signed tokens (expiry + JTI). **Business value:** prevents a class of injection attacks and the common pitfalls of hand-rolled crypto (weak key derivation, nonce reuse, timing leaks).

### 15. Structured Concurrency & Scheduling
`work` offers `Map`/`All`/`Race`/`Stream` with bounded pools and OTel tracing; `tick.Every` runs lifecycle-integrated periodic tasks with jitter and error policy; `cache` is a generic LRU+TTL store. **Business value:** safe fan-out/fan-in without goroutine leaks, thundering-herd-resistant periodic work, and a standard in-memory cache instead of per-team variants.

### 16. Webhook Delivery & Deploy Conventions
`webhook` sends HMAC-SHA256-signed deliveries with retry, delivery IDs, and a receive-side `VerifyPayload`; `deploy` discovers convention-based deploy directories, loads env/secret files without clobbering, finds TLS certs, and detects the runtime environment. **Business value:** integrity-verified event notifications to external systems, and consistent, secure per-environment configuration across the fleet.

### 17. Event Bus & Schema Contracts
`kafkakit` publishes/subscribes to Redpanda with a standard envelope (event ID, ms timestamp, source, subject, trace ID, tenant ID, entity refs), tenant-based delivery filtering, bounded metadata-preserving dead-letter routing, wildcard subscriptions, and deprecated auto-commit or partition-ordered `manual-contiguous` delivery; `schemakit` validates and registers Avro schemas in Confluent wire format. **Business value:** the event bus is the asynchronous backbone — standard envelopes keep every event traceable, attributable, and tenant-filterable, durable contiguous commits prevent poison records from being skipped, and schema enforcement protects event contracts.

### 18. Liveness & Lifecycle Events
`heartbeatkit` emits liveness to `ai8.infra.heartbeat` every 30s; `announcekit` emits service- and job-lifecycle events to `ai8.infra.{service}.lifecycle.{state}` and `.../job.{state}`. Both build on `kafkakit`. **Business value:** automated dead-service detection plus dashboards, alerting, and audit trails of service and batch-job state transitions.

### 19. Platform & Backend Clients
Typed `call`-backed clients: `registrykit` (registry_svc entity/relationship/graph operations), `lakekit` (lake_svc SQL/history/datasets), `inferkit` (OpenAI-compatible LLM chat/stream/embeddings), `ollamakit` (Ollama native API), `meilikit` (Meilisearch), `qdrantkit` (Qdrant vectors), and `posthogkit` (batched analytics). **Business value:** every consumer talks to shared infrastructure and common backends through one client with consistent timeouts, retries, headers (`X-Tenant-ID`, `X-Trace-ID`), and tracing — no ad-hoc HTTP clients.

### 20. CLI Toolkit, Secret Hydration & Durable Workflows
`clikit` is a stdlib-first CLI toolkit (flat commands, env+flag binding, JSON-safe output, exit codes, color, signal-aware context, opt-in registry completion) that never owns `main()` or calls `os.Exit`; `phasekit` hydrates env vars from Phase via the `phase` CLI before `config.MustLoad`; `inngestkit` is thin setup glue for the Inngest Go SDK. **Business value:** batch tools and CLIs join the same version-gate and operational fabric, secrets load consistently without linking a vendor SDK, and durable-workflow services wire up with minimal, opt-in glue.

# Business Logic and Rules / Key Design Decisions

| Decision | Rule | Why this matters |
|---|---|---|
| Toolkit, not framework | chassis never owns `main()` or calls app code | Business logic stays pure, portable, and testable; no lock-in to the toolkit |
| Version gate is mandatory | `RequireMajor(11)` first; every module asserts it | Upgrades are conscious checkpoints; skew fails loudly, not subtly |
| Fail fast everywhere | Missing config, bad guard params, wrong major, or pre-gate use all crash at startup | Catching errors before traffic is strictly safer than failing under load |
| Tier isolation | Importing `config`/`logz` does not pull gRPC or the OTel SDK; heavy deps live in the kits that need them | A simple CLI pays only for `golang.org/x/sync`, not the full dependency tree |
| OTel native | Tracing, metrics, and log correlation are built into every layer | Observability is the default state, not a per-team add-on |
| Standard interfaces | HTTP `func(http.Handler) http.Handler`, standard gRPC interceptors, `*slog.Logger` | Composes with the broader Go ecosystem; nothing proprietary to learn |
| Cardinality cap | `metrics` drops new label combos after 1000/metric | Prevents high-cardinality labels from exploding the metrics backend |
| Retry policy | `call`/`webhook` retry on 5xx with backoff+jitter, never on 4xx | Absorbs transient failures without hammering on deterministic client errors |
| Global breakers | Circuit breakers are singletons keyed by name | All clients to a downstream share breaker state, so the fleet fast-fails together |
| Registry security | Dirs 0700, PID files 0600, sensitive args redacted, atomic writes | Operational visibility must not leak secrets or corrupt state |
| secval scope | Reject `__proto__`/`constructor`/`prototype` and >20 nesting; not for file uploads/streams (parses fully into memory) | Stops prototype pollution and nesting attacks; size limits must come first |
| Go floor | `go.mod` and build/CI/Docker use Go 1.26.5+ | The patch floor carries stdlib/toolchain security fixes |

# How to Think About Code Changes

- **Preserve the toolkit contract.** Do not make chassis own `main()`, auto-wire, or call application code. New capabilities are opt-in packages the consumer assembles.
- **Respect tier isolation.** Foundation packages (`chassis`, `config`, `logz`, `clikit`, `lifecycle`, `registry`, `testkit`) must not gain transport/runtime dependencies (gRPC, OTel SDK, Kafka, Avro). Heavy dependencies belong only in the kit that needs them. Verify with `go list -deps`.
- **Keep the version gate first.** Any new entry point asserts the gate; any new module calls `AssertVersionChecked()` at its boundaries. The module path is `github.com/ai8future/chassis-go/v11` — all internal imports use `/v11`.
- **What belongs here vs. a sibling repo:** generic, cross-service infrastructure belongs in chassis-go. Database drivers do **not** — chassis ships none; Postgres pairs with `chassis-go-addons/pgkit`. Business/domain logic belongs in the consuming service, never here. Equivalent capabilities for other languages live in the sibling chassis repos, not here.
- **Honor AGENTS.md:** increment `VERSION` and annotate `CHANGELOG.md` for code changes (read `VERSION` only at the last moment to avoid collisions), keep `appversion.go` embedding `VERSION` in consumers, and stay out of the `_studies`/`_proposals`/`_rcodegen`/`_bugs_*` directories.

# Deployment Model / Scale

chassis-go is a **library**, not a deployed service — it has no runtime of its own and is `go get`-imported into consumer services. It is designed for fleet scale: every consumer self-registers, heartbeats, and emits lifecycle events so the fleet is observable as a coherent system. It targets containerized/Kubernetes deployment (graceful SIGTERM handling, deploy-directory conventions, OTLP export) but runs anywhere a Go binary runs. Dependency weight scales with usage — a CLI tool stays lean while a full service opts into transports, the event bus, and platform clients.

# Target Users / Use Cases

- New Go microservices needing a production baseline (config, logging, transports, health, shutdown) in hours.
- Services that must integrate with the suite's registry, data lake, or event bus.
- Services that call LLM inference, vector/search, or analytics backends and want one resilient, traced client.
- Version-gated CLI and batch tools that should be visible to operations like long-running services.

# Current State / Status

- **Version:** 11.3.5 (Go 1.26.5 floor; build/CI on Go 1.26.5+). MIT licensed.
- **Maturity:** in active production use across the fleet; eleven major versions since the initial release in February 2026 (the v4 module-path migration landed Feb 8, 2026), reflecting rapid iteration driven by real adoption.
- **Built today:** all packages in this document are implemented and tested (~63 test files), with runnable examples in `examples/01-cli` through `examples/05-clikit` and a `cmd/demo-shutdown` graceful-shutdown demo.
- **Notes / planned:** `inngestkit` durable-workflow integration is available but optional and not required for service completion; `phasekit` ships with dynamic secret leases disabled in v1; database access is intentionally out of scope (pair with `chassis-go-addons/pgkit`).
