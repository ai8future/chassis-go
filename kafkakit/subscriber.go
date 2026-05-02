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
	closeOnce     sync.Once
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
			kgo.OnPartitionsRevoked(func(context.Context, *kgo.Client, map[string][]int32) {
				s.wg.Wait()
			}),
		)
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		return fmt.Errorf("kafkakit: create subscriber client: %w", err)
	}
	s.mu.Lock()
	s.client = client
	s.mu.Unlock()
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
		s.closeOnce.Do(s.doClose)
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
			handled := true
			records := []*kgo.Record{}
			if s.concurrency() <= 1 {
				fetches.EachRecord(func(record *kgo.Record) {
					records = append(records, record)
					if !s.handleRecord(ctx, record) {
						handled = false
					}
				})
			} else {
				var batchHandled atomic.Bool
				batchHandled.Store(true)
				fetches.EachRecord(func(record *kgo.Record) {
					records = append(records, record)
					sem <- struct{}{}
					s.wg.Add(1)
					go func() {
						defer func() {
							<-sem
							s.wg.Done()
						}()
						if !s.handleRecord(ctx, record) {
							batchHandled.Store(false)
						}
					}()
				})
				s.wg.Wait()
				handled = batchHandled.Load()
			}
			if !handled {
				slog.Error("kafkakit: skipping offset commit after non-durable handler failure")
				continue
			}
			if len(records) == 0 {
				continue
			}
			if err := s.client.CommitRecords(ctx, records...); err != nil {
				slog.Error("kafkakit: commit offsets failed", "err", err)
			}
		} else {
			// Rolling model: semaphore caps concurrency, next poll starts
			// immediately. Workers drain on shutdown via deferred s.wg.Wait().
			if s.concurrency() <= 1 {
				fetches.EachRecord(func(record *kgo.Record) {
					_ = s.handleRecord(ctx, record)
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
						_ = s.handleRecord(ctx, record)
					}()
				})
			}
		}
	}
}

// handleRecord processes a single Kafka record through the handler pipeline.
// It returns false only when processing failed in a way that must not be offset-committed.
func (s *Subscriber) handleRecord(ctx context.Context, record *kgo.Record) bool {
	env, err := unwrapEnvelope(record.Value)
	if err != nil {
		slog.Error("kafkakit: unwrap envelope failed", "topic", record.Topic, "err", err)
		return true
	}

	evt := envelopeToEvent(env)

	// Tenant filtering
	if s.filter != nil && !s.filter.ShouldDeliver(evt.TenantID) {
		return true // silently skip events for other tenants
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
		return true
	}

	if err := handler(ctx, evt); err != nil {
		slog.Error("kafkakit: handler error", "subject", evt.Subject, "err", err)
		if dlqErr := s.routeToDLQ(evt, err); dlqErr != nil {
			slog.Error("kafkakit: DLQ routing failed; leaving offset uncommitted", "subject", evt.Subject, "err", dlqErr)
			return false
		}
	}
	return true
}

// doClose performs the actual shutdown: marks unhealthy, drains in-flight
// handlers, commits offsets, and closes the client.
func (s *Subscriber) doClose() {
	s.healthy.Store(false)
	s.wg.Wait()
	s.mu.RLock()
	cl := s.client
	s.mu.RUnlock()
	if cl == nil {
		return
	}
	if !s.cfg.Subscriber.AtLeastOnce {
		if err := cl.CommitUncommittedOffsets(context.Background()); err != nil {
			slog.Error("kafkakit: final commit failed", "err", err)
		}
	}
	cl.Close()
	s.mu.Lock()
	if s.client == cl {
		s.client = nil
	}
	s.mu.Unlock()
}

// Close shuts down the subscriber.
func (s *Subscriber) Close() error {
	s.closeOnce.Do(s.doClose)
	return nil
}

// Healthy returns whether the subscriber is actively consuming.
func (s *Subscriber) Healthy() bool {
	return s.healthy.Load()
}

// routeToDLQ publishes a failed event to the dead letter queue topic.
// DLQ topic format: ai8._dlq.{original_subject}
func (s *Subscriber) routeToDLQ(evt Event, handlerErr error) error {
	s.mu.RLock()
	cl := s.client
	s.mu.RUnlock()
	if cl == nil {
		return fmt.Errorf("no kafka client")
	}

	dlq := dlqTopic(evt.Subject)

	dlqPayload := map[string]any{
		"original_event": evt,
		"error":          handlerErr.Error(),
	}
	data, err := json.Marshal(dlqPayload)
	if err != nil {
		return fmt.Errorf("marshal DLQ payload: %w", err)
	}

	record := &kgo.Record{
		Topic: dlq,
		Value: data,
	}

	results := cl.ProduceSync(context.Background(), record)
	if err := results.FirstErr(); err != nil {
		return fmt.Errorf("produce DLQ record to %s: %w", dlq, err)
	}
	return nil
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
