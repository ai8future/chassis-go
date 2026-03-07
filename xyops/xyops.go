// Package xyops provides a client for the xyops API with curated methods,
// optional monitoring bridge, and a raw escape hatch. It leverages chassis
// modules (call, cache, tick, webhook) rather than hand-rolling infrastructure.
package xyops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	chassis "github.com/ai8future/chassis-go/v8"
	"github.com/ai8future/chassis-go/v8/cache"
	"github.com/ai8future/chassis-go/v8/call"
	"github.com/ai8future/chassis-go/v8/tick"
	"github.com/ai8future/chassis-go/v8/webhook"
)

// Config holds the environment-driven configuration for the xyops client.
type Config struct {
	BaseURL         string `env:"XYOPS_BASE_URL" required:"true"`
	APIKey          string `env:"XYOPS_API_KEY" required:"true"`
	ServiceName     string `env:"XYOPS_SERVICE_NAME"`
	MonitorEnabled  bool   `env:"XYOPS_MONITOR_ENABLED" default:"false"`
	MonitorInterval int    `env:"XYOPS_MONITOR_INTERVAL" default:"30"`
}

// Option configures a Client.
type Option func(*Client)

// WithMonitoring enables the monitoring bridge with the given push interval
// in seconds. The bridge pushes bridged metrics to the xyops API on a timer.
func WithMonitoring(intervalSec int) Option {
	return func(c *Client) {
		c.monitorEnabled = true
		c.monitorInterval = intervalSec
	}
}

// MetricGauge is the interface a metric must implement to be bridged to xyops.
type MetricGauge interface {
	Value() float64
}

// BridgeMetric registers a named metric gauge to be pushed during monitoring.
func BridgeMetric(name string, gauge MetricGauge) Option {
	return func(c *Client) {
		c.bridgedMetrics[name] = gauge
	}
}

// Client is the xyops API client. It provides curated methods for common
// operations plus a Raw escape hatch for anything not covered.
type Client struct {
	config          Config
	httpClient      *call.Client
	cache           *cache.Cache[string, json.RawMessage]
	webhookSender   *webhook.Sender
	monitorEnabled  bool
	monitorInterval int
	bridgedMetrics  map[string]MetricGauge
}

// JobStatus represents the state of a job in xyops.
type JobStatus struct {
	ID       string `json:"id"`
	EventID  string `json:"event_id"`
	State    string `json:"state"`
	Progress int    `json:"progress"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

// Event represents an xyops event definition.
type Event struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Alert represents an xyops alert.
type Alert struct {
	ID      string `json:"id"`
	Message string `json:"message"`
	State   string `json:"state"`
}

// New creates an xyops Client with the given config and options.
func New(cfg Config, opts ...Option) *Client {
	chassis.AssertVersionChecked()
	c := &Client{
		config:         cfg,
		httpClient:     call.New(call.WithTimeout(30 * time.Second)),
		cache:          cache.New[string, json.RawMessage](cache.MaxSize(500), cache.TTL(5*time.Minute), cache.Name("xyops")),
		webhookSender:  webhook.NewSender(),
		bridgedMetrics: make(map[string]MetricGauge),
	}
	for _, o := range opts {
		o(c)
	}
	if cfg.MonitorEnabled {
		c.monitorEnabled = true
	}
	if c.monitorInterval == 0 {
		c.monitorInterval = cfg.MonitorInterval
	}
	return c
}

// apiRequest performs a single HTTP request against the xyops API, returning
// the raw JSON response body.
func (c *Client) apiRequest(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var bodyReader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("xyops: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	u := c.config.BaseURL + path
	var req *http.Request
	var err error
	if bodyReader != nil {
		req, err = http.NewRequestWithContext(ctx, method, u, bodyReader)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, u, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("xyops: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("xyops: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("xyops: read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("xyops: %s %s: HTTP %d: %s", method, path, resp.StatusCode, string(respBody))
	}

	return json.RawMessage(respBody), nil
}

// --- Curated API ---

// RunEvent triggers an event by ID and returns the created job ID.
func (c *Client) RunEvent(ctx context.Context, eventID string, params map[string]string) (string, error) {
	resp, err := c.apiRequest(ctx, "POST", "/api/events/"+eventID+"/run", params)
	if err != nil {
		return "", err
	}
	var result struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return "", fmt.Errorf("xyops: unmarshal RunEvent response: %w", err)
	}
	return result.JobID, nil
}

// GetJobStatus retrieves the status of a job. Results are cached briefly to
// avoid redundant API calls for frequently-polled jobs.
func (c *Client) GetJobStatus(ctx context.Context, jobID string) (*JobStatus, error) {
	if cached, ok := c.cache.Get("job:" + jobID); ok {
		var status JobStatus
		if err := json.Unmarshal(cached, &status); err != nil {
			return nil, fmt.Errorf("xyops: unmarshal cached job status: %w", err)
		}
		return &status, nil
	}
	resp, err := c.apiRequest(ctx, "GET", "/api/jobs/"+jobID, nil)
	if err != nil {
		return nil, err
	}
	c.cache.Set("job:"+jobID, resp)
	var status JobStatus
	if err := json.Unmarshal(resp, &status); err != nil {
		return nil, fmt.Errorf("xyops: unmarshal job status: %w", err)
	}
	return &status, nil
}

// CancelJob aborts a running job and invalidates any cached status.
func (c *Client) CancelJob(ctx context.Context, jobID string) error {
	_, err := c.apiRequest(ctx, "POST", "/api/jobs/"+jobID+"/cancel", nil)
	c.cache.Delete("job:" + jobID)
	return err
}

// SearchJobs searches job history with a query string.
func (c *Client) SearchJobs(ctx context.Context, query string) ([]JobStatus, error) {
	resp, err := c.apiRequest(ctx, "GET", "/api/jobs?q="+url.QueryEscape(query), nil)
	if err != nil {
		return nil, err
	}
	var jobs []JobStatus
	if err := json.Unmarshal(resp, &jobs); err != nil {
		return nil, fmt.Errorf("xyops: unmarshal SearchJobs response: %w", err)
	}
	return jobs, nil
}

// ListEvents returns all available event definitions.
func (c *Client) ListEvents(ctx context.Context) ([]Event, error) {
	resp, err := c.apiRequest(ctx, "GET", "/api/events", nil)
	if err != nil {
		return nil, err
	}
	var events []Event
	if err := json.Unmarshal(resp, &events); err != nil {
		return nil, fmt.Errorf("xyops: unmarshal ListEvents response: %w", err)
	}
	return events, nil
}

// GetEvent retrieves a single event by ID.
func (c *Client) GetEvent(ctx context.Context, eventID string) (*Event, error) {
	resp, err := c.apiRequest(ctx, "GET", "/api/events/"+eventID, nil)
	if err != nil {
		return nil, err
	}
	var event Event
	if err := json.Unmarshal(resp, &event); err != nil {
		return nil, fmt.Errorf("xyops: unmarshal GetEvent response: %w", err)
	}
	return &event, nil
}

// ListActiveAlerts returns all currently-firing alerts.
func (c *Client) ListActiveAlerts(ctx context.Context) ([]Alert, error) {
	resp, err := c.apiRequest(ctx, "GET", "/api/alerts?state=firing", nil)
	if err != nil {
		return nil, err
	}
	var alerts []Alert
	if err := json.Unmarshal(resp, &alerts); err != nil {
		return nil, fmt.Errorf("xyops: unmarshal ListActiveAlerts response: %w", err)
	}
	return alerts, nil
}

// AckAlert acknowledges an alert by ID.
func (c *Client) AckAlert(ctx context.Context, alertID string) error {
	_, err := c.apiRequest(ctx, "POST", "/api/alerts/"+alertID+"/ack", nil)
	return err
}

// Ping verifies connectivity to the xyops API.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.apiRequest(ctx, "GET", "/api/ping", nil)
	return err
}

// FireWebhook triggers a webhook via the xyops webhook endpoint using the
// chassis webhook.Sender (with retry, HMAC signature, delivery tracking).
func (c *Client) FireWebhook(ctx context.Context, hookID string, data any) (string, error) {
	return c.webhookSender.Send(c.config.BaseURL+"/api/webhooks/"+hookID, data, c.config.APIKey)
}

// Raw is an escape hatch that sends an arbitrary request to any xyops API
// endpoint and returns the raw JSON response.
func (c *Client) Raw(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	return c.apiRequest(ctx, method, path, body)
}

// Run returns a lifecycle component for the monitoring bridge. When monitoring
// is disabled it blocks until the context is cancelled. When enabled it pushes
// bridged metrics to the xyops API on the configured interval using tick.Every.
func (c *Client) Run(ctx context.Context) error {
	if !c.monitorEnabled {
		<-ctx.Done()
		return nil
	}
	interval := time.Duration(c.monitorInterval) * time.Second
	return tick.Every(interval, func(ctx context.Context) error {
		metrics := make(map[string]float64)
		for name, gauge := range c.bridgedMetrics {
			metrics[name] = gauge.Value()
		}
		_, err := c.apiRequest(ctx, "POST", "/api/monitoring/push", map[string]any{
			"service": c.config.ServiceName,
			"metrics": metrics,
		})
		return err
	}, tick.Immediate())(ctx)
}
