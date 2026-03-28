# Auto-Rebuild Freshness Check Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Detect stale binaries at startup and automatically recompile + re-exec when the disk VERSION is newer than the compiled-in version.

**Architecture:** All logic lives in a new `freshness.go` file in the `chassis` package, with a single `checkFreshness()` call added to `RequireMajor()`. The check activates only when `SetAppVersion()` has been called. It walks up from the binary's resolved path to find the module root, compares versions, and rebuilds via `go build` + atomic rename + `syscall.Exec`.

**Tech Stack:** Go stdlib (`debug/buildinfo`, `os/exec`, `filepath`, `syscall`), no new dependencies.

**Spec:** `docs/superpowers/specs/2026-03-28-auto-rebuild-freshness-design.md`

**Implementation notes:**
- `freshness.go` imports accumulate across tasks. The final import block should include: `context`, `fmt`, `os`, `os/exec`, `path/filepath`, `runtime/debug`, `strconv`, `strings`, `syscall`, `time`.
- `freshness_test.go` imports accumulate similarly: `os`, `path/filepath`, `strings`, `testing`.
- **Known limitation:** The rebuild does not preserve original build tags or `CGO_ENABLED` settings. If the binary was originally built with custom flags, the rebuild may differ. This can be addressed in a future iteration if needed.

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `freshness.go` | Create | All freshness logic: `checkFreshness()`, `findModuleRoot()`, `readModulePath()`, `semverNewer()`, `resolveMainPackage()`, `rebuild()` |
| `freshness_test.go` | Create | Unit tests for semver comparison, module root discovery, package resolution, env var guards |
| `chassis.go` | Modify (line 38) | Add `checkFreshness()` call in `RequireMajor()` |

---

### Task 1: semverNewer — version comparison function

**Files:**
- Create: `freshness.go` (initial file with `semverNewer` only)
- Create: `freshness_test.go`

- [ ] **Step 1: Write failing tests for semverNewer**

In `freshness_test.go`:

```go
package chassis

import "testing"

func TestSemverNewer(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"1.0.1", "1.0.0", true},
		{"1.1.0", "1.0.9", true},
		{"2.0.0", "1.9.9", true},
		{"10.0.11", "10.0.8", true},
		{"1.0.0", "1.0.0", false},   // equal
		{"1.0.0", "1.0.1", false},   // older
		{"1.0.0", "2.0.0", false},   // older
		{"", "1.0.0", false},         // empty a
		{"1.0.0", "", false},         // empty b
		{"abc", "1.0.0", false},      // non-numeric
		{"1.0", "1.0.0", false},      // short segment count
		{"1.0.0.1", "1.0.0", true},   // extra segment
		{"1.0.0", "1.0.0.1", false},  // fewer segments
	}
	for _, tt := range tests {
		got := semverNewer(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("semverNewer(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run TestSemverNewer -v .`
Expected: FAIL — `semverNewer` undefined

- [ ] **Step 3: Implement semverNewer in freshness.go**

Create `freshness.go`:

```go
package chassis

import (
	"strconv"
	"strings"
)

// semverNewer returns true if version a is strictly newer than version b.
// Both must be dot-separated numeric strings (e.g., "10.0.11").
// Returns false on parse errors or equal versions.
func semverNewer(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	partsA := strings.Split(a, ".")
	partsB := strings.Split(b, ".")

	maxLen := len(partsA)
	if len(partsB) > maxLen {
		maxLen = len(partsB)
	}

	for i := range maxLen {
		var na, nb int
		var errA, errB error
		if i < len(partsA) {
			na, errA = strconv.Atoi(partsA[i])
		}
		if i < len(partsB) {
			nb, errB = strconv.Atoi(partsB[i])
		}
		if errA != nil || errB != nil {
			return false
		}
		if na > nb {
			return true
		}
		if na < nb {
			return false
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run TestSemverNewer -v .`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add freshness.go freshness_test.go
git commit -m "feat(freshness): add semverNewer version comparison"
```

---

### Task 2: findModuleRoot — locate VERSION + go.mod by walking up from binary

**Files:**
- Modify: `freshness.go`
- Modify: `freshness_test.go`

- [ ] **Step 1: Write failing tests for findModuleRoot**

Append to `freshness_test.go`:

```go
import (
	"os"
	"path/filepath"
)

func TestFindModuleRoot(t *testing.T) {
	// Create temp tree: root/go.mod + root/VERSION + root/bin/myservice
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	os.MkdirAll(binDir, 0o755)
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/myapp\n\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(root, "VERSION"), []byte("1.2.3\n"), 0o644)

	got := findModuleRoot(filepath.Join(binDir, "myservice"))
	if got != root {
		t.Errorf("findModuleRoot = %q, want %q", got, root)
	}
}

func TestFindModuleRootNoGoMod(t *testing.T) {
	// Only VERSION, no go.mod — should not match.
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "VERSION"), []byte("1.0.0\n"), 0o644)

	got := findModuleRoot(filepath.Join(root, "myservice"))
	if got != "" {
		t.Errorf("findModuleRoot without go.mod = %q, want empty", got)
	}
}

func TestFindModuleRootNoVersion(t *testing.T) {
	// Only go.mod, no VERSION — should not match.
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/myapp\n"), 0o644)

	got := findModuleRoot(filepath.Join(root, "myservice"))
	if got != "" {
		t.Errorf("findModuleRoot without VERSION = %q, want empty", got)
	}
}

func TestFindModuleRootDeeplyNested(t *testing.T) {
	// Binary deeply nested: root/cmd/subdir/nested/myservice
	root := t.TempDir()
	binDir := filepath.Join(root, "cmd", "subdir", "nested")
	os.MkdirAll(binDir, 0o755)
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/myapp\n\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(root, "VERSION"), []byte("2.0.0\n"), 0o644)

	got := findModuleRoot(filepath.Join(binDir, "myservice"))
	if got != root {
		t.Errorf("findModuleRoot deeply nested = %q, want %q", got, root)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run TestFindModuleRoot -v .`
Expected: FAIL — `findModuleRoot` undefined

- [ ] **Step 3: Implement findModuleRoot**

Add to `freshness.go`:

```go
import (
	"os"
	"path/filepath"
)

// findModuleRoot walks up from binPath's directory looking for a directory
// containing both go.mod and VERSION. Returns the directory path, or "" if
// not found.
func findModuleRoot(binPath string) string {
	dir := filepath.Dir(binPath)
	for {
		goMod := filepath.Join(dir, "go.mod")
		version := filepath.Join(dir, "VERSION")
		if fileExists(goMod) && fileExists(version) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run TestFindModuleRoot -v .`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add freshness.go freshness_test.go
git commit -m "feat(freshness): add findModuleRoot directory walker"
```

---

### Task 3: readModulePath — extract module directive from go.mod

**Files:**
- Modify: `freshness.go`
- Modify: `freshness_test.go`

- [ ] **Step 1: Write failing tests for readModulePath**

Append to `freshness_test.go`:

```go
func TestReadModulePath(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/ai8future/myapp/v10\n\ngo 1.25\n"), 0o644)

	got := readModulePath(root)
	if got != "github.com/ai8future/myapp/v10" {
		t.Errorf("readModulePath = %q, want %q", got, "github.com/ai8future/myapp/v10")
	}
}

func TestReadModulePathMissing(t *testing.T) {
	got := readModulePath(t.TempDir())
	if got != "" {
		t.Errorf("readModulePath on missing go.mod = %q, want empty", got)
	}
}

func TestReadModulePathMalformed(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("not a real go.mod\n"), 0o644)

	got := readModulePath(root)
	if got != "" {
		t.Errorf("readModulePath on malformed go.mod = %q, want empty", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run TestReadModulePath -v .`
Expected: FAIL — `readModulePath` undefined

- [ ] **Step 3: Implement readModulePath**

Add to `freshness.go`:

```go
// readModulePath reads go.mod in dir and returns the module path.
// Returns "" if the file is missing or malformed.
func readModulePath(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module"))
		}
	}
	return ""
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run TestReadModulePath -v .`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add freshness.go freshness_test.go
git commit -m "feat(freshness): add readModulePath go.mod parser"
```

---

### Task 4: resolveMainPackage — determine the import path to rebuild

**Files:**
- Modify: `freshness.go`
- Modify: `freshness_test.go`

- [ ] **Step 1: Write failing tests for resolveMainPackage**

Append to `freshness_test.go`:

```go
func TestResolveMainPackageFromBuildInfo(t *testing.T) {
	got := resolveMainPackage("github.com/ai8future/rcodegen/cmd/rserve", "github.com/ai8future/rcodegen", "/opt/myapp", "/opt/myapp/bin/rserve")
	if got != "github.com/ai8future/rcodegen/cmd/rserve" {
		t.Errorf("got %q, want full build info path", got)
	}
}

func TestResolveMainPackageFallback(t *testing.T) {
	// When buildInfo path is "command-line-arguments", compute from module root + binary dir.
	root := t.TempDir()
	binDir := filepath.Join(root, "cmd", "rserve")
	os.MkdirAll(binDir, 0o755)

	got := resolveMainPackage("command-line-arguments", "github.com/ai8future/rcodegen", root, filepath.Join(binDir, "rserve"))
	if got != "github.com/ai8future/rcodegen/cmd/rserve" {
		t.Errorf("got %q, want computed fallback path", got)
	}
}

func TestResolveMainPackageBinaryAtRoot(t *testing.T) {
	// Binary is in the module root — package path is just the module path.
	root := t.TempDir()

	got := resolveMainPackage("command-line-arguments", "github.com/ai8future/rcodegen", root, filepath.Join(root, "rcodegen"))
	if got != "github.com/ai8future/rcodegen" {
		t.Errorf("got %q, want module path", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run TestResolveMainPackage -v .`
Expected: FAIL — `resolveMainPackage` undefined

- [ ] **Step 3: Implement resolveMainPackage**

Add to `freshness.go`:

```go
// resolveMainPackage returns the Go import path for the binary's main package.
// If buildInfoPath is a real import path, use it directly. Otherwise fall back
// to computing it from the module path and the binary's relative location.
func resolveMainPackage(buildInfoPath, modulePath, moduleRoot, binPath string) string {
	if buildInfoPath != "" && buildInfoPath != "command-line-arguments" {
		return buildInfoPath
	}

	// Fallback: compute from binary directory relative to module root.
	binDir := filepath.Dir(binPath)
	rel, err := filepath.Rel(moduleRoot, binDir)
	if err != nil {
		return ""
	}

	if rel == "." {
		return modulePath
	}
	return modulePath + "/" + filepath.ToSlash(rel)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run TestResolveMainPackage -v .`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add freshness.go freshness_test.go
git commit -m "feat(freshness): add resolveMainPackage with command-line-arguments fallback"
```

---

### Task 5: rebuild — execute go build with atomic rename

**Files:**
- Modify: `freshness.go`
- Modify: `freshness_test.go`

- [ ] **Step 1: Write failing test for rebuild**

This tests the function signature and error path (no real `go build`). Append to `freshness_test.go`:

```go
func TestRebuildNoGo(t *testing.T) {
	// With an empty PATH, go binary won't be found.
	t.Setenv("PATH", "")

	err := rebuild("/tmp/fake", "example.com/app", "/tmp/fake/myservice")
	if err == nil {
		t.Fatal("expected error when go not in PATH")
	}
	if !strings.Contains(err.Error(), "go not found in PATH") {
		t.Errorf("expected 'go not found in PATH' error, got: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestRebuildNoGo -v .`
Expected: FAIL — `rebuild` undefined

- [ ] **Step 3: Implement rebuild**

Add to `freshness.go`:

```go
import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// rebuildTimeout is the maximum time to wait for go build.
var rebuildTimeout = 2 * time.Minute

// rebuild runs go build to produce a new binary at binPath, building pkgPath
// from moduleRoot. Builds to a temp file then atomically renames.
func rebuild(moduleRoot, pkgPath, binPath string) error {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("go not found in PATH: %w", err)
	}

	tmpPath := binPath + ".chassis-rebuild.tmp"

	ctx, cancel := context.WithTimeout(context.Background(), rebuildTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, goBin, "build", "-o", tmpPath, pkgPath)
	cmd.Dir = moduleRoot
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		os.Remove(tmpPath) // clean up on failure
		return fmt.Errorf("go build failed: %w", err)
	}

	if err := os.Rename(tmpPath, binPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename failed: %w", err)
	}

	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestRebuildNoGo -v .`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add freshness.go freshness_test.go
git commit -m "feat(freshness): add rebuild with atomic rename and timeout"
```

---

### Task 6: checkFreshness — orchestrate the full freshness check

**Files:**
- Modify: `freshness.go`
- Modify: `freshness_test.go`

- [ ] **Step 1: Write failing tests for env var guards**

Append to `freshness_test.go`:

```go
func TestCheckFreshnessSkipsWhenNoAppVersion(t *testing.T) {
	origAppVersion := appVersion
	appVersion = ""
	defer func() { appVersion = origAppVersion }()

	// Should return immediately without doing anything.
	checkFreshness()
}

func TestCheckFreshnessSkipsWithNoRebuildEnv(t *testing.T) {
	origAppVersion := appVersion
	appVersion = "1.0.0"
	defer func() { appVersion = origAppVersion }()
	t.Setenv("CHASSIS_NO_REBUILD", "1")

	// Should return immediately.
	checkFreshness()
}

func TestCheckFreshnessSkipsWithGuardEnv(t *testing.T) {
	origAppVersion := appVersion
	appVersion = "1.0.0"
	defer func() { appVersion = origAppVersion }()
	t.Setenv("CHASSIS_REBUILD_GUARD", "1")

	// Should return immediately.
	checkFreshness()
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run TestCheckFreshness -v .`
Expected: FAIL — `checkFreshness` undefined

- [ ] **Step 3: Implement checkFreshness**

Add to `freshness.go`:

```go
import (
	"runtime/debug"
	"syscall"
)

// checkFreshness compares the compiled-in appVersion against the VERSION file
// on disk at the binary's module root. If the disk version is newer, it
// rebuilds the binary and re-execs. Only active when SetAppVersion() has been
// called.
func checkFreshness() {
	if appVersion == "" {
		return
	}
	if os.Getenv("CHASSIS_NO_REBUILD") != "" {
		return
	}
	if os.Getenv("CHASSIS_REBUILD_GUARD") != "" {
		return
	}

	// Resolve binary path.
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return
	}

	// Find module root.
	moduleRoot := findModuleRoot(exePath)
	if moduleRoot == "" {
		return
	}

	// Verify module path matches build info.
	diskModulePath := readModulePath(moduleRoot)
	if diskModulePath == "" {
		return
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if info.Main.Path != diskModulePath {
		return
	}

	// Read and compare versions.
	diskVersionBytes, err := os.ReadFile(filepath.Join(moduleRoot, "VERSION"))
	if err != nil {
		return
	}
	diskVersion := strings.TrimSpace(string(diskVersionBytes))

	if !semverNewer(diskVersion, appVersion) {
		return
	}

	// Stale! Rebuild.
	fmt.Fprintf(os.Stderr, "chassis: stale binary (compiled %s, source %s) — rebuilding...\n",
		appVersion, diskVersion)

	pkgPath := resolveMainPackage(info.Path, diskModulePath, moduleRoot, exePath)
	if pkgPath == "" {
		fmt.Fprintf(os.Stderr, "chassis: cannot determine main package path — continuing stale\n")
		return
	}

	if err := rebuild(moduleRoot, pkgPath, exePath); err != nil {
		fmt.Fprintf(os.Stderr, "chassis: rebuild failed: %v — continuing stale\n", err)
		return
	}

	// Set guard and re-exec.
	os.Setenv("CHASSIS_REBUILD_GUARD", "1")
	execErr := syscall.Exec(exePath, os.Args, os.Environ())
	// If Exec fails, warn and continue.
	fmt.Fprintf(os.Stderr, "chassis: re-exec failed: %v — continuing stale\n", execErr)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run TestCheckFreshness -v .`
Expected: PASS (the three guard tests all pass)

- [ ] **Step 5: Commit**

```bash
git add freshness.go freshness_test.go
git commit -m "feat(freshness): add checkFreshness orchestrator with env guards"
```

---

### Task 7: Wire checkFreshness into RequireMajor

**Files:**
- Modify: `chassis.go:37-38`

- [ ] **Step 1: Add checkFreshness call to RequireMajor**

In `chassis.go`, change line 38 from:

```go
func RequireMajor(required int) {
	checkVersionFlag()
	majorVersionAsserted.Store(true)
```

To:

```go
func RequireMajor(required int) {
	checkVersionFlag()
	checkFreshness()
	majorVersionAsserted.Store(true)
```

- [ ] **Step 2: Run full test suite**

Run: `go test -v -count=1 .`
Expected: All existing tests still pass, plus new freshness tests pass.

- [ ] **Step 3: Run broader test suite to check for regressions**

Run: `go test ./...`
Expected: All packages pass.

- [ ] **Step 4: Commit**

```bash
git add chassis.go
git commit -m "feat: wire checkFreshness into RequireMajor"
```

---

### Task 8: Version bump, changelog, push

**Files:**
- Modify: `VERSION`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Read current VERSION**

Run: `cat VERSION`

- [ ] **Step 2: Increment VERSION**

Write the incremented version to `VERSION`.

- [ ] **Step 3: Update CHANGELOG.md**

Prepend entry:

```markdown
## [NEW_VERSION] - 2026-03-28

### New Features

- **chassis**: Auto-rebuild freshness check. When `SetAppVersion()` is called and the binary's compiled version is older than the VERSION file on disk, `RequireMajor()` automatically recompiles the binary and re-execs. Opt out with `CHASSIS_NO_REBUILD=1`. Builds to temp file with atomic rename for safety. 2-minute build timeout. Loop guard via `CHASSIS_REBUILD_GUARD` env var.

(Claude Code:Opus 4.6 (1M context))
```

- [ ] **Step 4: Commit and push**

```bash
git add VERSION CHANGELOG.md
git commit -m "feat: auto-rebuild freshness check in RequireMajor

Detects stale binaries by comparing compiled appVersion against the
VERSION file on disk. When stale, rebuilds via go build with atomic
rename and re-execs. Dormant until SetAppVersion() is called."
git push
```
