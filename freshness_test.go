package chassis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSemverNewer(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"1.0.1", "1.0.0", true},
		{"1.1.0", "1.0.9", true},
		{"2.0.0", "1.9.9", true},
		{"10.0.11", "10.0.8", true},
		{"1.0.0", "1.0.0", false},
		{"1.0.0", "1.0.1", false},
		{"1.0.0", "2.0.0", false},
		{"", "1.0.0", false},
		{"1.0.0", "", false},
		{"abc", "1.0.0", false},
		{"1.0", "1.0.0", false},
		{"1.0.0.1", "1.0.0", true},
		{"1.0.0", "1.0.0.1", false},
	}
	for _, tt := range tests {
		got := semverNewer(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("semverNewer(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestFindModuleRoot(t *testing.T) {
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
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "VERSION"), []byte("1.0.0\n"), 0o644)

	got := findModuleRoot(filepath.Join(root, "myservice"))
	if got != "" {
		t.Errorf("findModuleRoot without go.mod = %q, want empty", got)
	}
}

func TestFindModuleRootNoVersion(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/myapp\n"), 0o644)

	got := findModuleRoot(filepath.Join(root, "myservice"))
	if got != "" {
		t.Errorf("findModuleRoot without VERSION = %q, want empty", got)
	}
}

func TestFindModuleRootDeeplyNested(t *testing.T) {
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

func TestResolveMainPackageFromBuildInfo(t *testing.T) {
	got := resolveMainPackage("github.com/ai8future/rcodegen/cmd/rserve", "github.com/ai8future/rcodegen", "/opt/myapp", "/opt/myapp/bin/rserve")
	if got != "github.com/ai8future/rcodegen/cmd/rserve" {
		t.Errorf("got %q, want full build info path", got)
	}
}

func TestResolveMainPackageFallback(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "cmd", "rserve")
	os.MkdirAll(binDir, 0o755)

	got := resolveMainPackage("command-line-arguments", "github.com/ai8future/rcodegen", root, filepath.Join(binDir, "rserve"))
	if got != "github.com/ai8future/rcodegen/cmd/rserve" {
		t.Errorf("got %q, want computed fallback path", got)
	}
}

func TestResolveMainPackageBinaryAtRoot(t *testing.T) {
	root := t.TempDir()

	got := resolveMainPackage("command-line-arguments", "github.com/ai8future/rcodegen", root, filepath.Join(root, "rcodegen"))
	if got != "github.com/ai8future/rcodegen" {
		t.Errorf("got %q, want module path", got)
	}
}

func TestRebuildNoGo(t *testing.T) {
	t.Setenv("PATH", "")

	err := rebuild("/tmp/fake", "example.com/app", "/tmp/fake/myservice")
	if err == nil {
		t.Fatal("expected error when go not in PATH")
	}
	if !strings.Contains(err.Error(), "go not found in PATH") {
		t.Errorf("expected 'go not found in PATH' error, got: %v", err)
	}
}

func TestCheckFreshnessSkipsWhenNoAppVersion(t *testing.T) {
	origAppVersion := appVersion
	appVersion = ""
	defer func() { appVersion = origAppVersion }()

	checkFreshness()
}

func TestCheckFreshnessSkipsWithNoRebuildEnv(t *testing.T) {
	origAppVersion := appVersion
	appVersion = "1.0.0"
	defer func() { appVersion = origAppVersion }()
	t.Setenv("CHASSIS_NO_REBUILD", "1")

	checkFreshness()
}

func TestCheckFreshnessSkipsWithGuardEnv(t *testing.T) {
	origAppVersion := appVersion
	appVersion = "1.0.0"
	defer func() { appVersion = origAppVersion }()
	t.Setenv("CHASSIS_REBUILD_GUARD", "1")

	checkFreshness()
}
