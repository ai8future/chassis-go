// Package phasetest provides fake phase CLI binaries for testing phasekit
// integrations without network access.
package phasetest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// FakeBinary describes a fake phase binary installed on PATH for a test.
type FakeBinary struct {
	// Path is the absolute path to the fake phase binary.
	Path string

	dir       string
	envKeys   []string
	argsPath  string
	envPath   string
	exitCode  int
	recordEnv []string
}

// FakeOptions controls the behavior of WithFakeBinary.
type FakeOptions struct {
	// Secrets is marshaled to JSON and written to stdout when RawStdout is empty.
	Secrets map[string]string

	// RawStdout is written to stdout verbatim when non-empty.
	RawStdout string

	// Stderr is written to stderr verbatim.
	Stderr string

	// ExitCode is the process exit code. Defaults to 0.
	ExitCode int

	// Delay sleeps before emitting output. Useful for timeout tests.
	Delay time.Duration

	// RecordEnv names environment variables the fake binary should record.
	RecordEnv []string
}

// WithFakeBinary writes a fake phase binary, prepends it to PATH, and returns
// an object that can read the recorded argv and environment after invocation.
func WithFakeBinary(t testing.TB, opts FakeOptions) *FakeBinary {
	t.Helper()

	dir := t.TempDir()
	binPath := filepath.Join(dir, "phase")
	stdoutPath := filepath.Join(dir, "stdout")
	stderrPath := filepath.Join(dir, "stderr")
	exitCodePath := filepath.Join(dir, "exitcode")
	delayPath := filepath.Join(dir, "delay")
	argsPath := filepath.Join(dir, "args")
	envPath := filepath.Join(dir, "env")

	stdout := []byte(opts.RawStdout)
	if opts.RawStdout == "" {
		var err error
		stdout, err = json.Marshal(opts.Secrets)
		if err != nil {
			t.Fatalf("marshal fake secrets: %v", err)
		}
	}
	if err := os.WriteFile(stdoutPath, stdout, 0o600); err != nil {
		t.Fatalf("write fake stdout: %v", err)
	}
	if err := os.WriteFile(stderrPath, []byte(opts.Stderr), 0o600); err != nil {
		t.Fatalf("write fake stderr: %v", err)
	}
	if err := os.WriteFile(exitCodePath, []byte(strconv.Itoa(opts.ExitCode)), 0o600); err != nil {
		t.Fatalf("write fake exit code: %v", err)
	}
	if opts.Delay > 0 {
		delay := strconv.FormatFloat(opts.Delay.Seconds(), 'f', 3, 64)
		if err := os.WriteFile(delayPath, []byte(delay), 0o600); err != nil {
			t.Fatalf("write fake delay: %v", err)
		}
	}

	recordEnv := append([]string(nil), opts.RecordEnv...)
	script := fakeScript(argsPath, envPath, stdoutPath, stderrPath, exitCodePath, delayPath, recordEnv)
	if err := os.WriteFile(binPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake phase binary: %v", err)
	}

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)

	return &FakeBinary{
		Path:      binPath,
		dir:       dir,
		envKeys:   recordEnv,
		argsPath:  argsPath,
		envPath:   envPath,
		exitCode:  opts.ExitCode,
		recordEnv: recordEnv,
	}
}

// Args returns the argv values recorded by the fake binary.
func (f *FakeBinary) Args(t testing.TB) []string {
	t.Helper()
	return readLines(t, f.argsPath)
}

// Env returns the requested environment variables recorded by the fake binary.
func (f *FakeBinary) Env(t testing.TB) map[string]string {
	t.Helper()
	lines := readLines(t, f.envPath)
	env := make(map[string]string, len(lines))
	for _, line := range lines {
		key, val, ok := strings.Cut(line, "=")
		if ok {
			env[key] = val
		}
	}
	return env
}

func fakeScript(argsPath, envPath, stdoutPath, stderrPath, exitCodePath, delayPath string, recordEnv []string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("set -eu\n")
	fmt.Fprintf(&b, ": > %s\n", shellQuote(argsPath))
	b.WriteString("for arg in \"$@\"; do\n")
	fmt.Fprintf(&b, "  printf '%%s\\n' \"$arg\" >> %s\n", shellQuote(argsPath))
	b.WriteString("done\n")
	fmt.Fprintf(&b, ": > %s\n", shellQuote(envPath))
	for _, key := range recordEnv {
		fmt.Fprintf(&b, "printf '%%s=%%s\\n' %s \"${%s-}\" >> %s\n", shellQuote(key), key, shellQuote(envPath))
	}
	fmt.Fprintf(&b, "if [ -s %s ]; then sleep \"$(cat %s)\"; fi\n", shellQuote(delayPath), shellQuote(delayPath))
	fmt.Fprintf(&b, "if [ -s %s ]; then cat %s >&2; fi\n", shellQuote(stderrPath), shellQuote(stderrPath))
	fmt.Fprintf(&b, "cat %s\n", shellQuote(stdoutPath))
	fmt.Fprintf(&b, "exit \"$(cat %s)\"\n", shellQuote(exitCodePath))
	return b.String()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func readLines(t testing.TB, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
