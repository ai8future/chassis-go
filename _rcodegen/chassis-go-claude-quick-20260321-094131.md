Date Created: 2026-03-21T09:41:31-07:00
TOTAL_SCORE: 76/100

---

# chassis-go Quick Analysis Report

**Agent**: Claude Code (Claude:Opus 4.6)
**Module**: `github.com/ai8future/chassis-go/v9`
**Go Version**: 1.25.5
**Packages**: 22 source packages, 47 source files, 35 test files

## Score Breakdown

| Category | Score | Max | Notes |
|----------|-------|-----|-------|
| Security | 14 | 20 | HSTS spoofable via X-Forwarded-Proto, registry TOCTOU, webhook race |
| Code Quality | 16 | 20 | Silently discarded errors in metrics/registry, hardcoded Retry-After |
| Test Coverage | 15 | 20 | All packages have tests; many edge cases and new functions untested |
| Architecture | 18 | 20 | Clean toolkit design, good separation; registry global state is a concern |
| Documentation | 13 | 20 | Good godoc but missing security caveats on key functions |

---

## Section 1: AUDIT — Security and Code Quality Issues

### A1. [HIGH] webhook/webhook.go — Data Race on Delivery Fields

**Lines 82-124**: `Delivery` struct fields (`Attempts`, `Status`, `LastError`) are mutated inside the `Send` retry loop without holding `s.mu`, but `Status()` reads them under `s.mu`. This is a data race detectable by `-race`.

```diff
--- a/webhook/webhook.go
+++ b/webhook/webhook.go
@@ -79,8 +79,10 @@ func (s *Sender) Send(url string, payload any, secret string) (string, error) {

 	var lastErr error
 	for attempt := 1; attempt <= s.maxAttempts; attempt++ {
+		s.mu.Lock()
 		delivery.Attempts = attempt
+		s.mu.Unlock()

 		req, err := http.NewRequest("POST", url, bytes.NewReader(body))
 		if err != nil {
@@ -103,7 +105,9 @@ func (s *Sender) Send(url string, payload any, secret string) (string, error) {

 		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
+			s.mu.Lock()
 			delivery.Status = "delivered"
+			s.mu.Unlock()
 			return id, nil
 		}

 		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
+			s.mu.Lock()
 			delivery.Status = "failed"
 			delivery.LastError = fmt.Sprintf("HTTP %d", resp.StatusCode)
+			s.mu.Unlock()
 			return "", fmt.Errorf("%w: HTTP %d", ErrClientError, resp.StatusCode)
 		}

@@ -120,8 +124,10 @@ func (s *Sender) Send(url string, payload any, secret string) (string, error) {
 	}

+	s.mu.Lock()
 	delivery.Status = "failed"
 	delivery.LastError = lastErr.Error()
+	s.mu.Unlock()
 	return "", fmt.Errorf("%w: %v", ErrServerError, lastErr)
 }
```

### A2. [HIGH] webhook/webhook.go — generateID Silently Ignores rand.Read Error

**Line 161**: Unlike `httpkit/middleware.go:37` which has a fallback, `webhook/generateID` discards the error from `crypto/rand.Read`. If randomness is unavailable, all webhook IDs become `"00000000000000000000000000000000"`, defeating deduplication.

```diff
--- a/webhook/webhook.go
+++ b/webhook/webhook.go
@@ -1,6 +1,8 @@
 package webhook

 import (
+	"crypto/rand"
+	"encoding/hex"
 	"bytes"
-	"crypto/rand"
-	"encoding/hex"
 	"encoding/json"
 	"errors"
 	"fmt"
 	"net/http"
 	"strconv"
+	"sync/atomic"
 	"sync"
 	"time"

@@ -156,7 +158,14 @@ func VerifyPayload(headers http.Header, body []byte, secret string) ([]byte, err
 	return body, nil
 }

+var webhookIDCounter uint64
+
 func generateID() string {
 	b := make([]byte, 16)
-	rand.Read(b)
+	if _, err := rand.Read(b); err != nil {
+		// Fallback: timestamp + counter to ensure uniqueness
+		return fmt.Sprintf("%x-%d",
+			time.Now().UnixNano(),
+			atomic.AddUint64(&webhookIDCounter, 1))
+	}
 	return hex.EncodeToString(b)
 }
```

### A3. [HIGH] guard/secheaders.go — HSTS Set Based on Spoofable X-Forwarded-Proto

**Line 73**: `X-Forwarded-Proto` is client-controlled. Without a trusted proxy stripping it, a plain HTTP client can trigger HSTS headers over a non-TLS connection, which is an RFC 6797 violation.

```diff
--- a/guard/secheaders.go
+++ b/guard/secheaders.go
@@ -70,7 +70,10 @@ func SecurityHeaders(cfg SecurityHeadersConfig) func(http.Handler) http.Handler
 			if cfg.PermissionsPolicy != "" {
 				w.Header().Set("Permissions-Policy", cfg.PermissionsPolicy)
 			}
-			if hstsValue != "" && (r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https") {
+			// Only set HSTS when the connection is genuinely TLS-terminated.
+			// X-Forwarded-Proto is client-controlled and MUST be validated
+			// by a trusted reverse proxy before reaching this middleware.
+			if hstsValue != "" && r.TLS != nil {
 				w.Header().Set("Strict-Transport-Security", hstsValue)
 			}
 			if cfg.CrossOriginOpenerPolicy != "" {
```

### A4. [MEDIUM] registry/registry.go — TOCTOU on Directory Permission Check

**Lines 204-213, 335-339**: `os.MkdirAll` + `os.Stat` has a TOCTOU window. More critically, if `os.Stat` fails, the permission check is silently skipped (the `if err == nil` guard means a Stat error = no check at all).

```diff
--- a/registry/registry.go
+++ b/registry/registry.go
@@ -206,9 +206,11 @@ func Init(cancel context.CancelFunc, chassisVersion string) error {
 	// Verify the directory has safe permissions. On shared systems (/tmp),
 	// another user could pre-create the directory with open permissions.
-	if info, err := os.Stat(svcDir); err == nil {
-		if perm := info.Mode().Perm(); perm&0o077 != 0 {
-			return fmt.Errorf("registry: directory %s has unsafe permissions %o (want 0700)", svcDir, perm)
-		}
+	info, err := os.Stat(svcDir)
+	if err != nil {
+		return fmt.Errorf("registry: stat directory: %w", err)
+	}
+	if perm := info.Mode().Perm(); perm&0o077 != 0 {
+		return fmt.Errorf("registry: directory %s has unsafe permissions %o (want 0700)", svcDir, perm)
 	}
```

Same fix needed at lines 335-339 in `InitCLI`.

### A5. [MEDIUM] metrics/metrics.go — Silently Discarded Instrument Creation Errors

**Lines 192-207**: `Counter()` and `Histogram()` discard errors from OTel meter instrument creation with `_, _`. A failed creation returns a no-op instrument, so all metric recording silently drops.

```diff
--- a/metrics/metrics.go
+++ b/metrics/metrics.go
@@ -189,9 +189,12 @@ func (h *HistogramVec) Observe(ctx context.Context, val float64, labelPairs ...s
 // Counter creates and registers a new counter with the given name.
 func (r *Recorder) Counter(name string) *CounterVec {
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

@@ -199,10 +202,13 @@ func (r *Recorder) Counter(name string) *CounterVec {
 // Histogram creates and registers a new histogram with the given name and buckets.
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

### A6. [MEDIUM] config/config.go — regexp.MustCompile Panics on Invalid Pattern

**Line 178**: `validateField` calls `regexp.MustCompile(value)` where `value` comes from a struct tag. An invalid regex causes an unrecoverable panic with no user-friendly error.

```diff
--- a/config/config.go
+++ b/config/config.go
@@ -175,7 +175,11 @@ func validateField(name string, val reflect.Value, tag string) {
 			}
 		case "pattern":
-			re := regexp.MustCompile(value)
+			re, err := regexp.Compile(value)
+			if err != nil {
+				panic(fmt.Sprintf("config: field %s has invalid pattern %q in validate tag: %v", name, value, err))
+			}
 			actual := fmt.Sprintf("%v", val.Interface())
 			if !re.MatchString(actual) {
 				panic(fmt.Sprintf("config: field %s value %q does not match pattern %s", name, actual, value))
```

### A7. [MEDIUM] otel/otel.go — Shared Timeout Budget for Trace and Metric Shutdown

**Lines 120-125**: Both `tp.Shutdown` and `mp.Shutdown` share the same 5-second `shutdownCtx`. If trace shutdown takes the full 5 seconds, metric shutdown gets 0 seconds.

```diff
--- a/otel/otel.go
+++ b/otel/otel.go
@@ -119,8 +119,10 @@ func Init(cfg Config) ShutdownFunc {

 	return func(ctx context.Context) error {
-		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
-		defer cancel()
-		tErr := tp.Shutdown(shutdownCtx)
-		mErr := mp.Shutdown(shutdownCtx)
+		traceCtx, traceCancel := context.WithTimeout(ctx, 5*time.Second)
+		defer traceCancel()
+		tErr := tp.Shutdown(traceCtx)
+
+		metricCtx, metricCancel := context.WithTimeout(ctx, 5*time.Second)
+		defer metricCancel()
+		mErr := mp.Shutdown(metricCtx)
 		return errors.Join(tErr, mErr)
 	}
```

### A8. [LOW] guard/ratelimit.go — Retry-After Always Returns "1"

**Line 119**: Hardcoded `Retry-After: 1` regardless of actual refill rate. Misleads clients about when to retry.

```diff
--- a/guard/ratelimit.go
+++ b/guard/ratelimit.go
@@ -116,7 +116,8 @@ func RateLimit(cfg RateLimitConfig) func(http.Handler) http.Handler {
 		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
 			key := cfg.KeyFunc(r)
 			if !lim.allow(key) {
-				w.Header().Set("Retry-After", "1")
+				retryAfter := int(cfg.Window.Seconds())
+				w.Header().Set("Retry-After", strconv.Itoa(max(1, retryAfter)))
 				writeProblem(w, r, errors.RateLimitError("rate limit exceeded"))
 				return
 			}
```

### A9. [LOW] registry/registry.go — ShutdownCLI Discards atomicWrite Error

**Line 531**: If the final PID file write fails, the process on disk retains `"running"` status, misleading the monitoring viewer.

```diff
--- a/registry/registry.go
+++ b/registry/registry.go
@@ -528,7 +528,9 @@ func ShutdownCLI(exitCode int) {
 		reg.ExitedAt = ts()
 		ec := exitCode
 		reg.ExitCode = &ec
 		reg.Summary = lastProgress
-		atomicWrite(pidPath, reg)
+		if err := atomicWrite(pidPath, reg); err != nil {
+			fmt.Fprintf(os.Stderr, "registry: failed to write final PID file: %v\n", err)
+		}
 	}
```

---

## Section 2: TESTS — Proposed Unit Tests

### T1. webhook/webhook.go — Race Detection Test for Concurrent Send + Status

```diff
--- a/webhook/webhook_test.go
+++ b/webhook/webhook_test.go
@@ -0,0 +1,35 @@
+func TestSendAndStatusConcurrentRace(t *testing.T) {
+	chassis.RequireMajor(9)
+	// This test is meaningful under -race
+	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		time.Sleep(10 * time.Millisecond) // simulate slow endpoint
+		w.WriteHeader(http.StatusOK)
+	}))
+	defer srv.Close()
+
+	s := webhook.NewSender(webhook.MaxAttempts(2))
+
+	var wg sync.WaitGroup
+	var id string
+	var sendErr error
+
+	wg.Add(1)
+	go func() {
+		defer wg.Done()
+		id, sendErr = s.Send(srv.URL, map[string]string{"key": "val"}, "secret")
+	}()
+
+	// Poll Status concurrently during Send to trigger the race
+	done := make(chan struct{})
+	go func() {
+		for {
+			select {
+			case <-done:
+				return
+			default:
+				s.Status("any-id") // concurrent read
+			}
+		}
+	}()
+
+	wg.Wait()
+	close(done)
+	_ = id
+	_ = sendErr
+}
```

### T2. grpckit/health.go — RegisterHealth and Check Method

```diff
--- a/grpckit/health_test.go
+++ b/grpckit/health_test.go
@@ -0,0 +1,40 @@
+func TestHealthCheck_Serving(t *testing.T) {
+	chassis.RequireMajor(9)
+	s := grpc.NewServer()
+	grpckit.RegisterHealth(s, func(ctx context.Context) error {
+		return nil // healthy
+	})
+
+	lis := bufconn.Listen(1 << 20)
+	go s.Serve(lis)
+	defer s.Stop()
+
+	conn, err := grpc.NewClient("passthrough:///bufconn",
+		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
+			return lis.DialContext(ctx)
+		}),
+		grpc.WithTransportCredentials(insecure.NewCredentials()),
+	)
+	if err != nil {
+		t.Fatal(err)
+	}
+	defer conn.Close()
+
+	client := grpc_health_v1.NewHealthClient(conn)
+	resp, err := client.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
+	if err != nil {
+		t.Fatal(err)
+	}
+	if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
+		t.Errorf("expected SERVING, got %v", resp.Status)
+	}
+}
+
+func TestHealthCheck_NotServing(t *testing.T) {
+	chassis.RequireMajor(9)
+	// Same setup but checker returns an error
+	// Expected: NOT_SERVING
+}
```

### T3. seal/seal.go — ValidateToken Edge Cases

```diff
--- a/seal/seal_test.go
+++ b/seal/seal_test.go
@@ -0,0 +1,30 @@
+func TestValidateToken_NoDot(t *testing.T) {
+	chassis.RequireMajor(9)
+	_, err := seal.ValidateToken("nodothere", "secret")
+	if !errors.Is(err, seal.ErrTokenInvalid) {
+		t.Errorf("expected ErrTokenInvalid, got %v", err)
+	}
+}
+
+func TestValidateToken_MalformedBase64(t *testing.T) {
+	chassis.RequireMajor(9)
+	// Body is invalid base64, followed by a valid hex sig
+	_, err := seal.ValidateToken("!!!invalid!!!."+strings.Repeat("a", 64), "secret")
+	if !errors.Is(err, seal.ErrTokenInvalid) {
+		t.Errorf("expected ErrTokenInvalid, got %v", err)
+	}
+}
+
+func TestValidateToken_MissingExp(t *testing.T) {
+	chassis.RequireMajor(9)
+	// Manually craft a signed token without exp
+	claims := map[string]any{"sub": "test"}
+	body, _ := json.Marshal(claims)
+	encoded := base64.RawURLEncoding.EncodeToString(body)
+	sig := seal.Sign(body, "secret")
+	token := encoded + "." + sig
+
+	_, err := seal.ValidateToken(token, "secret")
+	if !errors.Is(err, seal.ErrTokenInvalid) {
+		t.Errorf("expected ErrTokenInvalid for missing exp, got %v", err)
+	}
+}
```

### T4. config/config.go — Invalid Regex Pattern in Validate Tag

```diff
--- a/config/config_test.go
+++ b/config/config_test.go
@@ -0,0 +1,18 @@
+func TestMustLoad_InvalidPatternPanics(t *testing.T) {
+	chassis.RequireMajor(9)
+	type Cfg struct {
+		Name string `env:"TEST_INVALID_PATTERN" validate:"pattern=[invalid"`
+	}
+	t.Setenv("TEST_INVALID_PATTERN", "anything")
+
+	defer func() {
+		r := recover()
+		if r == nil {
+			t.Fatal("expected panic for invalid regex pattern")
+		}
+		msg := fmt.Sprint(r)
+		if !strings.Contains(msg, "invalid pattern") {
+			t.Errorf("panic message should mention invalid pattern, got: %s", msg)
+		}
+	}()
+	config.MustLoad[Cfg]()
+}
```

### T5. cache/cache.go — Delete Non-existent Key and Prune with No TTL

```diff
--- a/cache/cache_test.go
+++ b/cache/cache_test.go
@@ -0,0 +1,20 @@
+func TestDeleteNonExistentKey(t *testing.T) {
+	chassis.RequireMajor(9)
+	c := cache.New[string, int](cache.MaxSize(10))
+	// Should not panic or error
+	c.Delete("nonexistent")
+	if _, ok := c.Get("nonexistent"); ok {
+		t.Error("expected key not found")
+	}
+}
+
+func TestPruneWithNoTTL(t *testing.T) {
+	chassis.RequireMajor(9)
+	c := cache.New[string, int](cache.MaxSize(10))
+	c.Set("a", 1)
+	c.Set("b", 2)
+	n := c.Prune()
+	if n != 0 {
+		t.Errorf("expected 0 pruned entries with no TTL, got %d", n)
+	}
+}
```

### T6. secval/secval.go — Deeply Nested Arrays and Empty Input

```diff
--- a/secval/secval_test.go
+++ b/secval/secval_test.go
@@ -0,0 +1,25 @@
+func TestValidateJSON_DeeplyNestedArrays(t *testing.T) {
+	chassis.RequireMajor(9)
+	// Build 21-level nested arrays: [[[[...]]]]
+	input := strings.Repeat("[", 21) + "1" + strings.Repeat("]", 21)
+	err := secval.ValidateJSON([]byte(input))
+	if !errors.Is(err, secval.ErrNestingDepth) {
+		t.Errorf("expected ErrNestingDepth for 21-level nested arrays, got %v", err)
+	}
+}
+
+func TestValidateJSON_EmptyInput(t *testing.T) {
+	chassis.RequireMajor(9)
+	err := secval.ValidateJSON([]byte{})
+	if !errors.Is(err, secval.ErrInvalidJSON) {
+		t.Errorf("expected ErrInvalidJSON for empty input, got %v", err)
+	}
+}
+
+func TestValidateIdentifier_BoundaryLength(t *testing.T) {
+	chassis.RequireMajor(9)
+	valid64 := "a" + strings.Repeat("b", 63) // exactly 64 chars
+	if err := secval.ValidateIdentifier(valid64); err != nil {
+		t.Errorf("64-char identifier should be valid, got %v", err)
+	}
+	invalid65 := valid64 + "c"
+	if err := secval.ValidateIdentifier(invalid65); err == nil {
+		t.Error("65-char identifier should be invalid")
+	}
+}
```

### T7. webhook/webhook.go — Status for Unknown ID and VerifyPayload Missing Headers

```diff
--- a/webhook/webhook_test.go
+++ b/webhook/webhook_test.go
@@ -0,0 +1,28 @@
+func TestStatusUnknownID(t *testing.T) {
+	chassis.RequireMajor(9)
+	s := webhook.NewSender()
+	d, ok := s.Status("nonexistent-id")
+	if ok {
+		t.Error("expected ok=false for unknown ID")
+	}
+	if d.ID != "" {
+		t.Error("expected zero Delivery for unknown ID")
+	}
+}
+
+func TestVerifyPayload_MissingTimestamp(t *testing.T) {
+	chassis.RequireMajor(9)
+	h := http.Header{}
+	h.Set("X-Webhook-Signature", "sha256=abc123")
+	// No X-Webhook-Timestamp
+	_, err := webhook.VerifyPayload(h, []byte(`{}`), "secret")
+	if !errors.Is(err, webhook.ErrBadSignature) {
+		t.Errorf("expected ErrBadSignature for missing timestamp, got %v", err)
+	}
+}
+
+func TestVerifyPayload_MissingSignature(t *testing.T) {
+	chassis.RequireMajor(9)
+	h := http.Header{}
+	h.Set("X-Webhook-Timestamp", "1234567890")
+	_, err := webhook.VerifyPayload(h, []byte(`{}`), "secret")
+	if !errors.Is(err, webhook.ErrBadSignature) {
+		t.Errorf("expected ErrBadSignature for missing signature, got %v", err)
+	}
+}
```

### T8. call/call.go — WithHTTPClient Option

```diff
--- a/call/call_test.go
+++ b/call/call_test.go
@@ -0,0 +1,22 @@
+func TestWithHTTPClient(t *testing.T) {
+	chassis.RequireMajor(9)
+	customTransportUsed := false
+	customClient := &http.Client{
+		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
+			customTransportUsed = true
+			return &http.Response{
+				StatusCode: 200,
+				Body:       io.NopCloser(strings.NewReader("ok")),
+			}, nil
+		}),
+	}
+
+	c := call.New(call.WithHTTPClient(customClient))
+	req, _ := http.NewRequest("GET", "http://example.com", nil)
+	resp, err := c.Do(req)
+	if err != nil {
+		t.Fatal(err)
+	}
+	resp.Body.Close()
+	if !customTransportUsed {
+		t.Error("custom HTTP client transport was not used")
+	}
+}
```

### T9. metrics/metrics.go — Odd-Length Label Pairs

```diff
--- a/metrics/metrics_test.go
+++ b/metrics/metrics_test.go
@@ -0,0 +1,14 @@
+func TestPairsToCombo_OddLength(t *testing.T) {
+	chassis.RequireMajor(9)
+	// Odd number of pairs should silently drop the last element
+	// This tests the current behavior — consider whether a panic/error is better
+	combo := pairsToCombo([]string{"method", "GET", "orphan"})
+	expected := "method=GET"
+	if combo != expected {
+		t.Errorf("expected %q, got %q", expected, combo)
+	}
+}
```

Note: `pairsToCombo` is unexported; this test would go in `metrics/metrics_test.go` (same package).

---

## Section 3: FIXES — Bugs, Issues, and Code Smells

### F1. [BUG] webhook/webhook.go:82-124 — Data Race

As documented in A1. The `Delivery` pointer is stored in the map under the lock, but then mutated without the lock while `Status()` can concurrently read it. This is a real Go race condition.

```diff
--- a/webhook/webhook.go
+++ b/webhook/webhook.go
@@ -79,8 +79,10 @@ func (s *Sender) Send(url string, payload any, secret string) (string, error) {

 	var lastErr error
 	for attempt := 1; attempt <= s.maxAttempts; attempt++ {
+		s.mu.Lock()
 		delivery.Attempts = attempt
+		s.mu.Unlock()

 		req, err := http.NewRequest("POST", url, bytes.NewReader(body))
 		if err != nil {
@@ -102,7 +104,9 @@ func (s *Sender) Send(url string, payload any, secret string) (string, error) {
 		resp.Body.Close()

 		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
+			s.mu.Lock()
 			delivery.Status = "delivered"
+			s.mu.Unlock()
 			return id, nil
 		}

@@ -109,8 +113,10 @@ func (s *Sender) Send(url string, payload any, secret string) (string, error) {

 		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
+			s.mu.Lock()
 			delivery.Status = "failed"
 			delivery.LastError = fmt.Sprintf("HTTP %d", resp.StatusCode)
+			s.mu.Unlock()
 			return "", fmt.Errorf("%w: HTTP %d", ErrClientError, resp.StatusCode)
 		}

@@ -120,8 +126,10 @@ func (s *Sender) Send(url string, payload any, secret string) (string, error) {
 	}

+	s.mu.Lock()
 	delivery.Status = "failed"
 	delivery.LastError = lastErr.Error()
+	s.mu.Unlock()
 	return "", fmt.Errorf("%w: %v", ErrServerError, lastErr)
 }
```

### F2. [BUG] registry/registry.go:209,335 — Stat Error Silently Skips Permission Check

As documented in A4. If `os.Stat` fails after `MkdirAll` succeeds, execution proceeds with potentially insecure directory permissions.

### F3. [SMELL] otel/otel.go:120-125 — Shared Timeout for Independent Shutdown Operations

Both trace and metric providers share the same 5-second deadline. If trace shutdown takes the full budget, metric provider gets no time to drain. Fix: use independent timeouts as shown in A7.

### F4. [SMELL] guard/timeout.go:40-51 — Goroutine Leak When Timeout Fires

When a request times out, the goroutine running the handler continues executing. The context is cancelled but handlers that don't check `ctx.Done()` will leak indefinitely. The code already has a comment acknowledging this matches `http.TimeoutHandler` behavior. No simple fix; documenting is the right approach for now.

### F5. [SMELL] registry/registry.go:666 — Log Write Error Discarded

`logFile.Write(...)` return value is discarded. If the disk is full or fd is closed, log writes silently fail. A low-severity issue since this is a diagnostic log.

### F6. [SMELL] flagz/flagz.go — Empty UserID in Percentage Rollouts

When `fctx.Percent` is between 1 and 99, `consistentBucket` hashes `name + "\x00" + ""` for users with empty UserID. All anonymous users land in the same bucket, causing either 100% or 0% rollout — likely not intentional.

```diff
--- a/flagz/flagz.go
+++ b/flagz/flagz.go
@@ -68,6 +68,10 @@ func (e *Engine) Evaluate(name string, fctx Context) bool {
 	// Percentage-based rollout.
 	if pct > 0 && pct < 100 {
+		if fctx.UserID == "" {
+			addSpanEvent(fctx.Ctx, name, false, "no-user-id")
+			return false
+		}
 		result := consistentBucket(name, fctx.UserID, 100) < pct
 		addSpanEvent(fctx.Ctx, name, result, fmt.Sprintf("bucket<%d", pct))
 		return result
```

### F7. [SMELL] metrics/metrics.go:213-218 — Silent Truncation of Odd Label Pairs

`pairsToCombo` and `pairsToAttributes` silently drop the last element when an odd number of pairs is passed. This should at minimum log a warning or panic (since it indicates a programming error at the call site).

```diff
--- a/metrics/metrics.go
+++ b/metrics/metrics.go
@@ -210,6 +210,9 @@ func (r *Recorder) Histogram(name string, buckets []float64) *HistogramVec {
 // Including both keys and values prevents collisions where different
 // label names happen to share the same values.
 func pairsToCombo(pairs []string) string {
+	if len(pairs)%2 != 0 {
+		slog.Warn("metrics: odd number of label pairs, last element dropped", "count", len(pairs))
+	}
 	parts := make([]string, 0, len(pairs)/2)
 	for i := 0; i+1 < len(pairs); i += 2 {
 		parts = append(parts, pairs[i]+"="+pairs[i+1])
```

---

## Section 4: REFACTOR — Improvement Opportunities

### R1. registry/registry.go — Extract Global State into a Struct

The `registry` package uses 15+ package-level mutable variables protected by a single `sync.Mutex`. This makes the package non-reentrant, non-testable in parallel, and requires `ResetForTest()` exported to production code. **Recommendation**: Refactor to a `Registry` struct with a package-level default instance. Tests create their own instances.

### R2. guard/keyfunc.go + guard/secheaders.go — Trusted Proxy Configuration

Both `XForwardedFor` key extraction and HSTS rely on `X-Forwarded-*` headers without a trusted proxy configuration. **Recommendation**: Add a `TrustedProxies []string` configuration that gates all proxy-header trust.

### R3. otel/otel.go — Partial Initialization Strategy

When the metric exporter fails but the trace exporter succeeds, the service silently runs with partial telemetry. The returned `ShutdownFunc` correctly handles only the trace provider. **Recommendation**: Return an error alongside the `ShutdownFunc` so the caller can decide whether partial telemetry is acceptable.

### R4. seal/seal.go — Upgrade scrypt Work Factor

Current N=16384 is the 2009-era recommendation. OWASP 2023 recommends N=2^17 (131072) for interactive logins. Since the `Encrypt` function accepts a "passphrase" parameter, upgrading the work factor would improve resistance to offline dictionary attacks. **Recommendation**: Bump to N=131072 and add a version field check in `Decrypt` to auto-detect the work factor.

### R5. cache/cache.go — RWMutex Optimization for Reads

`Get()` takes a write lock because it calls `MoveToFront()`. For read-heavy workloads this fully serializes all reads. **Recommendation**: Consider a probabilistic LRU promotion (promote only every N accesses) to allow `RLock` on the hot path, or switch to a sharded design.

### R6. work/work.go — Clarify Zero-Task Semantics

`Race([]...)` and `Map([]...)` with empty input return `(zero, nil)`. This is semantically ambiguous (success with no work vs. misconfiguration). **Recommendation**: Either return a sentinel error like `work.ErrNoTasks` or document the behavior explicitly.

### R7. httpkit/middleware.go — responseWriter.Write Missing StatusCode Set

`Write()` at line 83 sets `headerWritten = true` but doesn't set `statusCode = http.StatusOK`. It works only because the struct is initialized with `statusCode: http.StatusOK` at line 106. This is fragile — if anyone creates a `responseWriter{}` without the initializer, logging will show status 0. **Recommendation**: Explicitly set `rw.statusCode = http.StatusOK` in the `Write` method.

### R8. Consolidate generateID Implementations

Both `httpkit/middleware.go:35` and `webhook/webhook.go:159` have their own `generateID`. The httpkit version has a proper fallback; webhook's does not. **Recommendation**: Extract a shared `internal/idgen` package or have webhook import httpkit's ID generation.

### R9. logz/logz.go — Configurable Output Writer

Logger output is hardcoded to `os.Stderr` with no configuration option. **Recommendation**: Accept an optional `io.Writer` parameter for testability and flexibility.

### R10. registry/registry.go — ResetForTest Exported to Production

`ResetForTest()` is exported and callable from production code. **Recommendation**: Move to an `internal/registrytest` package or use build tags to restrict it to test builds.
