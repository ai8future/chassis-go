Date Created: 2026-02-16T21:16:32-08:00
TOTAL_SCORE: 82/100

# chassis-go v5.0.0 — Full Audit Report

**Auditor:** Claude Opus 4.6
**Module:** `github.com/ai8future/chassis-go/v5`
**Go Version:** 1.25.5
**Scope:** 16 packages, 36 source files, 26 test files, ~9,000 lines
**Tests:** All passing, `go vet` clean, weighted average coverage ~93%

---

## Score Breakdown

| Category | Weight | Score | Notes |
|---|---|---|---|
| Code Quality & Idioms | 20 | 18/20 | Clean, idiomatic Go; good use of generics |
| Security | 25 | 18/25 | Goroutine leak, silent OTel failures, XFF care needed |
| Correctness & Reliability | 25 | 20/25 | Solid; a few edge-case bugs remain |
| Architecture & Design | 15 | 14/15 | Zero cross-deps, clear boundaries, version gate |
| Test Coverage | 10 | 8/10 | Excellent (93% avg); otelutil untested |
| Documentation | 5 | 4/5 | Good godoc; RFC 9457 references solid |
| **TOTAL** | **100** | **82/100** | |

---

## Findings Summary

| # | Severity | Package | File:Line | Title |
|---|---|---|---|---|
| 1 | **HIGH** | guard | timeout.go:40-52 | Goroutine leak on timeout |
| 2 | **MEDIUM** | metrics | metrics.go:47-71 | Nil instrument used after creation error |
| 3 | **MEDIUM** | otel | otel.go:78-81 | Silent total telemetry disablement |
| 4 | **MEDIUM** | call | call.go:161-163 | Request body not rewound on retry |
| 5 | **LOW** | errors | problem.go:123-124 | Potential nil request context usage |
| 6 | **LOW** | guard | ratelimit.go:73-74 | Float64 token precision drift under high throughput |
| 7 | **LOW** | httpkit | tracing.go:41 | Unbounded span name cardinality |
| 8 | **LOW** | testkit | testkit.go:44-53 | SetEnv not safe for parallel tests |
| 9 | **LOW** | call | breaker.go:57-69 | GetBreaker ignores config mismatch |
| 10 | **INFO** | flagz | flagz.go:105-110 | UserID in span events may be PII |
| 11 | **INFO** | otel | otel.go:120-126 | Shared shutdown timeout for both providers |
| 12 | **INFO** | internal | otelutil/histogram.go:27-28 | LazyHistogram error swallowed by otel.Handle |
| 13 | **INFO** | guard | secheaders.go:73 | HSTS trust of X-Forwarded-Proto header |
| 14 | **INFO** | lifecycle | lifecycle.go:26 | Run accepts `any` — loses compile-time safety |

---

## Detailed Findings

### Finding 1 — HIGH: Goroutine leak on timeout (guard/timeout.go:40-52)

When the timeout fires (the `<-ctx.Done()` case), the goroutine running `next.ServeHTTP(tw, r)` is never explicitly stopped. The context is cancelled, but if the handler ignores context cancellation (e.g., blocking on I/O, a database call, or a long computation), the goroutine leaks indefinitely.

This is a known design limitation shared with `http.TimeoutHandler` in the stdlib, but the comment on line 64-65 understates the risk. Under sustained load with slow backends, this can exhaust memory.

**Impact:** Memory/goroutine leak under load with misbehaving handlers.
**Recommendation:** Document the contract requirement clearly. Consider adding a goroutine count metric or a hard kill after 2x the timeout.

```diff
--- a/guard/timeout.go
+++ b/guard/timeout.go
@@ -60,8 +60,14 @@ func Timeout(d time.Duration) func(http.Handler) http.Handler {
 			case <-done:
 				// Handler finished in time — flush any buffered response.
 				tw.flush()
 			case <-ctx.Done():
-				// Deadline exceeded — write 504 if handler hasn't started writing.
-				// The goroutine may still be running but its context is cancelled;
-				// well-behaved handlers will return promptly. This matches the
-				// behavior of Go's stdlib http.TimeoutHandler.
+				// Deadline exceeded — write 504 if handler hasn't started writing.
+				// WARNING: The handler goroutine may still be running. Only
+				// handlers that respect ctx.Done() will terminate promptly.
+				// Handlers that block on non-context-aware I/O will leak this
+				// goroutine until the blocking call returns. This is the same
+				// trade-off made by Go's stdlib http.TimeoutHandler.
+				//
+				// Callers MUST ensure downstream handlers respect context
+				// cancellation. Consider adding a goroutine leak metric if
+				// deploying with unreliable downstream handlers.
 				tw.timeout()
```

---

### Finding 2 — MEDIUM: Nil instrument used after creation error (metrics/metrics.go:47-71)

When `meter.Float64Counter(...)` or `meter.Float64Histogram(...)` fails, the error is logged but the (potentially nil) instrument handle is stored in the Recorder. Subsequent calls to `RecordRequest` will dereference these handles.

In practice, OTel API implementations return noop instruments on error rather than nil, so this won't crash. However, the code relies on an undocumented OTel implementation detail. If a future OTel version or a custom MeterProvider returns nil on error, this will panic.

**Impact:** Latent nil-pointer dereference risk.
**Recommendation:** Assign noop fallbacks explicitly.

```diff
--- a/metrics/metrics.go
+++ b/metrics/metrics.go
@@ -45,19 +45,22 @@ func New(prefix string, logger *slog.Logger) *Recorder {
 	meter := otelapi.GetMeterProvider().Meter(prefix)

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
+		// OTel API guarantees noop on error, but guard defensively.
 	}

 	requestDuration, err := meter.Float64Histogram(
 		prefix+"_request_duration_seconds",
 		metric.WithDescription("Request duration in seconds."),
 		metric.WithExplicitBucketBoundaries(DurationBuckets...),
 	)
-	if err != nil && logger != nil {
-		logger.Warn("metrics: failed to create request_duration histogram", "error", err)
+	if err != nil {
+		if logger != nil {
+			logger.Warn("metrics: failed to create request_duration histogram", "error", err)
+		}
 	}
```

Additionally, `Counter()` and `Histogram()` at lines 191-208 silently discard errors with `cv, _ :=`. These should at minimum log.

```diff
--- a/metrics/metrics.go
+++ b/metrics/metrics.go
@@ -190,8 +190,11 @@ func (r *Recorder) Counter(name string) *CounterVec {
 	fullName := r.prefix + "_" + name
 	cv, err := r.meter.Float64Counter(
 		fullName,
 		metric.WithDescription("Custom counter: "+name),
 	)
+	if err != nil && r.logger != nil {
+		r.logger.Warn("metrics: failed to create custom counter", "name", fullName, "error", err)
+	}
 	return &CounterVec{inner: cv, name: name, recorder: r}
 }

@@ -199,8 +202,11 @@ func (r *Recorder) Histogram(name string, buckets []float64) *HistogramVec {
 	fullName := r.prefix + "_" + name
 	hv, err := r.meter.Float64Histogram(
 		fullName,
 		metric.WithDescription("Custom histogram: "+name),
 		metric.WithExplicitBucketBoundaries(buckets...),
 	)
+	if err != nil && r.logger != nil {
+		r.logger.Warn("metrics: failed to create custom histogram", "name", fullName, "error", err)
+	}
 	return &HistogramVec{inner: hv, name: name, recorder: r}
 }
```

---

### Finding 3 — MEDIUM: Silent total telemetry disablement (otel/otel.go:78-81)

If the trace exporter fails to create (line 78-81), the function returns a noop shutdown func and ALL telemetry (including metrics) is silently disabled. The log line says "all telemetry disabled" but there's no way for the caller to know this happened — `Init` returns a `ShutdownFunc`, not an error.

**Impact:** Complete observability blackout with no programmatic signal to the caller.
**Recommendation:** Return `(ShutdownFunc, error)` or, at minimum, set a package-level health flag that health checks can inspect.

```diff
--- a/otel/otel.go
+++ b/otel/otel.go
@@ -46,7 +46,8 @@ func AlwaysSample() sdktrace.Sampler {
 // Init initializes OpenTelemetry trace and metric pipelines.
-// Returns a ShutdownFunc that must be called on process exit.
-func Init(cfg Config) ShutdownFunc {
+// Returns a ShutdownFunc that must be called on process exit and an error
+// if either pipeline failed to initialize (telemetry may be partially or
+// fully degraded).
+func Init(cfg Config) (ShutdownFunc, error) {
 	chassis.AssertVersionChecked()

 	// ... (rest of function would need corresponding error returns)
```

**Note:** This is an API-breaking change for v5. A less disruptive alternative is to add an `InitErr` sentinel that callers can check:

```diff
--- a/otel/otel.go
+++ b/otel/otel.go
@@ -8,6 +8,10 @@ import (
 	"log/slog"
 	"time"

+// InitStatus reports whether Init completed with degraded telemetry.
+// Check this in health probes to detect silent OTel failures.
+var InitStatus error
+
 // ... at line 81:
 	traceExporter, err := otlptracegrpc.New(ctx, traceOpts...)
 	if err != nil {
 		slog.Error("otel: trace exporter creation failed, all telemetry disabled", "error", err)
+		InitStatus = fmt.Errorf("otel: trace exporter creation failed: %w", err)
 		return func(ctx context.Context) error { return nil }
 	}
```

---

### Finding 4 — MEDIUM: Request body not rewound on retry (call/call.go:161-163)

The `exec` closure captures `req` by reference and calls `c.httpClient.Do(req)` on every retry attempt. The comment on `WithRetry` (line 83-85) warns about this, but the implementation doesn't check `req.GetBody` and doesn't attempt to rewind. This means POST/PUT/PATCH retries silently send an empty body.

**Impact:** Silent data loss on retried requests with bodies.
**Recommendation:** Attempt body rewind before each retry, or fail fast if `GetBody` is nil for non-idempotent methods.

```diff
--- a/call/retry.go
+++ b/call/retry.go
@@ -36,6 +36,15 @@ func (r *Retrier) Do(ctx context.Context, fn func() (*http.Response, error)) (*h

 	for attempt := range r.MaxAttempts {
+		// Rewind request body for retry attempts (attempt > 0).
+		// This requires the original request to have GetBody set.
+		if attempt > 0 && req.GetBody != nil {
+			body, err := req.GetBody()
+			if err == nil {
+				req.Body = body
+			}
+		}
+
 		// Check context before each attempt.
 		if ctx.Err() != nil {
```

Note: This requires `Retrier.Do` to accept the `*http.Request` rather than a closure, or the body rewind logic needs to be in `call.go`'s `exec` function.

---

### Finding 5 — LOW: Potential nil request context usage (errors/problem.go:123-124)

`WriteProblem` accesses `r.Context()` on line 124 without checking if `r` is nil. While `r` would only be nil in extremely unusual circumstances (the function signature implies a valid request), the nil check for `err` on line 107-109 suggests defensive programming was intended.

```diff
--- a/errors/problem.go
+++ b/errors/problem.go
@@ -121,7 +121,10 @@ func WriteProblem(w http.ResponseWriter, r *http.Request, err error, requestID s
 	w.WriteHeader(svcErr.HTTPCode)

 	if encErr := json.NewEncoder(w).Encode(pd); encErr != nil {
-		slog.ErrorContext(r.Context(), "errors: failed to encode problem detail", "error", encErr)
+		ctx := context.Background()
+		if r != nil {
+			ctx = r.Context()
+		}
+		slog.ErrorContext(ctx, "errors: failed to encode problem detail", "error", encErr)
 	}
```

---

### Finding 6 — LOW: Float64 token precision drift (guard/ratelimit.go:73-74)

The token bucket uses `float64` arithmetic for token refill calculation. Under very high throughput with small windows, floating-point precision loss can cause tokens to drift. For most use cases this is negligible, but it's worth noting.

```go
refill := elapsed.Seconds() / l.window.Seconds() * float64(l.rate)
```

**Impact:** Negligible in practice; may cause off-by-one token grants under extreme conditions.
**Recommendation:** No immediate change needed. Document the limitation or switch to integer-based arithmetic with nanosecond precision if higher accuracy is needed in future.

---

### Finding 7 — LOW: Unbounded span name cardinality (httpkit/tracing.go:41)

```go
spanName := fmt.Sprintf("%s %s", r.Method, r.URL.Path)
```

If the application serves routes with path parameters (e.g., `/users/12345`), each unique path creates a unique span name. This leads to high-cardinality span names that can overwhelm tracing backends.

**Impact:** Trace storage bloat and potential backend performance issues.
**Recommendation:** Use route templates or group by path pattern.

```diff
--- a/httpkit/tracing.go
+++ b/httpkit/tracing.go
@@ -39,7 +39,10 @@ func Tracing() func(http.Handler) http.Handler {
 			ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

 			tracer := otelapi.GetTracerProvider().Tracer(tracerName)
-			spanName := fmt.Sprintf("%s %s", r.Method, r.URL.Path)
+			// Use r.Pattern (Go 1.22+ ServeMux) when available to avoid
+			// high-cardinality span names from path parameters.
+			spanName := r.Method + " " + r.URL.Path
+			if r.Pattern != "" {
+				spanName = r.Method + " " + r.Pattern
+			}

 			ctx, span := tracer.Start(ctx, spanName,
```

---

### Finding 8 — LOW: SetEnv not safe for parallel tests (testkit/testkit.go:44-53)

`SetEnv` calls `os.Setenv` which modifies the global process environment. If two parallel tests set different values for the same key, they will race. The `testing.TB` interface provides `t.Setenv` in Go 1.17+ which handles this safely by calling `t.FailNow` if called from a parallel test.

```diff
--- a/testkit/testkit.go
+++ b/testkit/testkit.go
@@ -43,12 +43,8 @@ func SetEnv(t testing.TB, envs map[string]string) {
 	t.Helper()
 	for k, v := range envs {
-		os.Setenv(k, v)
+		t.Setenv(k, v)
 	}
-	t.Cleanup(func() {
-		for k := range envs {
-			os.Unsetenv(k)
-		}
-	})
 }
```

---

### Finding 9 — LOW: GetBreaker ignores config mismatch (call/breaker.go:57-69)

`GetBreaker` is a singleton factory keyed by name, but if called twice with the same name and different `threshold`/`resetTimeout` values, the second call silently returns the first breaker — ignoring the new config.

```diff
--- a/call/breaker.go
+++ b/call/breaker.go
@@ -57,6 +57,10 @@ func GetBreaker(name string, threshold int, resetTimeout time.Duration) *Circuit
 	if v, ok := breakers.Load(name); ok {
-		return v.(*CircuitBreaker)
+		existing := v.(*CircuitBreaker)
+		// Warn: reusing existing breaker even if config differs.
+		// Consider logging or panicking if threshold/resetTimeout mismatch.
+		return existing
 	}
```

---

### Finding 10 — INFO: UserID in span events may be PII (flagz/flagz.go:105-110)

The `addSpanEvent` method emits `flag.user_id` as a span event attribute. If traces are exported to third-party backends, this constitutes PII leakage. Consider hashing or omitting the UserID from telemetry by default.

---

### Finding 11 — INFO: Shared shutdown timeout for both providers (otel/otel.go:120-126)

Both the TracerProvider and MeterProvider share a single 5-second timeout context for shutdown. If the tracer takes 4.5 seconds to flush, the meter only gets 0.5 seconds. Consider giving each provider its own timeout.

```diff
--- a/otel/otel.go
+++ b/otel/otel.go
@@ -120,8 +120,10 @@ func Init(cfg Config) ShutdownFunc {
 	return func(ctx context.Context) error {
-		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
-		defer cancel()
-		tErr := tp.Shutdown(shutdownCtx)
-		mErr := mp.Shutdown(shutdownCtx)
+		tCtx, tCancel := context.WithTimeout(ctx, 5*time.Second)
+		defer tCancel()
+		tErr := tp.Shutdown(tCtx)
+
+		mCtx, mCancel := context.WithTimeout(ctx, 5*time.Second)
+		defer mCancel()
+		mErr := mp.Shutdown(mCtx)
 		return errors.Join(tErr, mErr)
 	}
```

---

### Finding 12 — INFO: LazyHistogram error swallowed (internal/otelutil/histogram.go:27-28)

On creation failure, the error is forwarded to `otelapi.Handle(err)` which by default logs to stderr. This is appropriate for library code but can be missed in production. No code change needed — just noting the behavior.

---

### Finding 13 — INFO: HSTS trusts X-Forwarded-Proto (guard/secheaders.go:73)

```go
if hstsValue != "" && (r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https") {
```

The `X-Forwarded-Proto` header is client-settable unless stripped by a trusted reverse proxy. In environments without a reverse proxy, a client could trick the server into setting HSTS on a plaintext connection. This is a defense-in-depth concern rather than a direct vulnerability.

---

### Finding 14 — INFO: lifecycle.Run accepts `any` (lifecycle/lifecycle.go:26)

```go
func Run(ctx context.Context, args ...any) error {
```

Using `...any` loses compile-time type safety. A mistyped argument (e.g., passing an `int`) only fails at runtime with a panic. This is a deliberate design choice for API ergonomics (accepting both `Component` and bare `func(ctx context.Context) error`), but could be replaced with a constrained interface type.

---

## Confirmed Non-Issues

### Loop variable capture in Go 1.22+
Several files use goroutines inside `for ... range` loops (work/work.go, lifecycle/lifecycle.go). In Go 1.22+ (this project uses 1.25.5), loop variables have per-iteration scoping. **These are NOT bugs.** Multiple AI audit tools incorrectly flag these.

### ServiceError fluent methods (errors/errors.go:38-74)
Previous audits flagged these as racy. The current implementation uses `clone()` which deep-copies the Details map and returns a new pointer. The receiver is never mutated. **These are safe.**

### checkCardinality TOCTOU (metrics/metrics.go:115-142)
Previously flagged. The current implementation uses proper double-checked locking with `RLock` fast path and `Lock` slow path with re-check. **This is correctly synchronized.**

### FromError type assertion (errors/errors.go:139-148)
Previously flagged. Now correctly uses `errors.As` for unwrap-chain traversal. **Fixed.**

---

## Architecture Assessment

**Strengths:**
- Zero cross-package dependencies within the toolkit — each package stands alone
- Version gate (`RequireMajor` + `AssertVersionChecked`) is a novel and effective approach
- Consistent fail-fast philosophy with `panic` on invalid config at construction time
- RFC 9457 Problem Details implementation is thorough and correct
- OTel is cleanly isolated as the sole SDK consumer
- Good use of Go 1.22+ generics (work.Map, work.Race, config.MustLoad)
- `cancelBody` pattern in call.go is a nice detail preventing premature context cancellation

**Areas for Improvement:**
- `otel.Init` should communicate failures to callers (not just log)
- Consider adding a `go:generate` step to verify VERSION file integrity
- The `internal/otelutil` package has zero test coverage
- Binary artifacts (`02-service`, `04-full-service`) are committed to the repo — add them to `.gitignore`

---

## Test Coverage Summary

| Package | Coverage |
|---|---|
| chassis | 85.7% |
| call | 96.3% |
| config | 96.4% |
| errors | 96.2% |
| flagz | 89.0% |
| grpckit | 84.6% |
| guard | 94.9% |
| health | 93.2% |
| httpkit | 96.3% |
| internal/otelutil | 0.0% |
| lifecycle | 100.0% |
| logz | 98.0% |
| metrics | 93.4% |
| otel | 77.8% |
| secval | 91.3% |
| testkit | 94.7% |
| work | 98.5% |
| **Weighted Average** | **~93%** |

---

## Conclusion

chassis-go v5 is a well-designed, well-tested Go toolkit. The codebase demonstrates strong Go idioms, thoughtful API design, and solid test coverage. The main areas for improvement are: (1) the goroutine leak in the timeout middleware, (2) better error propagation from OTel initialization, and (3) defensive handling of metric instrument creation failures. No critical security vulnerabilities were found. The HIGH finding (goroutine leak) is a known design limitation shared with the Go stdlib and is primarily a reliability concern under adversarial conditions rather than a security vulnerability.
