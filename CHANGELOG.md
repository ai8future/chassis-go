# Changelog

All notable changes to this project will be documented in this file.

## [5.0.1] - 2026-02-17

- Comprehensive README.md rewrite with full package documentation, usage examples, design principles, and observability reference (Claude Code:Opus 4.6)

## [5.0.0] - 2026-02-08

### Breaking Changes

- **Module path migrated to v5**: All import paths changed from `chassis-go/v4` to `chassis-go/v5`. All consumer code must update imports and call `chassis.RequireMajor(5)`.
- **OTLP defaults to TLS**: `otel.Init()` now uses TLS for OTLP gRPC connections by default. Set `Insecure: true` in `otel.Config` to use plaintext (dev/test environments).
- **Rate limiter requires MaxKeys**: `guard.RateLimitConfig` now requires a `MaxKeys int` field for LRU capacity. Rate limiter internals rewritten from O(n) sweep to O(1) LRU eviction using `container/list`.
- **Guard config validation panics**: `guard.RateLimit`, `guard.MaxBody`, and `guard.Timeout` now panic at construction on invalid config (zero rate, zero window, nil KeyFunc, zero MaxKeys, non-positive maxBytes, non-positive duration).
- **httpkit.JSONProblem delegates to errors.WriteProblem**: The `httpkit.JSONProblem` function now delegates to the consolidated `errors.WriteProblem` for RFC 9457 Problem Details rendering.
- **Health error wrapping preserves originals**: `health.All` now wraps check errors with `fmt.Errorf("%s: %w", name, err)`, preserving the original error chain for `errors.Is`/`errors.As`.
- **Metrics label hashing includes keys**: `metrics.CounterVec` and `metrics.HistogramVec` now hash `key=value` pairs (not just values), preventing collisions when different keys share the same values.

### New Features

- **`flagz` module**: Feature flags with pluggable sources (`FromEnv`, `FromMap`, `FromJSON`, `Multi`), boolean checks (`Enabled`), percentage rollouts with consistent FNV-1a hashing (`EnabledFor`), variant strings (`Variant`), and OTel span event integration.
- **`guard.CORS`**: Cross-Origin Resource Sharing middleware with preflight handling (204), origin matching, configurable methods/headers/max-age, and credentials validation.
- **`guard.SecurityHeaders`**: Security headers middleware (CSP, HSTS, X-Frame-Options, X-Content-Type-Options, Referrer-Policy, Permissions-Policy) with `DefaultSecurityHeaders` for secure defaults.
- **`guard.IPFilter`**: IP filtering middleware with CIDR-based allow/deny lists. Deny rules evaluated first (take precedence). Returns 403 Forbidden with RFC 9457 Problem Details.
- **`errors.ForbiddenError`**: New factory for 403 / PERMISSION_DENIED errors.
- **`errors.WriteProblem`**: Consolidated RFC 9457 Problem Details writer, used by `httpkit`, `guard`, and available for direct use. Accepts an optional `requestID` parameter.
- **Custom domain metrics**: `metrics.Recorder.Counter(name)` and `metrics.Recorder.Histogram(name, buckets)` for application-specific counters and histograms with cardinality protection.
- **`otel.Config.Insecure` field**: Explicit control over TLS vs plaintext for OTLP connections.

### Bug Fixes

- **Fix call retry panic on zero BaseDelay**: `backoff()` now defaults to 100ms when `BaseDelay <= 0`, preventing `rand.Int64N(0)` panic.
- **Fix httpkit generateID panic on crypto/rand failure**: Falls back to timestamp + atomic counter instead of panicking.

## [4.0.0] - 2026-02-08

### Breaking Changes

- **Module path migrated to v4**: The Go module path is now `github.com/ai8future/chassis-go/v4`. All import paths across all packages include the `/v4` suffix. This was done to unify the Go module version with the internal `VERSION` / `chassis.Version` which was already at `4.0.0`. (Claude:Opus 4.6)
- All consumer code must update imports from `github.com/ai8future/chassis-go/...` to `github.com/ai8future/chassis-go/v4/...`
- All consumer code must call `chassis.RequireMajor(4)`
- Tracer name constants updated to include `/v4` in their package paths

## [1.0.4] - 2026-02-03

- Fix chassis.Version constant drift (was stuck at 1.0.0), add float64 to INTEGRATING.md type list (Claude:Opus 4.5)

## [1.0.3] - 2026-02-03

- Add float64 support to config.MustLoad (Claude:Opus 4.5)

## [1.0.2] - 2026-02-03

- Document chassis.Version in INTEGRATING.md (Claude:Opus 4.5)

## [1.0.1] - 2026-02-03

- Add exported `chassis.Version` constant for integrator diagnostics (Claude:Opus 4.5)

## [1.0.0] - 2026-02-03

- Initial project setup with VERSION, CHANGELOG, AGENTS.md, and standard directories
- Existing codebase includes: call (retry/breaker), config, grpckit, health, httpkit, lifecycle, logz, testkit
