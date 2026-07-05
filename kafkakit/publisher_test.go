package kafkakit

import (
	"context"
	"errors"
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
