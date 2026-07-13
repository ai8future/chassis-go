// Package kafkakit provides publish/subscribe to Redpanda via the Kafka protocol,
// with envelope wrapping, tenant filtering, dead letter queue routing, and stats.
package kafkakit

import (
	"context"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// CommitModeLegacyAuto preserves the v11 auto-commit behavior. It can
	// acknowledge records before their handlers finish and is deprecated.
	CommitModeLegacyAuto CommitMode = "legacy-auto"
	// CommitModeManualContiguous commits only a durable contiguous prefix in
	// every partition after handler or DLQ completion.
	CommitModeManualContiguous CommitMode = "manual-contiguous"

	defaultMaxPollRecords = 500
	defaultDLQTimeout     = 10 * time.Second
	defaultDrainTimeout   = 30 * time.Second
)

// CommitMode selects subscriber offset ownership semantics.
type CommitMode string

// Config holds all kafkakit configuration.
type Config struct {
	BootstrapServers  string
	SchemaRegistryURL string
	TenantID          string
	Source            string // publisher identity — comes from config, NOT constructor
	Publisher         PublisherConfig
	Subscriber        SubscriberConfig
	TenantFilter      TenantFilterConfig
}

// Enabled returns true if BootstrapServers is configured.
func (c Config) Enabled() bool { return c.BootstrapServers != "" }

// PublisherConfig holds Kafka producer settings.
type PublisherConfig struct {
	Acks           string
	Compression    string
	MaxRetries     int
	RetryBackoffMs int
	LingerMs       int  // zero preserves the franz-go default
	DisableLinger  bool // explicitly select zero linger; conflicts with positive LingerMs
}

// SubscriberConfig holds Kafka consumer settings.
type SubscriberConfig struct {
	AutoOffsetReset  string
	EnableAutoCommit bool
	MaxPollRecords   int
	SessionTimeoutMs int
	Concurrency      int // 0 or 1 = sequential; >1 = parallel partition workers

	// CommitMode selects legacy auto commit or durable contiguous manual
	// commits. An empty value preserves v11 compatibility: AtLeastOnce selects
	// manual-contiguous, otherwise legacy-auto.
	CommitMode CommitMode
	// AtLeastOnce is the v11 compatibility switch for manual-contiguous mode.
	// Prefer CommitMode for new code.
	AtLeastOnce bool
	// DLQTimeoutMs bounds each dead-letter publication. Zero uses 10 seconds.
	DLQTimeoutMs int
	// DrainTimeoutMs bounds shutdown and current-batch drain. Zero uses 30 seconds.
	DrainTimeoutMs int
}

// TenantFilterConfig holds tenant filtering settings.
type TenantFilterConfig struct {
	Enabled        bool
	GrantsCacheTTL int // seconds
	GrantsURL      string
}

// Event represents an inbound event received from a topic.
type Event struct {
	ID         string
	Timestamp  time.Time
	Source     string
	Subject    string
	TraceID    string
	TenantID   string
	Version    string
	EntityRefs []string
	Data       map[string]any
	Raw        []byte
	headers    map[string]string
}

// Ack is a deprecated no-op retained for v11 source compatibility. The
// subscriber owns record disposition; use CommitModeManualContiguous for
// durable offset handling.
func (e *Event) Ack() error { return nil }

// Reject is a deprecated no-op retained for v11 source compatibility. Return
// an error from the handler to request dead-letter routing.
func (e *Event) Reject() error { return nil }

// Header returns the value of a message header by key. Returns empty string
// if the header is not present. Header keys are matched exactly; when Kafka
// contains duplicate keys the last value wins, matching franz-go conventions.
func (e *Event) Header(key string) string {
	if e == nil {
		return ""
	}
	return e.headers[key]
}

func resolveCommitMode(cfg SubscriberConfig) (CommitMode, error) {
	mode := CommitMode(strings.ToLower(strings.TrimSpace(string(cfg.CommitMode))))
	if mode == "" {
		switch {
		case cfg.AtLeastOnce && cfg.EnableAutoCommit:
			return "", configError("Subscriber.AtLeastOnce conflicts with Subscriber.EnableAutoCommit")
		case cfg.AtLeastOnce:
			return CommitModeManualContiguous, nil
		default:
			return CommitModeLegacyAuto, nil
		}
	}

	switch mode {
	case CommitModeLegacyAuto:
		if cfg.AtLeastOnce {
			return "", configError("Subscriber.CommitMode=legacy-auto conflicts with Subscriber.AtLeastOnce")
		}
		return mode, nil
	case CommitModeManualContiguous:
		if cfg.EnableAutoCommit {
			return "", configError("Subscriber.CommitMode=manual-contiguous conflicts with Subscriber.EnableAutoCommit")
		}
		return mode, nil
	default:
		return "", configError("Subscriber.CommitMode must be legacy-auto or manual-contiguous")
	}
}

type configError string

func (e configError) Error() string { return "kafkakit: invalid configuration: " + string(e) }

// HandlerFunc processes an inbound event. Return a non-nil error to trigger
// DLQ routing.
type HandlerFunc func(ctx context.Context, evt Event) error

// OutboundEvent represents an event to be published.
type OutboundEvent struct {
	Subject    string
	Data       any
	EntityRefs []string
}

// Stats contains publisher statistics.
type Stats struct {
	EventsPublishedTotal int64
	ErrorsTotal          int64
	LastEventPublished   time.Time
}

// publisherStats provides thread-safe counters for publisher metrics.
type publisherStats struct {
	published     atomic.Int64
	errors        atomic.Int64
	lastPublished atomic.Int64 // unix nano
}

func (s *publisherStats) incPublished() {
	s.published.Add(1)
	s.lastPublished.Store(time.Now().UnixNano())
}

func (s *publisherStats) incErrors() {
	s.errors.Add(1)
}

func (s *publisherStats) snapshot() Stats {
	var last time.Time
	if ns := s.lastPublished.Load(); ns > 0 {
		last = time.Unix(0, ns)
	}
	return Stats{
		EventsPublishedTotal: s.published.Load(),
		ErrorsTotal:          s.errors.Load(),
		LastEventPublished:   last,
	}
}
