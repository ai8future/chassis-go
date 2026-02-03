// Package testkit provides lightweight test helpers for chassis-go services.
package testkit

import (
	"io"
	"log/slog"
	"net"
	"os"
	"testing"

	"github.com/ai8future/chassis-go/config"
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

// LoadConfig sets the supplied environment variables, registers a t.Cleanup to
// unset them after the test, and then calls config.MustLoad[T]() to parse them
// into a struct of type T.
func LoadConfig[T any](t testing.TB, envs map[string]string) T {
	t.Helper()
	for k, v := range envs {
		os.Setenv(k, v)
	}
	t.Cleanup(func() {
		for k := range envs {
			os.Unsetenv(k)
		}
	})
	return config.MustLoad[T]()
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
