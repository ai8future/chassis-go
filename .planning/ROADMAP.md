# Roadmap: chassis-go

## Overview

Build a composable Go toolkit from the ground up, starting with project scaffolding and foundational packages (config, logging, lifecycle, testkit), then layering on transport utilities (HTTP, gRPC), health checking, and a resilient HTTP client. Each phase delivers one independent, testable package. Conclude with living documentation examples that validate the full toolkit integration.

## Domain Expertise

None

## Phases

- [ ] **Phase 1: Project Setup** - Go module init, CI, repo scaffolding
- [ ] **Phase 2: Config** - Env var loading into typed structs with MustLoad
- [ ] **Phase 3: Logging** - Structured JSON logging wrapping slog with TraceID extraction
- [ ] **Phase 4: Lifecycle** - Graceful shutdown orchestration via errgroup
- [ ] **Phase 5: Test Kit** - Testing utilities for logger, config, and ports
- [ ] **Phase 6: HTTP Kit** - HTTP server middleware (RequestID, logging, recovery, JSON errors)
- [ ] **Phase 7: Health** - Health check protocol with parallel aggregation
- [ ] **Phase 8: gRPC Kit** - gRPC server interceptors and health service wiring
- [ ] **Phase 9: Call** - Intelligent HTTP client with retries, circuit breaking, deadline propagation
- [ ] **Phase 10: Examples** - Living documentation (CLI, service, client examples)

## Phase Details

### Phase 1: Project Setup
**Goal**: Initialize the Go module, CI pipeline, and repo structure
**Depends on**: Nothing (first phase)
**Requirements**: SETUP-01, SETUP-02, SETUP-03
**Research**: Unlikely (standard Go project setup)
**Plans**: TBD

Plans:
- [ ] 01-01: Initialize Go module, .gitignore, LICENSE, README
- [ ] 01-02: Set up CI pipeline (GitHub Actions) with test, lint, vet

### Phase 2: Config
**Goal**: Implement `chassis/config` — env var loading into typed structs with fail-fast semantics
**Depends on**: Phase 1
**Requirements**: CFG-01, CFG-02, CFG-03, CFG-04, CFG-05
**Research**: Unlikely (env var parsing is well-understood, design doc specifies approach)
**Plans**: TBD

Plans:
- [ ] 02-01: Core MustLoad with struct tag parsing (string, int, bool, duration)
- [ ] 02-02: Slice support, default values, optional fields
- [ ] 02-03: Config package tests (happy path, panic on missing, defaults, edge cases)

### Phase 3: Logging
**Goal**: Implement `chassis/logz` — structured JSON logging with TraceID context extraction
**Depends on**: Phase 1
**Requirements**: LOG-01, LOG-02, LOG-03, LOG-04
**Research**: Unlikely (wrapping slog is standard, custom Handler pattern is documented)
**Plans**: TBD

Plans:
- [ ] 03-01: logz.New with JSON handler and level configuration
- [ ] 03-02: Custom slog Handler for TraceID extraction from context
- [ ] 03-03: Logging package tests (levels, JSON output, TraceID injection)

### Phase 4: Lifecycle
**Goal**: Implement `chassis/lifecycle` — orchestration and graceful shutdown
**Depends on**: Phase 1
**Requirements**: LIFE-01, LIFE-02, LIFE-03, LIFE-04
**Research**: Unlikely (errgroup + signal handling is established Go pattern)
**Plans**: TBD

Plans:
- [ ] 04-01: Component type, Run function with errgroup orchestration
- [ ] 04-02: Signal handling (SIGTERM/SIGINT) and context cancellation
- [ ] 04-03: Lifecycle package tests and shutdown verification

### Phase 5: Test Kit
**Goal**: Implement `chassis/testkit` — testing utilities for the ecosystem
**Depends on**: Phase 2, Phase 3 (testkit wraps config and logger patterns)
**Requirements**: TEST-01, TEST-02, TEST-03
**Research**: Unlikely (test helpers are straightforward)
**Plans**: TBD

Plans:
- [ ] 05-01: NewLogger, LoadConfig, GetFreePort implementations
- [ ] 05-02: Testkit package tests

### Phase 6: HTTP Kit
**Goal**: Implement `chassis/httpkit` — HTTP server middleware stack
**Depends on**: Phase 3 (logging middleware needs logz patterns)
**Requirements**: HTTP-01, HTTP-02, HTTP-03, HTTP-04
**Research**: Unlikely (standard net/http middleware patterns)
**Plans**: TBD

Plans:
- [ ] 06-01: RequestID middleware and JSON error response helper
- [ ] 06-02: Logging middleware and recovery middleware
- [ ] 06-03: HTTP kit integration tests (middleware chain)

### Phase 7: Health
**Goal**: Implement `chassis/health` — health check protocol with parallel aggregation
**Depends on**: Phase 1
**Requirements**: HLTH-01, HLTH-02, HLTH-03, HLTH-04, HLTH-05
**Research**: Likely (gRPC Health V1 protocol specifics)
**Research topics**: grpc.health.v1 proto contract, adapter pattern for mapping aggregated checks to gRPC health responses
**Plans**: TBD

Plans:
- [ ] 07-01: Check type, All() aggregator with errgroup and errors.Join
- [ ] 07-02: HTTP health handler (200/503 with check result body)
- [ ] 07-03: gRPC Health V1 adapter
- [ ] 07-04: Health package tests (parallel checks, failure aggregation, HTTP/gRPC responses)

### Phase 8: gRPC Kit
**Goal**: Implement `chassis/grpckit` — gRPC server interceptors and health wiring
**Depends on**: Phase 3, Phase 7 (logging interceptor needs logz, health wiring needs health package)
**Requirements**: GRPC-01, GRPC-02, GRPC-03, GRPC-04
**Research**: Likely (gRPC interceptor API, grpc-go middleware patterns)
**Research topics**: grpc-go unary/stream interceptor signatures, chaining interceptors, grpc.health.v1 service registration
**Plans**: TBD

Plans:
- [ ] 08-01: Unary interceptors (logging, recovery, metrics placeholder)
- [ ] 08-02: Stream interceptors (logging, recovery)
- [ ] 08-03: Health service registration helper
- [ ] 08-04: gRPC kit integration tests

### Phase 9: Call
**Goal**: Implement `chassis/call` — intelligent HTTP client with retries, circuit breaking, deadline propagation
**Depends on**: Phase 1
**Requirements**: CALL-01, CALL-02, CALL-03, CALL-04, CALL-05, CALL-06, CALL-07, CALL-08
**Research**: Likely (circuit breaker state machine design, backoff algorithms)
**Research topics**: Exponential backoff with jitter algorithm, circuit breaker state machine (Open/Half-Open/Closed), adapter pattern for implementation-agnostic breaker
**Plans**: TBD

Plans:
- [ ] 09-01: Client builder with timeout and context enforcement
- [ ] 09-02: Retry middleware (exponential backoff + jitter, 5xx only, deadline-aware)
- [ ] 09-03: Circuit breaker middleware (state machine, singleton named breakers)
- [ ] 09-04: No-4xx-retry guard and deadline propagation
- [ ] 09-05: Call package integration tests (retry behavior, breaker transitions)

### Phase 10: Examples
**Goal**: Create living documentation examples demonstrating full toolkit usage
**Depends on**: All previous phases
**Requirements**: EX-01, EX-02, EX-03
**Research**: Unlikely (assembling already-built packages)
**Plans**: TBD

Plans:
- [ ] 10-01: examples/01-cli — CLI using config + logz
- [ ] 10-02: examples/02-service — gRPC service using lifecycle + grpckit + health
- [ ] 10-03: examples/03-client — HTTP client demo with call (retries + circuit breaking)

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8 → 9 → 10

Note: Phases 2, 3, 4 can execute in parallel (all depend only on Phase 1). Phase 5 depends on 2+3. Phase 6 depends on 3. Phases 7 and 9 depend only on Phase 1. Phase 8 depends on 3+7. Phase 10 depends on all.

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Project Setup | 0/2 | Not started | - |
| 2. Config | 0/3 | Not started | - |
| 3. Logging | 0/3 | Not started | - |
| 4. Lifecycle | 0/3 | Not started | - |
| 5. Test Kit | 0/2 | Not started | - |
| 6. HTTP Kit | 0/3 | Not started | - |
| 7. Health | 0/4 | Not started | - |
| 8. gRPC Kit | 0/4 | Not started | - |
| 9. Call | 0/5 | Not started | - |
| 10. Examples | 0/3 | Not started | - |
