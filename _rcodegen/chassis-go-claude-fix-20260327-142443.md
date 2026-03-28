Date Created: 2026-03-27 14:24:43 UTC
TOTAL_SCORE: 88/100

# Chassis-Go Code Audit Report

**Agent:** Claude Code (Claude:Opus 4.6)
**Scope:** Full codebase analysis of `chassis-go` v10 — 39 packages, ~54 source files
**Go Version:** 1.25.5 (per-iteration loop scoping active since Go 1.22+)
**Module:** `github.com/ai8future/chassis-go/v10`

---

## Executive Summary

The chassis-go codebase is well-crafted with consistent patterns, proper concurrency primitives, and thoughtful security considerations. The architecture is clean — packages have zero cross-dependencies, the version gate pattern is solid, and the HTTP middleware composition is idiomatic. Most issues found are low severity. No critical security vulnerabilities were identified.

### Score Breakdown

| Category | Points | Deductions | Notes |
|----------|--------|------------|-------|
| Correctness | 22/25 | -3 | Shared shutdown timeout in otel, unchecked write in health handler |
| Security | 24/25 | -1 | HSTS applied on unvalidated X-Forwarded-Proto (minor) |
| Concurrency | 24/25 | -1 | Minor TOCTOU in breaker state observability |
| Code Quality | 18/25 | -7 | Silent error swallowing, untruncated panic logging, minor smells |
| **Total** | **88/100** | | |

---

## False Positives Dismissed

Before listing real issues, these commonly-flagged items are **NOT bugs** in this codebase:

### 1. Loop Variable Capture in Goroutines (FALSE POSITIVE)

Multiple automated tools flag closures capturing loop variables in `work.go`, `lifecycle.go`, and similar files. **This is safe in Go 1.22+**, which introduced per-iteration loop variable scoping. This project uses **Go 1.25.5** per `go.mod`. The following are all safe:

- `work/work.go:98` — `Map` goroutine captures `i` and `item`
- `work/work.go:166` — `All` goroutine captures `i` and `task`
- `work/work.go:233` — `Race` goroutine captures `i` and `task`
- `lifecycle/lifecycle.go:160` — component goroutine captures `c`

### 2. Rate Limiter "Burst Attack" (FALSE POSITIVE)

`guard/ratelimit.go:66` — New buckets initialized with `float64(l.rate)` tokens is standard token bucket behavior. A new client SHOULD receive their full rate allocation. The `MaxKeys` field with LRU eviction prevents unbounded memory growth. This is correct by design.

### 3. Lifecycle Shutdown Reason Logic (FALSE POSITIVE)

`lifecycle/lifecycle.go:184-186` — The condition `signalCtx.Err() != nil && ctx.Err() == nil` correctly distinguishes "shutdown due to OS signal" from "shutdown because caller cancelled their context." This is correct logic.

### 4. Guard Timeout Goroutine Leak (FALSE POSITIVE)

`guard/timeout.go:40-52` — The handler goroutine may outlive the timeout, but the context IS cancelled via `defer cancel()`. Well-behaved handlers will exit promptly. This matches Go's stdlib `http.TimeoutHandler` behavior and is explicitly documented in the code comments (lines 63-65).

### 5. Config Recursive Struct Loading (FALSE POSITIVE)

`config/config.go:43` — Cyclic struct references are not possible in Go without pointer indirection. The function only recurses into `reflect.Struct` kind fields, not pointer-to-struct. Go's type system prevents infinite recursion here.

---

## Real Issues Found

### Issue 1: Shared Timeout Context for Dual OTel Shutdown (MEDIUM)

**File:** `otel/otel.go:120-125`
**Severity:** Medium
**Type:** Correctness

The shutdown function creates a single 5-second timeout context and uses it for both trace provider and metric provider shutdown sequentially. If `tp.Shutdown()` takes close to 5 seconds, `mp.Shutdown()` gets almost no time, potentially losing metric data.

**Current code:**
```go
return func(ctx context.Context) error {
    shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()
    tErr := tp.Shutdown(shutdownCtx)
    mErr := mp.Shutdown(shutdownCtx)
    return errors.Join(tErr, mErr)
}
```

**Recommended fix:**
```diff
 return func(ctx context.Context) error {
-    shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
-    defer cancel()
-    tErr := tp.Shutdown(shutdownCtx)
-    mErr := mp.Shutdown(shutdownCtx)
+    traceCtx, traceCancel := context.WithTimeout(ctx, 5*time.Second)
+    defer traceCancel()
+    tErr := tp.Shutdown(traceCtx)
+
+    metricCtx, metricCancel := context.WithTimeout(ctx, 5*time.Second)
+    defer metricCancel()
+    mErr := mp.Shutdown(metricCtx)
+
     return errors.Join(tErr, mErr)
 }
```

---

### Issue 2: Untruncated Panic Values in gRPC Recovery Interceptors (LOW-MEDIUM)

**File:** `grpckit/interceptors.go:75-78` and `grpckit/interceptors.go:128-132`
**Severity:** Low-Medium
**Type:** Code Quality / Defensive Logging

Both `UnaryRecovery` and `StreamRecovery` log the raw panic value via `slog.Any("panic", r)`. If the panic value is a large struct or has circular references, this could cause excessive memory consumption during log serialization.

**Current code:**
```go
logger.LogAttrs(ctx, slog.LevelError, "panic recovered",
    slog.String("method", info.FullMethod),
    slog.Any("panic", r),
    slog.String("stack", string(debug.Stack())),
)
```

**Recommended fix:**
```diff
+panicMsg := fmt.Sprint(r)
+if len(panicMsg) > 1024 {
+    panicMsg = panicMsg[:1024] + "...(truncated)"
+}
 logger.LogAttrs(ctx, slog.LevelError, "panic recovered",
     slog.String("method", info.FullMethod),
-    slog.Any("panic", r),
+    slog.String("panic", panicMsg),
     slog.String("stack", string(debug.Stack())),
 )
```

Apply to both `UnaryRecovery` (line 75) and `StreamRecovery` (line 128).

---

### Issue 3: Silent Error Swallowing in Metrics Counter/Histogram Creation (LOW)

**File:** `metrics/metrics.go:192` and `metrics/metrics.go:202`
**Severity:** Low
**Type:** Observability Gap

The `Counter()` and `Histogram()` methods discard errors from `meter.Float64Counter()` and `meter.Float64Histogram()` using blank identifiers. While OTel implementations are required to return no-op instruments on error (so this won't crash), the error is never logged, making it invisible when metric registration fails.

**Current code:**
```go
func (r *Recorder) Counter(name string) *CounterVec {
    fullName := r.prefix + "_" + name
    cv, _ := r.meter.Float64Counter(
        fullName,
        metric.WithDescription("Custom counter: "+name),
    )
    return &CounterVec{inner: cv, name: name, recorder: r}
}
```

**Recommended fix:**
```diff
 func (r *Recorder) Counter(name string) *CounterVec {
     fullName := r.prefix + "_" + name
-    cv, _ := r.meter.Float64Counter(
+    cv, err := r.meter.Float64Counter(
         fullName,
         metric.WithDescription("Custom counter: "+name),
     )
+    if err != nil && r.logger != nil {
+        r.logger.Warn("metrics: failed to create counter", "name", fullName, "error", err)
+    }
     return &CounterVec{inner: cv, name: name, recorder: r}
 }
```

Apply same pattern to `Histogram()` at line 200.

---

### Issue 4: Unchecked Write Error in Health Handler (LOW)

**File:** `health/handler.go:46`
**Severity:** Low
**Type:** Code Quality

`w.Write(buf.Bytes())` return value is ignored. While unchecked writes are common in HTTP handlers (the connection may have been closed), this is inconsistent with the careful error handling of the JSON encoding on line 36.

**Current code:**
```go
w.Write(buf.Bytes())
```

**Recommended fix:**
```diff
-w.Write(buf.Bytes())
+if _, err := w.Write(buf.Bytes()); err != nil {
+    slog.ErrorContext(r.Context(), "health: response write failed", "error", err)
+}
```

---

### Issue 5: HSTS Header Applied Based on Unvalidated X-Forwarded-Proto (LOW)

**File:** `guard/secheaders.go:73`
**Severity:** Low
**Type:** Security (minimal impact)

The HSTS header is set when `r.Header.Get("X-Forwarded-Proto") == "https"`, but this header can be spoofed by clients in non-proxy setups. However, the practical impact is minimal since HSTS enforcement causes the browser to upgrade to HTTPS, which is a *more* secure outcome. The real concern is only when behind an HTTP-only service that should never send HSTS.

**Current code:**
```go
if hstsValue != "" && (r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https") {
```

**Recommended consideration:**
Document that `X-Forwarded-Proto` should only be trusted when behind a reverse proxy that strips/overwrites this header. No code change strictly required unless the middleware is used without a proxy.

---

### Issue 6: Minor TOCTOU in Circuit Breaker State Observability (LOW)

**File:** `call/call.go:223-245`
**Severity:** Low
**Type:** Observability accuracy

The code reads `s.State()` before `c.breaker.Record(success)` and again after to detect state transitions for OTel events. Between these calls, another goroutine could change the state, causing missed or incorrect transition events. This only affects observability data, not correctness of the circuit breaker itself.

**Current code:**
```go
var prevState State
hasPrev := false
if s, ok := c.breaker.(stater); ok {
    prevState = s.State()
    hasPrev = true
}

c.breaker.Record(success)

eventAttrs := []attribute.KeyValue{attribute.Bool("success", success)}
if hasPrev {
    if s, ok := c.breaker.(stater); ok {
        newState := s.State()
        if newState != prevState {
            // ...
```

**Recommended fix:**
The `CircuitBreaker.Record()` method could return the previous and new states to make this atomic, but this would change the `Breaker` interface. Given this only affects observability and the race window is tiny, this is acceptable as-is. Documenting the limitation is sufficient.

---

### Issue 7: Config Pattern Validation Compiles Regex on Each Call (LOW)

**File:** `config/config.go:191`
**Severity:** Low (startup only)
**Type:** Performance (negligible)

`regexp.MustCompile(value)` is called inside `validateField` for each field with a `pattern` validate tag. Since config loading happens once at startup, the performance impact is negligible. However, if the same pattern is used across multiple fields, it gets compiled multiple times.

**No change recommended** — startup-only cost is acceptable.

---

## Positive Observations

These deserve recognition as well-implemented patterns:

1. **Version Gate** (`chassis.go:25-53`) — The `RequireMajor`/`AssertVersionChecked` pattern is a clean way to prevent version mismatch at startup. Using `atomic.Bool` is correct for concurrent access.

2. **Cardinality Protection** (`metrics/metrics.go:115-142`) — The double-checked locking pattern with `sync.RWMutex` is correctly implemented, avoiding the TOCTOU that was previously present (per memory notes, this was already fixed).

3. **XForwardedFor with Trusted CIDRs** (`guard/keyfunc.go:37-82`) — Right-to-left walk of the XFF chain with trusted proxy validation is a security best practice. This correctly prevents the classic XFF spoofing attack.

4. **Timeout Writer** (`guard/timeout.go:72-160`) — The buffered response writer with mutex protection is well-designed. The `Unwrap()` method correctly supports `http.NewResponseController` for accessing `Flusher`/`Hijacker`.

5. **Circuit Breaker State Machine** (`call/breaker.go:74-134`) — Clean implementation with proper state transitions. The `stateProbing` internal state is correctly hidden from external consumers via the `State()` method.

6. **Cancel-on-Body-Close** (`call/call.go:30-42`) — The `cancelBody` wrapper that defers context cancellation until the response body is closed is a subtle but important correctness detail that prevents premature context cancellation.

7. **Token Bucket with LRU Eviction** (`guard/ratelimit.go:42-96`) — Bounded memory usage with `MaxKeys` and proper LRU eviction prevents the rate limiter from becoming a memory leak vector under attack.

8. **Health Check with work.Map** (`health/health.go:55-89`) — Parallel health check execution with deterministic ordering (sorted names) and proper error aggregation using `errors.Join`.

---

## Summary

| Severity | Count | Details |
|----------|-------|---------|
| Critical | 0 | — |
| Medium | 1 | Shared OTel shutdown timeout |
| Low-Medium | 1 | Untruncated panic logging in gRPC |
| Low | 5 | Silent errors, unchecked write, HSTS, TOCTOU, regex caching |
| False Positives Dismissed | 5 | Loop vars (Go 1.22+), rate limiter burst, shutdown logic, timeout goroutine, config recursion |

The codebase demonstrates strong engineering practices. The issues found are minor and none pose a risk of data loss, security breach, or service failure in production. The most actionable fix is the OTel shared timeout context (Issue 1).
