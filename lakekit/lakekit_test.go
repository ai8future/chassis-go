package lakekit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v9"
	"github.com/ai8future/chassis-go/v9/tracekit"
)

func init() {
	chassis.RequireMajor(9)
}

// --------------------------------------------------------------------------
// 1. Query returns results — verify headers
// --------------------------------------------------------------------------

func TestQuery_ReturnsResults(t *testing.T) {
	result := QueryResult{
		Columns:  []string{"id", "name", "amount"},
		Rows:     [][]any{{"ent_001", "Acme Corp", float64(1000)}},
		RowCount: 1,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers
		if r.Header.Get("X-Tenant-ID") != "tenant-abc" {
			t.Errorf("expected X-Tenant-ID=tenant-abc, got %q", r.Header.Get("X-Tenant-ID"))
		}
		if r.Header.Get("X-Trace-ID") != "tr_query" {
			t.Errorf("expected X-Trace-ID=tr_query, got %q", r.Header.Get("X-Trace-ID"))
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type=application/json, got %q", r.Header.Get("Content-Type"))
		}

		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		if payload["sql"] != "SELECT * FROM events WHERE id = ?" {
			t.Errorf("unexpected sql: %v", payload["sql"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))
	ctx := tracekit.WithTraceID(context.Background(), "tr_query")

	got, err := client.Query(ctx, "SELECT * FROM events WHERE id = ?", "ent_001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.RowCount != 1 {
		t.Errorf("expected RowCount=1, got %d", got.RowCount)
	}
	if len(got.Columns) != 3 {
		t.Errorf("expected 3 columns, got %d", len(got.Columns))
	}
	if len(got.Rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(got.Rows))
	}
}

// --------------------------------------------------------------------------
// 2. Query without params
// --------------------------------------------------------------------------

func TestQuery_WithoutParams(t *testing.T) {
	result := QueryResult{
		Columns:  []string{"count"},
		Rows:     [][]any{{float64(42)}},
		RowCount: 1,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		if _, ok := payload["params"]; ok {
			t.Error("expected no params field when empty")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))

	got, err := client.Query(context.Background(), "SELECT count(*) FROM events")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.RowCount != 1 {
		t.Errorf("expected RowCount=1, got %d", got.RowCount)
	}
}

// --------------------------------------------------------------------------
// 3. EntityHistory — verify headers
// --------------------------------------------------------------------------

func TestEntityHistory(t *testing.T) {
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	entries := []HistoryEntry{
		{
			Timestamp: ts,
			EventType: "created",
			Data:      map[string]any{"source": "import"},
		},
		{
			Timestamp: ts.Add(24 * time.Hour),
			EventType: "updated",
			Data:      map[string]any{"field": "name"},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Tenant-ID") != "tenant-abc" {
			t.Errorf("expected X-Tenant-ID=tenant-abc, got %q", r.Header.Get("X-Tenant-ID"))
		}
		if r.Header.Get("X-Trace-ID") != "tr_history" {
			t.Errorf("expected X-Trace-ID=tr_history, got %q", r.Header.Get("X-Trace-ID"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))
	ctx := tracekit.WithTraceID(context.Background(), "tr_history")

	got, err := client.EntityHistory(ctx, "ent_001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got[0].EventType != "created" {
		t.Errorf("expected EventType=created, got %q", got[0].EventType)
	}
	if got[1].EventType != "updated" {
		t.Errorf("expected EventType=updated, got %q", got[1].EventType)
	}
}

// --------------------------------------------------------------------------
// 4. EntityHistory not found (404)
// --------------------------------------------------------------------------

func TestEntityHistory_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))

	got, err := client.EntityHistory(context.Background(), "unknown")
	if err != nil {
		t.Fatalf("expected no error for 404, got: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for 404, got: %+v", got)
	}
}

// --------------------------------------------------------------------------
// 5. Datasets list — verify headers
// --------------------------------------------------------------------------

func TestDatasets(t *testing.T) {
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	datasets := []Dataset{
		{
			Name:       "events",
			RowCount:   1000000,
			LastUpdate: ts,
			Schema: []Column{
				{Name: "id", Type: "string"},
				{Name: "timestamp", Type: "datetime"},
				{Name: "data", Type: "json"},
			},
		},
		{
			Name:       "entities",
			RowCount:   50000,
			LastUpdate: ts,
			Schema: []Column{
				{Name: "id", Type: "string"},
				{Name: "name", Type: "string"},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Tenant-ID") != "tenant-abc" {
			t.Errorf("expected X-Tenant-ID=tenant-abc, got %q", r.Header.Get("X-Tenant-ID"))
		}
		if r.Header.Get("X-Trace-ID") != "tr_datasets" {
			t.Errorf("expected X-Trace-ID=tr_datasets, got %q", r.Header.Get("X-Trace-ID"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(datasets)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))
	ctx := tracekit.WithTraceID(context.Background(), "tr_datasets")

	got, err := client.Datasets(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 datasets, got %d", len(got))
	}
	if got[0].Name != "events" {
		t.Errorf("expected Name=events, got %q", got[0].Name)
	}
	if got[0].RowCount != 1000000 {
		t.Errorf("expected RowCount=1000000, got %d", got[0].RowCount)
	}
	if len(got[0].Schema) != 3 {
		t.Errorf("expected 3 schema columns, got %d", len(got[0].Schema))
	}
}

// --------------------------------------------------------------------------
// 6. DatasetStats — verify headers
// --------------------------------------------------------------------------

func TestDatasetStats(t *testing.T) {
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	dataset := Dataset{
		Name:       "events",
		RowCount:   1000000,
		LastUpdate: ts,
		Schema: []Column{
			{Name: "id", Type: "string"},
			{Name: "timestamp", Type: "datetime"},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Tenant-ID") != "tenant-abc" {
			t.Errorf("expected X-Tenant-ID=tenant-abc, got %q", r.Header.Get("X-Tenant-ID"))
		}
		if r.Header.Get("X-Trace-ID") != "tr_stats" {
			t.Errorf("expected X-Trace-ID=tr_stats, got %q", r.Header.Get("X-Trace-ID"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(dataset)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))
	ctx := tracekit.WithTraceID(context.Background(), "tr_stats")

	got, err := client.DatasetStats(ctx, "events")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected dataset, got nil")
	}
	if got.Name != "events" {
		t.Errorf("expected Name=events, got %q", got.Name)
	}
	if got.RowCount != 1000000 {
		t.Errorf("expected RowCount=1000000, got %d", got.RowCount)
	}
}

// --------------------------------------------------------------------------
// 7. DatasetStats not found (404)
// --------------------------------------------------------------------------

func TestDatasetStats_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))

	got, err := client.DatasetStats(context.Background(), "unknown")
	if err != nil {
		t.Fatalf("expected no error for 404, got: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for 404, got: %+v", got)
	}
}

// --------------------------------------------------------------------------
// 8. Service unavailable (503)
// --------------------------------------------------------------------------

func TestQuery_ServiceUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))

	_, err := client.Query(context.Background(), "SELECT 1")
	if err == nil {
		t.Fatal("expected error for 503, got nil")
	}
	if got := err.Error(); got != "lakekit: service unavailable" {
		t.Errorf("expected 'lakekit: service unavailable', got %q", got)
	}
}

// --------------------------------------------------------------------------
// 9. Network timeout
// --------------------------------------------------------------------------

func TestQuery_NetworkTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"), WithTimeout(50*time.Millisecond))

	_, err := client.Query(context.Background(), "SELECT 1")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

// --------------------------------------------------------------------------
// 10. Forbidden (403)
// --------------------------------------------------------------------------

func TestQuery_Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("tenant not authorized"))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("bad-tenant"))

	_, err := client.Query(context.Background(), "SELECT 1")
	if err == nil {
		t.Fatal("expected error for 403, got nil")
	}
	if got := err.Error(); got != "lakekit: forbidden: tenant not authorized" {
		t.Errorf("expected 'lakekit: forbidden: tenant not authorized', got %q", got)
	}
}
