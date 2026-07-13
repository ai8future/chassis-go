package kafkakit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/twmb/franz-go/pkg/kgo"
)

func init() {
	chassis.RequireMajor(11)
}

type fakeSubscriberClient struct {
	mu sync.Mutex

	pollFn    func(context.Context, int) kgo.Fetches
	produceFn func(context.Context, ...*kgo.Record) kgo.ProduceResults
	commitErr error

	pollMax  []int
	commits  [][]*kgo.Record
	produced []*kgo.Record
	allows   int
	closes   int
	events   []string
}

func (f *fakeSubscriberClient) PollRecords(ctx context.Context, maxRecords int) kgo.Fetches {
	f.mu.Lock()
	f.pollMax = append(f.pollMax, maxRecords)
	fn := f.pollFn
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, maxRecords)
	}
	<-ctx.Done()
	return nil
}

func (f *fakeSubscriberClient) CommitRecords(_ context.Context, records ...*kgo.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	copyRecords := append([]*kgo.Record(nil), records...)
	f.commits = append(f.commits, copyRecords)
	f.events = append(f.events, "commit")
	return f.commitErr
}

func (f *fakeSubscriberClient) AllowRebalance() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.allows++
	f.events = append(f.events, "allow")
}

func (f *fakeSubscriberClient) ProduceSync(ctx context.Context, records ...*kgo.Record) kgo.ProduceResults {
	f.mu.Lock()
	for _, record := range records {
		f.produced = append(f.produced, &kgo.Record{
			Topic: record.Topic,
			Key:   append([]byte(nil), record.Key...),
			Value: append([]byte(nil), record.Value...),
		})
	}
	fn := f.produceFn
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, records...)
	}
	results := make(kgo.ProduceResults, len(records))
	for i, record := range records {
		results[i] = kgo.ProduceResult{Record: record}
	}
	return results
}

func (f *fakeSubscriberClient) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	f.events = append(f.events, "close")
}

func (f *fakeSubscriberClient) snapshot() (pollMax []int, commits [][]*kgo.Record, produced []*kgo.Record, events []string, allows, closes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.pollMax...), append([][]*kgo.Record(nil), f.commits...), append([]*kgo.Record(nil), f.produced...), append([]string(nil), f.events...), f.allows, f.closes
}

func newTestSubscriber(t *testing.T, cfg SubscriberConfig) *Subscriber {
	t.Helper()
	s, err := NewSubscriber(Config{BootstrapServers: "localhost:9092", Subscriber: cfg}, "test-group")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func deliveryRecord(t *testing.T, topic, subject string, partition int32, offset int64, headers ...kgo.RecordHeader) *kgo.Record {
	t.Helper()
	env, err := wrapEnvelope(context.Background(), "test-source", subject, "", nil, []byte(fmt.Sprintf(`{"partition":%d,"offset":%d}`, partition, offset)))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return &kgo.Record{
		Topic:     topic,
		Partition: partition,
		Offset:    offset,
		Key:       []byte(fmt.Sprintf("key-%d-%d", partition, offset)),
		Headers:   headers,
		Value:     raw,
	}
}

func fetchBatch(records ...*kgo.Record) kgo.Fetches {
	byTopic := make(map[string]map[int32][]*kgo.Record)
	for _, record := range records {
		partitions := byTopic[record.Topic]
		if partitions == nil {
			partitions = make(map[int32][]*kgo.Record)
			byTopic[record.Topic] = partitions
		}
		partitions[record.Partition] = append(partitions[record.Partition], record)
	}
	topics := make([]kgo.FetchTopic, 0, len(byTopic))
	for topic, partitions := range byTopic {
		fetchPartitions := make([]kgo.FetchPartition, 0, len(partitions))
		for partition, partitionRecords := range partitions {
			fetchPartitions = append(fetchPartitions, kgo.FetchPartition{Partition: partition, Records: partitionRecords})
		}
		topics = append(topics, kgo.FetchTopic{Topic: topic, Partitions: fetchPartitions})
	}
	return kgo.Fetches{{Topics: topics}}
}

func TestProcessRecordPopulatesHeadersWithLastValue(t *testing.T) {
	s := newTestSubscriber(t, SubscriberConfig{})
	var got string
	if err := s.Subscribe("orders.created", func(_ context.Context, evt Event) error {
		got = evt.Header("trace")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	record := deliveryRecord(t, "orders.created", "orders.created", 0, 0,
		kgo.RecordHeader{Key: "trace", Value: []byte("first")},
		kgo.RecordHeader{Key: "trace", Value: []byte("last")},
	)
	if err := s.processRecord(context.Background(), &fakeSubscriberClient{}, record); err != nil {
		t.Fatal(err)
	}
	if got != "last" {
		t.Fatalf("Header(trace) = %q, want last", got)
	}
	if (&Event{}).Header("missing") != "" || (*Event)(nil).Header("missing") != "" {
		t.Fatal("missing headers must be empty and nil-safe")
	}
}

func TestPoisonRecordsUseMetadataPreservingDLQ(t *testing.T) {
	tests := []struct {
		name       string
		record     func(*testing.T) *kgo.Record
		configure  func(*testing.T, *Subscriber)
		wantReason string
		wantPanic  string
	}{
		{
			name: "malformed",
			record: func(t *testing.T) *kgo.Record {
				return &kgo.Record{Topic: "raw-topic", Partition: 2, Offset: 9, Key: []byte("key"), Headers: []kgo.RecordHeader{{Key: "h", Value: []byte("v")}}, Value: []byte("not-json")}
			},
			wantReason: "malformed envelope",
		},
		{
			name: "missing_handler",
			record: func(t *testing.T) *kgo.Record {
				return deliveryRecord(t, "raw-topic", "orders.missing", 2, 9, kgo.RecordHeader{Key: "h", Value: []byte("v")})
			},
			wantReason: "missing handler",
		},
		{
			name: "handler_panic",
			record: func(t *testing.T) *kgo.Record {
				return deliveryRecord(t, "raw-topic", "orders.panic", 2, 9, kgo.RecordHeader{Key: "h", Value: []byte("v")})
			},
			configure: func(t *testing.T, s *Subscriber) {
				if err := s.Subscribe("orders.panic", func(context.Context, Event) error { panic("secret panic value") }); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: "handler panic",
			wantPanic:  "string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestSubscriber(t, SubscriberConfig{})
			if tt.configure != nil {
				tt.configure(t, s)
			}
			client := &fakeSubscriberClient{}
			record := tt.record(t)
			if err := s.processRecord(context.Background(), client, record); err != nil {
				t.Fatalf("durable DLQ should resolve poison record: %v", err)
			}
			_, _, produced, _, _, _ := client.snapshot()
			if len(produced) != 1 {
				t.Fatalf("produced %d DLQ records, want 1", len(produced))
			}
			var payload deadLetterPayload
			if err := json.Unmarshal(produced[0].Value, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.OriginalTopic != record.Topic || payload.OriginalPartition != record.Partition || payload.OriginalOffset != record.Offset || !reflect.DeepEqual(payload.OriginalKey, record.Key) || !reflect.DeepEqual(payload.OriginalValue, record.Value) {
				t.Fatalf("DLQ metadata did not preserve original record: %+v", payload)
			}
			if len(payload.OriginalHeaders) != len(record.Headers) || payload.FailureReason != tt.wantReason || payload.PanicType != tt.wantPanic {
				t.Fatalf("DLQ failure metadata = %+v", payload)
			}
			if tt.wantPanic != "" && bytes.Contains(produced[0].Value, []byte("secret panic value")) {
				t.Fatal("panic value leaked into DLQ metadata")
			}
		})
	}
}

func TestDLQPublicationIsBoundedAndCallerCancelled(t *testing.T) {
	s := newTestSubscriber(t, SubscriberConfig{DLQTimeoutMs: 20})
	client := &fakeSubscriberClient{produceFn: func(ctx context.Context, records ...*kgo.Record) kgo.ProduceResults {
		<-ctx.Done()
		return kgo.ProduceResults{{Record: records[0], Err: ctx.Err()}}
	}}
	record := &kgo.Record{Topic: "bad", Value: []byte("bad")}

	started := time.Now()
	if err := s.processRecord(context.Background(), client, record); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("DLQ timeout took %v", elapsed)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started = time.Now()
	if err := s.processRecord(ctx, client, record); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 50*time.Millisecond {
		t.Fatalf("cancelled DLQ took %v", elapsed)
	}
}

func TestManualBatchSerialWithinPartitionConcurrentAcrossPartitions(t *testing.T) {
	s := newTestSubscriber(t, SubscriberConfig{CommitMode: CommitModeManualContiguous, Concurrency: 2})
	var mu sync.Mutex
	orders := map[string][]int{}
	active := map[string]int{}
	var maxActive int
	partitionOneStarted := make(chan struct{})
	var partitionOneOnce sync.Once

	if err := s.Subscribe("orders", func(_ context.Context, evt Event) error {
		partition := evt.Header("partition")
		offset := int(evt.Data["offset"].(float64))
		mu.Lock()
		if active[partition] != 0 {
			mu.Unlock()
			return fmt.Errorf("partition %s processed concurrently", partition)
		}
		active[partition]++
		total := 0
		for _, count := range active {
			total += count
		}
		if total > maxActive {
			maxActive = total
		}
		mu.Unlock()

		if partition == "0" && offset == 0 {
			select {
			case <-partitionOneStarted:
			case <-time.After(time.Second):
				return errors.New("other partition did not run concurrently")
			}
		}
		if partition == "1" && offset == 0 {
			partitionOneOnce.Do(func() { close(partitionOneStarted) })
		}
		time.Sleep(5 * time.Millisecond)

		mu.Lock()
		orders[partition] = append(orders[partition], offset)
		active[partition]--
		mu.Unlock()
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	records := []*kgo.Record{
		deliveryRecord(t, "orders", "orders", 0, 1, kgo.RecordHeader{Key: "partition", Value: []byte("0")}),
		deliveryRecord(t, "orders", "orders", 0, 0, kgo.RecordHeader{Key: "partition", Value: []byte("0")}),
		deliveryRecord(t, "orders", "orders", 1, 1, kgo.RecordHeader{Key: "partition", Value: []byte("1")}),
		deliveryRecord(t, "orders", "orders", 1, 0, kgo.RecordHeader{Key: "partition", Value: []byte("1")}),
	}
	client := &fakeSubscriberClient{}
	if err := s.processManualBatch(context.Background(), client, fetchBatch(records...)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(orders["0"], []int{0, 1}) || !reflect.DeepEqual(orders["1"], []int{0, 1}) {
		t.Fatalf("partition order = %#v", orders)
	}
	if maxActive != 2 {
		t.Fatalf("max concurrent partitions = %d, want 2", maxActive)
	}
}

func TestManualBatchCommitsOnlyDurableContiguousPrefixes(t *testing.T) {
	s := newTestSubscriber(t, SubscriberConfig{CommitMode: CommitModeManualContiguous, Concurrency: 2})
	var processedOffsetTwo atomic.Bool
	if err := s.Subscribe("orders", func(_ context.Context, evt Event) error {
		if evt.Header("fail") == "yes" {
			return errors.New("handler failed")
		}
		if evt.Header("offset") == "2" {
			processedOffsetTwo.Store(true)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	client := &fakeSubscriberClient{produceFn: func(_ context.Context, records ...*kgo.Record) kgo.ProduceResults {
		return kgo.ProduceResults{{Record: records[0], Err: errors.New("DLQ down")}}
	}}
	batch := fetchBatch(
		deliveryRecord(t, "orders", "orders", 0, 0, kgo.RecordHeader{Key: "offset", Value: []byte("0")}),
		deliveryRecord(t, "orders", "orders", 0, 1, kgo.RecordHeader{Key: "fail", Value: []byte("yes")}),
		deliveryRecord(t, "orders", "orders", 0, 2, kgo.RecordHeader{Key: "offset", Value: []byte("2")}),
		deliveryRecord(t, "orders", "orders", 1, 0),
		deliveryRecord(t, "orders", "orders", 1, 1),
	)
	if err := s.processManualBatch(context.Background(), client, batch); err == nil {
		t.Fatal("expected non-durable DLQ failure")
	}
	if processedOffsetTwo.Load() {
		t.Fatal("partition processed past a non-durable offset")
	}
	_, commits, _, _, _, _ := client.snapshot()
	if len(commits) != 1 || len(commits[0]) != 2 {
		t.Fatalf("commits = %#v, want one commit with two partition prefixes", commits)
	}
	got := map[int32]int64{}
	for _, record := range commits[0] {
		got[record.Partition] = record.Offset
	}
	if !reflect.DeepEqual(got, map[int32]int64{0: 0, 1: 1}) {
		t.Fatalf("last durable record offsets = %#v", got)
	}
}

func TestManualBatchDurableDLQAllowsContiguousCommit(t *testing.T) {
	s := newTestSubscriber(t, SubscriberConfig{CommitMode: CommitModeManualContiguous})
	if err := s.Subscribe("orders", func(_ context.Context, evt Event) error {
		if evt.Header("fail") == "yes" {
			return errors.New("handler failed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	client := &fakeSubscriberClient{}
	batch := fetchBatch(
		deliveryRecord(t, "orders", "orders", 0, 0),
		deliveryRecord(t, "orders", "orders", 0, 1, kgo.RecordHeader{Key: "fail", Value: []byte("yes")}),
		deliveryRecord(t, "orders", "orders", 0, 2),
	)
	if err := s.processManualBatch(context.Background(), client, batch); err != nil {
		t.Fatal(err)
	}
	_, commits, produced, _, _, _ := client.snapshot()
	if len(produced) != 1 || len(commits) != 1 || len(commits[0]) != 1 || commits[0][0].Offset != 2 {
		t.Fatalf("durable DLQ outcome produced=%d commits=%#v", len(produced), commits)
	}
}

func TestManualBatchReturnsCommitFailure(t *testing.T) {
	s := newTestSubscriber(t, SubscriberConfig{CommitMode: CommitModeManualContiguous})
	if err := s.Subscribe("orders", func(context.Context, Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	client := &fakeSubscriberClient{commitErr: errors.New("commit unavailable")}
	err := s.processManualBatch(context.Background(), client, fetchBatch(deliveryRecord(t, "orders", "orders", 0, 4)))
	if err == nil || !strings.Contains(err.Error(), "commit durable offsets") {
		t.Fatalf("commit error = %v", err)
	}
}

func TestManualStartOwnsOneBatchAndAllowsRebalanceAfterCommit(t *testing.T) {
	s := newTestSubscriber(t, SubscriberConfig{CommitMode: CommitModeManualContiguous, MaxPollRecords: 23})
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	if err := s.Subscribe("orders", func(context.Context, Event) error {
		close(handlerStarted)
		<-releaseHandler
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	client := &fakeSubscriberClient{}
	var pollCount atomic.Int32
	client.pollFn = func(ctx context.Context, _ int) kgo.Fetches {
		if pollCount.Add(1) == 1 {
			return fetchBatch(deliveryRecord(t, "orders", "orders", 0, 0))
		}
		<-ctx.Done()
		return nil
	}
	s.clientFactory = func(...kgo.Opt) (subscriberClient, error) { return client, nil }
	ctx, cancel := context.WithCancel(context.Background())
	startDone := make(chan error, 1)
	go func() { startDone <- s.Start(ctx) }()
	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	time.Sleep(20 * time.Millisecond)
	if got := pollCount.Load(); got != 1 {
		t.Fatalf("polled %d times before batch drain, want 1", got)
	}
	close(releaseHandler)

	deadline := time.Now().Add(time.Second)
	for {
		_, _, _, events, _, _ := client.snapshot()
		if reflect.DeepEqual(events, []string{"commit", "allow"}) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("commit/rebalance ordering = %#v", events)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-startDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("Start error = %v", err)
	}
	pollMax, _, _, events, allows, closes := client.snapshot()
	if len(pollMax) < 2 || pollMax[0] != 23 || allows < 2 || closes != 1 {
		t.Fatalf("pollMax=%v events=%v allows=%d closes=%d", pollMax, events, allows, closes)
	}
}

func TestSubscriberStartCloseAreRaceSafeAndIdempotent(t *testing.T) {
	s := newTestSubscriber(t, SubscriberConfig{CommitMode: CommitModeManualContiguous, DrainTimeoutMs: 200})
	if err := s.Subscribe("orders", func(context.Context, Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	pollStarted := make(chan struct{})
	var pollOnce sync.Once
	client := &fakeSubscriberClient{pollFn: func(ctx context.Context, _ int) kgo.Fetches {
		pollOnce.Do(func() { close(pollStarted) })
		<-ctx.Done()
		return nil
	}}
	s.clientFactory = func(...kgo.Opt) (subscriberClient, error) { return client, nil }
	startDone := make(chan error, 1)
	go func() { startDone <- s.Start(context.Background()) }()
	select {
	case <-pollStarted:
	case <-time.After(time.Second):
		t.Fatal("poll did not start")
	}
	if err := s.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("concurrent Start error = %v", err)
	}

	const callers = 8
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() { errs <- s.Close() }()
	}
	for i := 0; i < callers; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("Close error = %v", err)
		}
	}
	if err := <-startDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("Start error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("post-Close Start error = %v", err)
	}
	_, _, _, _, _, closes := client.snapshot()
	if closes != 1 || s.Healthy() {
		t.Fatalf("closes=%d healthy=%v", closes, s.Healthy())
	}
}

func TestCancelledBatchDrainIsBounded(t *testing.T) {
	s := newTestSubscriber(t, SubscriberConfig{CommitMode: CommitModeManualContiguous, DrainTimeoutMs: 20})
	handlerStarted := make(chan struct{})
	release := make(chan struct{})
	if err := s.Subscribe("orders", func(context.Context, Event) error {
		close(handlerStarted)
		<-release
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := s.processPartitions(ctx, &fakeSubscriberClient{}, fetchBatch(deliveryRecord(t, "orders", "orders", 0, 0)))
		done <- err
	}()
	<-handlerStarted
	cancel()
	started := time.Now()
	err := <-done
	if err == nil || time.Since(started) > 250*time.Millisecond {
		t.Fatalf("bounded drain error=%v elapsed=%v", err, time.Since(started))
	}
	close(release)
}
