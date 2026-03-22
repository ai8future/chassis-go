package graphkit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v10"
	"github.com/ai8future/chassis-go/v10/tracekit"
)

func init() {
	chassis.RequireMajor(10)
}

// --------------------------------------------------------------------------
// 1. Search returns results — verify headers
// --------------------------------------------------------------------------

func TestSearch_ReturnsResults(t *testing.T) {
	results := []SearchResult{
		{
			EntityName:    "Acme Corp",
			Relationships: []string{"subsidiary_of", "located_in"},
			Confidence:    0.95,
			Context:       "Technology company founded in 2010",
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers
		if r.Header.Get("X-Tenant-ID") != "tenant-abc" {
			t.Errorf("expected X-Tenant-ID=tenant-abc, got %q", r.Header.Get("X-Tenant-ID"))
		}
		if r.Header.Get("X-Trace-ID") != "tr_test123" {
			t.Errorf("expected X-Trace-ID=tr_test123, got %q", r.Header.Get("X-Trace-ID"))
		}
		// Verify query param
		if r.URL.Query().Get("q") != "Acme" {
			t.Errorf("expected q=Acme, got %q", r.URL.Query().Get("q"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))
	ctx := tracekit.WithTraceID(context.Background(), "tr_test123")

	got, err := client.Search(ctx, "Acme")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if got[0].EntityName != "Acme Corp" {
		t.Errorf("expected EntityName=Acme Corp, got %q", got[0].EntityName)
	}
	if got[0].Confidence != 0.95 {
		t.Errorf("expected Confidence=0.95, got %f", got[0].Confidence)
	}
	if len(got[0].Relationships) != 2 {
		t.Errorf("expected 2 relationships, got %d", len(got[0].Relationships))
	}
}

// --------------------------------------------------------------------------
// 2. Recall with time param — verify headers
// --------------------------------------------------------------------------

func TestRecall_WithTime(t *testing.T) {
	results := []SearchResult{
		{
			EntityName:    "Acme Corp",
			Relationships: []string{"owned_by"},
			Confidence:    0.88,
			Context:       "Historical state",
		},
	}

	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Tenant-ID") != "tenant-abc" {
			t.Errorf("expected X-Tenant-ID=tenant-abc, got %q", r.Header.Get("X-Tenant-ID"))
		}
		if r.Header.Get("X-Trace-ID") != "tr_recall" {
			t.Errorf("expected X-Trace-ID=tr_recall, got %q", r.Header.Get("X-Trace-ID"))
		}
		if r.URL.Query().Get("q") != "Acme" {
			t.Errorf("expected q=Acme, got %q", r.URL.Query().Get("q"))
		}
		if r.URL.Query().Get("at") == "" {
			t.Error("expected at parameter to be set")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))
	ctx := tracekit.WithTraceID(context.Background(), "tr_recall")

	got, err := client.Recall(ctx, "Acme", &ts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if got[0].EntityName != "Acme Corp" {
		t.Errorf("expected EntityName=Acme Corp, got %q", got[0].EntityName)
	}
}

// --------------------------------------------------------------------------
// 3. Recall without time param
// --------------------------------------------------------------------------

func TestRecall_WithoutTime(t *testing.T) {
	results := []SearchResult{
		{EntityName: "Current State", Confidence: 0.9},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("at") != "" {
			t.Error("expected no at parameter")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))

	got, err := client.Recall(context.Background(), "query", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
}

// --------------------------------------------------------------------------
// 4. Cypher with params — verify headers
// --------------------------------------------------------------------------

func TestCypher_WithParams(t *testing.T) {
	result := CypherResult{
		Columns: []string{"name", "count"},
		Rows: [][]any{
			{"Acme Corp", float64(42)},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Tenant-ID") != "tenant-abc" {
			t.Errorf("expected X-Tenant-ID=tenant-abc, got %q", r.Header.Get("X-Tenant-ID"))
		}
		if r.Header.Get("X-Trace-ID") != "tr_cypher" {
			t.Errorf("expected X-Trace-ID=tr_cypher, got %q", r.Header.Get("X-Trace-ID"))
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type=application/json, got %q", r.Header.Get("Content-Type"))
		}

		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		if payload["query"] != "MATCH (n) WHERE n.name = $name RETURN n" {
			t.Errorf("unexpected query: %v", payload["query"])
		}
		params, ok := payload["params"].(map[string]any)
		if !ok || params["name"] != "Acme" {
			t.Errorf("unexpected params: %v", payload["params"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))
	ctx := tracekit.WithTraceID(context.Background(), "tr_cypher")

	got, err := client.Cypher(ctx, "MATCH (n) WHERE n.name = $name RETURN n", map[string]any{"name": "Acme"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Columns) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(got.Columns))
	}
	if len(got.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got.Rows))
	}
}

// --------------------------------------------------------------------------
// 5. EntityGraph with depth — verify headers
// --------------------------------------------------------------------------

func TestEntityGraph_WithDepth(t *testing.T) {
	graphResult := GraphResult{
		Name:          "Acme Corp",
		Relationships: []string{"subsidiary_of"},
		Neighbors: []GraphResult{
			{
				Name:          "Global Inc",
				Relationships: []string{"parent_of"},
				Neighbors:     nil,
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Tenant-ID") != "tenant-abc" {
			t.Errorf("expected X-Tenant-ID=tenant-abc, got %q", r.Header.Get("X-Tenant-ID"))
		}
		if r.Header.Get("X-Trace-ID") != "tr_graph" {
			t.Errorf("expected X-Trace-ID=tr_graph, got %q", r.Header.Get("X-Trace-ID"))
		}
		if r.URL.Query().Get("depth") != "3" {
			t.Errorf("expected depth=3, got %q", r.URL.Query().Get("depth"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(graphResult)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))
	ctx := tracekit.WithTraceID(context.Background(), "tr_graph")

	got, err := client.EntityGraph(ctx, "Acme Corp", Depth(3))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected graph result, got nil")
	}
	if got.Name != "Acme Corp" {
		t.Errorf("expected Name=Acme Corp, got %q", got.Name)
	}
	if len(got.Neighbors) != 1 {
		t.Fatalf("expected 1 neighbor, got %d", len(got.Neighbors))
	}
	if got.Neighbors[0].Name != "Global Inc" {
		t.Errorf("expected neighbor name=Global Inc, got %q", got.Neighbors[0].Name)
	}
}

// --------------------------------------------------------------------------
// 6. EntityGraph not found (404)
// --------------------------------------------------------------------------

func TestEntityGraph_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))

	got, err := client.EntityGraph(context.Background(), "Unknown")
	if err != nil {
		t.Fatalf("expected no error for 404, got: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for 404, got: %+v", got)
	}
}

// --------------------------------------------------------------------------
// 7. EntityTimeline — verify headers
// --------------------------------------------------------------------------

func TestEntityTimeline(t *testing.T) {
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	entries := []TimelineEntry{
		{
			Timestamp:    ts,
			Relationship: "acquired_by",
			Entity:       "Global Inc",
			Action:       "created",
		},
		{
			Timestamp:    ts.Add(24 * time.Hour),
			Relationship: "located_in",
			Entity:       "New York",
			Action:       "updated",
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Tenant-ID") != "tenant-abc" {
			t.Errorf("expected X-Tenant-ID=tenant-abc, got %q", r.Header.Get("X-Tenant-ID"))
		}
		if r.Header.Get("X-Trace-ID") != "tr_timeline" {
			t.Errorf("expected X-Trace-ID=tr_timeline, got %q", r.Header.Get("X-Trace-ID"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))
	ctx := tracekit.WithTraceID(context.Background(), "tr_timeline")

	got, err := client.EntityTimeline(ctx, "Acme Corp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got[0].Action != "created" {
		t.Errorf("expected action=created, got %q", got[0].Action)
	}
	if got[1].Entity != "New York" {
		t.Errorf("expected entity=New York, got %q", got[1].Entity)
	}
}

// --------------------------------------------------------------------------
// 8. Paths with MaxHops — verify headers
// --------------------------------------------------------------------------

func TestPaths_WithMaxHops(t *testing.T) {
	paths := []Path{
		{
			Nodes: []string{"Acme Corp", "Global Inc", "NYC Office"},
			Edges: []string{"subsidiary_of", "located_in"},
			Hops:  2,
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Tenant-ID") != "tenant-abc" {
			t.Errorf("expected X-Tenant-ID=tenant-abc, got %q", r.Header.Get("X-Tenant-ID"))
		}
		if r.Header.Get("X-Trace-ID") != "tr_paths" {
			t.Errorf("expected X-Trace-ID=tr_paths, got %q", r.Header.Get("X-Trace-ID"))
		}
		if r.URL.Query().Get("from") != "Acme Corp" {
			t.Errorf("expected from=Acme Corp, got %q", r.URL.Query().Get("from"))
		}
		if r.URL.Query().Get("to") != "NYC Office" {
			t.Errorf("expected to=NYC Office, got %q", r.URL.Query().Get("to"))
		}
		if r.URL.Query().Get("max_hops") != "5" {
			t.Errorf("expected max_hops=5, got %q", r.URL.Query().Get("max_hops"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(paths)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))
	ctx := tracekit.WithTraceID(context.Background(), "tr_paths")

	got, err := client.Paths(ctx, "Acme Corp", "NYC Office", MaxHops(5))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 path, got %d", len(got))
	}
	if got[0].Hops != 2 {
		t.Errorf("expected Hops=2, got %d", got[0].Hops)
	}
	if len(got[0].Nodes) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(got[0].Nodes))
	}
}

// --------------------------------------------------------------------------
// 9. Service unavailable (503)
// --------------------------------------------------------------------------

func TestSearch_ServiceUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))

	_, err := client.Search(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error for 503, got nil")
	}
	if got := err.Error(); got != "graphkit: service unavailable" {
		t.Errorf("expected 'graphkit: service unavailable', got %q", got)
	}
}

// --------------------------------------------------------------------------
// 10. Network timeout
// --------------------------------------------------------------------------

func TestSearch_NetworkTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"), WithTimeout(50*time.Millisecond))

	_, err := client.Search(context.Background(), "test")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

// --------------------------------------------------------------------------
// 11. Cypher without params
// --------------------------------------------------------------------------

func TestCypher_WithoutParams(t *testing.T) {
	result := CypherResult{
		Columns: []string{"count"},
		Rows:    [][]any{{float64(100)}},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		if _, ok := payload["params"]; ok {
			t.Error("expected no params field when nil")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))

	got, err := client.Cypher(context.Background(), "MATCH (n) RETURN count(n)", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got.Rows))
	}
}

// --------------------------------------------------------------------------
// 12. EntityTimeline not found (404)
// --------------------------------------------------------------------------

func TestEntityTimeline_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))

	got, err := client.EntityTimeline(context.Background(), "Unknown")
	if err != nil {
		t.Fatalf("expected no error for 404, got: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for 404, got: %+v", got)
	}
}
