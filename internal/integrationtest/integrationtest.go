// Package integrationtest provides selection and completion enforcement for
// repository-owned, build-tagged integration suites.
package integrationtest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
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
