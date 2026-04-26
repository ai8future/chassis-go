// Package phasekit hydrates environment variables from Phase before
// config.MustLoad runs.
//
// Phasekit shells out to the phase CLI instead of linking the Phase SDK, keeping
// chassis free of additional Go module dependencies. The caller supplies
// bootstrap configuration directly, usually from PHASE_SERVICE_TOKEN and
// PHASE_HOST, then phasekit exports secrets as JSON and applies them to the
// process environment.
//
// Existing environment variables win by default. This lets local .env files,
// shell exports, and orchestrator-provided secrets override Phase unless the
// caller explicitly opts in to overwriting them.
//
// If the phase CLI binary is missing, Hydrate falls back to the existing
// process environment and returns Result.Source "env-fallback".
package phasekit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
)

const (
	defaultHost    = "https://console.phase.dev"
	defaultPath    = "/"
	defaultTimeout = 10 * time.Second
	sourcePhaseCLI = "phase-cli"
	sourceEnv      = "env-fallback"
	redactedValue  = "[REDACTED]"
)

var errPhaseBinaryMissing = errors.New("phasekit: phase binary not found in PATH")

// Config holds bootstrap parameters for hydrating env from Phase.
type Config struct {
	// ServiceToken is the Phase service token. Required.
	// Typically read from os.Getenv("PHASE_SERVICE_TOKEN") by the caller.
	// Required only when the phase CLI is available.
	ServiceToken string

	// App is the application name in Phase. Required.
	App string

	// Env is the environment name in Phase, for example "Production". Required.
	Env string

	// Path is the exact path within the app/env. Optional.
	// Defaults to "/" (root path only). Ignored when AllPaths is true.
	Path string

	// AllPaths fetches secrets from all paths by passing --path "" to the
	// Phase CLI. Defaults to false.
	AllPaths bool

	// RequiredKeys makes Hydrate fail if any listed key is missing from the
	// Phase response. RequiredKeys are not enforced when the phase CLI is
	// missing and phasekit falls back to the existing process environment.
	RequiredKeys []string

	// OverwriteExisting allows Phase values to replace variables already present
	// in the process environment. Defaults to false.
	OverwriteExisting bool

	// AllowRedacted allows literal "[REDACTED]" values returned by the Phase CLI.
	// Defaults to false because redacted secrets should fail startup.
	AllowRedacted bool

	// BinaryPath is the path to the phase binary. Defaults to exec.LookPath("phase").
	BinaryPath string

	// Host is the Phase API host. Defaults to "https://console.phase.dev".
	Host string

	// Timeout is the maximum duration for the phase CLI subprocess. Defaults to 10s.
	Timeout time.Duration
}

// Result reports the outcome of a successful hydration.
type Result struct {
	// Set lists keys hydrated into the process environment.
	Set []string

	// Skipped lists keys preserved because OverwriteExisting is false.
	Skipped []string

	// Source identifies the hydration source: "phase-cli" or "env-fallback".
	Source string
}

// MustHydrate calls Hydrate and panics on any error. Matches the ergonomics of
// config.MustLoad and chassis.RequireMajor.
func MustHydrate(ctx context.Context, cfg Config) Result {
	result, err := Hydrate(ctx, cfg)
	if err != nil {
		panic(fmt.Sprintf("phasekit: hydrate failed: %v", err))
	}
	return result
}

// Hydrate executes the phase CLI, parses its JSON output, and applies the
// secrets to the process environment.
func Hydrate(ctx context.Context, cfg Config) (Result, error) {
	chassis.AssertVersionChecked()

	if ctx == nil {
		return Result{}, fmt.Errorf("phasekit: context is nil")
	}

	cfg = applyDefaults(cfg)
	if err := validate(cfg); err != nil {
		return Result{}, err
	}

	out, err := exportSecrets(ctx, cfg)
	if err != nil {
		if errors.Is(err, errPhaseBinaryMissing) {
			result := Result{Source: sourceEnv}
			slog.Default().Warn("phasekit: phase CLI not found, using existing environment")
			return result, nil
		}
		return Result{}, err
	}

	secrets, err := parseSecrets(out, cfg.AllowRedacted)
	if err != nil {
		return Result{}, err
	}

	if err := validateRequiredKeys(secrets, cfg.RequiredKeys); err != nil {
		return Result{}, err
	}

	result, err := applyEnv(secrets, cfg.OverwriteExisting)
	if err != nil {
		return Result{}, err
	}

	slog.Default().Info("phasekit: hydrated environment", "set", len(result.Set), "skipped", len(result.Skipped))
	return result, nil
}

func applyDefaults(cfg Config) Config {
	if cfg.Host == "" {
		cfg.Host = defaultHost
	}
	if cfg.Path == "" {
		cfg.Path = defaultPath
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	return cfg
}

func validate(cfg Config) error {
	if strings.TrimSpace(cfg.App) == "" {
		return fmt.Errorf("phasekit: App is required")
	}
	if strings.TrimSpace(cfg.Env) == "" {
		return fmt.Errorf("phasekit: Env is required")
	}
	return nil
}

func exportSecrets(ctx context.Context, cfg Config) ([]byte, error) {
	binaryPath, err := resolveBinary(cfg.BinaryPath)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.ServiceToken) == "" {
		return nil, fmt.Errorf("phasekit: ServiceToken is required")
	}

	args := []string{
		"secrets", "export",
		"--app", cfg.App,
		"--env", cfg.Env,
		"--path", phasePath(cfg),
		"--format", "json",
		"--generate-leases=false",
	}

	execCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, binaryPath, args...)
	cmd.Env = phaseEnv(cfg)

	out, err := cmd.Output()
	if execCtx.Err() != nil {
		return nil, fmt.Errorf("phasekit: phase CLI timed out after %s: %w", cfg.Timeout, execCtx.Err())
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("phasekit: phase CLI exited %d: %s", exitErr.ExitCode(), strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("phasekit: phase CLI failed: %w", err)
	}

	return out, nil
}

func resolveBinary(binaryPath string) (string, error) {
	if binaryPath != "" {
		if !strings.ContainsRune(binaryPath, filepath.Separator) {
			path, err := exec.LookPath(binaryPath)
			if err != nil {
				return "", errPhaseBinaryMissing
			}
			return path, nil
		}
		if _, err := os.Stat(binaryPath); err != nil {
			if os.IsNotExist(err) {
				return "", errPhaseBinaryMissing
			}
			return "", fmt.Errorf("phasekit: phase binary %q unavailable: %w", binaryPath, err)
		}
		return binaryPath, nil
	}
	path, err := exec.LookPath("phase")
	if err != nil {
		return "", errPhaseBinaryMissing
	}
	return path, nil
}

func phasePath(cfg Config) string {
	if cfg.AllPaths {
		return ""
	}
	return cfg.Path
}

func phaseEnv(cfg Config) []string {
	env := []string{
		"PHASE_SERVICE_TOKEN=" + cfg.ServiceToken,
		"PHASE_HOST=" + cfg.Host,
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if val, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+val)
		}
	}
	return env
}

func parseSecrets(out []byte, allowRedacted bool) (map[string]string, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("phasekit: parse phase JSON: %w", err)
	}

	secrets := make(map[string]string, len(raw))
	for key, rawVal := range raw {
		var val string
		if err := json.Unmarshal(rawVal, &val); err != nil {
			return nil, fmt.Errorf("phasekit: key %q has non-string value", key)
		}
		if val == redactedValue && !allowRedacted {
			return nil, fmt.Errorf("phasekit: key %q returned redacted value", key)
		}
		secrets[key] = val
	}
	return secrets, nil
}

func validateRequiredKeys(secrets map[string]string, required []string) error {
	missing := make([]string, 0)
	for _, key := range required {
		if _, ok := secrets[key]; !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("phasekit: required keys missing from Phase response: %s", strings.Join(missing, ", "))
}

func applyEnv(secrets map[string]string, overwriteExisting bool) (Result, error) {
	keys := make([]string, 0, len(secrets))
	for key := range secrets {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := Result{Source: sourcePhaseCLI}
	for _, key := range keys {
		if !overwriteExisting {
			if _, exists := os.LookupEnv(key); exists {
				result.Skipped = append(result.Skipped, key)
				continue
			}
		}
		if err := os.Setenv(key, secrets[key]); err != nil {
			return Result{}, fmt.Errorf("phasekit: set env %q: %w", key, err)
		}
		result.Set = append(result.Set, key)
	}
	return result, nil
}
