Date Created: 2026-03-21 09:22:39 CET
TOTAL_SCORE: 71/100

# chassis-go Test Coverage Audit Report

**Agent:** Claude Code (Claude:Opus 4.6)
**Module:** `github.com/ai8future/chassis-go/v9`
**Go Version:** 1.25.5
**Packages Audited:** 24 (16 core + 5 new + 3 internal/support)
**Source Files:** 53 | **Test Files:** 31

---

## Scoring Breakdown

| Category | Max | Score | Notes |
|----------|-----|-------|-------|
| Test file existence (1:1 mapping) | 15 | 12 | `internal/otelutil/histogram.go` has zero tests; `guard/problem.go`, `errors/problem.go`, `health/handler.go`, `flagz/sources.go` lack dedicated files (partially covered by package-level tests) |
| Happy-path coverage | 25 | 22 | Nearly all exported functions have at least one success-path test |
| Error/edge-case coverage | 25 | 13 | Major gaps: nil inputs, malformed data, error-return paths, boundary conditions |
| Concurrency/race tests | 10 | 5 | Some packages (work, cache, registry) lack `-race` stress tests despite using mutexes |
| Observability path coverage | 15 | 10 | OTel span error-status branches, histogram recording, and flag evaluation tracing are largely untested |
| Boundary & regression guards | 10 | 9 | Good factory coverage; some boundary values (max length, zero values) missing |
| **TOTAL** | **100** | **71** | |

---

## Executive Summary

The chassis-go test suite is **solid on happy paths** — nearly every exported function has at least one positive test. The main weaknesses are:

1. **Error-path branches** — functions that return errors or handle nil/invalid inputs have many untested code paths
2. **OTel observability** — span error status, histogram recording, and flag evaluation tracing branches have near-zero coverage
3. **Four completely untested exported functions** in `xyops` (`CancelJob`, `SearchJobs`, `GetEvent`, `AckAlert`)
4. **`internal/otelutil/histogram.go`** has no test file at all
5. **`deploy.RunHook`** — all code paths completely untested

---

## Package-by-Package Gap Analysis

### 1. errors (`errors/errors.go`, `errors/problem.go`)

**Covered well:** All 9 error factories, `Error()`, `Unwrap()`, `GRPCStatus()`, `WithDetail`, `WithDetails`, `WithType`, `WithCause`, `FromError` (happy paths), `Errorf`, `ProblemDetail`, `WriteProblem`, `MarshalJSON`.

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| HIGH | `FromError(nil)` | Nil fast-path returns nil — never tested (line 140) |
| HIGH | `WriteProblem(w, r, nil, "")` | Nil-error early return — never tested (line 107 of problem.go) |
| HIGH | `ProblemDetail(nil)` | Nil request guard — never tested (line 83 of problem.go) |
| MEDIUM | `WithDetail` / `WithDetails` | Receiver immutability never verified — no test asserts original is unmodified |
| MEDIUM | `WithCause(nil)` | Clearing an existing cause untested |
| MEDIUM | `MarshalJSON` | `instance` absent from JSON when empty — omit branch untested |
| LOW | `ProblemDetail` type URIs | Status codes 401, 403, 413, 429, 500, 503, 504 type URIs never asserted |

### 2. guard (`guard/*.go`)

**Covered well:** All middleware constructors, happy paths, panic-on-invalid-config guards.

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| HIGH | `timeoutWriter.Write()` after timeout | Discard path returning `http.ErrHandlerTimeout` untested |
| HIGH | `Timeout` handler panic propagation | Panic → `panicChan` → re-panic on main goroutine untested |
| MEDIUM | `timeoutWriter.Unwrap()` | Completely untested |
| MEDIUM | `CORS` `Vary: Origin` header | Not asserted in any test |
| MEDIUM | `RateLimit` `Retry-After` header | Not asserted on 429 responses |
| MEDIUM | `IPFilter` custom `KeyFunc` path | Never tested through IPFilter directly |
| MEDIUM | `XForwardedFor` all-trusted-IPs branch | Loop exhaustion → fallback to `host` untested |
| LOW | `MaxBody` body exactly at limit | Boundary condition `ContentLength == maxBytes` untested |
| LOW | `SecurityHeaders` HSTS via `r.TLS != nil` | Only `X-Forwarded-Proto` path tested |

### 3. httpkit (`httpkit/*.go`)

**Covered well:** `RequestID`, `Logging`, `Recovery`, `JSONError`, `JSONProblem`, `Tracing` (happy paths).

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| **CRITICAL** | `Tracing` — 5xx span error status | `codes.Error` branch (line 59) never exercised |
| **CRITICAL** | `Tracing` — histogram recording | `getHTTPDurationHistogram().Record()` path untested |
| MEDIUM | `Recovery` — non-string panic value | `panic(42)` or `panic(errors.New(...))` untested |
| MEDIUM | `responseWriter.WriteHeader` | Double-call suppression not tested in isolation |
| LOW | `generateID` fallback | `crypto/rand.Read` failure branch unreachable without injection |

### 4. call (`call/*.go`)

**Covered well:** `New`, `Do` (success, timeout, retry, circuit breaker), `Batch` (success), `GetBreaker`, `Allow`, `Record`, `State`, `Token` (cache hit).

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| HIGH | `Token()` fetch error | Error return path in `token.go:52-55` untested |
| HIGH | `WithTokenSource` + error propagation through `Do` | `call.go:162-170` error wrapping untested |
| HIGH | `RemoveBreaker` | Completely untested (`breaker.go:141-143`) |
| HIGH | `backoff` exponential doubling | All tests use `attempt=0`, doubling loop never runs (`retry.go:94-96`) |
| MEDIUM | `Batch` partial failure | Only all-success path tested |
| MEDIUM | `Do` — pre-existing deadline skips context creation | `cancel == nil` path not isolated |
| MEDIUM | `WithHTTPClient` option | Never directly exercised |
| LOW | `Retrier.MaxAttempts = 0` | Direct construction (bypassing `WithRetry`) returns `(nil, nil)` |

### 5. config (`config/config.go`)

**Covered well:** All 6 field types, defaults, required/optional, parse errors, all 4 validators.

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| MEDIUM | Unsupported field type | `reflect.Struct`, `reflect.Map` etc. hit `default:` panic — untested |
| MEDIUM | Malformed tag values | `min=abc`, `max=xyz`, invalid regex in `pattern=` — untested |
| LOW | `validate:"oneof"` on non-string types | `fmt.Sprintf` comparison on int untested |
| LOW | `[]string` with empty elements | `","` producing empty strings after split untested |

### 6. lifecycle (`lifecycle/lifecycle.go`)

**Covered well:** Single/multi component, error propagation, context cancellation, signal handling, registry integration.

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| MEDIUM | `RunComponents` | Never directly tested (only `Run` is called) |
| MEDIUM | `Run` with zero components | Behavior unverified |
| LOW | `registry.RestartRequested()` → `syscall.Exec` | Restart branch has zero coverage |

### 7. logz (`logz/logz.go`)

**Covered well:** `New`, `parseLevel`, trace context injection, `WithAttrs`, `WithGroup`.

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| MEDIUM | Nested groups (depth > 1) | `logger.WithGroup("a").WithGroup("b")` recursive wrapping untested |
| LOW | `WithAttrs` slot-grow in deep groups | Group depth exceeding pre-allocated slots untested |

### 8. metrics (`metrics/metrics.go`)

**Covered well:** `New`, `RecordRequest`, `Counter`, `Histogram`, cardinality overflow, `pairsToCombo`.

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| MEDIUM | `CounterVec.Add` / `HistogramVec.Observe` cardinality limit | Only `RecordRequest` overflow tested |
| MEDIUM | Odd-length pairs in `pairsToCombo` | Trailing key silently dropped — untested |
| LOW | `DurationBuckets` / `ContentBuckets` values | Never asserted |

### 9. otel (`otel/otel.go`)

**Covered well:** `Init` (insecure, TLS, custom sampler), `AlwaysSample`, `RatioSample`, `DetachContext`.

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| MEDIUM | `Init` metric exporter failure | Partial-shutdown closure untested |
| MEDIUM | `Init` resource creation failure | Fallback to `resource.Default()` untested |
| LOW | `RatioSample` boundary values 0.0 and 1.0 | Only 0.5 tested |

### 10. secval (`secval/secval.go`)

**Covered well:** `ValidateJSON`, `RedactSecrets`, `SafeFilename`, `SafeFilenameURL`, `ValidateIdentifier`.

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| MEDIUM | Array nesting depth at boundary | Pure array `[[[...]]]` at depth 20/21 untested |
| MEDIUM | Non-ASCII evasion of dangerous keys | `"\x00__proto__"` normalizing to dangerous key untested |
| LOW | `ValidateIdentifier` max length (64 chars) | Boundary test missing |
| LOW | `SafeFilename` DEL char (127) | Not tested |

### 11. work (`work/work.go`)

**Covered well:** `Map`, `All`, `Race`, `Stream`, `Errors`.

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| MEDIUM | `Stream` context cancellation mid-flight | `goto drain` path untested |
| MEDIUM | `Map` pre-cancelled context | All items hitting `ctx.Done()` immediately untested |
| LOW | `Race` with single task | Degenerate case untested |
| LOW | `Workers(-5)` clamping | Only `Workers(0)` tested |

### 12. flagz (`flagz/flagz.go`, `flagz/sources.go`)

**Covered well:** `New`, `Enabled`, `EnabledFor`, `Variant`, `FromEnv`, `FromMap`, `FromJSON`, `Multi`.

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| MEDIUM | `addSpanEvent` recording path | OTel recording branch never reached |
| MEDIUM | `FromEnv` multi-segment names | `FLAG_MY_LONG_FEATURE` → `"my-long-feature"` untested |
| MEDIUM | `Multi` with zero sources | Empty iteration untested |
| LOW | `Enabled("TRUE")` / `Enabled("1")` | Case-sensitive comparison behavior undocumented/untested |
| LOW | `FromJSON` empty object `{}` | Untested |

### 13. health (`health/health.go`, `health/handler.go`)

**Covered well:** `CheckFunc`, `All` (parallel execution, errors, context cancellation), `Handler`.

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| MEDIUM | `Handler` Content-Type header | Never asserted |
| MEDIUM | `All` result sort order | Code sorts by name but no test asserts order |
| LOW | Empty checks map | `All(map[string]Check{})` and `Handler(map[string]Check{})` untested |

### 14. cache (`cache/cache.go`)

**Covered well:** `Get`, `Set`, `Delete`, `Len`, `Prune`, `MaxSize`, `TTL`.

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| MEDIUM | `Name` option | Completely untested |
| MEDIUM | `MaxSize(0)` clamping | `max < 1` guard untested |
| MEDIUM | Concurrent access | No race-detector tests |
| LOW | `Prune` with no TTL | Early return path untested |
| LOW | `Set` TTL refresh on overwrite | Not verified |

### 15. seal (`seal/seal.go`)

**Covered well:** `Encrypt`/`Decrypt` roundtrip, `Sign`/`Verify`, `NewToken`/`ValidateToken`.

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| HIGH | `Decrypt` malformed base64 fields | 4 independent decode branches untested |
| MEDIUM | `ValidateToken` no dot separator | `splitToken` returning nil untested |
| MEDIUM | `ValidateToken` valid signature but invalid JSON body | Unmarshal failure untested |
| LOW | Empty plaintext roundtrip | `Encrypt([]byte{})` untested |

### 16. tick (`tick/tick.go`)

**Covered well:** `Every`, `Immediate`, `OnError(Stop)`, `OnError(Skip)`, context cancellation.

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| HIGH | `Jitter` option | Entire jitter branch untested |
| MEDIUM | `Immediate` + `OnError(Stop)` | First-call failure before ticker starts untested |
| LOW | `Label` option | Never exercised |

### 17. webhook (`webhook/webhook.go`)

**Covered well:** `Send` (success, retry), `Status` (found), `VerifyPayload` (valid, bad sig).

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| MEDIUM | `Status` unknown ID | `!ok` branch untested |
| MEDIUM | `VerifyPayload` missing headers | Neither header-absent case tested |
| MEDIUM | `Send` network error | Transport failure (not HTTP status) untested |
| LOW | `Send` marshal failure | Un-marshalable payload untested |

### 18. deploy (`deploy/deploy.go`)

**Covered well:** `Discover`, `Environment`, `Spec`, `Endpoints`, `Dependencies`, `Health`, `FlagSource`, `TLS`.

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| HIGH | `RunHook` | All code paths completely untested |
| MEDIUM | `TLS` partial (cert only, no key) | Second `os.Stat` failure untested |
| MEDIUM | `TLS` with `ca.pem` | CA field never populated |
| MEDIUM | Malformed JSON paths | `Spec`, `Meta`, `FlagSource`, `Endpoints`, `Dependencies` unmarshal errors untested |

### 19. xyops (`xyops/xyops.go`)

**Covered well:** `New`, `Ping`, `RunEvent`, `GetJobStatus`, `FireWebhook`, `ListEvents`, `ListActiveAlerts`, `Raw`, `Run`.

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| **CRITICAL** | `CancelJob` | Completely untested |
| **CRITICAL** | `SearchJobs` | Completely untested |
| **CRITICAL** | `GetEvent` | Completely untested |
| **CRITICAL** | `AckAlert` | Completely untested |
| HIGH | API error responses | No 4xx/5xx error-path tests for any method |
| MEDIUM | Monitoring push error | 5xx from push endpoint untested |

### 20. xyopsworker (`xyopsworker/worker.go`)

**Covered well:** `New`, `Handle`, `HasHandler`, `Dispatch`, `Job` nil callbacks.

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| MEDIUM | `Run` | Never called in any test |
| MEDIUM | `Dispatch` handler returning error | Error propagation untested |
| LOW | `Job.Progress/Log/SetOutput` with live callbacks | Only nil-callback guard tested |

### 21. grpckit (`grpckit/*.go`)

**Covered well:** `RegisterHealth`, `Check` (SERVING/NOT_SERVING), `UnaryLogging`, `UnaryRecovery`, `StreamLogging`, `StreamRecovery`, `UnaryMetrics`, `StreamMetrics`, `UnaryTracing`, `StreamTracing`.

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| **CRITICAL** | `UnaryTracing` error path | Span error status on handler error untested (line 271) |
| **CRITICAL** | `StreamTracing` error path | Same gap for streams (line 303) |
| HIGH | `UnaryMetrics` / `StreamMetrics` error | Status code tagging on error untested |
| HIGH | `grpcCodeFromError` | All 3 branches untested in isolation |
| MEDIUM | `StreamLogging` error path | Error attr append never exercised |
| MEDIUM | `StreamTracing` W3C propagation | No equivalent of unary propagation test |
| LOW | `Watch` RPC | Unimplemented behavior undocumented by test |

### 22. registry (`registry/registry.go`)

**Covered well:** `Init`, `Shutdown`, `Status`, `Handle`, `Port`, `AssertActive`, `RestartRequested`, `StopRequested`, `InitCLI`, `ShutdownCLI`, `Progress`, `RunCommandPoll`.

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| MEDIUM | `RunHeartbeat` | Never called in any test |
| MEDIUM | `parseFlags` actual flag parsing | Only `reg.Flags != nil` checked, not parsed values |
| MEDIUM | `shouldPreservePIDFile` 24-hour branch | Preservation logic untested |
| LOW | `redactArgs` with sensitive flags | PID file redaction untested |

### 23. internal/otelutil (`internal/otelutil/histogram.go`)

**NO TEST FILE EXISTS.**

| Priority | Function | Gap |
|----------|----------|-----|
| HIGH | `LazyHistogram` | Entire function untested — happy path, sync.Once, error path, concurrent access |

### 24. testkit (`testkit/*.go`)

**Covered well:** `NewLogger`, `SetEnv`, `GetFreePort`, `NewHTTPServer`, `Respond`, `Sequence`.

**Gaps:**

| Priority | Function | Gap |
|----------|----------|-----|
| MEDIUM | `Sequence` repeat-last overflow | Third request on 2-handler sequence untested |
| MEDIUM | `Respond` Content-Type header | Never asserted |
| LOW | `NewLogger` output content | Only no-panic verified |

---

## Patch-Ready Diffs

### PATCH 1: `errors/errors_test.go` — Nil input guards and immutability

```diff
--- a/errors/errors_test.go
+++ b/errors/errors_test.go
@@ -end of file
+
+func TestFromErrorNil(t *testing.T) {
+	got := FromError(nil)
+	if got != nil {
+		t.Errorf("FromError(nil) = %v, want nil", got)
+	}
+}
+
+func TestWriteProblemNilError(t *testing.T) {
+	rec := httptest.NewRecorder()
+	req := httptest.NewRequest("GET", "/test", nil)
+	WriteProblem(rec, req, nil, "")
+	if rec.Code != http.StatusOK {
+		t.Errorf("WriteProblem(nil) status = %d, want 200", rec.Code)
+	}
+	if rec.Body.Len() != 0 {
+		t.Errorf("WriteProblem(nil) body = %q, want empty", rec.Body.String())
+	}
+}
+
+func TestProblemDetailNilRequest(t *testing.T) {
+	pd := InternalError("boom").ProblemDetail(nil)
+	if pd.Instance != "" {
+		t.Errorf("ProblemDetail(nil).Instance = %q, want empty", pd.Instance)
+	}
+	if pd.Status != http.StatusInternalServerError {
+		t.Errorf("ProblemDetail(nil).Status = %d, want 500", pd.Status)
+	}
+}
+
+func TestProblemDetailNilURL(t *testing.T) {
+	req := &http.Request{}
+	pd := InternalError("boom").ProblemDetail(req)
+	if pd.Instance != "" {
+		t.Errorf("ProblemDetail(nilURL).Instance = %q, want empty", pd.Instance)
+	}
+}
+
+func TestWithDetailImmutability(t *testing.T) {
+	original := ValidationError("test")
+	_ = original.WithDetail("key", "val")
+	if original.Details != nil {
+		t.Error("WithDetail mutated the receiver; Details should still be nil")
+	}
+}
+
+func TestWithDetailsImmutability(t *testing.T) {
+	original := ValidationError("test").WithDetail("existing", "val")
+	details := map[string]any{"new": "val"}
+	copy := original.WithDetails(details)
+	details["injected"] = "attack"
+	if _, found := copy.Details["injected"]; found {
+		t.Error("WithDetails did not deep-copy the input map")
+	}
+	if _, found := original.Details["new"]; found {
+		t.Error("WithDetails mutated the original receiver's Details")
+	}
+}
+
+func TestWithCauseNilClearsCause(t *testing.T) {
+	cause := fmt.Errorf("root cause")
+	err := TimeoutError("t").WithCause(cause).WithCause(nil)
+	if err.Unwrap() != nil {
+		t.Error("WithCause(nil) should clear the cause")
+	}
+}
+
+func TestMarshalJSONOmitsEmptyInstance(t *testing.T) {
+	pd := ProblemDetail{
+		Type:   "about:blank",
+		Title:  "test",
+		Status: 400,
+		Detail: "detail",
+	}
+	data, err := json.Marshal(pd)
+	if err != nil {
+		t.Fatalf("MarshalJSON: %v", err)
+	}
+	var m map[string]any
+	if err := json.Unmarshal(data, &m); err != nil {
+		t.Fatalf("Unmarshal: %v", err)
+	}
+	if _, ok := m["instance"]; ok {
+		t.Error("MarshalJSON should omit 'instance' when empty")
+	}
+}
+
+func TestProblemDetailTypeURIs(t *testing.T) {
+	tests := []struct {
+		factory func(string) *ServiceError
+		code    int
+		wantURI string
+	}{
+		{UnauthorizedError, 401, "unauthorized"},
+		{ForbiddenError, 403, "forbidden"},
+		{TimeoutError, 504, "timeout"},
+		{PayloadTooLargeError, 413, "payload-too-large"},
+		{RateLimitError, 429, "rate-limit"},
+		{DependencyError, 503, "dependency"},
+		{InternalError, 500, "internal"},
+	}
+	for _, tt := range tests {
+		err := tt.factory("msg")
+		req := httptest.NewRequest("GET", "/test", nil)
+		pd := err.ProblemDetail(req)
+		if pd.Type != tt.wantURI {
+			t.Errorf("HTTPCode %d: Type = %q, want %q", tt.code, pd.Type, tt.wantURI)
+		}
+	}
+}
+
+func TestErrorfWithMultipleFactories(t *testing.T) {
+	err := Errorf(InternalError, "server error: %d", 42)
+	if err.HTTPCode != http.StatusInternalServerError {
+		t.Errorf("Errorf(InternalError) HTTPCode = %d, want 500", err.HTTPCode)
+	}
+	if err.Message != "server error: 42" {
+		t.Errorf("Errorf message = %q", err.Message)
+	}
+}
```

### PATCH 2: `internal/otelutil/histogram_test.go` — New test file

```diff
--- /dev/null
+++ b/internal/otelutil/histogram_test.go
@@ -0,0 +1,49 @@
+package otelutil
+
+import (
+	"sync"
+	"testing"
+
+	"go.opentelemetry.io/otel/metric"
+)
+
+func TestLazyHistogramReturnsNonNil(t *testing.T) {
+	getter := LazyHistogram("test.meter", "test.histogram",
+		metric.WithUnit("ms"),
+	)
+	h := getter()
+	if h == nil {
+		t.Fatal("LazyHistogram returned nil histogram")
+	}
+}
+
+func TestLazyHistogramSyncOnce(t *testing.T) {
+	getter := LazyHistogram("test.meter", "test.once")
+	h1 := getter()
+	h2 := getter()
+	if h1 != h2 {
+		t.Error("LazyHistogram should return the same instance on repeated calls")
+	}
+}
+
+func TestLazyHistogramConcurrentInit(t *testing.T) {
+	getter := LazyHistogram("test.meter", "test.concurrent")
+	const goroutines = 50
+	results := make([]metric.Float64Histogram, goroutines)
+	var wg sync.WaitGroup
+	wg.Add(goroutines)
+	for i := range goroutines {
+		go func(idx int) {
+			defer wg.Done()
+			results[idx] = getter()
+		}(i)
+	}
+	wg.Wait()
+
+	first := results[0]
+	for i, h := range results {
+		if h != first {
+			t.Errorf("goroutine %d got different histogram instance", i)
+		}
+	}
+}
```

### PATCH 3: `guard/timeout_test.go` — Timeout edge cases

```diff
--- a/guard/timeout_test.go
+++ b/guard/timeout_test.go
@@ -end of file
+
+func TestTimeoutWriterUnwrap(t *testing.T) {
+	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		// http.NewResponseController calls Unwrap internally.
+		rc := http.NewResponseController(w)
+		// Flush exercises Unwrap → underlying Flusher.
+		if err := rc.Flush(); err != nil {
+			// httptest.ResponseRecorder supports Flush, so this should succeed.
+			t.Errorf("Flush via Unwrap failed: %v", err)
+		}
+		w.WriteHeader(http.StatusOK)
+	})
+
+	handler := guard.Timeout(5 * time.Second)(inner)
+	req := httptest.NewRequest("GET", "/", nil)
+	rec := httptest.NewRecorder()
+	handler.ServeHTTP(rec, req)
+
+	if rec.Code != http.StatusOK {
+		t.Errorf("status = %d, want 200", rec.Code)
+	}
+}
+
+func TestTimeoutPanicPropagation(t *testing.T) {
+	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		panic("handler blew up")
+	})
+
+	handler := guard.Timeout(5 * time.Second)(inner)
+	req := httptest.NewRequest("GET", "/", nil)
+	rec := httptest.NewRecorder()
+
+	defer func() {
+		r := recover()
+		if r == nil {
+			t.Fatal("expected panic to propagate through Timeout middleware")
+		}
+		if r != "handler blew up" {
+			t.Errorf("panic value = %v, want %q", r, "handler blew up")
+		}
+	}()
+
+	handler.ServeHTTP(rec, req)
+}
```

### PATCH 4: `guard/cors_test.go` — Vary header and credentials

```diff
--- a/guard/cors_test.go
+++ b/guard/cors_test.go
@@ -end of file
+
+func TestCORSVaryHeaderSet(t *testing.T) {
+	cfg := guard.CORSConfig{
+		AllowOrigins: []string{"https://example.com"},
+	}
+	handler := guard.CORS(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		w.WriteHeader(200)
+	}))
+
+	req := httptest.NewRequest("GET", "/", nil)
+	req.Header.Set("Origin", "https://example.com")
+	rec := httptest.NewRecorder()
+	handler.ServeHTTP(rec, req)
+
+	if got := rec.Header().Get("Vary"); got != "Origin" {
+		t.Errorf("Vary = %q, want %q", got, "Origin")
+	}
+}
+
+func TestCORSCredentialsWithSpecificOrigin(t *testing.T) {
+	cfg := guard.CORSConfig{
+		AllowOrigins:     []string{"https://example.com"},
+		AllowCredentials: true,
+	}
+	handler := guard.CORS(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		w.WriteHeader(200)
+	}))
+
+	req := httptest.NewRequest("GET", "/", nil)
+	req.Header.Set("Origin", "https://example.com")
+	rec := httptest.NewRecorder()
+	handler.ServeHTTP(rec, req)
+
+	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
+		t.Errorf("Allow-Credentials = %q, want %q", got, "true")
+	}
+}
```

### PATCH 5: `guard/ratelimit_test.go` — Retry-After header

```diff
--- a/guard/ratelimit_test.go
+++ b/guard/ratelimit_test.go
@@ -end of file
+
+func TestRateLimitRetryAfterHeader(t *testing.T) {
+	cfg := guard.RateLimitConfig{
+		Limit:  1,
+		Window: time.Hour,
+	}
+	handler := guard.RateLimit(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		w.WriteHeader(200)
+	}))
+
+	req := httptest.NewRequest("GET", "/", nil)
+	req.RemoteAddr = "10.0.0.1:1234"
+
+	// First request succeeds.
+	rec := httptest.NewRecorder()
+	handler.ServeHTTP(rec, req)
+	if rec.Code != 200 {
+		t.Fatalf("first request: status = %d, want 200", rec.Code)
+	}
+
+	// Second request is rate-limited.
+	rec = httptest.NewRecorder()
+	handler.ServeHTTP(rec, req)
+	if rec.Code != http.StatusTooManyRequests {
+		t.Fatalf("second request: status = %d, want 429", rec.Code)
+	}
+
+	ra := rec.Header().Get("Retry-After")
+	if ra == "" {
+		t.Error("429 response missing Retry-After header")
+	}
+}
```

### PATCH 6: `httpkit/httpkit_test.go` — Tracing 5xx span error and recovery non-string panic

```diff
--- a/httpkit/httpkit_test.go
+++ b/httpkit/httpkit_test.go
@@ -end of file
+
+func TestTracing_5xxSetsSpanError(t *testing.T) {
+	exporter := tracetest.NewInMemoryExporter()
+	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
+	otelapi.SetTracerProvider(tp)
+	defer otelapi.SetTracerProvider(sdktrace.NewTracerProvider())
+
+	handler := Tracing(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		w.WriteHeader(http.StatusInternalServerError)
+	}))
+
+	req := httptest.NewRequest("GET", "/fail", nil)
+	rec := httptest.NewRecorder()
+	handler.ServeHTTP(rec, req)
+	tp.ForceFlush(context.Background())
+
+	spans := exporter.GetSpans()
+	if len(spans) == 0 {
+		t.Fatal("expected at least one span")
+	}
+	span := spans[0]
+	// otelcodes.Error == 2
+	if span.Status.Code != 2 {
+		t.Errorf("span status code = %d, want Error (2)", span.Status.Code)
+	}
+}
+
+func TestRecovery_NonStringPanic(t *testing.T) {
+	handler := Recovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		panic(42)
+	}))
+
+	req := httptest.NewRequest("GET", "/", nil)
+	rec := httptest.NewRecorder()
+	handler.ServeHTTP(rec, req)
+
+	if rec.Code != http.StatusInternalServerError {
+		t.Errorf("status = %d, want 500", rec.Code)
+	}
+}
+
+func TestRecovery_NoPanic(t *testing.T) {
+	handler := Recovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		w.WriteHeader(http.StatusCreated)
+	}))
+
+	req := httptest.NewRequest("GET", "/", nil)
+	rec := httptest.NewRecorder()
+	handler.ServeHTTP(rec, req)
+
+	if rec.Code != http.StatusCreated {
+		t.Errorf("status = %d, want 201", rec.Code)
+	}
+}
```

### PATCH 7: `call/token_test.go` — Token fetch error

```diff
--- a/call/token_test.go
+++ b/call/token_test.go
@@ -end of file
+
+func TestCachedTokenFetchError(t *testing.T) {
+	wantErr := fmt.Errorf("token service down")
+	ct := NewCachedToken(func(ctx context.Context) (string, time.Time, error) {
+		return "", time.Time{}, wantErr
+	})
+
+	_, err := ct.Token(context.Background())
+	if err == nil {
+		t.Fatal("expected error from Token()")
+	}
+	if !errors.Is(err, wantErr) {
+		t.Errorf("error = %v, want %v", err, wantErr)
+	}
+}
+
+func TestCachedTokenDefaultLeeway(t *testing.T) {
+	calls := 0
+	ct := NewCachedToken(func(ctx context.Context) (string, time.Time, error) {
+		calls++
+		return "tok", time.Now().Add(10 * time.Minute), nil
+	})
+
+	// First call fetches.
+	tok, err := ct.Token(context.Background())
+	if err != nil || tok != "tok" {
+		t.Fatalf("Token() = %q, %v", tok, err)
+	}
+
+	// Second call should be cached (default leeway is 5min, token expires in 10min).
+	tok2, _ := ct.Token(context.Background())
+	if tok2 != "tok" || calls != 1 {
+		t.Errorf("expected cache hit; calls = %d", calls)
+	}
+}
```

### PATCH 8: `call/breaker_test.go` — RemoveBreaker

```diff
--- a/call/breaker_test.go
+++ b/call/breaker_test.go
@@ -end of file
+
+func TestRemoveBreaker(t *testing.T) {
+	name := "test-remove-breaker"
+	cb1 := GetBreaker(name, 5, 30*time.Second)
+
+	// Record failures to change internal state.
+	for range 5 {
+		cb1.Record(false)
+	}
+	if cb1.State() != StateOpen {
+		t.Fatalf("expected StateOpen, got %v", cb1.State())
+	}
+
+	RemoveBreaker(name)
+
+	// After removal, GetBreaker should return a fresh instance.
+	cb2 := GetBreaker(name, 5, 30*time.Second)
+	if cb2.State() != StateClosed {
+		t.Errorf("new breaker state = %v, want StateClosed", cb2.State())
+	}
+}
```

### PATCH 9: `call/retry_test.go` — Backoff exponential doubling and all-5xx exhaustion

```diff
--- a/call/retry_test.go
+++ b/call/retry_test.go
@@ -end of file
+
+func TestRetrier_BackoffExponentialDoubling(t *testing.T) {
+	// Verify that backoff with attempt > 0 runs longer than attempt 0.
+	ctx := context.Background()
+
+	start0 := time.Now()
+	backoff(ctx, 0, 10*time.Millisecond)
+	dur0 := time.Since(start0)
+
+	start2 := time.Now()
+	backoff(ctx, 2, 10*time.Millisecond)
+	dur2 := time.Since(start2)
+
+	// attempt=2 should sleep at least 4x the base (10ms * 2^2 = 40ms).
+	if dur2 < 2*dur0 {
+		t.Errorf("attempt=2 duration (%v) not meaningfully longer than attempt=0 (%v)", dur2, dur0)
+	}
+}
+
+func TestRetrier_All5xxExhaustion(t *testing.T) {
+	attempts := 0
+	r := Retrier{
+		MaxAttempts: 3,
+		BaseDelay:   time.Millisecond,
+	}
+	resp, err := r.Do(context.Background(), func() (*http.Response, error) {
+		attempts++
+		return &http.Response{
+			StatusCode: 503,
+			Body:       http.NoBody,
+		}, nil
+	})
+
+	if err != nil {
+		t.Fatalf("expected nil error on 5xx exhaustion, got %v", err)
+	}
+	if resp == nil || resp.StatusCode != 503 {
+		t.Errorf("expected last 503 response; got %v", resp)
+	}
+	if attempts != 3 {
+		t.Errorf("attempts = %d, want 3", attempts)
+	}
+}
```

### PATCH 10: `xyops/xyops_test.go` — CancelJob, SearchJobs, GetEvent, AckAlert

```diff
--- a/xyops/xyops_test.go
+++ b/xyops/xyops_test.go
@@ mux func - add new handlers to the mock mux:
+	m.HandleFunc("POST /api/jobs/{jobID}/cancel", func(w http.ResponseWriter, r *http.Request) {
+		w.Header().Set("Content-Type", "application/json")
+		w.WriteHeader(200)
+		w.Write([]byte(`{"status":"cancelled"}`))
+	})
+
+	m.HandleFunc("GET /api/jobs", func(w http.ResponseWriter, r *http.Request) {
+		w.Header().Set("Content-Type", "application/json")
+		w.WriteHeader(200)
+		w.Write([]byte(`{"jobs":[{"id":"job-1","status":"running"}]}`))
+	})
+
+	m.HandleFunc("GET /api/events/{eventID}", func(w http.ResponseWriter, r *http.Request) {
+		w.Header().Set("Content-Type", "application/json")
+		w.WriteHeader(200)
+		w.Write([]byte(`{"event_id":"evt-1","name":"deploy"}`))
+	})
+
+	m.HandleFunc("POST /api/alerts/{alertID}/ack", func(w http.ResponseWriter, r *http.Request) {
+		w.Header().Set("Content-Type", "application/json")
+		w.WriteHeader(200)
+		w.Write([]byte(`{"status":"acknowledged"}`))
+	})

@@ -end of file
+
+func TestCancelJob(t *testing.T) {
+	srv := testkit.NewHTTPServer(t, mux())
+	c := xyops.New(xyops.Config{BaseURL: srv.URL, APIKey: "test-key"})
+
+	err := c.CancelJob(context.Background(), "job-42")
+	if err != nil {
+		t.Fatalf("CancelJob: %v", err)
+	}
+}
+
+func TestSearchJobs(t *testing.T) {
+	srv := testkit.NewHTTPServer(t, mux())
+	c := xyops.New(xyops.Config{BaseURL: srv.URL, APIKey: "test-key"})
+
+	result, err := c.SearchJobs(context.Background(), "deploy")
+	if err != nil {
+		t.Fatalf("SearchJobs: %v", err)
+	}
+	if result == nil {
+		t.Fatal("SearchJobs returned nil")
+	}
+}
+
+func TestGetEvent(t *testing.T) {
+	srv := testkit.NewHTTPServer(t, mux())
+	c := xyops.New(xyops.Config{BaseURL: srv.URL, APIKey: "test-key"})
+
+	result, err := c.GetEvent(context.Background(), "evt-1")
+	if err != nil {
+		t.Fatalf("GetEvent: %v", err)
+	}
+	if result == nil {
+		t.Fatal("GetEvent returned nil")
+	}
+}
+
+func TestAckAlert(t *testing.T) {
+	srv := testkit.NewHTTPServer(t, mux())
+	c := xyops.New(xyops.Config{BaseURL: srv.URL, APIKey: "test-key"})
+
+	err := c.AckAlert(context.Background(), "alert-1")
+	if err != nil {
+		t.Fatalf("AckAlert: %v", err)
+	}
+}
+
+func TestXyopsAPIError(t *testing.T) {
+	errMux := http.NewServeMux()
+	errMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
+		w.WriteHeader(http.StatusInternalServerError)
+		w.Write([]byte(`{"error":"server error"}`))
+	})
+	srv := testkit.NewHTTPServer(t, errMux)
+	c := xyops.New(xyops.Config{BaseURL: srv.URL, APIKey: "test-key"})
+
+	err := c.Ping(context.Background())
+	if err == nil {
+		t.Fatal("expected error from 500 response")
+	}
+}
```

### PATCH 11: `tick/tick_test.go` — Jitter option

```diff
--- a/tick/tick_test.go
+++ b/tick/tick_test.go
@@ -end of file
+
+func TestEveryWithJitter(t *testing.T) {
+	var count atomic.Int32
+	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
+	defer cancel()
+
+	tick.Every(ctx, 50*time.Millisecond, func(ctx context.Context) error {
+		count.Add(1)
+		return nil
+	}, tick.Jitter(20*time.Millisecond))
+
+	got := count.Load()
+	if got < 2 {
+		t.Errorf("tick count = %d, want >= 2 (jitter should not prevent execution)", got)
+	}
+}
+
+func TestEveryImmediateWithOnErrorStop(t *testing.T) {
+	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
+	defer cancel()
+
+	var count atomic.Int32
+	tick.Every(ctx, 100*time.Millisecond, func(ctx context.Context) error {
+		count.Add(1)
+		return fmt.Errorf("fail on first call")
+	}, tick.Immediate(), tick.OnError(tick.Stop))
+
+	if got := count.Load(); got != 1 {
+		t.Errorf("count = %d, want 1 (Immediate + Stop should exit after first error)", got)
+	}
+}
```

### PATCH 12: `seal/seal_test.go` — Malformed base64 and token edge cases

```diff
--- a/seal/seal_test.go
+++ b/seal/seal_test.go
@@ -end of file
+
+func TestDecryptMalformedBase64Fields(t *testing.T) {
+	plaintext := []byte("test data")
+	pass := "test-passphrase-32chars-minimum!"
+	env, err := seal.Encrypt(plaintext, pass)
+	if err != nil {
+		t.Fatalf("Encrypt: %v", err)
+	}
+
+	fields := []struct {
+		name string
+		set  func(*seal.Envelope)
+	}{
+		{"Salt", func(e *seal.Envelope) { e.Salt = "!!!invalid-base64!!!" }},
+		{"IV", func(e *seal.Envelope) { e.IV = "!!!invalid-base64!!!" }},
+		{"Tag", func(e *seal.Envelope) { e.Tag = "!!!invalid-base64!!!" }},
+		{"CT", func(e *seal.Envelope) { e.CT = "!!!invalid-base64!!!" }},
+	}
+
+	for _, f := range fields {
+		t.Run(f.name, func(t *testing.T) {
+			e := env // copy
+			f.set(&e)
+			_, err := seal.Decrypt(e, pass)
+			if err == nil {
+				t.Errorf("Decrypt with malformed %s should return error", f.name)
+			}
+		})
+	}
+}
+
+func TestDecryptCorruptedCiphertext(t *testing.T) {
+	plaintext := []byte("test data")
+	pass := "test-passphrase-32chars-minimum!"
+	env, err := seal.Encrypt(plaintext, pass)
+	if err != nil {
+		t.Fatalf("Encrypt: %v", err)
+	}
+
+	// Flip a byte in the ciphertext.
+	ct := []byte(env.CT)
+	if len(ct) > 2 {
+		ct[1] ^= 0xFF
+	}
+	env.CT = string(ct)
+
+	_, err = seal.Decrypt(env, pass)
+	if err == nil {
+		t.Error("Decrypt with corrupted CT should return error")
+	}
+}
+
+func TestValidateTokenNoSeparator(t *testing.T) {
+	_, err := seal.ValidateToken("notokenatall", "secret")
+	if err == nil {
+		t.Error("ValidateToken with no dot separator should return error")
+	}
+}
+
+func TestValidateTokenEmptyString(t *testing.T) {
+	_, err := seal.ValidateToken("", "secret")
+	if err == nil {
+		t.Error("ValidateToken with empty string should return error")
+	}
+}
+
+func TestEncryptDecryptEmptyPlaintext(t *testing.T) {
+	pass := "test-passphrase-32chars-minimum!"
+	env, err := seal.Encrypt([]byte{}, pass)
+	if err != nil {
+		t.Fatalf("Encrypt empty: %v", err)
+	}
+	got, err := seal.Decrypt(env, pass)
+	if err != nil {
+		t.Fatalf("Decrypt empty: %v", err)
+	}
+	if len(got) != 0 {
+		t.Errorf("got %d bytes, want 0", len(got))
+	}
+}
```

### PATCH 13: `webhook/webhook_test.go` — Status miss and missing headers

```diff
--- a/webhook/webhook_test.go
+++ b/webhook/webhook_test.go
@@ -end of file
+
+func TestStatusUnknownID(t *testing.T) {
+	sender := webhook.NewSender(webhook.SenderConfig{
+		Secret: "test-secret",
+	})
+	_, found := sender.Status("nonexistent-id")
+	if found {
+		t.Error("Status should return false for unknown delivery ID")
+	}
+}
+
+func TestVerifyPayloadMissingSignatureHeader(t *testing.T) {
+	req := httptest.NewRequest("POST", "/hook", strings.NewReader(`{"ok":true}`))
+	req.Header.Set("X-Webhook-Timestamp", "1234567890")
+	// No X-Webhook-Signature header.
+
+	err := webhook.VerifyPayload(req, []byte(`{"ok":true}`), "secret")
+	if err == nil {
+		t.Error("VerifyPayload should fail when signature header is missing")
+	}
+}
+
+func TestVerifyPayloadMissingTimestampHeader(t *testing.T) {
+	req := httptest.NewRequest("POST", "/hook", strings.NewReader(`{"ok":true}`))
+	req.Header.Set("X-Webhook-Signature", "sha256=abc123")
+	// No X-Webhook-Timestamp header.
+
+	err := webhook.VerifyPayload(req, []byte(`{"ok":true}`), "secret")
+	if err == nil {
+		t.Error("VerifyPayload should fail when timestamp header is missing")
+	}
+}
```

### PATCH 14: `cache/cache_test.go` — Name option, MaxSize clamping, Prune no-TTL

```diff
--- a/cache/cache_test.go
+++ b/cache/cache_test.go
@@ -end of file
+
+func TestNameOption(t *testing.T) {
+	// Name option should not panic.
+	c := cache.New[string, string](cache.MaxSize(5), cache.Name("my-cache"))
+	c.Set("k", "v")
+	if got, ok := c.Get("k"); !ok || got != "v" {
+		t.Errorf("Get = (%q, %v), want (\"v\", true)", got, ok)
+	}
+}
+
+func TestMaxSizeClampedToOne(t *testing.T) {
+	c := cache.New[string, int](cache.MaxSize(0))
+	c.Set("a", 1)
+	c.Set("b", 2) // should evict "a" since maxSize is clamped to 1
+	if c.Len() > 1 {
+		t.Errorf("Len = %d, want <= 1 (maxSize clamped to 1)", c.Len())
+	}
+}
+
+func TestPruneWithoutTTL(t *testing.T) {
+	c := cache.New[string, string](cache.MaxSize(10))
+	c.Set("k", "v")
+	pruned := c.Prune()
+	if pruned != 0 {
+		t.Errorf("Prune without TTL removed %d items, want 0", pruned)
+	}
+	if c.Len() != 1 {
+		t.Errorf("Len after Prune = %d, want 1", c.Len())
+	}
+}
+
+func TestDeleteNonExistentKey(t *testing.T) {
+	c := cache.New[string, string](cache.MaxSize(10))
+	c.Delete("nonexistent") // should not panic
+	if c.Len() != 0 {
+		t.Errorf("Len = %d, want 0", c.Len())
+	}
+}
```

### PATCH 15: `work/work_test.go` — Pre-cancelled context and Stream cancellation

```diff
--- a/work/work_test.go
+++ b/work/work_test.go
@@ -end of file
+
+func TestMap_PreCancelledContext(t *testing.T) {
+	ctx, cancel := context.WithCancel(context.Background())
+	cancel() // cancel before calling Map
+
+	items := []int{1, 2, 3}
+	_, err := Map(ctx, items, func(ctx context.Context, n int) (string, error) {
+		return "", fmt.Errorf("should not be called")
+	})
+
+	if err == nil {
+		t.Fatal("Map with pre-cancelled context should return error")
+	}
+}
+
+func TestMap_AllFail(t *testing.T) {
+	items := []int{1, 2, 3}
+	_, err := Map(context.Background(), items, func(ctx context.Context, n int) (string, error) {
+		return "", fmt.Errorf("fail-%d", n)
+	})
+
+	if err == nil {
+		t.Fatal("Map where all items fail should return error")
+	}
+	var errs *Errors
+	if !errors.As(err, &errs) {
+		t.Fatalf("error type = %T, want *Errors", err)
+	}
+	if len(errs.Failures) != 3 {
+		t.Errorf("Failures count = %d, want 3", len(errs.Failures))
+	}
+}
+
+func TestRace_SingleTask(t *testing.T) {
+	result, err := Race(context.Background(), func(ctx context.Context) (string, error) {
+		return "only", nil
+	})
+	if err != nil {
+		t.Fatalf("Race single task: %v", err)
+	}
+	if result != "only" {
+		t.Errorf("result = %q, want %q", result, "only")
+	}
+}
+
+func TestWorkers_NegativeClamp(t *testing.T) {
+	items := []int{1, 2}
+	results, err := Map(context.Background(), items, func(ctx context.Context, n int) (int, error) {
+		return n * 2, nil
+	}, Workers(-5))
+
+	if err != nil {
+		t.Fatalf("Map with Workers(-5): %v", err)
+	}
+	sort.Ints(results)
+	if results[0] != 2 || results[1] != 4 {
+		t.Errorf("results = %v, want [2, 4]", results)
+	}
+}
```

### PATCH 16: `health/health_test.go` — Content-Type, empty checks, sort order

```diff
--- a/health/health_test.go
+++ b/health/health_test.go
@@ -end of file
+
+func TestHandler_ContentType(t *testing.T) {
+	checks := map[string]health.Check{
+		"db": func(ctx context.Context) error { return nil },
+	}
+	handler := health.Handler(checks)
+	req := httptest.NewRequest("GET", "/health", nil)
+	rec := httptest.NewRecorder()
+	handler.ServeHTTP(rec, req)
+
+	ct := rec.Header().Get("Content-Type")
+	if ct != "application/json" {
+		t.Errorf("Content-Type = %q, want application/json", ct)
+	}
+}
+
+func TestHandler_EmptyChecks(t *testing.T) {
+	handler := health.Handler(map[string]health.Check{})
+	req := httptest.NewRequest("GET", "/health", nil)
+	rec := httptest.NewRecorder()
+	handler.ServeHTTP(rec, req)
+
+	if rec.Code != http.StatusOK {
+		t.Errorf("status = %d, want 200", rec.Code)
+	}
+}
+
+func TestAll_EmptyChecks(t *testing.T) {
+	results, err := health.All(map[string]health.Check{})(context.Background())
+	if err != nil {
+		t.Fatalf("All(empty): %v", err)
+	}
+	if len(results) != 0 {
+		t.Errorf("results = %d, want 0", len(results))
+	}
+}
+
+func TestAll_ResultOrder(t *testing.T) {
+	checks := map[string]health.Check{
+		"zulu":  func(ctx context.Context) error { return nil },
+		"alpha": func(ctx context.Context) error { return nil },
+		"mike":  func(ctx context.Context) error { return nil },
+	}
+	results, _ := health.All(checks)(context.Background())
+
+	for i := 1; i < len(results); i++ {
+		if results[i].Name < results[i-1].Name {
+			t.Errorf("results not sorted: %q comes after %q", results[i].Name, results[i-1].Name)
+		}
+	}
+}
```

### PATCH 17: `grpckit/interceptors_test.go` — Error paths for tracing and metrics

```diff
--- a/grpckit/interceptors_test.go
+++ b/grpckit/interceptors_test.go
@@ -end of file
+
+func TestUnaryTracing_ErrorSetsSpanStatus(t *testing.T) {
+	exporter := tracetest.NewInMemoryExporter()
+	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
+	otelapi.SetTracerProvider(tp)
+	defer otelapi.SetTracerProvider(sdktrace.NewTracerProvider())
+
+	handler := func(ctx context.Context, req any) (any, error) {
+		return nil, status.Error(codes.NotFound, "not found")
+	}
+
+	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Missing"}
+	_, err := grpckit.UnaryTracing()(context.Background(), nil, info, handler)
+	if err == nil {
+		t.Fatal("expected error")
+	}
+	tp.ForceFlush(context.Background())
+
+	spans := exporter.GetSpans()
+	if len(spans) == 0 {
+		t.Fatal("expected a span")
+	}
+	// otelcodes.Error == 2
+	if spans[0].Status.Code != 2 {
+		t.Errorf("span status = %d, want Error (2)", spans[0].Status.Code)
+	}
+}
+
+func TestUnaryRecovery_NoPanic(t *testing.T) {
+	handler := func(ctx context.Context, req any) (any, error) {
+		return "ok", nil
+	}
+	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Ok"}
+
+	resp, err := grpckit.UnaryRecovery()(context.Background(), nil, info, handler)
+	if err != nil {
+		t.Fatalf("unexpected error: %v", err)
+	}
+	if resp != "ok" {
+		t.Errorf("response = %v, want %q", resp, "ok")
+	}
+}
+
+func TestStreamLogging_ErrorPath(t *testing.T) {
+	var buf bytes.Buffer
+	logger := slog.New(slog.NewJSONHandler(&buf, nil))
+	handler := func(srv any, ss grpc.ServerStream) error {
+		return status.Error(codes.Internal, "broken")
+	}
+	info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamErr"}
+
+	err := grpckit.StreamLogging(logger)(nil, &fakeServerStream{ctx: context.Background()}, info, handler)
+	if err == nil {
+		t.Fatal("expected error")
+	}
+	if !strings.Contains(buf.String(), "error") {
+		t.Error("error field not logged for failed stream RPC")
+	}
+}
```

### PATCH 18: `deploy/deploy_test.go` — Malformed JSON and partial TLS

```diff
--- a/deploy/deploy_test.go
+++ b/deploy/deploy_test.go
@@ -end of file
+
+func TestSpecMalformedJSON(t *testing.T) {
+	dir := t.TempDir()
+	if err := os.WriteFile(filepath.Join(dir, "deploy.json"), []byte(`{not valid json`), 0644); err != nil {
+		t.Fatal(err)
+	}
+	d := deploy.Discover(dir)
+	spec := d.Spec()
+	if spec != "" {
+		t.Errorf("Spec with malformed JSON = %q, want empty", spec)
+	}
+}
+
+func TestTLSPartialCertOnly(t *testing.T) {
+	dir := t.TempDir()
+	deployJSON := `{"name":"svc","environment":"test"}`
+	if err := os.WriteFile(filepath.Join(dir, "deploy.json"), []byte(deployJSON), 0644); err != nil {
+		t.Fatal(err)
+	}
+	tlsDir := filepath.Join(dir, "tls")
+	if err := os.MkdirAll(tlsDir, 0755); err != nil {
+		t.Fatal(err)
+	}
+	// Only create cert, no key.
+	if err := os.WriteFile(filepath.Join(tlsDir, "cert.pem"), []byte("fake-cert"), 0644); err != nil {
+		t.Fatal(err)
+	}
+
+	d := deploy.Discover(dir)
+	tls := d.TLS()
+	if tls.Cert != "" || tls.Key != "" {
+		t.Error("TLS with only cert (no key) should return empty TLS config")
+	}
+}
```

### PATCH 19: `flagz/flagz_test.go` — Multi zero sources, FromEnv multi-segment

```diff
--- a/flagz/flagz_test.go
+++ b/flagz/flagz_test.go
@@ -end of file
+
+func TestMultiZeroSources(t *testing.T) {
+	src := flagz.Multi()
+	f := flagz.New(src)
+	if f.Enabled(context.Background(), "anything") {
+		t.Error("Multi() with no sources should return false for Enabled")
+	}
+}
+
+func TestFromEnvMultiSegmentName(t *testing.T) {
+	t.Setenv("MYAPP_MY_LONG_FEATURE", "true")
+	src := flagz.FromEnv("MYAPP")
+	f := flagz.New(src)
+	if !f.Enabled(context.Background(), "my-long-feature") {
+		t.Error("multi-segment env var MYAPP_MY_LONG_FEATURE should produce flag 'my-long-feature' = true")
+	}
+}
+
+func TestFromJSONEmptyObject(t *testing.T) {
+	dir := t.TempDir()
+	path := dir + "/empty.json"
+	os.WriteFile(path, []byte(`{}`), 0644)
+	src := flagz.FromJSON(path)
+	f := flagz.New(src)
+	if f.Enabled(context.Background(), "nope") {
+		t.Error("FromJSON({}) should return false for any flag")
+	}
+}
+
+func TestEnabledCaseSensitive(t *testing.T) {
+	src := flagz.FromMap(map[string]string{
+		"flag-upper": "TRUE",
+		"flag-one":   "1",
+		"flag-yes":   "yes",
+	})
+	f := flagz.New(src)
+	for _, name := range []string{"flag-upper", "flag-one", "flag-yes"} {
+		if f.Enabled(context.Background(), name) {
+			t.Errorf("Enabled(%q) = true, want false (only lowercase 'true' matches)", name)
+		}
+	}
+}
```

### PATCH 20: `xyopsworker/worker_test.go` — Run and Dispatch error

```diff
--- a/xyopsworker/worker_test.go
+++ b/xyopsworker/worker_test.go
@@ -end of file
+
+func TestRunCancellation(t *testing.T) {
+	w := xyopsworker.New(xyopsworker.Config{ServiceName: "test-svc"})
+	ctx, cancel := context.WithCancel(context.Background())
+	cancel() // cancel immediately
+
+	err := w.Run(ctx)
+	if err != nil {
+		t.Errorf("Run after cancel = %v, want nil", err)
+	}
+}
+
+func TestDispatchHandlerError(t *testing.T) {
+	w := xyopsworker.New(xyopsworker.Config{ServiceName: "test-svc"})
+	w.Handle("fail-cmd", func(ctx context.Context, job xyopsworker.Job) error {
+		return fmt.Errorf("handler broke")
+	})
+
+	err := w.Dispatch(context.Background(), "fail-cmd", json.RawMessage(`{}`))
+	if err == nil {
+		t.Fatal("Dispatch should propagate handler error")
+	}
+	if !strings.Contains(err.Error(), "handler broke") {
+		t.Errorf("error = %v, want to contain 'handler broke'", err)
+	}
+}
```

---

## Prioritized Test Implementation Roadmap

### Tier 1 — Critical (ship-blocking observability/security gaps)

1. **xyops: 4 untested exported functions** (CancelJob, SearchJobs, GetEvent, AckAlert) — Patch 10
2. **grpckit: Tracing error span status** — Patch 17
3. **httpkit: Tracing 5xx span error** — Patch 6
4. **internal/otelutil: Entire LazyHistogram** — Patch 2
5. **errors: Nil input guards** (FromError(nil), WriteProblem(nil), ProblemDetail(nil)) — Patch 1

### Tier 2 — High (error-path gaps in core packages)

6. **call: Token fetch error propagation** — Patch 7
7. **call: RemoveBreaker** — Patch 8
8. **call: Backoff exponential doubling** — Patch 9
9. **seal: Malformed input decryption** — Patch 12
10. **tick: Jitter option** — Patch 11
11. **deploy: RunHook** (requires more design work; not included as patch)

### Tier 3 — Medium (edge cases and boundary conditions)

12. **guard: Timeout panic propagation, Unwrap** — Patch 3
13. **guard: CORS Vary header, RateLimit Retry-After** — Patches 4, 5
14. **work: Pre-cancelled context, all-fail** — Patch 15
15. **health: Content-Type, sort order, empty checks** — Patch 16
16. **cache: Name option, MaxSize clamping** — Patch 14
17. **webhook: Status miss, missing headers** — Patch 13
18. **deploy: Malformed JSON, partial TLS** — Patch 18
19. **flagz: Multi zero sources, multi-segment env** — Patch 19
20. **xyopsworker: Run, Dispatch error** — Patch 20

---

## Notes

- **Go 1.25.5 loop variable scoping**: All loop closures in the codebase are safe per Go 1.22+ per-iteration scoping. This is NOT a bug despite multiple audit tools flagging it.
- **Test pattern**: All test files use `chassis.RequireMajor(9)` in `TestMain` or `init()`. New test files must include this.
- **Module path**: All imports use `github.com/ai8future/chassis-go/v9/...`.
- Patches are written to append to existing test files (indicated by `@@ -end of file`). In practice, paste at the bottom of each file.
- Some patches (especially Patch 10 for xyops) require both mock handler additions in the `mux()` function AND new test functions. The diff shows both sections.
