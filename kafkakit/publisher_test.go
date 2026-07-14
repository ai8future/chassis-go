package kafkakit

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
)

func init() {
	chassis.RequireMajor(11)
}

func TestPublishBatchReportsPerRecordFailures(t *testing.T) {
	pub, err := NewPublisher(Config{
		BootstrapServers: "127.0.0.1:1",
		Source:           "test-svc",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	events := []OutboundEvent{
		{Subject: "ai8.test.a", Data: map[string]string{"k": "1"}},
		{Subject: "ai8.test.b", Data: map[string]string{"k": "2"}},
	}
	err = pub.PublishBatch(ctx, events)

	var be *BatchError
	if !errors.As(err, &be) {
		t.Fatalf("expected *BatchError, got %T: %v", err, err)
	}
	if len(be.Failures) != 2 || be.Succeeded != 0 {
		t.Fatalf("expected 2 failures / 0 succeeded, got %+v", be)
	}
	if be.Failures[0].Subject == "" {
		t.Fatal("expected failure to carry its subject")
	}
	if be.Failures[0].Index < 0 || be.Failures[0].Index >= len(events) {
		t.Fatalf("failure index out of range: %+v", be.Failures[0])
	}
	if len(be.Unwrap()) != len(be.Failures) {
		t.Fatalf("expected Unwrap to expose %d errors, got %d", len(be.Failures), len(be.Unwrap()))
	}
}

func TestPublisherRetryBackoffIncreasesWithAttempt(t *testing.T) {
	base := 50 * time.Millisecond

	first := publisherRetryBackoff(base, 1)
	later := publisherRetryBackoff(base, 4)

	if first < base/2 || first > base {
		t.Fatalf("first backoff %v outside expected jitter range [%v,%v]", first, base/2, base)
	}
	minLater := 4 * base
	maxLater := 8 * base
	if later < minLater || later > maxLater {
		t.Fatalf("later backoff %v outside expected jitter range [%v,%v]", later, minLater, maxLater)
	}
}

func TestPublishMarshalFailureUpdatesErrorStatistics(t *testing.T) {
	pub := &Publisher{source: "test-source"}
	err := pub.Publish(context.Background(), "events.created", func() {})
	if err == nil || !strings.Contains(err.Error(), "marshal data") {
		t.Fatalf("Publish error = %v", err)
	}
	stats := pub.Stats()
	if stats.ErrorsTotal != 1 || stats.EventsPublishedTotal != 0 || !stats.LastEventPublished.IsZero() {
		t.Fatalf("Stats = %+v", stats)
	}
}

func TestPublishCancelledProduceUpdatesErrorStatistics(t *testing.T) {
	pub, err := NewPublisher(Config{BootstrapServers: "127.0.0.1:1", Source: "test-source"})
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = pub.Publish(ctx, "events.created", map[string]string{"id": "1"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Publish error = %v, want context.Canceled", err)
	}
	stats := pub.Stats()
	if stats.ErrorsTotal != 1 || stats.EventsPublishedTotal != 0 {
		t.Fatalf("Stats = %+v", stats)
	}
}

func TestPublisherStatsSnapshotReportsSuccessfulPublication(t *testing.T) {
	pub := &Publisher{}
	pub.stats.incPublished()

	stats := pub.Stats()
	if stats.EventsPublishedTotal != 1 || stats.ErrorsTotal != 0 || stats.LastEventPublished.IsZero() {
		t.Fatalf("Stats = %+v", stats)
	}
}

func TestBatchErrorFormatsAndUnwrapsFailures(t *testing.T) {
	one := errors.New("one")
	two := errors.New("two")
	err := &BatchError{Succeeded: 1, Failures: []BatchFailure{{Err: one}, {Err: two}}}

	if got := err.Error(); got != "kafkakit: 2 of 3 batch record(s) failed" {
		t.Fatalf("Error() = %q", got)
	}
	if unwrapped := err.Unwrap(); len(unwrapped) != 2 || !errors.Is(unwrapped[0], one) || unwrapped[1] != two {
		t.Fatalf("Unwrap() = %#v", unwrapped)
	}
}
