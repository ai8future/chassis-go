// Package registry provides file-based service registration at /tmp/chassis/.
// It uses module-level state so that Status(), Errorf(), and Handle() work as
// package-level functions without passing an object around.
//
// The registry is initialised by lifecycle.Run(), but the module itself is
// self-contained with zero chassis dependencies — it uses only the stdlib.
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	DefaultBasePath          = "/tmp/chassis"
	DefaultHeartbeatInterval = 30 * time.Second
	DefaultCmdPollInterval   = 3 * time.Second
)

// Registration is the JSON structure written to the PID file.
type Registration struct {
	Name           string    `json:"name"`
	PID            int       `json:"pid"`
	Hostname       string    `json:"hostname"`
	StartedAt      string    `json:"started_at"`
	Version        string    `json:"version"`
	Language       string    `json:"language"`
	ChassisVersion string    `json:"chassis_version"`
	Args           []string  `json:"args"`
	Commands       []CmdInfo `json:"commands"`
}

// CmdInfo describes a command that can be sent to the service.
type CmdInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Builtin     bool   `json:"builtin,omitempty"`
}

type cmdRequest struct {
	Command  string `json:"command"`
	IssuedAt string `json:"issued_at"`
}

type handlerEntry struct {
	description string
	fn          func() error
}

var (
	mu          sync.Mutex
	active      atomic.Bool
	logFile     *os.File
	reg         *Registration
	svcDir      string
	pidPath     string
	logFilePath string
	cmdPath     string
	handlers    = map[string]handlerEntry{}
	startedAt   time.Time
	cancelFn    context.CancelFunc
	restart     atomic.Bool

	// BasePath is the root directory for service registrations.
	BasePath = DefaultBasePath
	// HeartbeatInterval controls how often heartbeat events are logged.
	HeartbeatInterval = DefaultHeartbeatInterval
	// CmdPollInterval controls how often the command file is polled.
	CmdPollInterval = DefaultCmdPollInterval
)

// Handle registers a custom command that can be issued to this service.
// Must be called before Init.
func Handle(name, description string, fn func() error) {
	mu.Lock()
	defer mu.Unlock()
	handlers[name] = handlerEntry{description: description, fn: fn}
}

// Status writes a status event to the service log.
func Status(msg string) {
	if !active.Load() {
		return
	}
	appendLog(map[string]any{"ts": ts(), "event": "status", "msg": msg})
}

// Errorf writes an error event to the service log.
func Errorf(format string, args ...any) {
	if !active.Load() {
		return
	}
	appendLog(map[string]any{"ts": ts(), "event": "error", "msg": fmt.Sprintf(format, args...)})
}

// RestartRequested returns true if a restart command was received.
func RestartRequested() bool {
	return restart.Load()
}

// Init initialises the registry, writing the PID file and creating the log.
// The chassisVersion parameter is the version of the chassis framework.
func Init(cancel context.CancelFunc, chassisVersion string) error {
	mu.Lock()
	defer mu.Unlock()

	startedAt = time.Now().UTC()
	cancelFn = cancel
	pid := os.Getpid()
	name := resolveName()
	host, _ := os.Hostname()
	ver := readVersion()

	svcDir = filepath.Join(BasePath, name)
	if err := os.MkdirAll(svcDir, 0o700); err != nil {
		return fmt.Errorf("registry: mkdir: %w", err)
	}
	cleanStale(svcDir)

	ps := strconv.Itoa(pid)
	pidPath = filepath.Join(svcDir, ps+".json")
	logFilePath = filepath.Join(svcDir, ps+".log.jsonl")
	cmdPath = filepath.Join(svcDir, ps+".cmd.json")

	cmds := []CmdInfo{
		{Name: "stop", Description: "Graceful shutdown", Builtin: true},
		{Name: "restart", Description: "Restart with same arguments", Builtin: true},
	}
	for n, h := range handlers {
		cmds = append(cmds, CmdInfo{Name: n, Description: h.description})
	}

	reg = &Registration{
		Name:           name,
		PID:            pid,
		Hostname:       host,
		StartedAt:      startedAt.Format(time.RFC3339),
		Version:        ver,
		Language:        "go",
		ChassisVersion: chassisVersion,
		Args:           os.Args,
		Commands:       cmds,
	}

	if err := atomicWrite(pidPath, reg); err != nil {
		return fmt.Errorf("registry: write registration: %w", err)
	}

	var err error
	logFile, err = os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("registry: create log: %w", err)
	}

	active.Store(true)

	appendLogLocked(map[string]any{
		"ts": startedAt.Format(time.RFC3339), "event": "startup",
		"name": name, "pid": pid, "hostname": host, "version": ver,
	})

	return nil
}

// RunHeartbeat periodically logs heartbeat events until ctx is cancelled.
func RunHeartbeat(ctx context.Context) error {
	t := time.NewTicker(HeartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			appendLog(map[string]any{"ts": ts(), "event": "heartbeat"})
		}
	}
}

// RunCommandPoll periodically checks for command files and executes them.
func RunCommandPoll(ctx context.Context) error {
	t := time.NewTicker(CmdPollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			pollOnce()
		}
	}
}

// Shutdown cleans up the registry, writing a shutdown event and removing the PID file.
func Shutdown(reason string) {
	mu.Lock()
	defer mu.Unlock()
	if !active.Load() {
		return
	}
	active.Store(false)
	up := int(time.Since(startedAt).Seconds())
	appendLogLocked(map[string]any{
		"ts": ts(), "event": "shutdown", "reason": reason, "uptime_sec": up,
	})
	if logFile != nil {
		logFile.Close()
		logFile = nil
	}
	os.Remove(pidPath)
}

// ResetForTest resets all module-level state. Only for use in tests.
func ResetForTest(path string) {
	mu.Lock()
	defer mu.Unlock()
	active.Store(false)
	restart.Store(false)
	if logFile != nil {
		logFile.Close()
		logFile = nil
	}
	BasePath = path
	HeartbeatInterval = DefaultHeartbeatInterval
	CmdPollInterval = DefaultCmdPollInterval
	handlers = map[string]handlerEntry{}
	reg = nil
	cancelFn = nil
}

// --- internal helpers ---

func resolveName() string {
	if n := os.Getenv("CHASSIS_SERVICE_NAME"); n != "" {
		return n
	}
	wd, err := os.Getwd()
	if err != nil {
		return "unknown"
	}
	return filepath.Base(wd)
}

func readVersion() string {
	b, err := os.ReadFile("VERSION")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(b))
}

func ts() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func atomicWrite(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".chassis-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // clean up on error; no-op after successful Rename
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func appendLog(entry map[string]any) {
	mu.Lock()
	defer mu.Unlock()
	appendLogLocked(entry)
}

func appendLogLocked(entry map[string]any) {
	if logFile == nil {
		return
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	logFile.Write(append(data, '\n'))
}

func cleanStale(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".cmd.json") || strings.HasSuffix(name, ".tmp") {
			continue
		}
		pidStr := strings.TrimSuffix(name, ".json")
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		if processAlive(pid) {
			continue
		}
		// Remove all files for this dead PID.
		ps := strconv.Itoa(pid)
		os.Remove(filepath.Join(dir, ps+".json"))
		os.Remove(filepath.Join(dir, ps+".log.jsonl"))
		os.Remove(filepath.Join(dir, ps+".cmd.json"))
	}
}

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func pollOnce() {
	data, err := os.ReadFile(cmdPath)
	if err != nil {
		return
	}
	os.Remove(cmdPath)

	var req cmdRequest
	if json.Unmarshal(data, &req) != nil {
		return
	}

	switch req.Command {
	case "stop":
		appendLog(map[string]any{"ts": ts(), "event": "command", "name": "stop", "result": "ok"})
		mu.Lock()
		fn := cancelFn
		mu.Unlock()
		if fn != nil {
			fn()
		}
	case "restart":
		appendLog(map[string]any{"ts": ts(), "event": "command", "name": "restart", "result": "ok"})
		restart.Store(true)
		mu.Lock()
		fn := cancelFn
		mu.Unlock()
		if fn != nil {
			fn()
		}
	default:
		mu.Lock()
		h, ok := handlers[req.Command]
		mu.Unlock()
		if !ok {
			appendLog(map[string]any{"ts": ts(), "event": "command", "name": req.Command, "result": "error", "detail": "unknown command"})
			return
		}
		if err := h.fn(); err != nil {
			appendLog(map[string]any{"ts": ts(), "event": "command", "name": req.Command, "result": "error", "detail": err.Error()})
		} else {
			appendLog(map[string]any{"ts": ts(), "event": "command", "name": req.Command, "result": "ok"})
		}
	}
}
