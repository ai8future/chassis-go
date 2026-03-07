Date Created: 2026-02-16T21:33:28-05:00
TOTAL_SCORE: 82/100

# Chassis-Go Refactor & Code Quality Report

**Module:** `github.com/ai8future/chassis-go/v5`
**Go Version:** 1.25.5
**Scope:** 16 packages, 36 source files, 26 test files (~8,957 lines total)
**Auditor:** Claude Opus 4.6

---

## Executive Summary

Chassis-go is a well-architected Go toolkit with strong fundamentals: zero circular dependencies, consistent fail-fast patterns, excellent OTel integration, and a clean "toolkit never owns main()" design. The codebase earns a solid **82/100**, with deductions primarily for a handful of security gaps, some code duplication, and a few areas where error handling silently degrades.

### Score Breakdown

| Category | Weight | Score | Weighted |
|----------|--------|-------|----------|
| Architecture & Design | 25% | 95/100 | 23.75 |
| Code Quality & Consistency | 20% | 85/100 | 17.00 |
| Security | 20% | 68/100 | 13.60 |
| Error Handling & Reliability | 15% | 75/100 | 11.25 |
| Test Quality | 10% | 90/100 | 9.00 |
| Maintainability & Duplication | 10% | 74/100 | 7.40 |
| **Total** | **100%** | | **82.00** |

---

## Architecture & Design (95/100)

### Strengths

- **Zero cross-package dependencies** — each package is independently importable
- **Version gate pattern** (`RequireMajor` / `AssertVersionChecked`) enforced at every public entry point
- **Clean dependency layering:** Core → Utilities → Infrastructure → Protocol → Flags
- **Fail-fast philosophy** — panics at startup for config/validation errors, never at runtime
- **Toolkit, not framework** — examples demonstrate perfect composition without magic

### Minor Deductions (-5)

- **No shutdown timeout guidance** — `lifecycle.Run` doesn't provide a way to bound graceful shutdown duration; examples use `srv.Shutdown(context.Background())` with no deadline
- **`internal/otelutil`** is the only internal package — the boundary between "internal" and "public" isn't fully leveraged

---

## Code Quality & Consistency (85/100)

### Strengths

- Consistent middleware signature: `func(http.Handler) http.Handler` across all guard middleware
- Consistent config struct pattern with `// REQUIRED` comments
- Idiomatic import grouping everywhere
- Good use of modern Go: generics (`config.MustLoad[T]`), range-over-int, `errors.Join`

### Issues (-15)

| Location | Issue | Severity |
|----------|-------|----------|
| `chassis.go:27,47` | Version parsing logic duplicated in `RequireMajor` and `AssertVersionChecked` | Low |
| `logz/logz.go:94-96` | Complex backward group reconstruction with no explaining comment | Low |
| `httpkit/middleware.go:51-54` | `RequestID` never checks for existing `X-Request-ID` header from upstream | Medium |
| `grpckit/interceptors.go:264,296` | `status.FromError` bool return ignored — non-gRPC errors report code 0 | Low |
| `grpckit/interceptors.go:142-150` | `grpcCodeFromError` helper exists but isn't used consistently | Low |
| `cmd/demo-shutdown/main.go:42-56` | Uses `fmt.Sprintf` for logging instead of structured attributes | Low |

---

## Security (68/100)

### Critical Issues (-20)

| Location | Issue |
|----------|-------|
| `guard/keyfunc.go:56-81` | **X-Forwarded-For IP spoofing** — validates that RemoteAddr is trusted, but attacker at a trusted proxy can inject arbitrary client IPs. Right-to-left parsing is correct, but the entire XFF chain is not validated against the trust list. This allows rate limit and IP filter bypass. |
| `secval/secval.go:66-72` | **Unicode normalization bypass** — dangerous key detection strips non-ASCII and lowercases, but doesn't use NFKC normalization. Homoglyph attacks or composed character sequences could bypass the dangerous keys list. Should use `golang.org/x/text/unicode/norm`. |

### Medium Issues (-8)

| Location | Issue |
|----------|-------|
| `guard/timeout.go:40-52,61-66` | **Goroutine leak on timeout** — when timeout fires, handler goroutine keeps running. No cancellation mechanism. Can accumulate under load with slow handlers. (Known issue.) |
| `guard/secheaders.go:73` | **X-Forwarded-Proto trusted without proxy validation** — any client can send `X-Forwarded-Proto: https` over HTTP to trigger HSTS header emission |
| `guard/cors.go:95-104` | Preflight OPTIONS accepted without validating `Access-Control-Request-Method` or `Access-Control-Request-Headers` |

### Low Issues (-4)

| Location | Issue |
|----------|-------|
| `guard/keyfunc.go:69-79` | Empty/whitespace XFF entries not validated after trim |
| `guard/keyfunc.go:14-20` | `remoteHost` returns full `addr:port` on parse failure; won't parse as valid IP |
| `errors/problem.go:58-63` | RFC 9457 extension key collisions silently dropped |

---

## Error Handling & Reliability (75/100)

### Silent Degradation Issues (-15)

| Location | Issue | Severity |
|----------|-------|----------|
| `metrics/metrics.go:47-62` | Meter creation errors silently discarded; later calls on nil metrics will panic | Medium |
| `metrics/metrics.go:192-207` | Custom `Counter`/`Histogram` creation ignores errors, returns potentially nil inner metric | Medium |
| `otel/otel.go:78-82` | Trace exporter failure disables ALL telemetry (including metrics), even though metrics could work independently | Medium |
| `otel/otel.go:66-69` | Resource creation failure loses service name/version attributes with only a Warn log | Low |
| `call/retry.go:39-43` | `io.Copy(io.Discard, resp.Body)` error ignored during body drain — potential resource leak | Medium |
| `health/handler.go:46` | `w.Write(buf.Bytes())` error unchecked | Low |

### Race Conditions (-5)

| Location | Issue | Severity |
|----------|-------|----------|
| `call/breaker.go:57-69` | TOCTOU in `GetBreaker` — two goroutines can create breakers with different configs for same name; first-write-wins is undocumented | High |
| `guard/timeout.go:143-144` | `tw.started` / `tw.written` checked under mutex but timeout/flush race is logically fragile | Medium |
| `errors/problem.go:106-108` | `WriteProblem` with nil `r` (request) will panic at `ProblemDetail(r)` despite nil `err` early return | Low |

### Positive

- Double-checked locking in `metrics/metrics.go:checkCardinality` is correctly implemented (previously fixed)
- `errors.FromError` correctly uses `errors.As` (previously fixed)
- `cancelBody` pattern in `call/call.go` properly manages HTTP response body lifecycle

---

## Test Quality (90/100)

### Strengths

- **Consistent `TestMain` pattern** with version gate across all 26 test files
- **Excellent table-driven tests** with subtests (`t.Run`)
- **Strong concurrency testing** — atomic counters for peak concurrency verification in `work` and `call`
- **Good edge case coverage** — empty inputs, zero values, boundary conditions, panic recovery
- **Proper test isolation** — `t.TempDir()`, `t.Setenv()`, `t.Cleanup()`, unique breaker names

### Gaps (-10)

| Area | Gap |
|------|-----|
| `secval` | No fuzz testing for JSON validation — strong candidate for `testing.F` |
| `call` | No test for batch partial failures or context cancellation mid-batch |
| `call` | No test for body cleanup when circuit breaker opens mid-request |
| `metrics` | No test for label key/value collision scenarios with special characters |
| Time-dependent tests | Several tests use hardcoded `50ms` sleeps — flaky in CI |
| `flagz` | No test for malformed percentage edge cases in `EnabledFor` |

---

## Maintainability & Duplication (74/100)

### Code Duplication (-16)

| Pattern | Locations | Recommendation |
|---------|-----------|----------------|
| **Version parsing** | `chassis.go:27` and `chassis.go:47` | Extract `func parseMajor(v string) (int, error)` helper |
| **Body drain-and-close** | `call/retry.go:41-42` and `call/retry.go:70-71` | Extract `func drainAndClose(resp *http.Response)` |
| **KeyFunc return validation** | `guard/ipfilter.go:39` and `guard/ratelimit.go:117` | Extract shared `validateKey(string) error` or document expectations |
| **responseWriter wrapping** | `httpkit/middleware.go:58-87` and `httpkit/tracing.go:53-54` | Double-wrapping when both middleware active; consider shared wrapper |
| **Panic message format** | All `guard/*.go` constructors | Minor: could use `guardPanic(fn, msg string)` helper for `"guard: %s: %s"` format |
| **OTel test setup** | `metrics_test.go`, `otel_test.go`, multiple httpkit/grpckit tests | `setupTestMeter()` pattern duplicated; could live in `testkit` |

### Structural Concerns (-10)

| Concern | Detail |
|---------|--------|
| `call/breaker.go:39-40` | `breakers` sync.Map never evicts entries — unbounded memory growth if dynamic names used |
| `guard/ratelimit.go:63-65` | Eviction loop is `for len >= maxKeys` instead of `if` — O(n²) under pathological key churn |
| `secval/secval.go:45` | `MaxNestingDepth = 20` hardcoded — not configurable |
| `guard/ratelimit.go:119` | `Retry-After: 1` is static; could compute actual wait from token bucket state |

---

## Refactoring Opportunities (Priority Ordered)

### High Priority (Security/Correctness)

1. **Fix XFF chain validation** (`guard/keyfunc.go:56-81`) — validate every hop in the chain, not just RemoteAddr. Reject requests where untrusted hops appear in the middle.

2. **Add NFKC normalization** (`secval/secval.go:66-72`) — use `golang.org/x/text/unicode/norm.NFKC` before dangerous key matching to prevent homoglyph bypass.

3. **Fix GetBreaker TOCTOU** (`call/breaker.go:57-69`) — use `LoadOrStore` directly without prior `Load` check, and log a warning if a second registration attempts different config.

4. **Handle nil metrics gracefully** (`metrics/metrics.go:47-62,192-207`) — either return noop wrappers on creation failure or check for nil before calling `Add`/`Record`.

### Medium Priority (Reliability)

5. **Add shutdown timeout** to `lifecycle.Run` or document the pattern — examples should show `context.WithTimeout` for `srv.Shutdown`.

6. **Separate trace/metric failure paths** (`otel/otel.go:78-82`) — don't bail on all telemetry when traces fail; continue setting up metrics.

7. **Check `io.Copy` error in retry drain** (`call/retry.go:39-43`) — log warning on drain failure.

8. **Extract version parsing helper** (`chassis.go`) — eliminate duplication between `RequireMajor` and `AssertVersionChecked`.

9. **Check incoming X-Request-ID** (`httpkit/middleware.go:51-54`) — propagate existing request ID from upstream before generating a new one.

### Low Priority (Maintainability)

10. **Extract `drainAndClose` helper** in `call/retry.go` — shared body cleanup function.

11. **Add breaker entry eviction** (`call/breaker.go`) — TTL or explicit `RemoveBreaker(name)` API.

12. **Fix ratelimit eviction loop** (`guard/ratelimit.go:63-65`) — use batch eviction or `if` instead of `for`.

13. **Add fuzz tests** for `secval.ValidateJSON` — strong candidate for Go's built-in fuzzing.

14. **Consolidate OTel test utilities** — move shared `setupTestMeter()` patterns into `testkit`.

15. **Add godoc for circuit breaker state machine** (`call/breaker.go`) — document Closed → Open → Probing → Closed/Open transitions.

---

## What's Working Well

These aspects of the codebase are exemplary and should be preserved:

- **Version gate system** — elegant, enforced everywhere, catches misuse at startup
- **Fail-fast constructors** — panics for invalid config prevent runtime surprises
- **Zero cross-package coupling** — any package can be imported independently
- **Fluent error API with clone safety** — `WithDetail`/`WithType` are thread-safe via cloning
- **OTel instrumentation depth** — traces, metrics, and log correlation throughout
- **Test discipline** — table-driven tests, subtests, proper isolation, meaningful assertions
- **RFC 9457 Problem Details** — proper content negotiation and extension handling
- **`work` package generics** — `Map`, `All`, `Race`, `Stream` are clean and reusable
- **Cardinality protection** in metrics with proper double-checked locking
- **`cancelBody` pattern** in `call/call.go` for HTTP response lifecycle management

---

## Conclusion

Chassis-go is a high-quality toolkit that gets the hard things right: concurrency safety, observability integration, graceful lifecycle management, and clean API design. The 18-point deduction comes from a few security gaps (XFF spoofing, Unicode normalization), silent error degradation in the metrics/OTel pipeline, and moderate code duplication that could be extracted into shared helpers. None of these issues are showstoppers — the toolkit is production-viable today — but addressing the high-priority items would push this into the 90+ range.
