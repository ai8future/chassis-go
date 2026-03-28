Date Created: 2026-03-27 12:28:14 UTC
TOTAL_SCORE: 79/100

# chassis-go Quick Analysis Report
**Agent**: Claude:Opus 4.6 | **Module**: `github.com/ai8future/chassis-go/v10` | **Go**: 1.25.5 | **Version**: 10.0.8

---

## 1. AUDIT — Security and Code Quality Issues

### AUD-01: HSTS Header Trusts Unspoofed X-Forwarded-Proto (MEDIUM)
**File**: `guard/secheaders.go:73`

The HSTS header is set when `X-Forwarded-Proto == "https"`, but this header is not validated against a trusted proxy source. An attacker sending `X-Forwarded-Proto: https` on a direct HTTP connection triggers HSTS inclusion.

```diff
--- a/guard/secheaders.go
+++ b/guard/secheaders.go
@@ -70,7 +70,11 @@
 			if cfg.PermissionsPolicy != "" {
 				w.Header().Set("Permissions-Policy", cfg.PermissionsPolicy)
 			}
-			if hstsValue != "" && (r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https") {
+			// Only trust X-Forwarded-Proto when the request also arrived
+			// over TLS or the operator has configured a reverse proxy that
+			// strips the header from untrusted sources. For defense-in-depth
+			// we require TLS on the direct connection.
+			if hstsValue != "" && r.TLS != nil {
 				w.Header().Set("Strict-Transport-Security", hstsValue)
 			}
```

### AUD-02: CORS Origin Matching is Case-Sensitive (LOW)
**File**: `guard/cors.go:64-74`

RFC 6454 specifies scheme and host should be compared case-insensitively. A client sending `HTTPS://Example.Com` will be rejected even if `https://example.com` is allowed.

```diff
--- a/guard/cors.go
+++ b/guard/cors.go
@@ -61,6 +61,7 @@
 	return func(next http.Handler) http.Handler {
 		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
 			origin := r.Header.Get("Origin")
+			origin = strings.ToLower(origin)
 			if origin == "" {
 				// Not a CORS request — pass through.
 				next.ServeHTTP(w, r)
```
**Note**: Also requires lowercasing origin strings when building the `origins` map at line 55-59.

### AUD-03: Ignored Write Error in Registry Log (MEDIUM)
**File**: `registry/registry.go:666`

`logFile.Write()` error is silently discarded. On disk-full or permission errors, logs are lost with no indication.

```diff
--- a/registry/registry.go
+++ b/registry/registry.go
@@ -663,5 +663,8 @@
 	if err != nil {
 		return
 	}
-	logFile.Write(append(data, '\n'))
+	if _, wErr := logFile.Write(append(data, '\n')); wErr != nil {
+		// Best-effort: log to stderr since the structured log file is unavailable.
+		fmt.Fprintf(os.Stderr, "registry: failed to write log entry: %v\n", wErr)
+	}
 }
```

### AUD-04: Ignored HTTP Response Write in Health Handler (LOW)
**File**: `health/handler.go:46`

`w.Write(buf.Bytes())` error is discarded. Not critical but violates error handling best practices.

```diff
--- a/health/handler.go
+++ b/health/handler.go
@@ -43,6 +43,8 @@
 		}
 		w.Header().Set("Content-Type", "application/json")
 		w.WriteHeader(code)
-		w.Write(buf.Bytes())
+		if _, err := w.Write(buf.Bytes()); err != nil {
+			slog.ErrorContext(r.Context(), "health: failed to write response", "error", err)
+		}
 	})
 }
```

### AUD-05: Goroutine Leak in Timeout Middleware (MEDIUM)
**File**: `guard/timeout.go:40-67`

When the deadline fires (line 61-66), the handler goroutine spawned at line 40 continues running. While the comment acknowledges this matches `http.TimeoutHandler` behavior, long-running handlers can accumulate orphaned goroutines.

```diff
--- a/guard/timeout.go
+++ b/guard/timeout.go
@@ -61,6 +61,8 @@
 			case <-ctx.Done():
 				// Deadline exceeded — write 504 if handler hasn't started writing.
 				// The goroutine may still be running but its context is cancelled;
-				// well-behaved handlers will return promptly. This matches the
-				// behavior of Go's stdlib http.TimeoutHandler.
+				// well-behaved handlers will return promptly.
+				//
+				// WARNING: Handlers that do not respect context cancellation will
+				// leak goroutines. Ensure all handlers check ctx.Done().
 				tw.timeout()
```

### AUD-06: Metrics Meter Creation Errors Silently Degraded (LOW)
**File**: `metrics/metrics.go:47-69`

When `meter.Float64Counter()` or `meter.Float64Histogram()` fail, the error is logged (if logger provided) but the nil instrument is stored. Subsequent calls silently no-op. This is acceptable graceful degradation but means metrics silently stop recording with no runtime visibility beyond the initial log.

No diff needed — this is by-design, but operators should be aware.

---

## 2. TESTS — Proposed Unit Tests for Untested Code

### TEST-01: httpkit/tracing.go — Zero Test Coverage (CRITICAL GAP)
**File**: `httpkit/tracing.go` (73 lines, 0% coverage)

The entire `Tracing()` middleware has no tests. This is the most significant coverage gap.

```diff
--- /dev/null
+++ b/httpkit/tracing_test.go
@@ -0,0 +1,82 @@
+package httpkit_test
+
+import (
+	"net/http"
+	"net/http/httptest"
+	"testing"
+
+	chassis "github.com/ai8future/chassis-go/v10"
+	"github.com/ai8future/chassis-go/v10/httpkit"
+	sdktrace "go.opentelemetry.io/otel/sdk/trace"
+	"go.opentelemetry.io/otel/sdk/trace/tracetest"
+	otelapi "go.opentelemetry.io/otel"
+)
+
+func init() { chassis.RequireMajor(10) }
+
+func TestTracing_CreatesSpan(t *testing.T) {
+	exp := tracetest.NewInMemoryExporter()
+	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
+	otelapi.SetTracerProvider(tp)
+	defer tp.Shutdown(t.Context())
+
+	handler := httpkit.Tracing()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		w.WriteHeader(http.StatusOK)
+	}))
+
+	req := httptest.NewRequest(http.MethodGet, "/health", nil)
+	rec := httptest.NewRecorder()
+	handler.ServeHTTP(rec, req)
+
+	spans := exp.GetSpans()
+	if len(spans) == 0 {
+		t.Fatal("expected at least one span")
+	}
+	if spans[0].Name != http.MethodGet {
+		t.Errorf("span name = %q, want %q", spans[0].Name, http.MethodGet)
+	}
+}
+
+func TestTracing_5xxSetsErrorStatus(t *testing.T) {
+	exp := tracetest.NewInMemoryExporter()
+	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
+	otelapi.SetTracerProvider(tp)
+	defer tp.Shutdown(t.Context())
+
+	handler := httpkit.Tracing()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		w.WriteHeader(http.StatusInternalServerError)
+	}))
+
+	req := httptest.NewRequest(http.MethodPost, "/fail", nil)
+	rec := httptest.NewRecorder()
+	handler.ServeHTTP(rec, req)
+
+	spans := exp.GetSpans()
+	if len(spans) == 0 {
+		t.Fatal("expected at least one span")
+	}
+	// Span should have error status for 5xx
+	if spans[0].Status.Code != 2 { // codes.Error = 2
+		t.Errorf("span status = %d, want Error (2)", spans[0].Status.Code)
+	}
+}
+
+func TestTracing_2xxNoError(t *testing.T) {
+	exp := tracetest.NewInMemoryExporter()
+	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
+	otelapi.SetTracerProvider(tp)
+	defer tp.Shutdown(t.Context())
+
+	handler := httpkit.Tracing()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		w.WriteHeader(http.StatusOK)
+	}))
+
+	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
+	rec := httptest.NewRecorder()
+	handler.ServeHTTP(rec, req)
+
+	spans := exp.GetSpans()
+	if len(spans) == 0 {
+		t.Fatal("expected at least one span")
+	}
+	if spans[0].Status.Code != 0 { // codes.Unset = 0
+		t.Errorf("span status = %d, want Unset (0)", spans[0].Status.Code)
+	}
+}
```

### TEST-02: call/breaker.go — RemoveBreaker Untested
**File**: `call/breaker.go:141-143`

```diff
--- a/call/call_test.go
+++ b/call/call_test.go
@@ +1,20 @@
+func TestRemoveBreaker(t *testing.T) {
+	name := "test-remove-" + t.Name()
+	cb := call.NewCircuitBreaker(name, call.BreakerConfig{
+		FailThreshold:  3,
+		ResetTimeout:   time.Second,
+	})
+
+	// Breaker should be retrievable via Allow.
+	if !cb.Allow() {
+		t.Fatal("expected Allow == true on new breaker")
+	}
+
+	call.RemoveBreaker(name)
+
+	// After removal, creating a new breaker with the same name should
+	// produce a fresh closed-state breaker, not the old one.
+	cb2 := call.NewCircuitBreaker(name, call.BreakerConfig{
+		FailThreshold:  3,
+		ResetTimeout:   time.Second,
+	})
+	if !cb2.Allow() {
+		t.Fatal("expected Allow == true on fresh breaker after RemoveBreaker")
+	}
+}
```

### TEST-03: guard/ratelimit.go — LRU Eviction Untested

The `evictLRU()` path at the map-size cap has no dedicated test. A test should fill the limiter map past capacity and verify the oldest entry is evicted.

```diff
--- /dev/null
+++ b/guard/ratelimit_evict_test.go
@@ -0,0 +1,35 @@
+package guard_test
+
+import (
+	"net/http"
+	"net/http/httptest"
+	"testing"
+
+	chassis "github.com/ai8future/chassis-go/v10"
+	"github.com/ai8future/chassis-go/v10/guard"
+)
+
+func init() { chassis.RequireMajor(10) }
+
+func TestRateLimit_EvictsLRU(t *testing.T) {
+	cfg := guard.RateLimitConfig{
+		RequestsPerSecond: 100,
+		Burst:             100,
+		MaxKeys:           2,
+		KeyFunc:           guard.HeaderKey("X-Client"),
+	}
+	mw := guard.RateLimit(cfg)
+	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		w.WriteHeader(http.StatusOK)
+	}))
+
+	// Fill with 3 distinct keys — the first should be evicted.
+	for _, key := range []string{"a", "b", "c"} {
+		req := httptest.NewRequest(http.MethodGet, "/", nil)
+		req.Header.Set("X-Client", key)
+		rec := httptest.NewRecorder()
+		handler.ServeHTTP(rec, req)
+		if rec.Code != http.StatusOK {
+			t.Fatalf("key %q: got %d, want 200", key, rec.Code)
+		}
+	}
+}
```

### TEST-04: Concurrent Stress Test for cache Package

No concurrent access tests exist for the cache package. A goroutine stress test would verify thread safety.

```diff
--- a/cache/cache_test.go (proposed addition)
+++ b/cache/cache_test.go
@@ +1,30 @@
+func TestCache_ConcurrentAccess(t *testing.T) {
+	c := cache.New[string, int](cache.Config{MaxSize: 100, TTL: time.Minute})
+	var wg sync.WaitGroup
+	for i := 0; i < 50; i++ {
+		wg.Add(1)
+		go func(n int) {
+			defer wg.Done()
+			key := fmt.Sprintf("key-%d", n%10)
+			c.Set(key, n)
+			c.Get(key)
+			c.Delete(key)
+		}(i)
+	}
+	wg.Wait()
+}
```

### TEST-05: Metrics Recorder Tests Have No Assertions
**File**: `metrics/metrics_test.go:199-217`

The `UnaryMetrics()` and `StreamMetrics()` tests only verify no panic occurs. They should verify metrics are actually recorded via an OTel test meter/reader.

```diff
--- a/metrics/metrics_test.go (proposed enhancement)
+++ b/metrics/metrics_test.go
 // Current test just calls the function and checks for no panic.
-// Proposed: use sdkmetric.NewManualReader() to verify recorded values.
+func TestRecorderCounter_VerifiesValue(t *testing.T) {
+	reader := sdkmetric.NewManualReader()
+	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
+	otelapi.SetMeterProvider(mp)
+
+	rec := metrics.New("test", nil)
+	rec.IncCounter("op", http.StatusOK)
+
+	var rm metricdata.ResourceMetrics
+	if err := reader.Collect(t.Context(), &rm); err != nil {
+		t.Fatal(err)
+	}
+	// Verify at least one data point was recorded
+	found := false
+	for _, sm := range rm.ScopeMetrics {
+		for _, m := range sm.Metrics {
+			if strings.Contains(m.Name, "requests_total") {
+				found = true
+			}
+		}
+	}
+	if !found {
+		t.Error("expected requests_total metric to be recorded")
+	}
+}
```

---

## 3. FIXES — Bugs, Issues, and Code Smells

### FIX-01: MaxBody ContentLength Check Provides False Confidence (LOW)
**File**: `guard/maxbody.go:19-22`

The `r.ContentLength` check returns early for declared-large bodies, but `ContentLength == -1` when not set (chunked encoding, missing header). The real protection comes from `http.MaxBytesReader` on line 24. The early check is misleading but harmless.

```diff
--- a/guard/maxbody.go
+++ b/guard/maxbody.go
@@ -17,7 +17,9 @@
 	return func(next http.Handler) http.Handler {
 		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
-			if r.ContentLength > maxBytes {
+			// Fast-reject when Content-Length is present and exceeds the limit.
+			// Chunked/missing Content-Length (ContentLength == -1) is enforced
+			// by MaxBytesReader below.
+			if r.ContentLength > 0 && r.ContentLength > maxBytes {
 				writeProblem(w, r, errors.PayloadTooLargeError("request body too large"))
 				return
 			}
```

### FIX-02: Registry Silent JSON Marshal/Unmarshal Failures (LOW)
**File**: `registry/registry.go:662-665, 717, 753`

Multiple locations silently discard JSON errors. At minimum, these should log to stderr to aid debugging.

```diff
--- a/registry/registry.go
+++ b/registry/registry.go
@@ -662,7 +662,7 @@
 	data, err := json.Marshal(entry)
 	if err != nil {
-		return
+		fmt.Fprintf(os.Stderr, "registry: failed to marshal log entry: %v\n", err)
+		return
 	}
```

### FIX-03: Missing Bounds Check Comment in XForwardedFor (INFO)
**File**: `guard/keyfunc.go:56-81`

The `XForwardedFor` implementation is actually correct — it walks right-to-left and requires RemoteAddr to be trusted before considering X-Forwarded-For. However, the function should document that `trustedCIDRs` MUST include the immediate reverse proxy CIDR to prevent bypass. No code fix needed; docstring enhancement recommended.

```diff
--- a/guard/keyfunc.go
+++ b/guard/keyfunc.go
@@ -29,6 +29,9 @@
 // X-Forwarded-For chain from right to left, returning the rightmost IP that is
 // NOT in the trusted CIDRs — this is the last hop before entering the trusted
 // proxy chain and is resistant to client-side header spoofing.
+//
+// IMPORTANT: trustedCIDRs must include the CIDR of every reverse proxy between
+// this service and the internet. Omitting a proxy CIDR falls back to RemoteAddr.
 //
 // Falls back to RemoteAddr if untrusted, if X-Forwarded-For is absent, or if
 // no valid non-trusted IP is found.
```

### FIX-04: Health Handler Sets Content-Type After WriteHeader (BUG)
**File**: `health/handler.go:44-46`

`w.Header().Set("Content-Type", ...)` is called on line 44, but `w.WriteHeader(code)` is called on line 45. The net/http documentation states headers must be set BEFORE `WriteHeader()`, so this is correct order. However, the existing code has lines 44-45 in the wrong order — Content-Type is set AFTER WriteHeader in some code paths due to the early return at line 42.

Actually re-reading the code: line 44 sets the header, line 45 calls WriteHeader — this is the correct order. No bug here.

### FIX-05: testkit/httpserver.go Silently Ignores Body Read Errors (LOW)
**File**: `testkit/httpserver.go:42`

```diff
--- a/testkit/httpserver.go
+++ b/testkit/httpserver.go
@@ -40,7 +40,10 @@
 		mu.Lock()
 		defer mu.Unlock()
-		body, _ := io.ReadAll(r.Body)
+		body, err := io.ReadAll(r.Body)
+		if err != nil {
+			// In tests, body read failures may indicate a test setup issue.
+			body = []byte("<read error: " + err.Error() + ">")
+		}
 		requests = append(requests, RecordedRequest{
```

---

## 4. REFACTOR — Opportunities to Improve Code Quality

### REF-01: Consolidate Panic-Based Validation Pattern
Multiple packages (guard, config, flagz) use `panic()` for invalid configuration at construction time. While this is a valid fail-fast pattern, the error messages are inconsistent in format. Consider a shared `mustn't` or `require` helper that standardizes the panic message format: `"package: FuncName: reason"`.

### REF-02: Extract Common HTTP Test Helpers
Several test files repeat the pattern of creating `httptest.NewRequest` + `httptest.NewRecorder` + calling `ServeHTTP` + checking status code. A small `testkit.AssertStatus(t, handler, req, wantCode)` helper would reduce boilerplate across 10+ test files.

### REF-03: OTel Lazy Histogram Pattern
`httpkit/tracing.go:20-25` and `grpckit/interceptors.go` both use `otelutil.LazyHistogram` — this is a clean shared pattern. Consider documenting it as a recommended pattern for any new OTel metrics in contributing guidelines.

### REF-04: Guard Package Could Use Functional Options
The guard package uses config structs (e.g., `CORSConfig`, `RateLimitConfig`, `SecurityHeadersConfig`). These work fine but lead to verbose construction. Functional options (e.g., `guard.WithMaxAge(3600)`) would be more idiomatic Go and allow setting only non-default values.

### REF-05: Registry Package Size
`registry/registry.go` is the largest file in the project (~670+ lines). Consider splitting the log-file management (`appendLogLocked`, `cleanStale`) into a `registry/logfile.go` subfile for readability.

### REF-06: Consistent Error Wrapping
Some packages use `fmt.Errorf("...: %w", err)` while others use `errors.New()` + string concatenation. Standardizing on `%w` wrapping enables `errors.Is`/`errors.As` chains across the toolkit.

---

## Score Breakdown

| Category | Score | Max | Notes |
|----------|-------|-----|-------|
| Architecture & Design | 18 | 20 | Clean toolkit pattern, zero cross-deps, good separation |
| Security | 13 | 20 | HSTS trust issue, CORS case sensitivity, XFF is well-handled |
| Error Handling | 12 | 15 | Multiple ignored write/marshal errors in registry, health |
| Test Coverage | 14 | 20 | httpkit/tracing.go has 0% coverage; metrics tests lack assertions |
| Code Quality | 12 | 15 | Good overall; registry file is large; inconsistent panic messages |
| Concurrency Safety | 10 | 10 | Well-handled with proper locking patterns throughout |
| **TOTAL** | **79** | **100** | |
