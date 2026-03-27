# chassis-go: Product Overview

## What Is This Product?

chassis-go is a standardized Go microservice toolkit that eliminates the need for every engineering team to independently solve the same foundational infrastructure problems. It provides a curated, pre-integrated set of production-grade building blocks covering configuration, logging, lifecycle management, HTTP and gRPC transports, resilient service-to-service communication, observability, security, event streaming, and operational visibility. It is the shared substrate on which all Go microservices in the ai8future ecosystem are built.

chassis-go is explicitly a **toolkit, not a framework**. It never owns `main()`. It never calls application code. The developer wires it together explicitly, choosing exactly the components they need. This distinction exists because the organization values business logic that remains pure, portable, and decoupled from infrastructure concerns.

---

## Why Does This Product Exist?

### The Core Problem

Every Go microservice needs the same foundational capabilities: environment-based configuration, structured JSON logging with trace correlation, graceful multi-component shutdown, health checks, HTTP middleware (request IDs, panic recovery, request logging), gRPC interceptors, resilient outbound HTTP calls with retry and circuit breaking, rate limiting, feature flags, security headers, CORS, distributed tracing, metrics, and webhook delivery.

Without a shared toolkit, teams re-implement these capabilities inconsistently across services. The results are predictable: inconsistent error formats between services, missing observability in some services, different retry strategies for outbound calls, no standardized health check protocol, no operational visibility into what services are running and in what state. Debugging and operating the fleet becomes increasingly difficult as the number of services grows.

### The Business Goal

**Reduce time-to-production for new microservices from weeks to hours while ensuring every service in the fleet meets a consistent production-readiness bar.**

Specifically:

1. **Consistency across the fleet**: Every service logs the same way, reports errors the same way, exposes health the same way, handles shutdown the same way. This makes the entire fleet operable as a coherent system rather than a collection of snowflakes.

2. **Faster onboarding**: A new developer can spin up a production-grade service by importing chassis packages and wiring them in `main()`. They do not need to learn how to set up OpenTelemetry, how to build a circuit breaker, or how to write a rate limiter.

3. **Operational visibility**: Every service that uses chassis automatically registers itself, publishes heartbeats, reports status, accepts operational commands, and (when integrated with xyops) pushes metrics to a centralized monitoring system. There are no invisible services.

4. **Fail-fast safety**: Configuration errors, version mismatches, and initialization ordering mistakes crash the process immediately at startup with clear messages. This prevents services from running in degraded or undefined states that are far harder to diagnose in production.

5. **Platform connectivity**: Services built with chassis can immediately connect to the organization's entity registry, knowledge graph, data lake, and event bus without writing boilerplate HTTP clients or Kafka producers/consumers.

---

## What Business Logic Does It Contain?

### 1. Version Compatibility Contract

chassis-go enforces a mandatory version gate. Every service must declare at the top of `main()` which major version of chassis it expects (`chassis.RequireMajor(10)`). If the installed library's major version does not match, the process exits immediately. Every chassis package also checks that `RequireMajor` was called before it runs.

**Business rationale**: This prevents silent behavioral changes when the toolkit is upgraded without review. A version mismatch produces a clear, actionable error message rather than subtle bugs. It also creates an upgrade checkpoint — teams must consciously acknowledge major version changes.

### 2. Environment-Based Configuration with Fail-Fast Semantics

The `config` package loads environment variables into typed Go structs via struct tags. Fields without defaults that are not present in the environment cause an immediate panic at startup.

**Business rationale**: Configuration errors are the most common class of production incidents that are preventable at startup. By panicking immediately when required configuration is missing, the service never reaches a state where it appears healthy but is actually misconfigured. This "binary safety" principle means: either the service has everything it needs to run, or it crashes before accepting any traffic.

Supported types include strings, integers, floats, booleans, durations, and comma-separated string slices, covering the vast majority of service configuration needs without requiring external config file parsers.

### 3. Structured Logging with Automatic Trace Correlation

The `logz` package wraps Go's standard `log/slog` library to produce JSON logs with automatic OpenTelemetry trace ID and span ID injection. When a request is being traced, every log line produced in that request's context automatically includes `trace_id` and `span_id` fields.

**Business rationale**: Structured JSON logging is required for production log aggregation systems (ELK, Loki, Datadog). Automatic trace correlation means engineers can jump from a log line directly to the distributed trace without manual instrumentation. This dramatically reduces mean-time-to-diagnose for production issues.

### 4. Graceful Shutdown Orchestration

The `lifecycle` package provides signal-aware shutdown orchestration. It catches SIGTERM/SIGINT, cancels a shared context, and waits for all service components (HTTP server, gRPC server, background workers, event consumers) to drain cleanly. If any component returns an error, all others are signaled to stop.

**Business rationale**: Kubernetes sends SIGTERM before killing pods. Services that do not handle graceful shutdown drop in-flight requests, corrupt data, or leave orphaned resources. By providing a standardized shutdown orchestrator, every chassis service handles these signals correctly without each team needing to implement the pattern themselves.

### 5. File-Based Service Registration and Command System

The `registry` package creates a per-service directory under `/tmp/chassis/<service-name>/` containing a JSON PID file, a structured JSONL event log, and a command file. Services automatically register on startup, log heartbeats every 30 seconds, poll for commands every 3 seconds, and clean up stale registrations from dead processes.

**Business rationale**: This solves the "what is running on this machine?" problem that plagues development and staging environments. External tooling (viewers, dashboards, CLI utilities) can discover all running chassis services, see their status, inspect their ports, and send commands (stop, restart, custom operations like cache flushing) without needing a service mesh or container orchestrator. The command system enables operational workflows like graceful restarts and custom administrative actions.

The registry supports both **service mode** (long-running processes with heartbeat) and **CLI/batch mode** (short-lived processes with progress tracking), ensuring that even batch jobs are visible to operations.

Security is enforced: directory permissions are verified at 0700, PID files use 0600 permissions, sensitive command-line arguments (passwords, tokens, keys) are automatically redacted, and atomic file writes prevent corruption.

### 6. Deterministic Port Assignment

The root `chassis` package provides `Port(name, offset)` which derives a stable, deterministic port number from a service name using the djb2 hash algorithm. The result is always in the range 5000-48000, safely below the OS ephemeral port range.

**Business rationale**: When multiple services run on the same development machine, port collisions are a constant friction. Deterministic ports mean that the same service always gets the same port across all developer machines and environments. Standard role offsets (HTTP=+0, gRPC=+1, metrics=+2) create a predictable convention that tooling can rely on.

### 7. Resilient Outbound HTTP Client

The `call` package provides an HTTP client with configurable retry (exponential backoff with jitter on 5xx, never on 4xx), circuit breaker (Closed/Open/HalfOpen states with configurable thresholds), timeout enforcement, automatic Bearer token injection, and automatic OTel client span creation with W3C trace header propagation.

Circuit breakers are global singletons keyed by name, so multiple clients hitting the same downstream service share circuit state. The client also supports batch concurrent requests via `work.Map`.

**Business rationale**: Service-to-service calls are the primary failure mode in microservice architectures. Retry with backoff handles transient failures. Circuit breakers prevent cascade failures by fast-failing when a downstream service is unhealthy. Token injection removes authentication boilerplate. OTel integration means every outbound call appears in distributed traces automatically.

### 8. Unified Error Type with Dual HTTP/gRPC Codes and RFC 9457 Problem Details

The `errors` package provides a `ServiceError` type that carries both an HTTP status code and a gRPC status code, fluent detail attachment, error cause chaining (compatible with `errors.Is/As`), and RFC 9457 Problem Details rendering.

Factory constructors cover the standard error categories: validation (400), unauthorized (401), forbidden (403), not found (404), payload too large (413), rate limit (429), timeout (504), dependency unavailable (503), and internal (500).

**Business rationale**: In a system with both HTTP and gRPC transports, errors must translate cleanly across protocols. A single error type that knows both its HTTP and gRPC representation prevents the common bug where an error is 404 in HTTP but maps to INTERNAL in gRPC. RFC 9457 Problem Details compliance means API consumers get structured, machine-readable error responses rather than ad-hoc JSON.

### 9. HTTP Middleware Stack

The `httpkit` package provides standard `func(http.Handler) http.Handler` middleware for request ID generation (UUID v4), structured request logging, panic recovery (catches panics, logs stack traces, returns 500), and OTel tracing (creates server spans, extracts W3C TraceContext from incoming headers).

**Business rationale**: Every HTTP service needs these four capabilities. By providing them as composable middleware compatible with any Go router (stdlib ServeMux, chi, gorilla/mux), chassis eliminates duplicate implementations while preserving full routing flexibility.

### 10. gRPC Interceptor Stack

The `grpckit` package provides unary and stream interceptors for logging, panic recovery (logs panics with full stack traces, returns `codes.Internal`), metrics (OTel `rpc.server.duration` histogram), and tracing. It also provides health service registration that decouples gRPC from the health check package.

**Business rationale**: The same four concerns that apply to HTTP apply to gRPC. Standardized interceptors ensure every gRPC service in the fleet has consistent observability and error handling.

### 11. Health Check Aggregation

The `health` package runs multiple dependency checks (database, cache, external services) in parallel, reports per-check results, and returns 200/503 for HTTP or healthy/unhealthy for gRPC Health V1. Individual check failures do not short-circuit other checks.

**Business rationale**: Load balancers and Kubernetes readiness probes need health endpoints. Running checks in parallel keeps health endpoint latency low. Reporting all failures (not just the first) is critical for diagnosing partial outages where multiple dependencies fail simultaneously.

### 12. Request Guards

The `guard` package provides HTTP middleware for six protection concerns:

- **Rate limiting**: Per-key token bucket with LRU eviction for bounded memory. Configurable key extraction (remote address, X-Forwarded-For with trusted CIDR ranges, arbitrary headers). Returns 429 with RFC 9457 Problem Details.
- **CORS**: Preflight handling, origin matching, configurable methods/headers/max-age, credentials validation with wildcard origin safety check.
- **Security headers**: CSP, HSTS (2 years), X-Frame-Options: DENY, X-Content-Type-Options, Referrer-Policy, Permissions-Policy.
- **IP filtering**: CIDR-based allow/deny lists with deny-takes-precedence semantics.
- **Body size limits**: Rejects oversized payloads with 413 before processing.
- **Request timeouts**: Buffered response writer with 504 Gateway Timeout on deadline.

**Business rationale**: These guards implement defense-in-depth at the application layer. Rate limiting prevents abuse and protects downstream services. CORS prevents cross-origin attacks. Security headers prevent common web vulnerabilities (clickjacking, MIME sniffing, XSS). IP filtering restricts access to internal services. Body limits prevent memory exhaustion attacks. Timeouts prevent slow requests from consuming resources indefinitely.

### 13. Feature Flags with Percentage Rollouts

The `flagz` package provides feature flags with boolean checks, percentage rollouts using consistent FNV-1a hashing (the same user always gets the same result for the same flag), string variants, and pluggable sources (environment variables, JSON files, in-memory maps, composable multi-source with override semantics).

Flag evaluations are automatically recorded as OTel span events when tracing is active.

**Business rationale**: Feature flags enable gradual rollouts, A/B testing, and kill switches without redeployment. Consistent hashing ensures that percentage rollouts are stable per-user, preventing the disorienting experience of features flickering on and off. Multi-source support allows flags to be managed differently per environment (dev vs. production).

### 14. OpenTelemetry-Native Observability

The `otel` package provides one-call initialization of the OpenTelemetry SDK with OTLP gRPC exporters for traces and metrics, W3C propagation, and configurable samplers. The `metrics` package provides a pre-configured metrics recorder with cardinality protection (max 1000 label combinations per metric to prevent backend explosions).

All chassis packages integrate with OTel automatically: HTTP middleware creates server spans, gRPC interceptors create server spans, outbound HTTP calls create client spans, structured concurrency operations create parent/child spans, feature flag evaluations create span events, and log lines include trace context.

**Business rationale**: Distributed tracing and metrics are essential for operating microservices in production. By building OTel integration into every layer of the toolkit, chassis ensures that observability is not an afterthought that requires manual instrumentation. Cardinality protection prevents the common problem of high-cardinality labels (user IDs, request paths) overwhelming metrics backends.

### 15. JSON Security Validation

The `secval` package validates JSON payloads against prototype pollution keys (`__proto__`, `constructor`, `prototype`) and excessive nesting (max 20 levels).

**Business rationale**: Prototype pollution is a class of attack where malicious JSON keys like `__proto__` can modify object prototypes in downstream JavaScript consumers, leading to security vulnerabilities. Even in a Go backend, these payloads may be stored and later consumed by frontend or Node.js services. Nesting depth limits prevent stack overflow attacks from deeply nested JSON.

### 16. Structured Concurrency Primitives

The `work` package provides four patterns for parallel execution with bounded worker pools and automatic OTel tracing:

- **Map**: Transform items concurrently, preserving input order.
- **All**: Run heterogeneous tasks concurrently, fail on first error.
- **Race**: First success wins, cancel the rest.
- **Stream**: Process channel items concurrently as a pipeline.

**Business rationale**: Fan-out/fan-in workloads are ubiquitous in microservices (fetching data from multiple sources, processing batches, racing primary vs. replica). These primitives prevent goroutine leaks, respect context cancellation, enforce bounded concurrency to prevent resource exhaustion, and automatically trace each unit of work.

### 17. Cryptographic Primitives

The `seal` package provides AES-256-GCM encryption/decryption with scrypt key derivation, HMAC-SHA256 signing/verification, and temporary signed tokens with expiry and unique JTI.

**Business rationale**: Services frequently need to encrypt sensitive data, sign payloads for integrity verification, and create short-lived tokens for inter-service authentication or user sessions. By providing these as tested, audited primitives, chassis prevents the common mistakes of hand-rolled cryptography (weak key derivation, nonce reuse, timing attacks in signature verification).

### 18. HMAC-Signed Webhook Delivery

The `webhook` package provides HMAC-SHA256 signed webhook delivery with automatic retry on 5xx errors, delivery tracking, and a `VerifyPayload` function for the receive side. Every webhook includes a unique delivery ID, timestamp, and cryptographic signature.

**Business rationale**: Webhooks are the standard mechanism for notifying external systems of events. HMAC signing ensures payload integrity and authenticity. Retry ensures delivery despite transient failures. Delivery tracking enables debugging delivery issues. The verify function on the receive side makes it easy for any chassis service to be a webhook consumer.

### 19. Convention-Based Deploy Directory

The `deploy` package discovers deploy-time configuration from a convention-based directory layout (`/app/deploy/<name>/` for Kubernetes, `~/deploy/<name>/` for developers). It loads environment files (config.env, secrets.env) without overwriting existing env vars, discovers TLS certificates, reads deploy metadata, detects the runtime environment (Kubernetes, container, VM, bare-metal), declares endpoints and dependency topology, and produces structured health payloads.

**Business rationale**: Services need different configuration in different environments. A convention-based directory structure means configuration management is consistent across the fleet. Loading secrets from files (rather than environment variables alone) is more secure for container deployments. Runtime detection enables environment-aware behavior without explicit configuration.

### 20. Periodic Task Scheduling

The `tick` package provides `Every(interval, fn, opts...)` which returns a lifecycle-compatible component for running periodic tasks with configurable jitter, immediate first execution, and error behavior (skip errors and continue, or stop on first error).

**Business rationale**: Many services need periodic background work (cache warming, metric aggregation, health checks, cleanup tasks). This primitive integrates cleanly with the lifecycle system and supports jitter to prevent thundering herds when multiple instances of a service tick at the same interval.

### 21. In-Memory Cache

The `cache` package provides a generic LRU+TTL in-memory cache with configurable max size, TTL, and a `Prune()` method.

**Business rationale**: Many services need short-lived caching of frequently accessed data (API responses, database query results, configuration lookups). A built-in cache prevents each team from implementing their own with different eviction and expiry semantics.

### 22. Event Bus Integration (Kafka/Redpanda)

The `kafkakit` package provides publish/subscribe to Kafka/Redpanda with a standardized event envelope (unique event IDs, millisecond timestamps, source identity, subject, OTel trace ID extraction, tenant ID, entity references), tenant-based filtering (own/shared/granted tenant delivery logic), dead letter queue routing on handler errors, wildcard pattern matching for subscriptions, and publisher statistics.

The `schemakit` package provides Avro schema loading from `.avsc` files, validation, serialization/deserialization in Confluent wire format, and schema registration with Confluent Schema Registry.

**Business rationale**: The event bus is the backbone of the asynchronous communication architecture. Standardized envelopes ensure every event is traceable, attributable to a source, and filterable by tenant. Tenant filtering is essential for multi-tenant architectures where a single Kafka cluster serves multiple tenants. Dead letter queues prevent poison messages from blocking consumers. Schema validation ensures event contracts are enforced at both publish and consume time.

### 23. Automatic Liveness and Lifecycle Events

The `heartbeatkit` package publishes heartbeat events to `ai8.infra.heartbeat` every 30 seconds with service name, hostname, PID, uptime, version, and optional publisher statistics. The `announcekit` package publishes standardized service lifecycle events (started, ready, stopping, failed) and job lifecycle events (started, complete, failed) to well-known Kafka subjects.

**Business rationale**: In a microservice fleet, knowing which services are alive and in what state is fundamental to operations. Heartbeats enable automated dead-service detection. Lifecycle events enable dashboards, alerting, and audit trails of service state transitions. Job lifecycle events enable tracking batch processing across the fleet.

### 24. Distributed Trace ID Propagation

The `tracekit` package provides lightweight trace ID propagation (`tr_` + 12 hex characters) via Go contexts and HTTP headers (`X-Trace-ID`). It complements OTel tracing for scenarios where the full OTel SDK is not available or appropriate.

**Business rationale**: Not every service or client can run the full OTel stack. A lightweight trace ID that propagates through HTTP headers and event envelopes ensures end-to-end traceability even in environments without OTel collectors.

### 25. Platform Service Clients

Three client packages provide typed HTTP clients for the organization's core platform services:

- **registrykit**: Client for `registry_svc` (entity registry). Supports entity resolution by CRD, domain, email, slug, or namespaced identifier; relationship traversal; ancestor/descendant queries; graph neighborhood queries; entity creation; identifier management; relationship creation; and entity merging.

- **graphkit**: Client for `graphiti_svc` (knowledge graph). Supports entity search, temporal recall, Cypher query execution, entity graph neighborhood queries, entity timeline history, and path traversal between entities.

- **lakekit**: Client for `lake_svc` (data lake). Supports SQL queries, entity event history, dataset listing, and dataset statistics.

All three clients set standardized `X-Tenant-ID` and `X-Trace-ID` headers on every request.

**Business rationale**: These three services (entity registry, knowledge graph, data lake) are shared infrastructure that multiple services need to query. Typed client libraries prevent each consuming service from writing ad-hoc HTTP clients with inconsistent error handling, timeout policies, and header conventions.

### 26. XYOps Integration (Operational Management)

The `xyops` package provides a client for the XYOps operational management platform with curated API methods (trigger events, poll job status, cancel jobs, search job history, list/acknowledge alerts, fire webhooks), a monitoring bridge that pushes bridged application metrics to XYOps on a configurable interval, response caching, and a raw escape hatch for custom API endpoints.

The `xyopsworker` package provides a job execution framework where services can register handlers for specific job types and act as XYOps satellite workers, receiving and executing jobs dispatched from the central orchestrator with progress reporting, live logging, and output capture.

**Business rationale**: XYOps is the operational control plane. A service that uses chassis but not XYOps is "invisible to operations" -- it cannot be monitored, alerted on, or managed through the standard operational workflows. The monitoring bridge is considered the single most important integration because it makes services visible to the operations team. The worker framework enables automation (deployments, migrations, batch processing, cleanup) to be orchestrated centrally while executing distributed.

---

## Who Are the Target Users?

1. **Service developers** building new Go microservices for the ai8future ecosystem. They import chassis, wire the packages together in `main()`, and get a production-ready service with consistent observability, error handling, and operational visibility.

2. **Platform engineers** maintaining the shared infrastructure (entity registry, knowledge graph, data lake, event bus, XYOps). They use the integration packages to ensure consistent cross-service communication patterns.

3. **Operations teams** who monitor and manage the fleet. The registry, heartbeat, lifecycle events, and XYOps integration give them visibility into what is running, what state it is in, and the ability to issue commands to running services.

---

## Design Philosophy as Business Strategy

### Toolkit, Not Framework
The organization's architecture principle is "Library-First" -- business logic stays pure and portable. chassis never owns the application. This prevents vendor lock-in to the toolkit itself and means business logic can be extracted and tested independently.

### Tier Isolation
Importing low-level packages (config, logz) does not pull in heavy dependencies (gRPC, OTel SDK). This means a simple CLI tool that only needs config and logging pays only the cost of `golang.org/x/sync`, not the full gRPC and OTel dependency tree. Dependencies scale with what you use.

### Fail Fast
Missing config panics. Invalid guard config panics. Wrong major version crashes. Using a chassis module before calling `RequireMajor` crashes. These are all intentional. The philosophy is that catching errors at process startup (before any traffic is served) is strictly better than catching them at runtime under load.

### OTel Native
Tracing, metrics, and log correlation are built into every layer rather than bolted on. This means observability is the default state, not something teams need to remember to add.

### Standard Interfaces
HTTP middleware uses the standard `func(http.Handler) http.Handler` signature. gRPC uses standard interceptors. Logging uses `*slog.Logger`. These choices mean chassis composes with the broader Go ecosystem rather than requiring a proprietary middleware framework.

---

## Current Version and Maturity

The toolkit is at version 10.0.5 as of March 2026. It has gone through 10 major versions since its initial release in February 2026, reflecting rapid iteration driven by real-world adoption across the ai8future service fleet. The first planned consumers were `pricing-cli` (CLI tool validating Tier 1 packages) and `serp_svc` (full-stack service validating all tiers).

The changelog shows a progression from foundational packages (config, logging, lifecycle, HTTP, gRPC, health, call) through security hardening (registry permissions, credential redaction, safe file writes), operational tooling (registry, CLI mode, deploy directories, XYOps), cross-cutting concerns (feature flags, guards, structured concurrency, cryptography, webhooks), and platform connectivity (event bus, schema management, service clients).
