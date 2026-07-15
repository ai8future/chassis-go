//go:build integration

package redpandaintegration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/internal/integrationtest"
	"github.com/ai8future/chassis-go/v11/kafkakit"
	"github.com/ai8future/chassis-go/v11/schemakit"
	"github.com/ai8future/chassis-go/v11/testkit"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

func init() {
	chassis.RequireMajor(11)
}

func TestRedpandaLiveIntegration(t *testing.T) {
	integrationtest.Run(t, "redpanda", func(t *testing.T) {
		image := integrationtest.LoadPinnedImage(t, "redpanda")
		svc := startRedpanda(t, image)
		admin := newKafkaClient(t, svc.bootstrap)
		defer admin.Close()

		t.Run("topic_admin_health", func(t *testing.T) {
			assertHTTPReady(t, svc.adminURL+"/v1/status/ready")
			createTopic(t, admin, uniqueTopic("admin"), 1)
		})
		t.Run("module_publish_consume_envelope_and_stats", func(t *testing.T) {
			testModulePublishConsume(t, svc.bootstrap, admin)
		})
		t.Run("raw_fixture_header_and_dlq_preserve_key_headers", func(t *testing.T) {
			testRawFixtureDLQ(t, svc.bootstrap, admin)
		})
		t.Run("multi_topic_subscription", func(t *testing.T) {
			testMultiTopicSubscription(t, svc.bootstrap, admin)
		})
		t.Run("two_member_group_redistributes_after_cancellation", func(t *testing.T) {
			testTwoMemberRedistribution(t, svc.bootstrap, admin)
		})
		t.Run("handler_cancellation_bounded_shutdown", func(t *testing.T) {
			testCancellationBoundedShutdown(t, svc.bootstrap, admin)
		})
		t.Run("schemakit_live_registry", func(t *testing.T) {
			testSchemaKitLiveRegistry(t, svc.schemaURL, svc.adminURL)
		})
	})
}

type redpandaService struct {
	bootstrap string
	schemaURL string
	adminURL  string
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func startRedpanda(t *testing.T, image string) redpandaService {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").CombinedOutput(); err != nil {
		t.Fatalf("selected redpanda integration requires healthy Docker: %v\n%s", err, out)
	}
	kafkaPort := freePort(t)
	schemaPort := freePort(t)
	adminPort := freePort(t)
	name := "chassis-redpanda-" + strings.ToLower(strings.ReplaceAll(uniqueTopic("suite"), ".", "-"))
	args := []string{
		"run", "-d", "--name", name, "--pull=missing",
		"-p", fmt.Sprintf("127.0.0.1:%d:19092", kafkaPort),
		"-p", fmt.Sprintf("127.0.0.1:%d:18081", schemaPort),
		"-p", fmt.Sprintf("127.0.0.1:%d:9644", adminPort),
		image,
		"redpanda", "start",
		"--kafka-addr", "internal://0.0.0.0:9092,external://0.0.0.0:19092",
		"--advertise-kafka-addr", fmt.Sprintf("internal://127.0.0.1:9092,external://127.0.0.1:%d", kafkaPort),
		"--pandaproxy-addr", "internal://0.0.0.0:8082,external://0.0.0.0:18082",
		"--advertise-pandaproxy-addr", "internal://127.0.0.1:8082,external://127.0.0.1:18082",
		"--schema-registry-addr", "internal://0.0.0.0:8081,external://0.0.0.0:18081",
		"--rpc-addr", "0.0.0.0:33145",
		"--advertise-rpc-addr", "127.0.0.1:33145",
		"--mode", "dev-container",
		"--smp", "1",
		"--default-log-level=info",
	}
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("start redpanda container with pinned image %s: %v\n%s", image, err, out)
	}
	t.Cleanup(func() {
		integrationtest.CleanupDocker(t, name, "redpanda")
	})
	svc := redpandaService{
		bootstrap: fmt.Sprintf("127.0.0.1:%d", kafkaPort),
		schemaURL: fmt.Sprintf("http://127.0.0.1:%d", schemaPort),
		adminURL:  fmt.Sprintf("http://127.0.0.1:%d", adminPort),
	}
	waitFor(t, 90*time.Second, func() (bool, string) {
		resp, err := http.Get(svc.adminURL + "/v1/status/ready")
		if err != nil {
			return false, err.Error()
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Sprintf("status %d: %s", resp.StatusCode, body)
		}
		return true, string(body)
	})
	waitFor(t, 60*time.Second, func() (bool, string) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cl, err := kgo.NewClient(kgo.SeedBrokers(svc.bootstrap))
		if err != nil {
			return false, err.Error()
		}
		defer cl.Close()
		req := kmsg.NewPtrMetadataRequest()
		req.Topics = []kmsg.MetadataRequestTopic{}
		_, err = req.RequestWith(ctx, cl)
		if err != nil {
			return false, err.Error()
		}
		return true, "metadata ok"
	})
	return svc
}

func freePort(t *testing.T) int {
	t.Helper()
	port, err := testkit.GetFreePort()
	if err != nil {
		t.Fatalf("allocate free port: %v", err)
	}
	return port
}

func assertHTTPReady(t *testing.T, url string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("admin health request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.Fatalf("admin health status %d: %s", resp.StatusCode, body)
	}
}

func newKafkaClient(t *testing.T, bootstrap string, opts ...kgo.Opt) *kgo.Client {
	t.Helper()
	all := []kgo.Opt{kgo.SeedBrokers(bootstrap)}
	all = append(all, opts...)
	client, err := kgo.NewClient(all...)
	if err != nil {
		t.Fatalf("new kafka client: %v", err)
	}
	return client
}

func createTopic(t *testing.T, client *kgo.Client, topic string, partitions int32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req := kmsg.NewPtrCreateTopicsRequest()
	reqTopic := kmsg.NewCreateTopicsRequestTopic()
	reqTopic.Topic = topic
	reqTopic.NumPartitions = partitions
	reqTopic.ReplicationFactor = 1
	req.Topics = append(req.Topics, reqTopic)
	resp, err := req.RequestWith(ctx, client)
	if err != nil {
		t.Fatalf("create topic %s: %v", topic, err)
	}
	for _, topicResp := range resp.Topics {
		if err := kerr.ErrorForCode(topicResp.ErrorCode); err != nil && !errors.Is(err, kerr.TopicAlreadyExists) {
			t.Fatalf("create topic %s response: %v", topic, err)
		}
	}
}

func uniqueTopic(prefix string) string {
	return fmt.Sprintf("ai8.integration.%s.%d", prefix, time.Now().UnixNano())
}

func testModulePublishConsume(t *testing.T, bootstrap string, admin *kgo.Client) {
	topic := uniqueTopic("module")
	createTopic(t, admin, topic, 1)
	events := make(chan kafkakit.Event, 1)
	group := uniqueTopic("group")
	sub := startModuleSubscriber(t, bootstrap, group, map[string]kafkakit.HandlerFunc{
		topic: func(_ context.Context, evt kafkakit.Event) error {
			events <- evt
			return nil
		},
	})
	defer sub.stop(t)

	publisher, err := kafkakit.NewPublisher(kafkakit.Config{BootstrapServers: bootstrap, Source: "integration-source", TenantID: "tenant-a", Publisher: kafkakit.PublisherConfig{Acks: "all", DisableLinger: true}})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer publisher.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := publisher.PublishBatch(ctx, []kafkakit.OutboundEvent{{Subject: topic, EntityRefs: []string{"entity:42"}, Data: map[string]any{"kind": "module", "count": 2}}}); err != nil {
		t.Fatalf("PublishBatch: %v", err)
	}
	evt := receiveEvent(t, events, 20*time.Second)
	if evt.Subject != topic || evt.Source != "integration-source" || evt.TenantID != "tenant-a" || !reflect.DeepEqual(evt.EntityRefs, []string{"entity:42"}) {
		t.Fatalf("event metadata = %+v", evt)
	}
	if evt.Data["kind"] != "module" || evt.Data["count"] != float64(2) || len(evt.Raw) == 0 || evt.ID == "" || evt.Timestamp.IsZero() || evt.Version != "1.0" {
		t.Fatalf("event payload/envelope = %+v", evt)
	}
	if stats := publisher.Stats(); stats.EventsPublishedTotal != 1 || stats.ErrorsTotal != 0 || stats.LastEventPublished.IsZero() {
		t.Fatalf("publisher stats = %+v", stats)
	}
}

type moduleSubscriber struct {
	cancel context.CancelFunc
	done   chan error
}

func startModuleSubscriber(t *testing.T, bootstrap, group string, handlers map[string]kafkakit.HandlerFunc) moduleSubscriber {
	t.Helper()
	sub, err := kafkakit.NewSubscriber(kafkakit.Config{BootstrapServers: bootstrap, Subscriber: kafkakit.SubscriberConfig{AutoOffsetReset: "earliest", CommitMode: kafkakit.CommitModeManualContiguous, DLQTimeoutMs: 2000, DrainTimeoutMs: 3000}}, group)
	if err != nil {
		t.Fatalf("NewSubscriber: %v", err)
	}
	if err := sub.SubscribeMulti(handlers); err != nil {
		t.Fatalf("SubscribeMulti: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sub.Start(ctx) }()
	return moduleSubscriber{cancel: cancel, done: done}
}

func (s moduleSubscriber) stop(t *testing.T) {
	t.Helper()
	s.cancel()
	select {
	case err := <-s.done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("subscriber stop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("subscriber stop timed out")
	}
}

func testRawFixtureDLQ(t *testing.T, bootstrap string, admin *kgo.Client) {
	topic := uniqueTopic("raw")
	dlq := "ai8._dlq." + topic
	createTopic(t, admin, topic, 1)
	createTopic(t, admin, dlq, 1)
	seenHeader := make(chan string, 1)
	sub := startModuleSubscriber(t, bootstrap, uniqueTopic("group"), map[string]kafkakit.HandlerFunc{
		topic: func(_ context.Context, evt kafkakit.Event) error {
			seenHeader <- evt.Header("duplicate")
			return errors.New("force DLQ")
		},
	})
	defer sub.stop(t)

	producer := newKafkaClient(t, bootstrap)
	defer producer.Close()
	produceRawEnvelope(t, producer, &kgo.Record{Topic: topic, Key: []byte("original-key"), Headers: []kgo.RecordHeader{{Key: "duplicate", Value: []byte("first")}, {Key: "duplicate", Value: []byte("last")}, {Key: "trace", Value: []byte("abc")}}, Value: rawEnvelope(t, "fixture-source", topic, "tenant-b", []string{"entity:raw"}, map[string]any{"id": "dlq-1"})})
	if got := receiveString(t, seenHeader, 20*time.Second); got != "last" {
		t.Fatalf("duplicate header last-value = %q, want last", got)
	}

	dlqClient := newKafkaClient(t, bootstrap, kgo.ConsumerGroup(uniqueTopic("dlq-group")), kgo.ConsumeTopics(dlq), kgo.ConsumeStartOffset(kgo.NewOffset().AtStart()), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	defer dlqClient.Close()
	record := pollRecord(t, dlqClient, 20*time.Second, func(r *kgo.Record) bool { return r.Topic == dlq })
	var payload struct {
		OriginalKey     []byte `json:"original_key"`
		OriginalHeaders []struct {
			Key   string `json:"key"`
			Value []byte `json:"value"`
		} `json:"original_headers"`
		FailureReason string `json:"failure_reason"`
	}
	if err := json.Unmarshal(record.Value, &payload); err != nil {
		t.Fatalf("decode DLQ payload: %v", err)
	}
	if string(payload.OriginalKey) != "original-key" || payload.FailureReason != "handler error" {
		t.Fatalf("DLQ key/reason = %+v", payload)
	}
	if len(payload.OriginalHeaders) != 3 ||
		payload.OriginalHeaders[0].Key != "duplicate" || string(payload.OriginalHeaders[0].Value) != "first" ||
		payload.OriginalHeaders[1].Key != "duplicate" || string(payload.OriginalHeaders[1].Value) != "last" ||
		payload.OriginalHeaders[2].Key != "trace" || string(payload.OriginalHeaders[2].Value) != "abc" {
		t.Fatalf("DLQ original headers = %+v", payload.OriginalHeaders)
	}
}

func testMultiTopicSubscription(t *testing.T, bootstrap string, admin *kgo.Client) {
	topics := []string{uniqueTopic("multi-a"), uniqueTopic("multi-b")}
	for _, topic := range topics {
		createTopic(t, admin, topic, 1)
	}
	seen := make(chan string, len(topics))
	handlers := map[string]kafkakit.HandlerFunc{}
	for _, topic := range topics {
		handlers[topic] = func(_ context.Context, evt kafkakit.Event) error {
			seen <- evt.Subject
			return nil
		}
	}
	sub := startModuleSubscriber(t, bootstrap, uniqueTopic("group"), handlers)
	defer sub.stop(t)
	pub, err := kafkakit.NewPublisher(kafkakit.Config{BootstrapServers: bootstrap, Source: "multi-source", Publisher: kafkakit.PublisherConfig{Acks: "all", DisableLinger: true}})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer pub.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pub.PublishBatch(ctx, []kafkakit.OutboundEvent{{Subject: topics[0], Data: map[string]any{"id": "a"}}, {Subject: topics[1], Data: map[string]any{"id": "b"}}}); err != nil {
		t.Fatalf("PublishBatch: %v", err)
	}
	got := map[string]bool{}
	waitFor(t, 20*time.Second, func() (bool, string) {
		for len(got) < 2 {
			select {
			case subject := <-seen:
				got[subject] = true
			default:
				return false, fmt.Sprintf("seen %v", got)
			}
		}
		return true, fmt.Sprintf("seen %v", got)
	})
}

func testTwoMemberRedistribution(t *testing.T, bootstrap string, admin *kgo.Client) {
	topic := uniqueTopic("rebalance")
	createTopic(t, admin, topic, 2)
	group := uniqueTopic("group")
	type seenRecord struct{ member, id string }
	seen := make(chan seenRecord, 16)
	makeHandler := func(member string) kafkakit.HandlerFunc {
		return func(_ context.Context, evt kafkakit.Event) error {
			if id, _ := evt.Data["id"].(string); id != "" {
				seen <- seenRecord{member: member, id: id}
			}
			return nil
		}
	}
	a := startModuleSubscriber(t, bootstrap, group, map[string]kafkakit.HandlerFunc{topic: makeHandler("a")})
	b := startModuleSubscriber(t, bootstrap, group, map[string]kafkakit.HandlerFunc{topic: makeHandler("b")})
	defer b.stop(t)
	producer := newKafkaClient(t, bootstrap, kgo.RecordPartitioner(kgo.ManualPartitioner()))
	defer producer.Close()
	// Give both members a bounded join window before producing the partitioned fixture set.
	time.Sleep(2 * time.Second)
	for i := 0; i < 4; i++ {
		produceRawEnvelope(t, producer, &kgo.Record{Topic: topic, Partition: int32(i % 2), Key: []byte(fmt.Sprintf("key-%d", i)), Value: rawEnvelope(t, "rebalance-source", topic, "", nil, map[string]any{"id": fmt.Sprintf("initial-%d", i)})})
	}
	initial := map[string]string{}
	members := map[string]bool{}
	waitFor(t, 30*time.Second, func() (bool, string) {
		for {
			select {
			case rec := <-seen:
				initial[rec.id] = rec.member
				members[rec.member] = true
			default:
				return len(initial) == 4 && len(members) == 2, fmt.Sprintf("initial=%v members=%v", initial, members)
			}
		}
	})
	a.stop(t)
	for i := 0; i < 2; i++ {
		produceRawEnvelope(t, producer, &kgo.Record{Topic: topic, Partition: int32(i), Key: []byte(fmt.Sprintf("survivor-%d", i)), Value: rawEnvelope(t, "rebalance-source", topic, "", nil, map[string]any{"id": fmt.Sprintf("survivor-%d", i)})})
	}
	survivor := map[string]bool{}
	var cancelledMemberViolation string
	waitFor(t, 30*time.Second, func() (bool, string) {
		if cancelledMemberViolation != "" {
			return false, cancelledMemberViolation
		}
		for {
			select {
			case rec := <-seen:
				if strings.HasPrefix(rec.id, "survivor-") {
					if rec.member != "b" {
						cancelledMemberViolation = fmt.Sprintf("cancelled member consumed survivor record: %+v", rec)
						return false, cancelledMemberViolation
					}
					survivor[rec.id] = true
				}
			default:
				return len(survivor) == 2, fmt.Sprintf("survivor=%v", survivor)
			}
		}
	})
}

func testCancellationBoundedShutdown(t *testing.T, bootstrap string, admin *kgo.Client) {
	topic := uniqueTopic("cancel")
	createTopic(t, admin, topic, 1)
	started := make(chan struct{})
	sub, err := kafkakit.NewSubscriber(kafkakit.Config{BootstrapServers: bootstrap, Subscriber: kafkakit.SubscriberConfig{AutoOffsetReset: "earliest", CommitMode: kafkakit.CommitModeManualContiguous, DrainTimeoutMs: 1000}}, uniqueTopic("group"))
	if err != nil {
		t.Fatalf("NewSubscriber: %v", err)
	}
	if err := sub.Subscribe(topic, func(ctx context.Context, evt kafkakit.Event) error {
		select {
		case <-started:
		default:
			close(started)
		}
		<-ctx.Done()
		return ctx.Err()
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	startDone := make(chan error, 1)
	go func() { startDone <- sub.Start(context.Background()) }()
	producer := newKafkaClient(t, bootstrap)
	defer producer.Close()
	produceRawEnvelope(t, producer, &kgo.Record{Topic: topic, Value: rawEnvelope(t, "cancel-source", topic, "", nil, map[string]any{"id": "cancel"})})
	select {
	case <-started:
	case <-time.After(20 * time.Second):
		t.Fatal("handler did not start")
	}
	begin := time.Now()
	if err := sub.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if time.Since(begin) > 5*time.Second {
		t.Fatalf("Close exceeded bounded shutdown: %v", time.Since(begin))
	}
	select {
	case err := <-startDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Start error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after Close")
	}
}

func testSchemaKitLiveRegistry(t *testing.T, schemaURL, adminURL string) {
	registry, err := schemakit.NewRegistry(schemaURL)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if err := registry.LoadSchemas(filepath.Join(repoRoot(t), "schemakit", "testdata", "schemas")); err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	schema := registry.GetSchema("ai8.scanner.gdelt.v1.SignalSurge")
	if schema == nil {
		t.Fatal("loaded schema not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := registry.Register(ctx, schema); err != nil {
		t.Fatalf("Register live schema: %v", err)
	}
	if schema.SchemaID <= 0 {
		t.Fatalf("schema ID = %d, want positive", schema.SchemaID)
	}
	data := map[string]any{"entity": "AAPL", "tier": "flash", "kind": "volume_spike", "current": float32(150.5), "baseline": float32(50.0), "multiplier": float32(3.01), "window_minutes": 15}
	raw, err := registry.Serialize(schema, data)
	if err != nil {
		t.Fatalf("Serialize cached schema: %v", err)
	}
	decoded, err := registry.Deserialize(raw)
	if err != nil {
		t.Fatalf("Deserialize cached schema: %v", err)
	}
	if decoded["entity"] != "AAPL" || decoded["kind"] != "volume_spike" {
		t.Fatalf("decoded data = %+v", decoded)
	}
	invalid := &schemakit.Schema{Subject: uniqueTopic("invalid-schema"), AvroJSON: `{"type":"record","name":"Broken","fields":[{"name":"x","type":"not-a-type"}]}`}
	if err := registry.Register(ctx, invalid); err == nil || !strings.Contains(err.Error(), "HTTP") {
		t.Fatalf("invalid schema Register error = %v, want HTTP mapping", err)
	}
	wrongEndpoint, err := schemakit.NewRegistry(adminURL)
	if err != nil {
		t.Fatalf("NewRegistry admin endpoint: %v", err)
	}
	if err := wrongEndpoint.Register(ctx, schema); err == nil || !strings.Contains(err.Error(), "HTTP") {
		t.Fatalf("failed registry HTTP mapping error = %v", err)
	}
}

func rawEnvelope(t *testing.T, source, subject, tenant string, refs []string, data any) []byte {
	t.Helper()
	dataBytes, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal raw data: %v", err)
	}
	if refs == nil {
		refs = []string{}
	}
	body := struct {
		ID         string   `json:"id"`
		Timestamp  int64    `json:"timestamp"`
		Source     string   `json:"source"`
		Subject    string   `json:"subject"`
		TraceID    string   `json:"trace_id"`
		TenantID   string   `json:"tenant_id"`
		Version    string   `json:"version"`
		EntityRefs []string `json:"entity_refs"`
		Data       []byte   `json:"data"`
	}{
		ID:         uniqueTopic("evt"),
		Timestamp:  time.Now().UnixMilli(),
		Source:     source,
		Subject:    subject,
		TenantID:   tenant,
		Version:    "1.0",
		EntityRefs: refs,
		Data:       dataBytes,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal raw envelope: %v", err)
	}
	return raw
}

func produceRawEnvelope(t *testing.T, producer *kgo.Client, record *kgo.Record) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := producer.ProduceSync(ctx, record).FirstErr(); err != nil {
		t.Fatalf("produce %s: %v", record.Topic, err)
	}
}

func receiveEvent(t *testing.T, ch <-chan kafkakit.Event, timeout time.Duration) kafkakit.Event {
	t.Helper()
	select {
	case evt := <-ch:
		return evt
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for event after %s", timeout)
	}
	return kafkakit.Event{}
}

func receiveString(t *testing.T, ch <-chan string, timeout time.Duration) string {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for string after %s", timeout)
	}
	return ""
}

func pollRecord(t *testing.T, client *kgo.Client, timeout time.Duration, match func(*kgo.Record) bool) *kgo.Record {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		fetches := client.PollFetches(ctx)
		cancel()
		if errs := fetches.Errors(); len(errs) > 0 {
			t.Fatalf("poll fetch errors: %v", errs)
		}
		iter := fetches.RecordIter()
		for !iter.Done() {
			record := iter.Next()
			if match(record) {
				return record
			}
		}
	}
	t.Fatalf("timed out polling record after %s", timeout)
	return nil
}

func waitFor(t *testing.T, timeout time.Duration, fn func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		ok, detail := fn()
		last = detail
		if ok {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s; last: %s", timeout, last)
}
