package kafkakit

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Publisher sends events to Kafka/Redpanda topics.
type Publisher struct {
	client   *kgo.Client
	source   string // from Config.Source
	tenantID string
	stats    publisherStats
}

// BatchFailure records one failed record in a batch publish.
type BatchFailure struct {
	Index   int
	Subject string
	Err     error
}

// BatchError reports per-record outcomes of PublishBatch. Records not listed
// in Failures were durably produced; retry only failed records to avoid
// duplicate publishes.
type BatchError struct {
	Failures  []BatchFailure
	Succeeded int
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("kafkakit: %d of %d batch record(s) failed", len(e.Failures), e.Succeeded+len(e.Failures))
}

func (e *BatchError) Unwrap() []error {
	out := make([]error, len(e.Failures))
	for i, failure := range e.Failures {
		out[i] = failure.Err
	}
	return out
}

// NewPublisher creates a Publisher connected to the configured Kafka brokers.
// Source identity is taken from Config.Source.
func NewPublisher(cfg Config) (*Publisher, error) {
	opts, err := buildPublisherOptions(cfg)
	if err != nil {
		return nil, err
	}
	client, err := newKafkaClient(opts...)
	if err != nil {
		return nil, err
	}

	return &Publisher{
		client:   client,
		source:   cfg.Source,
		tenantID: cfg.TenantID,
	}, nil
}

func publisherRetryBackoff(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = 100 * time.Millisecond
	}
	if attempt < 1 {
		attempt = 1
	}
	shift := attempt - 1
	if shift > 6 {
		shift = 6
	}
	delay := base << shift
	half := delay / 2
	return half + time.Duration(rand.Int64N(int64(half)+1))
}

// Publish sends a single event to the topic derived from the subject.
// Source is always taken from the publisher's config, never from parameters.
func (p *Publisher) Publish(ctx context.Context, subject string, data any) error {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		p.stats.incErrors()
		return fmt.Errorf("kafkakit: marshal data: %w", err)
	}

	env, err := wrapEnvelope(ctx, p.source, subject, p.tenantID, nil, dataBytes)
	if err != nil {
		p.stats.incErrors()
		return fmt.Errorf("kafkakit: wrap envelope: %w", err)
	}

	envBytes, err := json.Marshal(env)
	if err != nil {
		p.stats.incErrors()
		return fmt.Errorf("kafkakit: marshal envelope: %w", err)
	}

	// Use subject as topic name (dots are valid in Kafka topic names)
	record := &kgo.Record{
		Topic: subject,
		Value: envBytes,
	}

	// Synchronous produce
	results := p.client.ProduceSync(ctx, record)
	if err := results.FirstErr(); err != nil {
		p.stats.incErrors()
		return fmt.Errorf("kafkakit: produce to %s: %w", subject, err)
	}

	p.stats.incPublished()
	return nil
}

// PublishBatch sends multiple events. Each OutboundEvent is published to its
// respective subject topic. All events are produced synchronously.
func (p *Publisher) PublishBatch(ctx context.Context, events []OutboundEvent) error {
	records := make([]*kgo.Record, 0, len(events))

	for _, evt := range events {
		dataBytes, err := json.Marshal(evt.Data)
		if err != nil {
			p.stats.incErrors()
			return fmt.Errorf("kafkakit: marshal data for %s: %w", evt.Subject, err)
		}

		env, err := wrapEnvelope(ctx, p.source, evt.Subject, p.tenantID, evt.EntityRefs, dataBytes)
		if err != nil {
			p.stats.incErrors()
			return fmt.Errorf("kafkakit: wrap envelope for %s: %w", evt.Subject, err)
		}

		envBytes, err := json.Marshal(env)
		if err != nil {
			p.stats.incErrors()
			return fmt.Errorf("kafkakit: marshal envelope for %s: %w", evt.Subject, err)
		}

		records = append(records, &kgo.Record{
			Topic: evt.Subject,
			Value: envBytes,
		})
	}

	recordIndex := make(map[*kgo.Record]int, len(records))
	for i, record := range records {
		recordIndex[record] = i
	}

	results := p.client.ProduceSync(ctx, records...)
	failures := make([]BatchFailure, 0)
	var succeeded int
	for _, r := range results {
		if r.Err != nil {
			p.stats.incErrors()
			idx := recordIndex[r.Record]
			failures = append(failures, BatchFailure{
				Index:   idx,
				Subject: events[idx].Subject,
				Err:     r.Err,
			})
			continue
		}
		p.stats.incPublished()
		succeeded++
	}
	if len(failures) > 0 {
		return &BatchError{Failures: failures, Succeeded: succeeded}
	}
	return nil
}

// Close shuts down the publisher and flushes any pending messages.
func (p *Publisher) Close() error {
	p.client.Close()
	return nil
}

// Stats returns a snapshot of publisher statistics.
func (p *Publisher) Stats() Stats {
	return p.stats.snapshot()
}
