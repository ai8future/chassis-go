package clikit

import (
	"os/exec"
	"strings"
	"testing"

	chassis "github.com/ai8future/chassis-go/v11"
)

func init() {
	chassis.RequireMajor(11)
}

func TestDependencyAllowlist(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "./clikit")
	cmd.Dir = ".."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps ./clikit failed: %v\n%s", err, out)
	}
	banned := []string{
		"github.com/spf13/cobra",
		"github.com/urfave/cli",
		"golang.org/x/term",
		"github.com/charmbracelet/",
	}
	for _, dep := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		for _, bad := range banned {
			if strings.Contains(dep, bad) {
				t.Fatalf("banned dependency %q appears in clikit deps:\n%s", bad, out)
			}
		}
	}
}

func TestDirectImportBoundary(t *testing.T) {
	cmd := exec.Command("go", "list", "-f", `{{range .Imports}}{{println .}}{{end}}`, "./clikit")
	cmd.Dir = ".."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list imports failed: %v\n%s", err, out)
	}
	allowedChassis := map[string]struct{}{
		"github.com/ai8future/chassis-go/v11":          {},
		"github.com/ai8future/chassis-go/v11/config":   {},
		"github.com/ai8future/chassis-go/v11/logz":     {},
		"github.com/ai8future/chassis-go/v11/registry": {},
	}
	for _, imp := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if imp == "" {
			continue
		}
		if _, ok := allowedChassis[imp]; ok {
			continue
		}
		if strings.Contains(imp, ".") {
			t.Fatalf("unexpected direct non-stdlib import %q in clikit imports:\n%s", imp, out)
		}
	}
}
