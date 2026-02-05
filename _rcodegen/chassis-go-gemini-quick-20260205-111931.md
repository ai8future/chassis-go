Date Created: 20260205-111931
TOTAL_SCORE: 78/100

# 1. AUDIT

### [High Severity] DoS Vulnerability in Rate Limiter
**File:** `guard/ratelimit.go`
**Issue:** The `buckets` map grows indefinitely if unique keys (e.g., spoofed IPs) are sent. Additionally, the lazy sweep iterates the entire map while holding a lock, allowing an attacker to trigger CPU spikes and lock contention (Stop-The-World), effectively causing a Denial of Service.
**Mitigation:** Enforce a hard limit on the number of tracked keys to prevent memory exhaustion.

```go
		b, ok := l.buckets[key]
	if !ok {
		// Prevent unbounded memory growth (DoS vector).
		if len(l.buckets) >= 100000 {
			return false
		}
		b = &bucket{tokens: float64(l.rate), lastFill: now}
		l.buckets[key] = b
	}
>>>>
```

# 2. TESTS

### Missing Test: Config Empty Slice Handling
**File:** `config/config_test.go`
**Description:** Verify that an empty environment variable results in an empty slice, not a slice containing a single empty string.

```go

func TestMustLoad_SliceStringSingleElement(t *testing.T) {
	t.Setenv("TEST_TAGS", "solo")

	type cfg struct {
		Tags []string `env:"TEST_TAGS"`
	}
	c := MustLoad[cfg]()
	if len(c.Tags) != 1 || c.Tags[0] != "solo" {
		t.Errorf("Tags = %v, want [solo]", c.Tags)
	}
}

func TestMustLoad_SliceStringEmpty(t *testing.T) {
	t.Setenv("TEST_EMPTY_TAGS", "")

	type cfg struct {
		Tags []string `env:"TEST_EMPTY_TAGS"`
	}
	c := MustLoad[cfg]()
	if len(c.Tags) != 0 {
		t.Errorf("Tags = %v, want [] (empty slice)", c.Tags)
	}
}
>>>>
```

### Missing Test: Logging Original Path
**File:** `httpkit/httpkit_test.go`
**Description:** Verify that the logging middleware captures the original request path, even if a downstream handler (like `StripPrefix`) modifies it.

```go
	output := buf.String()
	if !strings.Contains(output, "request_id") {
		t.Errorf("expected request_id in log output:\n%s", output)
	}
}

func TestLogging_CapturesOriginalPath(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Use StripPrefix to modify the path within the handler chain.
	handler := Logging(logger)(http.StripPrefix("/api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	output := buf.String()
	// The log should show the original path "/api/users", not "/users".
	if !strings.Contains(output, `"/api/users"`) {
		t.Errorf("log output missing original path \"/api/users\":\n%s", output)
	}
}
>>>>
```

# 3. FIXES

### Bug: Config Slice Parsing Error
**File:** `config/config.go`
**Description:** `strings.Split("", ",")` returns `[""]` (length 1), causing `MustLoad` to return `[]string{""}` instead of `[]string{}` when the env var is empty.
**Fix:** Explicitly handle empty input strings for slices.

```go
	// Handle []string specially.
	if fieldVal.Type() == reflect.TypeOf([]string{}) {
		if raw == "" {
			fieldVal.Set(reflect.ValueOf([]string(nil)))
			return nil
		}
		parts := strings.Split(raw, ",")
		rimmed := make([]string, 0, len(parts))
		for _, p := range parts {
			rimmed = append(trimmed, strings.TrimSpace(p))
		}
		fieldVal.Set(reflect.ValueOf(trimmed))
		return nil
	}
>>>>
```

### Bug: Logging Middleware Incorrectly Captures Modified Request
**File:** `httpkit/middleware.go`
**Description:** The logging middleware captures `r.Method` and `r.URL.Path` *after* calling `next.ServeHTTP`. If the handler modifies the request (common with `StripPrefix` or muxers), the log entry reflects the internal state rather than the incoming request.
**Fix:** Capture request attributes before delegation.

```go
			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			// Capture request details before downstream handlers potentially modify them.
			method := r.Method
			path := r.URL.Path

			next.ServeHTTP(rw, r)

			attrs := []slog.Attr{
				slog.String("method", method),
				slog.String("path", path),
				slog.Int("status", rw.statusCode),
				slog.Duration("duration", time.Since(start)),
			}
>>>>
```

# 4. REFACTOR

1.  **Async Rate Limit Cleanup:** The `guard` package's "lazy sweep" happens during request processing while holding a lock. For high-throughput services, this should be moved to a background goroutine (using `time.Ticker`) to decouple cleanup latency from request latency.
2.  **Expanded Config Support:** The `config` package currently lacks support for unsigned integers (`uint`, `uint64`) and other common types (`int32`, `float32`). It should be extended to support all basic Go types.
3.  **Configurable Logger Output:** `logz.New` hardcodes output to `os.Stderr`. It should accept an `io.Writer` or functional options to allow logging to files or other sinks.
4.  **Error Handling in Config:** `MustLoad` panics on error. While appropriate for initialization, exposing a `Load` function that returns an error would make the package more testable and reusable in non-fatal contexts.

```