// Package lakekit provides an HTTP client for lake_svc,
// the data lake query service. It supports SQL queries, entity history,
// dataset listing, and dataset statistics.
package lakekit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	chassis "github.com/ai8future/chassis-go/v10"
	"github.com/ai8future/chassis-go/v10/tracekit"
)

// --------------------------------------------------------------------------
// Types
// --------------------------------------------------------------------------

// QueryResult represents the result of a SQL query against the data lake.
type QueryResult struct {
	Columns  []string `json:"columns"`
	Rows     [][]any  `json:"rows"`
	RowCount int      `json:"row_count"`
}

// HistoryEntry represents a temporal event for an entity in the data lake.
type HistoryEntry struct {
	Timestamp time.Time      `json:"timestamp"`
	EventType string         `json:"event_type"`
	Data      map[string]any `json:"data"`
}

// Column describes a column in a dataset schema.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Dataset represents metadata about a dataset in the data lake.
type Dataset struct {
	Name       string    `json:"name"`
	RowCount   int64     `json:"row_count"`
	LastUpdate time.Time `json:"last_update"`
	Schema     []Column  `json:"schema"`
}

// --------------------------------------------------------------------------
// Client
// --------------------------------------------------------------------------

// Client is an HTTP client for lake_svc.
type Client struct {
	baseURL  string
	tenantID string
	http     *http.Client
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithTenant sets the tenant ID for all requests.
func WithTenant(id string) ClientOption {
	return func(c *Client) { c.tenantID = id }
}

// WithTimeout sets the HTTP client timeout.
func WithTimeout(d time.Duration) ClientOption {
	return func(c *Client) { c.http.Timeout = d }
}

// NewClient creates a new lake_svc client.
// Default timeout is 5 seconds.
func NewClient(baseURL string, opts ...ClientOption) *Client {
	chassis.AssertVersionChecked()
	c := &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		tenantID: "",
		http:     &http.Client{Timeout: 5 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// --------------------------------------------------------------------------
// Query
// --------------------------------------------------------------------------

// Query executes a SQL query against the data lake.
func (c *Client) Query(ctx context.Context, sql string, params ...any) (*QueryResult, error) {
	payload := map[string]any{
		"sql": sql,
	}
	if len(params) > 0 {
		payload["params"] = params
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("lakekit: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/query", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("lakekit: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setHeaders(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lakekit: query: %w", err)
	}
	defer resp.Body.Close()

	if err := checkStatus(resp); err != nil {
		return nil, err
	}

	var result QueryResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("lakekit: decode query result: %w", err)
	}
	return &result, nil
}

// --------------------------------------------------------------------------
// EntityHistory
// --------------------------------------------------------------------------

// EntityHistory returns the event history for an entity in the data lake.
func (c *Client) EntityHistory(ctx context.Context, entityID string) ([]HistoryEntry, error) {
	u := c.baseURL + "/v1/entities/" + url.PathEscape(entityID) + "/history"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("lakekit: build request: %w", err)
	}
	c.setHeaders(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lakekit: entity history: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if err := checkStatus(resp); err != nil {
		return nil, err
	}

	var entries []HistoryEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("lakekit: decode history: %w", err)
	}
	return entries, nil
}

// --------------------------------------------------------------------------
// Datasets
// --------------------------------------------------------------------------

// Datasets returns a list of all datasets in the data lake.
func (c *Client) Datasets(ctx context.Context) ([]Dataset, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/datasets", nil)
	if err != nil {
		return nil, fmt.Errorf("lakekit: build request: %w", err)
	}
	c.setHeaders(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lakekit: datasets: %w", err)
	}
	defer resp.Body.Close()

	if err := checkStatus(resp); err != nil {
		return nil, err
	}

	var datasets []Dataset
	if err := json.NewDecoder(resp.Body).Decode(&datasets); err != nil {
		return nil, fmt.Errorf("lakekit: decode datasets: %w", err)
	}
	return datasets, nil
}

// --------------------------------------------------------------------------
// DatasetStats
// --------------------------------------------------------------------------

// DatasetStats returns metadata and statistics for a specific dataset.
func (c *Client) DatasetStats(ctx context.Context, name string) (*Dataset, error) {
	u := c.baseURL + "/v1/datasets/" + url.PathEscape(name) + "/stats"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("lakekit: build request: %w", err)
	}
	c.setHeaders(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lakekit: dataset stats: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if err := checkStatus(resp); err != nil {
		return nil, err
	}

	var dataset Dataset
	if err := json.NewDecoder(resp.Body).Decode(&dataset); err != nil {
		return nil, fmt.Errorf("lakekit: decode dataset: %w", err)
	}
	return &dataset, nil
}

// --------------------------------------------------------------------------
// Internal helpers
// --------------------------------------------------------------------------

// setHeaders adds the standard X-Tenant-ID and X-Trace-ID headers.
func (c *Client) setHeaders(ctx context.Context, req *http.Request) {
	if c.tenantID != "" {
		req.Header.Set("X-Tenant-ID", c.tenantID)
	}
	if tid := tracekit.TraceID(ctx); tid != "" {
		req.Header.Set("X-Trace-ID", tid)
	}
}

// checkStatus inspects the HTTP response status and returns an appropriate error.
func checkStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(resp.Body)
	detail := strings.TrimSpace(string(body))

	switch resp.StatusCode {
	case http.StatusForbidden:
		if detail != "" {
			return fmt.Errorf("lakekit: forbidden: %s", detail)
		}
		return fmt.Errorf("lakekit: forbidden")
	case http.StatusServiceUnavailable:
		return fmt.Errorf("lakekit: service unavailable")
	default:
		return fmt.Errorf("lakekit: unexpected status %d: %s", resp.StatusCode, detail)
	}
}
