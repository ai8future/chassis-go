Date Created: Thu Feb  5 11:17:26 CET 2026
TOTAL_SCORE: 82/100

## Executive Summary

The `chassis-go` codebase is well-structured, modern (Go 1.23+), and follows good engineering practices including structured logging (log/slog implied), OpenTelemetry integration, and clean module separation. The test coverage appears robust for the implemented features.

However, several issues were identified that could impact production stability, particularly concerning memory management in long-running services and potential deadlocks in concurrency primitives. The overall score of **82/100** reflects a solid foundation with specific, addressable flaws.

## Issues Found

### 1. Unbounded Memory Growth in Rate Limiter (Critical)
**File:** `guard/ratelimit.go`
**Description:** The `limiter` struct uses a `map[string]*bucket` to track rate limits per key. This map is never cleaned up. If the application uses high-cardinality keys (e.g., IP addresses, ephemeral user IDs), this map will grow indefinitely, leading to an Out-Of-Memory (OOM) crash.
**Remediation:** Implement a simple eviction strategy. A "clear-all" approach when the map exceeds a threshold is a simple, dependency-free fix that trades a temporary accuracy glitch for memory safety.

### 2. Potential Goroutine Leak / Deadlock in Stream Processing (High)
**File:** `work/work.go`
**Description:** In `Stream`, workers send results to an unbuffered channel. If the context is cancelled, the consumer might stop reading from the channel. The workers, currently unaware of the context cancellation during the send operation, will block forever on `out <- ...`, holding the semaphore and preventing `wg.Wait()` from returning. This leads to a goroutine leak and potentially hangs the application shutdown.
**Remediation:** Use a `select` statement when sending to the output channel to handle context cancellation.

### 3. Redundant and Complex Timeout Handling (Medium)
**File:** `call/call.go`
**Description:** The `Client.Do` method manually creates a `context.WithTimeout` and wraps the response body in a custom `cancelBody` to manage the lifecycle. This is redundant because the underlying `http.Client` already handles timeouts via its `Timeout` field (which is correctly set in `New`). The manual management adds unnecessary complexity and overhead without providing clear benefit over the standard library's behavior.
**Remediation:** Remove the manual context management and `cancelBody` wrapper, relying on `http.Client`'s native timeout mechanism.

### 4. Response Buffering in Timeout Middleware (Risk)
**File:** `guard/timeout.go`
**Description:** The `Timeout` middleware buffers the *entire* response body in memory to allow sending a 504 error if the handler is too slow. For large responses (files, large JSON dumps), this can cause significant memory pressure.
**Recommendation:** Document this limitation clearly. For a complete fix, a streaming approach that simply closes the connection on timeout (without sending 504 if headers were already sent) would be more robust but changes the behavior. No patch provided for this report as it requires a design decision.

### 5. Unbounded Circuit Breaker Registry (Medium)
**File:** `call/breaker.go`
**Description:** Similar to the rate limiter, the global `breakers` sync.Map grows indefinitely.
**Recommendation:** Monitoring is advised. As this is a global registry, fixing it requires API changes or a background cleaner.

## Proposed Fixes

### Fix 1: Cap Rate Limiter Map Size

```diff
--- guard/ratelimit.go
+++ guard/ratelimit.go
@@ -37,6 +37,10 @@
 	now := time.Now()
 	b, ok := l.buckets[key]
 	if !ok {
+		// Prevent unbounded growth of the buckets map
+		if len(l.buckets) > 10000 {
+			l.buckets = make(map[string]*bucket)
+		}
 		b = &bucket{tokens: float64(l.rate), lastFill: now}
 		l.buckets[key] = b
 	}
```

### Fix 2: Prevent Deadlock in Work Stream

```diff
--- work/work.go
+++ work/work.go
@@ -165,7 +165,10 @@
 					childSpan.RecordError(err)
 				}
 				childSpan.End()
-				out <- Result[R]{Value: val, Err: err, Index: currentIdx}
+				select {
+				case out <- Result[R]{Value: val, Err: err, Index: currentIdx}:
+				case <-ctx.Done():
+				}
 			}()
 		}
```

### Fix 3: Simplify HTTP Client Timeout

```diff
--- call/call.go
+++ call/call.go
@@ -3,7 +3,6 @@
 import (
 	"context"
 	"fmt"
-	"io"
 	"net/http"
 	"sync"
 	"time"
@@ -29,19 +28,6 @@
 	return clientDuration
 }
 
-// cancelBody wraps a response body so that a context cancel function is called
-// when the body is closed, rather than when Do() returns. This prevents
-// premature context cancellation from interrupting callers reading the body.
-type cancelBody struct {
-	io.ReadCloser
-	cancel context.CancelFunc
-}
-
-func (b *cancelBody) Close() error {
-	err := b.ReadCloser.Close()
-	b.cancel()
-	return err
-}
-
 // Client is a resilient HTTP client that wraps the standard http.Client with
 // optional retry, circuit breaker, and timeout middleware. Construct one using
 // New with functional options.
@@ -95,14 +81,7 @@
 func (c *Client) Do(req *http.Request) (*http.Response, error) {
 	start := time.Now()
 
-	// Ensure the request always has a context with a deadline.
 	ctx := req.Context()
-	var cancel context.CancelFunc
-	if _, ok := ctx.Deadline(); !ok {
-		ctx, cancel = context.WithTimeout(ctx, c.timeout)
-		req = req.WithContext(ctx)
-	}
 
 	// OTel: create client span and inject trace headers.
 	tracer := otelapi.GetTracerProvider().Tracer(tracerName)
@@ -124,9 +103,6 @@
 					attribute.String("error.type", fmt.Sprintf("%T", err)),
 				),
 			)
-			if cancel != nil {
-				cancel()
-			}
 			return nil, err
 		}
 	}
@@ -204,15 +180,5 @@
 		metric.WithAttributes(durationAttrs...),
 	)
 
-	// If we created a cancel func, attach it to the response body so the
-	// context lives until the caller closes the body. On error, cancel now.
-	if cancel != nil {
-		if err != nil || resp == nil {
-			cancel()
-		} else {
-			resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: cancel}
-		}
-	}
-
 	return resp, err
 }
```
