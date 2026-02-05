Date Created: Thursday, February 5, 2026 at 11:21:00 AM UTC
TOTAL_SCORE: 83/100

# 1. AUDIT

## [CRITICAL] `guard.Timeout` Middleware Swallows Panics

The `Timeout` middleware executes the downstream handler in a separate goroutine to manage the timeout race. However, it fails to recover from panics within this goroutine. Since the `http.Server` and upstream `Recovery` middleware only monitor the primary request goroutine, a panic in the `Timeout` handler will bypass standard recovery mechanisms and crash the entire application process.

### PATCH-READY DIFF

```go
--- guard/timeout.go
+++ guard/timeout.go
@@ -35,7 +35,14 @@
 			done := make(chan struct{})
+			panicChan := make(chan any, 1)
 			tw := &timeoutWriter{w: w, req: r}
 			go func() {
+				defer func() {
+					if p := recover(); p != nil {
+						panicChan <- p
+					}
+				}()
 				next.ServeHTTP(tw, r)
 				close(done)
 			}()
 
 			select {
+			case p := <-panicChan:
+				panic(p)
 			case <-done:
 				// Handler finished in time — flush any buffered response.
 				tw.flush()
```

# 2. TESTS

## Verify Panic Propagation in Timeout Middleware

This test ensures that a panic occurring within a handler protected by `Timeout` is correctly propagated to the main goroutine and caught by the `Recovery` middleware.

### PATCH-READY DIFF

```go
--- guard/timeout_test.go
+++ guard/timeout_test.go
@@ -0,0 +1,41 @@
+package guard_test
+
+import (
+	"net/http"
+	"net/http/httptest"
+	"testing"
+	"time"
+
+	"github.com/ai8future/chassis-go/guard"
+)
+
+func TestTimeout_PropagatesPanic(t *testing.T) {
+	// Setup a handler that panics
+	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		panic("boom")
+	})
+
+	// Wrap with Timeout
+	timeoutHandler := guard.Timeout(100 * time.Millisecond)(handler)
+
+	// Wrap with a recovery-like checker to verify propagation
+	recovered := false
+	safeHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		defer func() {
+			if err := recover(); err != nil {
+				recovered = true
+				if err != "boom" {
+					t.Errorf("expected panic 'boom', got %v", err)
+				}
+			}
+		}()
+		timeoutHandler.ServeHTTP(w, r)
+	})
+
+	req := httptest.NewRequest("GET", "/", nil)
+	rec := httptest.NewRecorder()
+
+	safeHandler.ServeHTTP(rec, req)
+
+	if !recovered {
+		t.Error("panic was not propagated from timeout goroutine")
+	}
+}
```

# 3. FIXES

## `config.MustLoad` Reflection Panic on Pointer Types

Using `config.MustLoad[*T]()` currently causes a cryptic reflection panic (`reflect: Call of ... on Ptr Value`) because the code assumes the generic type `T` is a struct, not a pointer.

### PATCH-READY DIFF

```go
--- config/config.go
+++ config/config.go
@@ -30,6 +30,9 @@
 	var cfg T
 	v := reflect.ValueOf(&cfg).Elem()
 	t := v.Type()
+
+	if t.Kind() != reflect.Struct {
+		panic(fmt.Sprintf("config: MustLoad[T] requires T to be a struct, got %s", t.Kind()))
+	}
 
 	for i := range t.NumField() {
 		field := t.Field(i)
```

# 4. REFACTOR

## Optimize `logz` for Reduced Allocations

The `logz` package frequently allocates slices for attributes, especially in `traceHandler.Handle` when groups are active.

**Recommendations:**
1.  **Buffer Pooling:** Implement a `sync.Pool` for the `[]slog.Attr` slice used in record reconstruction.
2.  **Pre-computation:** In `WithGroup` and `WithAttrs`, pre-format constant attributes where possible instead of deferring all work to `Handle`.
3.  **Trace ID Injection:** The current `traceHandler` logic for re-wrapping groups to inject `trace_id` at the root is expensive. A more performant approach would be to implement a custom `slog.Handler` that writes directly to a buffer (using `slog.JSONHandler` logic adapted) or to simply accept `trace_id` being inside the group if that satisfies the requirement, though lifting it is cleaner. The current "unwrap and re-wrap" strategy is correct for the requirement but slow ($O(N)$ copies).
