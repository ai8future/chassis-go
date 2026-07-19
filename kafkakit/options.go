package kafkakit

import (
	"fmt"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

func validateCommonConfig(cfg Config, effectiveTenantID string) error {
	if strings.TrimSpace(cfg.BootstrapServers) == "" {
		return configError("BootstrapServers is required")
	}
	for _, broker := range strings.Split(cfg.BootstrapServers, ",") {
		if strings.TrimSpace(broker) == "" {
			return configError("BootstrapServers contains an empty broker")
		}
	}
	if strings.TrimSpace(cfg.SchemaRegistryURL) != "" {
		return configError("SchemaRegistryURL is not supported by kafkakit v11; use schemakit explicitly")
	}
	if strings.TrimSpace(cfg.TenantFilter.GrantsURL) != "" || cfg.TenantFilter.GrantsCacheTTL != 0 {
		return configError("TenantFilter grants settings are not supported by kafkakit v11")
	}
	if cfg.TenantFilter.Enabled && strings.TrimSpace(effectiveTenantID) == "" {
		return configError("TenantFilter.Enabled requires TenantID")
	}
	return nil
}

func seedBrokerOptions(bootstrapServers string) []kgo.Opt {
	raw := strings.Split(bootstrapServers, ",")
	brokers := make([]string, 0, len(raw))
	for _, broker := range raw {
		brokers = append(brokers, strings.TrimSpace(broker))
	}
	return []kgo.Opt{kgo.SeedBrokers(brokers...)}
}

// buildPublisherOptions validates publisher settings and maps every supported
// field to its franz-go option. It has no network side effects.
func buildPublisherOptions(cfg Config) ([]kgo.Opt, error) {
	if err := validateCommonConfig(cfg, cfg.TenantID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Source) == "" {
		return nil, configError("Source is required")
	}
	if cfg.Publisher.MaxRetries < 0 {
		return nil, configError("Publisher.MaxRetries cannot be negative")
	}
	if cfg.Publisher.RetryBackoffMs < 0 {
		return nil, configError("Publisher.RetryBackoffMs cannot be negative")
	}
	if cfg.Publisher.LingerMs < 0 {
		return nil, configError("Publisher.LingerMs cannot be negative")
	}
	if cfg.Publisher.DisableLinger && cfg.Publisher.LingerMs > 0 {
		return nil, configError("Publisher.DisableLinger conflicts with positive Publisher.LingerMs")
	}

	opts := seedBrokerOptions(cfg.BootstrapServers)
	switch value := strings.ToLower(strings.TrimSpace(cfg.Publisher.Acks)); value {
	case "":
	case "0":
		opts = append(opts, kgo.RequiredAcks(kgo.NoAck()), kgo.DisableIdempotentWrite())
	case "1":
		opts = append(opts, kgo.RequiredAcks(kgo.LeaderAck()), kgo.DisableIdempotentWrite())
	case "all", "-1":
		opts = append(opts, kgo.RequiredAcks(kgo.AllISRAcks()))
	default:
		return nil, configError("Publisher.Acks must be 0, 1, all, or -1")
	}

	switch value := strings.ToLower(strings.TrimSpace(cfg.Publisher.Compression)); value {
	case "":
	case "none":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.NoCompression()))
	case "gzip":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.GzipCompression()))
	case "snappy":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.SnappyCompression()))
	case "lz4":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.Lz4Compression()))
	case "zstd":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.ZstdCompression()))
	default:
		return nil, configError("Publisher.Compression must be none, gzip, snappy, lz4, or zstd")
	}

	if cfg.Publisher.LingerMs > 0 {
		opts = append(opts, kgo.ProducerLinger(time.Duration(cfg.Publisher.LingerMs)*time.Millisecond))
	} else if cfg.Publisher.DisableLinger {
		opts = append(opts, kgo.ProducerLinger(0))
	}
	if cfg.Publisher.MaxRetries > 0 {
		backoff := time.Duration(cfg.Publisher.RetryBackoffMs) * time.Millisecond
		if backoff == 0 {
			backoff = 100 * time.Millisecond
		}
		opts = append(opts,
			kgo.RecordRetries(cfg.Publisher.MaxRetries),
			kgo.RetryBackoffFn(func(attempt int) time.Duration {
				return publisherRetryBackoff(backoff, attempt)
			}),
		)
	}
	return opts, nil
}

type subscriberSettings struct {
	commitMode     CommitMode
	maxPollRecords int
	dlqTimeout     time.Duration
	drainTimeout   time.Duration
}

// buildSubscriberOptions validates subscriber settings and maps supported
// fields to franz-go options. Topics are supplied at Start after subscriptions
// have been registered. It has no network side effects.
func buildSubscriberOptions(cfg Config, consumerGroup, effectiveTenantID string) ([]kgo.Opt, subscriberSettings, error) {
	var settings subscriberSettings
	if err := validateCommonConfig(cfg, effectiveTenantID); err != nil {
		return nil, settings, err
	}
	if strings.TrimSpace(consumerGroup) == "" {
		return nil, settings, configError("consumerGroup is required")
	}
	if cfg.Subscriber.SessionTimeoutMs < 0 {
		return nil, settings, configError("Subscriber.SessionTimeoutMs cannot be negative")
	}
	if cfg.Subscriber.SessionTimeoutMs > 0 && cfg.Subscriber.SessionTimeoutMs < 100 {
		return nil, settings, configError("Subscriber.SessionTimeoutMs must be at least 100 when set")
	}
	if cfg.Subscriber.MaxPollIntervalMs < 0 {
		return nil, settings, configError("Subscriber.MaxPollIntervalMs cannot be negative")
	}
	if cfg.Subscriber.DLQTimeoutMs < 0 {
		return nil, settings, configError("Subscriber.DLQTimeoutMs cannot be negative")
	}
	if cfg.Subscriber.DrainTimeoutMs < 0 {
		return nil, settings, configError("Subscriber.DrainTimeoutMs cannot be negative")
	}

	mode, err := resolveCommitMode(cfg.Subscriber)
	if err != nil {
		return nil, settings, err
	}
	settings = subscriberSettings{
		commitMode:     mode,
		maxPollRecords: cfg.Subscriber.MaxPollRecords,
		dlqTimeout:     time.Duration(cfg.Subscriber.DLQTimeoutMs) * time.Millisecond,
		drainTimeout:   time.Duration(cfg.Subscriber.DrainTimeoutMs) * time.Millisecond,
	}
	if settings.maxPollRecords <= 0 {
		settings.maxPollRecords = defaultMaxPollRecords
	}
	if settings.dlqTimeout == 0 {
		settings.dlqTimeout = defaultDLQTimeout
	}
	if settings.drainTimeout == 0 {
		settings.drainTimeout = defaultDrainTimeout
	}

	opts := seedBrokerOptions(cfg.BootstrapServers)
	opts = append(opts, kgo.ConsumerGroup(strings.TrimSpace(consumerGroup)))

	switch value := strings.ToLower(strings.TrimSpace(cfg.Subscriber.AutoOffsetReset)); value {
	case "":
	case "earliest":
		offset := kgo.NewOffset().AtStart()
		opts = append(opts, kgo.ConsumeStartOffset(offset), kgo.ConsumeResetOffset(offset))
	case "latest":
		offset := kgo.NewOffset().AtEnd()
		opts = append(opts, kgo.ConsumeStartOffset(offset), kgo.ConsumeResetOffset(offset))
	case "none":
		opts = append(opts,
			kgo.ConsumeStartOffset(kgo.NewOffset().AtCommitted()),
			kgo.ConsumeResetOffset(kgo.NoResetOffset()),
		)
	default:
		return nil, subscriberSettings{}, configError("Subscriber.AutoOffsetReset must be earliest, latest, or none")
	}

	if cfg.Subscriber.SessionTimeoutMs > 0 {
		opts = append(opts, kgo.SessionTimeout(time.Duration(cfg.Subscriber.SessionTimeoutMs)*time.Millisecond))
	}
	if cfg.Subscriber.MaxPollIntervalMs > 0 {
		// franz-go does not expose max.poll.interval.ms. RebalanceTimeout is
		// its group-protocol bound for finishing work and rejoining a rebalance.
		opts = append(opts, kgo.RebalanceTimeout(time.Duration(cfg.Subscriber.MaxPollIntervalMs)*time.Millisecond))
	}
	if mode == CommitModeManualContiguous {
		opts = append(opts, kgo.DisableAutoCommit(), kgo.BlockRebalanceOnPoll())
	}
	return opts, settings, nil
}

func newKafkaClient(opts ...kgo.Opt) (*kgo.Client, error) {
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("kafkakit: create kafka client: %w", err)
	}
	return client, nil
}
