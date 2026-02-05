// Package health provides composable health checks with parallel execution
// and a standard HTTP handler that returns structured JSON results.
package health

import (
	"context"
	"errors"

	chassis "github.com/ai8future/chassis-go"
	"github.com/ai8future/chassis-go/work"
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

// namedCheck pairs a name with its check function for use with work.Map.
type namedCheck struct {
	name  string
	check Check
}

// CheckFunc returns a simple health check function suitable for passing
// directly to grpckit.RegisterHealth. It runs all checks via All and
// discards the individual results, returning only the aggregate error.
func CheckFunc(checks map[string]Check) func(ctx context.Context) error {
	chassis.AssertVersionChecked()
	run := All(checks)
	return func(ctx context.Context) error {
		_, err := run(ctx)
		return err
	}
}

// All returns a function that runs every named check in parallel using
// work.Map. All checks execute regardless of individual failures. The
// returned error is errors.Join of every failing check (nil when all pass).
func All(checks map[string]Check) func(ctx context.Context) ([]Result, error) {
	chassis.AssertVersionChecked()
	return func(ctx context.Context) ([]Result, error) {
		entries := make([]namedCheck, 0, len(checks))
		for name, check := range checks {
			entries = append(entries, namedCheck{name: name, check: check})
		}

		results, _ := work.Map(ctx, entries, func(ctx context.Context, nc namedCheck) (Result, error) {
			err := nc.check(ctx)
			r := Result{Name: nc.name, Healthy: err == nil}
			if err != nil {
				r.Error = err.Error()
			}
			// Always return nil error so Map collects all results.
			return r, nil
		})

		var errs []error
		for _, r := range results {
			if !r.Healthy && r.Error != "" {
				errs = append(errs, errors.New(r.Error))
			}
		}

		return results, errors.Join(errs...)
	}
}
