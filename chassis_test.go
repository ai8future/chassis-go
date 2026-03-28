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

func TestVersionFlagPrintsVersionAndExitsZero(t *testing.T) {
	output, err := runHelper(t, "version-flag")
	if err != nil {
		t.Fatalf("expected exit 0, got error: %v\noutput: %s", err, string(output))
	}
	out := string(output)
	if !strings.Contains(out, "chassis-go/"+Version) {
		t.Fatalf("expected chassis-go/%s in output, got: %s", Version, out)
	}
}

func TestVersionFlagWithAppVersion(t *testing.T) {
	output, err := runHelper(t, "version-flag-app")
	if err != nil {
		t.Fatalf("expected exit 0, got error: %v\noutput: %s", err, string(output))
	}
	out := string(output)
	if !strings.Contains(out, "42.0.0") {
		t.Fatalf("expected app version 42.0.0 in output, got: %s", out)
	}
	if !strings.Contains(out, "chassis-go "+Version) {
		t.Fatalf("expected chassis-go %s in output, got: %s", Version, out)
	}
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
	case "version-flag":
		// Inject --version into os.Args so RequireMajor sees it.
		os.Args = []string{os.Args[0], "--version"}
		ResetVersionCheck()
		RequireMajor(parseMajorHelper(Version))
	case "version-flag-app":
		os.Args = []string{os.Args[0], "--version"}
		SetAppVersion("42.0.0")
		ResetVersionCheck()
		RequireMajor(parseMajorHelper(Version))
	}
	os.Exit(0)
}

func TestPortDeterministic(t *testing.T) {
	p1 := Port("serp_svc")
	p2 := Port("serp_svc")
	if p1 != p2 {
		t.Errorf("Port is not deterministic: %d != %d", p1, p2)
	}
}

func TestPortInRange(t *testing.T) {
	names := []string{"a", "serp_svc", "my-service", "x", "very-long-service-name-for-testing"}
	for _, name := range names {
		p := Port(name)
		if p < 5000 || p > 48000 {
			t.Errorf("Port(%q) = %d, outside range 5000–48000", name, p)
		}
	}
}

func TestPortDifferentNames(t *testing.T) {
	p1 := Port("service_a")
	p2 := Port("service_b")
	if p1 == p2 {
		t.Errorf("Port collision: service_a and service_b both got %d", p1)
	}
}

func TestPortOffset(t *testing.T) {
	base := Port("my_svc")
	http := Port("my_svc", PortHTTP)
	grpc := Port("my_svc", PortGRPC)
	metrics := Port("my_svc", PortMetrics)

	if http != base {
		t.Errorf("PortHTTP: got %d, want %d", http, base)
	}
	if grpc != base+1 {
		t.Errorf("PortGRPC: got %d, want %d", grpc, base+1)
	}
	if metrics != base+2 {
		t.Errorf("PortMetrics: got %d, want %d", metrics, base+2)
	}
}

func TestPortDefaultOffsetZero(t *testing.T) {
	if Port("x") != Port("x", 0) {
		t.Error("Port with no offset should equal Port with offset 0")
	}
}

func parseMajorHelper(version string) int {
	parts := strings.SplitN(strings.TrimSpace(version), ".", 2)
	major, _ := strconv.Atoi(parts[0])
	return major
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
