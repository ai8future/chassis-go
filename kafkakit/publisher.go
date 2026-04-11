package kafkakit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

// NewPublisher creates a Publisher connected to the configured Kafka brokers.
// Source identity is taken from Config.Source.
func NewPublisher(cfg Config) (*Publisher, error) {
	if !cfg.Enabled() {
		return nil, fmt.Errorf("kafkakit: BootstrapServers is required")
	}
	if cfg.Source == "" {
		return nil, fmt.Errorf("kafkakit: Source is required")
	}

	opts := []kgo.Opt{
		kgo.SeedBrokers(strings.Split(cfg.BootstrapServers, ",")...),
	}

	// Apply publisher-specific settings
	if cfg.Publisher.MaxRetries > 0 {
		backoffMs := cfg.Publisher.RetryBackoffMs
		opts = append(opts, kgo.RetryBackoffFn(func(int) time.Duration {
			return time.Duration(backoffMs) * time.Millisecond
		}))
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("kafkakit: create kafka client: %w", err)
	}

	return &Publisher{
		client:   client,
		source:   cfg.Source,
		tenantID: cfg.TenantID,
	}, nil
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

	results := p.client.ProduceSync(ctx, records...)
	for i, r := range results {
		if r.Err != nil {
			p.stats.incErrors()
			return fmt.Errorf("kafkakit: produce record %d failed: %w", i, r.Err)
		}
		p.stats.incPublished()
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
