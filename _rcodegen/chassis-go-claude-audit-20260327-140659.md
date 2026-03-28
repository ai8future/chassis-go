Date Created: 2026-03-27T14:06:59-04:00
TOTAL_SCORE: 68/100

# Chassis-Go Comprehensive Audit Report

**Auditor:** Claude Code (Claude:Opus 4.6)
**Module:** `github.com/ai8future/chassis-go/v10` (v10.0.8)
**Go Version:** 1.25.5
**Packages Audited:** config, lifecycle, logz, flagz, work, health, httpkit, grpckit, guard, call, metrics, otel, secval, registry, testkit, chassis.go
**Note:** Go 1.22+ per-iteration loop variable scoping applies — loop closure captures are NOT bugs in this codebase.

---

## Score Breakdown

| Category | Max | Score | Notes |
|----------|-----|-------|-------|
| Security | 30 | 20 | Path traversal (REG-1), HSTS spoofing, unbounded body drain, goroutine leak |
| Correctness | 25 | 17 | Nil panic in cancelBody, shared shutdown ctx, Stream span orphaning |
| Code Quality | 20 | 14 | Silent error discarding, mutable package-level vars, API gaps |
| Test Coverage | 15 | 10 | Missing cross-package integration tests, race-prone test patterns |
| Dependencies | 10 | 7 | Large gRPC surface area, x/crypto needs govulncheck |
| **TOTAL** | **100** | **68** | |

---

## HIGH Severity Issues

### HIGH-1: Registry `CHASSIS_SERVICE_NAME` Path Traversal (SECURITY)
**File:** `registry/registry.go:578-587, 203`

`resolveName()` reads `CHASSIS_SERVICE_NAME` from the environment and passes it unsanitized into `filepath.Join(BasePath, name)`. A value like `../../etc` resolves outside `BasePath`, allowing directory creation and PID/log file writes to arbitrary paths.

```diff
--- a/registry/registry.go
+++ b/registry/registry.go
@@ -576,7 +576,14 @@ func Port(name string) int {

 func resolveName() string {
 	if n := os.Getenv("CHASSIS_SERVICE_NAME"); n != "" {
-		return n
+		// Reject path separators and traversal to prevent writing outside BasePath.
+		if strings.ContainsAny(n, "/\\") || n == "." || n == ".." {
+			panic(fmt.Sprintf("registry: CHASSIS_SERVICE_NAME %q contains path separators or traversal", n))
+		}
+		if filepath.Base(n) != n {
+			panic(fmt.Sprintf("registry: CHASSIS_SERVICE_NAME %q resolves outside base path", n))
+		}
+		return n
 	}
 	wd, err := os.Getwd()
 	if err != nil {
```

---

### HIGH-2: OTel Shared Shutdown Context Starves Second Provider
**File:** `otel/otel.go:120-126`

A single 5-second `shutdownCtx` is shared sequentially by `tp.Shutdown` and `mp.Shutdown`. If the trace provider takes 4.9s, the metric provider gets 0.1s.

```diff
--- a/otel/otel.go
+++ b/otel/otel.go
@@ -118,9 +118,13 @@ func Init(opts ...Option) func(ctx context.Context) error {

 	return func(ctx context.Context) error {
-		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
-		defer cancel()
-		tErr := tp.Shutdown(shutdownCtx)
-		mErr := mp.Shutdown(shutdownCtx)
+		traceCtx, traceCancel := context.WithTimeout(ctx, 5*time.Second)
+		tErr := tp.Shutdown(traceCtx)
+		traceCancel()
+
+		metricCtx, metricCancel := context.WithTimeout(ctx, 5*time.Second)
+		mErr := mp.Shutdown(metricCtx)
+		metricCancel()
+
 		return errors.Join(tErr, mErr)
 	}
 }
```

---

### HIGH-3: `cancelBody.Close()` Panics on Nil Body (204 No Content)
**File:** `call/call.go:38-41, 287`

When a server returns 204 No Content, `resp.Body` may be nil. Wrapping it in `cancelBody` causes a nil pointer dereference when `Close()` calls `b.ReadCloser.Close()`.

```diff
--- a/call/call.go
+++ b/call/call.go
@@ -36,7 +36,10 @@ type cancelBody struct {
 }

 func (b *cancelBody) Close() error {
-	err := b.ReadCloser.Close()
+	var err error
+	if b.ReadCloser != nil {
+		err = b.ReadCloser.Close()
+	}
 	b.cancel()
 	return err
 }
```

---

### HIGH-4: Unbounded Response Body Drain in Retry Loop
**File:** `call/retry.go:73-75`

On 5xx retry, the failed response body is drained via `io.Copy(io.Discard, resp.Body)` with no size limit. A malicious server can send a multi-GB body that the retrier consumes in full.

```diff
--- a/call/retry.go
+++ b/call/retry.go
@@ -71,7 +71,7 @@ func (r *Retrier) Do(ctx context.Context, exec func() (*http.Response, error)) (
 		if attempt < r.MaxAttempts-1 {
 			trace.SpanFromContext(ctx).AddEvent("retry", trace.WithAttributes(
 				attribute.Int("attempt", attempt+1),
 				attribute.Int("http.status_code", resp.StatusCode),
 			))
 			// Drain and close the body so the underlying connection can be reused.
-			io.Copy(io.Discard, resp.Body)
+			io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)) // 1 MB max drain
 			resp.Body.Close()
```

---

### HIGH-5: Goroutine Leak in `guard.Timeout` When Deadline Fires
**File:** `guard/timeout.go:40-52, 61-66`

When the context deadline fires, the handler goroutine (line 50: `next.ServeHTTP(tw, r)`) continues running indefinitely if the handler doesn't check `ctx.Done()`. Under sustained load with slow handlers, goroutines accumulate without bound.

This matches Go's stdlib `http.TimeoutHandler` behavior, but the comment at line 63-65 should be promoted to package-level documentation. No simple patch — the fundamental design requires handlers to be context-aware. **Recommended mitigation:** add a prominent doc comment on `Timeout()`.

```diff
--- a/guard/timeout.go
+++ b/guard/timeout.go
@@ -15,6 +15,11 @@ import (
 // actively returns 504 Gateway Timeout if the handler does not complete before
 // the deadline fires. If the caller already set a tighter deadline, the tighter
 // deadline wins and no new deadline is applied.
+//
+// WARNING: When the deadline fires, the handler goroutine is NOT forcibly stopped.
+// It continues running until it returns or observes ctx.Done(). Handlers that
+// perform blocking I/O without checking the request context will leak goroutines.
+// Always honour ctx.Done() in handlers behind this middleware.
 func Timeout(d time.Duration) func(http.Handler) http.Handler {
```

---

### HIGH-6: `timeout()` Calls `writeProblem` While Holding Mutex — Deadlock Risk
**File:** `guard/timeout.go:152-159`

`writeProblem` performs network I/O (JSON encode + HTTP write) while `tw.mu` is held. If the write blocks (slow client), any concurrent call to `tw.Write` from the handler goroutine deadlocks on `tw.mu`.

```diff
--- a/guard/timeout.go
+++ b/guard/timeout.go
@@ -151,10 +151,14 @@ func (tw *timeoutWriter) flush() {
 // timeout writes 504 with RFC 9457 Problem Details if the handler hasn't started writing yet.
 func (tw *timeoutWriter) timeout() {
 	tw.mu.Lock()
-	defer tw.mu.Unlock()
 	if tw.written || tw.started {
+		tw.mu.Unlock()
 		return
 	}
 	tw.written = true
-	writeProblem(tw.w, tw.req, errors.TimeoutError("request timed out"))
+	tw.mu.Unlock()
+	// Write problem response outside the lock to avoid blocking the handler
+	// goroutine's tw.Write calls during network I/O.
+	writeProblem(tw.w, tw.req, errors.TimeoutError("request timed out"))
 }
```

---

### HIGH-7: Registry `appendLogLocked` Silently Discards Write Errors
**File:** `registry/registry.go:658-667`

`logFile.Write(...)` return values are discarded. On a full filesystem, log entries are silently lost with no indication.

```diff
--- a/registry/registry.go
+++ b/registry/registry.go
@@ -663,5 +663,7 @@ func appendLogLocked(entry map[string]any) {
 	if err != nil {
 		return
 	}
-	logFile.Write(append(data, '\n'))
+	if _, err := logFile.Write(append(data, '\n')); err != nil {
+		fmt.Fprintf(os.Stderr, "registry: log write failed: %v\n", err)
+	}
 }
```

---

### HIGH-8: Registry `ShutdownCLI` Ignores `atomicWrite` Error
**File:** `registry/registry.go:531`

If the final PID file rewrite fails (full disk), viewer tooling reads stale "running" state indefinitely.

```diff
--- a/registry/registry.go
+++ b/registry/registry.go
@@ -528,7 +528,9 @@ func ShutdownCLI(exitCode int) {
 		reg.ExitCode = &ec
 		reg.Summary = lastProgress
-		atomicWrite(pidPath, reg)
+		if err := atomicWrite(pidPath, reg); err != nil {
+			fmt.Fprintf(os.Stderr, "registry: failed to write final PID file: %v\n", err)
+		}
 	}
```

---

## MEDIUM Severity Issues

### MED-1: Lifecycle Heartbeat Receives Parent Context Instead of `infraCtx`
**File:** `lifecycle/lifecycle.go:134`

`heartbeatkit.Start(ctx, pub, ...)` uses the raw parent `ctx` (typically `context.Background()`), not `infraCtx` or `signalCtx`. The heartbeat ignores signal-driven cancellation and only stops via `heartbeatkit.Stop()` at line 176.

```diff
--- a/lifecycle/lifecycle.go
+++ b/lifecycle/lifecycle.go
@@ -131,7 +131,7 @@ func Run(ctx context.Context, opts ...Option) error {

 		// Start heartbeatkit.
-		heartbeatkit.Start(ctx, pub, heartbeatkit.Config{
+		heartbeatkit.Start(signalCtx, pub, heartbeatkit.Config{
 			ServiceName: svcName,
 			Version:     chassis.Version,
 		})
```

---

### MED-2: Lifecycle Shutdown Sequence — Heartbeat Can Fire After "Stopping" Announcement
**File:** `lifecycle/lifecycle.go:172-177`

The order is: announce stopping → stop heartbeat → close publisher. The heartbeat ticker could fire between the announcement and `heartbeatkit.Stop()`.

```diff
--- a/lifecycle/lifecycle.go
+++ b/lifecycle/lifecycle.go
@@ -171,9 +171,9 @@ func Run(ctx context.Context, opts ...Option) error {
 	// Kafkakit shutdown sequence.
 	if pub != nil {
+		heartbeatkit.Stop()
 		stopCtx, stopCancel := context.WithTimeout(context.Background(), AnnounceTimeout)
 		_ = announcekit.Stopping(stopCtx, pub)
 		stopCancel()
-		heartbeatkit.Stop()
 		pub.Close()
 	}
```

---

### MED-3: `work.Stream` Span Context Discarded — Child Spans Orphaned
**File:** `work/work.go:276`

The `_` discards the span context, so per-item child spans at line ~302 attach to the wrong parent.

```diff
--- a/work/work.go
+++ b/work/work.go
@@ -273,7 +273,7 @@ func Stream[T any, R any](ctx context.Context, in <-chan T, fn func(context.Cont

 		tracer := otelapi.GetTracerProvider().Tracer(tracerName)
-		_, span := tracer.Start(ctx, "work.Stream", trace.WithAttributes(
+		ctx, span := tracer.Start(ctx, "work.Stream", trace.WithAttributes(
 			attribute.String("work.pattern", "stream"),
 		))
 		defer span.End()
```

---

### MED-4: `guard.RateLimit` Hardcoded `Retry-After: 1` Ignores Rate Window
**File:** `guard/ratelimit.go:119`

A 1-minute rate limit window still tells clients to retry in 1 second, causing hammering.

```diff
--- a/guard/ratelimit.go
+++ b/guard/ratelimit.go
@@ -116,7 +116,7 @@ func RateLimit(cfg RateLimitConfig) func(http.Handler) http.Handler {
 		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
 			key := cfg.KeyFunc(r)
 			if !lim.allow(key) {
-				w.Header().Set("Retry-After", "1")
+				w.Header().Set("Retry-After", strconv.Itoa(max(1, int(cfg.Window.Seconds()))))
 				writeProblem(w, r, errors.RateLimitError("rate limit exceeded"))
 				return
 			}
```

---

### MED-5: HSTS Header Triggered by Untrusted `X-Forwarded-Proto`
**File:** `guard/secheaders.go:73`

A malicious client can send `X-Forwarded-Proto: https` to force HSTS on a plain HTTP response without TLS.

```diff
--- a/guard/secheaders.go
+++ b/guard/secheaders.go
@@ -70,7 +70,8 @@ func SecurityHeaders(cfg SecurityHeadersConfig) func(http.Handler) http.Handler
 			if cfg.PermissionsPolicy != "" {
 				w.Header().Set("Permissions-Policy", cfg.PermissionsPolicy)
 			}
-			if hstsValue != "" && (r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https") {
+			// Only trust X-Forwarded-Proto when behind TLS or a reverse proxy.
+			if hstsValue != "" && r.TLS != nil {
 				w.Header().Set("Strict-Transport-Security", hstsValue)
 			}
```

---

### MED-6: `call/token.go` Mutex Held During Remote Fetch
**File:** `call/token.go:44-58`

`ct.mu.Lock()` is held for the entire duration of `ct.fetch(ctx)`. If the OAuth server is slow, all concurrent `Token()` callers block. If the request context expires while locked, subsequent callers cascade-fail.

```diff
--- a/call/token.go
+++ b/call/token.go
@@ -43,15 +43,24 @@ func Leeway(d time.Duration) TokenOption {
 // Token returns a cached token, refreshing if needed.
 func (ct *CachedToken) Token(ctx context.Context) (string, error) {
 	ct.mu.Lock()
-	defer ct.mu.Unlock()
-
 	if ct.token != "" && time.Now().Add(ct.leeway).Before(ct.expires) {
+		token := ct.token
+		ct.mu.Unlock()
-		return ct.token, nil
+		return token, nil
 	}
+	ct.mu.Unlock()

+	// Fetch outside the lock to avoid blocking concurrent callers.
 	token, expires, err := ct.fetch(ctx)
 	if err != nil {
 		return "", err
 	}
+
+	ct.mu.Lock()
 	ct.token = token
 	ct.expires = expires
+	ct.mu.Unlock()
 	return token, nil
 }
```

---

### MED-7: `metrics.Counter()` / `Histogram()` Silently Discard Errors
**File:** `metrics/metrics.go:192, 202`

Unlike `New()` which logs meter creation failures, `Counter()` and `Histogram()` silently swallow the error. A custom metric silently becomes a no-op.

```diff
--- a/metrics/metrics.go
+++ b/metrics/metrics.go
@@ -189,8 +189,11 @@ func (r *Recorder) Counter(name string) *CounterVec {
 	fullName := r.prefix + "_" + name
-	cv, _ := r.meter.Float64Counter(
+	cv, err := r.meter.Float64Counter(
 		fullName,
 		metric.WithDescription("Custom counter: "+name),
 	)
+	if err != nil && r.logger != nil {
+		r.logger.Warn("metrics: counter creation failed", "name", fullName, "error", err)
+	}
 	return &CounterVec{inner: cv, name: name, recorder: r}
 }

@@ -199,8 +202,11 @@ func (r *Recorder) Histogram(name string, buckets []float64) *HistogramVec {
 	fullName := r.prefix + "_" + name
-	hv, _ := r.meter.Float64Histogram(
+	hv, err := r.meter.Float64Histogram(
 		fullName,
 		metric.WithDescription("Custom histogram: "+name),
 		metric.WithExplicitBucketBoundaries(buckets...),
 	)
+	if err != nil && r.logger != nil {
+		r.logger.Warn("metrics: histogram creation failed", "name", fullName, "error", err)
+	}
 	return &HistogramVec{inner: hv, name: name, recorder: r}
 }
```

---

### MED-8: `secval.SafeFilename` No Maximum Length Enforcement
**File:** `secval/secval.go:104-120`

No upper bound on output length. A 100 MB input produces a 100 MB "safe" filename that causes `ENAMETOOLONG`.

```diff
--- a/secval/secval.go
+++ b/secval/secval.go
@@ -117,6 +117,9 @@ func SafeFilename(s string) string {
 	if cleaned == "" {
 		return "unnamed"
 	}
+	if len(cleaned) > 255 {
+		cleaned = cleaned[:255]
+	}
 	return cleaned
 }
```

---

### MED-9: `call/breaker.go` Global `sync.Map` Grows Without Bound
**File:** `call/breaker.go:40-69`

`GetBreaker` stores circuit breakers in a package-level `sync.Map`. If callers use dynamic names (per-user, per-endpoint), the registry grows unboundedly. `RemoveBreaker` exists but is never called automatically.

**Recommended fix:** Add a `MaxBreakers` configuration or LRU eviction policy. At minimum, document the growth risk prominently.

---

### MED-10: `lifecycle.AnnounceTimeout` is a Mutable Package-Level Variable
**File:** `lifecycle/lifecycle.go:27`

Mutated in tests (`AnnounceTimeout = 100 * time.Millisecond`). Data race if tests ever run in parallel.

**Recommended fix:** Make it a field on the `options` struct, configurable via a `WithAnnounceTimeout` option.

---

### MED-11: `logz.traceHandler` Inner Slice Sharing in `WithAttrs`
**File:** `logz/logz.go:133-134`

`copy(groupAttrs, h.groupAttrs)` copies the outer slice but inner `[]slog.Attr` slices are shared references. Two concurrent `WithAttrs` calls on the same handler can race on the inner slices.

```diff
--- a/logz/logz.go
+++ b/logz/logz.go
@@ -131,6 +131,9 @@ func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
 	groupAttrs = make([][]slog.Attr, len(h.groupAttrs))
 	copy(groupAttrs, h.groupAttrs)
+	// Deep copy inner slices to prevent concurrent access races.
+	for i, inner := range groupAttrs {
+		groupAttrs[i] = append([]slog.Attr(nil), inner...)
+	}
```

---

### MED-12: `guard.Timeout` `flush()` Holds Mutex During Network I/O
**File:** `guard/timeout.go:129-149`

Same class as HIGH-6. `tw.w.Write(tw.buf)` at line 145 is called under `tw.mu`. If the write blocks, the handler goroutine's calls to `tw.Write` deadlock.

```diff
--- a/guard/timeout.go
+++ b/guard/timeout.go
@@ -128,8 +128,7 @@ func (tw *timeoutWriter) Write(b []byte) (int, error) {
 // flush writes the buffered response to the real ResponseWriter.
 func (tw *timeoutWriter) flush() {
 	tw.mu.Lock()
-	defer tw.mu.Unlock()
 	if tw.written {
+		tw.mu.Unlock()
 		return
 	}
 	tw.written = true
@@ -136,12 +135,15 @@ func (tw *timeoutWriter) flush() {
+	hdrs := tw.headers
+	code := tw.code
+	buf := tw.buf
+	tw.mu.Unlock()
+
-	for k, vs := range tw.headers {
+	for k, vs := range hdrs {
 		for _, v := range vs {
 			tw.w.Header().Add(k, v)
 		}
 	}
-	if tw.code > 0 {
-		tw.w.WriteHeader(tw.code)
+	if code > 0 {
+		tw.w.WriteHeader(code)
 	}
-	if len(tw.buf) > 0 {
-		if _, err := tw.w.Write(tw.buf); err != nil {
+	if len(buf) > 0 {
+		if _, err := tw.w.Write(buf); err != nil {
 			slog.Error("guard: timeout flush write failed", "error", err)
 		}
 	}
```

---

### MED-13: gRPC `StreamMetrics` and `StreamLogging` Use Cancelled Context Post-Handler
**File:** `grpckit/interceptors.go:110, 201-209`

After `handler(srv, ss)` returns, the stream context may be cancelled by gRPC. Logging and metric recording use this potentially-cancelled context.

**Recommended fix:** Capture the context before calling the handler, or use `context.WithoutCancel()` (Go 1.21+) for the post-handler log/metric calls.

---

### MED-14: `config.MustLoad` Silently Skips Pointer-to-Struct Fields
**File:** `config/config.go:52-56`

A config struct with `*DatabaseConfig` (pointer to struct) is silently skipped. Env vars for the nested struct are never loaded.

```diff
--- a/config/config.go
+++ b/config/config.go
@@ -50,6 +50,13 @@ func loadFields(v reflect.Value, t reflect.Type) {
 		if field.Type.Kind() == reflect.Struct && field.Type != reflect.TypeOf(time.Duration(0)) {
 			loadFields(fieldVal, field.Type)
 			continue
 		}
+		if field.Type.Kind() == reflect.Ptr && field.Type.Elem().Kind() == reflect.Struct {
+			if fieldVal.IsNil() {
+				fieldVal.Set(reflect.New(field.Type.Elem()))
+			}
+			loadFields(fieldVal.Elem(), field.Type.Elem())
+			continue
+		}
```

---

### MED-15: `call/call.go` OTel Span Not Ended on Panic
**File:** `call/call.go:148, 165-259`

`span.End()` is called explicitly in each return path but not via `defer`. A panic inside `exec()` or `retrier.Do()` leaves the span unfinished, leaking resources in the OTel SDK.

```diff
--- a/call/call.go
+++ b/call/call.go
@@ -146,6 +146,7 @@ func (c *Client) Do(req *http.Request) (*http.Response, error) {
 	ctx, span := tracer.Start(ctx, req.Method+" "+req.URL.Path,
 		trace.WithSpanKind(trace.SpanKindClient),
 	)
+	defer span.End()
```

Then remove the explicit `span.End()` calls from each early-return path.

---

### MED-16: `httpkit/middleware.go` ResponseWriter Wrapper Interop Bug
**File:** `httpkit/middleware.go:136-140`, `guard/timeout.go:74-84`

`Recovery` type-asserts `w` to `*responseWriter`. If `guard.Timeout` wraps `w` first, the assertion fails and a redundant wrapper is created, splitting `headerWritten` tracking across two incompatible wrappers.

**Recommended fix:** Use `http.ResponseController` / `Unwrap()` chain to find the inner `*responseWriter` instead of a direct type assertion.

---

## LOW Severity Issues

### LOW-1: `config.setField` Rejects `int8`, `int16`, `uint*`, `float32` With Opaque Error
**File:** `config/config.go:115-148` — Expand the type switch or document supported types.

### LOW-2: `flagz.Enabled` Only Matches Exact `"true"` — Rejects `"1"`, `"yes"`, `"on"`
**File:** `flagz/flagz.go:44-47` — Use `strconv.ParseBool` for broader acceptance.

### LOW-3: `flagz.consistentBucket` FNV-32a Mod 100 Has Modulo Bias (~0.002%)
**File:** `flagz/flagz.go:86-92` — Document known bias or use rejection sampling.

### LOW-4: `work.Stream` Silently Drops Results on Context Cancellation
**File:** `work/work.go:310-313` — Document that result count < input count on cancellation.

### LOW-5: `work.Map`/`All` `wg.Wait()` Has No Timeout
**File:** `work/work.go:89-116, 157-183` — Document that in-flight goroutines run to completion.

### LOW-6: `health.Handler` Ignores `w.Write` Error
**File:** `health/handler.go:46` — Log the write error for observability.

### LOW-7: `lifecycle.Run` With Zero Components Silently Succeeds
**File:** `lifecycle/lifecycle.go:86-196` — Add `slog.Warn` for empty component list.

### LOW-8: Signal-Handling Test Uses `time.Sleep` for Synchronisation
**File:** `lifecycle/lifecycle_test.go:134-135` — Replace with readiness channel to avoid flakiness.

### LOW-9: `secval.ValidateIdentifier` Blocks Only 20 SQL Reserved Words
**File:** `secval/secval.go:138-158` — Document limited scope or expand the list.

### LOW-10: `secval.ValidateJSON` Off-By-One for Map-Terminated Nesting at MaxDepth
**File:** `secval/secval.go:52-55` — Change `depth >= MaxNestingDepth` to `depth > MaxNestingDepth`.

### LOW-11: `testkit.Sequence` Panics on Zero Handlers
**File:** `testkit/httpserver.go:69-81` — Add guard: `if len(handlers) == 0 { panic(...) }`.

### LOW-12: `testkit.NewHTTPServer` Reads Entire Request Body With No Size Limit
**File:** `testkit/httpserver.go:41-55` — Use `io.LimitReader`.

### LOW-13: `call/retry.go` Exponential Backoff Can Overflow `time.Duration`
**File:** `call/retry.go:94-96` — Cap `delay` at a max (e.g., 30s) before applying jitter.

### LOW-14: `registry.readVersion()` Reads From Working Directory at Runtime
**File:** `registry/registry.go:589-594` — Accept version as a parameter instead.

### LOW-15: `registry.CmdPollInterval = 1` in Tests Is a Bare Integer (1 nanosecond)
**File:** `registry/registry_test.go:483` — Use `1 * time.Nanosecond` for clarity.

### LOW-16: `chassis.Port` Offset Can Exceed Ephemeral Port Range
**File:** `chassis.go:76-86` — Document or clamp `port + offset` to stay below 49152.

### LOW-17: `logz.New` Hardcoded to `os.Stderr`
**File:** `logz/logz.go:20-23` — Add `NewWithWriter(w io.Writer, ...)` variant.

### LOW-18: `grpckit.health` Watch RPC Unimplemented, Undocumented
**File:** `grpckit/health.go:25` — Document in `RegisterHealth` godoc.

### LOW-19: Dependency Surface — `golang.org/x/crypto` and `google.golang.org/grpc`
**File:** `go.mod:16,18` — Run `govulncheck ./...` in CI. gRPC had HTTP/2 Rapid Reset (CVE-2023-44487) issues.

---

## Strengths

- **Version gate mechanism** (`RequireMajor` + `AssertVersionChecked`) consistently applied across all packages
- **Cardinality protection** in `metrics` uses proper double-checked locking (previously fixed)
- **Atomic file writes** in `registry.atomicWrite` prevent corrupt PID files on crash
- **Permission validation** on the registry directory at `Init` time prevents symlink attacks
- **Fail-fast constructors** in `guard` package panic on invalid config instead of silent degradation
- **`secval.ValidateJSON`** is conservative and correct for its stated purpose
- **OTel as sole SDK consumer** — all other packages use API-only imports, preventing dependency conflicts
- **`cancelBody` pattern** in `call` correctly ties context lifetime to response body lifetime (minus the nil body edge case)
- **Test coverage is generally good** — the test files exercise most code paths with meaningful assertions

---

## Summary Statistics

| Severity | Count |
|----------|-------|
| HIGH | 8 |
| MEDIUM | 16 |
| LOW | 19 |
| **Total** | **43** |

| Category | Issues |
|----------|--------|
| Security | 4 (REG-1, HIGH-4, MED-5, LOW-9) |
| Concurrency | 8 (HIGH-5, HIGH-6, MED-6, MED-10, MED-11, MED-12, MED-13, LOW-5) |
| Correctness | 12 (HIGH-2, HIGH-3, HIGH-7, HIGH-8, MED-1, MED-2, MED-3, MED-7, MED-14, MED-15, LOW-4, LOW-10) |
| API Design | 10 (MED-4, MED-8, MED-9, MED-16, LOW-1, LOW-2, LOW-6, LOW-7, LOW-16, LOW-17) |
| Test Quality | 5 (LOW-8, LOW-11, LOW-12, LOW-15, LOW-18) |
| Dependencies | 1 (LOW-19) |
| Code Quality | 3 (LOW-3, LOW-13, LOW-14) |

---

*Report generated by Claude Code (Claude:Opus 4.6) — 2026-03-27*
