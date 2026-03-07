# Audit Round 2: 13 Findings Fixed (2026-03-07)

## Bugs
- **call/retry**: POST/PUT retries sent empty body because `GetBody` was never called to rewind. Fixed by calling `req.GetBody()` before each retry.
- **logz/traceHandler**: Attrs added via `WithAttrs` after `WithGroup` were dropped when trace context was present. Handle's grouped path bypassed `inner` handler and used `base` which didn't have the attrs. Fixed by tracking per-group attrs in `groupAttrs` slice.
- **guard/timeoutWriter**: `Write()` continued appending to buffer after timeout fired (only checked `started`, not `written`). Fixed by returning `http.ErrHandlerTimeout` when `written` is true.
- **work/Map+All**: Semaphore acquire blocked without checking `ctx.Done()`. Fixed with `select` on both channels.

## Security
- **registry**: `os.Args` written to PID file could leak secrets. Fixed with `redactArgs` that masks `--password=X` style flags.
- **registry**: Added permission check rejecting service dirs with group/world bits set.
- **secval**: Reduced dangerous key list from 15 to 3 (prototype pollution only). Common words like "command", "import" etc. were causing false positives.

## Design
- Added `lifecycle.RunComponents()` type-safe variant.
- Added `call.RemoveBreaker()` for breaker registry cleanup.
- `httpkit.errorForStatus` now preserves unmapped status codes.
- Replaced hand-rolled `contains` in config_test.go with `strings.Contains`.
- Documented registry config vars and `GetFreePort` TOCTOU.
