// Package work provides structured concurrency primitives with bounded
// parallelism and OpenTelemetry tracing. It offers Map, All, Race, and
// Stream patterns for fan-out/fan-in workloads.
package work

import (
	"context"
	"fmt"
	"runtime"
	"sync"

	chassis "github.com/ai8future/chassis-go"
	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/ai8future/chassis-go/work"

// Option configures a work function.
type Option func(*config)

type config struct {
	workers int
}

func defaults() config {
	return config{workers: runtime.NumCPU()}
}

// Workers sets the maximum concurrency level. Values less than 1 are clamped to 1.
func Workers(n int) Option {
	return func(c *config) { c.workers = max(1, n) }
}

// Result holds the outcome of processing a single item.
type Result[T any] struct {
	Value T
	Err   error
	Index int
}

// Errors collects per-item failures from Map or All.
type Errors struct {
	Failures []Failure
}

// Failure records the index and error of a single failed task.
type Failure struct {
	Index int
	Err   error
}

func (e *Errors) Error() string {
	return fmt.Sprintf("%d task(s) failed", len(e.Failures))
}

// Unwrap returns the underlying errors for use with errors.Is / errors.As.
func (e *Errors) Unwrap() []error {
	out := make([]error, len(e.Failures))
	for i, f := range e.Failures {
		out[i] = f.Err
	}
	return out
}

// Map applies fn to each item with bounded concurrency. Results are returned
// in input order. If any items fail, returns *Errors with all failures.
func Map[T, R any](ctx context.Context, items []T, fn func(context.Context, T) (R, error), opts ...Option) ([]R, error) {
	chassis.AssertVersionChecked()
	cfg := defaults()
	for _, o := range opts {
		o(&cfg)
	}

	tracer := otelapi.GetTracerProvider().Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "work.Map", trace.WithAttributes(
		attribute.Int("work.total", len(items)),
		attribute.String("work.pattern", "map"),
	))
	defer span.End()

	results := make([]R, len(items))
	errs := make([]error, len(items))

	sem := make(chan struct{}, cfg.workers)
	var wg sync.WaitGroup

	for i, item := range items {
		wg.Add(1)
		sem <- struct{}{} // acquire
		go func() {
			defer wg.Done()
			defer func() { <-sem }() // release

			childCtx, childSpan := tracer.Start(ctx, "work.Map.item",
				trace.WithAttributes(attribute.Int("work.index", i)),
			)
			defer childSpan.End()

			val, err := fn(childCtx, item)
			results[i] = val
			errs[i] = err
			if err != nil {
				childSpan.RecordError(err)
			}
		}()
	}

	wg.Wait()

	// Collect failures.
	var failures []Failure
	for i, err := range errs {
		if err != nil {
			failures = append(failures, Failure{Index: i, Err: err})
		}
	}

	succeeded := len(items) - len(failures)
	span.SetAttributes(
		attribute.Int("work.succeeded", succeeded),
		attribute.Int("work.failed", len(failures)),
	)

	if len(failures) > 0 {
		return results, &Errors{Failures: failures}
	}
	return results, nil
}

// All runs all tasks with bounded concurrency. Returns *Errors if any fail.
func All(ctx context.Context, tasks []func(context.Context) error, opts ...Option) error {
	chassis.AssertVersionChecked()
	cfg := defaults()
	for _, o := range opts {
		o(&cfg)
	}

	tracer := otelapi.GetTracerProvider().Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "work.All", trace.WithAttributes(
		attribute.Int("work.total", len(tasks)),
		attribute.String("work.pattern", "all"),
	))
	defer span.End()

	errs := make([]error, len(tasks))
	sem := make(chan struct{}, cfg.workers)
	var wg sync.WaitGroup

	for i, task := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			childCtx, childSpan := tracer.Start(ctx, "work.All.task",
				trace.WithAttributes(attribute.Int("work.index", i)),
			)
			defer childSpan.End()

			err := task(childCtx)
			errs[i] = err
			if err != nil {
				childSpan.RecordError(err)
			}
		}()
	}

	wg.Wait()

	var failures []Failure
	for i, err := range errs {
		if err != nil {
			failures = append(failures, Failure{Index: i, Err: err})
		}
	}

	succeeded := len(tasks) - len(failures)
	span.SetAttributes(
		attribute.Int("work.succeeded", succeeded),
		attribute.Int("work.failed", len(failures)),
	)

	if len(failures) > 0 {
		return &Errors{Failures: failures}
	}
	return nil
}

// Race launches all tasks concurrently and returns the result of the first
// one to succeed. If all tasks fail, returns *Errors. The context passed to
// tasks is cancelled once a winner is found.
func Race[R any](ctx context.Context, tasks ...func(context.Context) (R, error)) (R, error) {
	chassis.AssertVersionChecked()

	tracer := otelapi.GetTracerProvider().Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "work.Race", trace.WithAttributes(
		attribute.Int("work.total", len(tasks)),
		attribute.String("work.pattern", "race"),
	))
	defer span.End()

	var zero R
	if len(tasks) == 0 {
		return zero, nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type raceResult struct {
		value R
		err   error
		index int
	}

	ch := make(chan raceResult, len(tasks))
	for i, task := range tasks {
		go func() {
			val, err := task(ctx)
			ch <- raceResult{value: val, err: err, index: i}
		}()
	}

	var failures []Failure
	for range len(tasks) {
		r := <-ch
		if r.err == nil {
			cancel() // cancel remaining
			span.SetAttributes(
				attribute.Int("work.succeeded", 1),
				attribute.Int("work.winner_index", r.index),
			)
			return r.value, nil
		}
		failures = append(failures, Failure{Index: r.index, Err: r.err})
	}

	span.SetAttributes(
		attribute.Int("work.succeeded", 0),
		attribute.Int("work.failed", len(failures)),
	)
	return zero, &Errors{Failures: failures}
}

// Stream applies fn to values received from in with bounded concurrency,
// sending results to the returned channel. The output channel is closed
// when the input channel is closed and all in-flight work completes.
func Stream[T, R any](ctx context.Context, in <-chan T, fn func(context.Context, T) (R, error), opts ...Option) <-chan Result[R] {
	chassis.AssertVersionChecked()
	cfg := defaults()
	for _, o := range opts {
		o(&cfg)
	}

	out := make(chan Result[R])

	go func() {
		defer close(out)

		tracer := otelapi.GetTracerProvider().Tracer(tracerName)
		_, span := tracer.Start(ctx, "work.Stream", trace.WithAttributes(
			attribute.String("work.pattern", "stream"),
		))
		defer span.End()

		var wg sync.WaitGroup
		sem := make(chan struct{}, cfg.workers)
		idx := 0

		for item := range in {
			select {
			case <-ctx.Done():
				// Stop accepting new items but wait for in-flight workers.
				goto drain
			case sem <- struct{}{}:
			}

			wg.Add(1)
			currentIdx := idx
			currentItem := item
			idx++

			go func() {
				defer wg.Done()
				defer func() { <-sem }()

				childCtx, childSpan := tracer.Start(ctx, "work.Stream.item",
					trace.WithAttributes(attribute.Int("work.index", currentIdx)),
				)
				val, err := fn(childCtx, currentItem)
				if err != nil {
					childSpan.RecordError(err)
				}
				childSpan.End()
				out <- Result[R]{Value: val, Err: err, Index: currentIdx}
			}()
		}

	drain:
		wg.Wait()
	}()

	return out
}
