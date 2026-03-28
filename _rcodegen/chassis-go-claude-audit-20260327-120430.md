Date Created: 2026-03-27T12:04:30-07:00
TOTAL_SCORE: 61/100

# chassis-go Comprehensive Security & Code Quality Audit

**Auditor:** Claude Code (Claude:Opus 4.6)
**Module:** `github.com/ai8future/chassis-go/v10`
**Go Version:** 1.25.5
**Packages Audited:** 31 packages, ~60 source files, ~35 test files

---

## Scoring Breakdown

| Category | Score | Notes |
|----------|-------|-------|
| **Security** | 16/30 | Exploitable vulns in webhook, ratelimit, deploy; many data races |
| **Correctness** | 15/25 | Core logic solid; edge-case races in 8+ packages |
| **Code Quality** | 11/15 | Good Go idioms; global mutable state pattern is pervasive |
| **Testing** | 10/15 | Core coverage good; newer packages under-tested; some tests validate bugs |
| **Documentation** | 9/15 | Good godoc overall; stale comments; undocumented limitations |
| **TOTAL** | **61/100** | |

---

## Executive Summary

The core toolkit packages (config, lifecycle, logz, flagz, health, work) are well-designed and follow sound Go patterns. The "toolkit, not framework" philosophy is well-executed with clean package boundaries and zero cross-dependencies. Version gating via `RequireMajor`/`AssertVersionChecked` is a strong safety net.

However, the codebase has **significant security vulnerabilities** in the guard, webhook, and deploy packages, **pervasive data-race risks** from unsynchronized global state in announcekit/heartbeatkit/schemakit, and an **exploitable rate-limit bypass** in the guard package. The newer packages (xyopsworker, webhook, schemakit) are notably less mature than the core.

---

## Critical Findings (Fix Immediately)

### CRIT-1: Webhook Replay Attack — No Timestamp Validation in `VerifyPayload`
**File:** `webhook/webhook.go:138-157`
**Severity:** Critical (Security)

`VerifyPayload` includes the timestamp in the signed payload but never validates that the timestamp is recent. An attacker who captures a valid webhook delivery can replay it indefinitely.

```go
// CURRENT (webhook/webhook.go:138-157)
func VerifyPayload(headers http.Header, body []byte, secret string) ([]byte, error) {
    sig := headers.Get("X-Webhook-Signature")
    timestamp := headers.Get("X-Webhook-Timestamp")
    if sig == "" || timestamp == "" {
        return nil, ErrBadSignature
    }
    if len(sig) > 7 && sig[:7] == "sha256=" {
        sig = sig[7:]
    }
    sigPayload := timestamp + "." + string(body)
    if !seal.Verify([]byte(sigPayload), sig, secret) {
        return nil, ErrBadSignature
    }
    return body, nil
}
```

**Patch-ready diff:**
```diff
--- a/webhook/webhook.go
+++ b/webhook/webhook.go
@@ -1,6 +1,7 @@
 package webhook

 import (
+	"bytes"
 	"crypto/rand"
 	"encoding/hex"
 	"encoding/json"
@@ -18,8 +19,12 @@ import (
 )

 var (
-	ErrBadSignature = errors.New("webhook: signature verification failed")
-	ErrClientError  = errors.New("webhook: client error (4xx)")
-	ErrServerError  = errors.New("webhook: server error after all retries")
+	ErrBadSignature   = errors.New("webhook: signature verification failed")
+	ErrTimestampStale = errors.New("webhook: timestamp too old (possible replay)")
+	ErrClientError    = errors.New("webhook: client error (4xx)")
+	ErrServerError    = errors.New("webhook: server error after all retries")
+
+	// TimestampTolerance is the maximum age of a webhook timestamp before rejection.
+	TimestampTolerance = 5 * time.Minute
 )

@@ -138,6 +143,14 @@ func VerifyPayload(headers http.Header, body []byte, secret string) ([]byte, err
 		return nil, ErrBadSignature
 	}

+	// Reject stale timestamps to prevent replay attacks.
+	ts, err := strconv.ParseInt(timestamp, 10, 64)
+	if err != nil {
+		return nil, ErrBadSignature
+	}
+	if time.Since(time.Unix(ts, 0)).Abs() > TimestampTolerance {
+		return nil, fmt.Errorf("%w: age %v", ErrTimestampStale, time.Since(time.Unix(ts, 0)))
+	}
+
 	// Strip "sha256=" prefix
 	if len(sig) > 7 && sig[:7] == "sha256=" {
 		sig = sig[7:]
```

---

### CRIT-2: Rate-Limit Bypass via LRU Eviction
**File:** `guard/ratelimit.go:61-69`
**Severity:** Critical (Security)

When `MaxKeys` is reached and a key is evicted, the evicted key gets a **fresh full bucket** the next time it appears. An attacker can rotate between two IPs to force eviction and get unlimited fresh buckets:
1. Exhaust rate limit with IP-A
2. Send request from IP-B to force LRU eviction of IP-A
3. IP-A returns with a full bucket — repeat indefinitely

```go
// CURRENT (guard/ratelimit.go:61-69) — evicted keys get fresh full buckets
} else {
    for len(l.entries) >= l.maxKeys {
        l.evictLRU()
    }
    b := &bucket{tokens: float64(l.rate), lastFill: now}  // <-- FULL bucket
    elem := l.order.PushFront(key)
    entry = &lruEntry{key: key, bucket: b, elem: elem}
    l.entries[key] = entry
}
```

**Patch-ready diff:**
```diff
--- a/guard/ratelimit.go
+++ b/guard/ratelimit.go
@@ -33,8 +33,9 @@ type limiter struct {
-	mu      sync.Mutex
-	entries map[string]*lruEntry
-	order   *list.List // front=MRU, back=LRU
-	rate    int
-	window  time.Duration
-	maxKeys int
+	mu        sync.Mutex
+	entries   map[string]*lruEntry
+	order     *list.List // front=MRU, back=LRU
+	evicted   map[string]time.Time // tracks last-seen time for recently evicted keys
+	rate      int
+	window    time.Duration
+	maxKeys   int
 }

 func newLimiter(rate int, window time.Duration, maxKeys int) *limiter {
 	return &limiter{
-		entries: make(map[string]*lruEntry),
-		order:   list.New(),
-		rate:    rate,
-		window:  window,
-		maxKeys: maxKeys,
+		entries:   make(map[string]*lruEntry),
+		order:     list.New(),
+		evicted:   make(map[string]time.Time),
+		rate:      rate,
+		window:    window,
+		maxKeys:   maxKeys,
 	}
 }

@@ -61,7 +62,16 @@ func (l *limiter) allow(key string) bool {
 		for len(l.entries) >= l.maxKeys {
 			l.evictLRU()
 		}
-		b := &bucket{tokens: float64(l.rate), lastFill: now}
+		// If this key was recently evicted, back-calculate tokens from
+		// the time of eviction rather than granting a full bucket.
+		initialTokens := float64(l.rate)
+		if lastSeen, ok := l.evicted[key]; ok {
+			elapsed := now.Sub(lastSeen)
+			refill := elapsed.Seconds() / l.window.Seconds() * float64(l.rate)
+			initialTokens = min(refill, float64(l.rate))
+			delete(l.evicted, key)
+		}
+		b := &bucket{tokens: initialTokens, lastFill: now}
 		elem := l.order.PushFront(key)
 		entry = &lruEntry{key: key, bucket: b, elem: elem}
 		l.entries[key] = entry
@@ -87,6 +97,7 @@ func (l *limiter) evictLRU() {
 	}
 	key := back.Value.(string)
+	l.evicted[key] = time.Now()
 	l.order.Remove(back)
 	delete(l.entries, key)
 }
```

---

### CRIT-3: Webhook Delivery Map Grows Unbounded — Memory Leak
**File:** `webhook/webhook.go:71-79`
**Severity:** High (Resource Exhaustion)

Every `Send` call adds a `*Delivery` to `s.deliveries` and it is **never removed**. In a long-running service, this map grows without bound.

**Patch-ready diff:**
```diff
--- a/webhook/webhook.go
+++ b/webhook/webhook.go
@@ -34,8 +34,10 @@ type Sender struct {
 	mu          sync.Mutex
 	maxAttempts int
 	deliveries  map[string]*Delivery
+	maxHistory  int
 	httpClient  *http.Client
 }

 func NewSender(opts ...Option) *Sender {
 	s := &Sender{
 		maxAttempts: 3,
 		deliveries:  make(map[string]*Delivery),
+		maxHistory:  10000,
 		httpClient:  &http.Client{Timeout: 10 * time.Second},
 	}
@@ -77,6 +79,15 @@ func (s *Sender) Send(url string, payload any, secret string) (string, error) {
 	s.mu.Lock()
 	s.deliveries[id] = delivery
+	// Evict oldest deliveries if history exceeds limit.
+	if len(s.deliveries) > s.maxHistory {
+		var oldest string
+		var oldestTime time.Time
+		for k, d := range s.deliveries {
+			if oldest == "" || d.SentAt.Before(oldestTime) {
+				oldest, oldestTime = k, d.SentAt
+			}
+		}
+		delete(s.deliveries, oldest)
+	}
 	s.mu.Unlock()
```

---

### CRIT-4: Webhook Delivery Fields Written Without Lock — Data Race
**File:** `webhook/webhook.go:82-124`
**Severity:** High (Concurrency)

`delivery.Attempts`, `delivery.Status`, and `delivery.LastError` are written inside the retry loop without holding the mutex, while `Status()` reads them under the mutex. This is a data race.

**Patch-ready diff:**
```diff
--- a/webhook/webhook.go
+++ b/webhook/webhook.go
@@ -81,7 +81,9 @@ func (s *Sender) Send(url string, payload any, secret string) (string, error) {
 	var lastErr error
 	for attempt := 1; attempt <= s.maxAttempts; attempt++ {
-		delivery.Attempts = attempt
+		s.mu.Lock()
+		delivery.Attempts = attempt
+		s.mu.Unlock()

 		req, err := http.NewRequest("POST", url, bytes.NewReader(body))
@@ -105,7 +107,9 @@ func (s *Sender) Send(url string, payload any, secret string) (string, error) {

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
@@ -121,8 +125,10 @@ func (s *Sender) Send(url string, payload any, secret string) (string, error) {
 	}

-	delivery.Status = "failed"
-	delivery.LastError = lastErr.Error()
+	s.mu.Lock()
+	delivery.Status = "failed"
+	delivery.LastError = lastErr.Error()
+	s.mu.Unlock()
 	return "", fmt.Errorf("%w: %v", ErrServerError, lastErr)
```

---

## High Findings

### HIGH-1: Deploy `Path()` Does Not Block Path Traversal
**File:** `deploy/deploy.go:114-119`
**Severity:** High (Security)

`Path("../../etc/passwd")` resolves outside the deploy directory. `RunHook` has a traversal guard (line 321) but `Path()` does not.

**Patch-ready diff:**
```diff
--- a/deploy/deploy.go
+++ b/deploy/deploy.go
@@ -114,5 +114,9 @@ func (d *Deploy) Path(rel string) string {
 	if !d.found {
 		return ""
 	}
-	return filepath.Join(d.dir, rel)
+	joined := filepath.Join(d.dir, rel)
+	if !strings.HasPrefix(joined, d.dir+string(os.PathSeparator)) && joined != d.dir {
+		return "" // path traversal attempt
+	}
+	return joined
 }
```

---

### HIGH-2: CORS Allows `"null"` Origin with Credentials
**File:** `guard/cors.go:30-36`
**Severity:** High (Security)

The panic guard checks for `"*"` but not `"null"`. Browsers send `Origin: null` from sandboxed iframes. If an operator adds `"null"` to the allow list with credentials enabled, cookies will be sent to any sandboxed-context request.

**Patch-ready diff:**
```diff
--- a/guard/cors.go
+++ b/guard/cors.go
@@ -30,6 +30,9 @@ func CORS(cfg CORSConfig) func(http.Handler) http.Handler {
 	if cfg.AllowCredentials {
 		for _, o := range cfg.AllowOrigins {
 			if o == "*" {
 				panic("guard: CORSConfig.AllowCredentials cannot be used with wildcard origin \"*\"")
 			}
+			if strings.EqualFold(o, "null") {
+				panic("guard: CORSConfig.AllowCredentials cannot be used with \"null\" origin (sandboxed iframe attack)")
+			}
 		}
 	}
```

---

### HIGH-3: Announcekit Data Race on Package-Level `serviceName`
**File:** `announcekit/announcekit.go:18-23`
**Severity:** High (Concurrency)

`serviceName` is a bare package-level variable with no synchronization. `SetServiceName` writes it; every event function reads it concurrently.

**Patch-ready diff:**
```diff
--- a/announcekit/announcekit.go
+++ b/announcekit/announcekit.go
@@ -16,12 +16,16 @@ type publisher interface {
 }

-// serviceName must be set before calling lifecycle functions.
-var serviceName string
+var (
+	mu          sync.RWMutex
+	serviceName string
+)

-// SetServiceName configures the service identity for lifecycle events.
 func SetServiceName(name string) {
 	chassis.AssertVersionChecked()
+	mu.Lock()
+	defer mu.Unlock()
 	serviceName = name
 }
+
+func svcName() string {
+	mu.RLock()
+	defer mu.RUnlock()
+	return serviceName
+}

 func Started(ctx context.Context, pub publisher) error {
-	return pub.Publish(ctx, "ai8.infra."+serviceName+".lifecycle.started", map[string]any{
-		"service": serviceName, "state": "started",
+	n := svcName()
+	return pub.Publish(ctx, "ai8.infra."+n+".lifecycle.started", map[string]any{
+		"service": n, "state": "started",
 	})
 }
```
*(Apply the same `svcName()` pattern to all other functions: Ready, Stopping, Failed, JobStarted, JobComplete, JobFailed.)*

---

### HIGH-4: Announcekit `Failed`/`JobFailed` Nil Panic
**File:** `announcekit/announcekit.go:52,75`
**Severity:** High

`err.Error()` called without nil guard. Passing a nil error panics.

**Patch-ready diff:**
```diff
--- a/announcekit/announcekit.go
+++ b/announcekit/announcekit.go
@@ -50,7 +50,11 @@ func Failed(ctx context.Context, pub publisher, err error) error {
-	return pub.Publish(ctx, "ai8.infra."+serviceName+".lifecycle.failed", map[string]any{
-		"service": serviceName, "state": "failed", "error": err.Error(),
+	n := svcName()
+	errMsg := "<nil>"
+	if err != nil {
+		errMsg = err.Error()
+	}
+	return pub.Publish(ctx, "ai8.infra."+n+".lifecycle.failed", map[string]any{
+		"service": n, "state": "failed", "error": errMsg,
 	})
 }
```
*(Apply same nil-guard pattern to `JobFailed`.)*

---

### HIGH-5: SchemaKit Registry Has No Mutex — Concurrent Access Races
**File:** `schemakit/schemakit.go:31-34`
**Severity:** High (Concurrency)

The `cache` map has no mutex. `LoadSchemas` writes to it, `GetSchema`/`Deserialize` read from it concurrently. `Register` mutates `schema.SchemaID` on a shared `*Schema` without synchronization.

**Patch-ready diff:**
```diff
--- a/schemakit/schemakit.go
+++ b/schemakit/schemakit.go
@@ -30,7 +30,8 @@ type Schema struct {
 // Registry manages schema registration and validation.
 type Registry struct {
-	url   string
-	cache map[string]*Schema
+	mu    sync.RWMutex
+	url   string
+	cache map[string]*Schema
 }

 func (r *Registry) GetSchema(subject string) *Schema {
+	r.mu.RLock()
+	defer r.mu.RUnlock()
 	return r.cache[subject]
 }
```
*(Add `r.mu.Lock()`/`r.mu.Unlock()` around all write paths: `LoadSchemas`, `Register`.)*

---

### HIGH-6: Heartbeatkit Goroutine Leak on Double-Start
**File:** `heartbeatkit/heartbeatkit.go:36-54`
**Severity:** High (Resource Leak)

Calling `Start` a second time overwrites the package-level `stopCh` without closing the old one, leaking the first goroutine permanently. The goroutine captures `stopCh` by reference through the package-level variable.

**Patch-ready diff:**
```diff
--- a/heartbeatkit/heartbeatkit.go
+++ b/heartbeatkit/heartbeatkit.go
@@ -49,6 +49,11 @@ func Start(...) {
 	mu.Lock()
 	defer mu.Unlock()
+	// Stop any previously running heartbeat to prevent goroutine leaks.
+	if stopCh != nil {
+		close(stopCh)
+	}
 	stopCh = make(chan struct{})
+	localStop := stopCh  // capture by value for the goroutine
 	go func() {
-		// ... use stopCh in select
+		// ... use localStop in select instead of package-level stopCh
 	}()
```

---

### HIGH-7: Timeout Middleware Goroutine Leak
**File:** `guard/timeout.go:40-66`
**Severity:** High (Resource Leak)

When the timeout fires, the goroutine running `next.ServeHTTP(tw, r)` continues executing until the handler returns. If the handler ignores `r.Context().Done()`, the goroutine leaks indefinitely. Additionally, `tw.buf` is never freed after `flush()`/`timeout()`.

**Patch-ready diff:**
```diff
--- a/guard/timeout.go
+++ b/guard/timeout.go
@@ -135,6 +135,7 @@ func (tw *timeoutWriter) flush() {
 	tw.written = true
+	tw.buf = nil // release buffer memory
 	for k, vs := range tw.headers {
@@ -158,6 +159,7 @@ func (tw *timeoutWriter) timeout() {
 	}
 	tw.written = true
+	tw.buf = nil // release buffer memory
 	writeProblem(tw.w, tw.req, errors.TimeoutError("request timed out"))
 }
```

Also add to the `Timeout` godoc:
```diff
+// WARNING: Handlers must respect r.Context().Done() for this middleware to
+// fully bound goroutine lifetime. Slow handlers that ignore context
+// cancellation will remain running after the 504 is sent.
```

---

### HIGH-8: Registry `redactArgs` Does Not Handle `--flag value` Form
**File:** `registry/registry.go:604-624`
**Severity:** High (Security — Credential Exposure)

`redactArg` only handles `--flag=value` form. For `["--password", "hunter2"]`, the password appears in plaintext in the PID file.

**Patch-ready diff:**
```diff
--- a/registry/registry.go
+++ b/registry/registry.go
@@ -603,11 +603,22 @@ var sensitiveFlags = []string{

 func redactArgs(args []string) []string {
 	out := make([]string, len(args))
-	for i, arg := range args {
-		out[i] = redactArg(arg)
+	for i := 0; i < len(args); i++ {
+		arg := args[i]
+		redacted := redactArg(arg)
+		if redacted != arg {
+			out[i] = redacted
+			continue
+		}
+		// Check if this is a --flag followed by a separate value
+		name := strings.TrimLeft(strings.ToLower(arg), "-")
+		if strings.HasPrefix(arg, "-") && i+1 < len(args) {
+			for _, s := range sensitiveFlags {
+				if strings.Contains(name, s) {
+					out[i] = arg
+					i++
+					out[i] = "REDACTED"
+					break
+				}
+			}
+			if out[i] == "" {
+				out[i] = arg
+			}
+		} else {
+			out[i] = arg
+		}
 	}
 	return out
 }
```

---

### HIGH-9: `xyopsworker.Run()` Is an Unimplemented Stub
**File:** `xyopsworker/worker.go:116-127`
**Severity:** High (Operational)

`Run` blocks on `<-ctx.Done()` and silently does nothing. A service wiring this into `lifecycle.Run` will appear healthy but never process any jobs.

**Patch-ready diff:**
```diff
--- a/xyopsworker/worker.go
+++ b/xyopsworker/worker.go
@@ -116,8 +116,7 @@ func (w *Worker) Run(ctx context.Context) error {
-	// For now, block until context is cancelled
-	<-ctx.Done()
-	return nil
+	return fmt.Errorf("xyopsworker: WebSocket connection not yet implemented — Run() is a placeholder")
 }
```

---

## Medium Findings

### MED-1: Timeout `timeout()` Silent No-Op When Handler Started Writing
**File:** `guard/timeout.go:152-157`

If the handler called `WriteHeader()` but the timeout fires before `flush()`, the guard at line 155 returns without sending 504 **and** without flushing the buffered response. The client hangs.

**Patch-ready diff:**
```diff
--- a/guard/timeout.go
+++ b/guard/timeout.go
@@ -152,7 +152,10 @@ func (tw *timeoutWriter) timeout() {
 	tw.mu.Lock()
 	defer tw.mu.Unlock()
-	if tw.written || tw.started {
+	if tw.written {
+		return
+	}
+	if tw.started {
+		// Handler already started writing — flush what we have rather than hanging.
+		tw.written = true
+		tw.buf = nil
 		return
 	}
```

---

### MED-2: `call.cancelBody.Close` Context Leak When Body Read But Not Closed
**File:** `call/call.go:38-42`

If the caller reads to `io.EOF` but never calls `Close()`, the context leaks.

**Patch-ready diff:**
```diff
--- a/call/call.go
+++ b/call/call.go
@@ -33,6 +33,16 @@ type cancelBody struct {
 	cancel context.CancelFunc
 }

+func (b *cancelBody) Read(p []byte) (int, error) {
+	n, err := b.ReadCloser.Read(p)
+	if err == io.EOF {
+		b.cancel()
+	}
+	return n, err
+}
+
 func (b *cancelBody) Close() error {
 	err := b.ReadCloser.Close()
 	b.cancel()
```

---

### MED-3: Circuit Breaker `GetBreaker` Silent Parameter Mismatch
**File:** `call/breaker.go:57-70`

Two callers using the same breaker name with different parameters — the second silently uses the first's configuration.

**Patch-ready diff (add warning log):**
```diff
+	actual, loaded := breakers.LoadOrStore(name, cb)
+	if loaded {
+		existing := actual.(*CircuitBreaker)
+		if existing.threshold != threshold || existing.resetTimeout != resetTimeout {
+			slog.Warn("call: GetBreaker called with different parameters for existing breaker",
+				"name", name,
+				"existing_threshold", existing.threshold,
+				"requested_threshold", threshold,
+			)
+		}
+	}
+	return actual.(*CircuitBreaker)
```

---

### MED-4: `config.setField` Empty String Split Produces Non-Empty Slice
**File:** `config/config.go:106-112`

`strings.Split("", ",")` returns `[""]`, not `[]string{}`. An explicitly-empty env var produces a slice with one blank element.

**Patch-ready diff:**
```diff
--- a/config/config.go
+++ b/config/config.go
@@ -106,6 +106,9 @@ case reflect.Slice:
+	if raw == "" {
+		fieldVal.Set(reflect.MakeSlice(field.Type, 0, 0))
+		return ""
+	}
 	parts := strings.Split(raw, ",")
```

---

### MED-5: Kafkakit `Subscriber.Close()` Races with `Start()` on `s.client`
**File:** `kafkakit/subscriber.go:184-190`

`s.client` is assigned in `Start()` and read in `Close()` without any lock. Concurrent calls race.

---

### MED-6: `secheaders.go` Trusts `X-Forwarded-Proto` Unconditionally
**File:** `guard/secheaders.go:73`

HSTS is set based on `X-Forwarded-Proto` header, which a client can spoof when no trusted proxy strips it.

---

### MED-7: `guard/keyfunc.go` XFF Chain Has No Length Limit
**File:** `guard/keyfunc.go:68-79`

A client can send `X-Forwarded-For` with thousands of entries, causing linear CPU amplification per request.

**Patch-ready diff:**
```diff
--- a/guard/keyfunc.go
+++ b/guard/keyfunc.go
@@ -68,6 +68,7 @@ func XForwardedFor(trustedCount int) KeyFunc {
 		parts := strings.Split(xff, ",")
+		if len(parts) > 20 { parts = parts[len(parts)-20:] } // cap chain length
 		for i := len(parts) - 1; i >= 0; i-- {
```

---

### MED-8: `maxbody.go` Chunked Encoding Bypasses Early Rejection
**File:** `guard/maxbody.go:19-24`

Chunked requests (`Content-Length: -1`) skip the fast-path rejection. `MaxBytesReader` catches them at read time, but the 413 problem-detail response is never sent — the handler is responsible for detecting the truncation.

---

### MED-9: Unbounded `io.ReadAll` on Error Responses (3 packages)
**Files:** `graphkit/graphkit.go:369`, `lakekit/lakekit.go:251`, `registrykit/registrykit.go:492`

All three use `io.ReadAll(resp.Body)` on error responses with no size limit.

**Patch-ready diff (apply to all three):**
```diff
-body, _ := io.ReadAll(resp.Body)
+body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
```

---

### MED-10: OTel Shutdown Timeout Shared Across Both Providers
**File:** `otel/otel.go:121-126`

A single 5-second timeout context is shared between `tp.Shutdown` and `mp.Shutdown`. If traces take the full 5s, metrics get an expired context.

**Patch-ready diff:**
```diff
--- a/otel/otel.go
+++ b/otel/otel.go
@@ -119,8 +119,12 @@ return func(ctx context.Context) error {
-	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
-	defer cancel()
-	tpErr := tp.Shutdown(shutdownCtx)
-	mpErr := mp.Shutdown(shutdownCtx)
+	tpCtx, tpCancel := context.WithTimeout(ctx, 5*time.Second)
+	defer tpCancel()
+	tpErr := tp.Shutdown(tpCtx)
+
+	mpCtx, mpCancel := context.WithTimeout(ctx, 5*time.Second)
+	defer mpCancel()
+	mpErr := mp.Shutdown(mpCtx)
```

---

### MED-11: `xyops.GetJobStatus` Caches Transient States for 5 Minutes
**File:** `xyops/xyops.go:179-197`

A `"running"` job is cached for up to 5 minutes. Callers polling for completion see stale data.

---

### MED-12: `tracekit` Trace ID Spoofing via Unsanitized Header
**File:** `tracekit/tracekit.go:46-48`

`X-Trace-ID` is accepted without any length or character validation. Extremely long or special-character IDs could bloat logs.

**Patch-ready diff:**
```diff
--- a/tracekit/tracekit.go
+++ b/tracekit/tracekit.go
@@ -46,6 +46,9 @@ func Middleware(next http.Handler) http.Handler {
 	traceID := r.Header.Get("X-Trace-ID")
-	if traceID == "" {
+	if traceID == "" || len(traceID) > 64 {
+		traceID = GenerateID()
+	}
+	for _, c := range traceID {
+		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
+			traceID = GenerateID()
+			break
+		}
+	}
```

---

### MED-13: `cache.Prune` Modifies Map During Range Iteration
**File:** `cache/cache.go:136-143`

Deleting from a map during `range` is technically safe in Go but elements may be skipped. Collect keys first, then delete.

---

### MED-14: Token Acquisition Runs Before Circuit Breaker Check
**File:** `call/call.go:160-193`

Token is fetched (potentially hitting an auth server) before the circuit breaker check. If the breaker is open, the token fetch was wasted.

---

### MED-15: `registry.CHASSIS_SERVICE_NAME` Path Traversal
**File:** `registry/registry.go:579-581`

If `CHASSIS_SERVICE_NAME=../../etc`, the service directory is created outside `BasePath`.

**Patch-ready diff:**
```diff
+if n := os.Getenv("CHASSIS_SERVICE_NAME"); n != "" {
+	if filepath.Base(n) != n || strings.ContainsAny(n, "/\\") {
+		panic("registry: CHASSIS_SERVICE_NAME contains path separator")
+	}
 	return n
 }
```

---

## Low Findings (Summary Table)

| ID | Package | File:Line | Issue |
|----|---------|-----------|-------|
| L-1 | guard | `ratelimit.go:119` | `Retry-After: 1` hardcoded regardless of window |
| L-2 | guard | `ipfilter.go:40-44` | Invalid IP from KeyFunc → 403 with no server-side log |
| L-3 | guard | `keyfunc.go:86-94` | `HeaderKey` with client-controlled header enables rate-limit bypass (undocumented) |
| L-4 | config | `config.go:191` | `MustCompile` panic on invalid regex in struct tag — raw panic, not config-prefixed |
| L-5 | config | `config.go:53` | Recursive struct handling may populate coincidental embedded field names |
| L-6 | lifecycle | `lifecycle.go:108` | `signal.NotifyContext` stop function passed to registry — does not cancel context |
| L-7 | call | `token.go:44-58` | `CachedToken` holds mutex during entire network fetch — goroutine pile-up |
| L-8 | call | `retry.go:62-65` | Retrier does not respect 429 `Retry-After` header |
| L-9 | call | `call.go:296-301` | `Batch` partial failure leaks response bodies if caller returns early |
| L-10 | work | `work.go:218-220` | `Race` returns zero value with nil error for zero tasks |
| L-11 | work | `work.go:276` | `Stream` discards span context — child spans not parented to `work.Stream` span |
| L-12 | metrics | `metrics.go:213-218` | `pairsToCombo` silently drops odd trailing label |
| L-13 | secval | `secval.go:105-120` | `SafeFilename` does not enforce a maximum length |
| L-14 | secval | `secval.go:91` | `RedactSecrets` regex under-redacts multi-word secret values |
| L-15 | seal | `seal.go:40-47` | scrypt params not stored in `Envelope` — forward-incompatible if params change |
| L-16 | tick | `tick.go:77` | `time.After` in jitter select leaks timers on context cancel |
| L-17 | deploy | `deploy.go` | `deploy.json` re-read on every method call (up to 5x per `Health()`) |
| L-18 | kafkakit | `subscriber.go:223` | DLQ produce uses `context.Background()` — blocks indefinitely on shutdown |
| L-19 | logz | `logz.go:20` | Output hardcoded to `os.Stderr` — not configurable |
| L-20 | examples | `04-full-service:57` | OTel shutdown deferred with no-timeout `context.Background()` |
| L-21 | examples | `04-full-service:1` | Comment says "chassis 5.0" but module is v10 |
| L-22 | examples | `02/04` | Hardcoded well-known ports 8080/9090/50051 — `chassis.Port()` exists for this |

---

## Info / Design Notes (Summary)

| ID | Package | Issue |
|----|---------|-------|
| I-1 | registry | `sensitiveFlags` does not include `dsn` or `connection-string` |
| I-2 | registry | `processAlive` using `Signal(0)` is not portable to Windows |
| I-3 | flagz | `FromJSON` is eager-loading, not lazy — stale if file changes at runtime |
| I-4 | grpckit | `health.Check` ignores `req.Service` — always returns global health |
| I-5 | grpckit | `Watch` RPC returns `Unimplemented` — may confuse some K8s operators |
| I-6 | grpckit | Error strings logged verbatim — PII risk if handlers encode user data |
| I-7 | seal | scrypt `N=16384` is below 2023 OWASP recommendation of `N>=32768` |
| I-8 | xyopsworker | `ShellEnabled` config flag implies capability that doesn't exist |
| I-9 | kafkakit | `EventsPublished1h` name implies windowing but is a monotonic counter |
| I-10 | kafkakit | `publisherStats.mu` sync.Mutex declared but never used |
| I-11 | chassis.go | `ResetVersionCheck()` is exported — any consumer can bypass version gate |

---

## Test Coverage Gaps

| Package | Missing Test Cases |
|---------|-------------------|
| config | Empty `[]string` from explicit empty env var; pointer field; `validate:"min"` on string; invalid regex in tag |
| lifecycle | `WithKafkaConfig` path; `infraCtx` cancellation; restart exec path |
| guard | Chunked-encoding body for MaxBody; XFF chain >20 entries; `"null"` origin + credentials |
| call | `cancelBody` read-without-close; `GetBreaker` param mismatch; `Batch` partial failure body leak |
| work | `Race` with zero tasks error semantics; `Stream` span parenting |
| webhook | Replay attack with stale timestamp; `Status()` concurrent read during retry; `Send` after maxHistory |
| schemakit | Concurrent `GetSchema`/`Register`; duplicate subject; `Register` with `DefaultClient` timeout |
| announcekit | Concurrent `SetServiceName`/`Started`; nil error to `Failed` |
| heartbeatkit | Double-Start goroutine leak |
| secval | `SafeFilename` with 256+ char input; `RedactSecrets` multi-word value; `__-proto-__` blocked |

---

## Positive Observations

- **Version gating** (`RequireMajor`/`AssertVersionChecked`) is consistently applied across all packages — a strong safety net that prevents silent API drift.
- **Fail-fast constructors** (panic on invalid config) are used consistently in guard, lifecycle, and other packages.
- **RFC 9457 Problem Details** used throughout HTTP error responses — good standards compliance.
- **Zero cross-dependencies** between packages — the toolkit philosophy is well-executed.
- **`work.Map`/`work.All`** are well-designed concurrent primitives with proper semaphore-based concurrency control.
- **`seal` package** provides solid HMAC signing and AES-256-GCM encryption with scrypt KDF.
- **`health.All`** uses deterministic ordering (`sort.Strings`) for reproducible results.
- **Test coverage** for core packages is good, with proper use of `httptest`, table-driven tests, and race-safe patterns.

---

## Remediation Priority

**Immediate (security/data-loss):**
1. CRIT-1: Webhook replay attack
2. CRIT-2: Rate-limit bypass via LRU eviction
3. HIGH-8: Registry `redactArgs` leaks credentials
4. HIGH-1: Deploy path traversal
5. HIGH-2: CORS null origin + credentials

**This sprint (races/leaks):**
6. CRIT-3/4: Webhook unbounded map + data races
7. HIGH-3/4: Announcekit races + nil panic
8. HIGH-5: SchemaKit races
9. HIGH-6: Heartbeatkit goroutine leak
10. MED-15: Registry `CHASSIS_SERVICE_NAME` path traversal

**Next sprint (correctness/robustness):**
11. MED-2: `cancelBody` context leak
12. MED-3: Circuit breaker silent param mismatch
13. MED-9: Unbounded `io.ReadAll` (3 packages)
14. MED-10: OTel shutdown timeout sharing
15. HIGH-7: Timeout middleware goroutine leak documentation + buffer cleanup

---

*Audit performed by Claude Code (Claude:Opus 4.6) on 2026-03-27. No code was modified during this audit.*
