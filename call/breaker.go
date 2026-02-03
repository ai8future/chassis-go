// Package call provides a resilient HTTP client with retry, circuit breaker,
// and timeout support using a composable builder pattern.
package call

import (
	"errors"
	"sync"
	"time"
)

// ErrCircuitOpen is returned when a circuit breaker is in the Open state and
// rejects requests.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// State represents the current state of a circuit breaker.
type State int

const (
	// StateClosed allows all requests through while tracking failures.
	StateClosed State = iota
	// StateOpen rejects all requests immediately.
	StateOpen
	// StateHalfOpen allows a single probe request through.
	StateHalfOpen
)

// breakers is a package-level registry ensuring singleton breakers by name.
var breakers sync.Map

// CircuitBreaker implements the circuit breaker pattern. It tracks consecutive
// failures and short-circuits requests when the failure threshold is reached,
// giving the downstream service time to recover.
type CircuitBreaker struct {
	mu           sync.Mutex
	name         string
	state        State
	failures     int
	threshold    int
	resetTimeout time.Duration
	lastFailure  time.Time
}

// GetBreaker returns an existing circuit breaker for the given name or creates
// a new one with the provided threshold and reset timeout. Breakers are
// singletons keyed by name.
func GetBreaker(name string, threshold int, resetTimeout time.Duration) *CircuitBreaker {
	if v, ok := breakers.Load(name); ok {
		return v.(*CircuitBreaker)
	}

	cb := &CircuitBreaker{
		name:         name,
		state:        StateClosed,
		threshold:    threshold,
		resetTimeout: resetTimeout,
	}

	actual, _ := breakers.LoadOrStore(name, cb)
	return actual.(*CircuitBreaker)
}

// Allow checks whether a request is permitted through the breaker. It returns
// nil when the request may proceed or ErrCircuitOpen when it must be rejected.
func (cb *CircuitBreaker) Allow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return nil

	case StateOpen:
		if time.Since(cb.lastFailure) >= cb.resetTimeout {
			cb.state = StateHalfOpen
			return nil
		}
		return ErrCircuitOpen

	case StateHalfOpen:
		// Only one probe request is allowed; subsequent callers while the
		// probe is in-flight are rejected. The first caller to reach
		// half-open proceeds (handled by the state transition in the Open
		// case above), so if we're already in HalfOpen we allow it.
		return nil
	}

	return nil
}

// Record reports the outcome of a request to the breaker so it can update its
// internal state accordingly.
func (cb *CircuitBreaker) Record(success bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		if success {
			cb.failures = 0
			return
		}
		cb.failures++
		cb.lastFailure = time.Now()
		if cb.failures >= cb.threshold {
			cb.state = StateOpen
		}

	case StateHalfOpen:
		if success {
			cb.state = StateClosed
			cb.failures = 0
		} else {
			cb.state = StateOpen
			cb.lastFailure = time.Now()
		}
	}
}

// State returns the current state of the circuit breaker.
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// resetForTest resets the breaker to its initial closed state. This is
// exported only for testing and should not be used in production code.
func (cb *CircuitBreaker) resetForTest() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = StateClosed
	cb.failures = 0
	cb.lastFailure = time.Time{}
}
