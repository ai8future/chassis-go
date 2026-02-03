package call

import (
	"context"
	"math/rand/v2"
	"net/http"
	"time"
)

// Retrier provides retry logic with exponential backoff and jitter for
// transient server errors (5xx). It never retries client errors (4xx).
type Retrier struct {
	MaxAttempts int
	BaseDelay   time.Duration
}

// Do executes fn up to MaxAttempts times, retrying only when a 5xx status code
// is returned. Between attempts it sleeps with exponential backoff plus random
// jitter of up to 50% of the calculated delay. It respects context
// cancellation and deadline, stopping immediately when the context is done.
func (r *Retrier) Do(ctx context.Context, fn func() (*http.Response, error)) (*http.Response, error) {
	var (
		resp *http.Response
		err  error
	)

	for attempt := range r.MaxAttempts {
		// Check context before each attempt.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		resp, err = fn()
		if err != nil {
			// Network-level error — worth retrying.
			if attempt < r.MaxAttempts-1 {
				if waitErr := r.backoff(ctx, attempt); waitErr != nil {
					return nil, waitErr
				}
				continue
			}
			return nil, err
		}

		// 2xx/3xx — success, return immediately.
		if resp.StatusCode < 500 {
			return resp, nil
		}

		// 5xx — retry if we have attempts remaining.
		if attempt < r.MaxAttempts-1 {
			// Drain and close the body so the connection can be reused.
			resp.Body.Close()
			if waitErr := r.backoff(ctx, attempt); waitErr != nil {
				return nil, waitErr
			}
			continue
		}
	}

	// All attempts exhausted — return the last response and error.
	return resp, err
}

// backoff sleeps for an exponentially increasing duration with jitter. It
// returns an error if the context is cancelled during the wait.
func (r *Retrier) backoff(ctx context.Context, attempt int) error {
	delay := r.BaseDelay
	for range attempt {
		delay *= 2
	}

	// Add jitter: random duration in [0, delay/2).
	jitter := time.Duration(rand.Int64N(int64(delay / 2)))
	delay += jitter

	t := time.NewTimer(delay)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
