Date Created: 2026-03-27T12:19:03-04:00
TOTAL_SCORE: 72/100

# Chassis-Go Codebase Audit Report

**Agent:** Claude Code (Claude:Opus 4.6)
**Module:** `github.com/ai8future/chassis-go/v10`
**Go Version:** 1.25.5
**Scope:** All 16 packages, 36 source files, 26 test files

---

## Scoring Breakdown

| Category                  | Max | Score | Notes                                                  |
|---------------------------|-----|-------|--------------------------------------------------------|
| Correctness               | 25  | 16    | Several real bugs in lifecycle, config, timeout         |
| Security                  | 20  | 13    | HSTS trust model, CORS caching, rate-limit Retry-After |
| Concurrency Safety        | 20  | 15    | Goroutine leak vectors, token lock convoy               |
| Error Handling            | 15  | 11    | Silent error discard in metrics, otel, registry         |
| API Design / Contracts    | 10  | 8     | MaxBody bypass, timeout deadline comparison             |
| Observability             | 10  | 9     | Span name = method-only, flagz indistinguishable events |
| **TOTAL**                 |**100**|**72**|                                                        |

---

## Issue Index

| #  | Severity | Package       | File:Line                    | Summary                                           |
|----|----------|---------------|------------------------------|----------------------------------------------------|
| 1  | HIGH     | lifecycle     | lifecycle.go:134             | heartbeatkit.Start receives raw ctx, not infraCtx  |
| 2  | HIGH     | guard         | secheaders.go:73             | HSTS emitted based on spoofable X-Forwarded-Proto  |
| 3  | HIGH     | guard         | timeout.go:40-67             | Goroutine leak when timeout fires on non-ctx handler|
| 4  | HIGH     | guard         | timeout.go:152-159           | I/O under mutex in timeout() -- deadlock risk       |
| 5  | HIGH     | guard         | maxbody.go:18-26             | Chunked bodies bypass 413 enforcement               |
| 6  | HIGH     | guard         | cors.go:72-74                | Case-sensitive origin + missing Vary on wildcard    |
| 7  | HIGH     | guard         | ratelimit.go:119             | Retry-After hardcoded to "1" regardless of window   |
| 8  | HIGH     | httpkit       | middleware.go:136-139        | Recovery type-asserts concrete *responseWriter      |
| 9  | MEDIUM   | config        | config.go:154-157,190-191    | Comma in regex pattern corrupts validate tag parse  |
| 10 | MEDIUM   | lifecycle     | lifecycle.go:141             | announcekit uses raw ctx, not signalCtx             |
| 11 | MEDIUM   | call          | token.go:44-59               | Token fetch holds mutex -- lock convoy / thundering herd |
| 12 | MEDIUM   | otel          | otel.go:78-81                | Trace exporter failure: early return leaves providers stale |
| 13 | MEDIUM   | otel          | otel.go:120-125              | Shared shutdownCtx for sequential tp+mp shutdown    |
| 14 | MEDIUM   | metrics       | metrics.go:192,202           | Counter/Histogram silently discard meter errors     |
| 15 | MEDIUM   | httpkit       | tracing.go:42                | Span name is HTTP method only -- all routes merged  |
| 16 | MEDIUM   | guard         | ipfilter.go:39-43            | ParseIP failure returns 403 with no log             |
| 17 | MEDIUM   | call          | call.go:160-172              | span.End()+cancel() not cleaned on panic in tokenSource |
| 18 | LOW      | logz          | logz.go:154-167              | WithGroup("") appends empty key instead of no-op    |
| 19 | LOW      | guard         | timeout.go:27-31             | Comment says "tighter wins" but code skips on any deadline |
| 20 | LOW      | health        | handler.go:41                | Error fallback sends text/plain Content-Type for JSON |
| 21 | LOW      | health        | handler.go:46                | w.Write return value ignored                        |
| 22 | LOW      | httpkit       | middleware.go:83-86          | Write without WriteHeader doesn't set statusCode=200|
| 23 | LOW      | metrics       | metrics.go:137,146           | slog.Warn called while holding r.mu write lock      |

---

## Detailed Findings with Patch-Ready Diffs

### Issue 1 -- heartbeatkit.Start receives wrong context (HIGH)

**File:** `lifecycle/lifecycle.go:134`

`heartbeatkit.Start` receives `ctx` (the raw parent context from the caller), not `infraCtx` (cancelled when the service shuts down). If the caller passed `context.Background()`, the heartbeat goroutine runs indefinitely after SIGTERM.

```diff
--- a/lifecycle/lifecycle.go
+++ b/lifecycle/lifecycle.go
@@ -131,7 +131,11 @@
 		}

 		// Start heartbeatkit.
-		heartbeatkit.Start(ctx, pub, heartbeatkit.Config{
+		// NOTE: infraCtx is not yet created at this point in the code.
+		// The heartbeatkit.Start and announcekit calls should be moved
+		// after infraCtx creation (line 148), or infraCtx should be
+		// created earlier. Using signalCtx as intermediate fix:
+		heartbeatkit.Start(signalCtx, pub, heartbeatkit.Config{
 			ServiceName: svcName,
 			Version:     chassis.Version,
 		})
```

**Ideal fix:** Move the kafkakit block (lines 119-144) to after `infraCtx` creation (line 148) and pass `infraCtx` instead. This ensures heartbeat stops when user components finish.

---

### Issue 2 -- HSTS header emitted based on spoofable X-Forwarded-Proto (HIGH)

**File:** `guard/secheaders.go:73`

`X-Forwarded-Proto` is a client-supplied header. Without a trusted proxy stripping it, any HTTP client can force HSTS emission on a non-TLS connection. No documentation warns about this trust assumption.

```diff
--- a/guard/secheaders.go
+++ b/guard/secheaders.go
@@ -39,6 +39,9 @@
 // SecurityHeaders returns middleware that sets security-related HTTP headers
 // before calling the next handler.
+//
+// IMPORTANT: The HSTS header trusts X-Forwarded-Proto unconditionally. Deploy
+// behind a reverse proxy that strips or overwrites this header from clients.
 func SecurityHeaders(cfg SecurityHeadersConfig) func(http.Handler) http.Handler {
 	chassis.AssertVersionChecked()
```

**Recommended enhancement:** Accept a `TrustedProxies []string` config field, and only trust `X-Forwarded-Proto` when the request originates from a listed proxy IP.

---

### Issue 3 -- Goroutine leak when timeout fires (HIGH)

**File:** `guard/timeout.go:40-67`

When `ctx.Done()` fires, the goroutine running `next.ServeHTTP(tw, r)` at line 50 continues indefinitely if the handler does not respect context cancellation. The handler's writes will return `http.ErrHandlerTimeout` (line 118), but a handler blocked on I/O (e.g., a database query with no context propagation) will leak its goroutine permanently.

This is a known limitation (matching `http.TimeoutHandler`), but should be documented and monitored.

```diff
--- a/guard/timeout.go
+++ b/guard/timeout.go
@@ -60,8 +60,10 @@
 			tw.flush()
 		case <-ctx.Done():
 			// Deadline exceeded -- write 504 if handler hasn't started writing.
-			// The goroutine may still be running but its context is cancelled;
-			// well-behaved handlers will return promptly. This matches the
-			// behavior of Go's stdlib http.TimeoutHandler.
+			// WARNING: The handler goroutine may still be running. If the
+			// handler does not respect ctx.Done() (e.g., blocked on I/O
+			// without context propagation), this goroutine leaks permanently.
+			// This matches the behavior of Go's stdlib http.TimeoutHandler.
+			// Monitor goroutine counts to detect leaks in production.
 			tw.timeout()
 		}
```

---

### Issue 4 -- I/O under mutex in timeout() (HIGH)

**File:** `guard/timeout.go:152-159`

`writeProblem` performs HTTP I/O (header writes, body encoding) while `tw.mu` is held. If the underlying `ResponseWriter.Write` blocks (slow client, TCP backpressure), the mutex is held for the duration, blocking the handler goroutine's `Write` calls from detecting `tw.written` and bailing out.

```diff
--- a/guard/timeout.go
+++ b/guard/timeout.go
@@ -151,9 +151,14 @@
 // timeout writes 504 with RFC 9457 Problem Details if the handler hasn't started writing yet.
 func (tw *timeoutWriter) timeout() {
 	tw.mu.Lock()
-	defer tw.mu.Unlock()
 	if tw.written || tw.started {
+		tw.mu.Unlock()
 		return
 	}
 	tw.written = true
+	tw.mu.Unlock()
+
+	// Write the problem response outside the lock to prevent blocking the
+	// handler goroutine's Write() calls while I/O completes.
 	writeProblem(tw.w, tw.req, errors.TimeoutError("request timed out"))
 }
```

---

### Issue 5 -- Chunked bodies bypass MaxBody 413 enforcement (HIGH)

**File:** `guard/maxbody.go:18-26`

The `Content-Length` check on line 19 only catches requests that declare their body size. Chunked transfer encoding (or requests with `Content-Length: -1` / `Content-Length: 0` that stream a body) bypass the early 413 rejection. `http.MaxBytesReader` on line 24 enforces the limit at read time, but the handler receives a `*http.MaxBytesError` on `Read()` -- not a clean 413 response.

```diff
--- a/guard/maxbody.go
+++ b/guard/maxbody.go
@@ -10,6 +10,10 @@

 // MaxBody returns middleware that rejects requests with a body exceeding
 // maxBytes with 413 Payload Too Large.
+//
+// Note: When Content-Length is absent (chunked encoding), the limit is still
+// enforced via http.MaxBytesReader at read time. Handlers should check for
+// *http.MaxBytesError when decoding the body and return 413 if encountered.
 func MaxBody(maxBytes int64) func(http.Handler) http.Handler {
 	chassis.AssertVersionChecked()
 	if maxBytes <= 0 {
```

**Recommended enhancement:** Wrap `next.ServeHTTP` and intercept `*http.MaxBytesError` to automatically return 413, providing full enforcement regardless of transfer encoding.

---

### Issue 6 -- CORS origin comparison is case-sensitive; missing Vary on wildcard (HIGH)

**File:** `guard/cors.go:52-75,83-84`

**(a)** The origin map lookup at line 74 (`origins[origin]`) is case-sensitive. Per RFC 6454, origin scheme and host are case-insensitive. A request with `Origin: HTTPS://EXAMPLE.COM` won't match `https://example.com`.

**(b)** Wildcard responses (`Access-Control-Allow-Origin: *`) on non-OPTIONS requests omit `Vary: Origin`. CDNs may cache the wildcard response and serve it to non-CORS requests that should receive no CORS headers, or vice versa.

```diff
--- a/guard/cors.go
+++ b/guard/cors.go
@@ -52,7 +52,7 @@
 	wildcard := false
 	origins := make(map[string]struct{}, len(cfg.AllowOrigins))
 	for _, o := range cfg.AllowOrigins {
-		if o == "*" {
+		if strings.EqualFold(o, "*") {
 			wildcard = true
 		}
-		origins[o] = struct{}{}
+		origins[strings.ToLower(o)] = struct{}{}
 	}

 	return func(next http.Handler) http.Handler {
@@ -63,6 +63,7 @@
 			origin := r.Header.Get("Origin")
 			if origin == "" {
 				// Not a CORS request -- pass through.
+				w.Header().Add("Vary", "Origin")
 				next.ServeHTTP(w, r)
 				return
 			}
@@ -71,7 +72,7 @@
 			allowed := wildcard
 			if !allowed {
-				_, allowed = origins[origin]
+				_, allowed = origins[strings.ToLower(origin)]
 			}
 			if !allowed {
 				// Origin not allowed -- pass through without CORS headers.
```

---

### Issue 7 -- Retry-After hardcoded to "1" regardless of window (HIGH)

**File:** `guard/ratelimit.go:119`

`Retry-After: 1` is emitted regardless of the configured `Window`. A rate limiter with a 1-hour window tells clients to retry in 1 second, causing immediate re-rate-limiting and wasting resources.

```diff
--- a/guard/ratelimit.go
+++ b/guard/ratelimit.go
@@ -114,10 +114,15 @@
 	lim := newLimiter(cfg.Rate, cfg.Window, cfg.MaxKeys)
+
+	// Pre-compute Retry-After: time for one token to refill.
+	retryAfterSec := int(cfg.Window.Seconds()) / cfg.Rate
+	if retryAfterSec < 1 {
+		retryAfterSec = 1
+	}
+	retryAfter := strconv.Itoa(retryAfterSec)
+
 	return func(next http.Handler) http.Handler {
 		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
 			key := cfg.KeyFunc(r)
 			if !lim.allow(key) {
-				w.Header().Set("Retry-After", "1")
+				w.Header().Set("Retry-After", retryAfter)
 				writeProblem(w, r, errors.RateLimitError("rate limit exceeded"))
 				return
 			}
```

Note: requires adding `"strconv"` to the import block.

---

### Issue 8 -- Recovery type-asserts concrete *responseWriter (HIGH)

**File:** `httpkit/middleware.go:136-139`

`Recovery` does `w.(*responseWriter)` which only succeeds if the immediate `w` is `*responseWriter`. If any third-party middleware wraps `w` between `Tracing`/`Logging` and `Recovery`, the assertion fails, a second `*responseWriter` is allocated, and `headerWritten` state diverges between the two layers.

```diff
--- a/httpkit/middleware.go
+++ b/httpkit/middleware.go
@@ -133,7 +133,15 @@
 		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
 			registry.AssertActive()
 			// Ensure we have a responseWriter to track headerWritten state,
 			// whether or not Logging/Tracing middleware has already wrapped w.
-			rw, ok := w.(*responseWriter)
+			var rw *responseWriter
+			ok := false
+			// Walk the wrapper chain via Unwrap() to find an existing responseWriter.
+			candidate := w
+			for candidate != nil {
+				if rw, ok = candidate.(*responseWriter); ok {
+					break
+				}
+				u, isUnwrapper := candidate.(interface{ Unwrap() http.ResponseWriter })
+				if !isUnwrapper {
+					break
+				}
+				candidate = u.Unwrap()
+			}
 			if !ok {
 				rw = &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
 				w = rw
```

---

### Issue 9 -- Comma in regex pattern corrupts validate tag parsing (MEDIUM)

**File:** `config/config.go:154-157,190-191`

The validate tag is split by comma: `strings.Split(tag, ",")`. A regex pattern containing a comma (e.g., `validate:"pattern=^[a-z]{1,10}$"`) is split into `pattern=^[a-z]{1` and `10}$`. The first part compiles an invalid regex via `regexp.MustCompile`, causing an opaque panic. The second part hits the default no-op case.

```diff
--- a/config/config.go
+++ b/config/config.go
@@ -151,7 +151,13 @@
 // validateField checks a populated field against constraints in the validate
 // struct tag. Supported keys: min, max, oneof, pattern. Multiple constraints
-// are comma-separated (e.g. validate:"min=1,max=65535").
+// are comma-separated (e.g. validate:"min=1,max=65535"). The pattern constraint
+// must be the last constraint in the tag because its value may contain commas.
 func validateField(name string, val reflect.Value, tag string) {
-	parts := strings.Split(tag, ",")
-	for _, part := range parts {
+	// Handle "pattern=" specially: it must be the last constraint because
+	// its regex value may contain commas.
+	var patternValue string
+	if idx := strings.Index(tag, "pattern="); idx >= 0 {
+		patternValue = tag[idx+len("pattern="):]
+		tag = strings.TrimRight(tag[:idx], ",")
+	}
+	parts := strings.Split(tag, ",")
+	for _, part := range parts {
 		key, value, _ := strings.Cut(strings.TrimSpace(part), "=")
 		switch key {
@@ -189,7 +195,15 @@
-		case "pattern":
-			re := regexp.MustCompile(value)
+		}
+	}
+
+	// Handle pattern constraint (extracted above to preserve commas).
+	if patternValue != "" {
+		re, err := regexp.Compile(patternValue)
+		if err != nil {
+			panic(fmt.Sprintf("config: field %s has invalid pattern %q in validate tag: %v", name, patternValue, err))
+		}
 			actual := fmt.Sprintf("%v", val.Interface())
 			if !re.MatchString(actual) {
-				panic(fmt.Sprintf("config: field %s value %q does not match pattern %s", name, actual, value))
+			panic(fmt.Sprintf("config: field %s value %q does not match pattern %s", name, actual, patternValue))
 			}
-		}
 	}
 }
```

Additionally, `regexp.MustCompile` should be replaced with `regexp.Compile` + a contextual panic message for any remaining pattern usage (see diff above).

---

### Issue 10 -- announcekit uses raw ctx instead of signalCtx (MEDIUM)

**File:** `lifecycle/lifecycle.go:141`

`announcekit.Started` receives `ctx` (the raw parent) instead of `signalCtx`. If the parent context is already cancelled, the announcement silently fails.

```diff
--- a/lifecycle/lifecycle.go
+++ b/lifecycle/lifecycle.go
@@ -139,7 +139,7 @@
 		// Announce service started (best-effort with timeout).
 		announcekit.SetServiceName(svcName)
-		announceCtx, announceCancel := context.WithTimeout(ctx, AnnounceTimeout)
+		announceCtx, announceCancel := context.WithTimeout(signalCtx, AnnounceTimeout)
 		_ = announcekit.Started(announceCtx, pub)
 		announceCancel()
```

---

### Issue 11 -- Token fetch holds entire mutex during network call (MEDIUM)

**File:** `call/token.go:44-59`

The full token fetch (potentially a slow network call to an OAuth server) runs while holding `ct.mu`. All concurrent callers block for the duration.

```diff
--- a/call/token.go
+++ b/call/token.go
@@ -1,6 +1,7 @@
 package call

 import (
 	"context"
+	"golang.org/x/sync/singleflight"
 	"sync"
 	"time"
 )
@@ -14,6 +15,7 @@
 	fetch   func(ctx context.Context) (token string, expiresAt time.Time, err error)
 	leeway  time.Duration
 	mu      sync.Mutex
+	sf      singleflight.Group
 	token   string
 	expires time.Time
 }
@@ -43,14 +45,25 @@
 // Token returns a cached token, refreshing if needed.
 func (ct *CachedToken) Token(ctx context.Context) (string, error) {
 	ct.mu.Lock()
-	defer ct.mu.Unlock()
-
 	if ct.token != "" && time.Now().Add(ct.leeway).Before(ct.expires) {
+		t := ct.token
+		ct.mu.Unlock()
+		return t, nil
+	}
+	ct.mu.Unlock()
+
+	// Coalesce concurrent refreshes via singleflight.
+	v, err, _ := ct.sf.Do("refresh", func() (any, error) {
+		token, expires, err := ct.fetch(ctx)
+		if err != nil {
+			return "", err
+		}
+		ct.mu.Lock()
+		ct.token = token
+		ct.expires = expires
+		ct.mu.Unlock()
+		return token, nil
+	})
+	if err != nil {
+		return "", err
+	}
-		return ct.token, nil
+	return v.(string), nil
 	}
-
-	token, expires, err := ct.fetch(ctx)
-	if err != nil {
-		return "", err
-	}
-	ct.token = token
-	ct.expires = expires
-	return token, nil
 }
```

---

### Issue 12 -- OTel trace exporter failure leaves stale providers (MEDIUM)

**File:** `otel/otel.go:78-81`

When `otlptracegrpc.New` fails, `Init` returns a no-op shutdown without calling `otel.SetTracerProvider`. If a previous `Init` call set a provider (e.g., from a test), that provider is silently retained.

```diff
--- a/otel/otel.go
+++ b/otel/otel.go
@@ -78,6 +78,8 @@
 	traceExporter, err := otlptracegrpc.New(ctx, traceOpts...)
 	if err != nil {
 		slog.Error("otel: trace exporter creation failed, all telemetry disabled", "error", err)
+		otel.SetTracerProvider(trace.NewNoopTracerProvider())
+		otel.SetMeterProvider(metric.NewMeterProvider()) // no-op without reader
 		return func(ctx context.Context) error { return nil }
 	}
```

---

### Issue 13 -- Shared shutdown context for sequential provider shutdowns (MEDIUM)

**File:** `otel/otel.go:120-125`

A single 5-second `shutdownCtx` is shared by `tp.Shutdown` and `mp.Shutdown` called sequentially. If `tp.Shutdown` takes the full 5 seconds, `mp.Shutdown` gets an expired context.

```diff
--- a/otel/otel.go
+++ b/otel/otel.go
@@ -120,8 +120,10 @@
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

### Issue 14 -- Counter/Histogram silently discard meter creation errors (MEDIUM)

**File:** `metrics/metrics.go:192,202`

```diff
--- a/metrics/metrics.go
+++ b/metrics/metrics.go
@@ -190,7 +190,10 @@
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
@@ -199,7 +202,10 @@
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

### Issue 15 -- Span name is HTTP method only (MEDIUM)

**File:** `httpkit/tracing.go:42`

All GET requests produce a span named `"GET"`, making per-route analysis impossible in tracing backends.

```diff
--- a/httpkit/tracing.go
+++ b/httpkit/tracing.go
@@ -41,7 +41,7 @@
 			tracer := otelapi.GetTracerProvider().Tracer(tracerName)
-			spanName := r.Method
+			spanName := r.Method + " " + r.URL.Path

 			ctx, span := tracer.Start(ctx, spanName,
```

Note: This increases cardinality. For route-templated span names, the mux should set the route template in the context and Tracing should read it. The path-based approach is a better default than method-only.

---

### Issue 16 -- IPFilter ParseIP failure returns 403 with no log (MEDIUM)

**File:** `guard/ipfilter.go:39-43`

When `net.ParseIP` fails (e.g., KeyFunc returned a hostname), a generic 403 is returned with no logging, making misconfiguration impossible to diagnose.

```diff
--- a/guard/ipfilter.go
+++ b/guard/ipfilter.go
@@ -1,6 +1,7 @@
 package guard

 import (
+	"log/slog"
 	"net"
 	"net/http"
@@ -39,6 +40,7 @@
 			host := keyFunc(r)
 			ip := net.ParseIP(host)
 			if ip == nil {
+				slog.Warn("guard: IPFilter could not parse IP from KeyFunc", "raw", host, "remote", r.RemoteAddr)
 				writeProblem(w, r, errors.ForbiddenError("access denied"))
 				return
 			}
```

---

### Issue 17 -- span.End() and cancel() not cleaned up on panic in tokenSource (MEDIUM)

**File:** `call/call.go:160-172`

If `c.tokenSource.Token()` panics, neither `span.End()` nor `cancel()` is called. The four early-return paths each manually manage cleanup -- a systematic fragility.

```diff
--- a/call/call.go
+++ b/call/call.go
@@ -145,6 +145,14 @@
 	)
 	req = req.WithContext(ctx)
 	otelapi.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
+
+	// Ensure span and cancel are cleaned up on any early return or panic.
+	var committed bool
+	defer func() {
+		if !committed {
+			span.End()
+			if cancel != nil { cancel() }
+		}
+	}()

 	// Token injection
 	if c.tokenSource != nil {
@@ -161,8 +169,6 @@
 		if err != nil {
 			span.RecordError(err)
 			span.SetStatus(codes.Error, err.Error())
-			span.End()
-			if cancel != nil { cancel() }
 			return nil, fmt.Errorf("call: token source: %w", err)
 		}
 		req.Header.Set("Authorization", "Bearer "+token)
@@ -175,8 +181,6 @@
 		if err := c.breaker.Allow(); err != nil {
 			span.AddEvent("circuit_breaker_rejected")
-			span.End()
 			// ... record metric ...
-			if cancel != nil { cancel() }
 			return nil, err
 		}
 	}
@@ -258,7 +262,7 @@
 	}
-	span.End()
+	committed = true
+	span.End()

-	// ... metric recording ...
+	// ... metric recording unchanged ...

 	// If we created a cancel func, attach it to the response body
 	if cancel != nil {
```

This is a structural refactor -- the `committed` flag ensures `span.End()` and `cancel()` are always called exactly once, regardless of which path exits.

---

### Issue 18 -- WithGroup("") appends empty key (LOW)

**File:** `logz/logz.go:154-167`

Per `slog.Handler` contract, `WithGroup("")` should return the receiver unchanged.

```diff
--- a/logz/logz.go
+++ b/logz/logz.go
@@ -153,6 +153,9 @@
 // WithGroup returns a new traceHandler wrapping the inner handler's WithGroup result.
 func (h *traceHandler) WithGroup(name string) slog.Handler {
+	if name == "" {
+		return h
+	}
 	newGroups := make([]string, len(h.groups)+1)
```

---

### Issue 19 -- Timeout deadline comparison comment vs code mismatch (LOW)

**File:** `guard/timeout.go:27-31`

Comment says "tighter deadline wins" but the code skips on *any* existing deadline, even a looser one.

```diff
--- a/guard/timeout.go
+++ b/guard/timeout.go
@@ -26,8 +26,10 @@
 			ctx := r.Context()
-			if _, ok := ctx.Deadline(); ok {
-				// Caller already set a deadline -- respect it, don't override.
+			if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= d {
+				// Caller already set a tighter deadline -- respect it.
 				next.ServeHTTP(w, r)
 				return
 			}
```

This ensures the timeout middleware applies when the caller's deadline is *looser* than the configured duration.

---

### Issue 20 -- Health error fallback sends wrong Content-Type (LOW)

**File:** `health/handler.go:39-42`

```diff
--- a/health/handler.go
+++ b/health/handler.go
@@ -39,7 +39,9 @@
 		if encErr := json.NewEncoder(&buf).Encode(...); encErr != nil {
 			slog.ErrorContext(r.Context(), "health: failed to encode response", "error", encErr)
-			http.Error(w, `{"status":"error"}`, http.StatusInternalServerError)
+			w.Header().Set("Content-Type", "application/json")
+			w.WriteHeader(http.StatusInternalServerError)
+			w.Write([]byte(`{"status":"error"}`))
 			return
 		}
```

---

### Issue 21 -- health handler ignores w.Write error (LOW)

**File:** `health/handler.go:46`

```diff
--- a/health/handler.go
+++ b/health/handler.go
@@ -44,5 +44,7 @@
 		w.Header().Set("Content-Type", "application/json")
 		w.WriteHeader(code)
-		w.Write(buf.Bytes())
+		if _, err := w.Write(buf.Bytes()); err != nil {
+			slog.ErrorContext(r.Context(), "health: failed to write response", "error", err)
+		}
 	})
```

---

### Issue 22 -- responseWriter.Write doesn't set statusCode on implicit 200 (LOW)

**File:** `httpkit/middleware.go:83-86`

While the constructor initializes `statusCode: http.StatusOK`, the `Write` method should defensively set it for correctness.

```diff
--- a/httpkit/middleware.go
+++ b/httpkit/middleware.go
@@ -83,6 +83,7 @@
 func (rw *responseWriter) Write(b []byte) (int, error) {
 	if !rw.headerWritten {
+		rw.statusCode = http.StatusOK
 		rw.headerWritten = true
 	}
 	return rw.ResponseWriter.Write(b)
```

---

### Issue 23 -- slog.Warn called under write lock (LOW)

**File:** `metrics/metrics.go:137,146-156`

`warnOnceOverflowLocked` calls `r.logger.Warn()` while `r.mu` is held as a write lock. If the slog handler acquires its own lock, this creates a lock-ordering risk.

```diff
--- a/metrics/metrics.go
+++ b/metrics/metrics.go
@@ -136,7 +136,8 @@
 	if len(r.seenCombos[metricName]) >= MaxLabelCombinations {
-		r.warnOnceOverflowLocked(metricName)
+		shouldWarn := r.markOverflowLocked(metricName)
+		r.mu.Unlock()   // requires removing `defer r.mu.Unlock()` above
+		if shouldWarn && r.logger != nil {
+			r.logger.Warn("metrics cardinality limit reached", "metric", metricName, "limit", MaxLabelCombinations)
+		}
 		return false
 	}
```

This is a structural change that requires refactoring the defer pattern. The `markOverflowLocked` helper would just set the flag and return whether to log.

---

## Additional Observations (Not Scored)

1. **`work/work.go` Stream pattern (lines 285-318):** `goto drain` exits the input loop without draining `in`. If the producer doesn't share the same context, its goroutine leaks trying to send into the unconsumed channel. Should be documented.

2. **`registry/registry.go:531`:** `ShutdownCLI` calls `atomicWrite` and discards the error. A failed PID file update leaves the registry showing "running" after exit.

3. **`grpckit/interceptors.go`:** `registry.AssertActive()` is called on every RPC (by design, per CHANGELOG v6.0.11). This adds an atomic load per RPC. Consider also asserting at interceptor construction time.

4. **`otel/otel.go:60-69`:** `resource.New` can return a partial resource AND an error. The partial resource is discarded in favor of `resource.Default()`. Merging them would preserve partial attributes.

5. **`config/config.go:53`:** The `time.Duration` guard is unnecessary -- `time.Duration` is `int64`, not a struct. The recursion guard should check for `time.Time` instead (which IS a struct).

---

## Summary

The codebase demonstrates solid Go engineering with proper version gating, fail-fast panics, and structured OTel integration. The main areas of concern are:

- **Lifecycle context wiring** (Issues 1, 10): Critical background services receive the wrong context and may outlive the service.
- **Guard middleware security** (Issues 2, 5, 6, 7): Several middleware components have incomplete enforcement or trust untrusted inputs.
- **Concurrency patterns** (Issues 4, 11): Lock contention under load in timeout writer and token cache.
- **Error handling hygiene** (Issues 12, 13, 14): Silent error discard in OTel and metrics initialization.

The 72/100 score reflects a codebase that is well-structured and largely correct for the happy path, but has meaningful gaps in edge-case handling, security hardening, and shutdown correctness that would surface under production load or adversarial conditions.
