package qdrantkit

import (
	"context"
	"encoding/json"
	"io"
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
// Ping
// --------------------------------------------------------------------------

func TestPing_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/collections" {
			t.Errorf("expected /collections, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":{"collections":[]}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheck_ReturnsHealthCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":{"collections":[]}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	check := c.Check()
	if err := check(context.Background()); err != nil {
		t.Fatalf("health check failed: %v", err)
	}
}

func TestPing_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("expected error for 503, got nil")
	}
}

// --------------------------------------------------------------------------
// API key and trace headers
// --------------------------------------------------------------------------

func TestHeaders_APIKeyAndTrace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("api-key"); got != "test-key-123" {
			t.Errorf("expected api-key=test-key-123, got %q", got)
		}
		if got := r.Header.Get("X-Trace-ID"); got != "tr_abc" {
			t.Errorf("expected X-Trace-ID=tr_abc, got %q", got)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":{"collections":[]}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, APIKey: "test-key-123"})
	ctx := tracekit.WithTraceID(context.Background(), "tr_abc")
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --------------------------------------------------------------------------
// CreateCollection
// --------------------------------------------------------------------------

func TestCreateCollection_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/collections/my_vectors" {
			t.Errorf("expected /collections/my_vectors, got %s", r.URL.Path)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		vectors := body["vectors"].(map[string]any)
		if vectors["size"] != float64(384) {
			t.Errorf("expected size=384, got %v", vectors["size"])
		}
		if vectors["distance"] != "Cosine" {
			t.Errorf("expected distance=Cosine, got %v", vectors["distance"])
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":true,"status":"ok"}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	err := c.CreateCollection(context.Background(), "my_vectors", CollectionConfig{
		Dimension: 384,
		Distance:  Cosine,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateCollection_AlreadyExists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"status":{"error":"collection already exists"}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	err := c.CreateCollection(context.Background(), "exists", CollectionConfig{Dimension: 128})
	if err != nil {
		t.Fatalf("expected nil for 409, got: %v", err)
	}
}

func TestCreateCollection_DefaultDistance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		vectors := body["vectors"].(map[string]any)
		if vectors["distance"] != "Cosine" {
			t.Errorf("expected default distance=Cosine, got %v", vectors["distance"])
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":true}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	err := c.CreateCollection(context.Background(), "test", CollectionConfig{Dimension: 128})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --------------------------------------------------------------------------
// DeleteCollection
// --------------------------------------------------------------------------

func TestDeleteCollection_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":true}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	if err := c.DeleteCollection(context.Background(), "old"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteCollection_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	if err := c.DeleteCollection(context.Background(), "gone"); err != nil {
		t.Fatalf("expected nil for 404, got: %v", err)
	}
}

// --------------------------------------------------------------------------
// GetCollection
// --------------------------------------------------------------------------

func TestGetCollection_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":{"status":"green","vectors_count":500,"points_count":500}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	info, err := c.GetCollection(context.Background(), "my_coll")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected info, got nil")
	}
	if info.Status != "green" {
		t.Errorf("expected status=green, got %q", info.Status)
	}
	if info.VectorsCount != 500 {
		t.Errorf("expected vectors_count=500, got %d", info.VectorsCount)
	}
	if info.PointsCount != 500 {
		t.Errorf("expected points_count=500, got %d", info.PointsCount)
	}
}

func TestGetCollection_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"status":{"error":"not found"}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	info, err := c.GetCollection(context.Background(), "missing")
	if err != nil {
		t.Fatalf("expected no error for 404, got: %v", err)
	}
	if info != nil {
		t.Fatalf("expected nil for 404, got: %+v", info)
	}
}

// --------------------------------------------------------------------------
// ListCollections
// --------------------------------------------------------------------------

func TestListCollections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":{"collections":[{"name":"alpha"},{"name":"beta"}]}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	names, err := c.ListCollections(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 collections, got %d", len(names))
	}
	if names[0] != "alpha" || names[1] != "beta" {
		t.Errorf("expected [alpha beta], got %v", names)
	}
}

// --------------------------------------------------------------------------
// Upsert
// --------------------------------------------------------------------------

func TestUpsert_Batch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/collections/vectors/points" {
			t.Errorf("expected /collections/vectors/points, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("wait") != "true" {
			t.Errorf("expected wait=true query param")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type=application/json, got %q", r.Header.Get("Content-Type"))
		}

		var body struct {
			Points []struct {
				ID      string         `json:"id"`
				Vector  []float32      `json:"vector"`
				Payload map[string]any `json:"payload"`
			} `json:"points"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Points) != 2 {
			t.Errorf("expected 2 points, got %d", len(body.Points))
		}
		if body.Points[0].ID != "p1" {
			t.Errorf("expected first point ID=p1, got %q", body.Points[0].ID)
		}
		if len(body.Points[0].Vector) != 3 {
			t.Errorf("expected 3-dim vector, got %d", len(body.Points[0].Vector))
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":{"status":"completed"}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	err := c.Upsert(context.Background(), "vectors", []Point{
		{ID: "p1", Vector: []float32{0.1, 0.2, 0.3}, Payload: map[string]any{"name": "alice"}},
		{ID: "p2", Vector: []float32{0.4, 0.5, 0.6}, Payload: map[string]any{"name": "bob"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --------------------------------------------------------------------------
// Delete
// --------------------------------------------------------------------------

func TestDelete_ByIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/collections/vectors/points/delete" {
			t.Errorf("expected /collections/vectors/points/delete, got %s", r.URL.Path)
		}
		var body struct {
			Points []string `json:"points"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Points) != 2 {
			t.Errorf("expected 2 IDs, got %d", len(body.Points))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":{"status":"completed"}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	if err := c.Delete(context.Background(), "vectors", []string{"p1", "p2"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --------------------------------------------------------------------------
// Search
// --------------------------------------------------------------------------

func TestSearch_Basic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/collections/vectors/points/search" {
			t.Errorf("expected /collections/vectors/points/search, got %s", r.URL.Path)
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["limit"] != float64(5) {
			t.Errorf("expected limit=5, got %v", body["limit"])
		}
		if body["with_payload"] != true {
			t.Errorf("expected with_payload=true, got %v", body["with_payload"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":[
			{"id":"p1","score":0.95,"payload":{"name":"alice"}},
			{"id":"p2","score":0.80,"payload":{"name":"bob"}}
		]}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	results, err := c.Search(context.Background(), "vectors", []float32{0.1, 0.2, 0.3}, SearchOptions{
		Limit:       5,
		WithPayload: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ID != "p1" {
		t.Errorf("expected first ID=p1, got %q", results[0].ID)
	}
	if results[0].Score != 0.95 {
		t.Errorf("expected first score=0.95, got %v", results[0].Score)
	}
	if results[0].Payload["name"] != "alice" {
		t.Errorf("expected first payload name=alice, got %v", results[0].Payload["name"])
	}
}

func TestSearch_WithFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		filter, ok := body["filter"].(map[string]any)
		if !ok {
			t.Fatal("expected filter in request body")
		}
		must, ok := filter["must"].([]any)
		if !ok || len(must) != 1 {
			t.Fatalf("expected 1 must condition, got %v", filter["must"])
		}
		cond := must[0].(map[string]any)
		if cond["key"] != "city" {
			t.Errorf("expected filter key=city, got %v", cond["key"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":[{"id":"p3","score":0.92,"payload":{"city":"London"}}]}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	results, err := c.Search(context.Background(), "vectors", []float32{0.1, 0.2}, SearchOptions{
		Limit:       10,
		WithPayload: true,
		Filter:      Must(Match("city", "London")),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Payload["city"] != "London" {
		t.Errorf("expected city=London, got %v", results[0].Payload["city"])
	}
}

func TestSearch_WithVectorAndThreshold(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["with_vector"] != true {
			t.Errorf("expected with_vector=true, got %v", body["with_vector"])
		}
		if body["score_threshold"] == nil {
			t.Error("expected score_threshold in body")
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":[{"id":"p1","score":0.99,"vector":[0.1,0.2,0.3]}]}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	threshold := float32(0.8)
	results, err := c.Search(context.Background(), "vectors", []float32{0.1, 0.2, 0.3}, SearchOptions{
		Limit:          5,
		WithVector:     true,
		ScoreThreshold: &threshold,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(results[0].Vector) != 3 {
		t.Errorf("expected 3-dim vector, got %d", len(results[0].Vector))
	}
}

func TestSearch_DefaultLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["limit"] != float64(10) {
			t.Errorf("expected default limit=10, got %v", body["limit"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":[]}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	_, err := c.Search(context.Background(), "vectors", []float32{0.1}, SearchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSearch_IntegerID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":[{"id":42,"score":0.88,"payload":{}}]}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	results, err := c.Search(context.Background(), "vectors", []float32{0.1}, SearchOptions{Limit: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results[0].ID != "42" {
		t.Errorf("expected ID=42, got %q", results[0].ID)
	}
}

// --------------------------------------------------------------------------
// GetVectors
// --------------------------------------------------------------------------

func TestGetVectors_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/collections/vectors/points" {
			t.Errorf("expected /collections/vectors/points, got %s", r.URL.Path)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["with_vector"] != true {
			t.Errorf("expected with_vector=true")
		}
		if body["with_payload"] != false {
			t.Errorf("expected with_payload=false")
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":[
			{"id":"p1","vector":[0.1,0.2,0.3]},
			{"id":"p2","vector":[0.4,0.5,0.6]}
		]}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	vecs, err := c.GetVectors(context.Background(), "vectors", []string{"p1", "p2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(vecs))
	}
	if len(vecs["p1"]) != 3 {
		t.Errorf("expected 3-dim vector for p1, got %d", len(vecs["p1"]))
	}
	// Verify float64→float32 conversion
	if vecs["p1"][0] != float32(0.1) {
		t.Errorf("expected p1[0]=0.1, got %v", vecs["p1"][0])
	}
}

func TestGetVectors_EmptyIDs(t *testing.T) {
	c := New(Config{BaseURL: "http://unused"})
	vecs, err := c.GetVectors(context.Background(), "vectors", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vecs != nil {
		t.Fatalf("expected nil for empty IDs, got: %v", vecs)
	}
}

// --------------------------------------------------------------------------
// Scroll
// --------------------------------------------------------------------------

func TestScroll_Basic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/collections/vectors/points/scroll" {
			t.Errorf("expected /collections/vectors/points/scroll, got %s", r.URL.Path)
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["limit"] != float64(2) {
			t.Errorf("expected limit=2, got %v", body["limit"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":{
			"points":[
				{"id":"p1","payload":{"name":"alice"}},
				{"id":"p2","payload":{"name":"bob"}}
			],
			"next_page_offset":"p2"
		}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	result, err := c.Scroll(context.Background(), "vectors", ScrollOptions{
		Limit:       2,
		WithPayload: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Points) != 2 {
		t.Fatalf("expected 2 points, got %d", len(result.Points))
	}
	if result.Points[0].ID != "p1" {
		t.Errorf("expected first ID=p1, got %q", result.Points[0].ID)
	}
	if result.NextPageOffset == nil {
		t.Fatal("expected next_page_offset, got nil")
	}
	if *result.NextPageOffset != "p2" {
		t.Errorf("expected next_page_offset=p2, got %q", *result.NextPageOffset)
	}
}

func TestScroll_EndOfData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":{"points":[{"id":"p3","payload":{}}],"next_page_offset":null}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	result, err := c.Scroll(context.Background(), "vectors", ScrollOptions{Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(result.Points))
	}
	if result.NextPageOffset != nil {
		t.Errorf("expected nil next_page_offset, got %q", *result.NextPageOffset)
	}
}

func TestScroll_WithFilterAndVector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["with_vector"] != true {
			t.Errorf("expected with_vector=true, got %v", body["with_vector"])
		}
		if body["filter"] == nil {
			t.Error("expected filter in request body")
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":{"points":[{"id":"p1","payload":{"status":"active"},"vector":[0.1,0.2]}],"next_page_offset":null}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	result, err := c.Scroll(context.Background(), "vectors", ScrollOptions{
		Limit:       10,
		WithPayload: true,
		WithVector:  true,
		Filter:      Must(Match("status", "active")),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(result.Points))
	}
	if len(result.Points[0].Vector) != 2 {
		t.Errorf("expected 2-dim vector, got %d", len(result.Points[0].Vector))
	}
}

func TestScroll_WithOffset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["offset"] != "cursor-abc" {
			t.Errorf("expected offset=cursor-abc, got %v", body["offset"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":{"points":[],"next_page_offset":null}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	offset := "cursor-abc"
	_, err := c.Scroll(context.Background(), "vectors", ScrollOptions{Limit: 10, Offset: &offset})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --------------------------------------------------------------------------
// CreatePayloadIndex
// --------------------------------------------------------------------------

func TestCreatePayloadIndex_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/collections/vectors/index" {
			t.Errorf("expected /collections/vectors/index, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("wait") != "true" {
			t.Errorf("expected wait=true query param")
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["field_name"] != "tenant_id" {
			t.Errorf("expected field_name=tenant_id, got %v", body["field_name"])
		}
		if body["field_schema"] != "keyword" {
			t.Errorf("expected field_schema=keyword, got %v", body["field_schema"])
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":{"status":"completed"}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	err := c.CreatePayloadIndex(context.Background(), "vectors", "tenant_id", "keyword")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreatePayloadIndex_AlreadyExists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"status":{"error":"field index already exists"}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	err := c.CreatePayloadIndex(context.Background(), "vectors", "tenant_id", "keyword")
	if err != nil {
		t.Fatalf("expected nil for 400 (already exists), got: %v", err)
	}
}

func TestCreatePayloadIndex_BadFieldType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"status":{"error":"invalid field schema"}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	err := c.CreatePayloadIndex(context.Background(), "vectors", "bad_field", "not_a_real_type")
	if err == nil {
		t.Fatal("expected error for invalid field type, got nil")
	}
}

// --------------------------------------------------------------------------
// WithTimeout option
// --------------------------------------------------------------------------

func TestWithTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL}, WithTimeout(50*time.Millisecond))
	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

// --------------------------------------------------------------------------
// Filter builder JSON
// --------------------------------------------------------------------------

func TestFilter_Match_JSON(t *testing.T) {
	c := Match("city", "London")
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	json.Unmarshal(data, &got)
	if got["key"] != "city" {
		t.Errorf("expected key=city, got %v", got["key"])
	}
	m := got["match"].(map[string]any)
	if m["value"] != "London" {
		t.Errorf("expected match.value=London, got %v", m["value"])
	}
}

func TestFilter_Range_JSON(t *testing.T) {
	c := Range("price", Gt(10), Lt(100))
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	json.Unmarshal(data, &got)
	if got["key"] != "price" {
		t.Errorf("expected key=price, got %v", got["key"])
	}
	r := got["range"].(map[string]any)
	if r["gt"] != float64(10) {
		t.Errorf("expected range.gt=10, got %v", r["gt"])
	}
	if r["lt"] != float64(100) {
		t.Errorf("expected range.lt=100, got %v", r["lt"])
	}
}

func TestFilter_Range_GteLte_JSON(t *testing.T) {
	c := Range("score", Gte(0.5), Lte(1.0))
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	json.Unmarshal(data, &got)
	r := got["range"].(map[string]any)
	if r["gte"] != float64(0.5) {
		t.Errorf("expected range.gte=0.5, got %v", r["gte"])
	}
	if r["lte"] != float64(1.0) {
		t.Errorf("expected range.lte=1.0, got %v", r["lte"])
	}
}

func TestFilter_HasID_JSON(t *testing.T) {
	c := HasID("id1", "id2", "id3")
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	json.Unmarshal(data, &got)
	ids := got["has_id"].([]any)
	if len(ids) != 3 {
		t.Fatalf("expected 3 IDs, got %d", len(ids))
	}
	if ids[0] != "id1" {
		t.Errorf("expected first ID=id1, got %v", ids[0])
	}
}

func TestFilter_Must_JSON(t *testing.T) {
	f := Must(Match("city", "London"), Range("price", Gt(10)))
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	json.Unmarshal(data, &got)
	must := got["must"].([]any)
	if len(must) != 2 {
		t.Fatalf("expected 2 must conditions, got %d", len(must))
	}
}

func TestFilter_Should_JSON(t *testing.T) {
	f := Should(Match("color", "red"), Match("color", "blue"))
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	json.Unmarshal(data, &got)
	should := got["should"].([]any)
	if len(should) != 2 {
		t.Fatalf("expected 2 should conditions, got %d", len(should))
	}
}

func TestFilter_MustNot_JSON(t *testing.T) {
	f := MustNot(Match("status", "deleted"))
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	json.Unmarshal(data, &got)
	mustNot := got["must_not"].([]any)
	if len(mustNot) != 1 {
		t.Fatalf("expected 1 must_not condition, got %d", len(mustNot))
	}
}

func TestFilter_Combined(t *testing.T) {
	f := &Filter{
		Must:    []Condition{Match("city", "London")},
		MustNot: []Condition{Match("status", "archived")},
	}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	json.Unmarshal(data, &got)
	if got["must"] == nil {
		t.Error("expected must in output")
	}
	if got["must_not"] == nil {
		t.Error("expected must_not in output")
	}
	if got["should"] != nil {
		t.Error("expected should to be omitted")
	}
}

// --------------------------------------------------------------------------
// Error responses
// --------------------------------------------------------------------------

func TestSearch_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`internal error`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	_, err := c.Search(context.Background(), "vectors", []float32{0.1}, SearchOptions{Limit: 1})
	if err == nil {
		t.Fatal("expected error for 500, got nil")
	}
	if got := err.Error(); got != "qdrantkit: search vectors: status 500: internal error" {
		t.Errorf("unexpected error message: %q", got)
	}
}

func TestUpsert_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain request body to prevent broken pipe
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`bad vector dimension`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	err := c.Upsert(context.Background(), "vectors", []Point{
		{ID: "p1", Vector: []float32{0.1}},
	})
	if err == nil {
		t.Fatal("expected error for 400, got nil")
	}
}

// --------------------------------------------------------------------------
// Context cancellation
// --------------------------------------------------------------------------

func TestSearch_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := c.Search(ctx, "vectors", []float32{0.1}, SearchOptions{Limit: 1})
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

// --------------------------------------------------------------------------
// Collection name escaping
// --------------------------------------------------------------------------

func TestCollectionNameEscaping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// r.URL.Path is decoded by Go's HTTP server; check RequestURI for raw encoding
		if r.RequestURI != "/collections/my%20collection" {
			t.Errorf("expected escaped URI /collections/my%%20collection, got %s", r.RequestURI)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	// Should not error — 404 returns nil,nil for GetCollection
	info, err := c.GetCollection(context.Background(), "my collection")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Fatal("expected nil for 404")
	}
}

// --------------------------------------------------------------------------
// Search empty results
// --------------------------------------------------------------------------

func TestSearch_EmptyResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":[]}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	results, err := c.Search(context.Background(), "vectors", []float32{0.1}, SearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// --------------------------------------------------------------------------
// GetVectors partial results
// --------------------------------------------------------------------------

func TestGetVectors_PartialResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only p1 exists; p2 and p3 are missing from Qdrant
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":[{"id":"p1","vector":[0.1,0.2]}]}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	vecs, err := c.GetVectors(context.Background(), "vectors", []string{"p1", "p2", "p3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vecs) != 1 {
		t.Fatalf("expected 1 vector (only p1 found), got %d", len(vecs))
	}
	if _, ok := vecs["p1"]; !ok {
		t.Error("expected p1 in results")
	}
	if _, ok := vecs["p2"]; ok {
		t.Error("p2 should not be in results")
	}
}

func TestGetVectors_PointWithEmptyVector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// p2 exists but has an empty vector (edge case)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":[{"id":"p1","vector":[0.5]},{"id":"p2","vector":[]}]}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	vecs, err := c.GetVectors(context.Background(), "vectors", []string{"p1", "p2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vecs) != 1 {
		t.Fatalf("expected 1 vector (p2 empty skipped), got %d", len(vecs))
	}
	if _, ok := vecs["p1"]; !ok {
		t.Error("expected p1 in results")
	}
}

// --------------------------------------------------------------------------
// Match with non-string values
// --------------------------------------------------------------------------

func TestFilter_MatchInteger_JSON(t *testing.T) {
	c := Match("count", 42)
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	json.Unmarshal(data, &got)
	m := got["match"].(map[string]any)
	if m["value"] != float64(42) {
		t.Errorf("expected match.value=42, got %v", m["value"])
	}
}

func TestFilter_MatchBool_JSON(t *testing.T) {
	c := Match("active", true)
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	json.Unmarshal(data, &got)
	m := got["match"].(map[string]any)
	if m["value"] != true {
		t.Errorf("expected match.value=true, got %v", m["value"])
	}
}

// --------------------------------------------------------------------------
// Upsert with nil payload
// --------------------------------------------------------------------------

func TestUpsert_NilPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Points []struct {
				ID      string `json:"id"`
				Payload any    `json:"payload"`
			} `json:"points"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Points) != 1 {
			t.Fatalf("expected 1 point, got %d", len(body.Points))
		}
		// With omitempty, nil payload should be absent (decoded as nil)
		if body.Points[0].Payload != nil {
			t.Errorf("expected nil payload, got %v", body.Points[0].Payload)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":{"status":"completed"}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	err := c.Upsert(context.Background(), "vectors", []Point{
		{ID: "p1", Vector: []float32{0.1, 0.2}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --------------------------------------------------------------------------
// ListCollections empty
// --------------------------------------------------------------------------

func TestListCollections_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":{"collections":[]}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	names, err := c.ListCollections(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if names == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(names) != 0 {
		t.Errorf("expected 0 collections, got %d", len(names))
	}
}

// --------------------------------------------------------------------------
// Search with null payload
// --------------------------------------------------------------------------

func TestSearch_NullPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":[{"id":"p1","score":0.9,"payload":null}]}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	results, err := c.Search(context.Background(), "vectors", []float32{0.1}, SearchOptions{Limit: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Payload != nil {
		t.Errorf("expected nil payload, got %v", results[0].Payload)
	}
	if results[0].Score != 0.9 {
		t.Errorf("expected score=0.9, got %v", results[0].Score)
	}
}

// --------------------------------------------------------------------------
// Scroll default limit
// --------------------------------------------------------------------------

func TestScroll_DefaultLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["limit"] != float64(10) {
			t.Errorf("expected default limit=10, got %v", body["limit"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":{"points":[],"next_page_offset":null}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	_, err := c.Scroll(context.Background(), "vectors", ScrollOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
