package kafkakit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/twmb/franz-go/pkg/kgo"
)

func init() {
	chassis.RequireMajor(11)
}

type fakePublisherClient struct {
	records []*kgo.Record
	pingErr error
}

func (f *fakePublisherClient) ProduceSync(_ context.Context, records ...*kgo.Record) kgo.ProduceResults {
	f.records = append(f.records, records...)
	results := make(kgo.ProduceResults, len(records))
	for i, record := range records {
		results[i] = kgo.ProduceResult{Record: record}
	}
	return results
}

func (f *fakePublisherClient) Ping(context.Context) error { return f.pingErr }

func (f *fakePublisherClient) Close() {}

func TestPublishKeyedCopiesKeyAndPreservesEnvelope(t *testing.T) {
	client := &fakePublisherClient{}
	pub := &Publisher{client: client, source: "email-ai-svc", tenantID: "tenant-1"}
	key := []byte("message-42")
	data := map[string]any{"attempt": 1, "status": "queued"}

	if err := pub.PublishKeyed(context.Background(), "email.requested", key, data); err != nil {
		t.Fatal(err)
	}
	if len(client.records) != 1 {
		t.Fatalf("produced records = %d, want 1", len(client.records))
	}
	record := client.records[0]
	if record.Topic != "email.requested" || !bytes.Equal(record.Key, []byte("message-42")) {
		t.Fatalf("record topic/key = %q/%q", record.Topic, record.Key)
	}

	key[0] = 'X'
	if got := string(record.Key); got != "message-42" {
		t.Fatalf("record key aliased caller key: %q", got)
	}
	record.Key[1] = 'Y'
	if got := string(key); got != "Xessage-42" {
		t.Fatalf("caller key aliased record key: %q", got)
	}

	var got envelope
	if err := json.Unmarshal(record.Value, &got); err != nil {
		t.Fatalf("unmarshal produced envelope: %v", err)
	}
	dataBytes, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, err := json.Marshal(&envelope{
		ID:         got.ID,
		Timestamp:  got.Timestamp,
		Source:     "email-ai-svc",
		Subject:    "email.requested",
		TraceID:    "",
		TenantID:   "tenant-1",
		Version:    "1.0",
		EntityRefs: []string{},
		Data:       dataBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(record.Value, wantBytes) {
		t.Fatalf("envelope changed:\n got: %s\nwant: %s", record.Value, wantBytes)
	}
}

func TestPublishRemainsUnkeyed(t *testing.T) {
	client := &fakePublisherClient{}
	pub := &Publisher{client: client, source: "email-ai-svc"}

	if err := pub.Publish(context.Background(), "email.requested", map[string]string{"id": "42"}); err != nil {
		t.Fatal(err)
	}
	if len(client.records) != 1 || client.records[0].Key != nil {
		t.Fatalf("Publish record key = %#v, want nil", client.records[0].Key)
	}
}

func TestPublisherPingWrapsBrokerError(t *testing.T) {
	brokerErr := errors.New("broker unavailable")
	pub := &Publisher{client: &fakePublisherClient{pingErr: brokerErr}}

	err := pub.Ping(context.Background())
	if !errors.Is(err, brokerErr) || !strings.Contains(err.Error(), "kafkakit: ping brokers") {
		t.Fatalf("Ping error = %v", err)
	}

	pub.client = &fakePublisherClient{}
	if err := pub.Ping(context.Background()); err != nil {
		t.Fatalf("successful Ping error = %v", err)
	}
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
