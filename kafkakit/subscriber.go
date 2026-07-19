package kafkakit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

var (
	errHandlerPanic     = errors.New("kafkakit: handler panicked")
	errRebalancePending = errors.New("kafkakit: rebalance pending")
	errBatchDrain       = errors.New("kafkakit: batch drain timed out")
)

type subscriberClient interface {
	PollRecords(context.Context, int) kgo.Fetches
	CommitRecords(context.Context, ...*kgo.Record) error
	AllowRebalance()
	ProduceSync(context.Context, ...*kgo.Record) kgo.ProduceResults
	Close()
}

type subscriberClientFactory func(...kgo.Opt) (subscriberClient, error)

// Subscriber consumes events from Kafka/Redpanda topics.
type Subscriber struct {
	tenantID      string
	filter        *TenantFilter
	handlers      map[string]HandlerFunc
	consumerGroup string
	healthy       atomic.Bool
	cfg           Config
	settings      subscriberSettings
	baseOptions   []kgo.Opt
	clientFactory subscriberClientFactory
	rebalanceWait chan struct{}

	mu          sync.RWMutex
	client      subscriberClient
	running     bool
	closed      bool
	runCancel   context.CancelFunc
	runDone     chan struct{}
	closeClient func()
}

// concurrency returns the configured concurrency level.
func (s *Subscriber) concurrency() int {
	return s.cfg.Subscriber.Concurrency
}

func (s *Subscriber) workerCount(partitions int) int {
	workers := s.concurrency()
	if workers <= 1 {
		workers = 1
	}
	if workers > partitions {
		workers = partitions
	}
	return workers
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

// NewSubscriber creates a Subscriber for the given consumer group. It
// validates all Kafka settings without connecting to a broker; the client is
// created by Start after subscription topics are known.
func NewSubscriber(cfg Config, consumerGroup string, opts ...SubscriberOption) (*Subscriber, error) {
	s := &Subscriber{
		tenantID:      cfg.TenantID,
		handlers:      make(map[string]HandlerFunc),
		consumerGroup: consumerGroup,
		cfg:           cfg,
		rebalanceWait: make(chan struct{}, 1),
		clientFactory: func(opts ...kgo.Opt) (subscriberClient, error) {
			return newKafkaClient(opts...)
		},
	}

	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	baseOptions, settings, err := buildSubscriberOptions(cfg, consumerGroup, s.tenantID)
	if err != nil {
		return nil, err
	}
	s.settings = settings
	s.baseOptions = baseOptions
	if settings.commitMode == CommitModeManualContiguous {
		s.baseOptions = append(s.baseOptions, kgo.OnPartitionsCallbackBlocked(func(context.Context, *kgo.Client) {
			s.signalRebalanceBlocked()
		}))
	}

	if s.filter == nil && cfg.TenantFilter.Enabled {
		s.filter = NewTenantFilter(s.tenantID)
	}
	return s, nil
}

func (s *Subscriber) signalRebalanceBlocked() {
	select {
	case s.rebalanceWait <- struct{}{}:
	default:
	}
}

func (s *Subscriber) clearRebalanceBlocked() {
	select {
	case <-s.rebalanceWait:
	default:
	}
}

// Subscribe registers a handler for a subject pattern. The pattern supports
// wildcard matching: "ai8.scanner.>" matches "ai8.scanner.gdelt.signal.surge".
func (s *Subscriber) Subscribe(pattern string, handler HandlerFunc) error {
	if strings.TrimSpace(pattern) == "" {
		return fmt.Errorf("kafkakit: subscription pattern is required")
	}
	if handler == nil {
		return fmt.Errorf("kafkakit: handler for %q is nil", pattern)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[pattern] = handler
	return nil
}

// SubscribeMulti registers multiple handlers at once.
func (s *Subscriber) SubscribeMulti(handlers map[string]HandlerFunc) error {
	patterns := make([]string, 0, len(handlers))
	for pattern := range handlers {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	for _, pattern := range patterns {
		if err := s.Subscribe(pattern, handlers[pattern]); err != nil {
			return err
		}
	}
	return nil
}

func (s *Subscriber) subscriptionTopics() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	set := make(map[string]struct{}, len(s.handlers))
	for pattern := range s.handlers {
		set[patternToTopic(pattern)] = struct{}{}
	}
	topics := make([]string, 0, len(set))
	for topic := range set {
		topics = append(topics, topic)
	}
	sort.Strings(topics)
	return topics
}

// Start begins consuming messages and blocks until ctx is cancelled or a
// non-durable Kafka outcome occurs. A Subscriber is a one-shot lifecycle:
// concurrent or post-Close Start calls return an error.
func (s *Subscriber) Start(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("kafkakit: start context is nil")
	}
	topics := s.subscriptionTopics()
	if len(topics) == 0 {
		return fmt.Errorf("kafkakit: no handlers registered")
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("kafkakit: subscriber is closed")
	}
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("kafkakit: subscriber is already running")
	}
	opts := append([]kgo.Opt(nil), s.baseOptions...)
	opts = append(opts, kgo.ConsumeTopics(topics...))
	client, err := s.clientFactory(opts...)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var closeOnce sync.Once
	closeClient := func() {
		closeOnce.Do(func() {
			if s.settings.commitMode == CommitModeManualContiguous {
				client.AllowRebalance()
			}
			client.Close()
		})
	}
	s.client = client
	s.running = true
	s.runCancel = cancel
	s.runDone = done
	s.closeClient = closeClient
	s.mu.Unlock()

	s.healthy.Store(true)
	defer s.finishRun(client, cancel, done, closeClient)

	slog.Info("kafkakit: subscriber started",
		"concurrency", s.workerCount(max(1, s.concurrency())),
		"maxPollRecords", s.settings.maxPollRecords,
		"commitMode", s.settings.commitMode,
	)

	if s.settings.commitMode == CommitModeManualContiguous {
		return s.runManual(runCtx, client)
	}
	return s.runLegacy(runCtx, client)
}

func (s *Subscriber) finishRun(client subscriberClient, cancel context.CancelFunc, done chan struct{}, closeClient func()) {
	cancel()
	s.healthy.Store(false)
	closeClient()
	s.mu.Lock()
	if s.client == client {
		s.client = nil
	}
	s.running = false
	s.closed = true
	s.runCancel = nil
	s.closeClient = nil
	close(done)
	s.mu.Unlock()
}

func (s *Subscriber) runManual(ctx context.Context, client subscriberClient) error {
	for {
		fetches := client.PollRecords(ctx, s.settings.maxPollRecords)
		if err := ctx.Err(); err != nil {
			client.AllowRebalance()
			s.clearRebalanceBlocked()
			return err
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			client.AllowRebalance()
			s.clearRebalanceBlocked()
			return fmt.Errorf("kafkakit: fetch %s[%d]: %w", errs[0].Topic, errs[0].Partition, errs[0].Err)
		}

		err := s.processManualBatch(ctx, client, fetches)
		// BlockRebalanceOnPoll requires one release for every fully owned poll
		// batch, including a batch that ended in a non-durable outcome.
		client.AllowRebalance()
		s.clearRebalanceBlocked()
		if errors.Is(err, errRebalancePending) {
			continue
		}
		if err != nil {
			return err
		}
	}
}

func (s *Subscriber) runLegacy(ctx context.Context, client subscriberClient) error {
	for {
		fetches := client.PollRecords(ctx, s.settings.maxPollRecords)
		if err := ctx.Err(); err != nil {
			return err
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			return fmt.Errorf("kafkakit: fetch %s[%d]: %w", errs[0].Topic, errs[0].Partition, errs[0].Err)
		}
		_, err := s.processPartitions(ctx, client, fetches)
		if err != nil {
			return err
		}
	}
}

type topicPartition struct {
	topic     string
	partition int32
}

type partitionWork struct {
	key     topicPartition
	records []*kgo.Record
}

type partitionResult struct {
	key         topicPartition
	lastDurable *kgo.Record
	err         error
}

func partitionWorkFromFetches(fetches kgo.Fetches) []partitionWork {
	work := make([]partitionWork, 0)
	for _, fetch := range fetches {
		for _, topic := range fetch.Topics {
			for _, partition := range topic.Partitions {
				if len(partition.Records) == 0 {
					continue
				}
				records := partition.Records
				sort.SliceStable(records, func(i, j int) bool { return records[i].Offset < records[j].Offset })
				work = append(work, partitionWork{
					key:     topicPartition{topic: topic.Topic, partition: partition.Partition},
					records: records,
				})
			}
		}
	}
	sort.Slice(work, func(i, j int) bool {
		if work[i].key.topic == work[j].key.topic {
			return work[i].key.partition < work[j].key.partition
		}
		return work[i].key.topic < work[j].key.topic
	})
	return work
}

func (s *Subscriber) processManualBatch(ctx context.Context, client subscriberClient, fetches kgo.Fetches) error {
	processingCtx, cancelProcessing := context.WithCancelCause(ctx)
	watchDone := make(chan struct{})
	go func() {
		select {
		case <-s.rebalanceWait:
			cancelProcessing(errRebalancePending)
		case <-watchDone:
		}
	}()
	results, processingErr := s.processPartitions(processingCtx, client, fetches)
	close(watchDone)
	cancelProcessing(nil)
	commits := make([]*kgo.Record, 0, len(results))
	for _, result := range results {
		if result.lastDurable != nil {
			commits = append(commits, result.lastDurable)
		}
	}
	sort.Slice(commits, func(i, j int) bool {
		if commits[i].Topic == commits[j].Topic {
			return commits[i].Partition < commits[j].Partition
		}
		return commits[i].Topic < commits[j].Topic
	})
	if len(commits) > 0 {
		// franz-go CommitRecords advances each supplied record offset by one;
		// passing the last durable record therefore commits the exact next offset.
		if err := client.CommitRecords(ctx, commits...); err != nil {
			return fmt.Errorf("kafkakit: commit durable offsets: %w", err)
		}
	}
	return processingErr
}

func (s *Subscriber) processPartitions(ctx context.Context, client subscriberClient, fetches kgo.Fetches) ([]partitionResult, error) {
	work := partitionWorkFromFetches(fetches)
	if len(work) == 0 {
		return nil, nil
	}

	jobs := make(chan partitionWork, len(work))
	results := make(chan partitionResult, len(work))
	for _, item := range work {
		jobs <- item
	}
	close(jobs)

	workers := s.workerCount(len(work))
	for i := 0; i < workers; i++ {
		go func() {
			for item := range jobs {
				results <- s.processPartition(ctx, client, item)
			}
		}()
	}

	out := make([]partitionResult, 0, len(work))
	var firstErr error
	cancelled := false
	var drainTimer *time.Timer
	for len(out) < len(work) {
		if !cancelled {
			select {
			case result := <-results:
				out = append(out, result)
				if firstErr == nil && result.err != nil {
					firstErr = result.err
				}
			case <-ctx.Done():
				cancelled = true
				firstErr = context.Cause(ctx)
				drainTimer = time.NewTimer(s.settings.drainTimeout)
			}
			continue
		}
		select {
		case result := <-results:
			out = append(out, result)
		case <-drainTimer.C:
			return out, fmt.Errorf("%w after %s (cause: %v)", errBatchDrain, s.settings.drainTimeout, context.Cause(ctx))
		}
	}
	if drainTimer != nil {
		drainTimer.Stop()
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].key.topic == out[j].key.topic {
			return out[i].key.partition < out[j].key.partition
		}
		return out[i].key.topic < out[j].key.topic
	})
	return out, firstErr
}

func (s *Subscriber) processPartition(ctx context.Context, client subscriberClient, work partitionWork) partitionResult {
	result := partitionResult{key: work.key}
	for _, record := range work.records {
		if cause := context.Cause(ctx); cause != nil {
			result.err = cause
			return result
		}
		if err := s.processRecord(ctx, client, record); err != nil {
			result.err = err
			return result
		}
		result.lastDurable = record
	}
	return result
}

type recordFailure struct {
	reason    string
	panicType string
}

func (s *Subscriber) processRecord(ctx context.Context, client subscriberClient, record *kgo.Record) error {
	env, err := unwrapEnvelope(record.Value)
	if err != nil {
		return s.deadLetter(ctx, client, record, record.Topic, recordFailure{reason: "malformed envelope"})
	}

	evt := envelopeToEvent(env)
	evt.Key = string(append([]byte(nil), record.Key...))
	evt.headers = make(map[string]string, len(record.Headers))
	for _, header := range record.Headers {
		evt.headers[header.Key] = string(header.Value)
	}

	if s.filter != nil && !s.filter.ShouldDeliver(evt.TenantID) {
		return nil
	}

	handler := s.selectHandler(evt.Subject)
	if handler == nil {
		return s.deadLetter(ctx, client, record, evt.Subject, recordFailure{reason: "missing handler"})
	}

	panicType, handlerErr := callHandler(ctx, handler, evt)
	if handlerErr == nil {
		return nil
	}
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	failure := recordFailure{reason: "handler error", panicType: panicType}
	if errors.Is(handlerErr, errHandlerPanic) {
		failure.reason = "handler panic"
	}
	return s.deadLetter(ctx, client, record, evt.Subject, failure)
}

func callHandler(ctx context.Context, handler HandlerFunc, evt Event) (panicType string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = errHandlerPanic
			panicType = fmt.Sprintf("%T", recovered)
		}
	}()
	return "", handler(ctx, evt)
}

type deadLetterHeader struct {
	Key   string `json:"key"`
	Value []byte `json:"value"`
}

type deadLetterPayload struct {
	OriginalTopic     string             `json:"original_topic"`
	OriginalPartition int32              `json:"original_partition"`
	OriginalOffset    int64              `json:"original_offset"`
	OriginalKey       []byte             `json:"original_key,omitempty"`
	OriginalHeaders   []deadLetterHeader `json:"original_headers,omitempty"`
	OriginalValue     []byte             `json:"original_value"`
	FailureReason     string             `json:"failure_reason"`
	PanicType         string             `json:"panic_type,omitempty"`
}

func (s *Subscriber) deadLetter(ctx context.Context, client subscriberClient, record *kgo.Record, subject string, failure recordFailure) error {
	if client == nil {
		return fmt.Errorf("kafkakit: DLQ unavailable for %s[%d] offset %d", record.Topic, record.Partition, record.Offset)
	}
	headers := make([]deadLetterHeader, 0, len(record.Headers))
	for _, header := range record.Headers {
		headers = append(headers, deadLetterHeader{Key: header.Key, Value: append([]byte(nil), header.Value...)})
	}
	payload := deadLetterPayload{
		OriginalTopic:     record.Topic,
		OriginalPartition: record.Partition,
		OriginalOffset:    record.Offset,
		OriginalKey:       append([]byte(nil), record.Key...),
		OriginalHeaders:   headers,
		OriginalValue:     append([]byte(nil), record.Value...),
		FailureReason:     failure.reason,
		PanicType:         failure.panicType,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("kafkakit: marshal DLQ metadata: %w", err)
	}
	if subject == "" {
		subject = record.Topic
	}
	dlqCtx, cancel := context.WithTimeout(ctx, s.settings.dlqTimeout)
	defer cancel()
	results := client.ProduceSync(dlqCtx, &kgo.Record{
		Topic: dlqTopic(subject),
		Key:   append([]byte(nil), record.Key...),
		Value: data,
	})
	if err := results.FirstErr(); err != nil {
		return fmt.Errorf("kafkakit: DLQ publish failed for %s[%d] offset %d: %w", record.Topic, record.Partition, record.Offset, err)
	}
	return nil
}

// handleRecord is retained as a package test seam. It reports whether the
// record reached a durable handler or DLQ outcome.
func (s *Subscriber) handleRecord(ctx context.Context, record *kgo.Record) bool {
	s.mu.RLock()
	client := s.client
	s.mu.RUnlock()
	return s.processRecord(ctx, client, record) == nil
}

func (s *Subscriber) selectHandler(subject string) HandlerFunc {
	s.mu.RLock()
	defer s.mu.RUnlock()

	bestPattern := ""
	bestScore := -1
	var handler HandlerFunc
	for pattern, candidate := range s.handlers {
		if !matchPattern(pattern, subject) {
			continue
		}
		score := patternSpecificity(pattern)
		if handler == nil || score > bestScore || (score == bestScore && pattern < bestPattern) {
			bestPattern = pattern
			bestScore = score
			handler = candidate
		}
	}
	return handler
}

func patternSpecificity(pattern string) int {
	if strings.HasSuffix(pattern, ">") {
		return len(strings.TrimSuffix(pattern, ">")) * 2
	}
	return len(pattern)*2 + 1
}

// Close cancels consumption and waits for the active batch to drain. It is
// idempotent and safe before, during, or after Start.
func (s *Subscriber) Close() error {
	s.mu.Lock()
	if s.closed && !s.running {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	cancel := s.runCancel
	done := s.runDone
	closeClient := s.closeClient
	drainTimeout := s.settings.drainTimeout
	s.mu.Unlock()

	s.healthy.Store(false)
	if cancel == nil || done == nil {
		return nil
	}
	cancel()
	timer := time.NewTimer(drainTimeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		if closeClient != nil {
			closeClient()
		}
		return fmt.Errorf("kafkakit: subscriber close exceeded %s", drainTimeout)
	}
}

// Healthy returns whether the subscriber is actively consuming.
func (s *Subscriber) Healthy() bool {
	return s.healthy.Load()
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
func patternToTopic(pattern string) string {
	if strings.HasSuffix(pattern, ">") {
		return strings.TrimSuffix(pattern, ".>")
	}
	return pattern
}
