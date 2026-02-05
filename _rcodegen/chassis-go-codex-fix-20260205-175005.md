Date Created: 2026-02-05 17:50:05 +0100
TOTAL_SCORE: 86/100

**Summary**
- Quick scan identified two correctness issues in HTTP middleware wrapping and retry backoff jitter handling.
- Fixes are provided as patch-ready diffs only because the request explicitly forbids editing code.
- No VERSION or CHANGELOG updates were made and no commits were created due to the no-edit constraint.

**Findings**
1. `httpkit` response wrapper does not implement optional interfaces (Flusher, Hijacker, Pusher, ReaderFrom) and does not mark headers as written on `Write`. This can break streaming, WebSocket upgrades, and can cause `Recovery` to emit an error response after headers/body have already been written, corrupting responses.
2. `call.Retrier` panics when `BaseDelay` is zero or very small because `rand.Int64N` is called with `0`. This is a runtime panic under common configurations (for example, retry enabled with `baseDelay=0`).

**Patch-Ready Diffs**
```diff
diff --git a/httpkit/middleware.go b/httpkit/middleware.go
index 9c7c7a7..bfa7a56 100644
--- a/httpkit/middleware.go
+++ b/httpkit/middleware.go
@@ -1,13 +1,16 @@
 package httpkit
 
 import (
+    "bufio"
 	"context"
 	"crypto/rand"
 	"fmt"
+    "io"
 	"log/slog"
+    "net"
 	"net/http"
 	"runtime/debug"
 	"time"
@@ -50,6 +53,44 @@ type responseWriter struct {
 	statusCode    int
 	headerWritten bool
 }
 
+// Write ensures headerWritten is tracked even when handlers call Write without WriteHeader.
+func (rw *responseWriter) Write(p []byte) (int, error) {
+	if !rw.headerWritten {
+		rw.headerWritten = true
+		if rw.statusCode == 0 {
+			rw.statusCode = http.StatusOK
+		}
+	}
+	return rw.ResponseWriter.Write(p)
+}
+
+// Flush forwards to the underlying http.Flusher when available.
+func (rw *responseWriter) Flush() {
+	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
+		f.Flush()
+	}
+}
+
+// Hijack forwards to the underlying http.Hijacker when available.
+func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
+	h, ok := rw.ResponseWriter.(http.Hijacker)
+	if !ok {
+		return nil, nil, fmt.Errorf("httpkit: responseWriter does not support hijacking")
+	}
+	return h.Hijack()
+}
+
+// Push forwards to the underlying http.Pusher when available.
+func (rw *responseWriter) Push(target string, opts *http.PushOptions) error {
+	if p, ok := rw.ResponseWriter.(http.Pusher); ok {
+		return p.Push(target, opts)
+	}
+	return http.ErrNotSupported
+}
+
+// ReadFrom forwards to the underlying io.ReaderFrom when available.
+func (rw *responseWriter) ReadFrom(r io.Reader) (int64, error) {
+	if rf, ok := rw.ResponseWriter.(io.ReaderFrom); ok {
+		return rf.ReadFrom(r)
+	}
+	return io.Copy(rw.ResponseWriter, r)
+}
+
 // WriteHeader captures the status code before delegating to the underlying writer.
 // Only the first call's status code is recorded for logging/metrics accuracy.
 func (rw *responseWriter) WriteHeader(code int) {
@@ -131,7 +172,10 @@ func Recovery(logger *slog.Logger) func(http.Handler) http.Handler {
 	return func(next http.Handler) http.Handler {
 		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
 			rw, isWrapped := w.(*responseWriter)
+			if !isWrapped {
+				rw = &responseWriter{ResponseWriter: w}
+			}
 			defer func() {
 				if err := recover(); err != nil {
 					stack := debug.Stack()
 					logger.Error("panic recovered",
 						"error", fmt.Sprint(err),
 						"stack", string(stack),
 					)
-					if isWrapped && rw.headerWritten {
+					if rw.headerWritten {
 						return // headers already sent — cannot write error response
 					}
-					JSONError(w, r, http.StatusInternalServerError, "internal server error")
+					JSONError(rw, r, http.StatusInternalServerError, "internal server error")
 				}
 			}()
-			next.ServeHTTP(w, r)
+			next.ServeHTTP(rw, r)
 		})
 	}
 }
```

```diff
diff --git a/call/retry.go b/call/retry.go
index a7f4d3c..3b3c169 100644
--- a/call/retry.go
+++ b/call/retry.go
@@ -78,10 +78,17 @@ func (r *Retrier) backoff(ctx context.Context, attempt int) error {
 	for range attempt {
 		delay *= 2
 	}
 
+	if delay <= 0 {
+		return nil
+	}
+
 	// Add jitter: random duration in [0, delay/2).
-	jitter := time.Duration(rand.Int64N(int64(delay / 2)))
-	delay += jitter
+	jitterBound := delay / 2
+	if jitterBound > 0 {
+		jitter := time.Duration(rand.Int64N(int64(jitterBound)))
+		delay += jitter
+	}
*** End Patch
```

**Notes**
- These diffs are not applied in the working tree to respect the DO NOT EDIT CODE requirement.
- If you want these fixes applied, I will also update `VERSION`, annotate `CHANGELOG.md`, and commit/push per AGENTS.md.

**Tests**
- Not run (no code changes applied). Suggested after applying patches: `go test ./...`
