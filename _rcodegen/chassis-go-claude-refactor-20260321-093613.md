Date Created: 2026-03-21 09:36:13 UTC
TOTAL_SCORE: 82/100

# Chassis-Go Refactoring & Code Quality Report

**Agent**: Claude Code (Claude:Opus 4.6)
**Codebase**: chassis-go v9.0.2 — 23 packages, ~47 source files, ~36 test files, ~15,000 LOC
**Module**: `github.com/ai8future/chassis-go/v9` (Go 1.25.5)

---

## Executive Summary

Chassis-go is a well-architected Go toolkit with strong patterns, excellent test coverage, and disciplined zero-cross-dependency design. The codebase demonstrates mature engineering with consistent use of functional options, fail-fast panics for configuration, and comprehensive OTel integration. Deductions are for moderate code duplication (response writers), silent failure paths in observability setup, and a few long functions that could benefit from extraction.

---

## Scoring Breakdown

| Category | Weight | Score | Notes |
|----------|--------|-------|-------|
| Code Duplication | 20 | 15/20 | Response writer duplication, minor ID generation duplication |
| Code Quality | 20 | 16/20 | One 158-line function, some silent error paths |
| Maintainability | 20 | 17/20 | Excellent package isolation; minor coupling concerns |
| API Consistency | 15 | 14/15 | Functional options used uniformly; naming is deliberate |
| Test Coverage | 15 | 13/15 | All 23 packages have tests; most exceed 100% LOC ratio |
| Architecture | 10 | 7/10 | Strong separation; timeout goroutine leak risk; silent OTel degradation |
| **TOTAL** | **100** | **82/100** | |

---

## 1. Duplication Issues

### 1.1 Response Writer Wrappers (HIGH — est. 50 duplicate lines)

**Locations**: `guard/timeout.go:74-149` and `httpkit/middleware.go:60-95`

Both implement nearly identical http.ResponseWriter wrappers that:
- Track whether headers have been written
- Capture the status code
- Implement `WriteHeader()`, `Write()`, and `Unwrap()`

The `timeoutWriter` adds buffering for timeout scenarios, and `responseWriter` adds byte counting for metrics. Despite their differences, the core scaffolding (status tracking, WriteHeader guard, Unwrap for ResponseController) is duplicated.

**Recommendation**: Extract a shared base `internal/httputil/responsewriter.go` that both can embed, reducing ~30-40 lines of redundant boilerplate.

### 1.2 Guard Middleware Config Validation (MODERATE — est. 40 duplicate lines)

**Locations**: `guard/cors.go:25-36`, `guard/ratelimit.go:100-113`, `guard/ipfilter.go:23-26`, `guard/maxbody.go:12-15`, `guard/timeout.go:19-22`

Each guard constructor independently validates its config struct with the same pattern:
```go
if cfg.SomeField == zero {
    panic("guard: SomeField must be ...")
}
```

While each validates different fields, the boilerplate structure is identical across 5+ constructors.

**Recommendation**: Consider a small `guard/validate.go` with helpers like `mustPositive(name string, val int)` and `mustNotEmpty(name string, val string)` to reduce repetition.

### 1.3 ID Generation (LOW — 2 instances)

**Locations**: `httpkit/middleware.go` (generateID) and `webhook/webhook.go` (generateID)

Both use `crypto/rand` to generate random hex IDs with similar fallback logic. Minimal duplication (~15 lines each) but could share an `internal/randid` utility.

### 1.4 Error Response Indirection (LOW)

**Location**: `guard/problem.go` (13 lines)

`writeProblem()` is a thin wrapper around `errors.WriteProblem()`. All guard middleware could call `errors.WriteProblem()` directly, eliminating this indirection layer. However, the wrapper does provide a single point of change if the guard package ever needs to customize error responses.

---

## 2. Code Quality Issues

### 2.1 Long Functions

| Function | File | Lines | Concern |
|----------|------|-------|---------|
| `Do()` | `call/call.go:135-292` | 158 | Orchestrates circuit breaker, retry, token injection, OTel spans, metrics |
| `Map()` | `work/work.go:69-136` | 68 | Semaphore + WaitGroup + OTel per-item spans |
| `Handle()` | `logz/logz.go:88-119` | 50 | slog group reconstruction with trace injection |

The `Do()` method in `call.go` is the primary concern. It handles 8 distinct responsibilities in sequence. While the code is well-commented and flows linearly, extracting the circuit breaker state recording (lines 220-250) into a `recordBreakerOutcome()` helper would improve readability.

`Map()` and `Handle()` are at the edge but justified by their coordination requirements.

### 2.2 Silent Failure Paths (MODERATE)

**`metrics/metrics.go:47-71`**: Meter creation errors (Counter, Histogram) are logged at Warn level but never propagated. Callers have no way to know their metrics are being silently dropped.

**`otel/otel.go:78-81`**: When the trace exporter fails to initialize, the function returns a no-op `ShutdownFunc` without error. The caller believes telemetry is active when it isn't.

**Recommendation**: At minimum, return a `Warnings []error` field or log at Error level. For `otel.Init()`, consider returning a structured result that indicates which subsystems initialized successfully.

### 2.3 Duplicate Type Assertion in call.Do()

**Location**: `call/call.go:220-246`

The circuit breaker state is checked twice with the identical type assertion:
```go
if s, ok := c.breaker.(stater); ok { ... }
// ... several lines later ...
if s, ok := c.breaker.(stater); ok { ... }  // Same check
```

This appears to be an artifact of incremental development. The second check is redundant if the first succeeded.

### 2.4 Magic Numbers (MINOR)

- `flagz/flagz.go:91`: Hardcoded `100` for percentage bucketing — should be `const maxPercentBucket = 100`
- `otel/otel.go`: Error message strings like `"metric exporter creation failed"` appear in slightly different forms across trace/metric paths

### 2.5 Regex Recompilation (MINOR)

**Location**: `config/config.go` — `validate:"pattern=..."` tags cause regex compilation at validation time. If `MustLoad` is called once at startup (typical), this is not a performance concern. But if ever called in a hot path, patterns should be compiled once at package init.

---

## 3. Maintainability Assessment

### 3.1 Strengths

- **Zero cross-package dependencies**: Each package is independently usable. No import cycles. The dependency graph flows cleanly downward.
- **Consistent patterns**: Functional options, version gating (`AssertVersionChecked()`), fail-fast panics for config — applied uniformly across all 23 packages.
- **Well-scoped interfaces**: `flagz.Source` (1 method), `call.Breaker` (2 methods), `health.Check` (function type) — all appropriately minimal.
- **Comprehensive test suite**: All packages have tests. Most exceed 1:1 test-to-source LOC ratio. Table-driven tests, concurrency tests, and mock helpers are used appropriately.

### 3.2 Concerns

**Timeout middleware goroutine leak** (`guard/timeout.go:40-52`): When a timeout fires, the handler goroutine may continue executing indefinitely. The response is discarded via `timeoutWriter`, but the goroutine consumes resources until it naturally completes. This is a known Go pattern limitation but should be documented.

**Registry's file-based discovery** (`registry/`): Uses `/tmp/chassis` for service registration. No pluggable backend interface exists. Adding an in-memory backend for testing or a network-based backend for production would require refactoring.

**Cardinality limit not configurable** (`metrics/metrics.go:24`): `MaxLabelCombinations = 1000` is a package-level constant. High-cardinality services may need different limits per metric. A `Recorder.SetMaxCombinations()` method would add flexibility without breaking the API.

---

## 4. API Design Assessment

### 4.1 Constructor Pattern Consistency: EXCELLENT

All packages use one of two deliberate patterns:
- `New(opts ...Option)` — standard constructor with functional options
- `MustLoad[T]()` — generic config loader that panics on failure

Naming is purposeful and self-documenting. No inconsistencies detected.

### 4.2 Error Type Design: STRONG

`errors.ServiceError` supports dual HTTP/gRPC status codes with fluent builders. Factory functions (`ValidationError`, `NotFoundError`, etc.) cover common cases. `FromError()` correctly uses `errors.As()`. RFC 9457 Problem Details support via `WriteProblem()`.

### 4.3 OTel Integration: WELL-ARCHITECTED

Only `otel/otel.go` imports the OTel SDK. All other packages use API-only imports (`otelapi`). The `internal/otelutil.LazyHistogram` pattern prevents double-registration. Span naming is consistent (`work.Map`, `http.client.request.duration`, etc.).

---

## 5. Test Coverage Assessment

| Coverage Tier | Packages | Test:Source Ratio |
|---------------|----------|-------------------|
| Exceptional (>150%) | lifecycle (273%), logz (169%), health (165%), config (162%), errors (149%), httpkit (145%) | 6 packages |
| Strong (100-150%) | registry (130%), work (137%), tick (123%), deploy (115%), xyops (106%), flagz (105%) | 6 packages |
| Good (70-100%) | call (96%), grpckit (72%), metrics (83%), xyopsworker (84%), cache (78%), webhook (77%) | 6 packages |
| Adequate (50-70%) | seal (55%) | 1 package |

All 23 packages have test files. No package is untested. The `seal` package (crypto operations) has the lowest ratio but still covers core encrypt/decrypt/sign/verify paths.

---

## 6. Architectural Observations

### 6.1 Package Dependency Graph (simplified)

```
lifecycle → registry, health
httpkit → errors, registry (for tracing)
grpckit → errors (for interceptors)
call → errors (for circuit breaker errors)
guard → errors (for problem responses)
otel → (OTel SDK only)
All others → chassis root (version gating only)
```

This is a clean, shallow dependency tree. No circular dependencies. Each package can be extracted independently.

### 6.2 Concurrency Safety

- `metrics.checkCardinality()`: Proper double-checked locking with read lock fast path
- `call.CircuitBreaker`: Mutex-protected state machine with probe gating
- `work.Map/All/Race/Stream`: Semaphore-bounded with proper WaitGroup coordination
- `cache.Cache`: Mutex-protected with TTL/LRU eviction

All concurrency patterns are correct for Go 1.22+ (per-iteration loop variable scoping).

---

## 7. Prioritized Recommendations

| # | Priority | Issue | Impact | Effort |
|---|----------|-------|--------|--------|
| 1 | HIGH | Extract shared ResponseWriter base to `internal/httputil/` | Reduces ~50 lines of duplication, single source of truth for response wrapping | Low |
| 2 | HIGH | Surface OTel/metrics init failures to callers | Prevents silent observability gaps in production | Medium |
| 3 | MEDIUM | Extract `recordBreakerOutcome()` from `call.Do()` | Reduces 158-line function; removes duplicate type assertion | Low |
| 4 | MEDIUM | Add guard config validation helpers | Reduces ~40 lines of boilerplate across 5+ constructors | Low |
| 5 | MEDIUM | Document timeout goroutine behavior | Prevents confusion about resource consumption after timeouts | Low |
| 6 | LOW | Make `MaxLabelCombinations` configurable per Recorder | Supports high-cardinality use cases without code changes | Low |
| 7 | LOW | Extract shared ID generation to `internal/randid/` | Minor dedup; 2 nearly-identical implementations | Low |
| 8 | LOW | Define const for flagz percentage bucket (100) | Eliminates magic number | Trivial |

---

## 8. What's Done Well (Preserving Strengths)

These patterns should be maintained and not refactored away:

- **Fail-fast panic on invalid config**: Appropriate for a toolkit — catches misconfiguration at startup, not at 3 AM
- **Functional options everywhere**: Consistent, extensible, backward-compatible API pattern
- **Version gating via `RequireMajor()`**: Prevents silent major-version mismatches in large codebases
- **OTel API-only imports**: Only `otel/otel.go` touches the SDK; all other packages degrade gracefully with no-op providers
- **Zero cross-package dependencies**: Each package is independently testable and importable
- **Comprehensive test coverage**: No package left behind; table-driven tests and concurrency tests used appropriately

---

*Report generated by Claude Code (Claude:Opus 4.6) on 2026-03-21*
