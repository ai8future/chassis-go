package chassis

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func init() {
	RequireMajor(11)
}

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

func TestRebuildTempPathUnique(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "svc")

	a, err := rebuildTempPath(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(a)

	b, err := rebuildTempPath(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(b)

	if a == b {
		t.Fatalf("expected unique temp paths, got %q twice", a)
	}
	if filepath.Dir(a) != dir || filepath.Dir(b) != dir {
		t.Fatalf("temp paths must sit alongside the binary: %q, %q", a, b)
	}
}

func TestCheckFreshnessSkipsWhenNoAppVersion(t *testing.T) {
	orig := getAppVersion()
	SetAppVersion("")
	defer SetAppVersion(orig)

	checkFreshness()
}

func TestAutoRebuildEnabledByDefault(t *testing.T) {
	t.Setenv("CHASSIS_AUTO_REBUILD", "")
	t.Setenv("CHASSIS_NO_REBUILD", "")

	if autoRebuildDisabled() {
		t.Fatal("auto rebuild should be enabled by default")
	}
}

func TestAutoRebuildDisabledWithNoRebuildEnv(t *testing.T) {
	t.Setenv("CHASSIS_NO_REBUILD", "1")

	if !autoRebuildDisabled() {
		t.Fatal("auto rebuild should be disabled when CHASSIS_NO_REBUILD is set")
	}
}

func TestCheckFreshnessSkipsWithNoRebuildEnv(t *testing.T) {
	orig := getAppVersion()
	SetAppVersion("1.0.0")
	defer SetAppVersion(orig)
	t.Setenv("CHASSIS_NO_REBUILD", "1")

	checkFreshness()
}

func TestCheckFreshnessSkipsWithGuardEnv(t *testing.T) {
	orig := getAppVersion()
	SetAppVersion("1.0.0")
	defer SetAppVersion(orig)
	t.Setenv("CHASSIS_REBUILD_GUARD", "1")

	checkFreshness()
}

func TestCheckFreshnessAcceptsMatchingDiskVersion(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(exe)
	goMod := filepath.Join(dir, "go.mod")
	versionFile := filepath.Join(dir, "VERSION")
	if fileExists(goMod) || fileExists(versionFile) {
		t.Skip("test binary directory already contains module markers")
	}
	if err := os.WriteFile(goMod, []byte("module github.com/ai8future/chassis-go/v11\n"), 0o600); err != nil {
		t.Skipf("test binary directory is not writable: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(goMod) })
	if err := os.WriteFile(versionFile, []byte("1.2.3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(versionFile) })
	original := getAppVersion()
	SetAppVersion("1.2.3")
	t.Cleanup(func() { SetAppVersion(original) })
	t.Setenv("CHASSIS_NO_REBUILD", "")
	t.Setenv("CHASSIS_REBUILD_GUARD", "")

	checkFreshness()
}

func TestRebuildReplacesBinaryWithRunnableProgram(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain unavailable")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/rebuildtest\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cmd", "app", "main.go"), []byte("package main\nimport \"fmt\"\nfunc main(){fmt.Print(\"rebuilt\")}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(root, "app")
	if err := os.WriteFile(binPath, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := rebuild(root, "./cmd/app", binPath); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	out, err := exec.Command(binPath).CombinedOutput()
	if err != nil {
		t.Fatalf("rebuilt binary: %v: %s", err, out)
	}
	if string(out) != "rebuilt" {
		t.Fatalf("output = %q", out)
	}
}

func TestRebuildReportsBuildFailureWithoutReplacingBinary(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/rebuildtest\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(root, "app")
	if err := os.WriteFile(binPath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := rebuild(root, "./missing", binPath); err == nil || !strings.Contains(err.Error(), "go build failed") {
		t.Fatalf("rebuild error = %v", err)
	}
	data, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("binary changed to %q", data)
	}
}

func TestRebuildTempPathRejectsMissingDirectory(t *testing.T) {
	if _, err := rebuildTempPath(filepath.Join(t.TempDir(), "missing", "app")); err == nil {
		t.Fatal("expected missing-directory error")
	}
}
