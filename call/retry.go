package call

import (
	"context"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Retrier provides retry logic with exponential backoff and jitter for
// transient server errors (5xx). It never retries client errors (4xx) unless an
// explicit Retry-After header on 429 provides retry guidance.
type Retrier struct {
	MaxAttempts    int
	BaseDelay      time.Duration
	Policy         RetryPolicy
	IdempotentOnly bool
	method         string
}

// RetryContext describes a completed attempt for retry policy decisions.
type RetryContext struct {
	Attempt  int
	Method   string
	Response *http.Response
	Err      error
}

// RetryDecision is returned by RetryPolicy.
type RetryDecision struct {
	Retry  bool
	Delay  time.Duration
	Reason string
}

// RetryPolicy decides whether an attempt should be retried.
type RetryPolicy func(RetryContext) RetryDecision

// Do executes fn up to MaxAttempts times. The default policy preserves the
// historical behavior: network errors and 5xx responses retry with backoff. It
// additionally honors explicit Retry-After guidance on 429/503 responses. It
// respects context cancellation and deadline, stopping immediately when the
// context is done.
//
// If the request has a GetBody function, it is called before each retry to
// rewind the request body. Without GetBody, retries of requests with a body
// will send an empty/consumed body.
func (r *Retrier) Do(ctx context.Context, fn func() (*http.Response, error)) (*http.Response, error) {
	var (
		resp *http.Response
		err  error
	)

	maxAttempts := max(1, r.MaxAttempts)
	for attempt := range maxAttempts {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		resp, err = fn()
		if ctx.Err() != nil {
			drainAndClose(resp)
			return nil, ctx.Err()
		}
		decision := r.policy()(RetryContext{
			Attempt:  attempt + 1,
			Method:   r.method,
			Response: resp,
			Err:      err,
		})
		if decision.Retry && attempt < maxAttempts-1 {
			trace.SpanFromContext(ctx).AddEvent("retry", trace.WithAttributes(
				attribute.Int("attempt", attempt+1),
				attribute.String("reason", decision.reason()),
				attribute.Int("http.status_code", statusCode(resp)),
			))
			drainAndClose(resp)
			if waitErr := r.wait(ctx, attempt, decision.Delay); waitErr != nil {
				return nil, waitErr
			}
			continue
		}
		if err != nil {
			drainAndClose(resp)
			return nil, err
		}
		return resp, nil
	}

	return resp, err
}

func (r *Retrier) policy() RetryPolicy {
	policy := r.Policy
	if policy == nil {
		policy = DefaultRetryPolicy
	}
	if r.IdempotentOnly {
		policy = IdempotentOnlyPolicy(policy)
	}
	return policy
}

// DefaultRetryPolicy preserves historical defaults: retry network errors and
// 5xx responses with exponential backoff. It additionally honors explicit
// Retry-After guidance on 429 and 503 responses without changing no-header 429
// behavior.
func DefaultRetryPolicy(rc RetryContext) RetryDecision {
	if rc.Err != nil {
		return RetryDecision{Retry: true, Reason: "network_error"}
	}
	if rc.Response == nil {
		return RetryDecision{}
	}
	if delay := ParseRetryAfter(rc.Response.Header.Get("Retry-After"), time.Now()); delay > 0 &&
		(rc.Response.StatusCode == http.StatusTooManyRequests || rc.Response.StatusCode == http.StatusServiceUnavailable) {
		return RetryDecision{Retry: true, Delay: delay, Reason: "retry_after"}
	}
	if rc.Response.StatusCode >= 500 {
		return RetryDecision{Retry: true, Reason: "server_error"}
	}
	return RetryDecision{}
}

// IdempotentOnlyPolicy wraps a retry policy and suppresses retries for methods
// that are not idempotent by HTTP semantics.
func IdempotentOnlyPolicy(next RetryPolicy) RetryPolicy {
	if next == nil {
		next = DefaultRetryPolicy
	}
	return func(rc RetryContext) RetryDecision {
		if !isIdempotentMethod(rc.Method) {
			return RetryDecision{}
		}
		return next(rc)
	}
}

// ParseRetryAfter parses an RFC 9110 Retry-After header as seconds or HTTP-date.
func ParseRetryAfter(value string, now time.Time) time.Duration {
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	if now.IsZero() {
		now = time.Now()
	}
	if !when.After(now) {
		return 0
	}
	return when.Sub(now)
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

	return sleepContext(ctx, delay)
}

func (r *Retrier) wait(ctx context.Context, attempt int, explicit time.Duration) error {
	if explicit > 0 {
		return sleepContext(ctx, explicit)
	}
	return r.backoff(ctx, attempt)
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (d RetryDecision) reason() string {
	if d.Reason != "" {
		return d.Reason
	}
	return "policy"
}

func statusCode(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

func drainAndClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

func isIdempotentMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}
