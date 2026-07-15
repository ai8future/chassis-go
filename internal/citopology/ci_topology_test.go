package citopology_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	chassis "github.com/ai8future/chassis-go/v11"
)

func TestMain(m *testing.M) {
	chassis.RequireMajor(11)
	os.Exit(m.Run())
}

func TestWorkflowDefinesBoundedTriggersConcurrencyAndArtifacts(t *testing.T) {
	workflow := readRepoFile(t, ".github", "workflows", "ci.yml")
	for _, want := range []string{
		"pull_request:",
		"push:",
		"schedule:",
		"workflow_dispatch:",
		"concurrency:",
		"group: ci-${{ github.workflow }}-${{ github.ref }}",
		"cancel-in-progress: true",
		"timeout-minutes: 12",
		"actions/checkout@v7",
		"actions/setup-go@v6",
		"actions/upload-artifact@v7",
		"if: ${{ always() }}",
		"deterministic-diagnostics",
		"nightly-resilience-diagnostics",
	} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("workflow missing %q", want)
		}
	}
}

func TestWorkflowRunsEveryRegisteredLiveServiceInIsolatedMatrix(t *testing.T) {
	workflow := readRepoFile(t, ".github", "workflows", "ci.yml")
	services := readRegisteredServices(t)
	for _, service := range services {
		if !strings.Contains(workflow, service) {
			t.Fatalf("workflow live matrix missing service %q", service)
		}
	}
	for _, want := range []string{
		"name: Live integration (${{ matrix.service }})",
		"service: [redpanda, qdrant, meilisearch, otel-collector, inngest]",
		"CHASSIS_INTEGRATION_SERVICES: ${{ matrix.service }}",
		"./scripts/test-integration.sh \"${{ matrix.service }}\"",
		"artifacts/live/${{ matrix.service }}/image.txt",
		"live-${{ matrix.service }}-diagnostics",
		"docker logs --tail 300",
		"docker inspect",
		"docker rm -f",
	} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("workflow live job missing %q", want)
		}
	}
}

func TestNightlyScriptDiscoversFuzzTargetsAndRunsRealResilienceSelectors(t *testing.T) {
	script := readRepoFile(t, "scripts", "test-nightly.sh")
	for _, want := range []string{
		"go list ./...",
		"-list '^Fuzz'",
		"fuzz target discovery failed for package %s",
		"-fuzz=\"^${fuzz}$\"",
		"CHASSIS_NIGHTLY_RACE_PACKAGES:-./lifecycle ./work ./kafkakit",
		"CHASSIS_NIGHTLY_INTEGRATIONS:-all",
		"./scripts/test-integration.sh \"$selected\"",
		"CHASSIS_NIGHTLY_RESTART_SERVICES:-redpanda qdrant meilisearch otel-collector inngest",
		"docker restart \"$name\"",
		"restart probe complete: redpanda",
		"restart probe complete: qdrant",
		"restart probe complete: meilisearch",
		"restart probe complete: otel-collector",
		"restart probe complete: inngest",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("nightly script missing %q", want)
		}
	}
	if strings.Contains(script, "2>/dev/null") {
		t.Fatal("nightly fuzz discovery must not suppress discovery stderr")
	}
}

func TestNightlyScriptFailsWhenOnePackageFuzzDiscoveryFails(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	script := filepath.Join(repoRoot, "scripts", "test-nightly.sh")
	temp := t.TempDir()
	fakeGo := filepath.Join(temp, "go")
	const fake = `#!/bin/sh
if [ "$1" = "list" ] && [ "$2" = "./..." ]; then
  printf '%s\n' ./good ./bad
  exit 0
fi
if [ "$1" = "test" ] && [ "$2" = "./good" ] && [ "$6" = "^Fuzz" ]; then
  printf '%s\n' FuzzGood
  exit 0
fi
if [ "$1" = "test" ] && [ "$2" = "./bad" ] && [ "$6" = "^Fuzz" ]; then
  printf '%s\n' "bad discovery boom" >&2
  exit 13
fi
if [ "$1" = "test" ] && [ "$2" = "./good" ]; then
  exit 0
fi
printf 'unexpected go invocation: %s\n' "$*" >&2
exit 99
`
	if err := os.WriteFile(fakeGo, []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script)
	cmd.Env = append(os.Environ(),
		"PATH="+temp+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CHASSIS_NIGHTLY_ARTIFACT_DIR="+filepath.Join(temp, "artifacts"),
		"CHASSIS_FUZZTIME=1s",
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("nightly script succeeded despite one package discovery failure:\n%s", output)
	}
	for _, want := range []string{
		"bad discovery boom",
		"fuzz target discovery failed for package ./bad",
		"nightly exit status: 1",
	} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(string(output), "fuzz targets completed: 1") {
		t.Fatalf("nightly reported false-green fuzz completion after discovery failure:\n%s", output)
	}
}

func TestOTelReceiptsUseWritableIsolatedDurablePaths(t *testing.T) {
	script := readRepoFile(t, "scripts", "test-nightly.sh")
	for _, want := range []string{
		`mktemp -d "$root/${label}.XXXXXX"`,
		`chmod 0777 "$dir"`,
		`CHASSIS_OTEL_RECEIPT_DIR="$receipts_dir"`,
		`assert_otel_receipts "$receipts_dir"`,
		`traces.json metrics.json receipt.json`,
		`-exec chmod 0755 {} +`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("nightly OTel receipt path missing %q", want)
		}
	}

	integration := readRepoFile(t, "otel", "otel_integration_test.go")
	for _, want := range []string{
		`os.Getenv("CHASSIS_OTEL_RECEIPT_DIR")`,
		`filepath.IsAbs(dir)`,
		`os.Chmod(dir, 0o777)`,
		`os.Chmod(dir, 0o755)`,
		`receiptsDir + ":/receipts"`,
	} {
		if !strings.Contains(integration, want) {
			t.Fatalf("OTel live integration topology missing %q", want)
		}
	}
}

func TestCoverageScriptCanPreserveProfileArtifact(t *testing.T) {
	script := readRepoFile(t, "scripts", "check-coverage.sh")
	for _, want := range []string{"CHASSIS_COVERAGE_PROFILE", "mkdir -p \"$(dirname \"$profile\")\"", "-coverprofile=\"$profile\""} {
		if !strings.Contains(script, want) {
			t.Fatalf("coverage script missing %q", want)
		}
	}
}

func readRegisteredServices(t *testing.T) []string {
	t.Helper()
	registry := readRepoFile(t, "testing", "integration-suites.tsv")
	var services []string
	for _, line := range strings.Split(registry, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 2 {
			t.Fatalf("invalid registry row %q", line)
		}
		services = append(services, fields[0])
	}
	if len(services) == 0 {
		t.Fatal("no registered integration services")
	}
	return services
}

func readRepoFile(t *testing.T, elems ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", ".."}, elems...)...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
