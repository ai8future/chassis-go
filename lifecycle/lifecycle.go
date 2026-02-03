// Package lifecycle provides a minimal orchestration primitive for running
// concurrent components that share a single context for cancellation and
// graceful shutdown via OS signals.
package lifecycle

import (
	"context"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"
)

// Component is a long-running function that participates in the application
// lifecycle. It must respect ctx.Done() to allow graceful shutdown.
type Component func(ctx context.Context) error

// Run orchestrates one or more components. It creates a context that is
// cancelled on SIGTERM or SIGINT, launches every component as a goroutine
// in an errgroup, and waits for all of them to finish. If any component
// returns an error the shared context is cancelled, signalling the remaining
// components to shut down. The first non-nil error (if any) is returned.
func Run(ctx context.Context, components ...Component) error {
	signalCtx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	g, gCtx := errgroup.WithContext(signalCtx)

	for _, c := range components {
		g.Go(func() error {
			return c(gCtx)
		})
	}

	return g.Wait()
}
