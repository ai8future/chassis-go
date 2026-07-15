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
		"./scripts/cleanup-ci-docker.sh \"artifacts/live/${{ matrix.service }}\" 300",
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
		"CHASSIS_NIGHTLY_RACE_COUNT:-3",
		"CHASSIS_NIGHTLY_INTEGRATIONS:-all",
		"CHASSIS_NIGHTLY_INTEGRATION_COUNT:-2",
		"./scripts/test-integration.sh \"$selected\"",
		"CHASSIS_NIGHTLY_RESTART_SERVICES:-redpanda qdrant meilisearch otel-collector inngest",
		"docker restart \"$name\"",
		"TestRedpandaModuleClientRestartProbe",
		"CHASSIS_REDPANDA_RESTART_REQUIRED=1",
		"CHASSIS_REDPANDA_RESTART_BOOTSTRAP",
		"CHASSIS_REDPANDA_RESTART_ADMIN_URL",
		"CHASSIS_REDPANDA_RESTART_CONTAINER",
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
	if got := strings.Count(script, "restart probe complete:"); got != 5 {
		t.Fatalf("nightly restart completion count = %d, want 5", got)
	}
}

func TestWorkflowRequiresDockerE2EAndUsesFailClosedNightlyWrappers(t *testing.T) {
	workflow := readRepoFile(t, ".github", "workflows", "ci.yml")
	for _, want := range []string{
		"CHASSIS_E2E_DOCKER_REQUIRED=1 ./scripts/test-e2e.sh",
		"mkdir -p artifacts/nightly",
		"./scripts/run-nightly-ci.sh",
		"./scripts/cleanup-ci-docker.sh artifacts/nightly 500",
	} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("workflow missing fail-closed contract %q", want)
		}
	}

	wrapper := readRepoFile(t, "scripts", "run-nightly-ci.sh")
	for _, want := range []string{
		`mkdir -p "$artifact_root/nightly"`,
		`pipeline_status=("${PIPESTATUS[@]}")`,
		`producer_status="${pipeline_status[0]}"`,
		`tee_status="${pipeline_status[1]}"`,
	} {
		if !strings.Contains(wrapper, want) {
			t.Fatalf("nightly CI wrapper missing %q", want)
		}
	}
}

func TestNightlyCIWrapperCreatesArtifactsAndPropagatesPipelineFailures(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	wrapper := filepath.Join(repoRoot, "scripts", "run-nightly-ci.sh")
	temp := t.TempDir()
	producer := filepath.Join(temp, "producer")
	if err := os.WriteFile(producer, []byte("#!/bin/sh\nprintf 'producer diagnostics\\n'\nexit \"${FAKE_PRODUCER_STATUS:-0}\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name           string
		producerStatus string
		teeStatus      string
		wantStatus     int
	}{
		{name: "initially absent artifact directory succeeds"},
		{name: "producer failure", producerStatus: "23", wantStatus: 23},
		{name: "tee failure", teeStatus: "19", wantStatus: 19},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifactRoot := filepath.Join(temp, strings.ReplaceAll(tt.name, " ", "-"), "artifacts")
			pathValue := os.Getenv("PATH")
			if tt.teeStatus != "" {
				bin := t.TempDir()
				fakeTee := filepath.Join(bin, "tee")
				if err := os.WriteFile(fakeTee, []byte("#!/bin/sh\ncat >/dev/null\nexit \"${FAKE_TEE_STATUS:-1}\"\n"), 0o700); err != nil {
					t.Fatal(err)
				}
				pathValue = bin + string(os.PathListSeparator) + pathValue
			}
			cmd := exec.Command("bash", wrapper, producer)
			cmd.Env = append(os.Environ(),
				"PATH="+pathValue,
				"CHASSIS_NIGHTLY_CI_ARTIFACT_ROOT="+artifactRoot,
				"FAKE_PRODUCER_STATUS="+tt.producerStatus,
				"FAKE_TEE_STATUS="+tt.teeStatus,
			)
			output, err := cmd.CombinedOutput()
			gotStatus := 0
			if err != nil {
				exitErr, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatalf("nightly wrapper error = %v; output:\n%s", err, output)
				}
				gotStatus = exitErr.ExitCode()
			}
			if gotStatus != tt.wantStatus {
				t.Fatalf("nightly wrapper status = %d, want %d; output:\n%s", gotStatus, tt.wantStatus, output)
			}
			if _, err := os.Stat(filepath.Join(artifactRoot, "nightly")); err != nil {
				t.Fatalf("nightly artifact directory not created: %v", err)
			}
			if tt.teeStatus == "" {
				log, err := os.ReadFile(filepath.Join(artifactRoot, "nightly.log"))
				if err != nil || !strings.Contains(string(log), "producer diagnostics") {
					t.Fatalf("nightly diagnostics log = %q, %v", log, err)
				}
			}
		})
	}
}

func TestDockerE2ERequiredModeFailsClosedAndOptionalModeSkips(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	goCommand, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	fakeDocker := filepath.Join(temp, "docker")
	const fake = `#!/bin/sh
if [ "$1" = info ]; then
  printf 'cannot connect to unix:///missing-chassis-docker.sock\n' >&2
  exit 55
fi
printf 'unexpected docker invocation: %s\n' "$*" >&2
exit 99
`
	if err := os.WriteFile(fakeDocker, []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name       string
		required   string
		wantOK     bool
		wantOutput string
	}{
		{name: "optional", required: "0", wantOK: true, wantOutput: "explicit optional T1 Docker E2E skip"},
		{name: "required", required: "1", wantOutput: "required T1 Docker E2E needs a healthy Docker daemon"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(goCommand, "test", "-timeout=90s", "-count=1", "-tags=e2e", "./e2e", "-run=^TestFullServiceDockerBuildRunHealthBehaviorAndStop$", "-v")
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(),
				"PATH="+temp+string(os.PathListSeparator)+os.Getenv("PATH"),
				"DOCKER_HOST=unix:///missing-chassis-docker.sock",
				"CHASSIS_E2E_DOCKER_REQUIRED="+tt.required,
			)
			output, err := cmd.CombinedOutput()
			if (err == nil) != tt.wantOK {
				t.Fatalf("Docker E2E contract success = %v, want %v; output:\n%s", err == nil, tt.wantOK, output)
			}
			if !strings.Contains(string(output), tt.wantOutput) {
				t.Fatalf("Docker E2E contract output missing %q:\n%s", tt.wantOutput, output)
			}
		})
	}
}

func TestCICleanupPropagatesRemovalFailureAndWritesTruthfulMarkers(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	script := filepath.Join(repoRoot, "scripts", "cleanup-ci-docker.sh")
	temp := t.TempDir()
	fakeDocker := filepath.Join(temp, "docker")
	const fake = `#!/bin/sh
case "$1" in
  ps)
    case "$*" in *--format*) printf 'chassis-owned-container\n' ;; *) printf 'container inventory\n' ;; esac
    exit 0
    ;;
  images) printf 'image inventory\n'; exit 0 ;;
  logs|inspect) printf 'diagnostics\n'; exit 0 ;;
  rm)
    if [ "${FAKE_DOCKER_RM_STATUS:-0}" -ne 0 ]; then
      printf 'forced CI cleanup failure\n' >&2
      exit "$FAKE_DOCKER_RM_STATUS"
    fi
    printf 'chassis-owned-container\n'
    exit 0
    ;;
  *) printf 'unexpected docker invocation: %s\n' "$*" >&2; exit 99 ;;
esac
`
	if err := os.WriteFile(fakeDocker, []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name       string
		rmStatus   string
		wantOK     bool
		wantMarker string
		forbidden  string
	}{
		{name: "success", rmStatus: "0", wantOK: true, wantMarker: "cleanup_complete=", forbidden: "cleanup_failed="},
		{name: "failure", rmStatus: "29", wantMarker: "cleanup_failed=", forbidden: "cleanup_complete="},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out := filepath.Join(temp, tt.name)
			cmd := exec.Command("bash", script, out, "12")
			cmd.Env = append(os.Environ(),
				"PATH="+temp+string(os.PathListSeparator)+os.Getenv("PATH"),
				"FAKE_DOCKER_RM_STATUS="+tt.rmStatus,
			)
			output, err := cmd.CombinedOutput()
			if (err == nil) != tt.wantOK {
				t.Fatalf("CI cleanup success = %v, want %v; output:\n%s", err == nil, tt.wantOK, output)
			}
			marker, readErr := os.ReadFile(filepath.Join(out, "cleanup.txt"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !strings.Contains(string(marker), tt.wantMarker) || strings.Contains(string(marker), tt.forbidden) {
				t.Fatalf("CI cleanup marker = %q, want %q without %q", marker, tt.wantMarker, tt.forbidden)
			}
		})
	}
}

func TestNightlyOwnerFailsWhenOwnedContainerCleanupFails(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	script := filepath.Join(repoRoot, "scripts", "test-nightly.sh")
	temp := t.TempDir()
	fakeGo := filepath.Join(temp, "go")
	const goScript = `#!/bin/sh
if [ "$1" = list ] && [ "$2" = ./... ]; then
  printf './fake\n'
  exit 0
fi
if [ "$1" = test ] && [ "$2" = ./fake ] && [ "$6" = '^Fuzz' ]; then
  printf 'FuzzFake\n'
  exit 0
fi
if [ "$1" = test ]; then
  exit 0
fi
printf 'unexpected go invocation: %s\n' "$*" >&2
exit 99
`
	if err := os.WriteFile(fakeGo, []byte(goScript), 0o700); err != nil {
		t.Fatal(err)
	}
	fakeDocker := filepath.Join(temp, "docker")
	const dockerScript = `#!/bin/sh
case "$1" in
  version) printf 'fake docker version\n'; exit 0 ;;
  run) printf 'fake-container-id\n'; exit 0 ;;
  restart) printf '%s\n' "$2"; exit 0 ;;
  logs|inspect) printf 'fake diagnostics\n'; exit 0 ;;
  rm) printf 'forced nightly cleanup failure\n' >&2; exit 31 ;;
  *) printf 'unexpected docker invocation: %s\n' "$*" >&2; exit 99 ;;
esac
`
	if err := os.WriteFile(fakeDocker, []byte(dockerScript), 0o700); err != nil {
		t.Fatal(err)
	}
	fakeCurl := filepath.Join(temp, "curl")
	if err := os.WriteFile(fakeCurl, []byte("#!/bin/sh\nprintf '{\"result\":{}}\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	artifactDir := filepath.Join(temp, "nightly-artifacts")
	cmd := exec.Command("bash", script)
	cmd.Env = append(os.Environ(),
		"PATH="+temp+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CHASSIS_NIGHTLY_ARTIFACT_DIR="+artifactDir,
		"CHASSIS_FUZZTIME=1s",
		"CHASSIS_NIGHTLY_RACE_COUNT=1",
		"CHASSIS_NIGHTLY_RACE_PACKAGES=./fake",
		"CHASSIS_NIGHTLY_INTEGRATIONS=none",
		"CHASSIS_NIGHTLY_RESTART_SERVICES=qdrant",
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("nightly owner succeeded despite owned-container cleanup failure:\n%s", output)
	}
	if !strings.Contains(string(output), "nightly exit status: 1 (primary=0 cleanup=1)") {
		t.Fatalf("nightly cleanup failure did not become owner status:\n%s", output)
	}
	cleanup, readErr := os.ReadFile(filepath.Join(artifactDir, "container-cleanup.txt"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, want := range []string{"forced nightly cleanup failure", "container_cleanup_failed=", "cleanup_failed="} {
		if !strings.Contains(string(cleanup), want) {
			t.Fatalf("nightly cleanup evidence missing %q:\n%s", want, cleanup)
		}
	}
	if strings.Contains(string(cleanup), "cleanup_complete=") {
		t.Fatalf("nightly cleanup emitted false completion marker:\n%s", cleanup)
	}
}

func TestRedpandaNightlyRestartUsesChassisClientBehaviorBeforeAndAfter(t *testing.T) {
	probe := readRepoFile(t, "testing", "redpanda", "redpanda_restart_integration_test.go")
	for _, want := range []string{
		"assertRestartModuleRoundTrip(t, publisher, values[restartBootstrapEnv], beforeTopic, \"before\")",
		"restartContainer(t, values[restartContainerEnv])",
		"waitForRestartReady(t, admin, values[restartAdminURLEnv])",
		"assertRestartModuleRoundTrip(t, publisher, values[restartBootstrapEnv], afterTopic, \"after\")",
		"stats.EventsPublishedTotal != 2",
		"CHASSIS_REDPANDA_RESTART_PROBE:before",
		"CHASSIS_REDPANDA_RESTART_PROBE:after",
	} {
		if !strings.Contains(probe, want) {
			t.Fatalf("Redpanda restart client probe missing %q", want)
		}
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

func TestNightlyScriptFailsWhenPackageEnumerationFailsAfterPartialOutput(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	script := filepath.Join(repoRoot, "scripts", "test-nightly.sh")
	temp := t.TempDir()
	fakeGo := filepath.Join(temp, "go")
	const fake = `#!/bin/sh
if [ "$1" = "list" ] && [ "$2" = "./..." ]; then
  printf '%s\n' ./good
  printf '%s\n' "package enumeration boom" >&2
  exit 17
fi
if [ "$1" = "test" ]; then
  printf 'unexpected go test after failed package enumeration: %s\n' "$*" >&2
  exit 99
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
		t.Fatalf("nightly script succeeded despite package enumeration failure:\n%s", output)
	}
	for _, want := range []string{
		"package enumeration boom",
		"nightly package enumeration failed; refusing false-green nightly fuzz",
		"nightly exit status: 1",
	} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
	for _, forbidden := range []string{
		"unexpected go test after failed package enumeration",
		"fuzz targets completed",
	} {
		if strings.Contains(string(output), forbidden) {
			t.Fatalf("nightly continued after package enumeration failure (%q):\n%s", forbidden, output)
		}
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
