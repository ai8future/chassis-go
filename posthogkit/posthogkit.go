// Package posthogkit provides a non-blocking, batched PostHog analytics client.
// Events are buffered in memory and flushed periodically via tick or when the
// buffer reaches FlushSize. All capture methods are no-ops when Enabled is false.
package posthogkit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/call"
	"github.com/ai8future/chassis-go/v11/health"
	"github.com/ai8future/chassis-go/v11/seal"
	"github.com/ai8future/chassis-go/v11/tick"
)

// --------------------------------------------------------------------------
// Config
// --------------------------------------------------------------------------

// Config holds PostHog client configuration. Fields are populated by
// config.MustLoad[Config]() from environment variables using the env tags.
type Config struct {
	APIKey        string        `env:"POSTHOG_API_KEY"        required:"false"`
	Host          string        `env:"POSTHOG_HOST"           default:"https://us.i.posthog.com"`
	FlushInterval time.Duration `env:"POSTHOG_FLUSH_INTERVAL" default:"30s"`
	FlushSize     int           `env:"POSTHOG_FLUSH_SIZE"     default:"100"`
	Enabled       bool          `env:"POSTHOG_ENABLED"        default:"true"`
	HMACSecret    string        `env:"POSTHOG_HMAC_SECRET"    required:"false"`
}

// --------------------------------------------------------------------------
// Internal types
// --------------------------------------------------------------------------

// event is a single PostHog event in the batch buffer.
type event struct {
	Type       string         `json:"type"`
	Event      string         `json:"event,omitempty"`
	DistinctID string         `json:"distinct_id"`
	Properties map[string]any `json:"properties,omitempty"`
	Set        map[string]any `json:"$set,omitempty"`
	Timestamp  string         `json:"timestamp"`
}

// --------------------------------------------------------------------------
// Client
// --------------------------------------------------------------------------

// Client is a non-blocking, batched PostHog analytics client.
type Client struct {
	cfg  Config
	http *call.Client
	log  *slog.Logger
	mu   sync.Mutex
	buf  []event
}

// New creates a new PostHog client. Defaults are applied for Host
// ("https://us.i.posthog.com"), FlushInterval (30s), and FlushSize (100).
func New(cfg Config) *Client {
	chassis.AssertVersionChecked()
	if cfg.Host == "" {
		cfg.Host = "https://us.i.posthog.com"
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 30 * time.Second
	}
	if cfg.FlushSize <= 0 {
		cfg.FlushSize = 100
	}
	return &Client{
		cfg:  cfg,
		http: call.New(call.WithTimeout(10 * time.Second)),
		log:  slog.Default(),
		buf:  make([]event, 0, cfg.FlushSize),
	}
}

// --------------------------------------------------------------------------
// Capture
// --------------------------------------------------------------------------

// Capture records an analytics event.
func (c *Client) Capture(distinctID, eventName string, props map[string]any) {
	if !c.cfg.Enabled {
		return
	}
	c.enqueue(event{
		Type:       "capture",
		Event:      eventName,
		DistinctID: distinctID,
		Properties: props,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	})
}

// CaptureWithGroups records an event with group context.
func (c *Client) CaptureWithGroups(distinctID, eventName string, props map[string]any, groups map[string]string) {
	if !c.cfg.Enabled {
		return
	}
	merged := make(map[string]any, len(props)+1)
	for k, v := range props {
		merged[k] = v
	}
	gm := make(map[string]any, len(groups))
	for k, v := range groups {
		gm[k] = v
	}
	merged["$groups"] = gm
	c.enqueue(event{
		Type:       "capture",
		Event:      eventName,
		DistinctID: distinctID,
		Properties: merged,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	})
}

// --------------------------------------------------------------------------
// Identify
// --------------------------------------------------------------------------

// Identify associates properties with a distinct user.
func (c *Client) Identify(distinctID string, props map[string]any) {
	if !c.cfg.Enabled {
		return
	}
	c.enqueue(event{
		Type:       "identify",
		Event:      "$identify",
		DistinctID: distinctID,
		Set:        props,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	})
}

// GroupIdentify associates properties with a group.
func (c *Client) GroupIdentify(groupType, groupKey string, props map[string]any) {
	if !c.cfg.Enabled {
		return
	}
	c.enqueue(event{
		Type:       "capture",
		Event:      "$groupidentify",
		DistinctID: fmt.Sprintf("%s_%s", groupType, groupKey),
		Properties: map[string]any{
			"$group_type": groupType,
			"$group_key":  groupKey,
			"$group_set":  props,
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

// --------------------------------------------------------------------------
// Privacy
// --------------------------------------------------------------------------

// HashID returns an HMAC-SHA256 hex digest of id using the configured
// HMACSecret. Returns id unchanged if no secret is configured.
func (c *Client) HashID(id string) string {
	if c.cfg.HMACSecret == "" {
		return id
	}
	return seal.Sign([]byte(id), c.cfg.HMACSecret)
}

// --------------------------------------------------------------------------
// Flush lifecycle
// --------------------------------------------------------------------------

// Flusher returns a tick-compatible function that periodically flushes
// the event buffer. Pass the result to lifecycle.Run.
func (c *Client) Flusher() func(context.Context) error {
	return tick.Every(c.cfg.FlushInterval, c.flush)
}

// Close performs a final synchronous flush of any buffered events.
func (c *Client) Close() {
	if err := c.flush(context.Background()); err != nil {
		c.log.Warn("posthogkit: final flush failed", "error", err)
	}
}

// Check returns a health.Check that reports healthy when the client is
// enabled and configured with an API key. Register with health.All().
func (c *Client) Check() health.Check {
	return func(ctx context.Context) error {
		if !c.cfg.Enabled {
			return nil
		}
		if c.cfg.APIKey == "" {
			return fmt.Errorf("posthogkit: enabled but POSTHOG_API_KEY is empty")
		}
		return nil
	}
}

// --------------------------------------------------------------------------
// Internal
// --------------------------------------------------------------------------

// enqueue appends an event and triggers an async flush when the buffer
// reaches FlushSize.
func (c *Client) enqueue(e event) {
	c.mu.Lock()
	c.buf = append(c.buf, e)
	shouldFlush := len(c.buf) >= c.cfg.FlushSize
	c.mu.Unlock()
	if shouldFlush {
		go func() {
			if err := c.flush(context.Background()); err != nil {
				c.log.Warn("posthogkit: auto-flush failed", "error", err)
			}
		}()
	}
}

// flush sends all buffered events to the PostHog /batch endpoint.
func (c *Client) flush(ctx context.Context) error {
	c.mu.Lock()
	if len(c.buf) == 0 {
		c.mu.Unlock()
		return nil
	}
	batch := c.buf
	c.buf = make([]event, 0, c.cfg.FlushSize)
	c.mu.Unlock()

	requeue := func() {
		c.mu.Lock()
		c.buf = append(batch, c.buf...)
		c.mu.Unlock()
	}

	payload := map[string]any{
		"api_key": c.cfg.APIKey,
		"batch":   batch,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		requeue()
		return fmt.Errorf("posthogkit: marshal batch: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.cfg.Host, "/")+"/batch",
		bytes.NewReader(body))
	if err != nil {
		requeue()
		return fmt.Errorf("posthogkit: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		requeue()
		return fmt.Errorf("posthogkit: send batch: %w", err)
	}
	defer resp.Body.Close()

	// Drain body to enable HTTP connection reuse.
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		requeue()
		detail := strings.TrimSpace(string(respBody))
		if detail != "" {
			return fmt.Errorf("posthogkit: unexpected status %d: %s", resp.StatusCode, detail)
		}
		return fmt.Errorf("posthogkit: unexpected status %d", resp.StatusCode)
	}
	return nil
}
