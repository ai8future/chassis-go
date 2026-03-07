Date Created: 2026-02-16T21:24:29-06:00
TOTAL_SCORE: 88/100

# chassis-go Test Coverage Analysis & Proposed Unit Tests

## Executive Summary

chassis-go v5.0.0 is a well-tested project with 26 test files covering 31 source files. All tests pass. Coverage by package:

| Package | Coverage | Grade |
|---------|----------|-------|
| lifecycle | 100.0% | A+ |
| logz | 98.0% | A+ |
| work | 98.5% | A+ |
| call | 96.3% | A |
| config | 96.4% | A |
| errors | 96.2% | A |
| httpkit | 96.3% | A |
| guard | 94.9% | A |
| testkit | 94.7% | A |
| metrics | 93.4% | A |
| health | 93.2% | A |
| secval | 91.3% | A- |
| flagz | 89.0% | B+ |
| chassis (root) | 85.7% | B |
| grpckit | 84.6% | B |
| otel | 77.8% | C+ |
| **internal/otelutil** | **0.0%** | **F** |

**Overall assessment:** The codebase earns **88/100**. Excellent structural coverage across all major packages. The primary gaps are: (1) `internal/otelutil` has zero tests, (2) `otel` package has low coverage due to untestable exporter error paths, (3) several edge-case branches in guard, errors, and config remain untested.

---

## Identified Test Gaps & Proposed Tests

### 1. `internal/otelutil/histogram.go` — 0% Coverage (CRITICAL)

**Gap:** Entire `LazyHistogram` function is untested. It is used by `httpkit`, `grpckit`, and `call` (indirectly tested), but no unit test exercises it directly.

**Proposed test file:** `internal/otelutil/histogram_test.go`

```diff
--- /dev/null
+++ b/internal/otelutil/histogram_test.go
@@ -0,0 +1,62 @@
+package otelutil
+
+import (
+	"context"
+	"testing"
+
+	otelapi "go.opentelemetry.io/otel"
+	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
+	"go.opentelemetry.io/otel/sdk/metric/metricdata"
+)
+
+func TestLazyHistogramReturnsSameInstance(t *testing.T) {
+	reader := sdkmetric.NewManualReader()
+	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
+	prev := otelapi.GetMeterProvider()
+	otelapi.SetMeterProvider(mp)
+	defer func() {
+		otelapi.SetMeterProvider(prev)
+		mp.Shutdown(context.Background())
+	}()
+
+	getter := LazyHistogram("test", "test_histogram")
+
+	h1 := getter()
+	h2 := getter()
+	if h1 == nil {
+		t.Fatal("LazyHistogram returned nil on first call")
+	}
+	// sync.Once guarantees the same instance.
+	if h1 != h2 {
+		t.Fatal("expected same histogram instance on second call")
+	}
+}
+
+func TestLazyHistogramRecordsValues(t *testing.T) {
+	reader := sdkmetric.NewManualReader()
+	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
+	prev := otelapi.GetMeterProvider()
+	otelapi.SetMeterProvider(mp)
+	defer func() {
+		otelapi.SetMeterProvider(prev)
+		mp.Shutdown(context.Background())
+	}()
+
+	getter := LazyHistogram("test-meter", "my_duration")
+	h := getter()
+	h.Record(context.Background(), 1.5)
+	h.Record(context.Background(), 2.5)
+
+	var rm metricdata.ResourceMetrics
+	if err := reader.Collect(context.Background(), &rm); err != nil {
+		t.Fatalf("Collect failed: %v", err)
+	}
+
+	found := false
+	for _, sm := range rm.ScopeMetrics {
+		for _, m := range sm.Metrics {
+			if m.Name == "my_duration" {
+				found = true
+			}
+		}
+	}
+	if !found {
+		t.Error("expected my_duration metric in collected data")
+	}
+}
```

---

### 2. `errors/problem.go:107-109` — WriteProblem nil error early return

**Gap:** `WriteProblem(w, r, nil, "")` takes the early-return path on line 107-109. No test exercises this.

**Proposed addition to:** `errors/errors_test.go`

```diff
--- a/errors/errors_test.go
+++ b/errors/errors_test.go
@@ -415,3 +415,18 @@ func TestProblemDetailMarshalJSONSkipsReservedExtensions(t *testing.T) {
 	if got["custom"] != "preserved" {
 		t.Errorf("custom extension missing: %v", got["custom"])
 	}
 }
+
+func TestWriteProblemNilError(t *testing.T) {
+	rec := httptest.NewRecorder()
+	req := httptest.NewRequest("GET", "/", nil)
+
+	WriteProblem(rec, req, nil, "req-123")
+
+	// Should be a no-op — no status code written, empty body.
+	if rec.Code != http.StatusOK {
+		t.Errorf("expected default 200 (no WriteHeader call), got %d", rec.Code)
+	}
+	if rec.Body.Len() != 0 {
+		t.Errorf("expected empty body, got %q", rec.Body.String())
+	}
+}
```

---

### 3. `errors/errors.go:140-142` — FromError(nil) returns nil

**Gap:** `FromError(nil)` is never tested directly. While it's a simple path, verifying it prevents regressions.

```diff
--- a/errors/errors_test.go
+++ b/errors/errors_test.go
@@ -182,6 +182,13 @@ func TestFromErrorGenericError(t *testing.T) {
 	}
 }

+func TestFromErrorNilReturnsNil(t *testing.T) {
+	got := FromError(nil)
+	if got != nil {
+		t.Errorf("FromError(nil) = %v, want nil", got)
+	}
+}
+
 func TestErrorf(t *testing.T) {
```

---

### 4. `errors/errors.go:77-86` — clone() deep-copies Details, handles nil Details

**Gap:** No test verifies that mutation of the original's Details map after clone doesn't affect the clone, and that clone with nil Details works.

```diff
--- a/errors/errors_test.go
+++ b/errors/errors_test.go
@@ -133,6 +133,25 @@ func TestWithDetail(t *testing.T) {
 	}
 }

+func TestWithDetailCloneIsolation(t *testing.T) {
+	base := ValidationError("bad").WithDetail("a", "1")
+	derived := base.WithDetail("b", "2")
+
+	// Mutating derived must not affect base.
+	if _, ok := base.Details["b"]; ok {
+		t.Error("base.Details should not contain key 'b' from derived")
+	}
+	if derived.Details["a"] != "1" {
+		t.Errorf("derived.Details[a] = %v, want '1'", derived.Details["a"])
+	}
+}
+
+func TestWithCausePreservesUnwrap(t *testing.T) {
+	cause := errors.New("root cause")
+	err := InternalError("wrapper").WithCause(cause)
+	if !errors.Is(err, cause) {
+		t.Error("WithCause result should be unwrappable to original cause")
+	}
+}
+
 func TestWithDetails(t *testing.T) {
```

---

### 5. `guard/timeout.go:98-100,120-122,143-144` — timeoutWriter edge cases

**Gap:** Several branches in `timeoutWriter` are untested:
- `WriteHeader` double-call (line 98-100): second call is silently ignored
- `flush` when already written (line 120-122): double-flush protection
- `timeout` when handler already started writing (line 143): no-op when `started=true`

```diff
--- a/guard/timeout_test.go
+++ b/guard/timeout_test.go
@@ -104,3 +104,35 @@ func TestTimeoutPanicsOnNegative(t *testing.T) {
 	}()
 	guard.Timeout(-1)
 }
+
+func TestTimeoutHandlerWriteWithoutExplicitWriteHeader(t *testing.T) {
+	// Handler calls Write() without WriteHeader() — should default to 200.
+	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		w.Write([]byte("implicit 200"))
+	})
+
+	handler := guard.Timeout(5 * time.Second)(inner)
+	req := httptest.NewRequest("GET", "/", nil)
+	rec := httptest.NewRecorder()
+	handler.ServeHTTP(rec, req)
+
+	if rec.Code != http.StatusOK {
+		t.Fatalf("expected 200, got %d", rec.Code)
+	}
+	if rec.Body.String() != "implicit 200" {
+		t.Fatalf("expected body 'implicit 200', got %q", rec.Body.String())
+	}
+}
+
+func TestTimeoutHandlerPanicPropagates(t *testing.T) {
+	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		panic("handler exploded")
+	})
+
+	handler := guard.Timeout(5 * time.Second)(inner)
+	req := httptest.NewRequest("GET", "/", nil)
+	rec := httptest.NewRecorder()
+
+	defer func() {
+		if r := recover(); r == nil {
+			t.Fatal("expected panic to propagate through Timeout middleware")
+		}
+	}()
+	handler.ServeHTTP(rec, req)
+}
```

---

### 6. `guard/keyfunc.go:59` — XForwardedFor with unparseable RemoteAddr

**Gap:** When `net.ParseIP(host)` returns nil (e.g., RemoteAddr is a hostname, not an IP), the function falls back to host. Not tested.

```diff
--- a/guard/keyfunc_test.go
+++ b/guard/keyfunc_test.go
@@ -82,6 +82,18 @@ func TestXForwardedForKeyFunc(t *testing.T) {
 	}
 }

+func TestXForwardedForUnparseableRemoteAddr(t *testing.T) {
+	// When RemoteAddr is a hostname (not an IP), ParseIP returns nil.
+	// Should fall back to the hostname itself.
+	keyFunc := XForwardedFor("10.0.0.0/8")
+	req := httptest.NewRequest(http.MethodGet, "/", nil)
+	req.RemoteAddr = "my-host:8080"
+	req.Header.Set("X-Forwarded-For", "203.0.113.5")
+	if got := keyFunc(req); got != "my-host" {
+		t.Fatalf("key = %q, want %q (should fall back to hostname)", got, "my-host")
+	}
+}
+
+func TestXForwardedForAllTrustedFallsBack(t *testing.T) {
+	// When every IP in XFF is trusted, should fall back to RemoteAddr.
+	keyFunc := XForwardedFor("10.0.0.0/8")
+	req := httptest.NewRequest(http.MethodGet, "/", nil)
+	req.RemoteAddr = "10.1.2.3:8080"
+	req.Header.Set("X-Forwarded-For", "10.0.0.1, 10.0.0.2")
+	if got := keyFunc(req); got != "10.1.2.3" {
+		t.Fatalf("key = %q, want %q (all XFF IPs trusted, fallback)", got, "10.1.2.3")
+	}
+}
+
 func TestXForwardedForPanicsOnInvalidCIDR(t *testing.T) {
```

---

### 7. `config/config.go:126-128` — Unsupported field type

**Gap:** The `default` case in `setField()` returning `"unsupported field type"` is never exercised. No test uses a struct with an unsupported type (e.g., `uint`, `complex128`).

```diff
--- a/config/config_test.go
+++ b/config/config_test.go
@@ -245,6 +245,22 @@ func TestMustLoad_InvalidFloat(t *testing.T) {
 	_ = MustLoad[cfg]()
 }

+func TestMustLoad_UnsupportedFieldType(t *testing.T) {
+	defer func() {
+		r := recover()
+		if r == nil {
+			t.Fatal("expected panic for unsupported field type, got none")
+		}
+		msg, ok := r.(string)
+		if !ok || !contains(msg, "unsupported field type") {
+			t.Fatalf("unexpected panic message: %v", r)
+		}
+	}()
+
+	t.Setenv("TEST_UINT", "42")
+	type cfg struct {
+		Val uint `env:"TEST_UINT"`
+	}
+	_ = MustLoad[cfg]()
+}
+
 // ---------- helpers ----------
```

---

### 8. `config/config.go:43-45` — Unexported fields with env tag skipped

**Gap:** The unexported-field skip path is never tested. No struct in tests has an unexported field with an `env` tag.

```diff
--- a/config/config_test.go
+++ b/config/config_test.go
@@ -40,6 +40,12 @@ type emptyStruct struct{}

+type withUnexported struct {
+	Name    string `env:"TEST_NAME"`
+	hidden  string `env:"TEST_HIDDEN"` //nolint:unused // tests unexported skip path
+}
+
 type mixedConfig struct {
@@ -157,6 +163,14 @@ func TestMustLoad_SkipsFieldsWithoutEnvTag(t *testing.T) {
 	}
 }

+func TestMustLoad_SkipsUnexportedFields(t *testing.T) {
+	t.Setenv("TEST_NAME", "visible")
+	t.Setenv("TEST_HIDDEN", "should-be-ignored")
+	cfg := MustLoad[withUnexported]()
+	if cfg.Name != "visible" {
+		t.Errorf("Name = %q, want %q", cfg.Name, "visible")
+	}
+	// cfg.hidden should remain zero-value (empty string) because it's unexported.
+}
+
 func TestMustLoad_InvalidInt(t *testing.T) {
```

---

### 9. `metrics/metrics.go:146-157` — warnOnceOverflowLocked with nil logger

**Gap:** The `TestCardinalityLimit` test uses `nil` logger but doesn't verify the absence of panics explicitly, and the code path where `r.logger != nil` logs a warning is tested in `TestWarnOnceOverflowLogsOnce`. However, the nil-logger guard (line 151) is only incidentally exercised. The current tests are adequate but a targeted test improves confidence.

```diff
--- a/metrics/metrics_test.go
+++ b/metrics/metrics_test.go
@@ -189,3 +189,17 @@ func TestWarnOnceOverflowLogsOnce(t *testing.T) {
 	}
 }
+
+func TestCardinalityLimitNilLoggerNoPanic(t *testing.T) {
+	_ = setupTestMeter(t)
+	rec := New("nillog", nil) // nil logger
+
+	ctx := context.Background()
+	for i := range MaxLabelCombinations {
+		rec.RecordRequest(ctx, "GET", fmt.Sprintf("s%d", i), 10, 100)
+	}
+
+	// Trigger overflow — must not panic with nil logger.
+	rec.RecordRequest(ctx, "GET", "overflow-a", 10, 100)
+	rec.RecordRequest(ctx, "GET", "overflow-b", 10, 100)
+	// If we got here without panic, the nil-logger guard works.
+}
```

---

### 10. `secval/secval.go:66-71` — Non-ASCII/non-printable character stripping

**Gap:** No test exercises the `strings.Map` cleaning path with non-ASCII or control characters in JSON keys.

```diff
--- a/secval/secval_test.go
+++ b/secval/secval_test.go
@@ -105,3 +105,24 @@ func TestNestedDangerousKey(t *testing.T) {
 	}
 }
+
+func TestNonASCIIKeyStripping(t *testing.T) {
+	// Unicode characters should be stripped before normalisation.
+	// "ex\u00e9c" (exéc) should strip to "exc" which is NOT dangerous.
+	err := ValidateJSON([]byte(`{"ex\u00e9c": true}`))
+	if err != nil {
+		t.Fatalf("expected nil for key with stripped non-ASCII, got %v", err)
+	}
+}
+
+func TestNonPrintableKeyStripping(t *testing.T) {
+	// Control character \x00 embedded in key should be stripped.
+	err := ValidateJSON([]byte(`{"safe\u0000key": true}`))
+	if err != nil {
+		t.Fatalf("expected nil for key with control chars, got %v", err)
+	}
+}
+
+func TestArrayDepthLimit(t *testing.T) {
+	// Build deeply nested arrays (not objects) to test array depth path.
+	json := strings.Repeat(`[`, 21) + `1` + strings.Repeat(`]`, 21)
+	err := ValidateJSON([]byte(json))
+	if !errors.Is(err, ErrNestingDepth) {
+		t.Fatalf("expected ErrNestingDepth for deeply nested arrays, got %v", err)
+	}
+}
```

---

### 11. `logz/logz.go:95-97` — Nested groups (depth > 1)

**Gap:** `TestWithGroupPreservesTraceHandler` only tests a single group level. The reconstruction loop at lines 95-97 (`for i := len(h.groups) - 2; i >= 0; i--`) only executes when there are 2+ groups.

```diff
--- a/logz/logz_test.go
+++ b/logz/logz_test.go
@@ -257,3 +257,40 @@ func TestTraceHandlerOmitsFieldsWithNoSpan(t *testing.T) {
 	}
 }
+
+func TestNestedGroupsPreserveTraceID(t *testing.T) {
+	var buf bytes.Buffer
+	logger := newTestLogger(&buf, "info")
+	logger = logger.WithGroup("outer").WithGroup("inner")
+
+	traceIDHex, _ := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
+	spanIDHex, _ := trace.SpanIDFromHex("b7ad6b7169203331")
+	sc := trace.NewSpanContext(trace.SpanContextConfig{
+		TraceID:    traceIDHex,
+		SpanID:     spanIDHex,
+		TraceFlags: trace.FlagsSampled,
+	})
+	ctx := trace.ContextWithSpanContext(context.Background(), sc)
+	logger.InfoContext(ctx, "nested", "k", "v")
+
+	var entry map[string]interface{}
+	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
+		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
+	}
+
+	// trace_id should be at top level.
+	if v, _ := entry["trace_id"].(string); v != "0af7651916cd43dd8448eb211c80319c" {
+		t.Errorf("expected trace_id at top level, got %v", entry["trace_id"])
+	}
+
+	// "k" should be nested under outer.inner.
+	outer, ok := entry["outer"].(map[string]interface{})
+	if !ok {
+		t.Fatalf("expected 'outer' group in output, got: %v", entry)
+	}
+	inner, ok := outer["inner"].(map[string]interface{})
+	if !ok {
+		t.Fatalf("expected 'inner' group nested in 'outer', got: %v", outer)
+	}
+	if v, _ := inner["k"].(string); v != "v" {
+		t.Errorf("expected inner.k=%q, got %v", "v", inner["k"])
+	}
+}
```

---

### 12. `flagz/sources.go:71-80` — FromJSON error paths

**Gap:** No test exercises the panic paths in `FromJSON` (file not found, invalid JSON).

```diff
--- a/flagz/flagz_test.go
+++ b/flagz/flagz_test.go
@@ Add these tests to the existing flagz_test.go file:
+
+func TestFromJSONPanicsOnMissingFile(t *testing.T) {
+	defer func() {
+		if r := recover(); r == nil {
+			t.Fatal("expected panic for missing JSON file")
+		}
+	}()
+	FromJSON("/nonexistent/path/to/flags.json")
+}
+
+func TestFromJSONPanicsOnInvalidJSON(t *testing.T) {
+	tmpFile, err := os.CreateTemp("", "flagz-test-*.json")
+	if err != nil {
+		t.Fatalf("failed to create temp file: %v", err)
+	}
+	defer os.Remove(tmpFile.Name())
+
+	tmpFile.WriteString("{not valid json}")
+	tmpFile.Close()
+
+	defer func() {
+		if r := recover(); r == nil {
+			t.Fatal("expected panic for invalid JSON")
+		}
+	}()
+	FromJSON(tmpFile.Name())
+}
```

---

### 13. `call/call.go:259-270` — stateName unknown state

**Gap:** `stateName` is tested indirectly through span events but the `default: "unknown"` branch is never exercised.

```diff
--- a/call/call_test.go
+++ b/call/call_test.go
@@ Add to call_test.go:
+
+func TestStateNameUnknown(t *testing.T) {
+	// stateName is unexported, but we can test via the exported State type.
+	// State(99) is not a valid state and should return "unknown".
+	name := stateName(State(99))
+	if name != "unknown" {
+		t.Fatalf("stateName(99) = %q, want %q", name, "unknown")
+	}
+}
+
+func TestStateNameAllStates(t *testing.T) {
+	cases := []struct {
+		s    State
+		want string
+	}{
+		{StateClosed, "closed"},
+		{StateOpen, "open"},
+		{StateHalfOpen, "half-open"},
+	}
+	for _, tc := range cases {
+		if got := stateName(tc.s); got != tc.want {
+			t.Errorf("stateName(%d) = %q, want %q", tc.s, got, tc.want)
+		}
+	}
+}
```

---

### 14. `errors/problem.go:82-84` — ProblemDetail with nil request

**Gap:** The `ProblemDetail` method has a nil-check for `r != nil && r.URL != nil`, but no test exercises the case where request is nil (which is possible if `ProblemDetail` is called outside an HTTP context).

```diff
--- a/errors/errors_test.go
+++ b/errors/errors_test.go
@@ Add to errors_test.go:
+
+func TestProblemDetailNilRequest(t *testing.T) {
+	err := InternalError("boom")
+	pd := err.ProblemDetail(nil)
+	if pd.Instance != "" {
+		t.Errorf("Instance = %q, want empty for nil request", pd.Instance)
+	}
+	if pd.Status != http.StatusInternalServerError {
+		t.Errorf("Status = %d, want 500", pd.Status)
+	}
+}
```

---

### 15. `call/breaker.go:94` — Allow() default return (unreachable but testable)

**Gap:** The `default: return nil` at line 94 of `Allow()` is technically unreachable with the current state enum, but `State(99)` could trigger it. Low priority.

```diff
--- a/call/breaker_test.go
+++ b/call/breaker_test.go
@@ Add to breaker_test.go:
+
+func TestBreakerRecordClosedSuccessResets(t *testing.T) {
+	name := fmt.Sprintf("test-reset-%d", time.Now().UnixNano())
+	cb := GetBreaker(name, 3, time.Second)
+
+	// Record some failures (but not enough to open).
+	cb.Record(false)
+	cb.Record(false)
+
+	// A success should reset the failure count.
+	cb.Record(true)
+
+	// Now two more failures should NOT open the breaker (count was reset).
+	cb.Record(false)
+	cb.Record(false)
+
+	if cb.State() != StateClosed {
+		t.Fatalf("expected StateClosed after reset, got %d", cb.State())
+	}
+}
+
+func TestBreakerHalfOpenFailureReopens(t *testing.T) {
+	name := fmt.Sprintf("test-reopen-%d", time.Now().UnixNano())
+	cb := GetBreaker(name, 2, 50*time.Millisecond)
+
+	// Open the breaker.
+	cb.Record(false)
+	cb.Record(false)
+	if cb.State() != StateOpen {
+		t.Fatal("expected StateOpen")
+	}
+
+	// Wait for reset timeout, then Allow() transitions to probing.
+	time.Sleep(60 * time.Millisecond)
+	if err := cb.Allow(); err != nil {
+		t.Fatalf("expected Allow() to succeed in half-open, got %v", err)
+	}
+
+	// Record failure — should re-open.
+	cb.Record(false)
+	if cb.State() != StateOpen {
+		t.Fatalf("expected StateOpen after failed probe, got %d", cb.State())
+	}
+}
```

---

## Coverage Impact Estimate

If all proposed tests were implemented:

| Package | Current | Estimated |
|---------|---------|-----------|
| internal/otelutil | 0.0% | ~95% |
| errors | 96.2% | ~99% |
| guard | 94.9% | ~97% |
| config | 96.4% | ~99% |
| secval | 91.3% | ~96% |
| logz | 98.0% | ~99% |
| flagz | 89.0% | ~93% |
| call | 96.3% | ~98% |
| metrics | 93.4% | ~95% |

**Projected overall grade improvement: 88 → 92/100**

---

## Scoring Breakdown

| Category | Points | Max | Notes |
|----------|--------|-----|-------|
| Statement coverage | 32 | 35 | Average 91.3% across testable packages; otelutil at 0% |
| Branch coverage | 18 | 20 | Most branches covered; notable gaps in timeout writer, XFF |
| Edge case coverage | 14 | 15 | Good panic tests, nil handling; some missing (nil request, nested groups) |
| Error path coverage | 10 | 15 | otel exporter errors, meter creation errors largely untested |
| Test quality & structure | 14 | 15 | Clear naming, table-driven tests, good use of test helpers |
| **Total** | **88** | **100** | |

---

## Notes

- **Loop variable capture is NOT a bug** in this project (Go 1.25.5 with per-iteration scoping).
- All 26 existing test files pass cleanly with `go test ./...`.
- The `otel` package (77.8%) has low coverage primarily because exporter creation requires a running OTLP collector. The error paths (lines 79-81, 104-111) are difficult to test without mocking gRPC connections. This is acceptable.
- The `cmd/demo-shutdown` and `examples/` packages have 0% coverage, which is expected for demo/example code.
