# `phasekit` Build Plan

**Status:** Revised after audit; ready to implement
**Author:** Original: Claude Code (Opus 4.7); audit/revision: Codex (GPT-5)
**Date:** 2026-04-26
**Target package:** `github.com/ai8future/chassis-go/v11/phasekit`
**Estimated effort:** ~8-10 hours

---

## 1. Goal

Add a `phasekit` package to chassis-go that hydrates environment variables from a Phase secrets server before `config.MustLoad` runs. This enables consumer repos to manage secrets centrally (versioned, audited, role-based access controlled, rotatable) instead of via `.env` files while keeping chassis itself free of additional Go module dependencies.

## 2. Non-goals (v1)

- No native Software Development Kit (SDK) integration in v1. The current Phase Go SDK v2 appears Go-native rather than `libsodium`/CGO-bound, but using it would still add a third-party dependency and a broader Phase API surface to chassis. Shelling out to the Phase Command-Line Interface (CLI) is the smallest integration that preserves chassis' dependency policy. Revisit the SDK only if the CLI cannot satisfy a real consumer requirement.
- No background secret rotation or refresh. Hydration is startup-only.
- No dynamic secret lease generation in v1. Phase CLI `secrets export` defaults `--generate-leases=true`; phasekit must pass `--generate-leases=false` so startup hydration cannot silently inject short-lived credentials with no renewal/revoke story.
- No feature flag primitives. Phase has no flag evaluation engine — that's a separate concern handled by a future `flagkit` (OpenFeature provider, GrowthBook, Unleash, or similar).
- No write operations to Phase. Read-only at startup.
- No use of Phase as the source of truth for typed config. Typed config remains driven by `config.MustLoad` and struct tags. Phase only injects environment variables that `config.MustLoad` then reads.

## 3. Architecture decisions (locked, with verified evidence)

| # | Decision | Rationale | Evidence |
|---|---|---|---|
| 1 | Shell out to `phase secrets export --format=json` | Keeps chassis free of new Go dependencies. JavaScript Object Notation (JSON) gives proper escaping for any value content. Runtime images need the `phase` binary to hydrate from Phase; if absent, phasekit falls back to existing env. | Live-verified against `phase.z.secure`. JSON round-trips multi-line / quoted / backslash-containing values correctly via stdlib `json.Unmarshal`. Dotenv format proved broken (see §4.3). |
| 2 | Hydrate via `os.Setenv` before `config.MustLoad` | Minimal coupling, mirrors 12-factor patterns for Vault/Doppler/etc. No API change to existing `config` package. | — |
| 3 | Preserve existing env vars by default with `OverwriteExisting bool` | Go's zero value should be safe. `OverwriteExisting=false` means local `.env`, orchestrator secrets, and shell exports win over Phase. | Fixes the original `SkipExisting` contradiction where docs claimed default true but a plain bool defaults false. |
| 4 | `MustHydrate` panics, `Hydrate` returns error | Mirrors existing `config.MustLoad` / `chassis.RequireMajor` ergonomics. | Convention match with `posthogkit`, `kafkakit`. |
| 5 | Bootstrap config via struct literal | Phasekit must run *before* `MustLoad`, so its own config can't be loaded by `MustLoad`. Caller reads `PHASE_SERVICE_TOKEN` and `PHASE_HOST` from `os.Getenv` themselves. | — |
| 6 | `BinaryPath` overridable | Required for tests; useful for unusual deployments. Defaults to `exec.LookPath("phase")`. | — |
| 7 | Path is exact-match, not recursive; all-path export is explicit | `--path /db` returns only secrets at exactly `/db`. Empty string has Phase-specific meaning, so expose `AllPaths bool` instead of overloading `Path: ""`. | Live-verified: `/` returned 9 root keys, `/phasekit_test` returned only its 1 nested key, `''` returned all 10. |
| 8 | Treat `[REDACTED]` values as a hydration error by default | Phase docs state AI mode can redact `secrets export` output. Hydrating literal `[REDACTED]` would be worse than failing fast. | Add `AllowRedacted bool` for explicit opt-out in tests or unusual deployments. |
| 9 | Do not inherit the full parent environment into the Phase subprocess | Avoids leaking `CODEX`, `CLAUDECODE`, `AGENT`, and unrelated application env vars into the CLI. Pass only `PHASE_SERVICE_TOKEN`, `PHASE_HOST`, and a small network/TLS proxy allowlist. | Reduces AI-redaction and env-leak surprises while preserving service-token auth. |
| 10 | Disable dynamic leases explicitly | `secrets export` can auto-generate leases by default. Startup hydration has no renewal or revocation lifecycle. | Pass `--generate-leases=false` on every v1 export command. |
| 11 | Missing CLI falls back to existing env | Services should still start when Phase is not available locally or the runtime image intentionally relies on platform-provided env vars. | `Hydrate`/`MustHydrate` return success with `Result.Source == "env-fallback"` and leave env untouched. |

## 4. Verified facts (from live testing on `https://phase.z.secure/`)

These are recorded as the empirical basis for the design. Re-verify if Phase server major-versions change.

### 4.1 Authentication
- `PHASE_SERVICE_TOKEN` and `PHASE_HOST` environment variables work for headless authentication, exactly as documented.
- Service tokens of the form `pss_service:v2:...` authenticate against both Phase Cloud and self-hosted instances.

### 4.2 JSON export format
- Command: `phase secrets export --app NAME --env NAME --path PATH --format json --generate-leases=false`
- Output shape: flat JSON object `{"KEY": "value", ...}`
- Stdlib `encoding/json.Unmarshal(out, &map[string]string{})` parses cleanly
- Multi-line values, quotes (`"`), backslashes (`\`), and unicode all properly escaped

### 4.3 Dotenv export format is **broken**
- Source: `src/pkg/util/export.go` in `phasehq/cli`:
  ```go
  func ExportDotenv(secrets []KeyValue) {
      for _, kv := range secrets {
          fmt.Printf("%s=\"%s\"\n", kv.Key, kv.Value)
      }
  }
  ```
- No escaping of any kind. A value containing `"` terminates the quoting mid-line. A value with `\n` embeds raw newlines and breaks the line-per-record format. A backslash passes through verbatim.
- **Live-verified failure case:**
  - Stored value: `-----BEGIN TEST-----\nline2 with "quotes"\nline3 with \backslash\n-----END TEST-----`
  - Dotenv output (4 lines, with embedded newlines and unescaped quotes — unparseable):
    ```
    PHASEKIT_MULTILINE="-----BEGIN TEST-----
    line2 with "quotes"
    line3 with \backslash
    -----END TEST-----"
    ```
- This is conclusive evidence that phasekit must use JSON, not dotenv.

### 4.4 Path filtering semantics
| `--path` value | Returns |
|---|---|
| `/` (default) | Only secrets at exactly `/` |
| `/foo` | Only secrets at exactly `/foo` (NOT `/foo/bar`) |
| `''` (empty string) | All secrets at all paths |
| `/nonexistent` | Empty object `{}` (not an error) |

API implication: do not use `Path: ""` to mean all paths, because `Path`'s zero value also means "caller omitted it." Use `AllPaths: true` to pass `--path ""`; otherwise default `Path` to `/`.

### 4.5 AI agent redaction
- Detection env vars: `CLAUDECODE=1`, `CURSOR_AGENT=1`, `CODEX=1`, `OPENCODE=1`, `AGENT=<name>`
- Redaction only activates when both: (a) AI env var detected, AND (b) `~/.phase/ai.json` exists
- Default state (no `ai.json`): zero redaction, even when AI env vars are set
- Phase docs now state AI mode redacts sealed and secret-type values in `secrets export` output. The implementation must not rely on live-test defaults; it must fail fast if any exported value equals `[REDACTED]`, unless `AllowRedacted` is explicitly set.
- Secret types and redaction behavior:
  - `Sealed`: always redacted under AI mode
  - `Secret`: redacted under AI mode if `maskSecretValues: true`
  - `Config`: never redacted

### 4.6 Dynamic secret lease generation
- Phase CLI docs list `--generate-leases` on `secrets export` with default `true`.
- Phase docs also state that if dynamic secrets are configured, the CLI automatically generates leases and exports the fresh values with static secrets.
- Because phasekit v1 has no lease renewal/revoke lifecycle, it must pass `--generate-leases=false`.

## 5. Public API surface

```go
// Package phasekit hydrates environment variables from Phase before
// config.MustLoad runs. It shells out to the `phase` Command-Line Interface
// (CLI), keeping chassis free of Phase SDK dependencies.
//
// Bootstrap config (ServiceToken, Host, App, Env) is supplied via a struct
// literal because phasekit must run before config.MustLoad. All other
// secrets land as environment variables and are consumed by your service's
// normal config.MustLoad[YourConfig]() call.
package phasekit

// Config holds bootstrap parameters for hydrating env from Phase.
type Config struct {
    // Phase service token. Required.
    // Typically read from os.Getenv("PHASE_SERVICE_TOKEN") by the caller.
    ServiceToken string

    // Application name in Phase. Required.
    App string

    // Environment name in Phase (e.g. "Production", "Staging"). Required.
    Env string

    // Path within the app/env. Optional.
    // Default: "/"  (root path only — exact match, not recursive)
    // Ignored when AllPaths is true.
    Path string

    // If true, fetch secrets from all paths by passing --path "" to
    // the Phase CLI. Default: false (root path only).
    AllPaths bool

    // If non-empty, Hydrate fails if any of these keys are missing
    // from the Phase response. Not enforced when the phase CLI is
    // missing and phasekit falls back to the existing environment.
    RequiredKeys []string

    // If true, Phase values replace variables that are already present
    // in the process environment. Default: false (existing env wins).
    OverwriteExisting bool

    // If true, literal "[REDACTED]" values from Phase are allowed.
    // Default: false, because redacted secrets should fail startup.
    AllowRedacted bool

    // Path to the `phase` binary. Optional.
    // Default: exec.LookPath("phase").
    BinaryPath string

    // Phase API host. Optional.
    // Default: "https://console.phase.dev".
    // For self-hosted: e.g. "https://phase.example.com".
    Host string

    // Timeout for the phase CLI subprocess. Default: 10s.
    Timeout time.Duration
}

// Result reports the outcome of a successful hydration.
type Result struct {
    Set     []string  // keys hydrated into env
    Skipped []string  // keys preserved because OverwriteExisting is false
    Source  string    // "phase-cli" or "env-fallback"
}

// MustHydrate calls Hydrate and panics on any error. Matches the
// ergonomics of config.MustLoad and chassis.RequireMajor.
func MustHydrate(ctx context.Context, cfg Config) Result

// Hydrate executes the phase CLI, parses its JSON output, and applies
// the secrets to the process environment. Returns an error instead of
// panicking. Prefer this in tests or when you want to handle Phase
// being unavailable gracefully.
func Hydrate(ctx context.Context, cfg Config) (Result, error)
```

## 6. File structure

```
phasekit/
├── phasekit.go             # Public API + impl, single file with section dividers
│                           # (matches posthogkit single-file convention)
├── phasekit_test.go        # Unit + integration tests using fake binary
└── phasetest/
    ├── phasetest.go        # Helper: write fake `phase` binary, prepend to PATH
    └── phasetest_test.go   # Meta-test for the helper itself

INTEGRATING_PHASE.md         # Repo-root integration guide (mirrors INTEGRATING.md)
CHANGELOG.md                 # Append entry
VERSION                      # Bump (read last per project rule)
```

## 7. Implementation tasks (ordered)

Each task is independently testable and commit-worthy.

### Task 1 — Skeleton + Config struct + doc comments
- Create `phasekit/phasekit.go`
- Package doc comment with architectural overview, security notes, when-to-use guidance
- `Config` struct with all fields documented (one-line per field minimum)
- `Result` struct
- Stub `Hydrate` and `MustHydrate` returning empty `Result` / `nil`
- `chassis.AssertVersionChecked()` call at entry
- Verifies: `go build ./phasekit/...` passes

### Task 2 — Defaults + validation
- `applyDefaults(Config) Config`: fills `Path="/"`, `Host="https://console.phase.dev"`, `Timeout=10s`
- `validate(Config) error`: ServiceToken / App / Env required; others optional
- If `AllPaths` is true, `phasePath(cfg)` returns `""` for the CLI `--path` argument; otherwise it returns `cfg.Path` after defaults.
- Tests: each required field individually missing → distinct error message
- Tests: default path is `/`; `AllPaths: true` passes empty string as the `--path` value; non-root paths pass through unchanged.
- `OverwriteExisting` defaults to `false` via Go zero value; do not add pointer-bool or sentinel config.
- `AllowRedacted` defaults to `false` via Go zero value; a redacted Phase response fails fast.

### Task 3 — Subprocess invocation
- Build argv: `phase secrets export --app X --env Y --path Z --format json --generate-leases=false`
- Pass `PHASE_SERVICE_TOKEN` and `PHASE_HOST` via `cmd.Env` (NOT argv — avoids leaking via `ps`)
- Do not inherit the full parent environment. Build `cmd.Env` from:
  - `PHASE_SERVICE_TOKEN=<cfg.ServiceToken>`
  - `PHASE_HOST=<cfg.Host>`
  - optional network/TLS env copied from the parent if present: `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`, `SSL_CERT_FILE`, `SSL_CERT_DIR`
  - do **not** copy `CODEX`, `CLAUDECODE`, `CURSOR_AGENT`, `OPENCODE`, `AGENT`, or application secret env vars
- Resolve `BinaryPath` via `exec.LookPath("phase")` if empty
- If the binary cannot be resolved, return `Result{Source: "env-fallback"}` with no error and no env mutation; `config.MustLoad` remains responsible for failing on missing required runtime config.
- Use `exec.CommandContext` to honor context timeouts
- Wrap exit errors with stderr context for debuggability:
  ```go
  if exitErr, ok := err.(*exec.ExitError); ok {
      return fmt.Errorf("phasekit: phase CLI exited %d: %s", exitErr.ExitCode(), string(exitErr.Stderr))
  }
  ```
- Tests: use `phasetest.WithFakeBinary` to verify argv construction, env passing, timeout honoring, exit-code handling

### Task 4 — JSON parsing
- Decode `cmd.Output()` into `map[string]json.RawMessage`, then convert to `map[string]string` so malformed non-string values can report the offending key cleanly
- Handle empty-output case (path with no secrets returns `{}` — that's success, not error)
- Validate Phase didn't return non-string values (defensive: malformed response from old Phase server)
- Reject any value equal to `[REDACTED]` unless `AllowRedacted` is true
- Tests: well-formed JSON, empty object, malformed JSON, non-string values, redacted values rejected, redacted values allowed when explicitly configured

### Task 5 — `applyEnv` step
- Validate `RequiredKeys` before applying any environment variables so an error does not leave partial process state
- Iterate parsed pairs (sort keys for deterministic Result ordering)
- Preserve existing variables by default: if `OverwriteExisting` is false and `os.LookupEnv(key)` is true, skip
- Otherwise call `os.Setenv(key, value)`
- Build `Result{Set, Skipped, Source: "phase-cli"}`
- Tests use `t.Setenv` for automatic cleanup

### Task 6 — `Hydrate` / `MustHydrate` orchestration
- Wire: defaults → validate → exec → parse → validate required keys/redaction → applyEnv
- `MustHydrate` panics with formatted message including which step failed
- `MustHydrate` must not panic when the Phase CLI is missing; it should return the same env-fallback result as `Hydrate`
- Honor context cancellation (errors from `exec.CommandContext` surface naturally)
- Logging: emit one INFO log line via `slog.Default()` with count of keys hydrated, never the keys themselves (don't even leak names to logs)

### Task 7 — `phasetest` helper subpackage
- Prefer one helper with options so tests can assert process behavior, not just output:
  ```go
  type FakeBinary struct {
      Path string
      Args []string
      Env  map[string]string
  }

  type FakeOptions struct {
      Secrets    map[string]string
      RawStdout  string
      Stderr     string
      ExitCode   int
      Delay      time.Duration
      RecordEnv  []string
  }

  func WithFakeBinary(t *testing.T, opts FakeOptions) *FakeBinary
  ```
- `WithFakeBinary`:
  - Marshal `secrets` to JSON
  - Write a shell script to `t.TempDir()/phase` that:
    - records argv and requested env vars into temp files
    - sleeps for `Delay`, when set
    - writes `RawStdout` or marshaled `Secrets` to stdout
    - writes `Stderr` to stderr
    - exits with `ExitCode`
  - Make it executable, prepend the temp dir to `PATH` via `t.Setenv("PATH", ...)`
  - Register `t.Cleanup` to restore (handled automatically by `t.Setenv`)
- Meta-test (`phasetest_test.go`):
  - Verifies the fake binary actually runs, records argv/env, handles stderr/exit code, and supports timeout delays
  - Catches regressions in helper itself

### Task 8 — Integration tests in `phasekit_test.go`
- Full `Hydrate` flow against `phasetest.WithFakeBinary`:
  - Happy path: 3 keys, all hydrated
  - existing env preserved by default: pre-set one key via `t.Setenv`, verify it's skipped
  - `OverwriteExisting` honored: pre-set one key, set `OverwriteExisting: true`, verify Phase value replaces it
  - `RequiredKeys` missing: fail with clear error naming the missing key
  - `RequiredKeys` failure does not apply any env vars
  - Multi-line value (with `\n`, `\"`, `\\`): hydrates correctly via JSON
  - Empty Phase response (`{}`): success with empty Result
  - `[REDACTED]` value fails by default and is allowed only with `AllowRedacted: true`
  - argv includes `--generate-leases=false`
  - argv path handling: default `/`, custom exact path, and `AllPaths: true` passes empty string
  - missing phase binary: success with `Source: "env-fallback"`, no env mutation, no `MustHydrate` panic
  - subprocess env includes Phase/proxy allowlist and excludes AI/application env vars
  - Non-zero exit code from CLI: error mentions stderr
  - Context timeout: error wraps `context.DeadlineExceeded`
- `MustHydrate` panic path: catches via `recover()` in test

### Task 9 — Documentation
- Package doc comment in `phasekit.go` (already part of Task 1)
- Per-export doc comments on every public type, function, and Config field
- `INTEGRATING_PHASE.md` at repo root with sections:
  - **Why Phase** — versioning, audit, RBAC, rotation vs `.env`-in-vault-doc
  - **Quickstart** — 5-minute setup walkthrough
  - **Service wiring template** — `cmd/yourservice/main.go` example
  - **Dockerfile recipes**:
    - Alpine: `apk add curl && curl -fsSL .../install.sh | sh`
    - Distroless: copy `phase` binary from a builder stage
  - **Local dev workflow** — `.env` / shell exports override Phase by default; use `OverwriteExisting: true` only for deployments that intentionally want Phase to win
  - **Path semantics** — `Path` is exact-match; `AllPaths: true` fetches every path; no recursive subtree mode exists in v1
  - **Continuous Integration setup** — token storage in CI secrets
  - **Troubleshooting**:
    - `phase: command not found` → install in container
    - `403 Forbidden` → invalid or expired service token
    - `RequiredKey X missing` → verify key exists in Phase at the configured path
    - `[REDACTED]` values → `phase ai disable`, remove `~/.phase/ai.json`, or set `AllowRedacted` only when intentionally testing redaction behavior
  - **Security considerations**:
    - Service token storage (single bootstrap secret problem)
    - Env var leakage via `/proc/PID/environ` (acceptable trade)
    - Phase subprocess gets an allowlisted environment, not full `os.Environ()`
    - Dynamic leases are disabled in v1 via `--generate-leases=false`
    - When to use `Config` vs `Secret` vs `Sealed` types
- Cross-link from existing `INTEGRATING.md` and `README.md`; do not use `AGENTS.md` as product documentation

### Task 10 — Version + CHANGELOG + commit
- Read VERSION last (per project rule to avoid agent collisions)
- Increment patch number (or minor if revisions ≥15)
- CHANGELOG entry:
  ```markdown
  ## [v11.x.y] - 2026-04-26

  ### Added
  - `phasekit` package: hydrates environment variables from Phase secrets
    manager via the `phase` CLI before `config.MustLoad` runs. Read-only at
    startup, preserves existing env vars by default, rejects redacted values,
    and disables dynamic secret leases in v1. See INTEGRATING_PHASE.md.
  - `phasekit/phasetest` subpackage: fake-binary test helper for consumer
    repos that depend on phasekit.

  *(<actual coding agent and model, e.g. Codex:GPT-5 high>)*
  ```
- Single commit covering full feature, using the repo's Lore Commit Protocol
- Commit message template:
  ```
  Keep Phase hydration dependency-light and fail-fast

  Phase already owns secret retrieval and decryption, so chassis only needs a
  startup bridge that exports JSON through the Phase CLI and applies values
  before config.MustLoad runs.

  The implementation preserves existing env vars by default, rejects redacted
  values, and disables dynamic secret lease generation because v1 has no lease
  renewal or revocation lifecycle. Consumers who do not import phasekit are
  unaffected.

  Constraint: No new third-party Go modules for chassis v1 integration
  Constraint: Phase CLI export defaults dynamic leases on, so v1 disables them explicitly
  Rejected: Native Phase SDK integration | broader dependency/API surface without a proven v1 need
  Rejected: Hydrating redacted values | would convert startup success into runtime secret failures
  Confidence: high
  Scope-risk: narrow
  Agent: <actual coding agent and model>
  Tested: go test ./phasekit/...; go test -race ./phasekit/...; go vet ./phasekit/...; CGO_ENABLED=0 go build ./phasekit/...
  Not-tested: <manual Phase smoke result, or reason it was not run>
  ```

## 8. Test strategy

- **Unit (table-driven)**: defaults, validation, JSON parsing — fast, no input/output
- **Integration (fake binary via `phasetest`)**: full `Hydrate` flow, argv construction, subprocess env allowlist, error paths
- **Meta-test**: verify the `phasetest` helper itself works
- **No live network tests**: too brittle for CI. Document a manual smoke-test recipe in `INTEGRATING_PHASE.md` instead.
- **Coverage target**: ≥85% on `phasekit/`, 100% on parsing logic
- **Cleanup discipline**: every test touching environment uses `t.Setenv` for automatic restoration
- **Race detector**: `go test -race ./phasekit/...` clean

## 9. Acceptance criteria

- [ ] `go build ./phasekit/...` and `CGO_ENABLED=0 go build ./phasekit/...` both pass
- [ ] `go test ./phasekit/...` green with ≥85% coverage
- [ ] `go test -race ./phasekit/...` green
- [ ] `go vet ./phasekit/...` clean
- [ ] `go test ./...` green, unless unrelated pre-existing failures are documented
- [ ] No new entries in `go.mod` (only stdlib + existing chassis)
- [ ] `INTEGRATING_PHASE.md` reviewed for completeness
- [ ] Manual smoke test against `https://phase.z.secure/`:
  - Wire a tiny example service to use `phasekit.MustHydrate` + `config.MustLoad`
  - Verify the 9 secrets in `example-app/Production` hydrate into env vars
  - Verify a pre-set env var is preserved by default
  - Verify `OverwriteExisting: true` replaces a pre-set env var
  - Verify a `RequiredKeys` entry that doesn't exist in Phase causes a clean error
  - Verify dynamic secret leases are not generated by the default command
  - Verify `[REDACTED]` output is rejected, either via AI mode or a fake/manual controlled output
  - Verify removing `phase` from `PATH` falls back to existing env without a phasekit panic
- [ ] CHANGELOG entry added
- [ ] VERSION incremented (last step)
- [ ] Code review pass clean (via `code-reviewer` agent or human review)

## 10. Open questions / future work

| Item | Status | Resolution path |
|---|---|---|
| `--tags` flag support in Config | Deferred | Add `Tags []string` to Config in v2 if a user needs it |
| Native Go SDK variant (`phasekit/sdk`) | Deferred | Build only if a user demonstrates a real need. Do not justify deferral with a stale CGO/libsodium claim; current SDK v2 appears Go-native, but it is still a third-party dependency. |
| Background secret rotation | Deferred | Out of scope for v1. Rotating service tokens requires service restart; document this. |
| Dynamic secrets (Phase has these via `--generate-leases`) | Deferred | v1 explicitly disables lease generation. Add `GenerateLeases bool` and `LeaseTTLSeconds int` only with a lease lifecycle design. |
| Service account auth (vs service token) | Deferred | Confirm CLI invocation is identical; if so, transparent. If not, add `AuthMode` to Config. |
| Secret value size limit verification | Open | Test with a value >100KB against real Phase. Likely irrelevant for v1 but worth noting. |
| Refresh on SIGHUP | Deferred | Could be added as `phasekit.WatchSIGHUP(ctx, cfg)` in v2 |

## 11. Build sequence (estimated effort)

| Day | Tasks | Hours |
|---|---|---|
| 1 | Tasks 1-3 (skeleton, defaults+validation, subprocess) | ~3 |
| 2 | Tasks 4-6 (JSON parsing, applyEnv, Hydrate orchestration) | ~2 |
| 3 | Tasks 7-8 (`phasetest` helper, integration tests) | ~2 |
| 4 | Task 9 (docs) | ~1.5 |
| 4 | Task 10 (version, CHANGELOG, commit) | ~0.5 |
| — | Code review + iteration | ~1 |
| **Total** | | **~8-10** |

## 12. Risks

| Risk | Severity | Mitigation |
|---|---|---|
| Phase CLI output format changes between versions | Medium | Document minimum Phase CLI version (≥2.2.0) in `INTEGRATING_PHASE.md`. JSON format is documented and standard — unlikely to change. |
| Phase server downtime breaks service startup | Medium | `MustHydrate` panics, which is correct behavior for missing required secrets. Document that Phase availability is on the critical path for cold starts. Mention caching options if it becomes an issue. |
| Service token leak via `ps` or `/proc/PID/environ` | Low | Token is passed via `cmd.Env`, not argv (not visible to `ps`). `/proc` exposure is unavoidable for env-var-based bootstrap; acceptable trade. Documented. |
| Dynamic leases accidentally generated | High | Always pass `--generate-leases=false` in v1 and verify argv in tests. |
| AI redaction surprises in dev | Medium | Do not inherit AI env vars into the subprocess and reject literal `[REDACTED]` values unless `AllowRedacted` is explicitly true. |
| Required-key error leaves partial env mutation | Medium | Validate required keys before calling `os.Setenv`. Add regression test. |
| `phase` binary not in production image | Low | `Hydrate`/`MustHydrate` fall back to the existing environment with `Source: "env-fallback"`. `config.MustLoad` remains the required-config gate. |

## 13. Dependencies

- **External**: Phase CLI ≥2.2.0 installed in runtime image (verify the release asset name for each target OS/architecture)
- **Go stdlib only**: `os/exec`, `encoding/json`, `context`, `time`, `os`, `errors`, `fmt`, `log/slog`
- **Chassis internal**: `github.com/ai8future/chassis-go/v11` (for `chassis.AssertVersionChecked`)
- **No third-party Go modules**

## 14. References

- Phase CLI source: https://github.com/phasehq/cli
- Phase Go SDK (NOT used in v1 — for reference): https://github.com/phasehq/golang-sdk
- Phase docs: https://docs.phase.dev/
- Phase CLI commands reference: https://docs.phase.dev/cli/commands
- Phase Go SDK docs (reference only): https://pkg.go.dev/github.com/phasehq/golang-sdk/v2/phase
- Test server used during verification: `https://phase.z.secure/` (private)
- Verified CLI version: 2.2.0
- Verified test app: `example-app` / `Production` env

---

## Appendix A: Reference service wiring

```go
// cmd/myservice/main.go
package main

import (
    "context"
    "log"
    "os"

    chassis "github.com/ai8future/chassis-go/v11"
    "github.com/ai8future/chassis-go/v11/config"
    "github.com/ai8future/chassis-go/v11/phasekit"

    "github.com/example/myservice"
)

func main() {
    chassis.SetAppVersion(myservice.AppVersion)
    chassis.RequireMajor(11)

    ctx := context.Background()

    phasekit.MustHydrate(ctx, phasekit.Config{
        ServiceToken: os.Getenv("PHASE_SERVICE_TOKEN"),
        Host:         os.Getenv("PHASE_HOST"),  // "" → uses default
        App:          "myservice",
        Env:          envOr("APP_ENV", "Production"),
        // OverwriteExisting defaults false, so local/orchestrator env wins.
        RequiredKeys: []string{"DATABASE_URL", "JWT_SIGNING_KEY"},
    })

    cfg := config.MustLoad[myservice.Config]()
    if err := myservice.Run(ctx, cfg); err != nil {
        log.Fatal(err)
    }
}

func envOr(key, fallback string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return fallback
}
```

## Appendix B: Reference Dockerfile snippet

```dockerfile
# --- builder stage (existing) ---
FROM golang:1.26-alpine AS builder
# ... your normal build ...

# --- runtime stage ---
FROM alpine:3.20
RUN apk add --no-cache curl ca-certificates \
 && curl -fsSL -o /usr/local/bin/phase \
      https://github.com/phasehq/cli/releases/download/v2.2.0/phase_cli_2.2.0_linux_amd64 \
 && chmod +x /usr/local/bin/phase \
 && apk del curl

COPY --from=builder /app/myservice /usr/local/bin/myservice

ENV PHASE_HOST=https://phase.example.com
# PHASE_SERVICE_TOKEN injected at runtime by orchestrator (k8s secret, fly.io secret, etc.)

ENTRYPOINT ["/usr/local/bin/myservice"]
```

Verify the Phase release asset name before copying this snippet; release naming may vary by version, operating system, and architecture. For distroless images, copy the `phase` binary from a builder stage rather than installing curl in the final stage.

## Appendix C: Secret-type recommendations for chassis users

When storing secrets in Phase that will be hydrated by phasekit, choose the type based on sensitivity:

| Phase secret type | Use for | Behavior under AI mode (if `ai.json` enabled) |
|---|---|---|
| `Config` | Non-sensitive config (feature toggles, log levels, hostnames, ports) | Never redacted |
| `Secret` (default) | Most application secrets (API keys, database URLs) | Redacted only if `maskSecretValues: true` |
| `Sealed` | Production-only highly sensitive credentials (signing keys, root tokens) | Always redacted under AI mode |

Recommendation for chassis services:
- Use `Config` for things you'd be comfortable seeing in a `git log` (rare)
- Use `Secret` (default) for the vast majority of values
- Reserve `Sealed` for production-only secrets that must never appear in any output, including developer terminal sessions

## Appendix D: ADR

**Decision:** Build `phasekit` as a startup-only Phase CLI JSON export bridge. Preserve existing environment variables by default, fall back to existing env when the CLI is absent, reject redacted values by default, and disable dynamic secret leases in v1.

**Drivers:**
- Keep chassis-go free of new third-party Go modules for this integration.
- Keep the public surface small and compatible with existing `config.MustLoad` usage.
- Fail at startup instead of hydrating unusable values or short-lived credentials without lifecycle support.

**Alternatives considered:**
- Native Phase SDK: deferred because it adds a Phase dependency and broader API surface before a v1 consumer need exists.
- Dotenv export: rejected because Phase CLI dotenv output does not escape multiline/quoted values safely.
- Dynamic leases during startup hydration: rejected for v1 because there is no renewal/revoke lifecycle.

**Consequences:**
- Runtime images must include the `phase` binary to hydrate from Phase; otherwise phasekit falls back to existing env.
- Services depend on Phase availability during cold start only when the CLI is present and Phase returns an execution/auth/export error.
- Existing orchestrator or local environment variables win unless the caller sets `OverwriteExisting: true`.

**Follow-ups:**
- Add tags, dynamic leases, or SDK support only with concrete consumer requirements.
- Re-verify Phase CLI flags and AI redaction behavior before implementation if the target CLI version changes.
