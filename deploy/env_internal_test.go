package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	chassis "github.com/ai8future/chassis-go/v11"
)

func init() { chassis.RequireMajor(11) }

func TestParseEnvFileLongLine(t *testing.T) {
	dir := t.TempDir()
	longValue := strings.Repeat("x", 100*1024)
	path := filepath.Join(dir, "config.env")
	if err := os.WriteFile(path, []byte("LONG="+longValue+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := parseEnvFile(path)
	if got["LONG"] != longValue {
		t.Fatalf("expected %d-byte value to parse, got %d bytes", len(longValue), len(got["LONG"]))
	}
}
