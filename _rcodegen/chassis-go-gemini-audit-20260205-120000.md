Date Created: Thursday, February 5, 2026 12:00:00 PM
TOTAL_SCORE: 92/100

# Chassis-Go Audit Report

## Executive Summary

The `chassis-go` library demonstrates a high standard of Go engineering, utilizing modern language features (Go 1.25+) and adhering to strong architectural principles. The code is modular, well-documented, and effectively leverages OpenTelemetry for observability.

However, a **critical security vulnerability** was identified in the `guard` package: the rate limiter implementation suffers from unbounded memory growth, which exposes services to Denial of Service (DoS) attacks via memory exhaustion.

## Score Breakdown

| Category | Score | Notes |
| :--- | :--- | :--- |
| **Architecture** | 25/25 | Clean modularity, dependency injection patterns, effective use of options pattern. |
| **Security** | 20/25 | **CRITICAL**: Rate limiter memory leak. `secval` is good but specific to JS targets. |
| **Reliability** | 23/25 | robust lifecycle management, standard error handling. |
| **Observability** | 24/25 | Excellent integration of OTel tracing and metrics throughout. |
| **Total** | **92/100** | |

## Detailed Findings

### 1. [CRITICAL] Unbounded Memory Growth in Rate Limiter (`guard/ratelimit.go`)

**Severity**: High
**Description**: The `limiter` struct uses a `map[string]*bucket` to track token buckets for every unique key (e.g., IP address). There is no mechanism to remove old or unused buckets.
**Impact**: An attacker can exhaust server memory by sending requests with randomized keys (e.g., spoofing source IPs or changing the header used for identification).
**Recommendation**: Implement a cleanup mechanism to remove buckets that have not been accessed within a certain timeframe (TTL).

### 2. [INFO] Goroutine Management in `work` Package (`work/work.go`)

**Severity**: Low
**Description**: In `work.Race`, goroutines are spawned for every task. While the context is cancelled when a winner is found, the loser goroutines continue to run until they check the context.
**Recommendation**: Ensure documentation explicitly states that tasks provided to `Race` MUST respect `ctx.Done()` to avoid "leaking" execution time.

## Patch-Ready Diffs

### Fix: Rate Limiter Memory Leak

This patch introduces a `lastSeen` timestamp to the bucket and runs a background goroutine to clean up stale buckets every minute.

```diff
--- guard/ratelimit.go
+++ guard/ratelimit.go
@@ -19,6 +19,7 @@
 type bucket struct {
 	tokens   float64
 	lastFill time.Time
+	lastSeen time.Time
 }
 
 type limiter struct {
@@ -32,15 +33,38 @@
 	return &limiter{
 		buckets: make(map[string]*bucket),
 		rate:    rate,
 		window:  window,
 	}
 }
 
+// startCleanupLoop starts a background goroutine to remove stale buckets.
+// It runs once per window or every minute, whichever is larger, to avoid lock contention.
+func (l *limiter) startCleanupLoop() {
+	interval := l.window
+	if interval < time.Minute {
+		interval = time.Minute
+	}
+	ticker := time.NewTicker(interval)
+	go func() {
+		for range ticker.C {
+			l.cleanup(interval * 2)
+		}
+	}()
+}
+
+func (l *limiter) cleanup(ttl time.Duration) {
+	l.mu.Lock()
+	defer l.mu.Unlock()
+	deadline := time.Now().Add(-ttl)
+	for k, b := range l.buckets {
+		if b.lastSeen.Before(deadline) {
+			delete(l.buckets, k)
+		}
+	}
+}
+
 func (l *limiter) allow(key string) bool {
 	l.mu.Lock()
 	defer l.mu.Unlock()
 	now := time.Now()
 	b, ok := l.buckets[key]
 	if !ok {
-		b = &bucket{tokens: float64(l.rate), lastFill: now}
+		b = &bucket{tokens: float64(l.rate), lastFill: now, lastSeen: now}
 		l.buckets[key] = b
 	}
+	b.lastSeen = now
 	elapsed := now.Sub(b.lastFill)
 	refill := elapsed.Seconds() / l.window.Seconds() * float64(l.rate)
 	b.tokens += refill
@@ -73,6 +97,7 @@
 func RateLimit(cfg RateLimitConfig) func(http.Handler) http.Handler {
 	chassis.AssertVersionChecked()
 	lim := newLimiter(cfg.Rate, cfg.Window)
+	lim.startCleanupLoop()
 	return func(next http.Handler) http.Handler {
 		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
 			key := cfg.KeyFunc(r)
```
