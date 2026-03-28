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

func TestRemoveBreaker(t *testing.T) {
	name := uniqueBreakerName()
	cb := GetBreaker(name, 3, 5*time.Second)
	if cb == nil {
		t.Fatal("expected non-nil breaker")
	}

	RemoveBreaker(name)

	// After removal, GetBreaker should create a new instance.
	cb2 := GetBreaker(name, 5, 10*time.Second)
	if cb2 == cb {
		t.Error("expected new breaker instance after RemoveBreaker, got same pointer")
	}
	// Clean up.
	RemoveBreaker(name)
}

func TestRemoveBreaker_Nonexistent(t *testing.T) {
	// Should not panic.
	RemoveBreaker("does-not-exist-" + uniqueBreakerName())
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
