package work

import (
	"context"
	"errors"
	"os"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go"
)

func TestMain(m *testing.M) {
	chassis.RequireMajor(4)
	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// Map tests
// ---------------------------------------------------------------------------

func TestMap_Success(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	results, err := Map(context.Background(), items, func(_ context.Context, n int) (int, error) {
		return n * 2, nil
	}, Workers(2))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []int{2, 4, 6, 8, 10}
	for i, v := range results {
		if v != expected[i] {
			t.Errorf("results[%d] = %d, want %d", i, v, expected[i])
		}
	}
}

func TestMap_PartialFailure(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	results, err := Map(context.Background(), items, func(_ context.Context, n int) (int, error) {
		if n%2 == 0 {
			return 0, errors.New("even number")
		}
		return n * 2, nil
	}, Workers(3))

	if err == nil {
		t.Fatal("expected error for partial failures")
	}

	var workErrs *Errors
	if !errors.As(err, &workErrs) {
		t.Fatalf("expected *Errors, got %T", err)
	}

	if len(workErrs.Failures) != 2 {
		t.Fatalf("expected 2 failures, got %d", len(workErrs.Failures))
	}

	// Verify successful results are still present.
	if results[0] != 2 {
		t.Errorf("results[0] = %d, want 2", results[0])
	}
	if results[2] != 6 {
		t.Errorf("results[2] = %d, want 6", results[2])
	}
	if results[4] != 10 {
		t.Errorf("results[4] = %d, want 10", results[4])
	}

	// Verify failure indices.
	failIndices := make([]int, len(workErrs.Failures))
	for i, f := range workErrs.Failures {
		failIndices[i] = f.Index
	}
	sort.Ints(failIndices)
	if failIndices[0] != 1 || failIndices[1] != 3 {
		t.Errorf("expected failure indices [1, 3], got %v", failIndices)
	}
}

func TestMap_BoundedConcurrency(t *testing.T) {
	const maxWorkers = 2
	var active, peak atomic.Int32

	items := make([]int, 20)
	for i := range items {
		items[i] = i
	}

	_, err := Map(context.Background(), items, func(_ context.Context, _ int) (int, error) {
		cur := active.Add(1)
		// Update peak.
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		active.Add(-1)
		return 0, nil
	}, Workers(maxWorkers))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p := int(peak.Load()); p > maxWorkers {
		t.Fatalf("peak concurrency %d exceeds Workers(%d)", p, maxWorkers)
	}
}

func TestMap_EmptySlice(t *testing.T) {
	results, err := Map(context.Background(), []int{}, func(_ context.Context, n int) (int, error) {
		return n, nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected empty results, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// All tests
// ---------------------------------------------------------------------------

func TestAll_Success(t *testing.T) {
	var counter atomic.Int32

	tasks := make([]func(context.Context) error, 5)
	for i := range tasks {
		tasks[i] = func(_ context.Context) error {
			counter.Add(1)
			return nil
		}
	}

	err := All(context.Background(), tasks, Workers(3))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if n := int(counter.Load()); n != 5 {
		t.Fatalf("expected 5 tasks run, got %d", n)
	}
}

func TestAll_PartialFailure(t *testing.T) {
	tasks := []func(context.Context) error{
		func(_ context.Context) error { return nil },
		func(_ context.Context) error { return errors.New("task 1 failed") },
		func(_ context.Context) error { return nil },
		func(_ context.Context) error { return errors.New("task 3 failed") },
	}

	err := All(context.Background(), tasks, Workers(2))
	if err == nil {
		t.Fatal("expected error for partial failures")
	}

	var workErrs *Errors
	if !errors.As(err, &workErrs) {
		t.Fatalf("expected *Errors, got %T", err)
	}

	if len(workErrs.Failures) != 2 {
		t.Fatalf("expected 2 failures, got %d", len(workErrs.Failures))
	}
}

func TestAll_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var started atomic.Int32

	tasks := make([]func(context.Context) error, 10)
	for i := range tasks {
		tasks[i] = func(ctx context.Context) error {
			started.Add(1)
			<-ctx.Done()
			return ctx.Err()
		}
	}

	done := make(chan error, 1)
	go func() {
		done <- All(ctx, tasks, Workers(2))
	}()

	// Give some tasks time to start.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error after context cancellation")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for All to return")
	}
}

// ---------------------------------------------------------------------------
// Race tests
// ---------------------------------------------------------------------------

func TestRace_FirstSucceeds(t *testing.T) {
	fast := func(_ context.Context) (string, error) {
		return "fast", nil
	}
	slow := func(ctx context.Context) (string, error) {
		select {
		case <-time.After(5 * time.Second):
			return "slow", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	result, err := Race(context.Background(), fast, slow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "fast" {
		t.Fatalf("expected 'fast', got %q", result)
	}
}

func TestRace_AllFail(t *testing.T) {
	fail1 := func(_ context.Context) (string, error) {
		return "", errors.New("fail1")
	}
	fail2 := func(_ context.Context) (string, error) {
		return "", errors.New("fail2")
	}

	_, err := Race(context.Background(), fail1, fail2)
	if err == nil {
		t.Fatal("expected error when all tasks fail")
	}

	var workErrs *Errors
	if !errors.As(err, &workErrs) {
		t.Fatalf("expected *Errors, got %T", err)
	}
	if len(workErrs.Failures) != 2 {
		t.Fatalf("expected 2 failures, got %d", len(workErrs.Failures))
	}
}

func TestRace_ContextCancelled(t *testing.T) {
	var cancelled atomic.Bool

	winner := func(_ context.Context) (string, error) {
		return "winner", nil
	}
	loser := func(ctx context.Context) (string, error) {
		<-ctx.Done()
		cancelled.Store(true)
		return "", ctx.Err()
	}

	result, err := Race(context.Background(), winner, loser)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "winner" {
		t.Fatalf("expected 'winner', got %q", result)
	}

	// Give the loser goroutine time to observe cancellation.
	time.Sleep(50 * time.Millisecond)
	if !cancelled.Load() {
		t.Fatal("expected loser to observe context cancellation")
	}
}

// ---------------------------------------------------------------------------
// Stream tests
// ---------------------------------------------------------------------------

func TestStream_ProcessesAllItems(t *testing.T) {
	in := make(chan int, 5)
	for i := range 5 {
		in <- i
	}
	close(in)

	out := Stream(context.Background(), in, func(_ context.Context, n int) (int, error) {
		return n * 2, nil
	}, Workers(2))

	results := make(map[int]int)
	for r := range out {
		if r.Err != nil {
			t.Fatalf("unexpected error at index %d: %v", r.Index, r.Err)
		}
		results[r.Index] = r.Value
	}

	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(results))
	}

	for i := range 5 {
		if results[i] != i*2 {
			t.Errorf("result[%d] = %d, want %d", i, results[i], i*2)
		}
	}
}

func TestStream_BoundedConcurrency(t *testing.T) {
	const maxWorkers = 2
	var active, peak atomic.Int32

	in := make(chan int, 10)
	for i := range 10 {
		in <- i
	}
	close(in)

	out := Stream(context.Background(), in, func(_ context.Context, _ int) (int, error) {
		cur := active.Add(1)
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		active.Add(-1)
		return 0, nil
	}, Workers(maxWorkers))

	count := 0
	for range out {
		count++
	}

	if count != 10 {
		t.Fatalf("expected 10 results, got %d", count)
	}

	if p := int(peak.Load()); p > maxWorkers {
		t.Fatalf("peak concurrency %d exceeds Workers(%d)", p, maxWorkers)
	}
}

func TestStream_ClosedChannel(t *testing.T) {
	in := make(chan int)
	close(in)

	out := Stream(context.Background(), in, func(_ context.Context, n int) (int, error) {
		return n, nil
	})

	count := 0
	for range out {
		count++
	}

	if count != 0 {
		t.Fatalf("expected 0 results from closed channel, got %d", count)
	}
}
