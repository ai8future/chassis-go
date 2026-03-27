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
	Name           string            `json:"name"`
	PID            int               `json:"pid"`
	Hostname       string            `json:"hostname"`
	StartedAt      string            `json:"started_at"`
	Version        string            `json:"version"`
	Language       string            `json:"language"`
	ChassisVersion string            `json:"chassis_version"`
	BasePort       int               `json:"base_port"`
	Args           []string          `json:"args"`
	Ports          []PortInfo        `json:"ports"`
	Commands       []CmdInfo         `json:"commands"`
	Mode           string            `json:"mode"`
	Flags          map[string]string `json:"flags,omitempty"`
	Status         string            `json:"status"`
	ExitedAt       string            `json:"exited_at,omitempty"`
	ExitCode       *int              `json:"exit_code,omitempty"`
	Summary        *ProgressSummary  `json:"summary,omitempty"`
}

// ProgressSummary holds progress tracking state for CLI/batch processes.
type ProgressSummary struct {
	Done    int     `json:"done"`
	Total   int     `json:"total"`
	Failed  int     `json:"failed"`
	Percent float64 `json:"percent"`
}

// PortInfo describes a port declared by the service.
type PortInfo struct {
	Port  int    `json:"port"`
	Role  string `json:"role"`
	Proto string `json:"proto"`
	Label string `json:"label"`
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
	mu           sync.Mutex
	active       atomic.Bool
	logFile      *os.File
	reg          *Registration
	svcDir       string
	pidPath      string
	logFilePath  string
	cmdPath      string
	handlers     = map[string]handlerEntry{}
	ports        []PortInfo
	startedAt    time.Time
	cancelFn     context.CancelFunc
	restart      atomic.Bool
	stopRequested atomic.Bool
	lastProgress *ProgressSummary
	cliMode      bool
	cliDone      chan struct{}

	// BasePath is the root directory for service registrations.
	// Set before calling lifecycle.Run; not safe for concurrent modification.
	BasePath = DefaultBasePath
	// HeartbeatInterval controls how often heartbeat events are logged.
	// Must be a time.Duration (e.g. 30*time.Second, not bare 30).
	// Set before calling lifecycle.Run; not safe for concurrent modification.
	HeartbeatInterval = DefaultHeartbeatInterval
	// CmdPollInterval controls how often the command file is polled.
	// Must be a time.Duration (e.g. 3*time.Second, not bare 3).
	// Set before calling lifecycle.Run; not safe for concurrent modification.
	CmdPollInterval = DefaultCmdPollInterval
)

// Handle registers a custom command that can be issued to this service.
// Must be called before Init.
func Handle(name, description string, fn func() error) {
	mu.Lock()
	defer mu.Unlock()
	handlers[name] = handlerEntry{description: description, fn: fn}
}

// roleNames maps standard role offsets to their string names for JSON output.
var roleNames = map[int]string{0: "http", 1: "grpc", 2: "metrics"}

// defaultProtos maps role names to their default wire protocol.
var defaultProtos = map[string]string{"http": "http", "grpc": "h2c", "metrics": "http"}

// PortOption configures optional parameters for Port declarations.
type PortOption func(*PortInfo)

// Proto overrides the default protocol for a port declaration.
func Proto(proto string) PortOption {
	return func(p *PortInfo) { p.Proto = proto }
}

// Port declares a port that this service has opened. Call before lifecycle.Run().
// The role parameter is a standard offset (chassis.PortHTTP, chassis.PortGRPC,
// chassis.PortMetrics) or a custom integer (3+). The label is freeform text
// describing the port's purpose.
func Port(role int, port int, label string, opts ...PortOption) {
	mu.Lock()
	defer mu.Unlock()

	roleName, ok := roleNames[role]
	if !ok {
		roleName = fmt.Sprintf("custom-%d", role)
	}

	proto := defaultProtos[roleName]
	if proto == "" {
		proto = "http"
	}

	info := PortInfo{Port: port, Role: roleName, Proto: proto, Label: label}
	for _, opt := range opts {
		opt(&info)
	}
	ports = append(ports, info)
}

// AssertActive crashes the process if the registry has not been initialized.
// Post-lifecycle chassis modules call this to enforce mandatory registration.
func AssertActive() {
	if !active.Load() {
		fmt.Fprintf(os.Stderr,
			"FATAL: Registry not initialized. lifecycle.Run() must be called before using chassis service modules.\n"+
				"Every chassis service must use lifecycle.Run() — registry is mandatory, not optional.\n")
		os.Exit(1)
	}
}

// Status writes a status event to the service log.
func Status(msg string) {
	AssertActive()
	appendLog(map[string]any{"ts": ts(), "event": "status", "msg": msg})
}

// Errorf writes an error event to the service log.
func Errorf(format string, args ...any) {
	AssertActive()
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
	// Verify the directory has safe permissions. On shared systems (/tmp),
	// another user could pre-create the directory with open permissions.
	if info, err := os.Stat(svcDir); err == nil {
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			return fmt.Errorf("registry: directory %s has unsafe permissions %o (want 0700)", svcDir, perm)
		}
	}
	killPreviousInstances(svcDir, pid)
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

	declaredPorts := ports
	if declaredPorts == nil {
		declaredPorts = []PortInfo{}
	}

	reg = &Registration{
		Name:           name,
		PID:            pid,
		Hostname:       host,
		StartedAt:      startedAt.Format(time.RFC3339),
		Version:        ver,
		Language:        "go",
		ChassisVersion: chassisVersion,
		BasePort:       djb2Port(name),
		Args:           redactArgs(os.Args),
		Ports:          declaredPorts,
		Commands:       cmds,
		Mode:           "service",
		Status:         "running",
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

// InitCLI initializes the registry in CLI/batch mode.
// It parses flags from os.Args, writes a PID file with mode "cli",
// and starts command polling (but no heartbeat).
func InitCLI(chassisVersion string) error {
	mu.Lock()
	defer mu.Unlock()

	startedAt = time.Now().UTC()
	cliMode = true
	pid := os.Getpid()
	name := resolveName()
	host, _ := os.Hostname()
	ver := readVersion()

	svcDir = filepath.Join(BasePath, name)
	if err := os.MkdirAll(svcDir, 0o700); err != nil {
		return fmt.Errorf("registry: mkdir: %w", err)
	}
	if info, err := os.Stat(svcDir); err == nil {
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			return fmt.Errorf("registry: directory %s has unsafe permissions %o (want 0700)", svcDir, perm)
		}
	}
	killPreviousInstances(svcDir, pid)
	cleanStale(svcDir)

	ps := strconv.Itoa(pid)
	pidPath = filepath.Join(svcDir, ps+".json")
	logFilePath = filepath.Join(svcDir, ps+".log.jsonl")
	cmdPath = filepath.Join(svcDir, ps+".cmd.json")

	cmds := []CmdInfo{
		{Name: "stop", Description: "Graceful shutdown", Builtin: true},
	}
	for n, h := range handlers {
		cmds = append(cmds, CmdInfo{Name: n, Description: h.description})
	}

	declaredPorts := ports
	if declaredPorts == nil {
		declaredPorts = []PortInfo{}
	}

	flags := parseFlags(os.Args)

	reg = &Registration{
		Name:           name,
		PID:            pid,
		Hostname:       host,
		StartedAt:      startedAt.Format(time.RFC3339),
		Version:        ver,
		Language:       "go",
		ChassisVersion: chassisVersion,
		BasePort:       djb2Port(name),
		Args:           redactArgs(os.Args),
		Ports:          declaredPorts,
		Commands:       cmds,
		Mode:           "cli",
		Flags:          flags,
		Status:         "running",
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
		"mode": "cli",
	})

	// Start command polling goroutine for stop support (no heartbeat in CLI mode).
	cliDone = make(chan struct{})
	go func() {
		t := time.NewTicker(CmdPollInterval)
		defer t.Stop()
		for {
			select {
			case <-cliDone:
				return
			case <-t.C:
				if !active.Load() {
					return
				}
				pollOnce()
			}
		}
	}()

	return nil
}

// parseFlags parses command-line arguments into a map of flag names to values.
// It handles --flag=value, --flag value, -flag value, --flag (boolean), and -f (boolean) forms.
// Sensitive flags are redacted.
func parseFlags(args []string) map[string]string {
	flags := map[string]string{}
	if len(args) <= 1 {
		return flags
	}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break // stop parsing flags after --
		}
		if !strings.HasPrefix(arg, "-") {
			continue
		}

		// Handle --flag=value and -flag=value
		if idx := strings.Index(arg, "="); idx > 0 {
			name := strings.TrimLeft(arg[:idx], "-")
			value := arg[idx+1:]
			if isSensitiveFlag(name) {
				value = "REDACTED"
			}
			flags[name] = value
			continue
		}

		// Strip leading dashes to get the flag name
		name := strings.TrimLeft(arg, "-")
		if name == "" {
			continue
		}

		// Check if next arg is a value (not another flag) and exists
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			value := args[i+1]
			if isSensitiveFlag(name) {
				value = "REDACTED"
			}
			flags[name] = value
			i++ // skip next arg since we consumed it as a value
		} else {
			// Boolean flag (no value)
			flags[name] = "true"
		}
	}
	return flags
}

// isSensitiveFlag checks if a flag name matches any sensitive flag pattern.
func isSensitiveFlag(name string) bool {
	lower := strings.ToLower(name)
	for _, s := range sensitiveFlags {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// Progress writes a progress event to the service log.
func Progress(done, total, failed int) {
	AssertActive()
	pct := 0.0
	if total > 0 {
		pct = float64(done) / float64(total) * 100
	}
	mu.Lock()
	lastProgress = &ProgressSummary{Done: done, Total: total, Failed: failed, Percent: pct}
	mu.Unlock()
	appendLog(map[string]any{
		"ts": ts(), "event": "progress",
		"done": done, "total": total, "failed": failed, "percent": pct,
	})
}

// StopRequested returns true if a stop command was received (CLI mode).
func StopRequested() bool {
	return stopRequested.Load()
}

// ShutdownCLI writes completion status and rewrites the PID file with final state.
// Unlike service Shutdown which deletes the PID file, CLI mode keeps it for the viewer.
func ShutdownCLI(exitCode int) {
	mu.Lock()
	defer mu.Unlock()
	if !active.Load() {
		return
	}
	active.Store(false)
	if cliDone != nil {
		close(cliDone)
		cliDone = nil
	}

	status := "completed"
	if exitCode != 0 {
		status = "failed"
	}

	up := int(time.Since(startedAt).Seconds())
	appendLogLocked(map[string]any{
		"ts": ts(), "event": "shutdown",
		"status": status, "exit_code": exitCode, "uptime_sec": up,
	})

	// Rewrite PID file with completion status (don't delete it).
	if reg != nil {
		reg.Status = status
		reg.ExitedAt = ts()
		ec := exitCode
		reg.ExitCode = &ec
		reg.Summary = lastProgress
		atomicWrite(pidPath, reg)
	}

	if logFile != nil {
		logFile.Close()
		logFile = nil
	}
}

// ResetForTest resets all module-level state. Only for use in tests.
func ResetForTest(path string) {
	mu.Lock()
	defer mu.Unlock()
	active.Store(false)
	restart.Store(false)
	stopRequested.Store(false)
	if cliDone != nil {
		close(cliDone)
		cliDone = nil
	}
	lastProgress = nil
	cliMode = false
	if logFile != nil {
		logFile.Close()
		logFile = nil
	}
	BasePath = path
	HeartbeatInterval = DefaultHeartbeatInterval
	CmdPollInterval = DefaultCmdPollInterval
	handlers = map[string]handlerEntry{}
	ports = nil
	reg = nil
	cancelFn = nil
}

// djb2Port computes the deterministic base port for a service name.
// Mirrors chassis.Port() but avoids importing the root package.
func djb2Port(name string) int {
	var h uint32 = 5381
	for i := 0; i < len(name); i++ {
		h = h*33 + uint32(name[i])
	}
	return 5000 + int(h%43001)
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

// sensitiveFlags lists flag prefixes whose values should be redacted from the PID file.
var sensitiveFlags = []string{
	"password", "passwd", "secret", "token", "key", "credential",
	"api-key", "api_key", "apikey", "auth",
}

// redactArgs returns a copy of args with values of sensitive-looking flags replaced.
func redactArgs(args []string) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		out[i] = redactArg(arg)
	}
	return out
}

func redactArg(arg string) string {
	lower := strings.ToLower(arg)
	// Handle --flag=value and -flag=value
	if idx := strings.Index(arg, "="); idx > 0 {
		name := strings.TrimLeft(lower[:idx], "-")
		for _, s := range sensitiveFlags {
			if strings.Contains(name, s) {
				return arg[:idx+1] + "REDACTED"
			}
		}
	}
	return arg
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

// killPreviousInstances sends SIGTERM to any running instances of the same
// service, waits up to 3 seconds for graceful shutdown, then sends SIGKILL.
// This prevents port conflicts and duplicate daemons on restart.
func killPreviousInstances(dir string, myPID int) {
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
		if err != nil || pid == myPID {
			continue
		}
		if !processAlive(pid) {
			continue
		}
		p, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		fmt.Fprintf(os.Stderr, "registry: killing stale instance (PID %d)\n", pid)
		_ = p.Signal(syscall.SIGTERM)
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if !processAlive(pid) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if processAlive(pid) {
			_ = p.Signal(syscall.SIGKILL)
			time.Sleep(100 * time.Millisecond)
		}
	}
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

		// For dead PIDs, check if the PID file has a terminal status (completed/failed).
		// If so, only remove if exited_at is older than 24 hours.
		pidFile := filepath.Join(dir, name)
		if shouldPreservePIDFile(pidFile) {
			continue
		}

		// Remove all files for this dead PID.
		ps := strconv.Itoa(pid)
		os.Remove(filepath.Join(dir, ps+".json"))
		os.Remove(filepath.Join(dir, ps+".log.jsonl"))
		os.Remove(filepath.Join(dir, ps+".cmd.json"))
	}
}

// shouldPreservePIDFile returns true if the PID file has a terminal status
// (completed or failed) with an exited_at timestamp less than 24 hours old.
func shouldPreservePIDFile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var info struct {
		Status   string `json:"status"`
		ExitedAt string `json:"exited_at"`
	}
	if json.Unmarshal(data, &info) != nil {
		return false
	}
	if info.Status != "completed" && info.Status != "failed" {
		return false
	}
	if info.ExitedAt == "" {
		return false
	}
	exitedAt, err := time.Parse(time.RFC3339, info.ExitedAt)
	if err != nil {
		return false
	}
	return time.Since(exitedAt) < 24*time.Hour
}

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func pollOnce() {
	mu.Lock()
	path := cmdPath
	mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	os.Remove(path)

	var req cmdRequest
	if json.Unmarshal(data, &req) != nil {
		return
	}

	switch req.Command {
	case "stop":
		appendLog(map[string]any{"ts": ts(), "event": "command", "name": "stop", "result": "ok"})
		mu.Lock()
		cli := cliMode
		fn := cancelFn
		mu.Unlock()
		if cli {
			stopRequested.Store(true)
		} else if fn != nil {
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
