package kafkakit

import (
	"reflect"
	"strings"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/twmb/franz-go/pkg/kgo"
)

func init() {
	chassis.RequireMajor(11)
}

func publisherOptionClient(t *testing.T, cfg PublisherConfig) *kgo.Client {
	t.Helper()
	opts, err := buildPublisherOptions(Config{
		BootstrapServers: " localhost:9092 ",
		Source:           "test",
		Publisher:        cfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := kgo.NewClient(opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

func subscriberOptionClient(t *testing.T, cfg SubscriberConfig) (*kgo.Client, subscriberSettings) {
	t.Helper()
	opts, settings, err := buildSubscriberOptions(Config{
		BootstrapServers: "localhost:9092",
		Subscriber:       cfg,
	}, "group")
	if err != nil {
		t.Fatal(err)
	}
	opts = append(opts, kgo.ConsumeTopics("topic"))
	client, err := kgo.NewClient(opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client, settings
}

func TestBuildPublisherOptionsMapsAcks(t *testing.T) {
	tests := []struct {
		value string
		want  kgo.Acks
	}{
		{value: "0", want: kgo.NoAck()},
		{value: "1", want: kgo.LeaderAck()},
		{value: "all", want: kgo.AllISRAcks()},
		{value: "-1", want: kgo.AllISRAcks()},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			client := publisherOptionClient(t, PublisherConfig{Acks: tt.value})
			if got := client.OptValue(kgo.RequiredAcks); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("RequiredAcks = %#v, want %#v", got, tt.want)
			}
			if (tt.value == "0" || tt.value == "1") && client.OptValue(kgo.DisableIdempotentWrite) != true {
				t.Fatalf("acks=%s must disable idempotent writes because franz-go rejects the combination", tt.value)
			}
		})
	}
}

func TestBuildPublisherOptionsMapsRetry(t *testing.T) {
	client := publisherOptionClient(t, PublisherConfig{MaxRetries: 4, RetryBackoffMs: 25})
	if got := client.OptValue(kgo.RecordRetries); got != int64(4) {
		t.Fatalf("record retries = %v, want 4", got)
	}
	backoff, ok := client.OptValue(kgo.RetryBackoffFn).(func(int) time.Duration)
	if !ok {
		t.Fatalf("retry backoff type = %T", client.OptValue(kgo.RetryBackoffFn))
	}
	if got := backoff(1); got < 25*time.Millisecond/2 || got > 25*time.Millisecond {
		t.Fatalf("first retry backoff = %v", got)
	}
}

func TestBuildPublisherOptionsMapsCompression(t *testing.T) {
	tests := []struct {
		value string
		want  kgo.CompressionCodec
	}{
		{value: "none", want: kgo.NoCompression()},
		{value: "gzip", want: kgo.GzipCompression()},
		{value: "snappy", want: kgo.SnappyCompression()},
		{value: "lz4", want: kgo.Lz4Compression()},
		{value: "zstd", want: kgo.ZstdCompression()},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			client := publisherOptionClient(t, PublisherConfig{Compression: tt.value})
			want := []kgo.CompressionCodec{tt.want}
			if got := client.OptValue(kgo.ProducerBatchCompression); !reflect.DeepEqual(got, want) {
				t.Fatalf("compression = %#v, want %#v", got, want)
			}
		})
	}
}

func TestBuildPublisherOptionsLingerDistinguishesOmittedAndDisabled(t *testing.T) {
	defaultClient := publisherOptionClient(t, PublisherConfig{})
	disabledClient := publisherOptionClient(t, PublisherConfig{DisableLinger: true})
	positiveClient := publisherOptionClient(t, PublisherConfig{LingerMs: 17})

	if got := defaultClient.OptValue(kgo.ProducerLinger); got == time.Duration(0) {
		t.Fatalf("omitted linger unexpectedly disabled: %v", got)
	}
	if got := disabledClient.OptValue(kgo.ProducerLinger); got != time.Duration(0) {
		t.Fatalf("disabled linger = %v, want 0", got)
	}
	if got := positiveClient.OptValue(kgo.ProducerLinger); got != 17*time.Millisecond {
		t.Fatalf("positive linger = %v, want 17ms", got)
	}
}

func TestBuildPublisherOptionsRejectsUnsupportedValues(t *testing.T) {
	tests := []PublisherConfig{
		{Acks: "quorum"},
		{Compression: "brotli"},
		{LingerMs: -1},
		{LingerMs: 1, DisableLinger: true},
		{MaxRetries: -1},
		{RetryBackoffMs: -1},
	}
	for _, cfg := range tests {
		if _, err := buildPublisherOptions(Config{BootstrapServers: "localhost:9092", Source: "test", Publisher: cfg}); err == nil {
			t.Fatalf("expected configuration error for %+v", cfg)
		}
	}
}

func TestBuildSubscriberOptionsMapsOffsetsAndSession(t *testing.T) {
	tests := []struct {
		value     string
		wantStart kgo.Offset
		wantReset kgo.Offset
	}{
		{value: "earliest", wantStart: kgo.NewOffset().AtStart(), wantReset: kgo.NewOffset().AtStart()},
		{value: "latest", wantStart: kgo.NewOffset().AtEnd(), wantReset: kgo.NewOffset().AtEnd()},
		{value: "none", wantStart: kgo.NewOffset().AtCommitted(), wantReset: kgo.NoResetOffset()},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			client, _ := subscriberOptionClient(t, SubscriberConfig{AutoOffsetReset: tt.value, SessionTimeoutMs: 6000})
			if got := client.OptValue(kgo.ConsumeStartOffset); !reflect.DeepEqual(got, tt.wantStart) {
				t.Fatalf("start offset = %#v, want %#v", got, tt.wantStart)
			}
			if got := client.OptValue(kgo.ConsumeResetOffset); !reflect.DeepEqual(got, tt.wantReset) {
				t.Fatalf("reset offset = %#v, want %#v", got, tt.wantReset)
			}
			if got := client.OptValue(kgo.SessionTimeout); got != 6*time.Second {
				t.Fatalf("session timeout = %v, want 6s", got)
			}
		})
	}
}

func TestBuildSubscriberOptionsCommitModesAndBounds(t *testing.T) {
	manual, settings := subscriberOptionClient(t, SubscriberConfig{AtLeastOnce: true})
	if settings.commitMode != CommitModeManualContiguous {
		t.Fatalf("AtLeastOnce mode = %q", settings.commitMode)
	}
	if manual.OptValue(kgo.DisableAutoCommit) != true || manual.OptValue(kgo.BlockRebalanceOnPoll) != true {
		t.Fatal("manual mode must disable auto commit and block rebalance on poll")
	}
	if settings.maxPollRecords != defaultMaxPollRecords || settings.dlqTimeout != defaultDLQTimeout || settings.drainTimeout != defaultDrainTimeout {
		t.Fatalf("default settings = %+v", settings)
	}

	legacy, legacySettings := subscriberOptionClient(t, SubscriberConfig{EnableAutoCommit: true, MaxPollRecords: 37, DLQTimeoutMs: 11, DrainTimeoutMs: 22})
	if legacySettings.commitMode != CommitModeLegacyAuto || legacy.OptValue(kgo.DisableAutoCommit) != false {
		t.Fatalf("legacy settings = %+v, disableAuto=%v", legacySettings, legacy.OptValue(kgo.DisableAutoCommit))
	}
	if legacySettings.maxPollRecords != 37 || legacySettings.dlqTimeout != 11*time.Millisecond || legacySettings.drainTimeout != 22*time.Millisecond {
		t.Fatalf("explicit settings not preserved: %+v", legacySettings)
	}
}

func TestBuildSubscriberOptionsRejectsConflictsAndUnsupportedValues(t *testing.T) {
	tests := []SubscriberConfig{
		{CommitMode: CommitModeManualContiguous, EnableAutoCommit: true},
		{CommitMode: CommitModeLegacyAuto, AtLeastOnce: true},
		{AtLeastOnce: true, EnableAutoCommit: true},
		{CommitMode: "other"},
		{AutoOffsetReset: "middle"},
		{SessionTimeoutMs: -1},
		{DLQTimeoutMs: -1},
		{DrainTimeoutMs: -1},
	}
	for _, cfg := range tests {
		_, _, err := buildSubscriberOptions(Config{BootstrapServers: "localhost:9092", Subscriber: cfg}, "group")
		if err == nil {
			t.Fatalf("expected configuration error for %+v", cfg)
		}
		if !strings.Contains(err.Error(), "invalid configuration") {
			t.Fatalf("non-actionable error for %+v: %v", cfg, err)
		}
	}
}

func TestConstructorsRejectUnsupportedConfiguration(t *testing.T) {
	tests := []Config{
		{BootstrapServers: "localhost:9092", Source: "test", SchemaRegistryURL: "http://schema"},
		{BootstrapServers: "localhost:9092", Source: "test", TenantFilter: TenantFilterConfig{GrantsURL: "http://grants"}},
		{BootstrapServers: "localhost:9092", Source: "test", TenantFilter: TenantFilterConfig{GrantsCacheTTL: 60}},
		{BootstrapServers: "localhost:9092", Source: "test", TenantFilter: TenantFilterConfig{Enabled: true}},
	}
	for _, cfg := range tests {
		if _, err := NewPublisher(cfg); err == nil {
			t.Fatalf("NewPublisher accepted unsupported config %+v", cfg)
		}
		if _, err := NewSubscriber(cfg, "group"); err == nil {
			t.Fatalf("NewSubscriber accepted unsupported config %+v", cfg)
		}
	}
}
