package integrationtest

import (
	"os"
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
