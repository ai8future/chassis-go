// Package kafkakit provides publish/subscribe to Redpanda via the Kafka protocol,
// with envelope wrapping, tenant filtering, dead letter queue routing, and stats.
package kafkakit

import (
	"context"
	"sync/atomic"
	"time"
)

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
	LingerMs       int
}

// SubscriberConfig holds Kafka consumer settings.
type SubscriberConfig struct {
	AutoOffsetReset  string
	EnableAutoCommit bool
	MaxPollRecords   int
	SessionTimeoutMs int
	Concurrency      int  // 0 or 1 = sequential; >1 = parallel workers
	AtLeastOnce      bool // When true, offsets are committed only after all handlers in a batch complete. Use for slow handlers (>1s). Handler errors are committed only after successful DLQ routing.
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
}

// Ack acknowledges the event. Currently a no-op — offset commit is handled
// by the subscriber's auto-commit or explicit commit.
func (e *Event) Ack() error { return nil }

// Reject rejects the event. Currently a no-op — DLQ routing is handled by
// the subscriber when a handler returns an error.
func (e *Event) Reject() error { return nil }

// Header returns the value of a message header by key. Returns empty string
// if the header is not present (headers will be populated in integration).
func (e *Event) Header(key string) string { return "" }

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
