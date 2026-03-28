package chassis

import (
	"os"
	"path/filepath"
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
