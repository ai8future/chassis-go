// Package lifecycle provides a minimal orchestration primitive for running
// concurrent components that share a single context for cancellation and
// graceful shutdown via OS signals.
package lifecycle

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/announcekit"
	"github.com/ai8future/chassis-go/v11/heartbeatkit"
	"github.com/ai8future/chassis-go/v11/kafkakit"
	"github.com/ai8future/chassis-go/v11/registry"
	"golang.org/x/sync/errgroup"
)

var newPublisher = kafkakit.NewPublisher

// AnnounceTimeout is the maximum time to wait for lifecycle announcements
// (started/stopping). Announcements are best-effort and must not block the
// service lifecycle. Set before calling Run; not safe for concurrent
// modification.
var AnnounceTimeout = 5 * time.Second

// Component is a long-running function that participates in the application
// lifecycle. It must respect ctx.Done() to allow graceful shutdown.
type Component func(ctx context.Context) error

// Option configures optional lifecycle behavior. Pass Option values alongside
// Component values in the args to Run.
type Option func(*options)

type options struct {
	kafkaCfg    *kafkakit.Config
	serviceName string // resolved lazily if not set
}

// WithKafkaConfig enables kafkakit integration. When the config has
// BootstrapServers set, lifecycle.Run will automatically:
//   - Create a kafkakit.Publisher
//   - Start heartbeatkit (publishes liveness events)
//   - Announce service started/stopping via announcekit
//
// If BootstrapServers is empty the option is silently ignored.
func WithKafkaConfig(cfg kafkakit.Config) Option {
	return func(o *options) {
		o.kafkaCfg = &cfg
	}
}

// WithServiceName overrides the service name used by heartbeatkit and
// announcekit. If not set, the name is resolved from the CHASSIS_SERVICE_NAME
// environment variable or the working directory basename (same logic as
// registry).
func WithServiceName(name string) Option {
	return func(o *options) {
		o.serviceName = name
	}
}

// RunComponents is the type-safe variant of Run that accepts only Component
// values and optional Option values. Prefer this over Run when all components
// are known at compile time.
func RunComponents(ctx context.Context, components ...Component) error {
	args := make([]any, len(components))
	for i, c := range components {
		args[i] = c
	}
	return Run(ctx, args...)
}

// Run orchestrates one or more components. It accepts Component values
// (or bare func(ctx context.Context) error) and Option values. It creates a
// context cancelled on SIGTERM or SIGINT, launches every component as a
// goroutine in an errgroup, and waits for all of them to finish. If any
// component returns an error the shared context is cancelled, signalling the
// remaining components to shut down. The first non-nil error (if any) is
// returned.
//
// When WithKafkaConfig is provided and the config is enabled, Run automatically
// starts heartbeatkit and announcekit, and shuts them down on exit.
func Run(ctx context.Context, args ...any) error {
	chassis.AssertVersionChecked()

	var o options
	var components []Component

	for _, a := range args {
		switch v := a.(type) {
		case Component:
			components = append(components, v)
		case func(ctx context.Context) error:
			components = append(components, v)
		case Option:
			v(&o)
		default:
			panic(fmt.Sprintf("lifecycle: Run received unsupported argument type %T", a))
		}
	}

	signalCtx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if err := registry.Init(stop, chassis.Version); err != nil {
		return fmt.Errorf("lifecycle: registry: %w", err)
	}
	registryInitialized := true
	defer func() {
		if registryInitialized {
			registry.Shutdown("startup failed")
		}
	}()

	// Resolve service name for kafkakit integrations.
	svcName := o.serviceName
	if svcName == "" {
		svcName = resolveName()
	}

	// Initialize kafkakit if configured.
	var pub *kafkakit.Publisher
	if o.kafkaCfg != nil && o.kafkaCfg.Enabled() {
		// Default Source to service name if not set.
		cfg := *o.kafkaCfg
		if cfg.Source == "" {
			cfg.Source = svcName
		}

		var err error
		pub, err = newPublisher(cfg)
		if err != nil {
			return fmt.Errorf("lifecycle: kafkakit: %w", err)
		}

		// Start heartbeatkit.
		heartbeatkit.Start(signalCtx, pub, heartbeatkit.Config{
			ServiceName: svcName,
			Version:     chassis.Version,
		})

		// Announce service started (best-effort with timeout).
		announcekit.SetServiceName(svcName)
		announceCtx, announceCancel := context.WithTimeout(ctx, AnnounceTimeout)
		_ = announcekit.Started(announceCtx, pub)
		announceCancel()
	}

	// infraCtx is cancelled when all user components finish, which tells
	// the heartbeat and command-poll goroutines to exit.
	infraCtx, infraCancel := context.WithCancel(signalCtx)
	defer infraCancel()

	g, gCtx := errgroup.WithContext(signalCtx)

	g.Go(func() error { return registry.RunHeartbeat(infraCtx) })
	g.Go(func() error { return registry.RunCommandPoll(infraCtx) })

	// Run user components in a nested errgroup so we can detect when they
	// all finish and stop infrastructure goroutines.
	userG, userCtx := errgroup.WithContext(gCtx)
	for _, c := range components {
		userG.Go(func() error { return c(userCtx) })
	}

	g.Go(func() error {
		err := userG.Wait()
		infraCancel()
		return err
	})

	err := g.Wait()

	// Kafkakit shutdown sequence.
	if pub != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), AnnounceTimeout)
		_ = announcekit.Stopping(stopCtx, pub)
		stopCancel()
		heartbeatkit.Stop()
		pub.Close()
	}

	reason := "clean"
	if err != nil {
		reason = err.Error()
	}
	if signalCtx.Err() != nil && ctx.Err() == nil {
		reason = "signal"
	}
	registry.Shutdown(reason)
	registryInitialized = false

	if registry.RestartRequested() {
		exePath, exeErr := os.Executable()
		if exeErr != nil {
			return fmt.Errorf("lifecycle: resolve executable for restart: %w", exeErr)
		}
		if resolved, linkErr := filepath.EvalSymlinks(exePath); linkErr == nil {
			exePath = resolved
		}
		if execErr := syscall.Exec(exePath, os.Args, os.Environ()); execErr != nil {
			return fmt.Errorf("lifecycle: restart exec: %w", execErr)
		}
	}

	return err
}

// resolveName determines the service name. Uses CHASSIS_SERVICE_NAME env var
// if set, otherwise falls back to the working directory basename. Mirrors
// the logic in the registry package.
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
