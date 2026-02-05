package chassis

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestRequireMajorMatchesVersion(t *testing.T) {
	ResetVersionCheck()
	defer ResetVersionCheck()
	major := parseMajor(t, Version)
	RequireMajor(major)
	// Should not exit — assert succeeds.
	AssertVersionChecked()
}

func TestVersionNotEmpty(t *testing.T) {
	if Version == "" {
		t.Error("Version should not be empty")
	}
}

func TestAssertVersionCheckedExitsWhenNotCalled(t *testing.T) {
	output, err := runHelper(t, "assert")
	if err == nil {
		t.Fatalf("expected non-zero exit, got nil error. output=%s", string(output))
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() == 0 {
		t.Fatalf("expected non-zero exit code, got %d", exitErr.ExitCode())
	}
	if !strings.Contains(string(output), "RequireMajor") {
		t.Fatalf("expected RequireMajor hint in output, got: %s", string(output))
	}
}

func TestRequireMajorExitsOnMismatch(t *testing.T) {
	output, err := runHelper(t, "require")
	if err == nil {
		t.Fatalf("expected non-zero exit, got nil error. output=%s", string(output))
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() == 0 {
		t.Fatalf("expected non-zero exit code, got %d", exitErr.ExitCode())
	}
	if !strings.Contains(string(output), "requires chassis v999") {
		t.Fatalf("expected mismatch message in output, got: %s", string(output))
	}
}

func runHelper(t *testing.T, mode string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
	cmd.Env = append(os.Environ(),
		"GO_WANT_HELPER_PROCESS=1",
		"CHASSIS_HELPER_MODE="+mode,
	)
	return cmd.CombinedOutput()
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	switch os.Getenv("CHASSIS_HELPER_MODE") {
	case "assert":
		ResetVersionCheck()
		AssertVersionChecked()
	case "require":
		ResetVersionCheck()
		RequireMajor(999)
	}
	os.Exit(0)
}

func parseMajor(t *testing.T, version string) int {
	t.Helper()
	parts := strings.SplitN(strings.TrimSpace(version), ".", 2)
	if len(parts) == 0 {
		t.Fatalf("invalid Version string: %q", version)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		t.Fatalf("invalid major version in %q: %v", version, err)
	}
	return major
}
