Date Created: 2026-03-21 09:31:54 -0500
TOTAL_SCORE: 71/100

# Chassis-Go Code Audit & Fix Report

**Agent:** Claude Code (Claude:Opus 4.6)
**Module:** `github.com/ai8future/chassis-go/v9` — Go 1.25.5
**Scope:** Full codebase audit — all packages, tests, go.mod
**Note:** Loop variable capture is NOT a bug in Go 1.22+. Not flagged.

---

## Scoring Rationale

| Category | Score |
|----------|-------|
| Architecture & design | 9/10 — Clean toolkit design, zero cross-deps, well-separated concerns |
| Error handling | 6/10 — Several silent error discards (metrics, webhook, otel) |
| Concurrency safety | 5/10 — Data race in webhook, header mutation in call, shared shutdown ctx |
| API correctness | 7/10 — Mostly correct; timeout edge case and stream span orphaning |
| Test coverage | 7/10 — Good coverage but stale version strings and missing edge cases |
| Code quality & consistency | 8/10 — Idiomatic Go, consistent style, minor inconsistencies |
| Security | 8/10 — seal package is solid; rand.Read error ignored is the main gap |
| Documentation & naming | 7/10 — Generally clear; stub implementations lack warnings |

**Deductions:**
- 3 critical issues: -15 points
- 5 high issues: -20 points
- 6 medium issues: -12 points
- Partial credit for good architecture, test coverage, and clean API design: +18 points

**Final: 71/100** — Good codebase with real bugs that need attention before production hardening.

---

## Critical Issues

### CRIT-1: Data race on `Delivery` struct fields in `webhook/webhook.go`

**File:** `webhook/webhook.go:83-84, 96-113`
**Confidence:** 90%

The `Send` method mutates `delivery.Attempts`, `delivery.Status`, and `delivery.LastError` outside of `s.mu`. Meanwhile, `Status(id)` reads the full `Delivery` struct under the lock. This is a textbook data race detectable by `-race`.

```go
// Current (unsafe):
for attempt := 1; attempt <= s.maxAttempts; attempt++ {
    delivery.Attempts = attempt          // WRITE — no lock
    // ... HTTP call ...
    delivery.Status = "delivered"        // WRITE — no lock
    delivery.Status = "failed"           // WRITE — no lock
    delivery.LastError = ...             // WRITE — no lock
}
```

**Patch:**

```diff
--- a/webhook/webhook.go
+++ b/webhook/webhook.go
@@ -80,7 +80,10 @@ func (s *Sender) Send(url string, payload any, secret string) (string, error) {
 	var lastErr error
 	for attempt := 1; attempt <= s.maxAttempts; attempt++ {
-		delivery.Attempts = attempt
+		s.mu.Lock()
+		delivery.Attempts = attempt
+		s.mu.Unlock()

 		req, err := http.NewRequest("POST", url, bytes.NewReader(body))
@@ -104,15 +107,21 @@ func (s *Sender) Send(url string, payload any, secret string) (string, error) {
 		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
-			delivery.Status = "delivered"
+			s.mu.Lock()
+			delivery.Status = "delivered"
+			s.mu.Unlock()
 			return id, nil
 		}

 		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
-			delivery.Status = "failed"
-			delivery.LastError = fmt.Sprintf("HTTP %d", resp.StatusCode)
+			s.mu.Lock()
+			delivery.Status = "failed"
+			delivery.LastError = fmt.Sprintf("HTTP %d", resp.StatusCode)
+			s.mu.Unlock()
 			return "", fmt.Errorf("%w: HTTP %d", ErrClientError, resp.StatusCode)
 		}

 		lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
 	}

-	delivery.Status = "failed"
-	delivery.LastError = lastErr.Error()
+	s.mu.Lock()
+	delivery.Status = "failed"
+	delivery.LastError = lastErr.Error()
+	s.mu.Unlock()
```

---

### CRIT-2: Shared `shutdownCtx` starves metric provider shutdown in `otel/otel.go`

**File:** `otel/otel.go:120-126`
**Confidence:** 95%

`tp.Shutdown` and `mp.Shutdown` share the same `shutdownCtx` with a single 5-second timeout. If the trace provider consumes most of the budget, the metric provider gets whatever time remains — potentially zero — and silently fails to flush.

```go
// Current:
shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
defer cancel()
tErr := tp.Shutdown(shutdownCtx)   // may consume the full 5s
mErr := mp.Shutdown(shutdownCtx)   // runs on whatever time remains
```

**Patch:**

```diff
--- a/otel/otel.go
+++ b/otel/otel.go
@@ -120,9 +120,11 @@ func Setup(ctx context.Context, cfg Config) (func(context.Context) error, error)
 	return func(ctx context.Context) error {
-		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
-		defer cancel()
-		tErr := tp.Shutdown(shutdownCtx)
-		mErr := mp.Shutdown(shutdownCtx)
+		tCtx, tCancel := context.WithTimeout(ctx, 5*time.Second)
+		defer tCancel()
+		tErr := tp.Shutdown(tCtx)
+		mCtx, mCancel := context.WithTimeout(ctx, 5*time.Second)
+		defer mCancel()
+		mErr := mp.Shutdown(mCtx)
 		return errors.Join(tErr, mErr)
 	}, nil
```

---

### CRIT-3: `regexp.MustCompile` panics with opaque message in `config/config.go`

**File:** `config/config.go:177-182`
**Confidence:** 88%

Every other validation failure in `validateField` uses `panic(fmt.Sprintf("config: field %s ..."))` with context. The `pattern` validator calls `regexp.MustCompile(value)` which panics with a bare runtime message missing the field name, making the startup crash unintelligible.

**Patch:**

```diff
--- a/config/config.go
+++ b/config/config.go
@@ -177,7 +177,10 @@ func validateField(name string, val reflect.Value, tag string) {
 		case "pattern":
-			re := regexp.MustCompile(value)
+			re, reErr := regexp.Compile(value)
+			if reErr != nil {
+				panic(fmt.Sprintf("config: field %s has invalid pattern %q in validate tag: %v", name, value, reErr))
+			}
 			actual := fmt.Sprintf("%v", val.Interface())
 			if !re.MatchString(actual) {
 				panic(fmt.Sprintf("config: field %s value %q does not match pattern %s", name, actual, value))
```

---

## High Issues

### HIGH-1: `call/call.go` — `Do()` mutates caller's `http.Request.Header` map

**File:** `call/call.go:155-171`
**Confidence:** 90%

`req.WithContext(ctx)` returns a shallow copy — the `Header` map is shared by reference. Both OTel propagator injection (line 157) and `req.Header.Set("Authorization", ...)` (line 171) mutate the caller's original header map. In `Batch` scenarios where requests share headers, this is a data race.

**Patch:**

```diff
--- a/call/call.go
+++ b/call/call.go
@@ -155,6 +155,8 @@ func (c *Client) Do(req *http.Request) (*http.Response, error) {
 	ctx, span := tracer.Start(ctx, req.Method+" "+req.URL.Path, ...)
 	req = req.WithContext(ctx)
+	// Deep-copy headers to avoid mutating the caller's original Request.
+	req.Header = req.Header.Clone()
 	otelapi.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
```

---

### HIGH-2: `guard/timeout.go` — Client hangs when handler calls `WriteHeader` then blocks

**File:** `guard/timeout.go:151-160`
**Confidence:** 95%

If a handler calls `WriteHeader(200)` and then blocks on a slow operation, `tw.started` is `true`. When the deadline fires, `timeout()` checks `tw.written || tw.started` and silently returns — never flushing anything to the client. The client connection hangs indefinitely with no response.

**Patch:**

```diff
--- a/guard/timeout.go
+++ b/guard/timeout.go
@@ -151,9 +151,19 @@ func (tw *timeoutWriter) timeout() {
 	tw.mu.Lock()
 	defer tw.mu.Unlock()
-	if tw.written || tw.started {
+	if tw.written {
 		return
 	}
 	tw.written = true
-	writeProblem(tw.w, tw.req, errors.TimeoutError("request timed out"))
+	if tw.started {
+		// Handler wrote a status code but no body yet — flush whatever
+		// we have so the client sees a truncated response rather than hanging.
+		for k, vs := range tw.headers {
+			for _, v := range vs {
+				tw.w.Header().Add(k, v)
+			}
+		}
+		tw.w.WriteHeader(tw.code)
+	} else {
+		writeProblem(tw.w, tw.req, errors.TimeoutError("request timed out"))
+	}
 }
```

---

### HIGH-3: `work/work.go` — `Stream` span context discarded; child spans orphaned

**File:** `work/work.go:276-279`
**Confidence:** 82%

`Stream` starts a span but discards the enriched context. Child spans per-item use the original `ctx` and become orphaned — breaking the parent-child trace hierarchy. Compare to `Map` and `All` which correctly reassign `ctx`.

```go
// Current (broken):
_, span := tracer.Start(ctx, "work.Stream", ...)  // enriched ctx thrown away

// Correct (as in Map/All):
ctx, span := tracer.Start(ctx, "work.Map", ...)
```

**Patch:**

```diff
--- a/work/work.go
+++ b/work/work.go
@@ -276,7 +276,7 @@ func Stream[T any, R any](ctx context.Context, ...) (<-chan StreamResult[R], erro
-	_, span := tracer.Start(ctx, "work.Stream", trace.WithAttributes(
+	streamCtx, span := tracer.Start(ctx, "work.Stream", trace.WithAttributes(
 		attribute.String("work.pattern", "stream"),
 	))
 	defer span.End()
@@ -290,7 +290,7 @@ func Stream[T any, R any](ctx context.Context, ...) (<-chan StreamResult[R], erro
 		go func() {
 			...
-			childCtx, childSpan := tracer.Start(ctx, "work.Stream.item",
+			childCtx, childSpan := tracer.Start(streamCtx, "work.Stream.item",
```

---

### HIGH-4: `metrics/metrics.go` — `Counter`/`Histogram` silently discard meter creation errors

**File:** `metrics/metrics.go:191-208`
**Confidence:** 90%

`New()` logs meter creation failures with `logger.Warn`, but `Counter()` and `Histogram()` use `_` to discard the error. If meter creation fails (e.g., duplicate name with incompatible type), callers get a silently no-op instrument with no diagnostic. The `Recorder` already has a `logger` field available.

**Patch:**

```diff
--- a/metrics/metrics.go
+++ b/metrics/metrics.go
@@ -191,10 +191,13 @@ func (r *Recorder) Counter(name string) *CounterVec {
 	fullName := r.prefix + "_" + name
-	cv, _ := r.meter.Float64Counter(
+	cv, err := r.meter.Float64Counter(
 		fullName,
 		metric.WithDescription("Custom counter: "+name),
 	)
+	if err != nil && r.logger != nil {
+		r.logger.Warn("metrics: failed to create counter", "name", fullName, "error", err)
+	}
 	return &CounterVec{inner: cv, name: name, recorder: r}
 }

 func (r *Recorder) Histogram(name string, buckets []float64) *HistogramVec {
 	fullName := r.prefix + "_" + name
-	hv, _ := r.meter.Float64Histogram(
+	hv, err := r.meter.Float64Histogram(
 		fullName,
 		metric.WithDescription("Custom histogram: "+name),
 		metric.WithExplicitBucketBoundaries(buckets...),
 	)
+	if err != nil && r.logger != nil {
+		r.logger.Warn("metrics: failed to create histogram", "name", fullName, "error", err)
+	}
 	return &HistogramVec{inner: hv, name: name, recorder: r}
 }
```

---

### HIGH-5: `xyops/xyops.go` — Zero `monitorInterval` causes `time.NewTicker(0)` panic

**File:** `xyops/xyops.go:107-110`
**Confidence:** 82%

If `WithMonitoring(0)` is called or `Config.MonitorInterval` is 0, then `c.monitorInterval` stays at 0. When `Run` passes this to `tick.Every(0, ...)`, which calls `time.NewTicker(0)`, Go panics: "non-positive interval for NewTicker".

**Patch:**

```diff
--- a/xyops/xyops.go
+++ b/xyops/xyops.go
@@ -107,6 +107,9 @@ func New(cfg Config, opts ...Option) *Client {
 	if c.monitorInterval == 0 {
 		c.monitorInterval = cfg.MonitorInterval
 	}
+	if c.monitorInterval <= 0 {
+		c.monitorInterval = 30 // safe default: 30 seconds
+	}
 	return c
 }
```

---

## Medium Issues

### MED-1: `webhook/webhook.go` — `rand.Read` error silently ignored in `generateID`

**File:** `webhook/webhook.go:159-163`
**Confidence:** 90%

If `crypto/rand.Read` fails (extremely rare but possible under entropy starvation), `b` is zero-filled and all delivery IDs become `"00000000000000000000000000000000"`, causing tracking collisions.

**Patch:**

```diff
--- a/webhook/webhook.go
+++ b/webhook/webhook.go
@@ -159,6 +159,8 @@ func generateID() string {
 	b := make([]byte, 16)
-	rand.Read(b)
+	if _, err := rand.Read(b); err != nil {
+		panic("webhook: failed to generate random delivery ID: " + err.Error())
+	}
 	return hex.EncodeToString(b)
 }
```

---

### MED-2: `config/config.go` — Validate tag comma-split breaks regex patterns containing commas

**File:** `config/config.go:141-144`
**Confidence:** 83%

`validateField` splits the tag value on `","`. A pattern like `validate:"pattern=^(a,b)$"` is silently truncated to `pattern=^(a` and orphan part `b)$`. No escaping mechanism exists.

**Patch:** Document the limitation or implement key-aware splitting:

```diff
--- a/config/config.go
+++ b/config/config.go
@@ -139,6 +139,7 @@ func validateField(name string, val reflect.Value, tag string) {
 	if tag == "" {
 		return
 	}
+	// NOTE: commas inside pattern= values are not supported; use character classes instead.
 	parts := strings.Split(tag, ",")
```

---

### MED-3: `guard/ratelimit.go` — `Retry-After` header hardcoded to `"1"` regardless of window

**File:** `guard/ratelimit.go:119`
**Confidence:** 80%

Per RFC 6585, `Retry-After` should reflect the actual wait time. A 1-hour window rate limiter tells clients to retry after 1 second, causing a storm of immediately-retried requests.

**Patch:**

```diff
--- a/guard/ratelimit.go
+++ b/guard/ratelimit.go
@@ -116,7 +116,11 @@ func RateLimit(cfg RateLimitConfig) func(http.Handler) http.Handler {
 		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
 			key := cfg.KeyFunc(r)
 			if !lim.allow(key) {
-				w.Header().Set("Retry-After", "1")
+				retryAfter := int(cfg.Window.Seconds())
+				if retryAfter < 1 {
+					retryAfter = 1
+				}
+				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
 				writeProblem(w, r, errors.RateLimitError("rate limit exceeded"))
```

Note: `strconv` import needed in `ratelimit.go`.

---

### MED-4: `registry/registry.go` — `ResetForTest` does not zero path fields

**File:** `registry/registry.go:541+`
**Confidence:** 85%

`ResetForTest` resets `handlers`, `ports`, `reg`, and `cancelFn` but does not zero `pidPath`, `logFilePath`, `cmdPath`, or `svcDir`. Under parallel test execution, stale path values from a previous test can leak into the next test's state.

**Patch:**

```diff
--- a/registry/registry.go
+++ b/registry/registry.go
@@ -541,6 +541,10 @@ func ResetForTest(path string) {
 	handlers = map[string]handlerEntry{}
 	ports = nil
 	reg = nil
 	cancelFn = nil
+	pidPath = ""
+	logFilePath = ""
+	cmdPath = ""
+	svcDir = ""
 }
```

---

### MED-5: `health/handler.go` — `w.Write` return value discarded

**File:** `health/handler.go:46`
**Confidence:** 82%

The encode error two lines above is logged with `slog.ErrorContext`, but the write error is silently discarded. Inconsistent error handling.

**Patch:**

```diff
--- a/health/handler.go
+++ b/health/handler.go
@@ -45,7 +45,9 @@ func Handler(checker Checker) http.HandlerFunc {
 	w.WriteHeader(code)
-	w.Write(buf.Bytes())
+	if _, writeErr := w.Write(buf.Bytes()); writeErr != nil {
+		slog.ErrorContext(r.Context(), "health: failed to write response", "error", writeErr)
+	}
```

---

### MED-6: `xyopsworker/worker.go` — `Run()` is a silent no-op stub

**File:** `xyopsworker/worker.go:113-127`
**Confidence:** 90%

`Run()` is documented as a lifecycle component and wired into `lifecycle.Run` per XYOPS.md. It silently blocks until context cancellation — no connection, no job execution, no warning. Any service integrating `xyopsworker` appears healthy but processes zero jobs.

**Patch:**

```diff
--- a/xyopsworker/worker.go
+++ b/xyopsworker/worker.go
@@ -116,5 +116,7 @@ func (w *Worker) Run(ctx context.Context) error {
+	// TODO: Replace with real WebSocket implementation.
+	slog.WarnContext(ctx, "xyopsworker: Run() is a stub — no WebSocket connection to "+w.config.MasterURL)
 	<-ctx.Done()
 	return nil
 }
```

---

## Low Issues

### LOW-1: `logz/logz.go` — `spanID != ""` guard is always true

**File:** `logz/logz.go:81-84`
**Confidence:** 80%

`sc.SpanID().String()` returns a hex string of `[8]byte` — never empty. The guard `spanID != ""` is always true when reached (after `sc.IsValid()` check). Misleading to maintainers.

**Patch:**

```diff
--- a/logz/logz.go
+++ b/logz/logz.go
@@ -81,9 +81,7 @@ func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
 	r.AddAttrs(slog.String("trace_id", traceID))
-	if spanID != "" {
-		r.AddAttrs(slog.String("span_id", spanID))
-	}
+	r.AddAttrs(slog.String("span_id", spanID))
 	return h.inner.Handle(ctx, r)
```

---

### LOW-2: `go.mod` — `golang.org/x/crypto` marked `// indirect` but directly imported

**File:** `go.mod:27`
**Confidence:** 90%

`seal/seal.go` directly imports `golang.org/x/crypto/scrypt`. The `// indirect` comment is incorrect and would cause a dirty diff on `go mod tidy`.

**Patch:**

```diff
--- a/go.mod
+++ b/go.mod
 require (
+	golang.org/x/crypto v0.48.0
 	...
 )

 require (
-	golang.org/x/crypto v0.48.0 // indirect
 	...
 )
```

---

### LOW-3: Stale version strings in test files

**Files:** `registry/registry_test.go:29,84`, `httpkit/httpkit_test.go:34`, `grpckit/grpckit_test.go:29`
**Confidence:** 95%

Multiple test files initialize the registry with `"5.0.0-test"` or `"6.0.0-test"` while the project is on the v9 module path. Cosmetic inconsistency.

**Patch (registry_test.go example):**

```diff
--- a/registry/registry_test.go
+++ b/registry/registry_test.go
@@ -29,7 +29,7 @@ func initRegistry(t *testing.T) string {
-	if err := registry.Init(cancel, "5.0.0-test"); err != nil {
+	if err := registry.Init(cancel, "9.0.0-test"); err != nil {
@@ -83,4 +83,4 @@ func TestPIDFileContainsCorrectFields(t *testing.T) {
-	if reg.ChassisVersion != "5.0.0-test" {
-		t.Errorf("ChassisVersion = %q, want %q", reg.ChassisVersion, "5.0.0-test")
+	if reg.ChassisVersion != "9.0.0-test" {
+		t.Errorf("ChassisVersion = %q, want %q", reg.ChassisVersion, "9.0.0-test")
```

Same change for `httpkit/httpkit_test.go` and `grpckit/grpckit_test.go`.

---

### LOW-4: `init()` vs `TestMain` inconsistency for `RequireMajor(9)`

**Files:** `seal/seal_test.go:10`, `cache/cache_test.go`, `tick/tick_test.go`, `webhook/webhook_test.go`, `xyops/xyops_test.go`, `xyopsworker/worker_test.go`
**Confidence:** 95%

Six test files use `func init() { chassis.RequireMajor(9) }` while the established convention in older packages (`flagz`, `testkit`) is `TestMain`. Minor inconsistency.

---

### LOW-5: `seal/seal.go` — `splitToken` scans from right instead of left

**File:** `seal/seal.go:209-216`
**Confidence:** 88%

The signature is always the final segment (hex-encoded HMAC, no dots). Scanning from the right is correct today but semantically inverted from typical token parsing conventions. Scanning from the left would be clearer and produce identical results.

**Patch:**

```diff
--- a/seal/seal.go
+++ b/seal/seal.go
@@ -209,7 +209,7 @@ func splitToken(token string) []string {
-	for i := len(token) - 1; i >= 0; i-- {
+	for i := 0; i < len(token); i++ {
 		if token[i] == '.' {
 			return []string{token[:i], token[i+1:]}
 		}
```

---

### LOW-6: `testkit/httpserver.go` — `Sequence()` with zero handlers panics with cryptic message

**File:** `testkit/httpserver.go:69-81`
**Confidence:** 88%

Calling `Sequence()` with no arguments causes an index-out-of-bounds panic on the first request. A deliberate panic with context would be better.

**Patch:**

```diff
--- a/testkit/httpserver.go
+++ b/testkit/httpserver.go
@@ -69,6 +69,9 @@ func Sequence(handlers ...http.Handler) http.Handler {
+	if len(handlers) == 0 {
+		panic("testkit.Sequence: must provide at least one handler")
+	}
 	var mu sync.Mutex
 	idx := 0
```

---

## Previously Known Issues (from MEMORY.md)

The following issues were already documented and confirmed still present:

| Issue | Status |
|-------|--------|
| `guard/timeout.go:35-49` — Goroutine leak when timeout fires | **Still present** — matches stdlib `TimeoutHandler` behavior; no clean fix in Go |
| `guard/keyfunc.go:54-60` — X-Forwarded-For IP spoofing | **Still present** — requires trusted proxy validation |
| `metrics/metrics.go:47-62` — Silently discarded meter creation errors | **Still present** — addressed in HIGH-4 above |
| `otel/otel.go` silently degrades when exporters fail | **Still present** — partially addressed by CRIT-2 fix |

---

## Summary

| Severity | Count | Issues |
|----------|-------|--------|
| Critical | 3 | Webhook data race, OTel shutdown starvation, config panic message |
| High | 5 | Header mutation, timeout hang, stream span orphan, metrics silence, xyops panic |
| Medium | 6 | rand.Read, comma-split, Retry-After, ResetForTest paths, health write, xyopsworker stub |
| Low | 6 | Span ID guard, go.mod indirect, version strings, init/TestMain, splitToken direction, Sequence guard |
| **Total** | **20** | |

**Priority fixes (do these first):**
1. CRIT-1 — Webhook data race (production race condition)
2. HIGH-2 — Timeout client hang (user-facing connection hang)
3. HIGH-1 — Header mutation in `call.Do` (data race in Batch)
4. CRIT-2 — OTel shutdown starvation (metric data loss)
5. HIGH-5 — Xyops zero interval panic (startup crash)
