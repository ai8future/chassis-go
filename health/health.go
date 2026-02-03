// Package health provides composable health checks with parallel execution
// and a standard HTTP handler that returns structured JSON results.
package health

import (
	"context"
	"errors"
	"sync"

	"golang.org/x/sync/errgroup"
)

// Check is the standard health check signature. A nil return indicates a
// healthy dependency; any non-nil error is treated as unhealthy.
type Check func(ctx context.Context) error

// Result represents the outcome of a named health check.
type Result struct {
	Name    string `json:"name"`
	Healthy bool   `json:"healthy"`
	Error   string `json:"error,omitempty"`
}

// All returns a function that runs every named check in parallel using an
// errgroup. All checks execute regardless of individual failures. The
// returned error is errors.Join of every failing check (nil when all pass).
func All(checks map[string]Check) func(ctx context.Context) ([]Result, error) {
	return func(ctx context.Context) ([]Result, error) {
		var (
			mu      sync.Mutex
			results []Result
			errs    []error
		)

		g, gCtx := errgroup.WithContext(ctx)

		for name, check := range checks {
			g.Go(func() error {
				err := check(gCtx)

				r := Result{Name: name, Healthy: err == nil}
				if err != nil {
					r.Error = err.Error()
				}

				mu.Lock()
				results = append(results, r)
				if err != nil {
					errs = append(errs, err)
				}
				mu.Unlock()

				// Always return nil so errgroup does not cancel the
				// context and short-circuit the remaining checks.
				return nil
			})
		}

		_ = g.Wait()

		return results, errors.Join(errs...)
	}
}
