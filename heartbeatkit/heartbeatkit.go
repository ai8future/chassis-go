// Package heartbeatkit provides zero-config automatic liveness events.
// It publishes heartbeat payloads to "ai8.infra.heartbeat" at a fixed interval.
package heartbeatkit

import (
	"context"
	"os"
	"sync"
	"time"

	chassis "github.com/ai8future/chassis-go/v9"
)

// Config holds heartbeat configuration.
type Config struct {
	Interval    time.Duration // default 30s
	ServiceName string
	Version     string
}

// publisher interface — matches kafkakit.Publisher's Publish method via dependency inversion.
type publisher interface {
	Publish(ctx context.Context, subject string, data any) error
}

// statsProvider optionally enriches heartbeat payloads with publisher stats.
type statsProvider interface {
	Stats() struct {
		EventsPublished1h int64
		Errors1h          int64
		LastEventPublished time.Time
	}
}

var (
	stopCh chan struct{}
	mu     sync.Mutex
)

// Start begins publishing heartbeat events at the configured interval.
// If cfg.Interval is zero, it defaults to 30 seconds.
func Start(ctx context.Context, pub publisher, cfg Config) {
	chassis.AssertVersionChecked()
	mu.Lock()
	defer mu.Unlock()
	if cfg.Interval == 0 {
		cfg.Interval = 30 * time.Second
	}
	stopCh = make(chan struct{})
	startTime := time.Now()
	hostname, _ := os.Hostname()
	pid := os.Getpid()

	go func() {
		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				payload := map[string]any{
					"service":  cfg.ServiceName,
					"host":     hostname,
					"pid":      pid,
					"uptime_s": int64(time.Since(startTime).Seconds()),
					"version":  cfg.Version,
					"status":   "healthy",
				}
				// Add stats if publisher supports it
				if sp, ok := pub.(statsProvider); ok {
					stats := sp.Stats()
					payload["events_published_1h"] = stats.EventsPublished1h
					payload["errors_1h"] = stats.Errors1h
					if !stats.LastEventPublished.IsZero() {
						payload["last_event_published"] = stats.LastEventPublished.Format(time.RFC3339)
					}
				}
				_ = pub.Publish(ctx, "ai8.infra.heartbeat", payload)
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			}
		}
	}()
}

// Stop halts the heartbeat publisher.
func Stop() {
	mu.Lock()
	defer mu.Unlock()
	if stopCh != nil {
		close(stopCh)
		stopCh = nil
	}
}
