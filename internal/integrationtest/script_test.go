package integrationtest_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	chassis "github.com/ai8future/chassis-go/v11"
)

func TestIntegrationScriptRejectsFalseSuccess(t *testing.T) {
	chassis.RequireMajor(11)
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	script := filepath.Join(repoRoot, "scripts", "test-integration.sh")

	tests := []struct {
		name     string
		registry string
		request  string
		mode     string
		wantOK   bool
		output   string
	}{
		{name: "empty all", registry: "# none\n", request: "all", output: "refusing an empty success"},
		{name: "unknown", registry: "redpanda\t./kafkakit\n", request: "qdrant", output: "unknown integration service"},
		{name: "missing marker", registry: "redpanda\t./kafkakit\n", request: "redpanda", mode: "nomarker", output: "no valid completion marker"},
		{name: "go failure", registry: "redpanda\t./kafkakit\n", request: "redpanda", mode: "failure", output: "running selected"},
		{name: "one success", registry: "redpanda\t./kafkakit\n", request: "redpanda", mode: "success", wantOK: true},
		{name: "all success", registry: "redpanda\t./kafkakit\nqdrant\t./qdrantkit\n", request: "all", mode: "success", wantOK: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			temp := t.TempDir()
			registry := filepath.Join(temp, "registry.tsv")
			if err := os.WriteFile(registry, []byte(tt.registry), 0o600); err != nil {
				t.Fatal(err)
			}
			fakeGo := filepath.Join(temp, "go")
			const fake = `#!/bin/sh
case "$FAKE_GO_MODE" in
  success) printf '%s\n' "$CHASSIS_INTEGRATION_SERVICES" > "$CHASSIS_INTEGRATION_MARKER_DIR/$CHASSIS_INTEGRATION_SERVICES.complete" ;;
  nomarker|'') ;;
  failure) exit 7 ;;
  *) exit 9 ;;
esac
`
			if err := os.WriteFile(fakeGo, []byte(fake), 0o700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", script, tt.request)
			cmd.Env = append(os.Environ(),
				"PATH="+temp+string(os.PathListSeparator)+os.Getenv("PATH"),
				"CHASSIS_INTEGRATION_REGISTRY="+registry,
				"FAKE_GO_MODE="+tt.mode,
			)
			output, err := cmd.CombinedOutput()
			if (err == nil) != tt.wantOK {
				t.Fatalf("script success = %v, want %v; output:\n%s", err == nil, tt.wantOK, output)
			}
			if tt.output != "" && !strings.Contains(string(output), tt.output) {
				t.Fatalf("output missing %q:\n%s", tt.output, output)
			}
		})
	}
}
