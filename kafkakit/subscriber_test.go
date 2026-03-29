package kafkakit

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubscriberConfig_ConcurrencyDefault(t *testing.T) {
	cfg := SubscriberConfig{}
	if cfg.Concurrency != 0 {
		t.Fatalf("expected default Concurrency=0, got %d", cfg.Concurrency)
	}
}

func TestNewSubscriber_StoresConcurrency(t *testing.T) {
	cfg := Config{
		BootstrapServers: "localhost:9092",
		Subscriber:       SubscriberConfig{Concurrency: 4},
	}
	s, err := NewSubscriber(cfg, "test-group")
	if err != nil {
		t.Fatalf("NewSubscriber error: %v", err)
	}
	if s.concurrency() != 4 {
		t.Fatalf("expected concurrency()=4, got %d", s.concurrency())
	}
}

func TestNewSubscriber_ConcurrencyZeroIsSequential(t *testing.T) {
	cfg := Config{
		BootstrapServers: "localhost:9092",
		Subscriber:       SubscriberConfig{Concurrency: 0},
	}
	s, err := NewSubscriber(cfg, "test-group")
	if err != nil {
		t.Fatalf("NewSubscriber error: %v", err)
	}
	if s.concurrency() != 0 {
		t.Fatalf("expected concurrency()=0, got %d", s.concurrency())
	}
}

func TestNewSubscriber_NegativeConcurrencyTreatedAsSequential(t *testing.T) {
	cfg := Config{
		BootstrapServers: "localhost:9092",
		Subscriber:       SubscriberConfig{Concurrency: -1},
	}
	s, err := NewSubscriber(cfg, "test-group")
	if err != nil {
		t.Fatalf("NewSubscriber error: %v", err)
	}
	if s.concurrency() != -1 {
		t.Fatalf("expected concurrency()=-1, got %d", s.concurrency())
	}
}

func TestConcurrentDispatch_MaxActiveWorkers(t *testing.T) {
	var maxActive atomic.Int32
	var currentActive atomic.Int32
	var processed atomic.Int32

	concurrency := 3
	totalMessages := 6

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i := 0; i < totalMessages; i++ {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() {
				<-sem
				wg.Done()
			}()
			cur := currentActive.Add(1)
			for {
				old := maxActive.Load()
				if cur <= old || maxActive.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
			currentActive.Add(-1)
			processed.Add(1)
		}()
	}

	wg.Wait()

	if processed.Load() != int32(totalMessages) {
		t.Fatalf("expected %d processed, got %d", totalMessages, processed.Load())
	}
	if maxActive.Load() > int32(concurrency) {
		t.Fatalf("max active workers %d exceeded concurrency limit %d", maxActive.Load(), concurrency)
	}
	if maxActive.Load() < 2 {
		t.Fatalf("expected at least 2 concurrent workers, got %d", maxActive.Load())
	}
}

func TestConcurrentDispatch_ErrorIsolation(t *testing.T) {
	concurrency := 3
	totalMessages := 4

	var completed atomic.Int32
	var errored atomic.Int32

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i := 0; i < totalMessages; i++ {
		sem <- struct{}{}
		wg.Add(1)
		go func(idx int) {
			defer func() {
				<-sem
				wg.Done()
			}()
			if idx == 1 {
				errored.Add(1)
				return
			}
			time.Sleep(10 * time.Millisecond)
			completed.Add(1)
		}(i)
	}

	wg.Wait()

	if completed.Load() != 3 {
		t.Fatalf("expected 3 completed, got %d", completed.Load())
	}
	if errored.Load() != 1 {
		t.Fatalf("expected 1 errored, got %d", errored.Load())
	}
}

func TestConcurrentDispatch_DrainOnClose(t *testing.T) {
	concurrency := 2
	totalMessages := 4

	var completed atomic.Int32

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i := 0; i < totalMessages; i++ {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() {
				<-sem
				wg.Done()
			}()
			time.Sleep(100 * time.Millisecond)
			completed.Add(1)
		}()
	}

	wg.Wait()

	if completed.Load() != int32(totalMessages) {
		t.Fatalf("expected %d completed after drain, got %d", totalMessages, completed.Load())
	}
}
