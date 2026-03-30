package kafkakit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Subscriber consumes events from Kafka/Redpanda topics.
type Subscriber struct {
	client        *kgo.Client
	tenantID      string
	filter        *TenantFilter
	handlers      map[string]HandlerFunc
	consumerGroup string
	healthy       atomic.Bool
	mu            sync.RWMutex
	cfg           Config
	wg            sync.WaitGroup // tracks in-flight handlers; used by AtLeastOnce revoke callback and shutdown drain
}

// concurrency returns the configured concurrency level.
func (s *Subscriber) concurrency() int {
	return s.cfg.Subscriber.Concurrency
}

// SubscriberOption configures a Subscriber.
type SubscriberOption func(*Subscriber)

// WithTenant sets the tenant ID for tenant-based filtering.
func WithTenant(tenantID string) SubscriberOption {
	return func(s *Subscriber) {
		s.tenantID = tenantID
		s.filter = NewTenantFilter(tenantID)
	}
}

// NewSubscriber creates a Subscriber for the given consumer group.
func NewSubscriber(cfg Config, consumerGroup string, opts ...SubscriberOption) (*Subscriber, error) {
	if !cfg.Enabled() {
		return nil, fmt.Errorf("kafkakit: BootstrapServers is required")
	}
	if consumerGroup == "" {
		return nil, fmt.Errorf("kafkakit: consumerGroup is required")
	}

	s := &Subscriber{
		tenantID:      cfg.TenantID,
		handlers:      make(map[string]HandlerFunc),
		consumerGroup: consumerGroup,
		cfg:           cfg,
	}

	for _, opt := range opts {
		opt(s)
	}

	// Set up tenant filter if not already set by options and tenant filtering is enabled
	if s.filter == nil && cfg.TenantFilter.Enabled && cfg.TenantID != "" {
		s.filter = NewTenantFilter(cfg.TenantID)
	}

	return s, nil
}

// Subscribe registers a handler for a subject pattern. The pattern supports
// wildcard matching: "ai8.scanner.>" matches "ai8.scanner.gdelt.signal.surge".
func (s *Subscriber) Subscribe(pattern string, handler HandlerFunc) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[pattern] = handler
	return nil
}

// SubscribeMulti registers multiple handlers at once.
func (s *Subscriber) SubscribeMulti(handlers map[string]HandlerFunc) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for pattern, handler := range handlers {
		s.handlers[pattern] = handler
	}
	return nil
}

// Start begins consuming messages. It blocks until ctx is cancelled.
// The Kafka client is created at start time so that all subscribed topics
// are known.
func (s *Subscriber) Start(ctx context.Context) error {
	// Collect all topic names from registered patterns
	s.mu.RLock()
	topics := make([]string, 0, len(s.handlers))
	for pattern := range s.handlers {
		// For wildcard patterns, subscribe to the prefix topic
		// In practice, the caller should ensure topics exist
		topics = append(topics, patternToTopic(pattern))
	}
	s.mu.RUnlock()

	if len(topics) == 0 {
		return fmt.Errorf("kafkakit: no handlers registered")
	}

	opts := []kgo.Opt{
		kgo.SeedBrokers(strings.Split(s.cfg.BootstrapServers, ",")...),
		kgo.ConsumerGroup(s.consumerGroup),
		kgo.ConsumeTopics(topics...),
	}

	if s.cfg.Subscriber.AtLeastOnce {
		opts = append(opts,
			kgo.DisableAutoCommit(),
			kgo.OnPartitionsRevoked(func(ctx context.Context, cl *kgo.Client, _ map[string][]int32) {
				s.wg.Wait()
				if err := cl.CommitUncommittedOffsets(ctx); err != nil {
					slog.Error("kafkakit: revoke commit failed", "err", err)
				}
			}),
		)
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		return fmt.Errorf("kafkakit: create subscriber client: %w", err)
	}
	s.client = client
	s.healthy.Store(true)

	// Determine max records per poll: use config if set, otherwise scale with
	// concurrency so each poll returns enough records to saturate the worker pool.
	maxPoll := s.cfg.Subscriber.MaxPollRecords
	if s.concurrency() > 1 && maxPoll < s.concurrency() {
		maxPoll = s.concurrency() * 2 // 2x buffer to keep workers busy
	}

	// Create semaphore for concurrent dispatch.
	var sem chan struct{}
	if s.concurrency() > 1 {
		sem = make(chan struct{}, s.concurrency())
		slog.Info("kafkakit: subscriber started", "concurrency", s.concurrency(), "maxPollRecords", maxPoll, "atLeastOnce", s.cfg.Subscriber.AtLeastOnce)
	}

	defer func() {
		s.healthy.Store(false)
		s.wg.Wait() // drain in-flight workers before closing client
		// Commit any offsets for successfully processed messages before closing.
		// In AtLeastOnce mode this is essential; in auto-commit mode it's belt-and-suspenders.
		if err := s.client.CommitUncommittedOffsets(context.Background()); err != nil {
			slog.Error("kafkakit: final commit failed", "err", err)
		}
		s.client.Close()
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// PollRecords returns up to maxPoll records; 0 means unlimited (like PollFetches).
		fetches := s.client.PollRecords(ctx, maxPoll)
		if errs := fetches.Errors(); len(errs) > 0 {
			for _, e := range errs {
				// Context cancellation is not an error
				if ctx.Err() != nil {
					return ctx.Err()
				}
				slog.Error("kafkakit: fetch error", "topic", e.Topic, "partition", e.Partition, "err", e.Err)
			}
			continue
		}

		if s.cfg.Subscriber.AtLeastOnce {
			// Batch-and-wait: process all records, then commit offsets.
			if s.concurrency() <= 1 {
				fetches.EachRecord(func(record *kgo.Record) {
					s.handleRecord(ctx, record)
				})
			} else {
				fetches.EachRecord(func(record *kgo.Record) {
					sem <- struct{}{}
					s.wg.Add(1)
					go func() {
						defer func() {
							<-sem
							s.wg.Done()
						}()
						s.handleRecord(ctx, record)
					}()
				})
				s.wg.Wait()
			}
			if err := s.client.CommitUncommittedOffsets(ctx); err != nil {
				slog.Error("kafkakit: commit offsets failed", "err", err)
			}
		} else {
			// Rolling model: semaphore caps concurrency, next poll starts
			// immediately. Workers drain on shutdown via deferred s.wg.Wait().
			if s.concurrency() <= 1 {
				fetches.EachRecord(func(record *kgo.Record) {
					s.handleRecord(ctx, record)
				})
			} else {
				fetches.EachRecord(func(record *kgo.Record) {
					sem <- struct{}{}
					s.wg.Add(1)
					go func() {
						defer func() {
							<-sem
							s.wg.Done()
						}()
						s.handleRecord(ctx, record)
					}()
				})
			}
		}
	}
}

// handleRecord processes a single Kafka record through the handler pipeline.
func (s *Subscriber) handleRecord(ctx context.Context, record *kgo.Record) {
	env, err := unwrapEnvelope(record.Value)
	if err != nil {
		slog.Error("kafkakit: unwrap envelope failed", "topic", record.Topic, "err", err)
		return
	}

	evt := envelopeToEvent(env)

	// Tenant filtering
	if s.filter != nil && !s.filter.ShouldDeliver(evt.TenantID) {
		return // silently skip events for other tenants
	}

	// Find matching handler
	s.mu.RLock()
	var handler HandlerFunc
	for pattern, h := range s.handlers {
		if matchPattern(pattern, evt.Subject) {
			handler = h
			break
		}
	}
	s.mu.RUnlock()

	if handler == nil {
		slog.Warn("kafkakit: no handler for subject", "subject", evt.Subject)
		return
	}

	if err := handler(ctx, evt); err != nil {
		slog.Error("kafkakit: handler error", "subject", evt.Subject, "err", err)
		s.routeToDLQ(evt, err)
	}
}

// Close shuts down the subscriber.
func (s *Subscriber) Close() error {
	s.healthy.Store(false)
	if s.client != nil {
		s.client.Close()
	}
	return nil
}

// Healthy returns whether the subscriber is actively consuming.
func (s *Subscriber) Healthy() bool {
	return s.healthy.Load()
}

// routeToDLQ publishes a failed event to the dead letter queue topic.
// DLQ topic format: ai8._dlq.{original_subject}
func (s *Subscriber) routeToDLQ(evt Event, handlerErr error) {
	if s.client == nil {
		slog.Error("kafkakit: cannot route to DLQ, no client")
		return
	}

	dlq := dlqTopic(evt.Subject)

	dlqPayload := map[string]any{
		"original_event": evt,
		"error":          handlerErr.Error(),
	}
	data, err := json.Marshal(dlqPayload)
	if err != nil {
		slog.Error("kafkakit: marshal DLQ payload", "err", err)
		return
	}

	record := &kgo.Record{
		Topic: dlq,
		Value: data,
	}

	// Fire-and-forget to DLQ
	s.client.Produce(context.Background(), record, func(_ *kgo.Record, err error) {
		if err != nil {
			slog.Error("kafkakit: DLQ produce failed", "topic", dlq, "err", err)
		}
	})
}

// dlqTopic returns the dead letter queue topic for the given subject.
func dlqTopic(subject string) string {
	return "ai8._dlq." + subject
}

// matchPattern checks if a subject matches a pattern. The pattern can end with
// ">" which matches any remaining segments. Otherwise, exact match is required.
func matchPattern(pattern, subject string) bool {
	if pattern == subject {
		return true
	}
	if strings.HasSuffix(pattern, ">") {
		prefix := strings.TrimSuffix(pattern, ">")
		return strings.HasPrefix(subject, prefix)
	}
	return false
}

// patternToTopic converts a subscription pattern to a Kafka topic name.
// For wildcard patterns, returns the prefix without the trailing ">".
// In practice, topic routing depends on the Kafka setup.
func patternToTopic(pattern string) string {
	if strings.HasSuffix(pattern, ">") {
		// For wildcard patterns, we'd need regex-based topic subscription
		// or a single topic per service. For now, return the pattern as-is
		// since actual topic mapping depends on the deployment.
		return strings.TrimSuffix(pattern, ".>")
	}
	return pattern
}
