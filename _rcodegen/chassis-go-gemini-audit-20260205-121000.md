Date Created: 2026-02-05 12:10:00
TOTAL_SCORE: 78/100

# Audit Report: chassis-go

## Executive Summary
The `chassis-go` project demonstrates a modern, modular Go architecture utilizing Go 1.25 features, `log/slog`, and OpenTelemetry. The code is generally clean, well-documented, and follows idiomatic patterns. However, the audit revealed significant reliability and security concerns, specifically a potential Denial-of-Service (DoS) vector in the rate limiter and unsafe panic usage in library code (Retry and HTTP middleware).

## Scoring Breakdown
- **Architecture & Documentation (20/20):** Excellent modular design and clear comments.
- **Security (15/25):** Rate limiter implementation has a CPU exhaustion DoS vector. `secval` relies on a blacklist.
- **Reliability (18/25):** Library code contains panics on zero-value configuration (`BaseDelay`) and rare errors (`crypto/rand`), which can crash consuming services. `lifecycle` silently ignores invalid arguments.
- **Testing (25/30):** Test coverage is present for most modules, though edge cases causing panics were missed.

**Total:** 78/100

## Key Findings & Recommendations

### 1. High Severity: Rate Limiter DoS Vector
**Location:** `guard/ratelimit.go`
**Issue:** The `allow` method performs a full O(N) sweep of the `buckets` map inside a mutex lock when `len(buckets) >= maxBuckets`. If an attacker fills the map with unique keys (e.g., via IP spoofing), every subsequent request triggers this loop. If the buckets are not yet stale, nothing is deleted, keeping the map full and forcing the O(N) loop on every request, causing CPU exhaustion and high latency.
**Recommendation:** Enforce a minimum interval between sweeps to prevent "sweep storms" during an attack.

### 2. Medium Severity: Retry Panic on Zero Configuration
**Location:** `call/retry.go`
**Issue:** If `Retrier` is zero-initialized (defaulting `BaseDelay` to 0), `rand.Int64N(int64(delay / 2))` calls `rand.Int64N(0)`, which panics. Library code should safely handle zero values.
**Recommendation:** Set a default `BaseDelay` if it is zero.

### 3. Medium Severity: Lifecycle Silently Ignores Invalid Arguments
**Location:** `lifecycle/lifecycle.go`
**Issue:** The `Run` function accepts `args ...any` but only processes specific types. If a user passes an invalid type (e.g., a struct instead of a function), it is silently ignored, leading to a service potentially running without critical components.
**Recommendation:** Return an error if an argument does not match the expected types.

### 4. Low Severity: Middleware Panic
**Location:** `httpkit/middleware.go`
**Issue:** `generateID` panics if `crypto/rand.Read` fails. While rare, a library should prefer returning an error (yielding a 500 status) over crashing the process.
**Recommendation:** Propagate errors from ID generation.

---

## Patch-Ready Diffs

### Fix 1: Rate Limiter Sweep Protection

```diff
--- guard/ratelimit.go
+++ guard/ratelimit.go
@@ -28,6 +28,10 @@
 // forced and the oldest idle buckets are evicted.
 const maxBuckets = 100_000
 
+// minSweepInterval prevents CPU exhaustion by limiting how often
+// we scan the map when it is full.
+const minSweepInterval = 1 * time.Second
+
 type limiter struct {
 	mu        sync.Mutex
 	buckets   map[string]*bucket
@@ -49,14 +53,16 @@
 	now := time.Now()
 
 	// Lazy sweep: evict stale buckets every window period or when map is full.
-	if now.Sub(l.lastSweep) >= l.window || len(l.buckets) >= maxBuckets {
+	// We add a check for minSweepInterval to prevent O(N) loops on every request
+	// when the map is full of active keys.
+	if now.Sub(l.lastSweep) >= l.window || (len(l.buckets) >= maxBuckets && now.Sub(l.lastSweep) > minSweepInterval) {
 		staleThreshold := now.Add(-2 * l.window)
 		for k, b := range l.buckets {
 			if b.lastFill.Before(staleThreshold) {
 				delete(l.buckets, k)
 			}
 		}
 		l.lastSweep = now
 	}
 
 	b, ok := l.buckets[key]
```

### Fix 2: Safer Retrier Defaults

```diff
--- call/retry.go
+++ call/retry.go
@@ -82,6 +82,9 @@
 // returns an error if the context is cancelled during the wait.
 func (r *Retrier) backoff(ctx context.Context, attempt int) error {
 	delay := r.BaseDelay
+	if delay <= 0 {
+		delay = 100 * time.Millisecond
+	}
 	for range attempt {
 		delay *= 2
 	}
```

### Fix 3: Strict Lifecycle Arguments

```diff
--- lifecycle/lifecycle.go
+++ lifecycle/lifecycle.go
@@ -8,6 +8,7 @@
 	"context"
 	"os/signal"
 	"syscall"
+	"fmt"
 
 	chassis "github.com/ai8future/chassis-go"
 	"golang.org/x/sync/errgroup"
@@ -34,6 +35,8 @@
 			components = append(components, v)
 		case func(ctx context.Context) error:
 			components = append(components, v)
+		default:
+			return fmt.Errorf("lifecycle: invalid component type %T (expected Component or func(ctx) error)", a)
 		}
 	}
 
```
