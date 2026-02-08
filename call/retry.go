package call

import (
	"context"
	"io"
	"math/rand/v2"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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
			// Drain and close any partial response body so the connection can be reused.
			if resp != nil && resp.Body != nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			// Network-level error — worth retrying.
			if attempt < r.MaxAttempts-1 {
				trace.SpanFromContext(ctx).AddEvent("retry", trace.WithAttributes(
					attribute.Int("attempt", attempt+1),
					attribute.String("reason", "network_error"),
				))
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
			trace.SpanFromContext(ctx).AddEvent("retry", trace.WithAttributes(
				attribute.Int("attempt", attempt+1),
				attribute.Int("http.status_code", resp.StatusCode),
			))
			// Drain and close the body so the underlying connection can be reused.
			io.Copy(io.Discard, resp.Body)
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
	if delay <= 0 {
		delay = 100 * time.Millisecond
	}
	for range attempt {
		delay *= 2
	}

	// Add jitter: random duration in [0, delay/2).
	if half := int64(delay / 2); half > 0 {
		delay += time.Duration(rand.Int64N(half))
	}

	t := time.NewTimer(delay)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
