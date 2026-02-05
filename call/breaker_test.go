package call

import (
	"testing"
	"time"
)

func TestCircuitBreaker_ProbeBlocksConcurrentAllow(t *testing.T) {
	name := uniqueBreakerName()
	cb := GetBreaker(name, 1, 25*time.Millisecond)
	cb.resetForTest()

	// Trip the breaker with one failure (threshold=1).
	cb.Record(false)
	if cb.State() != StateOpen {
		t.Fatalf("expected StateOpen, got %d", cb.State())
	}

	// Wait for reset timeout to elapse.
	time.Sleep(30 * time.Millisecond)

	// First Allow transitions to probing (reported as HalfOpen).
	if err := cb.Allow(); err != nil {
		t.Fatalf("expected probe allow, got %v", err)
	}
	if cb.State() != StateHalfOpen {
		t.Fatalf("expected StateHalfOpen during probe, got %d", cb.State())
	}

	// Second Allow must be rejected — only one probe at a time.
	if err := cb.Allow(); err != ErrCircuitOpen {
		t.Fatalf("expected ErrCircuitOpen for concurrent probe, got %v", err)
	}

	// Successful probe closes the breaker.
	cb.Record(true)
	if cb.State() != StateClosed {
		t.Fatalf("expected StateClosed after success, got %d", cb.State())
	}
}

func TestStateName(t *testing.T) {
	cases := []struct {
		state State
		want  string
	}{
		{StateClosed, "closed"},
		{StateOpen, "open"},
		{StateHalfOpen, "half-open"},
		{State(99), "unknown"},
	}
	for _, tc := range cases {
		if got := stateName(tc.state); got != tc.want {
			t.Fatalf("stateName(%d) = %q, want %q", tc.state, got, tc.want)
		}
	}
}
