package registry_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ai8future/chassis-go/v6/registry"
)

// helper: initialise registry with a temp dir and return the service dir path.
func initRegistry(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	registry.ResetForTest(tmp)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if err := registry.Init(cancel, "5.0.0-test"); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	t.Cleanup(func() { registry.Shutdown("test-done") })

	_ = ctx // cancel is passed to Init

	// Determine the service directory name (cwd basename or env var).
	name := os.Getenv("CHASSIS_SERVICE_NAME")
	if name == "" {
		wd, _ := os.Getwd()
		name = filepath.Base(wd)
	}
	return filepath.Join(tmp, name)
}

func TestInitCreatesDirectoryAndFiles(t *testing.T) {
	svcDir := initRegistry(t)
	pid := strconv.Itoa(os.Getpid())

	// PID.json must exist.
	pidFile := filepath.Join(svcDir, pid+".json")
	if _, err := os.Stat(pidFile); err != nil {
		t.Fatalf("PID file not found: %v", err)
	}

	// Log file must exist.
	logFile := filepath.Join(svcDir, pid+".log.jsonl")
	if _, err := os.Stat(logFile); err != nil {
		t.Fatalf("Log file not found: %v", err)
	}
}

func TestPIDFileContainsCorrectFields(t *testing.T) {
	svcDir := initRegistry(t)
	pid := os.Getpid()
	pidFile := filepath.Join(svcDir, strconv.Itoa(pid)+".json")

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read PID file: %v", err)
	}

	var reg registry.Registration
	if err := json.Unmarshal(data, &reg); err != nil {
		t.Fatalf("parse PID JSON: %v", err)
	}

	if reg.PID != pid {
		t.Errorf("PID = %d, want %d", reg.PID, pid)
	}
	if reg.Language != "go" {
		t.Errorf("Language = %q, want %q", reg.Language, "go")
	}
	if reg.ChassisVersion != "5.0.0-test" {
		t.Errorf("ChassisVersion = %q, want %q", reg.ChassisVersion, "5.0.0-test")
	}
	if reg.Name == "" {
		t.Error("Name is empty")
	}
	if reg.StartedAt == "" {
		t.Error("StartedAt is empty")
	}
	if reg.Hostname == "" {
		t.Error("Hostname is empty")
	}
	if len(reg.Args) == 0 {
		t.Error("Args is empty")
	}

	// Must have at least the two builtin commands.
	foundStop, foundRestart := false, false
	for _, cmd := range reg.Commands {
		if cmd.Name == "stop" && cmd.Builtin {
			foundStop = true
		}
		if cmd.Name == "restart" && cmd.Builtin {
			foundRestart = true
		}
	}
	if !foundStop {
		t.Error("builtin 'stop' command not found")
	}
	if !foundRestart {
		t.Error("builtin 'restart' command not found")
	}
}

func TestStartupEventInLog(t *testing.T) {
	svcDir := initRegistry(t)
	pid := strconv.Itoa(os.Getpid())
	logFile := filepath.Join(svcDir, pid+".log.jsonl")

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 {
		t.Fatal("log file is empty")
	}

	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("parse first log line: %v", err)
	}

	if first["event"] != "startup" {
		t.Errorf("first event = %q, want %q", first["event"], "startup")
	}
	if first["name"] == nil || first["name"] == "" {
		t.Error("startup event missing 'name'")
	}
	if first["pid"] == nil {
		t.Error("startup event missing 'pid'")
	}
	if first["hostname"] == nil {
		t.Error("startup event missing 'hostname'")
	}
	if first["ts"] == nil {
		t.Error("startup event missing 'ts'")
	}
}

func TestStatusWritesStatusEvent(t *testing.T) {
	svcDir := initRegistry(t)
	pid := strconv.Itoa(os.Getpid())

	registry.Status("all systems nominal")

	logFile := filepath.Join(svcDir, pid+".log.jsonl")
	events := readLogEvents(t, logFile)

	found := false
	for _, ev := range events {
		if ev["event"] == "status" && ev["msg"] == "all systems nominal" {
			found = true
			break
		}
	}
	if !found {
		t.Error("status event not found in log")
	}
}

func TestErrorfWritesErrorEvent(t *testing.T) {
	svcDir := initRegistry(t)
	pid := strconv.Itoa(os.Getpid())

	registry.Errorf("connection failed: %s", "timeout")

	logFile := filepath.Join(svcDir, pid+".log.jsonl")
	events := readLogEvents(t, logFile)

	found := false
	for _, ev := range events {
		if ev["event"] == "error" && ev["msg"] == "connection failed: timeout" {
			found = true
			break
		}
	}
	if !found {
		t.Error("error event not found in log")
	}
}

func TestShutdownRemovesPIDFileButLeavesLog(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := registry.Init(cancel, "5.0.0-test"); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	wd, _ := os.Getwd()
	name := filepath.Base(wd)
	svcDir := filepath.Join(tmp, name)
	pid := strconv.Itoa(os.Getpid())
	pidFile := filepath.Join(svcDir, pid+".json")
	logFile := filepath.Join(svcDir, pid+".log.jsonl")

	// Verify PID file exists before shutdown.
	if _, err := os.Stat(pidFile); err != nil {
		t.Fatalf("PID file missing before shutdown: %v", err)
	}

	registry.Shutdown("test-complete")

	// PID file must be removed.
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Errorf("PID file still exists after shutdown")
	}

	// Log file must remain.
	if _, err := os.Stat(logFile); err != nil {
		t.Errorf("Log file missing after shutdown: %v", err)
	}
}

func TestShutdownWritesShutdownEvent(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := registry.Init(cancel, "5.0.0-test"); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	wd, _ := os.Getwd()
	name := filepath.Base(wd)
	svcDir := filepath.Join(tmp, name)
	pid := strconv.Itoa(os.Getpid())
	logFile := filepath.Join(svcDir, pid+".log.jsonl")

	registry.Shutdown("graceful-exit")

	events := readLogEvents(t, logFile)
	found := false
	for _, ev := range events {
		if ev["event"] == "shutdown" {
			found = true
			if ev["reason"] != "graceful-exit" {
				t.Errorf("shutdown reason = %q, want %q", ev["reason"], "graceful-exit")
			}
			if ev["uptime_sec"] == nil {
				t.Error("shutdown event missing 'uptime_sec'")
			}
			break
		}
	}
	if !found {
		t.Error("shutdown event not found in log")
	}
}

func TestHandleRegistersCustomCommands(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)

	registry.Handle("reload-config", "Reload configuration files", func() error {
		return nil
	})

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := registry.Init(cancel, "5.0.0-test"); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	t.Cleanup(func() { registry.Shutdown("test-done") })

	wd, _ := os.Getwd()
	name := filepath.Base(wd)
	svcDir := filepath.Join(tmp, name)
	pid := strconv.Itoa(os.Getpid())
	pidFile := filepath.Join(svcDir, pid+".json")

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read PID file: %v", err)
	}

	var reg registry.Registration
	if err := json.Unmarshal(data, &reg); err != nil {
		t.Fatalf("parse PID JSON: %v", err)
	}

	found := false
	for _, cmd := range reg.Commands {
		if cmd.Name == "reload-config" && cmd.Description == "Reload configuration files" && !cmd.Builtin {
			found = true
			break
		}
	}
	if !found {
		t.Error("custom command 'reload-config' not found in PID file")
	}
}

func TestChassisServiceNameEnvVar(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)
	t.Setenv("CHASSIS_SERVICE_NAME", "my-custom-svc")

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := registry.Init(cancel, "5.0.0-test"); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	t.Cleanup(func() { registry.Shutdown("test-done") })

	svcDir := filepath.Join(tmp, "my-custom-svc")
	if _, err := os.Stat(svcDir); err != nil {
		t.Fatalf("service dir for custom name not found: %v", err)
	}

	pid := strconv.Itoa(os.Getpid())
	pidFile := filepath.Join(svcDir, pid+".json")
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read PID file: %v", err)
	}

	var reg registry.Registration
	if err := json.Unmarshal(data, &reg); err != nil {
		t.Fatalf("parse PID JSON: %v", err)
	}

	if reg.Name != "my-custom-svc" {
		t.Errorf("Name = %q, want %q", reg.Name, "my-custom-svc")
	}
}

func TestStaleCleanupRemovesDeadPIDFiles(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)

	wd, _ := os.Getwd()
	name := filepath.Base(wd)
	svcDir := filepath.Join(tmp, name)
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Create a stale PID file for a PID that almost certainly does not exist.
	stalePID := 99999999
	stalePath := filepath.Join(svcDir, strconv.Itoa(stalePID)+".json")
	if err := os.WriteFile(stalePath, []byte(`{"pid": 99999999}`), 0o644); err != nil {
		t.Fatalf("write stale PID file: %v", err)
	}

	// Verify it exists before Init.
	if _, err := os.Stat(stalePath); err != nil {
		t.Fatalf("stale file missing before Init: %v", err)
	}

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := registry.Init(cancel, "5.0.0-test"); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	t.Cleanup(func() { registry.Shutdown("test-done") })

	// The stale file should have been cleaned up by Init.
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Error("stale PID file was not cleaned up")
	}
}

func TestStatusNoOpBeforeInit(t *testing.T) {
	registry.ResetForTest(t.TempDir())

	// These must not panic or write anything — no Init has been called.
	registry.Status("should be ignored")
	registry.Errorf("should also be %s", "ignored")
	// If we reach here without panic, the test passes.
}

func TestDoubleShutdownIsIdempotent(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := registry.Init(cancel, "5.0.0-test"); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// First shutdown.
	registry.Shutdown("first")
	// Second shutdown must not panic.
	registry.Shutdown("second")
}

func TestRestartRequestedDefaultFalse(t *testing.T) {
	registry.ResetForTest(t.TempDir())
	if registry.RestartRequested() {
		t.Error("RestartRequested should be false after reset")
	}
}

func TestPollOnceStopCommand(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := registry.Init(cancel, "5.0.0-test"); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	t.Cleanup(func() { registry.Shutdown("test-done") })

	// Write a stop command file.
	wd, _ := os.Getwd()
	name := filepath.Base(wd)
	svcDir := filepath.Join(tmp, name)
	pid := strconv.Itoa(os.Getpid())
	cmdFile := filepath.Join(svcDir, pid+".cmd.json")

	cmd := fmt.Sprintf(`{"command":"stop","issued_at":"%s"}`, "2026-03-07T00:00:00Z")
	if err := os.WriteFile(cmdFile, []byte(cmd), 0o644); err != nil {
		t.Fatalf("write cmd file: %v", err)
	}

	// Use a very short poll interval so RunCommandPoll processes it quickly.
	// But instead, we can directly trigger pollOnce by calling RunCommandPoll
	// with a cancelled context after a tick. However, pollOnce is unexported.
	// The simplest approach: set a short interval and let RunCommandPoll run briefly.
	registry.CmdPollInterval = 1 // 1 nanosecond - minimum tick
	go registry.RunCommandPoll(ctx)

	// Wait for context to be cancelled (stop command triggers cancel).
	<-ctx.Done()

	// Verify the command file was removed.
	if _, err := os.Stat(cmdFile); !os.IsNotExist(err) {
		t.Error("command file was not removed after processing")
	}
}

func TestPollOnceRestartCommand(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := registry.Init(cancel, "5.0.0-test"); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	t.Cleanup(func() { registry.Shutdown("test-done") })

	wd, _ := os.Getwd()
	name := filepath.Base(wd)
	svcDir := filepath.Join(tmp, name)
	pid := strconv.Itoa(os.Getpid())
	cmdFile := filepath.Join(svcDir, pid+".cmd.json")

	cmd := fmt.Sprintf(`{"command":"restart","issued_at":"%s"}`, "2026-03-07T00:00:00Z")
	if err := os.WriteFile(cmdFile, []byte(cmd), 0o644); err != nil {
		t.Fatalf("write cmd file: %v", err)
	}

	registry.CmdPollInterval = 1
	go registry.RunCommandPoll(ctx)

	<-ctx.Done()

	if !registry.RestartRequested() {
		t.Error("RestartRequested should be true after restart command")
	}
}

// --- helpers ---

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
