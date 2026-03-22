// Package graphkit provides an HTTP client for graphiti_svc,
// the knowledge graph service. It supports entity search, recall,
// Cypher queries, entity graphs, timelines, and path traversal.
package graphkit

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

	chassis "github.com/ai8future/chassis-go/v9"
	"github.com/ai8future/chassis-go/v9/tracekit"
)

// --------------------------------------------------------------------------
// Types
// --------------------------------------------------------------------------

// SearchResult represents a search hit from the knowledge graph.
type SearchResult struct {
	EntityName    string   `json:"entity_name"`
	Relationships []string `json:"relationships"`
	Confidence    float64  `json:"confidence"`
	Context       string   `json:"context"`
}

// CypherResult represents the result of a Cypher query.
type CypherResult struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// GraphResult represents an entity and its neighborhood in the graph.
type GraphResult struct {
	Name          string        `json:"name"`
	Relationships []string      `json:"relationships"`
	Neighbors     []GraphResult `json:"neighbors"`
}

// TimelineEntry represents a temporal event for an entity.
type TimelineEntry struct {
	Timestamp    time.Time `json:"timestamp"`
	Relationship string    `json:"relationship"`
	Entity       string    `json:"entity"`
	Action       string    `json:"action"`
}

// Path represents a traversal path between two entities.
type Path struct {
	Nodes []string `json:"nodes"`
	Edges []string `json:"edges"`
	Hops  int      `json:"hops"`
}

// --------------------------------------------------------------------------
// Client
// --------------------------------------------------------------------------

// Client is an HTTP client for graphiti_svc.
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

// NewClient creates a new graphiti_svc client.
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
// Search
// --------------------------------------------------------------------------

// Search queries the knowledge graph and returns matching entities.
func (c *Client) Search(ctx context.Context, query string) ([]SearchResult, error) {
	params := url.Values{}
	params.Set("q", query)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/search?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("graphkit: build request: %w", err)
	}
	c.setHeaders(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graphkit: search: %w", err)
	}
	defer resp.Body.Close()

	if err := checkStatus(resp); err != nil {
		return nil, err
	}

	var results []SearchResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("graphkit: decode search results: %w", err)
	}
	return results, nil
}

// --------------------------------------------------------------------------
// Recall
// --------------------------------------------------------------------------

// Recall retrieves entities from the knowledge graph, optionally at a
// specific point in time.
func (c *Client) Recall(ctx context.Context, query string, at *time.Time) ([]SearchResult, error) {
	params := url.Values{}
	params.Set("q", query)
	if at != nil {
		params.Set("at", at.Format(time.RFC3339))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/recall?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("graphkit: build request: %w", err)
	}
	c.setHeaders(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graphkit: recall: %w", err)
	}
	defer resp.Body.Close()

	if err := checkStatus(resp); err != nil {
		return nil, err
	}

	var results []SearchResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("graphkit: decode recall results: %w", err)
	}
	return results, nil
}

// --------------------------------------------------------------------------
// Cypher
// --------------------------------------------------------------------------

// Cypher executes a Cypher query against the knowledge graph.
func (c *Client) Cypher(ctx context.Context, query string, params map[string]any) (*CypherResult, error) {
	payload := map[string]any{
		"query": query,
	}
	if params != nil {
		payload["params"] = params
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("graphkit: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/cypher", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("graphkit: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setHeaders(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graphkit: cypher: %w", err)
	}
	defer resp.Body.Close()

	if err := checkStatus(resp); err != nil {
		return nil, err
	}

	var result CypherResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("graphkit: decode cypher result: %w", err)
	}
	return &result, nil
}

// --------------------------------------------------------------------------
// EntityGraph
// --------------------------------------------------------------------------

type graphParams struct {
	depth int
}

// GraphOption configures an EntityGraph call.
type GraphOption func(*graphParams)

// Depth sets the traversal depth for the graph query.
func Depth(n int) GraphOption {
	return func(p *graphParams) { p.depth = n }
}

// EntityGraph returns the graph neighborhood of an entity.
func (c *Client) EntityGraph(ctx context.Context, entityName string, opts ...GraphOption) (*GraphResult, error) {
	p := &graphParams{depth: 1}
	for _, o := range opts {
		o(p)
	}

	params := url.Values{}
	params.Set("depth", fmt.Sprintf("%d", p.depth))

	u := c.baseURL + "/v1/entities/" + url.PathEscape(entityName) + "/graph?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("graphkit: build request: %w", err)
	}
	c.setHeaders(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graphkit: entity graph: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if err := checkStatus(resp); err != nil {
		return nil, err
	}

	var result GraphResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("graphkit: decode graph result: %w", err)
	}
	return &result, nil
}

// --------------------------------------------------------------------------
// EntityTimeline
// --------------------------------------------------------------------------

// EntityTimeline returns the temporal event history for an entity.
func (c *Client) EntityTimeline(ctx context.Context, entityName string) ([]TimelineEntry, error) {
	u := c.baseURL + "/v1/entities/" + url.PathEscape(entityName) + "/timeline"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("graphkit: build request: %w", err)
	}
	c.setHeaders(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graphkit: entity timeline: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if err := checkStatus(resp); err != nil {
		return nil, err
	}

	var entries []TimelineEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("graphkit: decode timeline: %w", err)
	}
	return entries, nil
}

// --------------------------------------------------------------------------
// Paths
// --------------------------------------------------------------------------

type pathParams struct {
	maxHops int
}

// PathOption configures a Paths call.
type PathOption func(*pathParams)

// MaxHops sets the maximum number of hops for path traversal.
func MaxHops(n int) PathOption {
	return func(p *pathParams) { p.maxHops = n }
}

// Paths finds paths between two entities in the knowledge graph.
func (c *Client) Paths(ctx context.Context, from, to string, opts ...PathOption) ([]Path, error) {
	p := &pathParams{maxHops: 3}
	for _, o := range opts {
		o(p)
	}

	params := url.Values{}
	params.Set("from", from)
	params.Set("to", to)
	params.Set("max_hops", fmt.Sprintf("%d", p.maxHops))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/paths?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("graphkit: build request: %w", err)
	}
	c.setHeaders(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graphkit: paths: %w", err)
	}
	defer resp.Body.Close()

	if err := checkStatus(resp); err != nil {
		return nil, err
	}

	var paths []Path
	if err := json.NewDecoder(resp.Body).Decode(&paths); err != nil {
		return nil, fmt.Errorf("graphkit: decode paths: %w", err)
	}
	return paths, nil
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
			return fmt.Errorf("graphkit: forbidden: %s", detail)
		}
		return fmt.Errorf("graphkit: forbidden")
	case http.StatusServiceUnavailable:
		return fmt.Errorf("graphkit: service unavailable")
	default:
		return fmt.Errorf("graphkit: unexpected status %d: %s", resp.StatusCode, detail)
	}
}
