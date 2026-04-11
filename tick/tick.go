// Package tick provides periodic task components for use with lifecycle.Run.
package tick

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
)

type ErrorBehavior int

const (
	Skip ErrorBehavior = iota
	Stop
)

type Option func(*config)

type config struct {
	immediate bool
	jitter    time.Duration
	onError   ErrorBehavior
	label     string
}

func Immediate() Option {
	return func(c *config) { c.immediate = true }
}

func Jitter(d time.Duration) Option {
	return func(c *config) { c.jitter = d }
}

func OnError(b ErrorBehavior) Option {
	return func(c *config) { c.onError = b }
}

func Label(s string) Option {
	return func(c *config) { c.label = s }
}

func Every(interval time.Duration, fn func(context.Context) error, opts ...Option) func(context.Context) error {
	chassis.AssertVersionChecked()
	cfg := &config{onError: Skip}
	for _, o := range opts {
		o(cfg)
	}

	return func(ctx context.Context) error {
		if interval <= 0 {
			return fmt.Errorf("tick: interval must be positive, got %v", interval)
		}
		if cfg.immediate {
			if err := fn(ctx); err != nil {
				if cfg.onError == Stop {
					return err
				}
			}
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				if cfg.jitter > 0 {
					jitterDelay := time.Duration(rand.Int64N(int64(cfg.jitter)))
					select {
					case <-ctx.Done():
						return nil
					case <-func() <-chan time.Time {
						t := time.NewTimer(jitterDelay)
						// Note: if ctx.Done fires, the timer is GC'd after firing.
						return t.C
					}():
					}
				}
				if err := fn(ctx); err != nil {
					if cfg.onError == Stop {
						return err
					}
				}
			}
		}
	}
}
