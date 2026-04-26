package phasetest

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
)

func init() {
	chassis.RequireMajor(11)
}

func TestWithFakeBinaryOutputsJSONAndRecordsArgsEnv(t *testing.T) {
	t.Setenv("PHASE_SERVICE_TOKEN", "token")
	t.Setenv("PHASE_HOST", "https://phase.example.com")
	fake := WithFakeBinary(t, FakeOptions{
		Secrets:   map[string]string{"A": "1"},
		RecordEnv: []string{"PHASE_SERVICE_TOKEN", "PHASE_HOST"},
	})

	out, err := exec.Command("phase", "secrets", "export", "--format", "json").Output()
	if err != nil {
		t.Fatalf("fake phase returned error: %v", err)
	}
	if string(out) != `{"A":"1"}` {
		t.Fatalf("expected JSON output, got %q", string(out))
	}

	wantArgs := []string{"secrets", "export", "--format", "json"}
	if got := fake.Args(t); !slices.Equal(got, wantArgs) {
		t.Fatalf("expected args %v, got %v", wantArgs, got)
	}
	env := fake.Env(t)
	if env["PHASE_SERVICE_TOKEN"] != "token" {
		t.Fatalf("expected service token to be recorded, got %q", env["PHASE_SERVICE_TOKEN"])
	}
	if env["PHASE_HOST"] != "https://phase.example.com" {
		t.Fatalf("expected host to be recorded, got %q", env["PHASE_HOST"])
	}
}

func TestWithFakeBinaryError(t *testing.T) {
	WithFakeBinary(t, FakeOptions{
		RawStdout: "partial",
		Stderr:    "denied",
		ExitCode:  7,
	})

	cmd := exec.Command("phase")
	out, err := cmd.Output()
	if err == nil {
		t.Fatal("expected fake phase to fail")
	}
	if string(out) != "partial" {
		t.Fatalf("expected stdout to be preserved, got %q", string(out))
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %T", err)
	}
	if exitErr.ExitCode() != 7 {
		t.Fatalf("expected exit code 7, got %d", exitErr.ExitCode())
	}
	if !strings.Contains(string(exitErr.Stderr), "denied") {
		t.Fatalf("expected stderr to contain denied, got %q", string(exitErr.Stderr))
	}
}

func TestWithFakeBinaryDelay(t *testing.T) {
	WithFakeBinary(t, FakeOptions{
		RawStdout: "ok",
		Delay:     50 * time.Millisecond,
	})

	start := time.Now()
	out, err := exec.Command("phase").Output()
	if err != nil {
		t.Fatalf("fake phase returned error: %v", err)
	}
	if string(out) != "ok" {
		t.Fatalf("expected ok output, got %q", string(out))
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Fatalf("expected delay to be honored, elapsed %s", elapsed)
	}
}
