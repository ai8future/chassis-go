# Auto-Rebuild Freshness Check

**Date:** 2026-03-28
**Status:** Approved design, pending implementation

## Problem

Binaries built with chassis-go frequently run stale. Code and VERSION are updated but the binary doesn't get recompiled, causing services to silently run versions that are multiple releases behind. All chassis-integrated repos ship source alongside binaries and have a VERSION file at the repo root.

## Solution

A freshness check inside `chassis.RequireMajor()` that compares the binary's compiled-in version against the VERSION file on disk. If the disk version is newer, the binary automatically recompiles itself and re-execs.

## Design

### Placement in RequireMajor

The freshness check runs inside `RequireMajor()`, after the `--version` flag check and before the major version assertion:

```
RequireMajor(10)
  ├─ checkVersionFlag()        → --version prints and exits
  ├─ checkFreshness()          → find VERSION on disk, compare, rebuild if stale
  ├─ majorVersionAsserted = true
  └─ major version comparison
```

Running before the major version assertion means even a major version mismatch gets caught and fixed by rebuild.

### Activation

The freshness check **only activates when `SetAppVersion()` has been called**. Without it, the only embedded version is `chassis.Version` (the library version), which is unrelated to the consumer's VERSION file. Comparing them would always mismatch.

Consumer adoption pattern:

```go
//go:embed VERSION
var rawVersion string

func main() {
    chassis.SetAppVersion(strings.TrimSpace(rawVersion))
    chassis.RequireMajor(10)
    // ...
}
```

### Finding VERSION on disk

Resolve symlinks on the binary path via `filepath.EvalSymlinks(os.Executable())`, then walk up from that resolved directory looking for a directory containing both `go.mod` and `VERSION`. This is the module root.

```
/opt/myapp/bin/myservice      <- binary (or symlink target)
/opt/myapp/bin/               <- no go.mod, skip
/opt/myapp/                   <- go.mod + VERSION -> module root
```

**Module path verification:** Read the `go.mod` file and extract the `module` directive. Compare it to `debug.ReadBuildInfo().Main.Path`. If they don't match, the binary is nested inside a different project — skip silently.

### Version comparison

Numeric semver comparison: split on `.`, compare each segment left to right as integers.

| Scenario | Behavior |
|----------|----------|
| Equal | Binary is fresh, continue normally |
| Disk is newer | Binary is stale, trigger rebuild |
| Disk is older | Source was rolled back, skip silently |

### Rebuild mechanism

1. **Print to stderr:**
   ```
   chassis: stale binary (compiled 10.0.8, source 10.0.11) — rebuilding...
   ```

2. **Resolve the main package path.** `debug.ReadBuildInfo().Path` returns the main package import path (e.g., `github.com/ai8future/rcodegen/cmd/rserve`). However, when the binary was built with a relative path (`go build ./cmd/rserve`), this field is `command-line-arguments`. Fallback strategy for this case: compute the relative path from the module root to the binary's directory, then join the module path with that relative path (e.g., module `github.com/ai8future/rcodegen` + `cmd/rserve` → `github.com/ai8future/rcodegen/cmd/rserve`). If neither approach yields a valid package path, warn and continue stale.

3. **Build to a temporary file** in the same directory as the binary (e.g., `myservice.chassis-rebuild.tmp`), then `os.Rename()` it over the original. This avoids `ETXTBSY` errors on macOS/Darwin where overwriting a running binary directly can fail. The atomic rename is safe on all platforms since the OS holds an inode reference to the old binary.

   ```
   go build -o /opt/myapp/bin/myservice.chassis-rebuild.tmp github.com/ai8future/rcodegen/cmd/rserve
   os.Rename("/opt/myapp/bin/myservice.chassis-rebuild.tmp", "/opt/myapp/bin/myservice")
   ```

   The build runs from the module root directory. A 2-minute timeout (`context.WithTimeout`) prevents indefinite hangs from network issues or slow compilation.

4. **Re-exec** via `syscall.Exec(os.Args[0], os.Args, os.Environ())` — replaces the current process with the freshly built binary. Same pattern already used by `lifecycle.Run` for restart.

5. **Loop guard:** Set `CHASSIS_REBUILD_GUARD=1` in the environment before re-exec. At the top of `checkFreshness()`, if the guard is already set, skip the entire check. The guard is **never cleared** — it persists for the lifetime of the re-exec'd process. This prevents infinite loops even if the rebuild somehow produces another stale binary.

6. **Failure is non-fatal:** If `go build` fails, print the error to stderr, clean up the temp file, and continue running the stale binary. Better to run stale than to crash.

### Opt-out

`CHASSIS_NO_REBUILD=1` skips the entire freshness check.

### Silent skip conditions

The check is silently skipped (no warning, no error) when:

- `SetAppVersion()` was not called (no consumer version to compare)
- `os.Executable()` or symlink resolution fails
- No `go.mod` + `VERSION` found walking up from binary
- `go.mod` module path doesn't match `ReadBuildInfo().Main.Path`
- `CHASSIS_NO_REBUILD=1` is set
- `CHASSIS_REBUILD_GUARD=1` is set (loop prevention)

### Edge cases

| Scenario | Behavior |
|----------|----------|
| `go run ./cmd/myservice` | Binary is in a temp dir — no `go.mod` found walking up, silently skip |
| Binary not in a Go module tree | `go.mod` not found, silently skip |
| `go` not in PATH | `exec.LookPath` fails, warn and continue stale |
| Binary path not writable | `go build` fails, warn and continue stale |
| Read-only filesystem (containers) | `go build` fails, warn and continue stale |
| Rebuild still stale | `CHASSIS_REBUILD_GUARD` prevents infinite loop (never cleared) |
| VERSION file missing on disk | Nothing to compare, skip |
| VERSION updated during build | Benign race — new binary gets the newer version, which is correct |
| `ReadBuildInfo().Path` is `command-line-arguments` | Fallback to module path + relative directory computation |
| Multi-binary repo (`cmd/server`, `cmd/worker`, etc.) | Each binary independently checks freshness and rebuilds only itself |

### Security considerations

- The rebuild trusts the source tree at the binary's resolved location. This is the same trust model as the initial build — the binary was already compiled from this source.
- If the source tree is writable by untrusted users, auto-rebuild extends the attack window from deploy-time to runtime. In such environments, disable via `CHASSIS_NO_REBUILD=1`.
- In production/container deployments where there is no Go toolchain, the check naturally skips (no `go` in PATH). Setting `CHASSIS_NO_REBUILD=1` in container entrypoints is still recommended to avoid the overhead of the disk walk.

## File structure

- `chassis.go` — Add `checkFreshness()` call inside `RequireMajor()`
- `freshness.go` — All freshness logic: `checkFreshness()`, `findModuleRoot()`, `readDiskVersion()`, `semverNewer()`, `resolveMainPackage()`, `rebuild()`
- `freshness_test.go` — Tests

## Testing strategy

- **Unit:** `semverNewer(a, b)` with table-driven cases
- **Unit:** `findModuleRoot()` with temp directories containing various combinations of `go.mod` + `VERSION`
- **Unit:** Module path mismatch detection
- **Unit:** `resolveMainPackage()` with both full import path and `command-line-arguments` fallback
- **Integration:** Subprocess helper pattern (existing `TestHelperProcess` approach) to verify rebuild is attempted when versions mismatch
- **Unit:** `CHASSIS_NO_REBUILD` env var skips check
- **Unit:** `CHASSIS_REBUILD_GUARD` prevents infinite loop and is never cleared

## Consumer migration

| Codebase | Changes |
|----------|---------|
| **chassis-go** | New `freshness.go`, one-line addition to `RequireMajor()` |
| **Consumer repos** | Add `//go:embed VERSION` + `SetAppVersion()` per binary entry point. Optionally remove custom `--version` handling. |

The feature ships dormant — no consumer breaks on upgrade. Consumers opt in by calling `SetAppVersion()`.
