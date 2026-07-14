package integrationtest_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/internal/integrationtest"
)

func TestMain(m *testing.M) {
	chassis.RequireMajor(11)
	os.Exit(m.Run())
}

func TestParseSelection(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
		err  string
	}{
		{name: "empty", want: []string{}},
		{name: "trimmed and sorted", raw: " qdrant,redpanda ", want: []string{"qdrant", "redpanda"}},
		{name: "empty token", raw: "qdrant,", err: "must not be empty"},
		{name: "duplicate", raw: "qdrant,qdrant", err: "duplicate"},
		{name: "reserved all", raw: "all", err: "reserved"},
		{name: "invalid", raw: "Qdrant", err: "invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selection, err := integrationtest.ParseSelection(tt.raw)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("ParseSelection(%q) error = %v, want containing %q", tt.raw, err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSelection(%q): %v", tt.raw, err)
			}
			if got := selection.Services(); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("services = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunSelectionAndCompletion(t *testing.T) {
	tests := []struct {
		name       string
		selection  string
		mode       string
		wantOK     bool
		wantMarker bool
		wantOutput string
	}{
		{name: "unselected skips", selection: "qdrant", mode: "success", wantOK: true, wantOutput: "SKIP"},
		{name: "selected completes", selection: "redpanda", mode: "success", wantOK: true, wantMarker: true},
		{name: "selected skip has no marker", selection: "redpanda", mode: "skip", wantOK: true, wantOutput: "SKIP"},
		{name: "selected missing config fails", selection: "redpanda", mode: "missing", wantOutput: "missing required environment"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			markerDir := t.TempDir()
			cmd := exec.Command("go", "test", ".", "-run=^TestRunChild$", "-count=1", "-v")
			cmd.Env = append(os.Environ(),
				"INTEGRATIONTEST_CHILD=1",
				"INTEGRATIONTEST_MODE="+tt.mode,
				integrationtest.ServicesEnv+"="+tt.selection,
				integrationtest.MarkerDirEnv+"="+markerDir,
			)
			output, err := cmd.CombinedOutput()
			if (err == nil) != tt.wantOK {
				t.Fatalf("go test success = %v, want %v; output:\n%s", err == nil, tt.wantOK, output)
			}
			if tt.wantOutput != "" && !strings.Contains(string(output), tt.wantOutput) {
				t.Fatalf("output missing %q:\n%s", tt.wantOutput, output)
			}
			_, markerErr := os.Stat(filepath.Join(markerDir, "redpanda.complete"))
			if (markerErr == nil) != tt.wantMarker {
				t.Fatalf("marker present = %v, want %v", markerErr == nil, tt.wantMarker)
			}
		})
	}
}

func TestRunDirectSuccessAndUnselected(t *testing.T) {
	markerDir := t.TempDir()
	t.Run("selected", func(t *testing.T) {
		t.Setenv(integrationtest.ServicesEnv, "redpanda")
		t.Setenv(integrationtest.MarkerDirEnv, markerDir)
		t.Setenv("INTEGRATIONTEST_ENDPOINT", "http://127.0.0.1:1")
		integrationtest.Run(t, "redpanda", func(t *testing.T) {
			values := integrationtest.RequireEnv(t, "INTEGRATIONTEST_ENDPOINT")
			if values["INTEGRATIONTEST_ENDPOINT"] == "" {
				t.Fatal("required value missing")
			}
		})
	})
	if contents, err := os.ReadFile(filepath.Join(markerDir, "redpanda.complete")); err != nil || string(contents) != "redpanda\n" {
		t.Fatalf("completion marker = %q, %v", contents, err)
	}

	t.Run("selected without script marker directory", func(t *testing.T) {
		t.Setenv(integrationtest.ServicesEnv, "qdrant")
		t.Setenv(integrationtest.MarkerDirEnv, "")
		integrationtest.Run(t, "qdrant", func(*testing.T) {})
	})
	t.Run("unselected", func(t *testing.T) {
		t.Setenv(integrationtest.ServicesEnv, "qdrant")
		integrationtest.Run(t, "redpanda", func(*testing.T) {
			t.Fatal("unselected callback ran")
		})
	})
}

func TestRunChild(t *testing.T) {
	if os.Getenv("INTEGRATIONTEST_CHILD") != "1" {
		t.Skip("helper process")
	}
	integrationtest.Run(t, "redpanda", func(t *testing.T) {
		switch os.Getenv("INTEGRATIONTEST_MODE") {
		case "success":
		case "skip":
			t.Skip("selected suite attempted to skip")
		case "missing":
			integrationtest.RequireEnv(t, "INTEGRATIONTEST_REQUIRED_ENDPOINT")
		default:
			t.Fatalf("unknown child mode")
		}
	})
}
