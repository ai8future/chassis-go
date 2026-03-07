package registry_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v7"
	"github.com/ai8future/chassis-go/v7/registry"
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
	if err := os.MkdirAll(svcDir, 0o700); err != nil {
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

func TestStatusCrashesBeforeInit(t *testing.T) {
	if os.Getenv("TEST_CRASH_STATUS") == "1" {
		registry.ResetForTest(t.TempDir())
		registry.Status("should crash")
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestStatusCrashesBeforeInit")
	cmd.Env = append(os.Environ(), "TEST_CRASH_STATUS=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return // expected crash
	}
	t.Fatal("expected Status() to crash before Init, but it did not")
}

func TestErrorfCrashesBeforeInit(t *testing.T) {
	if os.Getenv("TEST_CRASH_ERRORF") == "1" {
		registry.ResetForTest(t.TempDir())
		registry.Errorf("should also %s", "crash")
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestErrorfCrashesBeforeInit")
	cmd.Env = append(os.Environ(), "TEST_CRASH_ERRORF=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return // expected crash
	}
	t.Fatal("expected Errorf() to crash before Init, but it did not")
}

func TestAssertActiveCrashesBeforeInit(t *testing.T) {
	if os.Getenv("TEST_CRASH_ASSERT") == "1" {
		registry.ResetForTest(t.TempDir())
		registry.AssertActive()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestAssertActiveCrashesBeforeInit")
	cmd.Env = append(os.Environ(), "TEST_CRASH_ASSERT=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return // expected crash
	}
	t.Fatal("expected AssertActive() to crash before Init, but it did not")
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

func TestPortDeclarationAppearsInPIDFile(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)

	registry.Port(0, 8080, "REST API")
	registry.Port(1, 8081, "gRPC API")
	registry.Port(2, 8082, "Prometheus metrics")

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := registry.Init(cancel, "6.0.0-test"); err != nil {
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

	if len(reg.Ports) != 3 {
		t.Fatalf("expected 3 ports, got %d", len(reg.Ports))
	}

	expected := []struct {
		port  int
		role  string
		proto string
		label string
	}{
		{8080, "http", "http", "REST API"},
		{8081, "grpc", "h2c", "gRPC API"},
		{8082, "metrics", "http", "Prometheus metrics"},
	}

	for i, exp := range expected {
		p := reg.Ports[i]
		if p.Port != exp.port || p.Role != exp.role || p.Proto != exp.proto || p.Label != exp.label {
			t.Errorf("port[%d] = %+v, want {port:%d role:%s proto:%s label:%s}",
				i, p, exp.port, exp.role, exp.proto, exp.label)
		}
	}
}

func TestPortProtoOverride(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)

	registry.Port(0, 443, "HTTPS API", registry.Proto("https"))

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := registry.Init(cancel, "6.0.0-test"); err != nil {
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

	if len(reg.Ports) != 1 {
		t.Fatalf("expected 1 port, got %d", len(reg.Ports))
	}
	if reg.Ports[0].Proto != "https" {
		t.Errorf("Proto = %q, want %q", reg.Ports[0].Proto, "https")
	}
}

func TestBasePortComputedFromName(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)
	t.Setenv("CHASSIS_SERVICE_NAME", "test_svc")

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := registry.Init(cancel, "6.0.0-test"); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	t.Cleanup(func() { registry.Shutdown("test-done") })

	svcDir := filepath.Join(tmp, "test_svc")
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

	// BasePort must match chassis.Port() for the same name.
	expected := chassis.Port("test_svc")
	if reg.BasePort != expected {
		t.Errorf("BasePort = %d, want %d (from chassis.Port)", reg.BasePort, expected)
	}
}

func TestCustomRolePort(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)

	registry.Port(3, 9000, "worker", registry.Proto("tcp"))

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := registry.Init(cancel, "6.0.0-test"); err != nil {
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

	if len(reg.Ports) != 1 {
		t.Fatalf("expected 1 port, got %d", len(reg.Ports))
	}
	p := reg.Ports[0]
	if p.Role != "custom-3" {
		t.Errorf("Role = %q, want %q", p.Role, "custom-3")
	}
	if p.Proto != "tcp" {
		t.Errorf("Proto = %q, want %q", p.Proto, "tcp")
	}
	if p.Label != "worker" {
		t.Errorf("Label = %q, want %q", p.Label, "worker")
	}
}

func TestNoPortsEmptySlice(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := registry.Init(cancel, "6.0.0-test"); err != nil {
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

	// Verify "ports" serializes as [] (empty array), not null.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse PID JSON: %v", err)
	}
	if _, ok := raw["base_port"]; !ok {
		t.Error("'base_port' key missing from PID JSON")
	}
	portsJSON, ok := raw["ports"]
	if !ok {
		t.Fatal("'ports' key missing from PID JSON")
	}
	if string(portsJSON) == "null" {
		t.Error("'ports' should be [] not null when no ports declared")
	}
}

func TestInitCLI(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)

	if err := registry.InitCLI("7.0.0-test"); err != nil {
		t.Fatalf("InitCLI failed: %v", err)
	}
	t.Cleanup(func() { registry.ShutdownCLI(0) })

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

	if reg.Mode != "cli" {
		t.Errorf("Mode = %q, want %q", reg.Mode, "cli")
	}
	if reg.Status != "running" {
		t.Errorf("Status = %q, want %q", reg.Status, "running")
	}
	if reg.ChassisVersion != "7.0.0-test" {
		t.Errorf("ChassisVersion = %q, want %q", reg.ChassisVersion, "7.0.0-test")
	}
	if reg.Language != "go" {
		t.Errorf("Language = %q, want %q", reg.Language, "go")
	}
	// Flags should be parsed from os.Args (at minimum it should be a map, possibly empty)
	if reg.Flags == nil {
		t.Error("Flags is nil, expected non-nil map")
	}
}

func TestInitServiceMode(t *testing.T) {
	svcDir := initRegistry(t)
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

	if reg.Mode != "service" {
		t.Errorf("Mode = %q, want %q", reg.Mode, "service")
	}
	if reg.Status != "running" {
		t.Errorf("Status = %q, want %q", reg.Status, "running")
	}
}

func TestProgress(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)

	if err := registry.InitCLI("7.0.0-test"); err != nil {
		t.Fatalf("InitCLI failed: %v", err)
	}
	t.Cleanup(func() { registry.ShutdownCLI(0) })

	registry.Progress(50, 100, 3)

	wd, _ := os.Getwd()
	name := filepath.Base(wd)
	svcDir := filepath.Join(tmp, name)
	pid := strconv.Itoa(os.Getpid())
	logFile := filepath.Join(svcDir, pid+".log.jsonl")

	events := readLogEvents(t, logFile)
	found := false
	for _, ev := range events {
		if ev["event"] == "progress" {
			found = true
			if ev["done"] != float64(50) {
				t.Errorf("done = %v, want 50", ev["done"])
			}
			if ev["total"] != float64(100) {
				t.Errorf("total = %v, want 100", ev["total"])
			}
			if ev["failed"] != float64(3) {
				t.Errorf("failed = %v, want 3", ev["failed"])
			}
			if ev["percent"] != float64(50) {
				t.Errorf("percent = %v, want 50", ev["percent"])
			}
			break
		}
	}
	if !found {
		t.Error("progress event not found in log")
	}
}

func TestShutdownCLI(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)

	if err := registry.InitCLI("7.0.0-test"); err != nil {
		t.Fatalf("InitCLI failed: %v", err)
	}

	// Record some progress before shutdown
	registry.Progress(10, 20, 1)

	wd, _ := os.Getwd()
	name := filepath.Base(wd)
	svcDir := filepath.Join(tmp, name)
	pid := strconv.Itoa(os.Getpid())
	pidFile := filepath.Join(svcDir, pid+".json")

	// Verify PID file exists before shutdown.
	if _, err := os.Stat(pidFile); err != nil {
		t.Fatalf("PID file missing before ShutdownCLI: %v", err)
	}

	registry.ShutdownCLI(0)

	// PID file must STILL exist after ShutdownCLI (not deleted like service mode).
	if _, err := os.Stat(pidFile); err != nil {
		t.Fatalf("PID file should persist after ShutdownCLI: %v", err)
	}

	// Read the PID file and verify completion status.
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read PID file: %v", err)
	}

	var reg registry.Registration
	if err := json.Unmarshal(data, &reg); err != nil {
		t.Fatalf("parse PID JSON: %v", err)
	}

	if reg.Status != "completed" {
		t.Errorf("Status = %q, want %q", reg.Status, "completed")
	}
	if reg.ExitedAt == "" {
		t.Error("ExitedAt is empty")
	}
	if reg.ExitCode == nil {
		t.Fatal("ExitCode is nil")
	}
	if *reg.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", *reg.ExitCode)
	}
	if reg.Summary == nil {
		t.Fatal("Summary is nil")
	}
	if reg.Summary.Done != 10 {
		t.Errorf("Summary.Done = %d, want 10", reg.Summary.Done)
	}
	if reg.Summary.Total != 20 {
		t.Errorf("Summary.Total = %d, want 20", reg.Summary.Total)
	}
	if reg.Summary.Failed != 1 {
		t.Errorf("Summary.Failed = %d, want 1", reg.Summary.Failed)
	}
}

func TestShutdownCLIFailed(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)

	if err := registry.InitCLI("7.0.0-test"); err != nil {
		t.Fatalf("InitCLI failed: %v", err)
	}

	registry.ShutdownCLI(1)

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

	if reg.Status != "failed" {
		t.Errorf("Status = %q, want %q", reg.Status, "failed")
	}
	if reg.ExitCode == nil || *reg.ExitCode != 1 {
		t.Errorf("ExitCode = %v, want 1", reg.ExitCode)
	}
}

func TestStopRequested(t *testing.T) {
	tmp := t.TempDir()
	registry.ResetForTest(tmp)

	// StopRequested should be false initially.
	if registry.StopRequested() {
		t.Error("StopRequested should be false after reset")
	}

	if err := registry.InitCLI("7.0.0-test"); err != nil {
		t.Fatalf("InitCLI failed: %v", err)
	}
	t.Cleanup(func() { registry.ShutdownCLI(0) })

	// Write a stop command file.
	wd, _ := os.Getwd()
	name := filepath.Base(wd)
	svcDir := filepath.Join(tmp, name)
	pid := strconv.Itoa(os.Getpid())
	cmdFile := filepath.Join(svcDir, pid+".cmd.json")

	cmdJSON := fmt.Sprintf(`{"command":"stop","issued_at":"%s"}`, "2026-03-07T00:00:00Z")
	if err := os.WriteFile(cmdFile, []byte(cmdJSON), 0o644); err != nil {
		t.Fatalf("write cmd file: %v", err)
	}

	// In CLI mode, the background poll goroutine started by InitCLI should pick it up.
	// Wait briefly for the poll to process the command.
	registry.CmdPollInterval = 1 // 1 nanosecond
	// The internal poll goroutine uses the CmdPollInterval set at InitCLI time.
	// Since we can't easily control the internal goroutine's timing, let's just
	// use RunCommandPoll briefly to ensure the command is processed.
	ctx, cancel := context.WithCancel(context.Background())
	go registry.RunCommandPoll(ctx)

	// Poll until StopRequested becomes true or timeout.
	deadline := time.After(2 * time.Second)
	for {
		if registry.StopRequested() {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatal("StopRequested did not become true within timeout")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
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
