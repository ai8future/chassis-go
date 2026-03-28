Date Created: 2026-03-27 14:28:08 UTC
TOTAL_SCORE: 79/100

# Chassis-Go Refactor Audit Report

**Agent:** Claude Code (Claude:Opus 4.6)
**Scope:** Code quality, duplication, and maintainability review across all packages
**Module:** `github.com/ai8future/chassis-go/v10` (Go 1.25.5)

---

## Score Breakdown

| Category | Score | Max | Notes |
|----------|-------|-----|-------|
| Architecture & Separation of Concerns | 17 | 20 | Excellent package isolation, zero cross-deps between packages |
| Code Duplication | 12 | 20 | Several significant duplication clusters identified |
| Naming & API Consistency | 13 | 15 | Minor inconsistencies in constructors and error handling philosophy |
| Test Quality & Organization | 14 | 15 | Strong coverage but helper duplication and underutilized testkit |
| Error Handling Consistency | 8 | 10 | Mixed panic vs. error-return philosophy across packages |
| Documentation & Discoverability | 8 | 10 | Good function-level docs; package-level docs sparse |
| Maintainability & Extensibility | 7 | 10 | Options pattern excellent; some global state hinders testability |

---

## 1. CODE DUPLICATION (Most Impactful Findings)

### 1a. DJB2 Hash Function — Exact Copy (HIGH)

**Locations:**
- `chassis/chassis.go` — `Port()` function (lines ~76-86)
- `registry/registry.go` — `djb2Port()` function (lines ~566-573)

Both implement the identical DJB2 hash algorithm with the same constants (`5381`, `*33`, `%43001`, `+5000`). This is a textbook extract-to-shared-function opportunity.

**Recommendation:** Extract to `chassis.hashName()` (unexported) and have both `Port()` and registry call it, or have registry import `chassis.Port()` directly.

---

### 1b. gRPC Interceptor Pairs — ~90% Duplication (HIGH)

**Location:** `grpckit/interceptors.go`

Four interceptor pairs (Unary + Stream variants) share nearly identical logic:

| Pair | Unary Lines | Stream Lines | Shared Logic |
|------|-------------|--------------|--------------|
| Logging | 36-60 | 89-113 | Attribute construction, time tracking, log call |
| Recovery | 64-85 | 117-138 | Panic catch, error formatting, status code mapping |
| Metrics | 157-185 | 189-213 | Histogram recording, attribute building |
| Tracing | 253-299 | 301-323 | Span creation, attribute setting, error handling |

The only structural difference is the function signature (`UnaryHandler` vs `StreamHandler`) and context extraction (`ctx` parameter vs `ss.Context()`).

**Recommendation:** Extract shared logic into internal helpers:
- `logRPC(ctx, logger, method, duration, err)` for logging
- `recordRPCMetrics(ctx, method, code, duration)` for metrics
- `recoverRPC(ctx, logger) (code, err)` for recovery

Each interceptor pair would become a thin wrapper calling the shared helper.

---

### 1c. Panic Recovery Logging — Inconsistent Duplication (MEDIUM)

**Locations:**
- `httpkit/middleware.go` (lines ~142-147): Uses `logger.Error("panic recovered", ...)` with `fmt.Sprint(err)` and `string(stack)`
- `grpckit/interceptors.go` (lines ~75-79): Uses `logger.LogAttrs(ctx, slog.LevelError, "panic recovered", ...)` with `slog.Any("panic", r)` and `slog.String("stack", ...)`

Same intent, different logging APIs and field names. A unified `logz.LogPanic(ctx, logger, recovered, stack)` helper would standardize panic telemetry.

---

### 1d. Test Helper Duplication (MEDIUM)

| Helper | Duplicated In | Times |
|--------|---------------|-------|
| `newTestLogger()` | `logz/logz_test.go`, `grpckit/grpckit_test.go` | 2x (testkit.NewLogger exists) |
| `initRegistryForTest()` | `httpkit/httpkit_test.go`, `grpckit/grpckit_test.go` | 2x (identical implementations) |
| `json.NewDecoder(rec.Body).Decode(&pd)` | httpkit, guard, errors tests | ~22x |
| `defer func() { recover() ... }()` panic assertion | config, guard, lifecycle tests | ~34x |

**Recommendation:**
- Consolidate logger setup to use `testkit.NewLogger()` everywhere
- Extract `testkit.InitRegistryForTest()`
- Add `testkit.DecodeJSON[T](body)` and `testkit.AssertPanics(t, fn, contains)` helpers

---

## 2. NAMING & API CONSISTENCY

### 2a. Constructor Return Type Inconsistency (LOW-MEDIUM)

The codebase uses three different constructor return patterns:

| Pattern | Examples | Returns |
|---------|----------|---------|
| Instance | `metrics.New()`, `flagz.New()`, `call.New()` | `*T` or `*T, error` |
| Factory function | `health.CheckFunc()`, `health.All()` | `func(...) -> checker` |
| Channel-wrapped function | `work.Stream()` | `func() <-chan T` |

This is not necessarily wrong — each serves its use case — but newcomers may find the API surface inconsistent. A brief comment on the design philosophy in a package doc would help.

### 2b. KeyFunc Creator Naming (LOW)

**Location:** `guard/keyfunc.go`

- `RemoteAddr()` — returns a `KeyFunc`
- `XForwardedFor()` — returns a `KeyFunc`
- `HeaderKey()` — returns a `KeyFunc`

The first two are noun-like (the thing being extracted), while `HeaderKey` is also noun-like but takes a parameter. Consider `RemoteAddrKey()` or `ByRemoteAddr()` for consistency, though this is cosmetic.

### 2c. Config Struct Naming (LOW)

Guard package config structs are well-named (`CORSConfig`, `IPFilterConfig`, `RateLimitConfig`, `SecurityHeadersConfig`), but `MaxBody()` and `Timeout()` take bare parameters instead of config structs. This is actually appropriate given their simplicity — not a real issue, just an observation.

---

## 3. ERROR HANDLING PHILOSOPHY

### 3a. Panic vs. Error Return — Undocumented Policy (MEDIUM)

The codebase has a clear but undocumented policy:

| Context | Strategy | Examples |
|---------|----------|---------|
| Configuration/setup errors | `panic()` | config.MustLoad, guard constructors, flagz.FromJSON |
| Runtime errors | `error` return | call.Do, seal.Encrypt, health checks |
| Version mismatch | `os.Exit(1)` | chassis.AssertVersionChecked |

This fail-fast philosophy is defensible (configuration errors should be caught at startup, not silently degraded at runtime). However, it's not documented anywhere.

**Recommendation:** Add a brief "Error Philosophy" section to INTEGRATING.md or a top-level doc explaining when consumers should expect panics vs. errors.

### 3b. Error Type Fragmentation (LOW)

- Guard/httpkit: Uses `errors.ServiceError` with RFC 9457 Problem Details
- secval: Uses module-local sentinel errors (`ErrDangerousKey`, `ErrNestingDepth`)
- registry: Uses `fmt.Errorf` wrapping
- call: Uses stdlib `errors` + gRPC codes

The secval isolation is intentional (zero cross-deps for security modules). The others could benefit from a documented error taxonomy.

---

## 4. GLOBAL STATE & TESTABILITY

### 4a. Package-Level Singletons (MEDIUM)

Several packages use package-level global state:

| Package | Global State | Mechanism |
|---------|-------------|-----------|
| `registry` | Single registration, mutex, atomic bools | `sync.Mutex` + `atomic.Bool` |
| `call/breaker` | Circuit breaker registry | `sync.Map` |
| `chassis` | Version assertion flag | `atomic.Bool` |
| `otelutil` | Lazy histogram cache | `sync.Once` per histogram |

**Impact on testing:** Both `httpkit_test.go` and `grpckit_test.go` need identical `initRegistryForTest()` setup functions because registry is global. The `registry.ResetForTest()` function exists but requires careful orchestration.

**Recommendation:** Not necessarily wrong for a toolkit (services are singletons), but document the "one service per process" assumption prominently. Consider whether `call/breaker` could accept a `BreakerRegistry` option for testability.

---

## 5. ARCHITECTURAL OBSERVATIONS

### 5a. Options Pattern — Excellent (STRENGTH)

Pervasive, consistent, idiomatic:
```go
// Used in: call, work, cache, tick, metrics, health, kafkakit, ...
type Option func(*config)
```

This is the strongest architectural pattern in the codebase. Every package that accepts configuration uses it correctly.

### 5b. Middleware Composition — Clean (STRENGTH)

Both HTTP and gRPC middleware follow idiomatic Go patterns:
- HTTP: `func(http.Handler) http.Handler`
- gRPC: `grpc.UnaryServerInterceptor` / `grpc.StreamServerInterceptor`

The guard package's `writeProblem()` unifies error responses across all middleware via RFC 9457.

### 5c. OTel Integration — Non-Intrusive (STRENGTH)

The `otelutil.LazyHistogram` pattern allows metrics to be recorded without requiring OTel initialization:
```go
var hist = otelutil.LazyHistogram(...)
if h := hist(); h != nil { h.Record(ctx, value) }
```

Graceful degradation when OTel is not configured. Used consistently in httpkit, grpckit, and call.

### 5d. Version Gate — Effective but Heavy (OBSERVATION)

`chassis.AssertVersionChecked()` is called at the entry of every public function across ~15+ packages. The `atomic.Bool` load is cheap but the pattern adds noise. Consider whether a single check at `lifecycle.Run()` would suffice, with the per-function checks reserved for library-mode usage.

---

## 6. TEST QUALITY

### 6a. Coverage Patterns

**Excellent coverage:** config, work, logz, httpkit, metrics, health — comprehensive edge cases including concurrency, cancellation, error paths.

**Good coverage:** lifecycle, guard/ratelimit, call — covers main flows and key edge cases.

**Minimal coverage:** guard/cors (7 tests, no complex origin matching edge cases), cache (no concurrent access tests).

### 6b. Testing Style

- **Subtests (`t.Run`)**: Low adoption. Only httpkit uses them for status code mapping.
- **Table-driven tests**: Moderate adoption. secval uses them well; config/guard tests are sequential.
- **`t.Parallel()`**: Almost never used. Could safely be added to most guard and utility tests.
- **Regression tests**: `logz_test.go` includes an explicit "BUG-2" regression test — good practice.

### 6c. Timing-Dependent Tests (FRAGILITY RISK)

`lifecycle_test.go` uses `time.Sleep(50ms)` for synchronization, which can be flaky on loaded CI systems. Consider using channels or `sync.WaitGroup` for deterministic synchronization.

---

## 7. MINOR OBSERVATIONS

- **responseWriter struct** in `httpkit/middleware.go` is well-implemented with `Unwrap()` for `http.ResponseController` compatibility
- **Timeout middleware** (`guard/timeout.go`) has a known goroutine leak when timeout fires (handler goroutine keeps running) — documented in project memory as a known issue
- **X-Forwarded-For** in `guard/keyfunc.go` does not validate the forwarded IP against trusted proxy lists — documented as known issue
- **OTel meter errors** silently discarded in `metrics/metrics.go` — pragmatic choice but worth a debug log

---

## 8. PRIORITIZED RECOMMENDATIONS

### Must-Do (High Impact, Low Effort)
1. **Extract DJB2 hash** — Eliminate exact duplication between chassis.go and registry.go
2. **Document error handling philosophy** — One paragraph in INTEGRATING.md

### Should-Do (High Impact, Medium Effort)
3. **Extract gRPC interceptor helpers** — Reduce ~200 lines of near-identical code to ~80
4. **Consolidate test helpers** — Move duplicated helpers into testkit package
5. **Add `logz.LogPanic()` helper** — Standardize panic recovery logging

### Nice-to-Have (Medium Impact)
6. **Add `t.Parallel()` to independent tests** — Faster CI runs
7. **Replace `time.Sleep` in lifecycle tests** — Reduce flakiness
8. **Add package-level documentation** — Help new contributors navigate

---

## Summary

Chassis-go is a well-architected Go toolkit with strong patterns (options, middleware composition, OTel integration) and good test coverage. The primary maintainability concerns are:

1. **Code duplication** in gRPC interceptors and the DJB2 hash function
2. **Test helper duplication** across packages despite a good testkit foundation
3. **Undocumented error handling philosophy** (panic vs. return)

The 79/100 score reflects a production-quality codebase with real but addressable technical debt. The duplication issues are the main drag — fixing them would push the score into the mid-80s. The architecture itself is sound, the package boundaries are clean, and the API design is largely consistent.
