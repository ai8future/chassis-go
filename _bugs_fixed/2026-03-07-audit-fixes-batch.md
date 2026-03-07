# 2026-03-07 Audit Fixes Batch

## B1: responseWriter garbled output on panic-after-write
`httpkit/middleware.go` - `responseWriter` did not override `Write()`, so `headerWritten` was never set when handlers called `Write()` directly. If a panic occurred after a partial write, Recovery middleware would try to write a 500 response on top of already-sent headers, producing garbled output. Fixed by adding `Write()` override.

## B2: Data race on `active` in registry
`registry/registry.go` - `active` was a plain `bool` written under lock but read without lock in `Status()` and `Errorf()`. Changed to `atomic.Bool`.

## B3: Data race on `cancelFn` in registry pollOnce
`registry/registry.go` - `cancelFn` was read without lock in the stop/restart command handlers. Added mutex lock around reads.

## B4: syscall.Exec error silently ignored
`lifecycle/lifecycle.go` - `syscall.Exec` return value was discarded on restart failure. Now returns a wrapped error.

## B5: Unreliable signal detection
`lifecycle/lifecycle.go` - Double signal registration (`signal.NotifyContext` + `signal.Notify`) caused a race where Go's `select` could pick either channel randomly. Removed the redundant registration; now uses `signalCtx.Err()` check which is deterministic.

## B6: Orphaned log files never cleaned
`registry/registry.go` - `cleanStale` only removed `.json` PID files for dead processes, leaving `.log.jsonl` and `.cmd.json` files to accumulate. Now removes all three.

## B7: timeoutWriter missing Unwrap
`guard/timeout.go` - Unlike `responseWriter`, `timeoutWriter` didn't implement `Unwrap()`, breaking `http.NewResponseController` access to `http.Flusher`/`http.Hijacker`.

## S1: World-accessible registry in /tmp
`registry/registry.go` - Directory was 0755 and files were 0644, allowing any local user to enumerate services and inject commands. Changed to 0700/0600. Also fixed atomicWrite to use `os.CreateTemp` instead of predictable `.tmp` path (symlink attack vector).

## S2: Full URL leaked in span attributes
`call/call.go` - `http.url` attribute included query parameters which may contain API keys or PII. Changed to `url.path` only.

## S3: High-cardinality span names
`httpkit/tracing.go` - Span name included raw URL path, causing unbounded cardinality. Changed to HTTP method only.
