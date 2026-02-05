package lifecycle

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go"
)

func TestMain(m *testing.M) {
	chassis.RequireMajor(4)
	os.Exit(m.Run())
}

func TestRunSingleComponentCleanShutdown(t *testing.T) {
	comp := func(ctx context.Context) error {
		return nil
	}

	err := Run(context.Background(), comp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestRunComponentReturnsError(t *testing.T) {
	want := errors.New("component failed")

	comp := func(ctx context.Context) error {
		return want
	}

	err := Run(context.Background(), comp)
	if !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
}

func TestRunMultipleComponentsOneFails(t *testing.T) {
	compErr := errors.New("boom")

	// Track whether the healthy component observed cancellation.
	var cancelled atomic.Bool

	failing := func(ctx context.Context) error {
		return compErr
	}

	healthy := func(ctx context.Context) error {
		// Block until context is cancelled by the failing component.
		<-ctx.Done()
		cancelled.Store(true)
		return nil
	}

	err := Run(context.Background(), failing, healthy)
	if !errors.Is(err, compErr) {
		t.Fatalf("expected %v, got %v", compErr, err)
	}
	if !cancelled.Load() {
		t.Fatal("expected healthy component to observe context cancellation")
	}
}

func TestRunComponentsRespectContextCancellation(t *testing.T) {
	// Pre-cancel the parent context to simulate an external shutdown trigger.
	ctx, cancel := context.WithCancel(context.Background())

	var stopped atomic.Int32

	makeComp := func() Component {
		return func(ctx context.Context) error {
			<-ctx.Done()
			stopped.Add(1)
			return nil
		}
	}

	// Cancel immediately so components unblock right away.
	cancel()

	err := Run(ctx, makeComp(), makeComp(), makeComp())
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if n := stopped.Load(); n != 3 {
		t.Fatalf("expected 3 components to observe cancellation, got %d", n)
	}
}

func TestRunIgnoresUnknownArgs(t *testing.T) {
	comp := func(ctx context.Context) error {
		return nil
	}

	// Passing non-Component args should be silently ignored.
	err := Run(context.Background(), comp, "ignored-string", 42)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestRunSignalHandling(t *testing.T) {
	done := make(chan error, 1)

	go func() {
		err := Run(context.Background(), func(ctx context.Context) error {
			<-ctx.Done()
			return nil
		})
		done <- err
	}()

	// Give Run a moment to register the signal handler.
	time.Sleep(50 * time.Millisecond)

	// Send SIGINT to our own process.
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("failed to send SIGINT: %v", err)
	}

	select {
	case err := <-done:
		// context.Canceled is acceptable; nil is also fine.
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("expected nil or context.Canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Run to return after SIGINT")
	}
}
