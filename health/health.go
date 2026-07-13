// Package health provides composable health checks with parallel execution
// and a standard HTTP handler that returns structured JSON results.
package health

import (
	"context"
	"errors"
	"fmt"
	"sort"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/work"
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

// checkResult holds the Result plus the original error for wrapping.
type checkResult struct {
	result Result
	err    error
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
// Original errors are wrapped with the check name using fmt.Errorf so that
// errors.Is chains are preserved.
func All(checks map[string]Check) func(ctx context.Context) ([]Result, error) {
	chassis.AssertVersionChecked()
	return func(ctx context.Context) ([]Result, error) {
		names := make([]string, 0, len(checks))
		for name := range checks {
			names = append(names, name)
		}
		sort.Strings(names)
		entries := make([]namedCheck, 0, len(checks))
		for _, name := range names {
			entries = append(entries, namedCheck{name: name, check: checks[name]})
		}

		crs, mapErr := work.Map(ctx, entries, func(ctx context.Context, nc namedCheck) (checkResult, error) {
			err := nc.check(ctx)
			r := Result{Name: nc.name, Healthy: err == nil}
			if err != nil {
				r.Error = err.Error()
			}
			return checkResult{result: r, err: err}, err
		})

		failures := make(map[int]error)
		var workErrs *work.Errors
		if errors.As(mapErr, &workErrs) {
			for _, failure := range workErrs.Failures {
				failures[failure.Index] = failure.Err
			}
		}

		results := make([]Result, len(entries))
		var errs []error
		for i, entry := range entries {
			result := crs[i].result
			result.Name = entry.name
			checkErr := crs[i].err
			if failure, ok := failures[i]; ok {
				checkErr = failure
			}
			result.Healthy = checkErr == nil
			result.Error = ""
			if checkErr != nil {
				result.Error = checkErr.Error()
				errs = append(errs, fmt.Errorf("%s: %w", entry.name, checkErr))
			}
			results[i] = result
		}
		if mapErr != nil && workErrs == nil {
			errs = append(errs, mapErr)
		}

		return results, errors.Join(errs...)
	}
}
