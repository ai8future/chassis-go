Date Created: 2026-03-27T14:35:06-04:00
TOTAL_SCORE: 66/100

# Chassis-Go Combined Analysis Report

**Agent:** Claude Code (Claude:Opus 4.6)
**Codebase:** chassis-go v10 — Go toolkit for building services
**Module:** `github.com/ai8future/chassis-go/v10` | Go 1.25.5

## Score Breakdown

| Category | Score | Notes |
|----------|-------|-------|
| Security | 18/30 | Critical webhook replay, path traversal, URL injection |
| Code Quality | 16/22 | Data races, silent error drops, lock contention |
| Test Coverage | 14/20 | Many edge paths untested (seal, tick, webhook, config) |
| Architecture | 12/18 | Significant duplication, singleton anti-patterns, shipped stub |
| Documentation | 6/10 | Missing godoc on several public APIs |
| **TOTAL** | **66/100** | |

---

# Section 1: AUDIT — Security and Code Quality Issues

## CRIT-1: Webhook Timestamp Replay — No Age Validation in VerifyPayload

**File:** `webhook/webhook.go` ~lines 139-156
**Severity:** Critical | **Confidence:** 92%

`VerifyPayload` verifies the HMAC signature over `timestamp + "." + body`, but never checks that the timestamp is recent. An attacker who intercepts a valid webhook delivery can replay it indefinitely — the HMAC will always verify because the payload has not changed. This defeats the entire purpose of the timestamp-based signing scheme.

```diff
 func VerifyPayload(headers http.Header, body []byte, secret string) ([]byte, error) {
 	sig := headers.Get("X-Webhook-Signature")
 	timestamp := headers.Get("X-Webhook-Timestamp")
 	if sig == "" || timestamp == "" {
 		return nil, ErrBadSignature
 	}

+	// Reject replays: timestamp must be within 5 minutes of now.
+	ts, err := strconv.ParseInt(timestamp, 10, 64)
+	if err != nil {
+		return nil, ErrBadSignature
+	}
+	age := time.Now().Unix() - ts
+	if age < -30 || age > 300 { // 5-minute window, allow 30s clock skew
+		return nil, ErrBadSignature
+	}
+
 	// Strip "sha256=" prefix
 	if len(sig) > 7 && sig[:7] == "sha256=" {
 		sig = sig[7:]
 	}
```

---

## CRIT-2: Path Traversal via RunHook Name

**File:** `deploy/deploy.go` ~lines 315-328
**Severity:** Critical | **Confidence:** 88%

`RunHook` uses a prefix check to prevent path traversal, but `name` is unsanitized. On Windows `os.PathSeparator` is `\` but `filepath.Join` accepts `/`, so the prefix check can be bypassed. The fix is to reject names with path separators before joining.

```diff
 func (d *Deploy) RunHook(name string) error {
 	if !d.found {
 		return nil
 	}
+	// Reject names with any path component separator.
+	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
+		return nil // silently ignore — callers control the name
+	}
 	hooksDir := filepath.Join(d.dir, "hooks")
 	hookPath := filepath.Join(hooksDir, name)
+	hookPath = filepath.Clean(hookPath)
 	if !strings.HasPrefix(hookPath, hooksDir+string(os.PathSeparator)) {
 		return nil // path traversal attempt
 	}
```

---

## CRIT-3: CachedToken Holds Mutex During Network Call

**File:** `call/token.go` ~lines 44-58
**Severity:** Critical | **Confidence:** 95%

`Token()` acquires `ct.mu.Lock()` and then calls `ct.fetch(ctx)`, which typically makes a network request. All concurrent callers serialize behind the lock for the full RTT. Worse, context cancellation cannot interrupt blocked goroutines waiting for the lock.

```diff
 func (ct *CachedToken) Token(ctx context.Context) (string, error) {
-	ct.mu.Lock()
-	defer ct.mu.Unlock()
-
-	if ct.token != "" && time.Now().Add(ct.leeway).Before(ct.expires) {
-		return ct.token, nil
+	ct.mu.Lock()
+	if ct.token != "" && time.Now().Add(ct.leeway).Before(ct.expires) {
+		t := ct.token
+		ct.mu.Unlock()
+		return t, nil
 	}
-
-	token, expires, err := ct.fetch(ctx)
+	ct.mu.Unlock()
+
+	// Fetch outside the lock — this may be slow.
+	token, expires, err := ct.fetch(ctx)
 	if err != nil {
 		return "", err
 	}
+
+	ct.mu.Lock()
+	defer ct.mu.Unlock()
+	// Re-check: another goroutine may have refreshed while we were fetching.
+	if ct.token != "" && time.Now().Add(ct.leeway).Before(ct.expires) {
+		return ct.token, nil
+	}
 	ct.token = token
 	ct.expires = expires
 	return token, nil
 }
```

---

## HIGH-1: URL Path Injection in XYOps API Methods

**File:** `xyops/xyops.go` ~lines 163-243
**Severity:** High | **Confidence:** 90%

`RunEvent`, `GetJobStatus`, `CancelJob`, `GetEvent`, and `AckAlert` all concatenate caller-supplied IDs directly into URL paths without sanitization. An `eventID` of `../../../admin/delete` can manipulate the path.

```diff
+import "net/url"

 func (c *Client) RunEvent(ctx context.Context, eventID string, params map[string]string) (string, error) {
-	resp, err := c.apiRequest(ctx, "POST", "/api/events/"+eventID+"/run", params)
+	resp, err := c.apiRequest(ctx, "POST", "/api/events/"+url.PathEscape(eventID)+"/run", params)
```

Apply the same `url.PathEscape` to `jobID` in `GetJobStatus`/`CancelJob`, `alertID` in `AckAlert`, and `eventID` in `GetEvent`.

---

## HIGH-2: Kafkakit Subscriber Data Race on `s.client`

**File:** `kafkakit/subscriber.go` ~lines 88-143
**Severity:** High | **Confidence:** 88%

`Start()` sets `s.client = client` without holding `s.mu`. `Close()` reads `s.client` without a lock. `routeToDLQ()` reads `s.client` without a lock. This is an unsynchronized concurrent read/write.

```diff
+	s.mu.Lock()
 	s.client = client
 	s.healthy.Store(true)
+	s.mu.Unlock()
```

And in `Close`:
```diff
 func (s *Subscriber) Close() error {
 	s.healthy.Store(false)
+	s.mu.Lock()
+	cl := s.client
+	s.mu.Unlock()
-	if s.client != nil {
-		s.client.Close()
+	if cl != nil {
+		cl.Close()
 	}
 	return nil
 }
```

---

## HIGH-3: Webhook Sender Mutates Delivery Outside Lock

**File:** `webhook/webhook.go` ~lines 70-125
**Severity:** High | **Confidence:** 85%

`Send` creates a `Delivery`, stores it in `s.deliveries` under `s.mu.Lock()`, then mutates `delivery.Attempts`, `delivery.Status`, `delivery.LastError` *outside* the lock during the retry loop. Concurrently, `Status()` acquires `s.mu.Lock()` and reads the same struct — a data race.

```diff
-    delivery.Attempts = attempt
+    s.mu.Lock()
+    delivery.Attempts = attempt
+    s.mu.Unlock()
```

Apply same pattern to `delivery.Status` and `delivery.LastError` assignments.

---

## HIGH-4: OTel Init Silently Drops All Telemetry on Trace Exporter Failure

**File:** `otel/otel.go` ~lines 78-82
**Severity:** High | **Confidence:** 85%

When `otlptracegrpc.New` fails, `Init` logs an error and returns a no-op `ShutdownFunc`. It also never registers the MeterProvider, so metrics are silently disabled. The metric failure path only disables metrics, but the trace failure path skips everything. The service runs with zero observability and only one startup log line.

```diff
 if err != nil {
-    slog.Error("trace exporter creation failed, all telemetry disabled", "error", err)
-    return func(context.Context) error { return nil }
+    slog.Warn("trace exporter failed — attempting metrics-only mode", "error", err)
+    // Continue to set up the MeterProvider even without traces.
 }
```

---

## HIGH-5: MaxBody Content-Length Check Easily Bypassed

**File:** `guard/maxbody.go` ~lines 18-21
**Severity:** High | **Confidence:** 82%

The middleware checks `r.ContentLength > maxBytes` for early rejection, but `Content-Length` is client-controlled. Omitting it (chunked encoding) results in `r.ContentLength == -1`, bypassing the early check. While `MaxBytesReader` still enforces the limit, the early check provides false confidence.

```diff
 return func(next http.Handler) http.Handler {
     return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
-        if r.ContentLength > maxBytes {
-            writeProblem(w, r, errors.PayloadTooLargeError("request body too large"))
-            return
-        }
         if r.Body != nil {
             r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
         }
         next.ServeHTTP(w, r)
     })
 }
```

---

## MED-1: Metrics Counter/Histogram Silently Discard Creation Errors

**File:** `metrics/metrics.go` ~lines 190-208
**Severity:** Medium | **Confidence:** 82%

`Counter()` and `Histogram()` use blank identifiers for the error from `meter.Float64Counter` / `meter.Float64Histogram`. If the meter name is invalid, the instrument is a no-op with no diagnostics.

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

---

## MED-2: Registry `appendLogLocked` Silently Drops Write Errors

**File:** `registry/registry.go` ~line 666
**Severity:** Medium | **Confidence:** 85%

`logFile.Write(append(data, '\n'))` discards the return value. On a full disk, log entries are silently lost.

```diff
-	logFile.Write(append(data, '\n'))
+	if _, err := logFile.Write(append(data, '\n')); err != nil {
+		fmt.Fprintf(os.Stderr, "registry: log write failed: %v\n", err)
+	}
```

---

## MED-3: Config `validateField` Panics on Invalid Regex Pattern

**File:** `config/config.go` ~line 191
**Severity:** Medium | **Confidence:** 80%

`validateField` calls `regexp.MustCompile(value)` which panics on an invalid pattern rather than returning a config error.

```diff
 case "pattern":
-    re := regexp.MustCompile(value)
+    re, err := regexp.Compile(value)
+    if err != nil {
+        panic(fmt.Sprintf("config: field %s has invalid pattern %q in validate tag: %v", name, value, err))
+    }
     actual := fmt.Sprintf("%v", val.Interface())
     if !re.MatchString(actual) {
```

---

## MED-4: HSTS Sent on Spoofable X-Forwarded-Proto Header

**File:** `guard/secheaders.go` ~line 73
**Severity:** Medium | **Confidence:** 80%

`X-Forwarded-Proto` is client-controlled. Any client can send `X-Forwarded-Proto: https` to trigger HSTS on an HTTP response without trusted-proxy validation.

---

## MED-5: Lifecycle Passes Wrong Context to heartbeatkit/announcekit

**File:** `lifecycle/lifecycle.go` ~lines 134-143
**Severity:** Medium | **Confidence:** 82%

`heartbeatkit.Start(ctx, ...)` receives the original `ctx`, not `signalCtx`. On SIGTERM, `signalCtx` is cancelled but heartbeat goroutines continue running until the caller's context expires.

```diff
-    heartbeatkit.Start(ctx, pub, heartbeatkit.Config{
+    heartbeatkit.Start(signalCtx, pub, heartbeatkit.Config{
```

---

## LOW-1: Retry-After Header Hardcoded to "1"

**File:** `guard/ratelimit.go` ~line 119
**Severity:** Low | **Confidence:** 80%

`Retry-After: 1` is sent regardless of the actual window size. A 60-second window causes 59 unnecessary 429 responses.

```diff
-w.Header().Set("Retry-After", "1")
+retryAfter := int(lim.window.Seconds())
+if retryAfter < 1 {
+    retryAfter = 1
+}
+w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
```

---

## LOW-2: Recovery Middleware Only Unwraps One Layer of responseWriter

**File:** `httpkit/middleware.go` ~lines 136-139
**Severity:** Low | **Confidence:** 80%

If `Timeout` middleware wraps `w` before `Recovery`, the type assertion `w.(*responseWriter)` fails and `Recovery` creates a redundant wrapper. Writes to the original `ResponseWriter` are not detected by `headerWritten`.

---

## LOW-3: schemakit Uses `http.DefaultClient` — No Timeout, No Retry

**File:** `schemakit/schemakit.go` ~line 199
**Severity:** Low | **Confidence:** 85%

The only place in the entire codebase using `http.DefaultClient`. No timeout, no retry, no OTel tracing for schema registration calls.

---

# Section 2: TESTS — Proposed Unit Tests for Untested Code

## TEST-1: `call` — WithHTTPClient Option and cancelBody.Close Lifecycle

**Untested:** `WithHTTPClient()` is never exercised. `cancelBody.Close()` cancel lifecycle not directly verified.

```diff
--- a/call/call_test.go
+++ b/call/call_test.go
@@ -end of file
+
+func TestWithHTTPClientUsesProvidedTransport(t *testing.T) {
+	var transportCalled atomic.Int32
+	custom := &http.Client{
+		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
+			transportCalled.Add(1)
+			return &http.Response{
+				StatusCode: http.StatusOK,
+				Body:       http.NoBody,
+			}, nil
+		}),
+	}
+
+	c := New(WithHTTPClient(custom))
+	req, _ := http.NewRequest(http.MethodGet, "http://example.internal/", nil)
+	resp, err := c.Do(req)
+	if err != nil {
+		t.Fatalf("Do: %v", err)
+	}
+	resp.Body.Close()
+
+	if transportCalled.Load() != 1 {
+		t.Fatalf("expected custom transport called once, got %d", transportCalled.Load())
+	}
+}
+
+type roundTripFunc func(*http.Request) (*http.Response, error)
+
+func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
+
+func TestCancelBodyCloseCallsCancel(t *testing.T) {
+	var cancelCalls atomic.Int32
+	cancelFn := func() { cancelCalls.Add(1) }
+
+	cb := &cancelBody{
+		ReadCloser: http.NoBody,
+		cancel:     cancelFn,
+	}
+
+	if err := cb.Close(); err != nil {
+		t.Fatalf("Close() error: %v", err)
+	}
+	if cancelCalls.Load() != 1 {
+		t.Fatalf("expected cancel called once, got %d", cancelCalls.Load())
+	}
+}
```

---

## TEST-2: `config` — Nested Struct Recursion and Invalid Validate Tag

**Untested:** `loadFields` nested struct recursion. `validateField` with malformed `min` value.

```diff
--- a/config/config_test.go
+++ b/config/config_test.go
@@ -end of file
+
+type dbConfig struct {
+	Host string `env:"DB_HOST"`
+	Port int    `env:"DB_PORT" default:"5432"`
+}
+
+type serviceConfig struct {
+	Name string   `env:"SVC_NAME"`
+	DB   dbConfig
+}
+
+func TestMustLoad_NestedStruct(t *testing.T) {
+	t.Setenv("SVC_NAME", "my-service")
+	t.Setenv("DB_HOST", "db.internal")
+
+	cfg := MustLoad[serviceConfig]()
+
+	if cfg.Name != "my-service" {
+		t.Errorf("Name = %q, want %q", cfg.Name, "my-service")
+	}
+	if cfg.DB.Host != "db.internal" {
+		t.Errorf("DB.Host = %q, want %q", cfg.DB.Host, "db.internal")
+	}
+	if cfg.DB.Port != 5432 {
+		t.Errorf("DB.Port = %d, want 5432 (default)", cfg.DB.Port)
+	}
+}
+
+func TestMustLoad_NestedStructMissingRequired(t *testing.T) {
+	defer func() {
+		r := recover()
+		if r == nil {
+			t.Fatal("expected panic for missing required nested env var")
+		}
+		msg, ok := r.(string)
+		if !ok {
+			t.Fatalf("panic value is not string: %v", r)
+		}
+		if !strings.Contains(msg, "DB_HOST") {
+			t.Errorf("panic message %q does not mention DB_HOST", msg)
+		}
+	}()
+	_ = MustLoad[serviceConfig]()
+}
```

---

## TEST-3: `seal` — Corrupted Envelope Fields and Malformed Token

**Untested:** Each of the four base64-decode corruption paths in `Decrypt`. Token with no dot separator. Token missing `exp` claim.

```diff
--- a/seal/seal_test.go
+++ b/seal/seal_test.go
@@ -imports
+	"errors"
+
@@ -end of file
+
+func TestDecryptCorruptedSalt(t *testing.T) {
+	env, _ := seal.Encrypt([]byte("data"), "passphrase-long-enough-here!!")
+	env.Salt = "!!!not-base64!!!"
+	_, err := seal.Decrypt(env, "passphrase-long-enough-here!!")
+	if err == nil {
+		t.Fatal("expected error for corrupted salt")
+	}
+	if !errors.Is(err, seal.ErrDecrypt) {
+		t.Fatalf("expected ErrDecrypt, got %v", err)
+	}
+}
+
+func TestDecryptCorruptedIV(t *testing.T) {
+	env, _ := seal.Encrypt([]byte("data"), "passphrase-long-enough-here!!")
+	env.IV = "!!!not-base64!!!"
+	_, err := seal.Decrypt(env, "passphrase-long-enough-here!!")
+	if err == nil {
+		t.Fatal("expected error for corrupted IV")
+	}
+	if !errors.Is(err, seal.ErrDecrypt) {
+		t.Fatalf("expected ErrDecrypt, got %v", err)
+	}
+}
+
+func TestDecryptCorruptedTag(t *testing.T) {
+	env, _ := seal.Encrypt([]byte("data"), "passphrase-long-enough-here!!")
+	env.Tag = "!!!not-base64!!!"
+	_, err := seal.Decrypt(env, "passphrase-long-enough-here!!")
+	if err == nil {
+		t.Fatal("expected error for corrupted tag")
+	}
+	if !errors.Is(err, seal.ErrDecrypt) {
+		t.Fatalf("expected ErrDecrypt, got %v", err)
+	}
+}
+
+func TestDecryptCorruptedCiphertext(t *testing.T) {
+	env, _ := seal.Encrypt([]byte("data"), "passphrase-long-enough-here!!")
+	env.CT = "!!!not-base64!!!"
+	_, err := seal.Decrypt(env, "passphrase-long-enough-here!!")
+	if err == nil {
+		t.Fatal("expected error for corrupted ciphertext")
+	}
+	if !errors.Is(err, seal.ErrDecrypt) {
+		t.Fatalf("expected ErrDecrypt, got %v", err)
+	}
+}
+
+func TestValidateTokenNoDot(t *testing.T) {
+	_, err := seal.ValidateToken("nodothereatall", "secret")
+	if err == nil {
+		t.Fatal("expected error for token with no separator")
+	}
+	if !errors.Is(err, seal.ErrTokenInvalid) {
+		t.Fatalf("expected ErrTokenInvalid, got %v", err)
+	}
+}
```

---

## TEST-4: `tick` — Zero Interval, Label Option, Jitter Context Cancellation

**Untested:** `Every` with zero/negative interval. `Label()` and `Jitter()` option constructors.

```diff
--- a/tick/tick_test.go
+++ b/tick/tick_test.go
@@ -end of file
+
+func TestEveryZeroIntervalReturnsError(t *testing.T) {
+	component := tick.Every(0, func(_ context.Context) error { return nil })
+	err := component(context.Background())
+	if err == nil {
+		t.Fatal("expected error for zero interval")
+	}
+}
+
+func TestEveryNegativeIntervalReturnsError(t *testing.T) {
+	component := tick.Every(-1*time.Second, func(_ context.Context) error { return nil })
+	err := component(context.Background())
+	if err == nil {
+		t.Fatal("expected error for negative interval")
+	}
+}
+
+func TestLabelOptionDoesNotPanic(t *testing.T) {
+	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
+	defer cancel()
+
+	var count atomic.Int32
+	component := tick.Every(
+		10*time.Millisecond,
+		func(_ context.Context) error { count.Add(1); return nil },
+		tick.Label("my-ticker"),
+	)
+	_ = component(ctx)
+
+	if count.Load() < 1 {
+		t.Fatal("expected at least one tick with Label option")
+	}
+}
+
+func TestJitterOptionRespectsContextCancellation(t *testing.T) {
+	ctx, cancel := context.WithCancel(context.Background())
+	component := tick.Every(
+		10*time.Millisecond,
+		func(_ context.Context) error { return nil },
+		tick.Jitter(5*time.Second),
+	)
+
+	done := make(chan error, 1)
+	go func() { done <- component(ctx) }()
+
+	time.Sleep(25 * time.Millisecond)
+	cancel()
+
+	select {
+	case <-done:
+		// Good
+	case <-time.After(300 * time.Millisecond):
+		t.Fatal("Every did not respect context cancellation during jitter sleep")
+	}
+}
```

---

## TEST-5: `webhook` — Missing Headers, Unknown Status ID, All Retries Exhausted

**Untested:** `VerifyPayload` with one header missing. `Status` for unknown ID. `Send` exhausting all retries.

```diff
--- a/webhook/webhook_test.go
+++ b/webhook/webhook_test.go
@@ -imports
+	"errors"
+
@@ -end of file
+
+func TestVerifyPayloadMissingSignatureHeader(t *testing.T) {
+	headers := http.Header{}
+	headers.Set("X-Webhook-Timestamp", "1700000000")
+	_, err := webhook.VerifyPayload(headers, []byte("body"), "secret")
+	if err == nil {
+		t.Fatal("expected error when X-Webhook-Signature is absent")
+	}
+	if !errors.Is(err, webhook.ErrBadSignature) {
+		t.Fatalf("expected ErrBadSignature, got %v", err)
+	}
+}
+
+func TestVerifyPayloadMissingTimestampHeader(t *testing.T) {
+	headers := http.Header{}
+	headers.Set("X-Webhook-Signature", "sha256=abc123")
+	_, err := webhook.VerifyPayload(headers, []byte("body"), "secret")
+	if err == nil {
+		t.Fatal("expected error when X-Webhook-Timestamp is absent")
+	}
+	if !errors.Is(err, webhook.ErrBadSignature) {
+		t.Fatalf("expected ErrBadSignature, got %v", err)
+	}
+}
+
+func TestStatusUnknownID(t *testing.T) {
+	sender := webhook.NewSender()
+	_, ok := sender.Status("does-not-exist")
+	if ok {
+		t.Fatal("expected Status to return false for unknown delivery ID")
+	}
+}
+
+func TestSendAllRetriesExhausted(t *testing.T) {
+	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		w.WriteHeader(http.StatusInternalServerError)
+	}))
+	defer srv.Close()
+
+	sender := webhook.NewSender(webhook.MaxAttempts(2))
+	_, err := sender.Send(srv.URL, "payload", "secret")
+	if err == nil {
+		t.Fatal("expected error when all retries exhausted")
+	}
+}
```

---

## TEST-6: `work` — Map Pre-Cancelled Context, Stream Mid-Stream Cancel

**Untested:** `Map` with pre-cancelled context. `Stream` context cancelled while input channel has items.

```diff
--- a/work/work_test.go
+++ b/work/work_test.go
@@ -end of file
+
+func TestMap_PreCancelledContext(t *testing.T) {
+	ctx, cancel := context.WithCancel(context.Background())
+	cancel()
+
+	items := []int{1, 2, 3, 4, 5}
+	var executed atomic.Int32
+
+	_, err := Map(ctx, items, func(_ context.Context, n int) (int, error) {
+		executed.Add(1)
+		return n, nil
+	}, Workers(2))
+
+	if err == nil {
+		t.Fatal("expected error from pre-cancelled context")
+	}
+
+	var workErrs *Errors
+	if !errors.As(err, &workErrs) {
+		t.Fatalf("expected *Errors, got %T: %v", err, err)
+	}
+
+	var ctxErrCount int
+	for _, f := range workErrs.Failures {
+		if errors.Is(f.Err, context.Canceled) {
+			ctxErrCount++
+		}
+	}
+	if ctxErrCount == 0 {
+		t.Fatal("expected at least one context.Canceled failure")
+	}
+}
+
+func TestStream_ContextCancelledMidStream(t *testing.T) {
+	in := make(chan int, 20)
+	for i := range 20 {
+		in <- i
+	}
+
+	ctx, cancel := context.WithCancel(context.Background())
+
+	var processed atomic.Int32
+	out := Stream(ctx, in, func(_ context.Context, n int) (int, error) {
+		processed.Add(1)
+		time.Sleep(20 * time.Millisecond)
+		return n, nil
+	}, Workers(2))
+
+	time.Sleep(30 * time.Millisecond)
+	cancel()
+
+	count := 0
+	for range out {
+		count++
+	}
+
+	if processed.Load() == 20 {
+		t.Fatal("expected Stream to stop processing after cancellation")
+	}
+	close(in)
+}
```

---

## TEST-7: `cache` — Prune With No TTL, MaxSize Zero Clamp

**Untested:** `Prune()` on a cache without TTL. `New` with `MaxSize(0)`.

```diff
--- a/cache/cache_test.go
+++ b/cache/cache_test.go
@@ -end of file
+
+func TestPruneWithNoTTLReturnsZero(t *testing.T) {
+	c := cache.New[string, string](cache.MaxSize(10))
+	c.Set("a", "1")
+	c.Set("b", "2")
+
+	removed := c.Prune()
+	if removed != 0 {
+		t.Fatalf("expected 0 pruned (no TTL), got %d", removed)
+	}
+	if c.Len() != 2 {
+		t.Fatalf("expected len=2 after Prune with no TTL, got %d", c.Len())
+	}
+}
+
+func TestNewMaxSizeZeroClampedToOne(t *testing.T) {
+	c := cache.New[string, int](cache.MaxSize(0))
+
+	c.Set("a", 1)
+	if c.Len() != 1 {
+		t.Fatalf("expected len=1 after first Set, got %d", c.Len())
+	}
+
+	c.Set("b", 2)
+	if c.Len() != 1 {
+		t.Fatalf("expected len=1 after second Set (eviction), got %d", c.Len())
+	}
+	if _, ok := c.Get("a"); ok {
+		t.Fatal("expected 'a' to be evicted when maxSize is 1")
+	}
+}
```

---

## TEST-8: `lifecycle` — RunComponents Typed Wrapper, resolveName Paths

**Untested:** `RunComponents` never called in tests. `resolveName` env-var vs fallback branches.

```diff
--- a/lifecycle/lifecycle_test.go
+++ b/lifecycle/lifecycle_test.go
@@ -end of file
+
+func TestRunComponentsTypedWrapper(t *testing.T) {
+	var executed atomic.Int32
+
+	comp1 := Component(func(ctx context.Context) error {
+		executed.Add(1)
+		return nil
+	})
+	comp2 := Component(func(ctx context.Context) error {
+		executed.Add(1)
+		return nil
+	})
+
+	err := RunComponents(context.Background(), comp1, comp2)
+	if err != nil {
+		t.Fatalf("RunComponents returned unexpected error: %v", err)
+	}
+	if n := executed.Load(); n != 2 {
+		t.Fatalf("expected 2 components executed, got %d", n)
+	}
+}
+
+func TestResolveName_FallsBackToWorkDir(t *testing.T) {
+	t.Setenv("CHASSIS_SERVICE_NAME", "")
+
+	name := resolveName()
+	if name == "" {
+		t.Fatal("expected non-empty name from working directory basename")
+	}
+}
+
+func TestResolveName_UsesEnvVar(t *testing.T) {
+	t.Setenv("CHASSIS_SERVICE_NAME", "override-svc")
+
+	name := resolveName()
+	if name != "override-svc" {
+		t.Fatalf("expected override-svc, got %q", name)
+	}
+}
```

---

## TEST-9: `grpckit` — grpcCodeFromError Non-gRPC Error, metadataCarrier Methods

**Untested:** `grpcCodeFromError` with plain error. `metadataCarrier.Set` and `.Keys`.

```diff
--- a/grpckit/grpckit_test.go
+++ b/grpckit/grpckit_test.go
@@ -end of file
+
+func TestGRPCCodeFromError_Nil(t *testing.T) {
+	if got := grpcCodeFromError(nil); got != codes.OK {
+		t.Fatalf("grpcCodeFromError(nil) = %v, want OK", got)
+	}
+}
+
+func TestGRPCCodeFromError_PlainError(t *testing.T) {
+	err := errors.New("plain error")
+	if got := grpcCodeFromError(err); got != codes.Unknown {
+		t.Fatalf("grpcCodeFromError(plain) = %v, want Unknown", got)
+	}
+}
+
+func TestGRPCCodeFromError_GRPCStatus(t *testing.T) {
+	for _, tc := range []struct {
+		code codes.Code
+	}{
+		{codes.NotFound},
+		{codes.PermissionDenied},
+		{codes.Internal},
+	} {
+		t.Run(tc.code.String(), func(t *testing.T) {
+			err := status.Error(tc.code, "msg")
+			if got := grpcCodeFromError(err); got != tc.code {
+				t.Fatalf("got %v, want %v", got, tc.code)
+			}
+		})
+	}
+}
+
+func TestMetadataCarrier_SetAndKeys(t *testing.T) {
+	md := metadata.New(map[string]string{"existing": "value"})
+	carrier := metadataCarrier{md: md}
+
+	carrier.Set("new-key", "new-value")
+	if got := carrier.Get("new-key"); got != "new-value" {
+		t.Fatalf("Get after Set: got %q, want %q", got, "new-value")
+	}
+
+	keys := carrier.Keys()
+	if len(keys) < 2 {
+		t.Fatalf("expected at least 2 keys, got %d", len(keys), keys)
+	}
+}
```

---

## TEST-10: `metrics` — pairsToCombo With Odd-Length Pairs

**Untested:** `pairsToCombo` with trailing key (no value).

```diff
--- a/metrics/metrics_test.go
+++ b/metrics/metrics_test.go
@@ -end of file
+
+func TestPairsToComboOddLength(t *testing.T) {
+	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
+	r := New("oddtest", logger)
+	counter := r.Counter("events")
+
+	ctx := context.Background()
+	// Odd-length pairs: "type" has no value — should not panic.
+	counter.Add(ctx, 1, "env", "prod", "type")
+	counter.Add(ctx, 1, "env", "prod", "type")
+	// If no panic, test passes.
+}
```

---

# Section 3: FIXES — Bugs, Issues, and Code Smells

## FIX-1: `guard/ratelimit.go` — Retry-After Always "1" Regardless of Window

**Severity:** Low | **Impact:** Protocol non-compliance, client retry storms

Clients with a 60-second rate limit window are told `Retry-After: 1`, generating 59 wasted 429 requests.

```diff
-w.Header().Set("Retry-After", "1")
+retryAfter := int(lim.window.Seconds())
+if retryAfter < 1 {
+    retryAfter = 1
+}
+w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
```

---

## FIX-2: `registrykit` — Double JSON Conversion via `strings.NewReader(string(body))`

**File:** `registrykit/registrykit.go` ~lines 372, 405, 428, 456
**Severity:** Low | **Impact:** Unnecessary allocation

All four mutation methods marshal to `[]byte`, convert to `string`, then wrap in `strings.NewReader`. Should use `bytes.NewReader(body)`.

```diff
-	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
+	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
```

---

## FIX-3: `deploy/deploy.go` — deploy.json Read on Every Method Call

**File:** `deploy/deploy.go`
**Severity:** Medium | **Impact:** Performance — triple file reads per `Health()` call

`Spec()`, `Meta()`, `Endpoints()`, `Dependencies()`, `Environment()` all read and parse `deploy.json` from disk on every call. A single `Health()` call triggers 3 reads.

```diff
+// Add sync.Once field to Deploy struct
+type Deploy struct {
+    dir   string
+    found bool
+    spec  *DeploySpec
+    once  sync.Once
+}
+
+func (d *Deploy) loadSpec() *DeploySpec {
+    d.once.Do(func() {
+        data, err := os.ReadFile(filepath.Join(d.dir, "deploy.json"))
+        if err != nil { return }
+        var spec DeploySpec
+        if json.Unmarshal(data, &spec) == nil {
+            d.spec = &spec
+        }
+    })
+    return d.spec
+}
```

---

## FIX-4: `guard/ipfilter.go` — "whitelist/blacklist" Terminology in Comments

**File:** `guard/ipfilter.go` ~line 14
**Severity:** Low | **Impact:** Naming inconsistency

Comments say "whitelist/blacklist" while field names use "Allow/Deny".

```diff
-	Allow []string `json:"allow"` // CIDR notation whitelist
-	Deny  []string `json:"deny"`  // CIDR notation blacklist
+	Allow []string `json:"allow"` // CIDR notation allow list
+	Deny  []string `json:"deny"`  // CIDR notation deny list
```

---

## FIX-5: `xyopsworker/worker.go` — Run() Is a Silent No-Op Stub

**File:** `xyopsworker/worker.go` ~lines 113-127
**Severity:** High | **Impact:** Services wiring xyopsworker into lifecycle.Run get a component that never connects

`Worker.Run` contains an explicit placeholder comment and immediately blocks on `<-ctx.Done()`. Any service using this is silently doing nothing.

Recommended: Either implement the WebSocket connection or replace with `ErrNotImplemented` to fail loudly.

---

# Section 4: REFACTOR — Opportunities to Improve Code Quality

## R-01: Extract Shared Service Client Base (High Priority)

**Packages:** `graphkit`, `registrykit`, `lakekit`

All three packages duplicate identical `Client` structs, `checkStatus()` functions, `setHeaders()` methods, `WithTenant()`/`WithTimeout()` options, and `NewClient()` constructors. ~80 lines of structural duplication each.

**Recommendation:** Extract `internal/svclient` with a `BaseClient` struct. The three `*kit` packages embed or wrap it. This also enables swapping the raw `*http.Client` for `call.Client` to get retry/circuit-breaker/tracing for free.

---

## R-02: Deduplicate `djb2Port()` and `resolveName()` (High Priority)

**Packages:** `chassis.go` + `registry/registry.go`, `lifecycle/lifecycle.go` + `registry/registry.go`

Both functions are byte-for-byte identical across packages. The stated reason (avoiding root import) creates maintenance hazard.

**Recommendation:** Move to `internal/nameutil` so both can import without creating a cycle.

---

## R-03: Replace Singleton Pattern in heartbeatkit (High Priority)

**Package:** `heartbeatkit`

Module-level `stopCh` and `mu` make the package a global singleton. Cannot run two heartbeats, cannot test in parallel.

**Recommendation:** Replace with `*Heartbeat` struct carrying its own stop channel. Consistent with `call.Client`, `cache.Cache`, `webhook.Sender`.

---

## R-04: Replace Global State in announcekit (High Priority)

**Package:** `announcekit`

`SetServiceName(name)` sets a package-level variable. Two services in one process get last-write-wins.

**Recommendation:** Replace with `type Client struct { serviceName string }`. Event functions become methods.

---

## R-05: Deduplicate Map/All Goroutine Pool Logic (Medium Priority)

**Package:** `work`

`Map` and `All` share identical semaphore + WaitGroup + failure collection machinery (~60 lines each).

**Recommendation:** Extract private `runPool[T any]` helper. Both functions call it.

---

## R-06: Deduplicate Init/InitCLI Setup in Registry (Medium Priority)

**Package:** `registry`

`Init` and `InitCLI` share ~80 lines of identical directory/file setup logic.

**Recommendation:** Extract `initBase(mode string) error` with shared logic.

---

## R-07: Standardize Option Type Names (Medium Priority)

**Packages:** Multiple

Inconsistent naming: `Option` vs `ClientOption` vs `SubscriberOption` for the same pattern.

**Recommendation:** Use `Option` for single-constructor packages. Reserve prefixed names for operation-scoped options.

---

## R-08: Simplify logz traceHandler Group Reconstruction (Medium Priority)

**Package:** `logz`

The `Handle` method maintains parallel handler chains and reconstructs group structures from scratch on every log call.

**Recommendation:** Evaluate if the "trace_id at top level when groups active" invariant justifies the complexity. If rare, document the limitation instead.

---

## R-09: Service Client Packages Should Use call.Client (Low Priority)

**Packages:** `graphkit`, `registrykit`, `lakekit`

All three use bare `*http.Client`, bypassing the toolkit's retry, circuit breaker, timeout, and OTel tracing.

**Recommendation:** Use `call.Client` as underlying transport.

---

## R-10: Cache deploy.json Reads (Medium Priority)

**Package:** `deploy`

`deploy.json` parsed from disk on every accessor call. `Health()` triggers 3 reads.

**Recommendation:** Lazy parse with `sync.Once`. Safe since `Deploy` is a startup-time value object.

---

*End of report.*
