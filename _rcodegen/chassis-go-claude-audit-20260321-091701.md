Date Created: 2026-03-21T09:17:01-07:00
TOTAL_SCORE: 63/100

# chassis-go Full Audit Report

**Agent**: Claude:Opus 4.6
**Module**: `github.com/ai8future/chassis-go/v9` (Go 1.25.5)
**Scope**: All 16+ packages, go.mod, examples, tests
**Note**: Go 1.25.5 means loop variable capture is NOT a bug (per-iteration scoping since Go 1.22). All such patterns are safe.

---

## Scoring Breakdown

| Category | Max | Score | Notes |
|----------|-----|-------|-------|
| Security | 25 | 12 | Webhook replay, HSTS spoofing, scrypt params, rand.Read |
| Concurrency | 20 | 12 | Webhook data race, timeout goroutine leak, shutdown deadline sharing |
| Error Handling | 15 | 10 | Silently swallowed errors in metrics, otel, webhook |
| API Design | 15 | 11 | durationMs footgun, splitToken fragility, non-context-aware retry |
| Test Quality | 10 | 8 | Good coverage but missing RequireMajor in some files, racy otel pattern |
| Code Quality | 15 | 10 | Clean architecture, some wrapper layering issues, RWMutex misuse |
| **Total** | **100** | **63** | |

---

## Issues Summary

| # | Severity | Package | File:Lines | Issue |
|---|----------|---------|-----------|-------|
| 1 | Critical | webhook | webhook.go:82-125 | Data race on `Delivery` fields |
| 2 | Critical | webhook | webhook.go:139-156 | No timestamp replay protection in `VerifyPayload` |
| 3 | Critical | guard | timeout.go:40-67 | Goroutine leak when timeout fires |
| 4 | High | guard | timeout.go:152-160 | `timeout()` no-ops if handler called WriteHeader without Write |
| 5 | High | grpckit | interceptors.go:195-212 | `StreamMetrics` reads context after stream teardown |
| 6 | High | otel | otel.go:120-126 | Shared shutdown deadline causes mp.Shutdown to time out |
| 7 | High | guard | maxbody.go:19-25 | Chunked bodies bypass early 413 rejection |
| 8 | High | guard | secheaders.go:73 | HSTS set on spoofable `X-Forwarded-Proto` (RFC 6797 violation) |
| 9 | High | config | config.go:178 | `regexp.MustCompile` panic lacks field context |
| 10 | High | metrics | metrics.go:190-207 | `Counter()`/`Histogram()` silently discard errors |
| 11 | High | metrics | metrics.go:87 | `durationMs` parameter name is a footgun |
| 12 | High | work | work.go:285-315 | `Stream` silently drops in-flight results on cancel |
| 13 | High | lifecycle | lifecycle.go:63-82 | Infra errgroup can kill user components |
| 14 | High | webhook | webhook.go:161 | `rand.Read` error silently ignored |
| 15 | High | seal | seal.go:209-216 | `splitToken` right-to-left scan is fragile |
| 16 | High | webhook | webhook.go:99,119 | Retry backoff not context-aware |
| 17 | High | testkit | httpserver.go:69-81 | `Sequence()` panics on empty handlers |
| 18 | Medium | httpkit | middleware.go:136-154 | Recovery->Tracing double-wrap breaks headerWritten detection |
| 19 | Medium | guard | ratelimit.go:119 | `Retry-After` hardcoded to "1" |
| 20 | Medium | seal | seal.go:41-43 | scrypt N=2^14 below OWASP 2023 minimums |
| 21 | Medium | cache | cache.go:12,66 | `sync.RWMutex` declared but used as plain mutex |
| 22 | Medium | otel | otel_test.go:36-108 | Per-test `ResetVersionCheck` is racy |
| 23 | Medium | secval,registry | *_test.go | Missing `chassis.RequireMajor(9)` |
| 24 | Medium | config | config.go:48-64 | Empty-string env var treated as "unset" |
| 25 | Medium | go.mod | go.mod:27 | `golang.org/x/crypto` marked indirect despite direct import |
| 26 | Low | flagz | flagz.go:51 | `EnabledFor` doc imprecise about Percent=0 |
| 27 | Low | registry | registry.go:669-703 | `cleanStale` filter logic correct but fragile |

---

## Detailed Findings with Patch-Ready Diffs

### Issue 1 (Critical): webhook/webhook.go — Data race on Delivery fields

`delivery.Attempts`, `delivery.Status`, and `delivery.LastError` are mutated inside the retry loop without holding `s.mu`, but `Status()` reads the Delivery under `s.mu`. Any concurrent call to `Status()` while `Send()` is in-flight observes torn writes.

```diff
--- a/webhook/webhook.go
+++ b/webhook/webhook.go
@@ -79,7 +79,9 @@ func (s *Sender) Send(eventType string, payload []byte) (string, error) {
 	for attempt := 1; attempt <= s.maxAttempts; attempt++ {
-		delivery.Attempts = attempt
+		s.mu.Lock()
+		delivery.Attempts = attempt
+		s.mu.Unlock()

 		req, err := http.NewRequest("POST", s.targetURL, bytes.NewReader(payload))
 		if err != nil {
@@ -103,8 +105,10 @@ func (s *Sender) Send(eventType string, payload []byte) (string, error) {

 		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
-			delivery.Status = "delivered"
-			delivery.LastError = ""
+			s.mu.Lock()
+			delivery.Status = "delivered"
+			delivery.LastError = ""
+			s.mu.Unlock()
 			return id, nil
 		}

@@ -108,8 +112,10 @@ func (s *Sender) Send(eventType string, payload []byte) (string, error) {
 		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
-			delivery.Status = "failed"
-			delivery.LastError = fmt.Sprintf("HTTP %d (non-retryable)", resp.StatusCode)
+			s.mu.Lock()
+			delivery.Status = "failed"
+			delivery.LastError = fmt.Sprintf("HTTP %d (non-retryable)", resp.StatusCode)
+			s.mu.Unlock()
 			return id, fmt.Errorf("webhook: non-retryable status %d", resp.StatusCode)
 		}

@@ -120,8 +126,10 @@ func (s *Sender) Send(eventType string, payload []byte) (string, error) {
 	}

-	delivery.Status = "failed"
-	delivery.LastError = fmt.Sprintf("exhausted %d attempts", s.maxAttempts)
+	s.mu.Lock()
+	delivery.Status = "failed"
+	delivery.LastError = fmt.Sprintf("exhausted %d attempts", s.maxAttempts)
+	s.mu.Unlock()
 	return id, fmt.Errorf("webhook: exhausted %d attempts", s.maxAttempts)
 }
```

---

### Issue 2 (Critical): webhook/webhook.go — No timestamp replay protection

`VerifyPayload` includes the timestamp in the HMAC but never checks that it is recent. An attacker who captures a valid signed webhook can replay it indefinitely.

```diff
--- a/webhook/webhook.go
+++ b/webhook/webhook.go
@@ -1,6 +1,8 @@
 package webhook

 import (
+	"math"
+	"strconv"
+	"time"
 	// ... existing imports
 )

@@ -139,6 +141,14 @@ func VerifyPayload(body []byte, sigHeader, timestamp, secret string) ([]byte, er
 		return nil, ErrBadSignature
 	}

+	ts, err := strconv.ParseInt(timestamp, 10, 64)
+	if err != nil {
+		return nil, ErrBadSignature
+	}
+	if math.Abs(float64(time.Now().Unix()-ts)) > 300 {
+		return nil, fmt.Errorf("webhook: timestamp too old (replay protection)")
+	}
+
 	sigPayload := timestamp + "." + string(body)
 	mac := hmac.New(sha256.New, []byte(secret))
 	mac.Write([]byte(sigPayload))
```

---

### Issue 3 (Critical): guard/timeout.go — Goroutine leak

When the timeout fires, the goroutine running `next.ServeHTTP(tw, r)` keeps running until the handler checks `ctx.Err()`. Non-context-aware handlers leak the goroutine permanently. The `tw.Write` returning `http.ErrHandlerTimeout` is the only implicit signal, but handlers that don't check write errors will never stop.

```diff
--- a/guard/timeout.go
+++ b/guard/timeout.go
@@ -38,12 +38,20 @@ func (t *Timeout) ServeHTTP(w http.ResponseWriter, r *http.Request) {
 	ctx, cancel := context.WithTimeout(r.Context(), t.timeout)
 	defer cancel()
 	r = r.WithContext(ctx)
+
+	// Upper-bound kill timer: if handler doesn't respect context, force cleanup
+	killTimer := time.AfterFunc(t.timeout*2, func() {
+		// Log that the handler did not exit after 2x the timeout.
+		// This is the best we can do without runtime.Goexit — Go has no
+		// mechanism to forcibly terminate a goroutine.
+	})

 	done := make(chan struct{})
 	go func() {
 		defer close(done)
 		next.ServeHTTP(tw, r)
+		killTimer.Stop()
 	}()

 	select {
```

> **Note**: Go cannot forcibly kill a goroutine. The real fix is to document that handlers MUST be context-aware, and add a metric/log when the kill timer fires so operators can detect misbehaving handlers. The diff above adds the scaffolding for that detection.

---

### Issue 4 (High): guard/timeout.go — timeout() silently no-ops after WriteHeader

If the handler called `WriteHeader(200)` but the timeout fires before `Write`, the `timeout()` method sees `tw.started == true` and returns without writing anything. The client gets an empty 200 instead of a 504.

```diff
--- a/guard/timeout.go
+++ b/guard/timeout.go
@@ -150,7 +150,7 @@ func (tw *timeoutWriter) timeout() {
 	tw.mu.Lock()
 	defer tw.mu.Unlock()

-	if tw.written || tw.started {
+	if tw.written {
 		return
 	}

```

---

### Issue 5 (High): grpckit/interceptors.go — StreamMetrics reads context after stream teardown

After `handler(srv, ss)` returns, `ss.Context()` may return a cancelled context, causing the OTel Record to be lost.

```diff
--- a/grpckit/interceptors.go
+++ b/grpckit/interceptors.go
@@ -193,10 +193,11 @@ func StreamMetrics(rec *metrics.Recorder) grpc.StreamServerInterceptor {
 	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
+		sctx := ss.Context() // capture before handler may tear down the stream
 		start := time.Now()
 		err := handler(srv, ss)
 		duration := time.Since(start).Seconds()

 		status := statusFromError(err)
-		rec.RecordRequest(ctx(ss), info.FullMethod, status, duration*1000, 0)
+		rec.RecordRequest(sctx, info.FullMethod, status, duration*1000, 0)
 		return err
 	}
 }
```

---

### Issue 6 (High): otel/otel.go — Shared shutdown deadline

`tp.Shutdown` and `mp.Shutdown` share a single 5-second context. If traces take 4.9s to flush, metrics get 0.1s.

```diff
--- a/otel/otel.go
+++ b/otel/otel.go
@@ -118,9 +118,13 @@ func Setup(ctx context.Context, cfg Config) (func(context.Context) error, error)
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
+
 		return errors.Join(tErr, mErr)
 	}, nil
```

---

### Issue 7 (High): guard/secheaders.go — HSTS on spoofable X-Forwarded-Proto

`X-Forwarded-Proto` is client-controllable. Sending HSTS over HTTP violates RFC 6797 section 7.2.

```diff
--- a/guard/secheaders.go
+++ b/guard/secheaders.go
@@ -20,6 +20,7 @@ type SecurityHeadersConfig struct {
 	ContentSecurityPolicy string
 	HSTSMaxAge            int
 	HSTSIncludeSubdomains bool
+	TrustForwardedProto   bool // Set true only when behind a trusted reverse proxy
 }

@@ -70,7 +71,7 @@ func SecurityHeaders(cfg SecurityHeadersConfig) func(http.Handler) http.Handler
 		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
 			// ... existing header writes ...

-			if hstsValue != "" && (r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https") {
+			if hstsValue != "" && (r.TLS != nil || (cfg.TrustForwardedProto && r.Header.Get("X-Forwarded-Proto") == "https")) {
 				w.Header().Set("Strict-Transport-Security", hstsValue)
 			}
```

---

### Issue 8 (High): config/config.go — regexp.MustCompile panic lacks context

When a validate tag has a bad regex, the panic message is the raw regexp error with no field name.

```diff
--- a/config/config.go
+++ b/config/config.go
@@ -175,7 +175,10 @@ func validateField(name string, val reflect.Value, rules string) {
 			// ...
 		case "pattern":
-			re := regexp.MustCompile(value)
+			re, err := regexp.Compile(value)
+			if err != nil {
+				panic(fmt.Sprintf("config: field %s has invalid pattern %q: %v", name, value, err))
+			}
 			actual := fmt.Sprintf("%v", val.Interface())
 			if !re.MatchString(actual) {
```

---

### Issue 9 (High): metrics/metrics.go — Counter/Histogram silently discard errors

Unlike `New()` which logs warnings, `Counter()` and `Histogram()` silently drop instrument creation errors.

```diff
--- a/metrics/metrics.go
+++ b/metrics/metrics.go
@@ -188,7 +188,10 @@ func (r *Recorder) Counter(name string) *CounterVec {
 	fullName := r.prefix + "_" + name
-	cv, _ := r.meter.Float64Counter(fullName,
+	cv, err := r.meter.Float64Counter(fullName,
 		metric.WithDescription(name+" counter"),
 	)
+	if err != nil && r.logger != nil {
+		r.logger.Warn("metrics: failed to create counter", "name", fullName, "error", err)
+	}
 	return &CounterVec{inner: cv, name: name, recorder: r}
 }

@@ -198,7 +201,10 @@ func (r *Recorder) Histogram(name string, buckets []float64) *HistogramVec {
 	fullName := r.prefix + "_" + name
-	hv, _ := r.meter.Float64Histogram(fullName,
+	hv, err := r.meter.Float64Histogram(fullName,
 		metric.WithDescription(name+" histogram"),
 	)
+	if err != nil && r.logger != nil {
+		r.logger.Warn("metrics: failed to create histogram", "name", fullName, "error", err)
+	}
 	return &HistogramVec{inner: hv, name: name, recorder: r}
 }
```

---

### Issue 10 (High): metrics/metrics.go — durationMs parameter name is a footgun

Callers passing `time.Since(start).Seconds()` will get values off by 1000x with no error.

```diff
--- a/metrics/metrics.go
+++ b/metrics/metrics.go
@@ -85,8 +85,8 @@ func (r *Recorder) RecordRequest(ctx context.Context, method, status string, dur
 // RecordRequest records a request with the given method, status, duration, and content length.
-// durationMs is the request duration in milliseconds; it is converted to seconds internally.
-func (r *Recorder) RecordRequest(ctx context.Context, method, status string, durationMs float64, contentLength float64) {
+// durationSecs is the request duration in seconds (e.g., time.Since(start).Seconds()).
+func (r *Recorder) RecordRequest(ctx context.Context, method, status string, durationSecs float64, contentLength float64) {
 	attrs := metric.WithAttributes(
 		attribute.String("method", method),
 		attribute.String("status", status),
 	)
-	r.requestDuration.Record(ctx, durationMs/1000, attrs)
+	r.requestDuration.Record(ctx, durationSecs, attrs)
 	r.responseSize.Record(ctx, contentLength, attrs)
```

> **Note**: This is a breaking API change. All callers currently passing milliseconds must be updated to pass seconds. Grep for `RecordRequest` across the codebase and update all call sites (e.g., `grpckit/interceptors.go` already passes `duration*1000` — change to just `duration`).

---

### Issue 11 (High): work/work.go — Stream silently drops results on cancel

When context is cancelled, in-flight workers exit via `<-ctx.Done()`, dropping their results with no indication to consumers.

```diff
--- a/work/work.go
+++ b/work/work.go
@@ -300,7 +300,10 @@ func Stream[T any, R any](ctx context.Context, workers int, input <-chan T, fn f
 			select {
 			case out <- Result[R]{Value: val, Err: err, Index: currentIdx}:
 			case <-ctx.Done():
-				// result dropped; context cancelled
+				// Best-effort: send a cancellation result so consumers can
+				// account for all indices.
+				select {
+				case out <- Result[R]{Err: ctx.Err(), Index: currentIdx}:
+				default: // channel full or closed, truly drop
+				}
 			}
```

---

### Issue 12 (High): webhook/webhook.go — No timestamp replay protection (VerifyPayload)

See Issue 2 above (same diff).

---

### Issue 13 (High): webhook/webhook.go — rand.Read error ignored

```diff
--- a/webhook/webhook.go
+++ b/webhook/webhook.go
@@ -158,7 +158,10 @@ func generateID() string {
 	b := make([]byte, 16)
-	rand.Read(b)
+	if _, err := rand.Read(b); err != nil {
+		// crypto/rand.Read panics on error in Go 1.20+, but be explicit
+		panic("webhook: crypto/rand.Read failed: " + err.Error())
+	}
 	return hex.EncodeToString(b)
 }
```

---

### Issue 14 (High): seal/seal.go — splitToken right-to-left scan fragile

```diff
--- a/seal/seal.go
+++ b/seal/seal.go
@@ -207,8 +207,9 @@ func splitToken(token string) (body, sig string, err error) {
-	// scan from right to find the separator
-	idx := strings.LastIndex(token, ".")
+	// The signature is always a 64-char lowercase hex string (SHA-256 HMAC).
+	// Split on the last dot, which separates base64url(claims) from hex(sig).
+	idx := strings.LastIndex(token, ".")
 	if idx < 0 || idx == len(token)-1 {
 		return "", "", fmt.Errorf("seal: malformed token")
 	}
```

> **Note**: The existing logic is technically correct since hex never contains dots. Add a validation that the signature part is exactly 64 hex characters to make the format unambiguous:

```diff
+	sig = token[idx+1:]
+	if len(sig) != 64 {
+		return "", "", fmt.Errorf("seal: malformed token: signature must be 64 hex chars, got %d", len(sig))
+	}
```

---

### Issue 15 (High): webhook/webhook.go — Retry backoff not context-aware

```diff
--- a/webhook/webhook.go
+++ b/webhook/webhook.go
@@ -75,7 +75,7 @@ type Sender struct {

-func (s *Sender) Send(eventType string, payload []byte) (string, error) {
+func (s *Sender) Send(ctx context.Context, eventType string, payload []byte) (string, error) {
 	// ... setup ...

@@ -97,7 +97,11 @@ func (s *Sender) Send(eventType string, payload []byte) (string, error) {
 		// backoff before retry
-		time.Sleep(time.Duration(attempt*100) * time.Millisecond)
+		select {
+		case <-ctx.Done():
+			return id, ctx.Err()
+		case <-time.After(time.Duration(attempt*100) * time.Millisecond):
+		}
 	}
```

---

### Issue 16 (High): testkit/httpserver.go — Sequence panics on empty handlers

```diff
--- a/testkit/httpserver.go
+++ b/testkit/httpserver.go
@@ -67,6 +67,9 @@ func RoundTrip(handler http.Handler) *http.Client {
 // Sequence returns a handler that serves the given handlers in order.
 func Sequence(handlers ...http.Handler) http.Handler {
+	if len(handlers) == 0 {
+		panic("testkit.Sequence: called with no handlers")
+	}
 	var mu sync.Mutex
 	idx := 0
```

---

### Issue 17 (Medium): guard/ratelimit.go — Retry-After hardcoded

```diff
--- a/guard/ratelimit.go
+++ b/guard/ratelimit.go
@@ -116,7 +116,9 @@ func (rl *RateLimiter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
 	if !allowed {
-		w.Header().Set("Retry-After", "1")
+		// Estimate seconds until next token is available
+		retryAfter := int(math.Ceil(rl.window.Seconds()))
+		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
 		writeProblem(w, http.StatusTooManyRequests, "rate limit exceeded")
 		return
 	}
```

---

### Issue 18 (Medium): seal/seal.go — scrypt N=2^14 below OWASP recommendations

```diff
--- a/seal/seal.go
+++ b/seal/seal.go
@@ -39,7 +39,8 @@ const (
-	scryptN = 16384  // 2^14
+	// OWASP 2023 recommends N=2^17 for password hashing.
+	scryptN = 131072 // 2^17
 	scryptR = 8
 	scryptP = 1
 )
```

> **Note**: This increases `Encrypt`/`Decrypt` CPU time ~8x. If performance is critical, expose N/r/p as config options with the new default.

---

### Issue 19 (Medium): cache/cache.go — RWMutex declared but used as plain Mutex

```diff
--- a/cache/cache.go
+++ b/cache/cache.go
@@ -10,7 +10,8 @@ type Cache[K comparable, V any] struct {
-	mu sync.RWMutex
+	// Write lock used for all operations because LRU promotion mutates on read.
+	mu sync.Mutex
 	items map[K]*entry[K, V]
```

---

### Issue 20 (Medium): otel/otel_test.go — Per-test ResetVersionCheck is racy

```diff
--- a/otel/otel_test.go
+++ b/otel/otel_test.go
@@ -1,6 +1,7 @@
 package otel_test

 import (
+	"os"
 	"testing"

 	chassis "github.com/ai8future/chassis-go/v9"
@@ -8,6 +9,11 @@ import (
 )

+func TestMain(m *testing.M) {
+	chassis.RequireMajor(9)
+	os.Exit(m.Run())
+}
+
 func TestSetup(t *testing.T) {
-	chassis.ResetVersionCheck()
-	chassis.RequireMajor(9)
 	// ... rest of test unchanged, remove per-test Reset+Require calls
```

---

### Issue 21 (Medium): secval_test.go and registry_test.go — Missing RequireMajor(9)

```diff
--- a/secval/secval_test.go
+++ b/secval/secval_test.go
@@ -1,8 +1,15 @@
 package secval_test

 import (
+	"os"
 	"testing"
+
+	chassis "github.com/ai8future/chassis-go/v9"
 )
+
+func TestMain(m *testing.M) {
+	chassis.RequireMajor(9)
+	os.Exit(m.Run())
+}
```

Same pattern for `registry/registry_test.go`.

---

### Issue 22 (Medium): go.mod — x/crypto marked indirect

```diff
--- a/go.mod
+++ b/go.mod
@@ -24,7 +24,7 @@ require (
 )

 require (
-	golang.org/x/crypto v0.33.0 // indirect
+	golang.org/x/crypto v0.33.0
 )
```

Move `golang.org/x/crypto` from the indirect block to the direct `require` block since `seal/seal.go` imports it directly.

---

### Issue 23 (Medium): httpkit/middleware.go — Recovery->Tracing double-wrap

When chained as `Recovery -> Tracing -> handler`, both middlewares create their own `*responseWriter`. If the handler panics after `Tracing`'s wrapper called `WriteHeader`, `Recovery`'s outer wrapper won't detect it.

```diff
--- a/httpkit/middleware.go
+++ b/httpkit/middleware.go
@@ -134,9 +134,15 @@ func Recovery(logger *slog.Logger) func(http.Handler) http.Handler {
 		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
 			rw, ok := w.(*responseWriter)
 			if !ok {
-				rw = &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
-				w = rw
+				rw = &responseWriter{ResponseWriter: w, statusCode: http.StatusOK, headerWritten: false}
 			}
+			// Track whether ANY wrapper in the chain has written headers.
+			// Check the underlying ResponseWriter's state via Unwrap if available.
+			origRW := rw

 			defer func() {
 				if err := recover(); err != nil {
-					if !rw.headerWritten {
+					// Check if headers were written by any wrapper in the chain
+					if !origRW.headerWritten {
```

> **Note**: A complete fix requires implementing `http.ResponseController`-compatible `Unwrap()` on `responseWriter`. Document that `Recovery` must wrap `Logging` (not `Tracing` alone) for correct header-written detection.

---

## Clean Packages (No Issues Found)

The following packages were reviewed and found to be well-implemented:

- **call/call.go**: Circuit breaker state machine is correct; `cancelBody` properly defers context cancellation to body close.
- **call/token.go**: `CachedToken` mutex usage is correct; leeway logic is sound.
- **call/retry.go**: Backoff is context-aware with proper `ctx.Done()` select. Body draining on retry is correct.
- **call/breaker.go**: `stateProbing` sentinel is clean; `GetBreaker` uses `LoadOrStore` to avoid TOCTOU.
- **health/**: Clean implementation. `Watch` returning UNIMPLEMENTED is intentional.
- **guard/cors.go**: Correct CORS handling.
- **guard/ipfilter.go**: Correct IP filtering.
- **tick/tick.go**: Jitter implementation correct; context cancellation handled at both stages.
- **examples/**: All use `chassis.RequireMajor(9)`, handle errors from `lifecycle.Run`.
- **cmd/demo-shutdown/**: Correct shutdown pattern.

---

## Architectural Observations

**Strengths:**
- Zero cross-deps between packages — excellent toolkit design
- Version gate (`RequireMajor` + `AssertVersionChecked`) is a smart adoption safety mechanism
- Config package's reflection-based env loading with fail-fast panics is well-suited for 12-factor apps
- Guard package's constructor-panic pattern ensures invalid middleware never enters the chain
- OTel is sole SDK consumer; all other packages use API-only imports (correct boundary)

**Weaknesses:**
- `webhook` package has the most issues (3 critical/high) — needs the most attention
- Error handling philosophy is inconsistent: some packages panic, some log, some silently swallow
- No `context.Context` threading in `webhook.Send` — only package without context awareness
- `httpkit` wrapper layering model breaks when middlewares are composed in non-standard orders

---

## Recommended Priority

1. **Immediate**: Fix webhook data race (#1) and add replay protection (#2) — security critical
2. **Soon**: Fix timeout goroutine leak (#3) and WriteHeader no-op (#4) — production stability
3. **Next sprint**: Fix all High issues (#5-16) — correctness and API quality
4. **Backlog**: Medium issues (#17-25) — code quality and standards compliance
