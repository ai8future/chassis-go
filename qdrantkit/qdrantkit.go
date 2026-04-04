// Package qdrantkit provides an HTTP client for Qdrant vector database.
// Built on chassis call.Client for retry, circuit breaker, and OTel tracing.
//
// Replaces custom Qdrant REST clients in equinox_graph, equinox_api,
// airborne, bizops, and agent-memory-benchmark.
package qdrantkit

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
	"github.com/ai8future/chassis-go/v10/call"
	"github.com/ai8future/chassis-go/v10/health"
	"github.com/ai8future/chassis-go/v10/tracekit"
)

// --------------------------------------------------------------------------
// Config
// --------------------------------------------------------------------------

// Config holds Qdrant connection settings.
type Config struct {
	BaseURL string        `env:"QDRANT_URL" default:"http://localhost:6333"`
	APIKey  string        `env:"QDRANT_API_KEY"`
	Timeout time.Duration `env:"QDRANT_TIMEOUT" default:"10s"`
}

// --------------------------------------------------------------------------
// Types
// --------------------------------------------------------------------------

// Distance is the vector similarity metric.
type Distance string

const (
	Cosine    Distance = "Cosine"
	Euclid    Distance = "Euclid"
	Dot       Distance = "Dot"
	Manhattan Distance = "Manhattan"
)

// CollectionConfig defines parameters for creating a collection.
type CollectionConfig struct {
	Dimension int
	Distance  Distance
}

// CollectionInfo contains details about a collection.
type CollectionInfo struct {
	Status       string `json:"status"`
	VectorsCount int64  `json:"vectors_count"`
	PointsCount  int64  `json:"points_count"`
}

// Point is a vector with ID and optional payload, used for upsert.
type Point struct {
	ID      string         `json:"id"`
	Vector  []float32      `json:"vector"`
	Payload map[string]any `json:"payload,omitempty"`
}

// ScoredPoint is a search result with similarity score.
type ScoredPoint struct {
	ID      string         `json:"id"`
	Score   float32        `json:"score"`
	Payload map[string]any `json:"payload,omitempty"`
	Vector  []float32      `json:"vector,omitempty"`
}

// SearchOptions configures a vector search.
type SearchOptions struct {
	Limit          int
	Filter         *Filter
	WithPayload    bool
	WithVector     bool
	ScoreThreshold *float32
}

// ScrollOptions configures a scroll (paginated scan) query.
type ScrollOptions struct {
	Limit       int
	Filter      *Filter
	Offset      *string
	WithPayload bool
	WithVector  bool
}

// ScrollResult is a page from a scroll query.
type ScrollResult struct {
	Points         []ScoredPoint
	NextPageOffset *string
}

// --------------------------------------------------------------------------
// Client
// --------------------------------------------------------------------------

// Client is an HTTP client for Qdrant backed by call.Client.
type Client struct {
	baseURL string
	apiKey  string
	http    *call.Client
}

// options collects configuration before building the call.Client.
type options struct {
	callOptions []call.Option
}

// Option configures a Client.
type Option func(*options)

// WithTimeout overrides the default per-request timeout.
func WithTimeout(d time.Duration) Option {
	return func(o *options) { o.callOptions = append(o.callOptions, call.WithTimeout(d)) }
}

// WithRetry enables automatic retries for transient (5xx) errors using
// exponential backoff with jitter.
func WithRetry(maxAttempts int, baseDelay time.Duration) Option {
	return func(o *options) { o.callOptions = append(o.callOptions, call.WithRetry(maxAttempts, baseDelay)) }
}

// WithCircuitBreaker protects the client with a named circuit breaker.
func WithCircuitBreaker(name string, threshold int, cooldown time.Duration) Option {
	return func(o *options) {
		o.callOptions = append(o.callOptions, call.WithCircuitBreaker(name, threshold, cooldown))
	}
}

// WithHTTPClient replaces the underlying *http.Client used by call.Client.
func WithHTTPClient(hc *http.Client) Option {
	return func(o *options) { o.callOptions = append(o.callOptions, call.WithHTTPClient(hc)) }
}

// New creates a new Qdrant client.
func New(cfg Config, opts ...Option) *Client {
	chassis.AssertVersionChecked()

	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	// Build call.Client options: config defaults first, then user overrides.
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	callOpts := []call.Option{call.WithTimeout(timeout)}
	callOpts = append(callOpts, o.callOptions...)

	return &Client{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		http:    call.New(callOpts...),
	}
}

// --------------------------------------------------------------------------
// Health
// --------------------------------------------------------------------------

// Check returns a health.Check that pings Qdrant. Register with health.All().
func (c *Client) Check() health.Check {
	return c.Ping
}

// Ping checks Qdrant connectivity by listing collections.
func (c *Client) Ping(ctx context.Context) error {
	resp, err := c.doReq(ctx, http.MethodGet, "/collections", nil)
	if err != nil {
		return fmt.Errorf("qdrantkit: ping: %w", err)
	}
	defer drainClose(resp)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("qdrantkit: ping: status %d", resp.StatusCode)
	}
	return nil
}

// --------------------------------------------------------------------------
// Collections
// --------------------------------------------------------------------------

// CreateCollection creates a collection with the given vector configuration.
// Returns nil if the collection already exists (HTTP 409).
func (c *Client) CreateCollection(ctx context.Context, name string, cfg CollectionConfig) error {
	dist := cfg.Distance
	if dist == "" {
		dist = Cosine
	}
	body := map[string]any{
		"vectors": map[string]any{
			"size":     cfg.Dimension,
			"distance": string(dist),
		},
	}
	resp, err := c.doJSON(ctx, http.MethodPut, collPath(name), body)
	if err != nil {
		return fmt.Errorf("qdrantkit: create collection %s: %w", name, err)
	}
	defer drainClose(resp)
	if resp.StatusCode == http.StatusConflict {
		return nil // already exists
	}
	if resp.StatusCode >= 300 {
		return respErr("create collection "+name, resp)
	}
	return nil
}

// DeleteCollection deletes a collection. Returns nil if not found (HTTP 404).
func (c *Client) DeleteCollection(ctx context.Context, name string) error {
	resp, err := c.doReq(ctx, http.MethodDelete, collPath(name), nil)
	if err != nil {
		return fmt.Errorf("qdrantkit: delete collection %s: %w", name, err)
	}
	defer drainClose(resp)
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= 300 {
		return respErr("delete collection "+name, resp)
	}
	return nil
}

// GetCollection returns info about a collection. Returns nil, nil if not found.
func (c *Client) GetCollection(ctx context.Context, name string) (*CollectionInfo, error) {
	resp, err := c.doReq(ctx, http.MethodGet, collPath(name), nil)
	if err != nil {
		return nil, fmt.Errorf("qdrantkit: get collection %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		io.Copy(io.Discard, resp.Body)
		return nil, nil
	}
	if resp.StatusCode >= 300 {
		return nil, respErr("get collection "+name, resp)
	}

	var wrapper struct {
		Result CollectionInfo `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("qdrantkit: decode collection info: %w", err)
	}
	return &wrapper.Result, nil
}

// ListCollections returns the names of all collections.
func (c *Client) ListCollections(ctx context.Context) ([]string, error) {
	resp, err := c.doReq(ctx, http.MethodGet, "/collections", nil)
	if err != nil {
		return nil, fmt.Errorf("qdrantkit: list collections: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, respErr("list collections", resp)
	}

	var wrapper struct {
		Result struct {
			Collections []struct {
				Name string `json:"name"`
			} `json:"collections"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("qdrantkit: decode collections: %w", err)
	}
	names := make([]string, len(wrapper.Result.Collections))
	for i, c := range wrapper.Result.Collections {
		names[i] = c.Name
	}
	return names, nil
}

// --------------------------------------------------------------------------
// Vectors
// --------------------------------------------------------------------------

// Upsert inserts or updates points in a collection.
// Uses wait=true so the call blocks until Qdrant confirms persistence.
func (c *Client) Upsert(ctx context.Context, collection string, points []Point) error {
	type wire struct {
		ID      string         `json:"id"`
		Vector  []float32      `json:"vector"`
		Payload map[string]any `json:"payload,omitempty"`
	}
	pts := make([]wire, len(points))
	for i, p := range points {
		pts[i] = wire{ID: p.ID, Vector: p.Vector, Payload: p.Payload}
	}
	body := map[string]any{"points": pts}
	resp, err := c.doJSON(ctx, http.MethodPut, collPath(collection)+"/points?wait=true", body)
	if err != nil {
		return fmt.Errorf("qdrantkit: upsert: %w", err)
	}
	defer drainClose(resp)
	if resp.StatusCode >= 300 {
		return respErr("upsert", resp)
	}
	return nil
}

// Delete removes points by ID from a collection.
func (c *Client) Delete(ctx context.Context, collection string, ids []string) error {
	body := map[string]any{"points": ids}
	resp, err := c.doJSON(ctx, http.MethodPost, collPath(collection)+"/points/delete?wait=true", body)
	if err != nil {
		return fmt.Errorf("qdrantkit: delete points: %w", err)
	}
	defer drainClose(resp)
	if resp.StatusCode >= 300 {
		return respErr("delete points", resp)
	}
	return nil
}

// Search performs a nearest-neighbor vector search.
func (c *Client) Search(ctx context.Context, collection string, vector []float32, opts SearchOptions) ([]ScoredPoint, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	body := map[string]any{
		"vector":       vector,
		"limit":        limit,
		"with_payload": opts.WithPayload,
	}
	if opts.WithVector {
		body["with_vector"] = true
	}
	if opts.Filter != nil {
		body["filter"] = opts.Filter
	}
	if opts.ScoreThreshold != nil {
		body["score_threshold"] = *opts.ScoreThreshold
	}

	resp, err := c.doJSON(ctx, http.MethodPost, collPath(collection)+"/points/search", body)
	if err != nil {
		return nil, fmt.Errorf("qdrantkit: search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, respErr("search "+collection, resp)
	}

	var wrapper struct {
		Result []struct {
			ID      any            `json:"id"`
			Score   float32        `json:"score"`
			Payload map[string]any `json:"payload"`
			Vector  []float64      `json:"vector"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("qdrantkit: decode search: %w", err)
	}

	results := make([]ScoredPoint, len(wrapper.Result))
	for i, r := range wrapper.Result {
		results[i] = ScoredPoint{
			ID:      normalizeID(r.ID),
			Score:   r.Score,
			Payload: r.Payload,
			Vector:  toFloat32(r.Vector),
		}
	}
	return results, nil
}

// GetVectors fetches vectors by point IDs. Returns a map from ID to vector.
func (c *Client) GetVectors(ctx context.Context, collection string, ids []string) (map[string][]float32, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	body := map[string]any{
		"ids":          ids,
		"with_vector":  true,
		"with_payload": false,
	}
	resp, err := c.doJSON(ctx, http.MethodPost, collPath(collection)+"/points", body)
	if err != nil {
		return nil, fmt.Errorf("qdrantkit: get vectors: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, respErr("get vectors "+collection, resp)
	}

	var wrapper struct {
		Result []struct {
			ID     any       `json:"id"`
			Vector []float64 `json:"vector"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("qdrantkit: decode get vectors: %w", err)
	}

	out := make(map[string][]float32, len(wrapper.Result))
	for _, p := range wrapper.Result {
		if len(p.Vector) > 0 {
			out[normalizeID(p.ID)] = toFloat32(p.Vector)
		}
	}
	return out, nil
}

// Scroll performs a paginated scan of points in a collection.
func (c *Client) Scroll(ctx context.Context, collection string, opts ScrollOptions) (*ScrollResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	body := map[string]any{
		"limit":        limit,
		"with_payload": opts.WithPayload,
	}
	if opts.WithVector {
		body["with_vector"] = true
	}
	if opts.Filter != nil {
		body["filter"] = opts.Filter
	}
	if opts.Offset != nil {
		body["offset"] = *opts.Offset
	}

	resp, err := c.doJSON(ctx, http.MethodPost, collPath(collection)+"/points/scroll", body)
	if err != nil {
		return nil, fmt.Errorf("qdrantkit: scroll: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, respErr("scroll "+collection, resp)
	}

	var wrapper struct {
		Result struct {
			Points []struct {
				ID      any            `json:"id"`
				Payload map[string]any `json:"payload"`
				Vector  []float64      `json:"vector"`
			} `json:"points"`
			NextPageOffset any `json:"next_page_offset"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("qdrantkit: decode scroll: %w", err)
	}

	result := &ScrollResult{
		Points: make([]ScoredPoint, len(wrapper.Result.Points)),
	}
	for i, p := range wrapper.Result.Points {
		result.Points[i] = ScoredPoint{
			ID:      normalizeID(p.ID),
			Payload: p.Payload,
			Vector:  toFloat32(p.Vector),
		}
	}
	if wrapper.Result.NextPageOffset != nil {
		s := normalizeID(wrapper.Result.NextPageOffset)
		if s != "" {
			result.NextPageOffset = &s
		}
	}
	return result, nil
}

// --------------------------------------------------------------------------
// Index management
// --------------------------------------------------------------------------

// CreatePayloadIndex creates a field index for filtered search performance.
// Returns nil if the index already exists.
func (c *Client) CreatePayloadIndex(ctx context.Context, collection, field, fieldType string) error {
	body := map[string]any{
		"field_name":   field,
		"field_schema": fieldType,
	}
	resp, err := c.doJSON(ctx, http.MethodPut, collPath(collection)+"/index?wait=true", body)
	if err != nil {
		return fmt.Errorf("qdrantkit: create index %s.%s: %w", collection, field, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
		if resp.StatusCode == http.StatusBadRequest && strings.Contains(string(detail), "already exists") {
			return nil
		}
		d := strings.TrimSpace(string(detail))
		if d != "" {
			return fmt.Errorf("qdrantkit: create index %s.%s: status %d: %s", collection, field, resp.StatusCode, d)
		}
		return fmt.Errorf("qdrantkit: create index %s.%s: status %d", collection, field, resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

// --------------------------------------------------------------------------
// Internal helpers
// --------------------------------------------------------------------------

// collPath returns the URL path for a collection, escaping the name.
func collPath(name string) string {
	return "/collections/" + url.PathEscape(name)
}

// setHeaders adds api-key and X-Trace-ID headers.
func (c *Client) setHeaders(ctx context.Context, req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("api-key", c.apiKey)
	}
	if tid := tracekit.TraceID(ctx); tid != "" {
		req.Header.Set("X-Trace-ID", tid)
	}
}

// doReq builds and executes an HTTP request without a JSON body.
func (c *Client) doReq(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	c.setHeaders(ctx, req)
	return c.http.Do(req)
}

// doJSON marshals body to JSON, builds and executes the request.
func (c *Client) doJSON(ctx context.Context, method, path string, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setHeaders(ctx, req)
	return c.http.Do(req)
}

// maxErrBody is the maximum bytes read from an error response body.
const maxErrBody = 4096

// respErr reads the response body and returns a formatted error.
func respErr(op string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
	detail := strings.TrimSpace(string(body))
	if detail != "" {
		return fmt.Errorf("qdrantkit: %s: status %d: %s", op, resp.StatusCode, detail)
	}
	return fmt.Errorf("qdrantkit: %s: status %d", op, resp.StatusCode)
}

// drainClose drains and closes the response body.
func drainClose(resp *http.Response) {
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// normalizeID converts a Qdrant point ID (string or float64) to a string.
func normalizeID(v any) string {
	switch id := v.(type) {
	case string:
		return id
	case float64:
		return fmt.Sprintf("%d", int64(id))
	default:
		return fmt.Sprintf("%v", v)
	}
}

// toFloat32 converts a []float64 (from JSON) to []float32.
func toFloat32(v []float64) []float32 {
	if len(v) == 0 {
		return nil
	}
	out := make([]float32, len(v))
	for i, f := range v {
		out[i] = float32(f)
	}
	return out
}
