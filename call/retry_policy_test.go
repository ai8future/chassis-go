package call

import (
	"context"
	"net/http"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
)

func init() { chassis.RequireMajor(11) }

func TestParseRetryAfterSecondsAndDate(t *testing.T) {
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	if got := ParseRetryAfter("2", now); got != 2*time.Second {
		t.Fatalf("seconds delay = %s, want 2s", got)
	}
	date := now.Add(3 * time.Second).Format(http.TimeFormat)
	if got := ParseRetryAfter(date, now); got < 2*time.Second || got > 3*time.Second {
		t.Fatalf("date delay = %s, want about 3s", got)
	}
}

func TestRetrierHonorsRetryAfter429(t *testing.T) {
	r := &Retrier{MaxAttempts: 2, BaseDelay: time.Hour}
	attempts := 0
	start := time.Now()
	resp, err := r.Do(context.Background(), func() (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {"1"}}, Body: http.NoBody}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	})
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	defer resp.Body.Close()
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if time.Since(start) < time.Second {
		t.Fatalf("Retry-After was not honored")
	}
}

func TestIdempotentOnlyRetriesSuppressesPost(t *testing.T) {
	r := &Retrier{MaxAttempts: 3, BaseDelay: time.Millisecond, IdempotentOnly: true, method: http.MethodPost}
	attempts := 0
	resp, err := r.Do(context.Background(), func() (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody}, nil
	})
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	defer resp.Body.Close()
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestCustomRetryPolicy(t *testing.T) {
	r := &Retrier{MaxAttempts: 2, BaseDelay: time.Millisecond, Policy: func(rc RetryContext) RetryDecision {
		if rc.Response != nil && rc.Response.StatusCode == http.StatusConflict {
			return RetryDecision{Retry: true, Reason: "conflict"}
		}
		return RetryDecision{}
	}}
	attempts := 0
	resp, err := r.Do(context.Background(), func() (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return &http.Response{StatusCode: http.StatusConflict, Body: http.NoBody}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	})
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	defer resp.Body.Close()
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}
