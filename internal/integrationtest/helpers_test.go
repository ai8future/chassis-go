package integrationtest

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
)

func init() {
	chassis.RequireMajor(11)
}

func TestValidateService(t *testing.T) {
	tests := []struct {
		name    string
		service string
		wantErr string
	}{
		{name: "simple", service: "qdrant"},
		{name: "with dash and digit", service: "otel-collector2"},
		{name: "empty", wantErr: "must not be empty"},
		{name: "leading digit", service: "1qdrant", wantErr: "invalid"},
		{name: "uppercase", service: "Qdrant", wantErr: "invalid"},
		{name: "underscore", service: "otel_collector", wantErr: "invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateService(tt.service)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateService(%q): %v", tt.service, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateService(%q) error = %v, want containing %q", tt.service, err, tt.wantErr)
			}
		})
	}
}

func TestWriteCompletionMarker(t *testing.T) {
	markerDir := t.TempDir()
	t.Setenv(MarkerDirEnv, markerDir)
	if err := writeCompletionMarker("qdrant"); err != nil {
		t.Fatalf("writeCompletionMarker: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(markerDir, "qdrant.complete"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "qdrant\n" {
		t.Fatalf("marker contents = %q", contents)
	}
}

func TestLoadPinnedImageSuccess(t *testing.T) {
	image := LoadPinnedImage(t, "qdrant")
	if !strings.HasPrefix(image, "qdrant/qdrant:v") || !strings.Contains(image, "@sha256:") {
		t.Fatalf("LoadPinnedImage(qdrant) = %q", image)
	}
}

func TestRequireDockerUsesDockerCLI(t *testing.T) {
	temp := t.TempDir()
	fakeDocker := filepath.Join(temp, "docker")
	if err := os.WriteFile(fakeDocker, []byte("#!/bin/sh\nif [ \"$1\" = version ]; then echo 28.0.0; exit 0; fi\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", temp+string(os.PathListSeparator)+os.Getenv("PATH"))
	RequireDocker(t, "qdrant")
}

func TestCleanupDockerFailurePropagatesAndPreservesPrimaryFailure(t *testing.T) {
	temp := t.TempDir()
	fakeDocker := filepath.Join(temp, "docker")
	const fake = `#!/bin/sh
case "$1" in
  logs|inspect) exit 0 ;;
  rm) printf 'forced container removal failure\n' >&2; exit 23 ;;
  *) printf 'unexpected docker command: %s\n' "$*" >&2; exit 99 ;;
esac
`
	if err := os.WriteFile(fakeDocker, []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}

	for _, primaryFailure := range []bool{false, true} {
		name := "cleanup-only"
		if primaryFailure {
			name = "primary-and-cleanup"
		}
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command("go", "test", ".", "-run=^TestCleanupDockerChild$", "-count=1", "-v")
			cmd.Env = append(os.Environ(),
				"PATH="+temp+string(os.PathListSeparator)+os.Getenv("PATH"),
				"INTEGRATIONTEST_CLEANUP_CHILD=1",
				"INTEGRATIONTEST_PRIMARY_FAILURE="+map[bool]string{false: "0", true: "1"}[primaryFailure],
			)
			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("cleanup child succeeded despite forced removal failure:\n%s", output)
			}
			for _, want := range []string{"remove owned container owned-redpanda for redpanda", "forced container removal failure"} {
				if !strings.Contains(string(output), want) {
					t.Fatalf("cleanup child output missing %q:\n%s", want, output)
				}
			}
			if primaryFailure && !strings.Contains(string(output), "primary owner failure") {
				t.Fatalf("cleanup child masked primary failure:\n%s", output)
			}
		})
	}
}

func TestCleanupDockerAndImageSucceedWithBoundedCLI(t *testing.T) {
	temp := t.TempDir()
	fakeDocker := filepath.Join(temp, "docker")
	const fake = `#!/bin/sh
case "$1" in
  rm|rmi) printf 'removed %s\n' "$3"; exit 0 ;;
  *) printf 'unexpected docker command: %s\n' "$*" >&2; exit 99 ;;
esac
`
	if err := os.WriteFile(fakeDocker, []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", temp+string(os.PathListSeparator)+os.Getenv("PATH"))
	CleanupDocker(t, "owned-container", "redpanda")
	CleanupDockerImage(t, "owned-image:tag", "full-service E2E")
}

func TestRunDockerCommandReportsDeadline(t *testing.T) {
	temp := t.TempDir()
	fakeDocker := filepath.Join(temp, "docker")
	if err := os.WriteFile(fakeDocker, []byte("#!/bin/sh\nsleep 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", temp+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := runDockerCommand(10*time.Millisecond, "rm", "-f", "owned-container"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runDockerCommand error = %v, want deadline exceeded", err)
	}
}

func TestCleanupDockerChild(t *testing.T) {
	if os.Getenv("INTEGRATIONTEST_CLEANUP_CHILD") != "1" {
		t.Skip("helper process")
	}
	if os.Getenv("INTEGRATIONTEST_PRIMARY_FAILURE") == "1" {
		t.Error("primary owner failure")
	}
	CleanupDocker(t, "owned-redpanda", "redpanda")
}

func TestWaitForReturnsAfterSuccessfulProbe(t *testing.T) {
	attempts := 0
	WaitFor(t, time.Second, func() (bool, string) {
		attempts++
		return attempts == 2, "not yet"
	})
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}
