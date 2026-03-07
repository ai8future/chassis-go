// Package lifecycle provides a minimal orchestration primitive for running
// concurrent components that share a single context for cancellation and
// graceful shutdown via OS signals.
package lifecycle

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	chassis "github.com/ai8future/chassis-go/v5"
	"github.com/ai8future/chassis-go/v5/registry"
	"golang.org/x/sync/errgroup"
)

// Component is a long-running function that participates in the application
// lifecycle. It must respect ctx.Done() to allow graceful shutdown.
type Component func(ctx context.Context) error

// Run orchestrates one or more components. It accepts Component values
// (or bare func(ctx context.Context) error). It creates a context cancelled
// on SIGTERM or SIGINT, launches every component as a goroutine in an
// errgroup, and waits for all of them to finish. If any component returns
// an error the shared context is cancelled, signalling the remaining
// components to shut down. The first non-nil error (if any) is returned.
func Run(ctx context.Context, args ...any) error {
	chassis.AssertVersionChecked()

	var components []Component

	for _, a := range args {
		switch v := a.(type) {
		case Component:
			components = append(components, v)
		case func(ctx context.Context) error:
			components = append(components, v)
		default:
			panic(fmt.Sprintf("lifecycle: Run received unsupported argument type %T", a))
		}
	}

	signalCtx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Track whether shutdown was triggered by an OS signal (as opposed to
	// user components finishing or the parent context being cancelled).
	var gotSignal atomic.Bool
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case <-signalCh:
			gotSignal.Store(true)
		case <-signalCtx.Done():
		}
		signal.Stop(signalCh)
	}()

	if err := registry.Init(stop, chassis.Version); err != nil {
		return fmt.Errorf("lifecycle: registry: %w", err)
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

	reason := "clean"
	if err != nil {
		reason = err.Error()
	}
	if gotSignal.Load() {
		reason = "signal"
	}
	registry.Shutdown(reason)

	if registry.RestartRequested() {
		syscall.Exec(os.Args[0], os.Args, os.Environ())
	}

	return err
}
