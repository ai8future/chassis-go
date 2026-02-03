// Package testkit provides lightweight test helpers for chassis-go services.
// It has zero dependencies on other chassis packages.
package testkit

import (
	"io"
	"log/slog"
	"net"
	"os"
	"testing"
)

// testWriter is an io.Writer that forwards all writes to testing.TB.Log.
type testWriter struct {
	t testing.TB
}

func (w *testWriter) Write(p []byte) (int, error) {
	w.t.Helper()
	w.t.Log(string(p))
	return len(p), nil
}

// NewLogger returns a *slog.Logger that writes JSON output to t.Log so that
// log lines appear alongside test output and are suppressed on success unless
// -v is passed.  The level is set to Debug so every message is captured.
func NewLogger(t testing.TB) *slog.Logger {
	t.Helper()
	w := &testWriter{t: t}
	handler := slog.NewJSONHandler(io.Writer(w), &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	return slog.New(handler)
}

// SetEnv sets the supplied environment variables and registers a t.Cleanup to
// unset them after the test. This is the building block for test config — pair
// it with config.MustLoad[T]() in your test to load typed configuration.
//
// Example:
//
//	testkit.SetEnv(t, map[string]string{"PORT": "8080"})
//	cfg := config.MustLoad[AppConfig]()
func SetEnv(t testing.TB, envs map[string]string) {
	t.Helper()
	for k, v := range envs {
		os.Setenv(k, v)
	}
	t.Cleanup(func() {
		for k := range envs {
			os.Unsetenv(k)
		}
	})
}

// GetFreePort asks the OS for an available TCP port by listening on :0, then
// closes the listener and returns the assigned port.  This is useful for
// parallel tests that each need their own listener.
func GetFreePort() (int, error) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port, nil
}
