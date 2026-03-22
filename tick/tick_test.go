package tick_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v10"
	"github.com/ai8future/chassis-go/v10/tick"
)

func init() { chassis.RequireMajor(10) }

func TestEveryRunsRepeatedly(t *testing.T) {
	var count atomic.Int32
	fn := func(ctx context.Context) error {
		count.Add(1)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	component := tick.Every(50*time.Millisecond, fn)
	err := component(ctx)
	if err != nil && err != context.DeadlineExceeded {
		t.Fatalf("unexpected error: %v", err)
	}
	got := count.Load()
	if got < 3 {
		t.Fatalf("expected at least 3 ticks in 250ms at 50ms interval, got %d", got)
	}
}

func TestEveryImmediate(t *testing.T) {
	var count atomic.Int32
	fn := func(ctx context.Context) error {
		count.Add(1)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	component := tick.Every(1*time.Hour, fn, tick.Immediate())
	_ = component(ctx)
	if count.Load() < 1 {
		t.Fatal("expected immediate first tick")
	}
}

func TestEveryOnErrorStop(t *testing.T) {
	callCount := 0
	fn := func(ctx context.Context) error {
		callCount++
		return context.Canceled
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	component := tick.Every(10*time.Millisecond, fn, tick.OnError(tick.Stop))
	err := component(ctx)
	if err == nil {
		t.Fatal("expected error propagation")
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call before stop, got %d", callCount)
	}
}

func TestEveryOnErrorSkip(t *testing.T) {
	var count atomic.Int32
	fn := func(ctx context.Context) error {
		count.Add(1)
		return context.Canceled
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	component := tick.Every(30*time.Millisecond, fn, tick.OnError(tick.Skip))
	_ = component(ctx)
	if count.Load() < 2 {
		t.Fatalf("expected multiple ticks despite errors, got %d", count.Load())
	}
}

func TestEveryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fn := func(ctx context.Context) error { return nil }

	done := make(chan error, 1)
	go func() { done <- tick.Every(50*time.Millisecond, fn)(ctx) }()

	cancel()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("tick did not stop on context cancellation")
	}
}
