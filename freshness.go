package chassis

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"
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

// resolveMainPackage returns the Go import path for the binary's main package.
// If buildInfoPath is a real import path, use it directly. Otherwise fall back
// to computing it from the module path and the binary's relative location.
func resolveMainPackage(buildInfoPath, modulePath, moduleRoot, binPath string) string {
	if buildInfoPath != "" && buildInfoPath != "command-line-arguments" {
		return buildInfoPath
	}

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
		os.Remove(tmpPath)
		return fmt.Errorf("go build failed: %w", err)
	}

	if err := os.Rename(tmpPath, binPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename failed: %w", err)
	}

	return nil
}

func autoRebuildDisabled() bool {
	return os.Getenv("CHASSIS_NO_REBUILD") != ""
}

// checkFreshness compares the compiled-in appVersion against the VERSION file
// on disk at the binary's module root. If the disk version is newer, it
// rebuilds the binary and re-execs. Only active when SetAppVersion() has been
// called, and disabled when CHASSIS_NO_REBUILD is set.
func checkFreshness() {
	av := getAppVersion()
	if av == "" {
		return
	}
	if autoRebuildDisabled() {
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

	if !semverNewer(diskVersion, av) {
		return
	}

	// Stale! Rebuild.
	fmt.Fprintf(os.Stderr, "chassis: stale binary (compiled %s, source %s) — rebuilding...\n",
		av, diskVersion)

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
