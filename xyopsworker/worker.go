// Package xyopsworker lets a chassis service receive and execute jobs
// dispatched by xyops, behaving as a satellite server.
package xyopsworker

import (
	"context"
	"fmt"
	"os"
	"sync"

	chassis "github.com/ai8future/chassis-go/v9"
)

// Config holds the environment-driven settings for an xyops worker.
type Config struct {
	MasterURL    string `env:"XYOPS_WORKER_MASTER_URL" required:"true"`
	SecretKey    string `env:"XYOPS_WORKER_SECRET_KEY" required:"true"`
	Hostname     string `env:"XYOPS_WORKER_HOSTNAME"`
	Groups       string `env:"XYOPS_WORKER_GROUPS"`
	ShellEnabled bool   `env:"XYOPS_WORKER_SHELL_ENABLED" default:"false"`
}

// Job represents a job received from xyops.
type Job struct {
	ID      string
	EventID string
	Params  map[string]string

	progress func(pct int, msg string)
	log      func(msg string)
	output   func(data string)
}

// Progress reports progress back to xyops.
func (j *Job) Progress(pct int, message string) {
	if j.progress != nil {
		j.progress(pct, message)
	}
}

// Log appends a message to the live job log.
func (j *Job) Log(message string) {
	if j.log != nil {
		j.log(message)
	}
}

// SetOutput sets the job result data.
func (j *Job) SetOutput(data string) {
	if j.output != nil {
		j.output(data)
	}
}

// HandlerFunc is the signature for job handlers.
type HandlerFunc func(ctx context.Context, job Job) error

// Worker manages a WebSocket connection to xyops and dispatches jobs.
type Worker struct {
	config   Config
	handlers map[string]HandlerFunc
	mu       sync.RWMutex
	hostname string
}

// New creates a new xyops worker.
func New(cfg Config) *Worker {
	chassis.AssertVersionChecked()
	hostname := cfg.Hostname
	if hostname == "" {
		hostname, _ = os.Hostname()
	}
	return &Worker{
		config:   cfg,
		handlers: make(map[string]HandlerFunc),
		hostname: hostname,
	}
}

// Handle registers a handler for the given command name.
func (w *Worker) Handle(command string, fn HandlerFunc) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.handlers[command] = fn
}

// HasHandler checks if a handler is registered for the command.
func (w *Worker) HasHandler(command string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	_, ok := w.handlers[command]
	return ok
}

// Dispatch executes a job using the registered handler.
// Returns an error if no handler matches and shell is not enabled.
func (w *Worker) Dispatch(ctx context.Context, job Job) error {
	w.mu.RLock()
	handler, ok := w.handlers[job.EventID]
	w.mu.RUnlock()

	if ok {
		return handler(ctx, job)
	}

	if w.config.ShellEnabled {
		return fmt.Errorf("xyopsworker: shell execution not yet implemented for %q", job.EventID)
	}

	return fmt.Errorf("xyopsworker: no handler registered for %q and shell disabled", job.EventID)
}

// Run is a lifecycle component that connects to xyops master via WebSocket.
// This is a placeholder — actual WebSocket implementation requires
// gorilla/websocket or nhooyr.io/websocket.
func (w *Worker) Run(ctx context.Context) error {
	// In production, this would:
	// 1. Connect WebSocket to w.config.MasterURL
	// 2. Authenticate with w.config.SecretKey
	// 3. Register into w.config.Groups
	// 4. Loop: receive launch_job commands, dispatch via w.Dispatch
	// 5. On context cancellation: stop accepting, wait for in-flight, disconnect

	// For now, block until context is cancelled
	<-ctx.Done()
	return nil
}
