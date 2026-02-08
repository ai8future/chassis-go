# Fix Plan: chassis-go rcodegen Reports

## Context

Nine rcodegen reports exist (3 fix, 3 audit, 3 quick — from Gemini, Codex, Claude, all dated 2026-02-08). This plan cross-references all reports, deduplicates findings, assesses each as fix advice, and determines which to act on.

**Already fixed in prior refactoring session:** lifecycle.Run unknown args (now panics), FromError uses errors.As (known issue but NOT yet fixed — still uses type assertion), LazyHistogram extraction.

**Note:** lifecycle.Run was already fixed to panic on unknown args. The `FromError` issue is documented in MEMORY.md but has NOT been fixed yet — it still uses type assertion.

---

## Cross-Report Deduplication & Assessment

### TIER 1: Fix (High Confidence, Clear Bugs)

| ID | Issue | Reports | Verdict | Reasoning |
|----|-------|---------|---------|-----------|
| F1 | `call/retry.go:94` — `rand.Int64N(0)` panic with tiny BaseDelay | Codex-fix, Gemini-quick, Codex-quick | **AGREE** | Trivially reproducible: `BaseDelay=1ns` causes `delay/2==0`, panicking `rand.Int64N(0)`. 3-line guard. |
| F2 | `errors/errors.go:124` — `FromError` uses type assertion not `errors.As` | Claude-fix, Claude-quick, Claude-audit | **AGREE** | Wrapped ServiceErrors silently become 500s. Known issue in MEMORY.md. The `errors` package shadows stdlib `errors`, so need alias import `stderrors`. |
| F3 | `otel/otel.go:78-80,102-110` — Silent OTel exporter failures | Claude-fix, Claude-quick, Claude-audit | **AGREE** | Zero logging when trace/metric exporters fail. Add `slog.Error`/`slog.Warn`. |
| F4 | `cmd/demo-shutdown/main.go` — Missing `RequireMajor(5)` | Codex-fix, Codex-audit, Claude-audit | **AGREE** | Crashes immediately. Trivial fix. |
| F5 | `otel/otel_test.go` — Tests fail without OTLP collector | Codex-fix, Codex-audit, Claude-fix, Claude-audit | **AGREE** | Two tests `t.Fatalf` on shutdown error when no collector. Use short timeout + tolerate connection errors. |
| F6 | `metrics/metrics.go:106-132` — TOCTOU race in checkCardinality | Claude-fix, Claude-quick, Claude-audit | **AGREE** | Between RUnlock and Lock, another goroutine can add the same combo. Add re-check under write lock + move overflow warning under lock. |
| F7 | `guard/keyfunc.go:34-38` — `XForwardedFor` silently ignores invalid CIDRs | Claude-audit | **AGREE** | Inconsistent with `parseCIDRs` which panics. Should panic on invalid CIDR (fail-fast). |
| F8 | `guard/cors.go:95` — Missing `Vary` headers on preflight | Codex-audit | **AGREE** | Preflight responses need `Vary: Access-Control-Request-Method, Access-Control-Request-Headers` for cache correctness. |
| F9 | `flagz/flagz.go:88-89` — Hash collision without separator | Gemini-audit | **AGREE** | `name="ab" + userID="c"` and `name="a" + userID="bc"` both hash "abc". Add null-byte separator. |
| F10 | `health/health.go:57-60` — Non-deterministic result order | Claude-fix, Codex-fix, Codex-audit | **AGREE** | Map iteration randomizes order. Sort by name before scheduling. |
| F11 | `guard/ipfilter.go` — Add KeyFunc for proxy-aware IP extraction | Gemini-quick, Claude-quick, Claude-audit, Gemini-audit | **AGREE** | Behind LBs, RemoteAddr is the proxy IP. Add optional `KeyFunc` field (defaults to `RemoteAddr()`). |
| F12 | `examples/04-full-service/main.go:1` — Comment says "4.0" but code is v5 | Claude-audit | **AGREE** | Trivial doc fix. |
| F13 | `otel/otel.go:60-68` — Resource used after failed creation | Claude-fix | **AGREE** | When `resource.New` fails, `res` may be nil. Fall back to `resource.Default()`. |
| F14 | `guard/ratelimit.go:116-124` — Missing Retry-After header on 429 | Claude-audit | **AGREE** | RFC 6585 recommends it. One-line fix. |

### TIER 2: Disagree / Not Acting On

| ID | Issue | Reports | Verdict | Reasoning |
|----|-------|---------|---------|-----------|
| D1 | `guard/timeout.go` goroutine leak docs | Claude-fix, Claude-audit, Claude-quick | **DISAGREE** | Already documented in existing godoc: "the deadline fires." The goroutine leak is inherent to the pattern and is the same limitation as stdlib `http.TimeoutHandler`. Adding more docs is just noise. |
| D2 | `guard/timeout.go` — Codex timeout `started` vs `written` semantics | Codex-audit | **DISAGREE** | The existing logic is correct: `timeout()` checks `tw.written || tw.started` — if the handler has called WriteHeader/Write, we don't override. The Codex patch changes semantics in ways that would discard legitimate buffered writes. |
| D3 | `guard/timeout.go:37` — Panic in spawned goroutine (Codex-quick) | Codex-quick | **DISAGREE** | Recovery middleware (`httpkit.Recovery` or `guard.Timeout`) should be composed; the timeout goroutine catching panics would mask bugs. The caller should use Recovery middleware. |
| D4 | `call/retry.go` — Unbounded body drain (Gemini-audit) | Gemini-audit | **DISAGREE** | `io.LimitReader(resp.Body, 4096)` is over-engineering. HTTP/1.1 error responses are virtually always small. The real fix for malicious servers is the client timeout, not limiting drain bytes. |
| D5 | `call/retry.go` — No backoff cap (Claude-audit CQ-03) | Claude-audit | **DISAGREE** | Context deadline already caps effective sleep. Adding `maxBackoff` constant is reasonable but adds complexity for a toolkit. Users configure their own timeout. |
| D6 | `call` retries reuse consumed request bodies (Codex-quick A2) | Codex-quick | **DISAGREE** | The existing `WithRetry` godoc explicitly documents this: "the body must be rewindable (implement GetBody) or the retry will send an empty/consumed body." This is by design. Adding `requestForAttempt()` + `nonRetryableError` adds significant complexity for a documented behavior. |
| D7 | `metrics/metrics.go` — Odd label pair count (Claude-fix, Codex-quick) | Multiple | **DISAGREE** | Silently dropping the trailing key is the standard Go convention (same as `slog.With("key")`, OTel `attribute.String` calls). Adding runtime validation is over-engineering for an internal helper. |
| D8 | `testkit.SetEnv` uses os.Setenv (Claude-audit SEC-01) | Claude-audit | **DISAGREE** | `testkit.SetEnv` is used with `testing.TB` (interface that includes both `*testing.T` and `*testing.B`). The `t.Setenv` method only exists on `*testing.T`. The current impl with `os.Setenv` + cleanup is the correct approach for the generic `testing.TB` interface. Config tests should not use `t.Parallel()` anyway since they modify process-global env. |
| D9 | `errors.FromError` leaks internal error messages (Claude-audit SEC-02) | Claude-audit | **DISAGREE** | Replacing `err.Error()` with generic "internal server error" loses debugging information. The caller controls what gets shown to clients via `WriteProblem`. The error message in `ServiceError` is for logging/debugging, not direct client exposure. |
| D10 | `work.Stream` blocks on cancelled consumers (Codex-quick F2) | Codex-quick | **DISAGREE** | Adding `select { case out <- ...: case <-ctx.Done(): }` silently drops results. The current pattern is correct: the goroutine in `Stream` calls `wg.Wait()` then `close(out)`, which unblocks any consumer. If a consumer stops reading, the goroutine is already bounded by the semaphore. |
| D11 | `guard/ratelimit.go` LRU eviction `for` vs `if` (Claude-quick FIX-1) | Claude-quick | **DISAGREE** | Defensive `for` loop is correct — even if currently only one excess entry is possible, using `for` is safer if the logic ever changes. No functional difference. |
| D12 | `otel.Init()` should return error (Claude-quick REFACTOR-7) | Claude-quick | **DISAGREE (wrong report type)** | This is an API design change, not a fix. |
| D13 | Go toolchain version upgrade to 1.25.7 (Codex-audit) | Codex-audit | **DISAGREE** | This is an operational concern outside the scope of code fixes. Go version upgrades should be deliberate, not driven by an audit report with potentially hallucinated CVEs. |
| D14 | `metrics/metrics.go` — Swallowed meter/counter errors (Claude-fix Issue 4) | Claude-fix | **DISAGREE** | OTel's API guarantees that failed instrument creation returns a no-op instrument, not nil. Logging errors here adds noise for a non-actionable case. |
| D15 | `guard/maxbody.go` — ContentLength bypass via chunked (Claude-quick AUDIT-4) | Claude-quick | **DISAGREE** | The early `ContentLength` check is an optimization, not a security boundary. `http.MaxBytesReader` on line 24 is the actual enforcement and handles chunked encoding correctly. Removing the early check loses a useful fast-path rejection. |
| D16 | `guard/timeout.go:114-116` — Ignored Write error in flush (Claude-audit CQ-05) | Claude-audit | **DISAGREE** | Adding `_, _ =` is cosmetic noise. Go's `http.ResponseWriter.Write` error is conventionally ignored since there's nothing actionable (client disconnected). |
| D17 | `secval` dangerous key list too broad (Claude-audit SEC-05) | Claude-audit | **DISAGREE** | This is a design/scope discussion, not a fix. The blocklist is intentionally broad for prototype-pollution protection. |
| D18 | `health/handler.go` — JSON encoding error handling (Claude-quick AUDIT-7) | Claude-quick | **DISAGREE** | Buffering to `bytes.Buffer` before writing adds allocation for an error that virtually never happens. The current pattern (log + continue) is standard Go HTTP handler practice. |
| D19 | `guard/ratelimit.go` — Lock contention (Gemini-audit) | Gemini-audit | **DISAGREE** | The report itself says "for a chassis library, this is acceptable for typical loads." Sharded maps add significant complexity. |
| D20 | `call/retry.go` body drain on network error (Claude-fix Issue 11) | Claude-fix | **DISAGREE** | On network errors, the connection is typically broken anyway. Draining a broken connection is pointless. The existing `resp.Body.Close()` is sufficient. |
| D21 | `metrics RecordRequest` ms vs sec naming (Claude-quick FIX-4) | Claude-quick | **DISAGREE** | This is a breaking API change. The callers know the contract. |

---

## Execution Order (14 items)

### Phase 1: Critical Correctness (3 items)
1. **F1** — Retry panic guard (`call/retry.go`)
2. **F2** — `FromError` use `errors.As` (`errors/errors.go`)
3. **F6** — checkCardinality TOCTOU race (`metrics/metrics.go`)

### Phase 2: OTel Reliability (3 items)
4. **F3** — Log OTel exporter failures (`otel/otel.go`)
5. **F13** — Fallback to `resource.Default()` (`otel/otel.go`)
6. **F5** — Fix OTel tests (`otel/otel_test.go`)

### Phase 3: Guard & Security (4 items)
7. **F7** — `XForwardedFor` panic on invalid CIDRs (`guard/keyfunc.go`)
8. **F8** — CORS preflight Vary headers (`guard/cors.go`)
9. **F11** — IPFilter KeyFunc support (`guard/ipfilter.go`)
10. **F14** — Retry-After header on 429 (`guard/ratelimit.go`)

### Phase 4: Small Fixes (4 items)
11. **F4** — demo-shutdown RequireMajor (`cmd/demo-shutdown/main.go`)
12. **F9** — flagz hash separator (`flagz/flagz.go`)
13. **F10** — health.All deterministic order (`health/health.go`)
14. **F12** — Example comment v4→v5 (`examples/04-full-service/main.go`)

## Verification

After all changes:
```bash
go build ./...
go test ./...
go vet ./...
```
