package lifecycle

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v10"
	"github.com/ai8future/chassis-go/v10/registry"
)

func TestMain(m *testing.M) {
	chassis.RequireMajor(10)
	os.Exit(m.Run())
}

func TestRunSingleComponentCleanShutdown(t *testing.T) {
	comp := func(ctx context.Context) error {
		return nil
	}

	err := Run(context.Background(), comp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestRunComponentReturnsError(t *testing.T) {
	want := errors.New("component failed")

	comp := func(ctx context.Context) error {
		return want
	}

	err := Run(context.Background(), comp)
	if !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
}

func TestRunMultipleComponentsOneFails(t *testing.T) {
	compErr := errors.New("boom")

	// Track whether the healthy component observed cancellation.
	var cancelled atomic.Bool

	failing := func(ctx context.Context) error {
		return compErr
	}

	healthy := func(ctx context.Context) error {
		// Block until context is cancelled by the failing component.
		<-ctx.Done()
		cancelled.Store(true)
		return nil
	}

	err := Run(context.Background(), failing, healthy)
	if !errors.Is(err, compErr) {
		t.Fatalf("expected %v, got %v", compErr, err)
	}
	if !cancelled.Load() {
		t.Fatal("expected healthy component to observe context cancellation")
	}
}

func TestRunComponentsRespectContextCancellation(t *testing.T) {
	// Pre-cancel the parent context to simulate an external shutdown trigger.
	ctx, cancel := context.WithCancel(context.Background())

	var stopped atomic.Int32

	makeComp := func() Component {
		return func(ctx context.Context) error {
			<-ctx.Done()
			stopped.Add(1)
			return nil
		}
	}

	// Cancel immediately so components unblock right away.
	cancel()

	err := Run(ctx, makeComp(), makeComp(), makeComp())
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if n := stopped.Load(); n != 3 {
		t.Fatalf("expected 3 components to observe cancellation, got %d", n)
	}
}

func TestRunPanicsOnUnknownArgs(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on unsupported argument type, got none")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected string panic, got %T: %v", r, r)
		}
		if got, want := msg, "lifecycle: Run received unsupported argument type string"; got != want {
			t.Fatalf("panic message = %q, want %q", got, want)
		}
	}()

	comp := func(ctx context.Context) error {
		return nil
	}
	Run(context.Background(), comp, "unsupported-string")
}

func TestRunSignalHandling(t *testing.T) {
	done := make(chan error, 1)

	go func() {
		err := Run(context.Background(), func(ctx context.Context) error {
			<-ctx.Done()
			return nil
		})
		done <- err
	}()

	// Give Run a moment to register the signal handler.
	time.Sleep(50 * time.Millisecond)

	// Send SIGINT to our own process.
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("failed to send SIGINT: %v", err)
	}

	select {
	case err := <-done:
		// context.Canceled is acceptable; nil is also fine.
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("expected nil or context.Canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Run to return after SIGINT")
	}
}

func TestRunRegistryIntegration(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)
	t.Cleanup(func() { registry.ResetForTest(t.TempDir()) })

	// Use short intervals so registry goroutines don't block long.
	registry.HeartbeatInterval = 1 // 1 nanosecond — minimal tick
	registry.CmdPollInterval = 1

	// Determine the expected service directory.
	name := os.Getenv("CHASSIS_SERVICE_NAME")
	if name == "" {
		wd, _ := os.Getwd()
		name = filepath.Base(wd)
	}
	svcDir := filepath.Join(tmp, name)
	pid := strconv.Itoa(os.Getpid())
	pidFile := filepath.Join(svcDir, pid+".json")
	logFile := filepath.Join(svcDir, pid+".log.jsonl")

	// A component that verifies the PID file exists mid-run, then exits.
	comp := func(ctx context.Context) error {
		if _, err := os.Stat(pidFile); err != nil {
			return errors.New("PID file should exist during Run: " + err.Error())
		}
		return nil
	}

	err := Run(context.Background(), comp)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// After Run returns, the PID file should be cleaned up by Shutdown.
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("PID file should be removed after Run completes")
	}

	// The log file should still exist with startup and shutdown events.
	if _, err := os.Stat(logFile); err != nil {
		t.Fatalf("log file should exist after Run: %v", err)
	}

	events := readLogEvents(t, logFile)
	if len(events) < 2 {
		t.Fatalf("expected at least 2 log events (startup+shutdown), got %d", len(events))
	}

	// First event must be startup.
	if events[0]["event"] != "startup" {
		t.Errorf("first event = %q, want %q", events[0]["event"], "startup")
	}

	// Last event must be shutdown with reason "clean".
	last := events[len(events)-1]
	if last["event"] != "shutdown" {
		t.Errorf("last event = %q, want %q", last["event"], "shutdown")
	}
	if last["reason"] != "clean" {
		t.Errorf("shutdown reason = %q, want %q", last["reason"], "clean")
	}
}

func TestRunRegistryShutdownReasonOnError(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)
	t.Cleanup(func() { registry.ResetForTest(t.TempDir()) })

	registry.HeartbeatInterval = 1
	registry.CmdPollInterval = 1

	name := os.Getenv("CHASSIS_SERVICE_NAME")
	if name == "" {
		wd, _ := os.Getwd()
		name = filepath.Base(wd)
	}
	svcDir := filepath.Join(tmp, name)
	pid := strconv.Itoa(os.Getpid())
	logFile := filepath.Join(svcDir, pid+".log.jsonl")

	compErr := errors.New("component-crashed")
	comp := func(ctx context.Context) error {
		return compErr
	}

	err := Run(context.Background(), comp)
	if !errors.Is(err, compErr) {
		t.Fatalf("expected %v, got %v", compErr, err)
	}

	events := readLogEvents(t, logFile)
	last := events[len(events)-1]
	if last["event"] != "shutdown" {
		t.Fatalf("last event = %q, want %q", last["event"], "shutdown")
	}
	if reason, ok := last["reason"].(string); !ok || !strings.Contains(reason, "component-crashed") {
		t.Errorf("shutdown reason = %q, want it to contain %q", last["reason"], "component-crashed")
	}
}

// readLogEvents parses a JSONL log file into a slice of maps.
func readLogEvents(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open log file: %v", err)
	}
	defer f.Close()

	var events []map[string]any
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("parse log line %q: %v", line, err)
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan log file: %v", err)
	}
	return events
}
