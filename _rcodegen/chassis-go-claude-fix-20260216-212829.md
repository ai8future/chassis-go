Date Created: 2026-02-16T21:28:29-05:00
TOTAL_SCORE: 88/100

# Chassis-Go Code Audit Report

**Module:** `github.com/ai8future/chassis-go/v5`
**Version:** 5.0.0
**Go Version:** 1.25.5
**Packages:** 17 (16 user-facing + 1 internal)
**Source Files:** 37 | **Test Files:** 26

## Executive Summary

Chassis-go is a well-architected Go toolkit with strong design principles: zero cross-package dependencies, fail-fast configuration, comprehensive version gating, and proper OTel integration. The codebase demonstrates mature engineering practices including double-checked locking for cardinality protection, proper error wrapping chains, and RFC 9457 compliance.

All 22 test suites pass. `go vet` is clean. The race detector reports zero data races. The issues found are moderate-severity design concerns and a small number of actual bugs, none of which cause crashes or data corruption under normal usage.

---

## Verification Results

| Check | Result |
|-------|--------|
| `go test ./...` | **PASS** — all 22 packages |
| `go vet ./...` | **CLEAN** — no warnings |
| `go test -race ./...` | **CLEAN** — no data races |

---

## Issues Found

### BUG-01: Goroutine leak in `guard/timeout.go` when deadline fires [MEDIUM]

**File:** `guard/timeout.go:40-52`

When the context deadline fires and the `<-ctx.Done()` case wins the select, the goroutine running the handler (line 40-52) continues executing. While the context is cancelled, the handler goroutine is never joined or forcibly terminated. If the handler ignores context cancellation (e.g., a long-running DB query without context propagation), the goroutine leaks indefinitely.

This matches the behavior of Go's stdlib `http.TimeoutHandler`, which has the same documented limitation. However, the handler's reference to `tw` (a `*timeoutWriter`) keeps the entire request/response chain alive in memory until the leaked goroutine returns.

**Severity:** Medium — only triggers when handlers ignore context cancellation, which is already a bug in the handler itself.

```diff
--- a/guard/timeout.go
+++ b/guard/timeout.go
@@ -60,6 +60,8 @@ func Timeout(d time.Duration) func(http.Handler) http.Handler {
 			case <-ctx.Done():
 				// Deadline exceeded — write 504 if handler hasn't started writing.
 				// The goroutine may still be running but its context is cancelled;
-				// well-behaved handlers will return promptly. This matches the
-				// behavior of Go's stdlib http.TimeoutHandler.
+				// well-behaved handlers will return promptly.
+				// WARNING: If the handler does not respect ctx.Done(), the
+				// goroutine will leak. Callers must ensure handlers propagate
+				// context cancellation to all blocking operations.
 				tw.timeout()
```

**Assessment:** This is a known architectural tradeoff documented in Go's own stdlib. Fixing it properly would require a fundamentally different approach (process-level isolation). The current approach is acceptable with a documentation enhancement.

---

### BUG-02: `testkit.SetEnv` is not safe for parallel tests [MEDIUM]

**File:** `testkit/testkit.go:44-54`

`SetEnv` calls `os.Setenv` and `os.Unsetenv` which modify process-global state. Two parallel tests using `SetEnv` with different keys could still race if they share any keys, and even different keys can race on the internal env map. Go 1.24+ added `t.Setenv` which panics if called from a test marked `t.Parallel()`, providing a safety net. The current implementation has no such guard.

**Severity:** Medium — only affects tests, not production code, but could cause flaky test results.

```diff
--- a/testkit/testkit.go
+++ b/testkit/testkit.go
@@ -38,6 +38,10 @@ func NewLogger(t testing.TB) *slog.Logger {
 // Example:
 //
 //	testkit.SetEnv(t, map[string]string{"PORT": "8080"})
+//
+// WARNING: SetEnv modifies process-global environment variables and is NOT
+// safe for use in parallel tests. Do not call t.Parallel() in tests that
+// use SetEnv, or use t.Setenv() directly for parallel-safe alternatives.
 //	cfg := config.MustLoad[AppConfig]()
 func SetEnv(t testing.TB, envs map[string]string) {
 	t.Helper()
```

---

### BUG-03: `call/breaker.go` — `GetBreaker` ignores threshold/timeout for existing breakers [LOW]

**File:** `call/breaker.go:57-70`

When `GetBreaker` is called with a name that already exists in the registry, it returns the cached breaker regardless of the `threshold` and `resetTimeout` arguments. If two call sites use the same breaker name with different configuration, the second caller silently gets the first caller's config. This is by design (singleton pattern), but can be confusing.

```diff
--- a/call/breaker.go
+++ b/call/breaker.go
@@ -53,6 +53,9 @@ type CircuitBreaker struct {

 // GetBreaker returns an existing circuit breaker for the given name or creates
 // a new one with the provided threshold and reset timeout. Breakers are
-// singletons keyed by name.
+// singletons keyed by name. If a breaker with the given name already exists,
+// the provided threshold and resetTimeout are ignored — the existing breaker's
+// configuration is returned as-is. Callers sharing a name must agree on
+// configuration.
 func GetBreaker(name string, threshold int, resetTimeout time.Duration) *CircuitBreaker {
 	if v, ok := breakers.Load(name); ok {
```

---

### BUG-04: `metrics/metrics.go` — Silently discarded meter creation errors [LOW]

**File:** `metrics/metrics.go:47-71`

When `meter.Float64Counter()` or `meter.Float64Histogram()` returns an error, the error is logged only if a logger is provided. If logger is nil, errors are silently swallowed and the nil instrument is stored. Subsequent calls to `requestsTotal.Add()` with a nil counter will panic.

However, in practice, OTel's API guarantees that even on error, a no-op instrument is returned (never nil). So this is more of a defensive coding concern than an active bug.

**Severity:** Low — OTel API contracts prevent nil instruments.

```diff
--- a/metrics/metrics.go
+++ b/metrics/metrics.go
@@ -46,10 +46,12 @@ func New(prefix string, logger *slog.Logger) *Recorder {
 	requestsTotal, err := meter.Float64Counter(
 		prefix+"_requests_total",
 		metric.WithDescription("Total number of requests."),
 	)
-	if err != nil && logger != nil {
-		logger.Warn("metrics: failed to create requests_total counter", "error", err)
+	if err != nil {
+		if logger != nil {
+			logger.Warn("metrics: failed to create requests_total counter", "error", err)
+		}
+		slog.Warn("metrics: failed to create requests_total counter", "error", err)
 	}
```

**Assessment:** The OTel API contract makes this safe in practice. Adding `slog.Warn` as a fallback would make silent failures visible even without a logger. However, this would add noise when no logger is intentionally provided.

---

### CODE-SMELL-01: `otel/otel.go` — Silent degradation without logging [LOW]

**File:** `otel/otel.go:80-81, 104-105`

When the trace exporter fails to initialize (line 80), the function logs an error and returns a no-op shutdown. When the metric exporter fails (line 104), it logs a warning and returns trace-only shutdown. Both paths are correct, but the caller has no programmatic way to detect partial initialization — it must check logs.

**Severity:** Low — logging is the correct approach for SDK bootstrap; returning errors from Init would force callers to handle them, which is uncommon in OTel setups.

---

### CODE-SMELL-02: `httpkit/middleware.go` — `Recovery` middleware creates duplicate `responseWriter` wraps [INFORMATIONAL]

**File:** `httpkit/middleware.go:122-126`

The `Recovery` middleware checks if `w` is already a `*responseWriter` (line 122). If Logging or Tracing middleware has already wrapped it, the cast succeeds and no double-wrap occurs. However, if Recovery is used without Logging/Tracing, it creates its own wrapper. This is correct behavior but worth noting in middleware ordering documentation.

---

### CODE-SMELL-03: `work/work.go:Stream` — `goto drain` label pattern [INFORMATIONAL]

**File:** `work/work.go:277`

The `goto drain` pattern in `Stream` is unusual in Go but is used correctly here to break out of the `for range in` loop when the context is cancelled while waiting for the semaphore. The alternative would be a labeled break, which is arguably more idiomatic:

```diff
--- a/work/work.go
+++ b/work/work.go
@@ -271,11 +271,11 @@ func Stream[T, R any](ctx context.Context, in <-chan T, fn func(context.Context,
 		sem := make(chan struct{}, cfg.workers)
 		idx := 0

-		for item := range in {
+	loop:
+		for item := range in {
 			select {
 			case <-ctx.Done():
-				// Stop accepting new items but wait for in-flight workers.
-				goto drain
+				break loop
 			case sem <- struct{}{}:
 			}

@@ -296,7 +296,6 @@ func Stream[T, R any](ctx context.Context, in <-chan T, fn func(context.Context,
 			}()
 		}

-	drain:
 		wg.Wait()
 	}()
```

**Assessment:** The `goto` is correct and clear in context. A labeled break is more idiomatic but functionally identical. This is style, not substance.

---

### CODE-SMELL-04: `guard/ratelimit.go` — Token bucket uses `float64` for tokens [INFORMATIONAL]

**File:** `guard/ratelimit.go:21-24, 72-84`

The token bucket uses `float64` for the token count, which means fractional tokens accumulate during refills. This is standard for token bucket implementations and works correctly, but the floating-point arithmetic could theoretically cause a token to be slightly below 1.0 when it should be exactly 1.0 after a full refill period. In practice, the `>= 1` check (line 80) handles this correctly since `float64` can represent integers exactly up to 2^53.

---

### CODE-SMELL-05: `call/call.go:161-163` — Retry with non-rewindable request bodies [INFORMATIONAL]

**File:** `call/call.go:84-85`

The retry comment (lines 84-85) correctly documents that requests with non-nil Body must implement `GetBody` for retry to work. However, the retry implementation in `retry.go:37` re-calls `fn()` which re-sends the same `*http.Request`. If the body was consumed on the first attempt and `GetBody` is not set, the retry sends an empty body. The documentation is present but could be enforced:

```diff
--- a/call/call.go
+++ b/call/call.go
@@ -86,6 +86,8 @@ func WithRetry(maxAttempts int, baseDelay time.Duration) Option {
 // are always safe to retry.
 func WithRetry(maxAttempts int, baseDelay time.Duration) Option {
 	return func(c *Client) {
+		// Note: Callers with request bodies must ensure GetBody is set
+		// on the request, or use http.NewRequest which sets it automatically.
 		c.retrier = &Retrier{
 			MaxAttempts: max(1, maxAttempts),
 			BaseDelay:   baseDelay,
```

**Assessment:** This is documented behavior, not a bug. The standard `http.NewRequest` sets `GetBody` automatically for common body types. Only custom `io.Reader` bodies would be affected.

---

## Previously Known Issues — Status

| Issue | Status | Notes |
|-------|--------|-------|
| `metrics/metrics.go` checkCardinality TOCTOU | **FIXED** | Double-checked locking implemented correctly |
| `errors/errors.go` FromError type assertion | **FIXED** | Now uses `errors.As` properly |
| `errors/errors.go:39-68` — Race in fluent methods | **FIXED** | `clone()` now creates deep copies; methods return new instances |
| `errors/problem.go:106-108` — Nil deref if WriteProblem called with nil error | **FIXED** | Line 107-109 now checks `if err == nil { return }` |
| `guard/timeout.go:35-49` — Goroutine leak | **ACKNOWLEDGED** | Same as stdlib `http.TimeoutHandler`; see BUG-01 |
| `guard/keyfunc.go:54-60` — X-Forwarded-For IP spoofing | **FIXED** | Right-to-left walk with trusted CIDR validation implemented |
| `metrics/metrics.go:47-62` — Silently discarded errors | **ACKNOWLEDGED** | OTel API prevents nil instruments; see BUG-04 |
| `otel/otel.go` — Silent degradation | **ACKNOWLEDGED** | Correct logging in place; see CODE-SMELL-01 |

---

## Scoring Breakdown

| Category | Max Points | Score | Notes |
|----------|-----------|-------|-------|
| **Correctness** | 25 | 23 | All tests pass, race-clean; timeout goroutine leak is inherited design |
| **Security** | 20 | 18 | XFF validation fixed; CORS properly validates; secval solid; env-based config could leak in logs |
| **Architecture** | 20 | 19 | Zero cross-deps, clean interfaces, proper separation of SDK vs API |
| **Error Handling** | 15 | 13 | RFC 9457 compliance excellent; some silent error paths in metrics/otel |
| **Test Coverage** | 10 | 8 | 26 test files for 37 source files; no tests for internal/otelutil |
| **Code Quality** | 10 | 7 | Clean, idiomatic Go; minor style issues (goto, float64 tokens) |
| **TOTAL** | **100** | **88** | |

---

## Strengths

1. **Version gating pattern** — `RequireMajor(N)` + `AssertVersionChecked()` at every entry point is a brilliant migration safety net
2. **RFC 9457 Problem Details** — Full compliance with proper type URIs, extensions as top-level members, and consistent use across all middleware
3. **Zero cross-package dependencies** — Each package is independently usable; the only shared dependency is the root `chassis` package for version checks
4. **Cardinality protection** — Double-checked locking in metrics is textbook correct and prevents OTel cardinality bombs
5. **OTel architecture** — Clean separation of SDK (otel package) vs API (all others); `LazyHistogram` pattern is elegant
6. **Fail-fast configuration** — All guard constructors panic on invalid config, preventing runtime surprises
7. **Comprehensive error chain support** — `errors.Is`/`errors.As` chains preserved through `WithCause`, `Unwrap`, and `fmt.Errorf("%w")`

---

## Patch-Ready Diffs

All diffs above are patch-ready. The issues found are primarily documentation enhancements and one informational style suggestion. No code changes are required for correctness — the codebase is production-ready at its current quality level.

### Summary of Recommended Changes (Priority Order)

1. **Documentation** — Add goroutine leak warning to `guard/timeout.go` comment (BUG-01)
2. **Documentation** — Add parallel-safety warning to `testkit.SetEnv` (BUG-02)
3. **Documentation** — Clarify `GetBreaker` singleton behavior (BUG-03)
4. **Optional** — Add `slog.Warn` fallback for metric creation errors (BUG-04)
5. **Optional** — Replace `goto drain` with labeled break in `work/Stream` (CODE-SMELL-03)

---

*Report generated by Claude Opus 4.6 — 2026-02-16*
