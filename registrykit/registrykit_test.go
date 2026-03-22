package registrykit

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
// 1. Resolve found — verify headers
// --------------------------------------------------------------------------

func TestResolve_Found(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	entity := Entity{
		ID:            "ent_001",
		Types:         []string{"organization"},
		CanonicalName: "Acme Corp",
		Metadata:      map[string]any{"industry": "tech"},
		Identifiers:   map[string]string{"crd": "CRD-001"},
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers
		if r.Header.Get("X-Tenant-ID") != "tenant-abc" {
			t.Errorf("expected X-Tenant-ID=tenant-abc, got %q", r.Header.Get("X-Tenant-ID"))
		}
		if r.Header.Get("X-Trace-ID") != "tr_test123" {
			t.Errorf("expected X-Trace-ID=tr_test123, got %q", r.Header.Get("X-Trace-ID"))
		}
		// Verify query params
		if r.URL.Query().Get("entity_type") != "organization" {
			t.Errorf("expected entity_type=organization, got %q", r.URL.Query().Get("entity_type"))
		}
		if r.URL.Query().Get("crd") != "CRD-001" {
			t.Errorf("expected crd=CRD-001, got %q", r.URL.Query().Get("crd"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entity)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))
	ctx := tracekit.WithTraceID(context.Background(), "tr_test123")

	got, err := client.Resolve(ctx, "organization", ByCRD("CRD-001"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected entity, got nil")
	}
	if got.ID != "ent_001" {
		t.Errorf("expected ID=ent_001, got %q", got.ID)
	}
	if got.CanonicalName != "Acme Corp" {
		t.Errorf("expected CanonicalName=Acme Corp, got %q", got.CanonicalName)
	}
	if len(got.Types) != 1 || got.Types[0] != "organization" {
		t.Errorf("expected Types=[organization], got %v", got.Types)
	}
}

// --------------------------------------------------------------------------
// 2. Resolve not found (404)
// --------------------------------------------------------------------------

func TestResolve_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))

	got, err := client.Resolve(context.Background(), "organization", ByDomain("unknown.com"))
	if err != nil {
		t.Fatalf("expected no error for 404, got: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil entity for 404, got: %+v", got)
	}
}

// --------------------------------------------------------------------------
// 3. Resolve service unavailable
// --------------------------------------------------------------------------

func TestResolve_ServiceUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))

	_, err := client.Resolve(context.Background(), "organization", BySlug("acme"))
	if err == nil {
		t.Fatal("expected error for 503, got nil")
	}
	if got := err.Error(); got != "registrykit: service unavailable" {
		t.Errorf("expected 'registrykit: service unavailable', got %q", got)
	}
}

// --------------------------------------------------------------------------
// 4. Resolve tenant denied (403)
// --------------------------------------------------------------------------

func TestResolve_TenantDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("tenant not authorized"))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("bad-tenant"))

	_, err := client.Resolve(context.Background(), "organization", ByEmail("info@acme.com"))
	if err == nil {
		t.Fatal("expected error for 403, got nil")
	}
	if got := err.Error(); got != "registrykit: forbidden: tenant not authorized" {
		t.Errorf("expected 'registrykit: forbidden: tenant not authorized', got %q", got)
	}
}

// --------------------------------------------------------------------------
// 5. Related returns relationships
// --------------------------------------------------------------------------

func TestRelated_ReturnsRelationships(t *testing.T) {
	rels := []Relationship{
		{
			FromEntity:   "ent_001",
			ToEntity:     "ent_002",
			Relationship: "subsidiary_of",
			TenantID:     "tenant-abc",
			Metadata:     map[string]any{"since_year": float64(2020)},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Tenant-ID") != "tenant-abc" {
			t.Errorf("expected X-Tenant-ID=tenant-abc, got %q", r.Header.Get("X-Tenant-ID"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rels)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))

	got, err := client.Related(context.Background(), "ent_001", OfType("organization"), Rel("subsidiary_of"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(got))
	}
	if got[0].Relationship != "subsidiary_of" {
		t.Errorf("expected relationship=subsidiary_of, got %q", got[0].Relationship)
	}
	if got[0].ToEntity != "ent_002" {
		t.Errorf("expected to_entity=ent_002, got %q", got[0].ToEntity)
	}
}

// --------------------------------------------------------------------------
// 6. CreateEntity returns entity
// --------------------------------------------------------------------------

func TestCreateEntity_ReturnsEntity(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	created := Entity{
		ID:            "ent_new",
		Types:         []string{"organization"},
		CanonicalName: "NewCo",
		Metadata:      map[string]any{},
		Identifiers:   map[string]string{"crd": "CRD-NEW"},
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type=application/json, got %q", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("X-Tenant-ID") != "tenant-abc" {
			t.Errorf("expected X-Tenant-ID=tenant-abc, got %q", r.Header.Get("X-Tenant-ID"))
		}

		var req CreateEntityRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		if req.CanonicalName != "NewCo" {
			t.Errorf("expected canonical_name=NewCo, got %q", req.CanonicalName)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(created)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))

	got, err := client.CreateEntity(context.Background(), CreateEntityRequest{
		EntityTypes:   []string{"organization"},
		CanonicalName: "NewCo",
		Identifiers:   map[string]string{"crd": "CRD-NEW"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected entity, got nil")
	}
	if got.ID != "ent_new" {
		t.Errorf("expected ID=ent_new, got %q", got.ID)
	}
	if got.CanonicalName != "NewCo" {
		t.Errorf("expected CanonicalName=NewCo, got %q", got.CanonicalName)
	}
}

// --------------------------------------------------------------------------
// 7. Merge conflict (409)
// --------------------------------------------------------------------------

func TestMerge_Conflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"detail":"entities already merged"}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))

	err := client.Merge(context.Background(), "ent_001", "ent_002", "duplicate")
	if err == nil {
		t.Fatal("expected error for 409, got nil")
	}
	if got := err.Error(); got != `registrykit: conflict: {"detail":"entities already merged"}` {
		t.Errorf("expected conflict error with details, got %q", got)
	}
}

// --------------------------------------------------------------------------
// 8. Network timeout
// --------------------------------------------------------------------------

func TestResolve_NetworkTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"), WithTimeout(50*time.Millisecond))

	_, err := client.Resolve(context.Background(), "organization", ByCRD("CRD-001"))
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

// --------------------------------------------------------------------------
// 9. Resolve with ByIdentifier option
// --------------------------------------------------------------------------

func TestResolve_ByIdentifier(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("identifier_ns") != "lei" {
			t.Errorf("expected identifier_ns=lei, got %q", r.URL.Query().Get("identifier_ns"))
		}
		if r.URL.Query().Get("identifier_val") != "ABC123" {
			t.Errorf("expected identifier_val=ABC123, got %q", r.URL.Query().Get("identifier_val"))
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))

	got, err := client.Resolve(context.Background(), "organization", ByIdentifier("lei", "ABC123"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for 404, got: %+v", got)
	}
}

// --------------------------------------------------------------------------
// 10. Graph returns node tree
// --------------------------------------------------------------------------

func TestGraph_ReturnsTree(t *testing.T) {
	node := GraphNode{
		Entity: Entity{
			ID:            "ent_001",
			Types:         []string{"organization"},
			CanonicalName: "ParentCo",
		},
		Relationships: []Relationship{
			{FromEntity: "ent_001", ToEntity: "ent_002", Relationship: "parent_of"},
		},
		Children: []GraphNode{
			{
				Entity: Entity{
					ID:            "ent_002",
					Types:         []string{"organization"},
					CanonicalName: "ChildCo",
				},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("depth") != "2" {
			t.Errorf("expected depth=2, got %q", r.URL.Query().Get("depth"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(node)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))

	got, err := client.Graph(context.Background(), "ent_001", Depth(2))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected graph node, got nil")
	}
	if got.Entity.ID != "ent_001" {
		t.Errorf("expected root entity ID=ent_001, got %q", got.Entity.ID)
	}
	if len(got.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(got.Children))
	}
	if got.Children[0].Entity.CanonicalName != "ChildCo" {
		t.Errorf("expected child name=ChildCo, got %q", got.Children[0].Entity.CanonicalName)
	}
}

// --------------------------------------------------------------------------
// 11. Descendants
// --------------------------------------------------------------------------

func TestDescendants(t *testing.T) {
	entities := []Entity{
		{ID: "ent_002", Types: []string{"organization"}, CanonicalName: "Sub A"},
		{ID: "ent_003", Types: []string{"organization"}, CanonicalName: "Sub B"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entities)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))

	got, err := client.Descendants(context.Background(), "ent_001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 descendants, got %d", len(got))
	}
}

// --------------------------------------------------------------------------
// 12. Ancestors
// --------------------------------------------------------------------------

func TestAncestors(t *testing.T) {
	entities := []Entity{
		{ID: "ent_parent", Types: []string{"organization"}, CanonicalName: "ParentCo"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entities)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))

	got, err := client.Ancestors(context.Background(), "ent_001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 ancestor, got %d", len(got))
	}
	if got[0].CanonicalName != "ParentCo" {
		t.Errorf("expected ParentCo, got %q", got[0].CanonicalName)
	}
}

// --------------------------------------------------------------------------
// 13. AddIdentifier
// --------------------------------------------------------------------------

func TestAddIdentifier(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		var payload map[string]string
		json.NewDecoder(r.Body).Decode(&payload)
		if payload["namespace"] != "lei" || payload["value"] != "LEI-001" {
			t.Errorf("unexpected payload: %+v", payload)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))

	err := client.AddIdentifier(context.Background(), "ent_001", "lei", "LEI-001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --------------------------------------------------------------------------
// 14. CreateRelationship
// --------------------------------------------------------------------------

func TestCreateRelationship(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		var req CreateRelationshipRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.FromEntity != "ent_001" || req.ToEntity != "ent_002" {
			t.Errorf("unexpected request: %+v", req)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, WithTenant("tenant-abc"))

	err := client.CreateRelationship(context.Background(), CreateRelationshipRequest{
		FromEntity:   "ent_001",
		ToEntity:     "ent_002",
		Relationship: "subsidiary_of",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
