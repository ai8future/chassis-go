package phasekit

import (
	"context"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/phasekit/phasetest"
)

func init() {
	chassis.RequireMajor(11)
}

func TestApplyDefaults(t *testing.T) {
	cfg := applyDefaults(Config{})

	if cfg.Host != defaultHost {
		t.Fatalf("expected default host %q, got %q", defaultHost, cfg.Host)
	}
	if cfg.Path != defaultPath {
		t.Fatalf("expected default path %q, got %q", defaultPath, cfg.Path)
	}
	if cfg.Timeout != defaultTimeout {
		t.Fatalf("expected default timeout %s, got %s", defaultTimeout, cfg.Timeout)
	}
}

func TestValidateRequiredFields(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "missing service token",
			cfg:  Config{App: "app", Env: "Production"},
			want: "ServiceToken is required",
		},
		{
			name: "missing app",
			cfg:  Config{ServiceToken: "token", Env: "Production"},
			want: "App is required",
		},
		{
			name: "missing env",
			cfg:  Config{ServiceToken: "token", App: "app"},
			want: "Env is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate(tt.cfg)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestPhasePath(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{name: "default", cfg: applyDefaults(Config{}), want: "/"},
		{name: "custom path", cfg: applyDefaults(Config{Path: "/db"}), want: "/db"},
		{name: "all paths", cfg: applyDefaults(Config{AllPaths: true}), want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := phasePath(tt.cfg); got != tt.want {
				t.Fatalf("expected path %q, got %q", tt.want, got)
			}
		})
	}
}

func TestParseSecrets(t *testing.T) {
	tests := []struct {
		name          string
		out           []byte
		allowRedacted bool
		want          map[string]string
		wantErr       string
	}{
		{
			name: "well formed",
			out:  []byte(`{"DATABASE_URL":"postgres://x","MULTI":"line1\nline2 \"quoted\" \\slash"}`),
			want: map[string]string{
				"DATABASE_URL": "postgres://x",
				"MULTI":        "line1\nline2 \"quoted\" \\slash",
			},
		},
		{
			name: "empty object",
			out:  []byte(`{}`),
			want: map[string]string{},
		},
		{
			name:    "malformed",
			out:     []byte(`{`),
			wantErr: "parse phase JSON",
		},
		{
			name:    "non string",
			out:     []byte(`{"PORT": 8080}`),
			wantErr: `key "PORT" has non-string value`,
		},
		{
			name:    "redacted rejected",
			out:     []byte(`{"TOKEN":"[REDACTED]"}`),
			wantErr: `key "TOKEN" returned redacted value`,
		},
		{
			name:          "redacted allowed",
			out:           []byte(`{"TOKEN":"[REDACTED]"}`),
			allowRedacted: true,
			want:          map[string]string{"TOKEN": "[REDACTED]"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSecrets(tt.out, tt.allowRedacted)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("expected %#v, got %#v", tt.want, got)
			}
		})
	}
}

func TestValidateRequiredKeys(t *testing.T) {
	secrets := map[string]string{"A": "1", "C": ""}
	if err := validateRequiredKeys(secrets, []string{"A", "C"}); err != nil {
		t.Fatalf("expected required keys to pass, got %v", err)
	}

	err := validateRequiredKeys(secrets, []string{"B", "A", "D"})
	if err == nil {
		t.Fatal("expected missing key error")
	}
	if !strings.Contains(err.Error(), "B, D") {
		t.Fatalf("expected sorted missing keys in error, got %v", err)
	}
}

func TestHydrateHappyPath(t *testing.T) {
	fake := phasetest.WithFakeBinary(t, phasetest.FakeOptions{
		Secrets: map[string]string{
			"PHASEKIT_ALPHA": "a",
			"PHASEKIT_BETA":  "b",
			"PHASEKIT_GAMMA": "g",
		},
		RecordEnv: []string{"PHASE_SERVICE_TOKEN", "PHASE_HOST"},
	})

	result, err := Hydrate(context.Background(), Config{
		ServiceToken: "pss_service:v2:test",
		App:          "example-app",
		Env:          "Production",
		RequiredKeys: []string{"PHASEKIT_ALPHA", "PHASEKIT_BETA"},
	})
	if err != nil {
		t.Fatalf("Hydrate returned error: %v", err)
	}

	wantSet := []string{"PHASEKIT_ALPHA", "PHASEKIT_BETA", "PHASEKIT_GAMMA"}
	if !slices.Equal(result.Set, wantSet) {
		t.Fatalf("expected set keys %v, got %v", wantSet, result.Set)
	}
	if len(result.Skipped) != 0 {
		t.Fatalf("expected no skipped keys, got %v", result.Skipped)
	}
	if result.Source != sourcePhaseCLI {
		t.Fatalf("expected source %q, got %q", sourcePhaseCLI, result.Source)
	}
	for _, key := range wantSet {
		if got := os.Getenv(key); got == "" {
			t.Fatalf("expected %s to be set", key)
		}
	}

	wantArgs := []string{
		"secrets", "export",
		"--app", "example-app",
		"--env", "Production",
		"--path", "/",
		"--format", "json",
		"--generate-leases=false",
	}
	if got := fake.Args(t); !slices.Equal(got, wantArgs) {
		t.Fatalf("expected args %v, got %v", wantArgs, got)
	}
	env := fake.Env(t)
	if env["PHASE_SERVICE_TOKEN"] != "pss_service:v2:test" {
		t.Fatalf("expected service token to be passed, got %q", env["PHASE_SERVICE_TOKEN"])
	}
	if env["PHASE_HOST"] != defaultHost {
		t.Fatalf("expected host %q, got %q", defaultHost, env["PHASE_HOST"])
	}
}

func TestHydratePreservesExistingByDefault(t *testing.T) {
	t.Setenv("PHASEKIT_EXISTING", "local")
	t.Setenv("PHASEKIT_EMPTY_EXISTING", "")
	phasetest.WithFakeBinary(t, phasetest.FakeOptions{
		Secrets: map[string]string{
			"PHASEKIT_EXISTING":       "phase",
			"PHASEKIT_EMPTY_EXISTING": "phase-empty",
			"PHASEKIT_NEW":            "new",
		},
	})

	result, err := Hydrate(context.Background(), validConfig())
	if err != nil {
		t.Fatalf("Hydrate returned error: %v", err)
	}

	if got := os.Getenv("PHASEKIT_EXISTING"); got != "local" {
		t.Fatalf("expected existing env to win, got %q", got)
	}
	if got := os.Getenv("PHASEKIT_EMPTY_EXISTING"); got != "" {
		t.Fatalf("expected empty existing env to be preserved, got %q", got)
	}
	if got := os.Getenv("PHASEKIT_NEW"); got != "new" {
		t.Fatalf("expected new env to be set, got %q", got)
	}
	if !slices.Equal(result.Skipped, []string{"PHASEKIT_EMPTY_EXISTING", "PHASEKIT_EXISTING"}) {
		t.Fatalf("expected skipped existing keys, got %v", result.Skipped)
	}
}

func TestHydrateOverwriteExisting(t *testing.T) {
	t.Setenv("PHASEKIT_OVERWRITE", "local")
	phasetest.WithFakeBinary(t, phasetest.FakeOptions{
		Secrets: map[string]string{"PHASEKIT_OVERWRITE": "phase"},
	})

	cfg := validConfig()
	cfg.OverwriteExisting = true
	result, err := Hydrate(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Hydrate returned error: %v", err)
	}

	if got := os.Getenv("PHASEKIT_OVERWRITE"); got != "phase" {
		t.Fatalf("expected Phase value to overwrite existing env, got %q", got)
	}
	if !slices.Equal(result.Set, []string{"PHASEKIT_OVERWRITE"}) {
		t.Fatalf("expected overwritten key in Set, got %v", result.Set)
	}
}

func TestHydrateRequiredKeysMissingDoesNotApplyEnv(t *testing.T) {
	phasetest.WithFakeBinary(t, phasetest.FakeOptions{
		Secrets: map[string]string{"PHASEKIT_SHOULD_NOT_APPLY": "value"},
	})

	cfg := validConfig()
	cfg.RequiredKeys = []string{"PHASEKIT_REQUIRED_MISSING"}
	_, err := Hydrate(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected missing required key error")
	}
	if _, ok := os.LookupEnv("PHASEKIT_SHOULD_NOT_APPLY"); ok {
		t.Fatal("expected env to remain unmodified after required key failure")
	}
}

func TestHydrateMultiLineValue(t *testing.T) {
	want := "-----BEGIN TEST-----\nline2 with \"quotes\"\nline3 with \\backslash\n-----END TEST-----"
	phasetest.WithFakeBinary(t, phasetest.FakeOptions{
		Secrets: map[string]string{"PHASEKIT_MULTILINE": want},
	})

	if _, err := Hydrate(context.Background(), validConfig()); err != nil {
		t.Fatalf("Hydrate returned error: %v", err)
	}
	if got := os.Getenv("PHASEKIT_MULTILINE"); got != want {
		t.Fatalf("expected multiline value to round trip, got %q", got)
	}
}

func TestHydrateEmptyResponse(t *testing.T) {
	phasetest.WithFakeBinary(t, phasetest.FakeOptions{Secrets: map[string]string{}})

	result, err := Hydrate(context.Background(), validConfig())
	if err != nil {
		t.Fatalf("Hydrate returned error: %v", err)
	}
	if len(result.Set) != 0 || len(result.Skipped) != 0 {
		t.Fatalf("expected empty result, got %#v", result)
	}
}

func TestHydrateRedactedValue(t *testing.T) {
	phasetest.WithFakeBinary(t, phasetest.FakeOptions{
		Secrets: map[string]string{"PHASEKIT_REDACTED": "[REDACTED]"},
	})

	if _, err := Hydrate(context.Background(), validConfig()); err == nil {
		t.Fatal("expected redacted value error")
	}
}

func TestHydrateAllowRedacted(t *testing.T) {
	phasetest.WithFakeBinary(t, phasetest.FakeOptions{
		Secrets: map[string]string{"PHASEKIT_REDACTED_ALLOWED": "[REDACTED]"},
	})

	cfg := validConfig()
	cfg.AllowRedacted = true
	if _, err := Hydrate(context.Background(), cfg); err != nil {
		t.Fatalf("Hydrate returned error: %v", err)
	}
	if got := os.Getenv("PHASEKIT_REDACTED_ALLOWED"); got != "[REDACTED]" {
		t.Fatalf("expected redacted value to be applied when allowed, got %q", got)
	}
}

func TestHydrateCustomPathAndAllPaths(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		wantPath string
	}{
		{name: "custom path", cfg: Config{Path: "/db"}, wantPath: "/db"},
		{name: "all paths", cfg: Config{AllPaths: true}, wantPath: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := phasetest.WithFakeBinary(t, phasetest.FakeOptions{Secrets: map[string]string{}})
			cfg := validConfig()
			cfg.Path = tt.cfg.Path
			cfg.AllPaths = tt.cfg.AllPaths
			if _, err := Hydrate(context.Background(), cfg); err != nil {
				t.Fatalf("Hydrate returned error: %v", err)
			}
			args := fake.Args(t)
			gotPath := args[indexAfter(t, args, "--path")]
			if gotPath != tt.wantPath {
				t.Fatalf("expected path %q, got %q", tt.wantPath, gotPath)
			}
		})
	}
}

func TestHydrateSubprocessEnvAllowlist(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://proxy.example")
	t.Setenv("CODEX", "1")
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("APPLICATION_SECRET", "do-not-pass")
	fake := phasetest.WithFakeBinary(t, phasetest.FakeOptions{
		Secrets:   map[string]string{},
		RecordEnv: []string{"PHASE_SERVICE_TOKEN", "PHASE_HOST", "HTTPS_PROXY", "CODEX", "CLAUDECODE", "APPLICATION_SECRET"},
	})

	if _, err := Hydrate(context.Background(), validConfig()); err != nil {
		t.Fatalf("Hydrate returned error: %v", err)
	}
	env := fake.Env(t)
	if env["HTTPS_PROXY"] != "http://proxy.example" {
		t.Fatalf("expected HTTPS_PROXY to be forwarded, got %q", env["HTTPS_PROXY"])
	}
	for _, key := range []string{"CODEX", "CLAUDECODE", "APPLICATION_SECRET"} {
		if env[key] != "" {
			t.Fatalf("expected %s to be excluded from subprocess env, got %q", key, env[key])
		}
	}
}

func TestHydrateNonZeroExit(t *testing.T) {
	phasetest.WithFakeBinary(t, phasetest.FakeOptions{
		Stderr:   "no access",
		ExitCode: 23,
	})

	_, err := Hydrate(context.Background(), validConfig())
	if err == nil {
		t.Fatal("expected phase CLI exit error")
	}
	if !strings.Contains(err.Error(), "phase CLI exited 23: no access") {
		t.Fatalf("expected stderr in error, got %v", err)
	}
}

func TestHydrateContextTimeout(t *testing.T) {
	phasetest.WithFakeBinary(t, phasetest.FakeOptions{
		Secrets: map[string]string{"PHASEKIT_TIMEOUT": "value"},
		Delay:   2 * time.Second,
	})

	cfg := validConfig()
	cfg.Timeout = 20 * time.Millisecond
	_, err := Hydrate(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}
}

func TestMustHydratePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()

	MustHydrate(context.Background(), Config{})
}

func validConfig() Config {
	return Config{
		ServiceToken: "pss_service:v2:test",
		App:          "example-app",
		Env:          "Production",
	}
}

func indexAfter(t *testing.T, args []string, flag string) int {
	t.Helper()
	for i, arg := range args {
		if arg == flag {
			if i+1 >= len(args) {
				t.Fatalf("flag %s has no value in args %v", flag, args)
			}
			return i + 1
		}
	}
	t.Fatalf("flag %s not found in args %v", flag, args)
	return -1
}
