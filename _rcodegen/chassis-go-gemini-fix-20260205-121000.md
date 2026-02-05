Date Created: 2026-02-05 12:10:00
TOTAL_SCORE: 85/100

# Chassis-Go Codebase Analysis

## Executive Summary

The `chassis-go` library provides a solid foundation for building Go microservices, featuring modular components for lifecycle management, configuration, logging, metrics, and tracing. The code is generally clean, idiomatic, and follows modern Go practices (e.g., `slog`, generics).

However, a few critical issues were identified that could impact reliability and observability in production environments. Specifically, the error handling mechanism fails to unwrap errors correctly, potentially masking service errors as internal server errors. Additionally, the retry mechanism has a boundary condition bug, and the rate limiter exhibits a potential performance bottleneck at scale.

## Issue Details

### 1. Error Unwrapping Failure (Critical)
**File:** `errors/errors.go`

The `FromError` function uses a direct type assertion (`err.(*ServiceError)`) to check if an error is a `ServiceError`. If a `ServiceError` is wrapped (e.g., using `fmt.Errorf("context: %w", err)`), this assertion fails. Consequently, the error is treated as a generic `InternalError`, causing the loss of specific HTTP/gRPC status codes and structured details.

**Remediation:** Use `errors.As` (from the standard library) to correctly unwrap and identify `ServiceError` instances within the error chain.

### 2. Retrier Zero-Configuration Bug (Major)
**File:** `call/retry.go`

The `Retrier.Do` method iterates using `range r.MaxAttempts`. If `MaxAttempts` is not initialized (defaults to 0) or explicitly set to <= 0, the loop body is never executed. The method returns `nil, nil` immediately, leading to a confusing state where no request is made, but no error is reported.

**Remediation:** Add a guard clause to ensure `MaxAttempts` is at least 1, returning a configuration error otherwise.

### 3. Rate Limiter Linear Sweep (Performance)
**File:** `guard/ratelimit.go`

The token bucket implementation uses a "lazy sweep" to evict stale buckets. This sweep iterates over the entire `buckets` map (up to 100,000 entries) while holding the main mutex. When the map is full or the window expires, a single request will trigger this O(N) cleanup, blocking all other incoming requests and causing a significant latency spike ("stop-the-world").

**Remediation:** (Observation only) Consider moving cleanup to a background goroutine or using a sharded map/lock structure.

### 4. Slog Group Allocation (Performance)
**File:** `logz/logz.go`

The `traceHandler` reconstructs `slog.Record` objects recursively when groups are active to hoist `trace_id` to the top level. This involves allocating new slices and copying attributes for every log message within a group, which is CPU and memory intensive.

**Remediation:** (Observation only) While this ensures correct JSON structure, verify the performance impact if high-volume logging with groups is expected.

## Patch-Ready Diffs

### Fix: Use `errors.As` in `FromError`

```diff
--- errors/errors.go
+++ errors/errors.go
@@ -3,6 +3,7 @@
 
 import (
+	std_errors "errors"
 	"fmt"
 	"net/http"
 
@@ -107,8 +108,9 @@
 	if err == nil {
 		return nil
 	}
-	if se, ok := err.(*ServiceError); ok {
-		return se
+	var se *ServiceError
+	if std_errors.As(err, &se) {
+		return se
 	}
 	return InternalError(err.Error()).WithCause(err)
 }
```

### Fix: Validate `MaxAttempts` in `Retrier`

```diff
--- call/retry.go
+++ call/retry.go
@@ -4,6 +4,7 @@
 import (
 	"context"
+	"errors"
 	"io"
 	"math/rand/v2"
 	"net/http"
@@ -24,6 +25,10 @@
 		err  error
 	)
 
+	if r.MaxAttempts < 1 {
+		return nil, errors.New("call: Retrier.MaxAttempts must be >= 1")
+	}
+
 	for attempt := range r.MaxAttempts {
 		// Check context before each attempt.
 		if ctx.Err() != nil {
```
