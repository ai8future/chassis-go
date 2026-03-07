# Changelog

## [8.0.2] - 2026-03-07

### Bug Fixes

- **deploy**: Use `os.LookupEnv` instead of `os.Getenv` to correctly distinguish empty env vars from unset ones in `LoadEnv`
- **deploy**: Strip surrounding quotes (single/double) from `.env` file values for Docker/dotenv compatibility
- **deploy**: Add path confinement check in `RunHook` to prevent path traversal outside hooks directory
- **tick**: Implement `Jitter` option — previously accepted but silently ignored; now applies random delay before each tick
- **config**: Panic on malformed `min`/`max` values in validate tag instead of silently defaulting to 0
- **config**: Extend `fieldAsFloat` to handle all integer, unsigned integer, and float types for validation
- **secval**: Hoist `\s+` regex to package-level var in `SafeFilename` to avoid recompilation on every call

(Claude Code:Opus 4.6)

## [8.0.1] - 2026-03-07

### New Features

- **xyops**: Add xyops client module — curated API methods (RunEvent, GetJobStatus, CancelJob, SearchJobs, ListEvents, GetEvent, ListActiveAlerts, AckAlert, Ping, FireWebhook), Raw escape hatch, monitoring bridge with metric push via tick.Every, response caching via cache, webhook dispatch via webhook.Sender

### Tests

- **xyops**: Add TestPing, TestRunEvent, TestGetJobStatusWithCaching, TestFireWebhook, TestClientConstruction, TestMonitoringBridgeDisabled, TestMonitoringBridgeEnabled, TestListEvents, TestListActiveAlerts, TestRawEscapeHatch

(Claude Code:Opus 4.6)

## [7.0.0] - 2026-03-07

### Breaking Changes

- **Module path migrated to v7**: All import paths changed from `chassis-go/v6` to `chassis-go/v7`. All consumer code must update imports and call `chassis.RequireMajor(7)`.

### New Features

- **registry**: Add CLI/batch mode support via `InitCLI(chassisVersion)` for CLI tools and batch processes that need visibility without being long-running services
- **registry**: Add `Progress(done, total, failed)` for tracking batch progress with percentage calculation
- **registry**: Add `StopRequested()` for cooperative stop signaling in CLI mode
- **registry**: Add `ShutdownCLI(exitCode)` which rewrites the PID file with completion status instead of deleting it
- **registry**: Add `ProgressSummary` struct for progress tracking state
- **registry**: Add `parseFlags()` helper that parses CLI arguments into a `map[string]string` with sensitive flag redaction
- **registry**: Add `Mode`, `Flags`, `Status`, `ExitedAt`, `ExitCode`, and `Summary` fields to `Registration` struct
- **registry**: Service mode `Init()` now sets `mode: "service"` and `status: "running"` in the PID file
- **registry**: Stale cleanup now preserves completed/failed CLI PID files for 24 hours before removing them
- **registry**: Stop command in CLI mode sets `stopRequested` flag instead of calling cancelFn

### Tests

- **registry**: Add `TestInitCLI` — verify PID file has mode "cli" and parsed flags
- **registry**: Add `TestInitServiceMode` — verify service mode sets mode and status
- **registry**: Add `TestProgress` — verify progress events in log
- **registry**: Add `TestShutdownCLI` — verify PID file is rewritten (not deleted) with completion status
- **registry**: Add `TestShutdownCLIFailed` — verify failed exit code handling
- **registry**: Add `TestStopRequested` — verify stop command sets the flag in CLI mode
- **registry**: Add `TestParseFlags` — verify various flag formats (equals, space-separated, boolean, short, sensitive redaction)
- **registry**: Add `TestIsSensitiveFlag` — verify sensitive flag detection

### Documentation

- **INTEGRATING.md**: Add CLI/batch mode section with usage example
- **INTEGRATING.md, README.md, AGENTS.md**: Update all version references from v6 to v7

(Claude Code:Opus 4.6)

## [6.0.11] - 2026-03-07

### Breaking Changes

- **registry**: `Status()` and `Errorf()` now crash the process (via `os.Exit(1)`) if called before `Init()` / `lifecycle.Run()`. Previously they were silent no-ops.
- **registry**: New `AssertActive()` function crashes the process if the registry is not initialized. All post-lifecycle chassis modules (`httpkit`, `grpckit`, `call`, `work`, `health`) now call `AssertActive()` at runtime, enforcing that `lifecycle.Run()` must be called before any chassis service module is used.

### Improvements

- **httpkit**: `RequestID`, `Logging`, `Recovery`, and `Tracing` middleware handlers call `registry.AssertActive()` on first request
- **grpckit**: `UnaryLogging`, `UnaryRecovery`, `UnaryTracing`, `UnaryMetrics`, `StreamLogging`, `StreamRecovery`, `StreamTracing`, and `StreamMetrics` interceptors call `registry.AssertActive()` on each RPC
- **call**: `Client.Do()` calls `registry.AssertActive()` before executing requests
- **work**: `Map`, `All`, `Race`, and `Stream` call `registry.AssertActive()` alongside `chassis.AssertVersionChecked()`
- **health**: `Handler()`, `All()`, and `CheckFunc()` call `registry.AssertActive()` at construction time

### Tests

- **registry**: Replace `TestStatusNoOpBeforeInit` with `TestStatusCrashesBeforeInit`, `TestErrorfCrashesBeforeInit`, and `TestAssertActiveCrashesBeforeInit` using subprocess crash detection
- **call, grpckit, health, httpkit, work**: Add registry initialization to `TestMain` so tests pass with mandatory registry enforcement

(Claude Code:Opus 4.6)

## [6.0.10] - 2026-03-07
- Sync uncommitted changes

All notable changes to this project will be documented in this file.

## [6.0.9] - 2026-03-07

### Documentation

- **INTEGRATING.md**: Fix grpckit metric name (`rpc.server.duration`, not `grpc_server_duration_seconds`)
- **INTEGRATING.md**: Fix testkit.SetEnv description (uses `t.Setenv`, not `os.Setenv` + `t.Cleanup`)

(Claude Code:Opus 4.6)

## [6.0.8] - 2026-03-07

### Documentation

- **README.md**: Update version to 6.0.8, fix secval key list (only 3 prototype pollution keys), add missing `PayloadTooLargeError` to factory list, fix `guard.IPFilter` field names (`Allow`/`Deny` not `AllowCIDRs`/`DenyAction`), fix `guard.CORS` `MaxAge` type (`time.Duration` not int), add `chassis.Port()` and `registry.Port()` mentions

(Claude Code:Opus 4.6)

## [6.0.7] - 2026-03-07

### Documentation

- **INTEGRATING.md**: Fix `EnabledFor` example — restore missing `ctx` first argument to match actual function signature

(Claude Code:Opus 4.6)

## [6.0.6] - 2026-03-07

### Documentation

- **INTEGRATING.md**: Fix secval dangerous keys list to match implementation (only `__proto__`, `constructor`, `prototype` — business-domain words intentionally excluded)
- **INTEGRATING.md**: Fix lifecycle.Run docs — unsupported argument types panic at startup, not silently ignored

(Claude Code:Opus 4.6)

## [6.0.5] - 2026-03-07

### New Features

- **chassis**: Add `Port(name, offset...)` — deterministic port assignment using djb2 hash, mapping service names to stable ports in range 5000–48000
- **chassis**: Add `PortHTTP` (0), `PortGRPC` (1), `PortMetrics` (2) standard role offset constants
- **registry**: Add `Port(role, port, label, ...opts)` for declaring service ports in PID registration JSON
- **registry**: Add `Proto(string)` option to override default wire protocol per port declaration
- **registry**: Add `BasePort` and `Ports` fields to `Registration` struct; `base_port` and `ports` now appear in PID JSON for viewer/operational tooling

(Claude Code:Opus 4.6)

## [6.0.4] - 2026-03-07

### Bug Fixes

- **call**: Fix data race in retry body rewind — move `GetBody` call from shared `Retrier.req` field into per-call closure with local attempt counter, preventing races under concurrent `Client.Do` use
- **logz**: Add regression test for `WithGroup` + `WithAttrs` + trace context interaction

(Claude Code:Opus 4.6)

## [6.0.3] - 2026-03-07

### Bug Fixes

- **call**: Rewind request body via `GetBody` before each retry attempt; previously POST/PUT retries silently sent empty bodies
- **logz**: Fix `traceHandler` dropping `WithAttrs` attributes when groups are active and trace context is present; attrs added after `WithGroup` are now included in reconstructed records
- **guard**: `timeoutWriter.Write` now returns `http.ErrHandlerTimeout` after the deadline fires, preventing unbounded buffer growth from slow handler goroutines
- **work**: `Map` and `All` now use `select` with `ctx.Done()` when acquiring the semaphore, so cancelled contexts are respected immediately instead of blocking

### Security Fixes

- **registry**: Redact sensitive command-line arguments (passwords, tokens, keys) from PID file `args` field to prevent credential leakage
- **registry**: Validate service directory permissions on Init; reject directories with group/world-readable permissions (must be 0700)
- **secval**: Reduce dangerous key list to prototype-pollution vectors only (`__proto__`, `constructor`, `prototype`); remove common business-domain words that caused false positives

### Improvements

- **lifecycle**: Add `RunComponents()` type-safe variant of `Run()` that accepts `...Component` for compile-time type checking
- **call**: Add `RemoveBreaker(name)` to allow cleanup of named circuit breakers, preventing memory leaks with dynamic breaker names
- **httpkit**: `errorForStatus` now preserves the caller's original HTTP status code for unmapped values instead of silently replacing with 500
- **config**: Remove redundant hand-rolled `contains`/`searchString` test helpers; use `strings.Contains`
- **registry**: Document that exported config variables (`BasePath`, `HeartbeatInterval`, `CmdPollInterval`) must be set before `lifecycle.Run` and are `time.Duration` values
- **testkit**: Document TOCTOU race inherent in `GetFreePort`

(Claude Code:Opus 4.6)

## [6.0.2] - 2026-03-07

### Security Fixes

- **registry**: Restrict directory permissions from 0755 to 0700 and file permissions from 0644 to 0600, preventing local users from enumerating/controlling services
- **registry**: Replace predictable `.tmp` path in atomicWrite with `os.CreateTemp` to prevent symlink attacks
- **call**: Remove full URL (including query params) from OTel span attributes; log only `url.path` to prevent leaking secrets
- **httpkit**: Use HTTP method only as OTel span name instead of `method + path` to prevent high-cardinality span explosion

### Bug Fixes

- **httpkit**: Add `Write()` override to `responseWriter` so `headerWritten` is tracked correctly; fixes garbled output when panic occurs after partial response write
- **registry**: Change `active` from plain `bool` to `atomic.Bool` to eliminate data race in `Status()` and `Errorf()`
- **registry**: Read `cancelFn` under mutex lock in `pollOnce()` to eliminate data race on stop/restart commands
- **registry**: Clean up orphaned `.log.jsonl` and `.cmd.json` files for dead processes in `cleanStale()`
- **lifecycle**: Handle `syscall.Exec` error on restart; previously the error was silently discarded
- **lifecycle**: Fix unreliable signal detection by removing redundant `signal.Notify` registration; use `signalCtx.Err()` check instead, which is deterministic
- **guard**: Add `Unwrap()` to `timeoutWriter` so `http.NewResponseController` can access `http.Flusher`/`http.Hijacker` through timeout middleware

### Improvements

- **testkit**: Delegate `SetEnv` to `t.Setenv` for automatic parallel-test safety and cleanup
- **work**: Use `select` with `ctx.Done()` when sending `Stream` results to prevent goroutine leaks if consumer stops reading
- **call**: Remove redundant `http.Client.Timeout` assignment; context-based timeout in `Do()` is sufficient

(Claude Code:Opus 4.6)

## [6.0.1] - 2026-03-07

- Update README.md and INTEGRATING.md for v6: change all v5 references to v6, update RequireMajor(5) to RequireMajor(6), add `registry` module to package tables and import lists, add registry documentation sections covering Status/Errorf/Handle API, file layout, built-in commands, and automatic lifecycle integration. (Claude Code:Opus 4.6)

## [6.0.0] - 2026-03-07

### Breaking Changes

- **Module path migrated to v6**: All import paths changed from `chassis-go/v5` to `chassis-go/v6`
- **`lifecycle.Run()` now auto-initializes registry**: Every service is automatically registered at `/tmp/chassis/` on startup. This is mandatory and cannot be disabled.

### New Features

- **`registry` module**: File-based service self-registration with heartbeat, status logging, error reporting, and bidirectional command system
  - `registry.Status(msg)`: Write progress/status updates
  - `registry.Errorf(fmt, args...)`: Write error events
  - `registry.Handle(name, desc, fn)`: Register custom commands
  - Built-in `stop` and `restart` commands
  - Automatic heartbeat every 30s, command polling every 3s
  - Stale PID cleanup on startup
  - Atomic file writes for crash safety

(Claude Code:Opus 4.6)

## [5.0.3] - 2026-03-07

- Integrate `registry` module into `lifecycle.Run()`: auto-initializes registry on startup, runs heartbeat and command-poll goroutines, determines shutdown reason (clean/error/signal), calls `registry.Shutdown()`, and supports `syscall.Exec` restart on restart command. Added integration tests verifying PID file creation during Run and cleanup after shutdown. (Claude Code:Opus 4.6)

## [5.0.2] - 2026-03-07

- New `registry` package: file-based service registration at `/tmp/chassis/` with PID tracking, JSONL logging, heartbeat, command polling (stop/restart/custom), and stale PID cleanup. Zero chassis dependencies — stdlib only. (Claude Code:Opus 4.6)

## [5.0.1] - 2026-02-17

- Comprehensive README.md rewrite with full package documentation, usage examples, design principles, and observability reference (Claude Code:Opus 4.6)

## [5.0.0] - 2026-02-08

### Breaking Changes

- **Module path migrated to v5**: All import paths changed from `chassis-go/v4` to `chassis-go/v6`. All consumer code must update imports and call `chassis.RequireMajor(5)`.
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
