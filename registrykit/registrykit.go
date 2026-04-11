// Package registrykit provides an HTTP client for registry_svc,
// the entity registry service. It supports entity resolution, relationship
// traversal, graph queries, and entity management operations.
package registrykit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/call"
	"github.com/ai8future/chassis-go/v11/tracekit"
)

// --------------------------------------------------------------------------
// Types
// --------------------------------------------------------------------------

// Entity represents a resolved entity from the registry.
type Entity struct {
	ID            string            `json:"entity_id"`
	Types         []string          `json:"entity_types"`
	CanonicalName string            `json:"canonical_name"`
	Metadata      map[string]any    `json:"metadata"`
	Identifiers   map[string]string `json:"identifiers"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

// Relationship represents a directed edge between two entities.
type Relationship struct {
	FromEntity   string         `json:"from_entity"`
	ToEntity     string         `json:"to_entity"`
	Relationship string         `json:"relationship"`
	TenantID     string         `json:"tenant_id"`
	Since        *time.Time     `json:"since"`
	Until        *time.Time     `json:"until"`
	Metadata     map[string]any `json:"metadata"`
}

// GraphNode represents a node in an entity relationship graph.
type GraphNode struct {
	Entity        Entity         `json:"entity"`
	Relationships []Relationship `json:"relationships"`
	Children      []GraphNode    `json:"children"`
}

// CreateEntityRequest is the payload for creating a new entity.
type CreateEntityRequest struct {
	EntityTypes   []string          `json:"entity_types"`
	CanonicalName string            `json:"canonical_name"`
	Metadata      map[string]any    `json:"metadata,omitempty"`
	Identifiers   map[string]string `json:"identifiers,omitempty"`
}

// CreateRelationshipRequest is the payload for creating a relationship.
type CreateRelationshipRequest struct {
	FromEntity   string         `json:"from_entity"`
	ToEntity     string         `json:"to_entity"`
	Relationship string         `json:"relationship"`
	Since        *time.Time     `json:"since,omitempty"`
	Until        *time.Time     `json:"until,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

// --------------------------------------------------------------------------
// Client
// --------------------------------------------------------------------------

// Client is an HTTP client for registry_svc backed by call.Client.
type Client struct {
	baseURL  string
	tenantID string
	http     *call.Client
}

// clientOptions collects configuration before building the call.Client.
type clientOptions struct {
	tenantID    string
	callOptions []call.Option
}

// ClientOption configures a Client.
type ClientOption func(*clientOptions)

// WithTenant sets the tenant ID for all requests.
func WithTenant(id string) ClientOption {
	return func(o *clientOptions) { o.tenantID = id }
}

// WithTimeout sets the per-request timeout.
func WithTimeout(d time.Duration) ClientOption {
	return func(o *clientOptions) { o.callOptions = append(o.callOptions, call.WithTimeout(d)) }
}

// WithRetry enables automatic retries for transient (5xx) errors.
func WithRetry(maxAttempts int, baseDelay time.Duration) ClientOption {
	return func(o *clientOptions) { o.callOptions = append(o.callOptions, call.WithRetry(maxAttempts, baseDelay)) }
}

// WithCircuitBreaker protects the client with a named circuit breaker.
func WithCircuitBreaker(name string, threshold int, cooldown time.Duration) ClientOption {
	return func(o *clientOptions) {
		o.callOptions = append(o.callOptions, call.WithCircuitBreaker(name, threshold, cooldown))
	}
}

// NewClient creates a new registry_svc client.
func NewClient(baseURL string, opts ...ClientOption) *Client {
	chassis.AssertVersionChecked()
	o := &clientOptions{}
	for _, opt := range opts {
		opt(o)
	}

	callOpts := []call.Option{call.WithTimeout(5 * time.Second)}
	callOpts = append(callOpts, o.callOptions...)

	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		tenantID: o.tenantID,
		http:     call.New(callOpts...),
	}
}

// --------------------------------------------------------------------------
// Resolve
// --------------------------------------------------------------------------

// ResolveOption configures a Resolve call.
type ResolveOption func(url.Values)

// ByCRD resolves by CRD identifier.
func ByCRD(crd string) ResolveOption {
	return func(v url.Values) { v.Set("crd", crd) }
}

// ByDomain resolves by domain.
func ByDomain(domain string) ResolveOption {
	return func(v url.Values) { v.Set("domain", domain) }
}

// ByEmail resolves by email address.
func ByEmail(email string) ResolveOption {
	return func(v url.Values) { v.Set("email", email) }
}

// BySlug resolves by slug.
func BySlug(slug string) ResolveOption {
	return func(v url.Values) { v.Set("slug", slug) }
}

// ByIdentifier resolves by a namespaced identifier.
func ByIdentifier(ns, val string) ResolveOption {
	return func(v url.Values) {
		v.Set("identifier_ns", ns)
		v.Set("identifier_val", val)
	}
}

// Resolve looks up an entity by type and one or more identifying attributes.
// Returns nil, nil when the entity is not found (HTTP 404).
func (c *Client) Resolve(ctx context.Context, entityType string, opts ...ResolveOption) (*Entity, error) {
	params := url.Values{}
	params.Set("entity_type", entityType)
	for _, o := range opts {
		o(params)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/resolve?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("registrykit: build request: %w", err)
	}
	c.setHeaders(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registrykit: resolve: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if err := checkStatus(resp); err != nil {
		return nil, err
	}

	var entity Entity
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&entity); err != nil {
		return nil, fmt.Errorf("registrykit: decode entity: %w", err)
	}
	return &entity, nil
}

// --------------------------------------------------------------------------
// Related
// --------------------------------------------------------------------------

// RelatedOption configures a Related call.
type RelatedOption func(url.Values)

// OfType filters related entities by type.
func OfType(entityType string) RelatedOption {
	return func(v url.Values) { v.Set("of_type", entityType) }
}

// Rel filters by relationship name.
func Rel(relationship string) RelatedOption {
	return func(v url.Values) { v.Set("rel", relationship) }
}

// AsOf filters relationships as of a specific point in time.
func AsOf(t time.Time) RelatedOption {
	return func(v url.Values) { v.Set("as_of", t.Format(time.RFC3339)) }
}

// Related returns relationships for an entity.
func (c *Client) Related(ctx context.Context, entityID string, opts ...RelatedOption) ([]Relationship, error) {
	params := url.Values{}
	for _, o := range opts {
		o(params)
	}

	u := c.baseURL + "/v1/entities/" + url.PathEscape(entityID) + "/related"
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("registrykit: build request: %w", err)
	}
	c.setHeaders(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registrykit: related: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if err := checkStatus(resp); err != nil {
		return nil, err
	}

	var rels []Relationship
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&rels); err != nil {
		return nil, fmt.Errorf("registrykit: decode relationships: %w", err)
	}
	return rels, nil
}

// --------------------------------------------------------------------------
// Descendants / Ancestors
// --------------------------------------------------------------------------

// Descendants returns all descendant entities of the given entity.
func (c *Client) Descendants(ctx context.Context, entityID string, opts ...RelatedOption) ([]Entity, error) {
	params := url.Values{}
	for _, o := range opts {
		o(params)
	}

	u := c.baseURL + "/v1/entities/" + url.PathEscape(entityID) + "/descendants"
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("registrykit: build request: %w", err)
	}
	c.setHeaders(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registrykit: descendants: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if err := checkStatus(resp); err != nil {
		return nil, err
	}

	var entities []Entity
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&entities); err != nil {
		return nil, fmt.Errorf("registrykit: decode entities: %w", err)
	}
	return entities, nil
}

// Ancestors returns all ancestor entities of the given entity.
func (c *Client) Ancestors(ctx context.Context, entityID string) ([]Entity, error) {
	u := c.baseURL + "/v1/entities/" + url.PathEscape(entityID) + "/ancestors"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("registrykit: build request: %w", err)
	}
	c.setHeaders(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registrykit: ancestors: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if err := checkStatus(resp); err != nil {
		return nil, err
	}

	var entities []Entity
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&entities); err != nil {
		return nil, fmt.Errorf("registrykit: decode entities: %w", err)
	}
	return entities, nil
}

// --------------------------------------------------------------------------
// Graph
// --------------------------------------------------------------------------

// GraphOption configures a Graph call.
type GraphOption func(url.Values)

// Depth sets the traversal depth for the graph query.
func Depth(n int) GraphOption {
	return func(v url.Values) { v.Set("depth", fmt.Sprintf("%d", n)) }
}

// Graph returns a graph rooted at the given entity.
func (c *Client) Graph(ctx context.Context, entityID string, opts ...GraphOption) (*GraphNode, error) {
	params := url.Values{}
	for _, o := range opts {
		o(params)
	}

	u := c.baseURL + "/v1/entities/" + url.PathEscape(entityID) + "/graph"
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("registrykit: build request: %w", err)
	}
	c.setHeaders(ctx, req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registrykit: graph: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if err := checkStatus(resp); err != nil {
		return nil, err
	}

	var node GraphNode
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&node); err != nil {
		return nil, fmt.Errorf("registrykit: decode graph: %w", err)
	}
	return &node, nil
}

// --------------------------------------------------------------------------
// Mutations
// --------------------------------------------------------------------------

// CreateEntity creates a new entity in the registry.
func (c *Client) CreateEntity(ctx context.Context, req CreateEntityRequest) (*Entity, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("registrykit: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/entities", strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("registrykit: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.setHeaders(ctx, httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("registrykit: create entity: %w", err)
	}
	defer resp.Body.Close()

	if err := checkStatus(resp); err != nil {
		return nil, err
	}

	var entity Entity
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&entity); err != nil {
		return nil, fmt.Errorf("registrykit: decode entity: %w", err)
	}
	return &entity, nil
}

// AddIdentifier adds a namespaced identifier to an existing entity.
func (c *Client) AddIdentifier(ctx context.Context, entityID, namespace, value string) error {
	payload := map[string]string{"namespace": namespace, "value": value}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("registrykit: marshal request: %w", err)
	}

	u := c.baseURL + "/v1/entities/" + url.PathEscape(entityID) + "/identifiers"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("registrykit: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.setHeaders(ctx, httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("registrykit: add identifier: %w", err)
	}
	defer resp.Body.Close()

	return checkStatus(resp)
}

// CreateRelationship creates a relationship between two entities.
func (c *Client) CreateRelationship(ctx context.Context, req CreateRelationshipRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("registrykit: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/relationships", strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("registrykit: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.setHeaders(ctx, httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("registrykit: create relationship: %w", err)
	}
	defer resp.Body.Close()

	return checkStatus(resp)
}

// Merge merges two entities, keeping the winner and retiring the loser.
func (c *Client) Merge(ctx context.Context, winnerID, loserID, reason string) error {
	payload := map[string]string{
		"winner_id": winnerID,
		"loser_id":  loserID,
		"reason":    reason,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("registrykit: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/merge", strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("registrykit: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.setHeaders(ctx, httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("registrykit: merge: %w", err)
	}
	defer resp.Body.Close()

	return checkStatus(resp)
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

// maxResponseBody is the maximum bytes read from a successful response body.
const maxResponseBody = 32 << 20

// maxErrorBody caps how much of an error response we read (64 KB).
const maxErrorBody = 64 << 10

// checkStatus inspects the HTTP response status and returns an appropriate error.
func checkStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	detail := strings.TrimSpace(string(body))

	switch resp.StatusCode {
	case http.StatusForbidden:
		if detail != "" {
			return fmt.Errorf("registrykit: forbidden: %s", detail)
		}
		return fmt.Errorf("registrykit: forbidden")
	case http.StatusConflict:
		if detail != "" {
			return fmt.Errorf("registrykit: conflict: %s", detail)
		}
		return fmt.Errorf("registrykit: conflict")
	case http.StatusServiceUnavailable:
		return fmt.Errorf("registrykit: service unavailable")
	default:
		return fmt.Errorf("registrykit: unexpected status %d: %s", resp.StatusCode, detail)
	}
}
