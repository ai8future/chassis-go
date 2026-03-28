Date Created: 2026-03-27 12:24:35 UTC
TOTAL_SCORE: 74/100

# Chassis-Go Refactoring Assessment

**Agent:** Claude:Opus 4.6
**Scope:** Code quality, duplication, and maintainability review across ~55 source files and ~15 new/untracked packages (~16,700 lines total)

---

## Score Breakdown

| Category | Score | Max | Notes |
|----------|-------|-----|-------|
| Code Organization | 14 | 18 | Clean package boundaries; some oversized files |
| Duplication | 10 | 18 | HTTP helpers, CIDR parsing, test setup, panic recovery all repeated |
| Consistency | 12 | 16 | Guard validation gaps, mixed test patterns, assertion style drift |
| Test Quality | 12 | 16 | Good coverage breadth but helpers underused, timing deps |
| API Design | 14 | 16 | Clear option patterns, good composition; minor constructor inconsistency |
| Maintainability | 12 | 16 | Clear structure but some large files and tight coupling |
| **Total** | **74** | **100** | |

---

## 1. Cross-Package Duplication (Highest Impact)

### 1.1 HTTP Client Helper Triplication

`setHeaders()` and `checkStatus()` are copy-pasted across three kit packages with near-identical implementations:

- `graphkit/graphkit.go` — sets X-Tenant-ID, X-Trace-ID, Authorization; checks HTTP status
- `lakekit/lakekit.go` — identical helper pair
- `registrykit/registrykit.go` — identical helper pair

**Impact:** ~60 lines duplicated 3x. Any change to header propagation or error mapping must be made in three places.

**Recommendation:** Extract to `internal/httputil` or a shared `kitutil` package:
```go
func SetStandardHeaders(req *http.Request, tenant, traceID, token string)
func CheckStatus(resp *http.Response) error
```

### 1.2 Client Constructor Pattern (5 instances)

Identical `NewClient` + `ClientOption` functional-options boilerplate in:
- `call/call.go`
- `graphkit/graphkit.go`
- `lakekit/lakekit.go`
- `registrykit/registrykit.go`
- `xyops/xyops.go`

Each creates an `http.Client` with a default timeout, applies options, and stores `baseURL`. The structure is byte-for-byte identical aside from field names and default timeout values.

**Impact:** ~25 lines x 5 = ~125 lines of structural duplication. Low bug risk but high maintenance cost when evolving the client pattern.

### 1.3 Panic Recovery Pattern (5 locations)

Nearly identical `defer func() { if r := recover(); r != nil { ... } }()` with stack capture and structured logging in:
- `httpkit/middleware.go` (HTTP recovery middleware)
- `grpckit/interceptors.go` (UnaryRecovery, StreamRecovery)
- `logz/logz.go` (trace handler)

Each varies slightly in log fields and response writing, but the core recovery + stack capture + structured log is the same.

**Recommendation:** Extract `internal/recoverutil.Capture(logger, func())` that handles defer/recover/stack/log, with a callback for protocol-specific error response.

### 1.4 CIDR Parsing Duplication

`parseCIDRs()` in `guard/ipfilter.go` and inline CIDR parsing in `guard/keyfunc.go` (XForwardedFor) implement the same validation loop with nearly identical panic messages.

**Recommendation:** Single `guard.parseCIDRs()` function shared by both.

### 1.5 OTel Tracer/Meter Boilerplate (~8 packages)

Each OTel-instrumented package independently calls:
```go
tracer := otelapi.GetTracerProvider().Tracer("github.com/ai8future/chassis-go/v10/pkg")
```
Some use `internal/otelutil.LazyHistogram()`, others don't. Inconsistent instrumentation setup.

---

## 2. Guard Package Consistency Gaps

The guard package contains 6 middleware constructors. Five follow the panic-on-bad-config pattern consistently. One does not:

| Middleware | Validation Checks | Panics on Bad Config |
|---|---|---|
| CORS | 2 (AllowOrigins, Credentials+wildcard) | Yes |
| IPFilter | 2 (Allow/Deny presence, valid CIDRs) | Yes |
| MaxBody | 1 (maxBytes > 0) | Yes |
| RateLimit | 4 (Rate, Window, KeyFunc, MaxKeys) | Yes |
| SecurityHeaders | **0** | **No** |
| Timeout | 1 (duration > 0) | Yes |

**SecurityHeaders performs zero validation.** It accepts empty config silently and only conditionally writes headers at runtime. This is inconsistent with the stated architecture principle of "all constructors panic on invalid config (fail-fast)."

### Panic Message Formatting

Messages are inconsistently formatted across the guard package:
```
"guard: CORSConfig.AllowOrigins must not be empty"    // Qualified with type
"guard: maxBytes must be > 0"                          // Unqualified
"guard: Timeout duration must be > 0"                  // Partially qualified
"guard: RateLimitConfig.KeyFunc must not be nil"       // Qualified with type
```

**Recommendation:** Standardize to `"guard: TypeName.FieldName constraint"` pattern.

### Constructor Signature Inconsistency

- `MaxBody(int64)` and `Timeout(time.Duration)` take scalar parameters
- `CORS(CORSConfig)`, `RateLimit(RateLimitConfig)`, etc. take config structs

Scalar constructors are simpler but break the extensibility pattern. If MaxBody ever needs a custom error message or Timeout needs a fallback handler, they'll need breaking API changes.

---

## 3. Test Quality Issues

### 3.1 Underused testkit Package

`testkit` provides `NewLogger()`, `NewHTTPServer()`, `Respond()`, `GetFreePort()`, and `SetEnv()` but most test files don't use them:

| Utility | Used In | Should Also Be Used In |
|---|---|---|
| `testkit.NewLogger()` | testkit_test.go | grpckit, metrics (both duplicate logger creation) |
| `testkit.NewHTTPServer()` | testkit_test.go | registrykit, graphkit, webhook, lakekit (all use raw httptest) |
| `testkit.GetFreePort()` | testkit, grpckit/health | lifecycle, httpkit |
| `testkit.SetEnv()` | testkit_test.go | deploy, flagz (both use t.Setenv directly) |

### 3.2 RequireMajor() Inconsistency

Three patterns in use across test files:

1. **TestMain pattern** (correct): config, testkit, grpckit, flagz, deploy, health, metrics, lifecycle
2. **init() pattern** (works but less ideal): webhook, cache, seal, tracekit, registrykit, graphkit
3. **Missing entirely**: errors, secval

The `init()` approach is less debuggable and can't be controlled per-test. Missing calls in errors and secval mean those packages skip the version gate.

### 3.3 Time-Dependent Tests (Flaky Risk)

- `cache/cache_test.go`: `time.Sleep(60ms)` for TTL expiry testing
- `health/health_test.go`: Elapsed time assertions (`elapsed > limit`)
- `lifecycle/lifecycle_test.go`: `time.Sleep(50ms)` before signal delivery
- `graphkit/graphkit_test.go`: `time.Sleep(200ms)` in test handler

These pass locally but are flaky candidates under CI load.

### 3.4 Duplicated Test Helpers

- `grpckit_test.go` has `newTestLogger()` — should use `testkit.NewLogger()`
- `metrics_test.go` has `setupTestMeter()` — should be in testkit for reuse by call, grpckit, tracekit
- `lifecycle_test.go` has `readLogEvents()` — reusable JSONL log parser that belongs in testkit
- `grpckit_test.go` has `mockServerStream` — should be in testkit for gRPC test reuse

---

## 4. Tight Coupling: registry.AssertActive()

`httpkit/middleware.go` and `grpckit/interceptors.go` call `registry.AssertActive()`, creating a hard dependency on the registry lifecycle for all HTTP/gRPC middleware users. This means:

- You cannot use httpkit in a context where registry is not initialized (e.g., migration scripts, CLI tools, lightweight services)
- Testing middleware requires registry setup or mocking

**Recommendation:** Move registry assertion to a separate middleware that services opt into, rather than embedding it in the core httpkit/grpckit middleware chain.

---

## 5. File Size Hotspots

Files over 300 lines that may benefit from splitting:

| File | Lines | Concern |
|---|---|---|
| `registry/registry.go` | 792 | Module-level state registry + heartbeat + command polling |
| `registrykit/registrykit.go` | 512 | Entity resolution + graph operations + mutations |
| `deploy/deploy.go` | 471 | Directory discovery + runtime detection + TLS + env loading + hooks |
| `graphkit/graphkit.go` | 384 | Full graph query client |
| `grpckit/interceptors.go` | 323 | Logging + recovery + tracing interceptors |
| `work/work.go` | 322 | Map + All + Race + Stream (4 distinct patterns) |
| `call/call.go` | 315 | Resilient HTTP client with retry + circuit breaker |
| `xyops/xyops.go` | 303 | Full xyops API client + monitoring bridge |

`registry.go` (792 lines) and `deploy.go` (471 lines) are the clearest candidates for decomposition:
- registry: split heartbeat/command polling into `registry/poller.go`
- deploy: split TLS discovery, env loading, and hook execution into separate files

---

## 6. New Package Quality Assessment

The 15 untracked packages are generally high quality:

**Excellent:**
- `seal/` — Production-grade crypto (AES-256-GCM, HMAC-SHA256, constant-time comparison)
- `announcekit/` — Minimal, focused, well-tested (13 tests)
- `cache/` — Clean generic LRU with proper concurrency
- `tracekit/` — Small, focused trace ID propagation
- `tick/` — Clean periodic task runner with jitter support

**Good with caveats:**
- `kafkakit/` — Well-architected (780 lines across 5 files) but complex; 26 tests
- `xyops/` — Best example of composing other chassis modules; large (303 lines)
- `graphkit/`, `lakekit/`, `registrykit/` — Solid but duplicate helper functions (see 1.1)
- `schemakit/` — Proper Avro handling; calls `regexp.MustCompile()` on every parse (should cache)

**Needs work:**
- `xyopsworker/` — WebSocket implementation is stubbed out; incomplete package

**All new packages correctly use `v10` module path and `AssertVersionChecked()`.**

---

## 7. Minor Quality Issues

### Variable Naming
- `guard/keyfunc.go`: `ff` for X-Forwarded-For header, `n` for IPNet, `l` for limiter
- `guard/keyfunc.go`: `XForwardedFor()` is 46 lines with deep nesting — high cognitive load

### Silent Failures
- `guard/ipfilter.go:40-43`: Unparseable client IPs are silently treated as "access denied" with no logging
- `otel/otel.go`: Silently degrades when exporters fail to init (no logging)
- `metrics/metrics.go:47-62`: Meter creation errors silently discarded

### Config Performance
- `config/config.go`: `regexp.MustCompile()` called on every `Load()` for pattern validation — should cache compiled patterns

---

## 8. Strengths (What NOT to Refactor)

- **work/ package**: Excellent structured concurrency with proper OTel integration. Clean, DRY, well-designed. Leave it alone.
- **Toolkit principle**: No package owns `main()`. Zero cross-deps between sibling packages. This is executed well.
- **Option pattern**: Consistent `func(*Config)` pattern across all constructors. Repetitive but clear and idiomatic Go.
- **Fail-fast constructors**: `panic()` on bad config at init time is the right choice for a toolkit (except SecurityHeaders gap).
- **xyops composition**: Excellent example of reusing chassis modules (call, cache, tick, webhook) rather than reimplementing.
- **seal/ crypto**: Uses standard library crypto throughout. Correct primitives (scrypt, AES-GCM, HMAC-SHA256, constant-time comparison).

---

## Summary of Refactoring Priorities

| Priority | Issue | Estimated LOC Reduction | Effort |
|----------|-------|------------------------|--------|
| **P0** | Extract shared HTTP helpers (setHeaders/checkStatus) from 3 kit packages | ~120 lines | Low |
| **P0** | Add validation to SecurityHeaders (consistency fix) | +10 lines | Low |
| **P1** | Consolidate test helpers into testkit (logger, meter, log parser, mock stream) | ~80 lines | Low |
| **P1** | Standardize RequireMajor() to TestMain pattern; add to missing packages | ~20 lines | Low |
| **P1** | Extract shared CIDR parsing in guard package | ~30 lines | Low |
| **P2** | Extract panic recovery helper for HTTP/gRPC | ~60 lines | Medium |
| **P2** | Decouple registry.AssertActive() from httpkit/grpckit middleware | ~20 lines | Medium |
| **P2** | Split registry.go (792 lines) into focused files | 0 (restructure) | Medium |
| **P3** | Standardize guard panic message formatting | ~15 lines | Low |
| **P3** | Cache compiled regexps in config validation | ~10 lines | Low |
| **P3** | Replace time.Sleep in tests with event-driven assertions | ~30 lines | Medium |

**Total potential reduction:** ~385 duplicated lines across the codebase, plus improved consistency and testability.
