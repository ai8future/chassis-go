// Package integrationtest provides selection and completion enforcement for
// repository-owned, build-tagged integration suites.
package integrationtest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	// ServicesEnv selects comma-separated integration services.
	ServicesEnv = "CHASSIS_INTEGRATION_SERVICES"
	// MarkerDirEnv is set by scripts/test-integration.sh. A selected suite only
	// writes its completion marker after its callback returns without failing or
	// skipping.
	MarkerDirEnv = "CHASSIS_INTEGRATION_MARKER_DIR"
)

// Selection is an immutable set of explicitly selected service names.
type Selection struct {
	services map[string]struct{}
}

// ParseSelection parses the exact, comma-separated service selector contract.
// Empty input selects no services. Empty tokens, duplicates, invalid names, and
// the script-only "all" selector are rejected.
func ParseSelection(raw string) (Selection, error) {
	selection := Selection{services: make(map[string]struct{})}
	if strings.TrimSpace(raw) == "" {
		return selection, nil
	}

	for _, rawService := range strings.Split(raw, ",") {
		service := strings.TrimSpace(rawService)
		if err := validateService(service); err != nil {
			return Selection{}, err
		}
		if service == "all" {
			return Selection{}, fmt.Errorf("service %q is reserved for scripts/test-integration.sh", service)
		}
		if _, exists := selection.services[service]; exists {
			return Selection{}, fmt.Errorf("duplicate integration service %q", service)
		}
		selection.services[service] = struct{}{}
	}
	return selection, nil
}

// Services returns selected services in deterministic order.
func (s Selection) Services() []string {
	services := make([]string, 0, len(s.services))
	for service := range s.services {
		services = append(services, service)
	}
	sort.Strings(services)
	return services
}

// Includes reports whether service was explicitly selected.
func (s Selection) Includes(service string) bool {
	_, ok := s.services[service]
	return ok
}

// Run skips only when service is not selected. For a selected service, fn must
// return successfully before the harness emits the marker required by the
// integration script. A selected fn that fails or calls Skip cannot create a
// false-green completion marker.
func Run(t *testing.T, service string, fn func(*testing.T)) {
	t.Helper()
	if err := validateService(service); err != nil {
		t.Fatal(err)
	}
	selection, err := ParseSelection(os.Getenv(ServicesEnv))
	if err != nil {
		t.Fatalf("invalid %s: %v", ServicesEnv, err)
	}
	if !selection.Includes(service) {
		t.Skipf("integration service %q is not selected by %s", service, ServicesEnv)
	}

	fn(t)
	if t.Failed() {
		return
	}
	if err := writeCompletionMarker(service); err != nil {
		t.Fatalf("write integration completion marker: %v", err)
	}
	t.Logf("CHASSIS_INTEGRATION_SUITE_COMPLETE:%s", service)
}

// RequireEnv returns required endpoint/config values. It must be called inside
// Run so missing configuration for a selected service is a hard failure.
func RequireEnv(t *testing.T, names ...string) map[string]string {
	t.Helper()
	values := make(map[string]string, len(names))
	var missing []string
	for _, name := range names {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			missing = append(missing, name)
			continue
		}
		values[name] = value
	}
	if len(missing) > 0 {
		t.Fatalf("selected integration suite is missing required environment: %s", strings.Join(missing, ", "))
	}
	return values
}

func writeCompletionMarker(service string) error {
	dir := os.Getenv(MarkerDirEnv)
	if dir == "" {
		return nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	marker := filepath.Join(dir, service+".complete")
	return os.WriteFile(marker, []byte(service+"\n"), 0o600)
}

func validateService(service string) error {
	if service == "" {
		return fmt.Errorf("integration service name must not be empty")
	}
	for i, r := range service {
		valid := r >= 'a' && r <= 'z'
		if i > 0 {
			valid = valid || r >= '0' && r <= '9' || r == '-'
		}
		if !valid {
			return fmt.Errorf("invalid integration service name %q", service)
		}
	}
	return nil
}

// LoadPinnedImage returns the immutable image reference registered for service.
// Selected live suites call this so missing or mutable image config is a hard
// failure, not a silent skip.
func LoadPinnedImage(t *testing.T, service string) string {
	t.Helper()
	if err := validateService(service); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repoRoot(t), "testing", "integration-images.tsv")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("selected integration suite is missing pinned image config %s: %v", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 5 {
			t.Fatalf("invalid pinned image config row %q", line)
		}
		if fields[0] != service {
			continue
		}
		image := strings.TrimSpace(fields[1])
		if !strings.Contains(image, "@sha256:") || strings.Contains(image, ":latest") {
			t.Fatalf("%s image must be an immutable non-latest digest pin, got %q", service, image)
		}
		for _, manifest := range fields[2:4] {
			if !strings.HasPrefix(strings.TrimSpace(manifest), "sha256:") {
				t.Fatalf("%s image row must include per-arch manifest digests, got %q", service, line)
			}
		}
		t.Logf("CHASSIS_INTEGRATION_IMAGE:%s:%s", service, image)
		return image
	}
	t.Fatalf("selected integration suite is missing pinned image config for %q", service)
	return ""
}

// RequireDocker hard-fails selected suites when Docker is unavailable or
// unhealthy. Selected live suites must not skip required service startup.
func RequireDocker(t *testing.T, service string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").CombinedOutput()
	if err != nil {
		t.Fatalf("selected %s integration requires healthy Docker: %v\n%s", service, err, out)
	}
}

// CleanupDocker removes a suite-owned container, preserving any primary test
// failure while making removal failure fail an otherwise successful owner.
func CleanupDocker(t *testing.T, name, service string) {
	t.Helper()
	if t.Failed() {
		if logs, err := runDockerCommand(10*time.Second, "logs", "--tail", "200", name); err == nil {
			t.Logf("%s logs:\n%s", service, logs)
		}
		if inspect, err := runDockerCommand(10*time.Second, "inspect", name); err == nil {
			t.Logf("%s inspect:\n%s", service, inspect)
		}
	}
	cleanupDockerResource(t, service, "container", "rm", "-f", name)
}

// CleanupDockerImage removes a suite-owned image and propagates removal
// failures using the same bounded cleanup contract as CleanupDocker.
func CleanupDockerImage(t *testing.T, reference, service string) {
	t.Helper()
	cleanupDockerResource(t, service, "image", "rmi", "-f", reference)
}

func cleanupDockerResource(t *testing.T, service, resource string, args ...string) {
	t.Helper()
	out, err := runDockerCommand(20*time.Second, args...)
	if err != nil {
		t.Errorf("remove owned %s %s for %s: %v\n%s", resource, args[len(args)-1], service, err, out)
	}
}

func runDockerCommand(timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return out, ctx.Err()
	}
	return out, err
}

// WaitFor polls readiness with a bounded context and reports the last observed
// diagnostic on timeout.
func WaitFor(t *testing.T, timeout time.Duration, probe func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := "probe did not run"
	for time.Now().Before(deadline) {
		ok, detail := probe()
		if detail != "" {
			last = detail
		}
		if ok {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("readiness timed out after %s: %s", timeout, last)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("resolve repository root: go.mod not found")
		}
		dir = parent
	}
}
