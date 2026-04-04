package meilikit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v10"
	"github.com/ai8future/chassis-go/v10/tracekit"
)

func init() {
	chassis.RequireMajor(10)
}

// --------------------------------------------------------------------------
// Client creation
// --------------------------------------------------------------------------

func TestNew_EmptyBaseURL(t *testing.T) {
	_, err := New(Config{})
	if err == nil {
		t.Fatal("expected error for empty base URL")
	}
}

func TestNew_Success(t *testing.T) {
	c, err := New(Config{BaseURL: "http://localhost:7700", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.baseURL != "http://localhost:7700" {
		t.Fatalf("baseURL = %q, want http://localhost:7700", c.baseURL)
	}
	if c.http == nil {
		t.Fatal("call.Client must not be nil")
	}
}

func TestNew_TrimsTrailingSlash(t *testing.T) {
	c, err := New(Config{BaseURL: "http://localhost:7700/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.baseURL != "http://localhost:7700" {
		t.Fatalf("baseURL = %q, want trailing slash trimmed", c.baseURL)
	}
}

func TestNew_WithOptions(t *testing.T) {
	c, err := New(Config{BaseURL: "http://localhost:7700"},
		WithTenant("tenant-42"),
		WithTimeout(10*time.Second),
		WithRetry(3, 100*time.Millisecond),
		WithCircuitBreaker("meili", 5, 30*time.Second),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.tenantID != "tenant-42" {
		t.Fatalf("tenantID = %q, want tenant-42", c.tenantID)
	}
	if c.http == nil {
		t.Fatal("call.Client must not be nil")
	}
}

// --------------------------------------------------------------------------
// Index name validation
// --------------------------------------------------------------------------

func TestIndex_EmptyName(t *testing.T) {
	c, _ := New(Config{BaseURL: "http://localhost:7700"})
	_, err := c.Index("")
	if err == nil {
		t.Fatal("expected error for empty index name")
	}
}

func TestIndex_InvalidChars(t *testing.T) {
	c, _ := New(Config{BaseURL: "http://localhost:7700"})
	invalid := []string{
		"bad/name",
		"has space",
		"has.dot",
		"query?param",
		"hash#frag",
		"pct%20enc",
		"../traversal",
		"tab\there",
		"unicode\u00e9",
	}
	for _, name := range invalid {
		_, err := c.Index(name)
		if err == nil {
			t.Errorf("expected error for index name %q", name)
		}
	}
}

func TestIndex_ValidName(t *testing.T) {
	c, _ := New(Config{BaseURL: "http://localhost:7700"})
	valid := []string{"people", "my-index", "my_index", "Index123", "A", "a-b_c-0"}
	for _, name := range valid {
		idx, err := c.Index(name)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", name, err)
		}
		if idx.name != name {
			t.Fatalf("name = %q, want %q", idx.name, name)
		}
	}
}

// --------------------------------------------------------------------------
// Ping
// --------------------------------------------------------------------------

func TestPing_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("path = %q, want /health", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"available"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPing_Unavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		w.Write([]byte(`{"message":"service unavailable","code":"service_unavailable","type":"system"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error for 503")
	}
	me, ok := err.(*MeiliError)
	if !ok {
		t.Fatalf("expected *MeiliError, got %T", err)
	}
	if me.StatusCode != 503 {
		t.Fatalf("StatusCode = %d, want 503", me.StatusCode)
	}
}

// --------------------------------------------------------------------------
// Header verification
// --------------------------------------------------------------------------

func TestHeaders_AuthAndTenantAndTrace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer my-key" {
			t.Errorf("Authorization = %q, want Bearer my-key", got)
		}
		if got := r.Header.Get("X-Tenant-ID"); got != "t-99" {
			t.Errorf("X-Tenant-ID = %q, want t-99", got)
		}
		if got := r.Header.Get("X-Trace-ID"); got != "tr_abc123" {
			t.Errorf("X-Trace-ID = %q, want tr_abc123", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"hits":[],"query":"x","processingTimeMs":0}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL, APIKey: "my-key"}, WithTenant("t-99"))
	idx, _ := c.Index("test")
	ctx := tracekit.WithTraceID(context.Background(), "tr_abc123")
	idx.Search(ctx, "x", SearchOptions{})
}

// --------------------------------------------------------------------------
// Search
// --------------------------------------------------------------------------

func TestSearch_QueryAndOptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/indexes/people/search" {
			t.Errorf("path = %q, want /indexes/people/search", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}

		var body searchRequest
		json.NewDecoder(r.Body).Decode(&body)
		if body.Q != "john" {
			t.Errorf("q = %q, want john", body.Q)
		}
		if body.Filter != "company_id = 42" {
			t.Errorf("filter = %q, want company_id = 42", body.Filter)
		}
		if body.Limit != 10 {
			t.Errorf("limit = %d, want 10", body.Limit)
		}

		w.WriteHeader(200)
		w.Write([]byte(`{
			"hits": [{"id": "1", "name": "John"}],
			"query": "john",
			"processingTimeMs": 5,
			"estimatedTotalHits": 1
		}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("people")
	result, err := idx.Search(context.Background(), "john", SearchOptions{
		Filter: "company_id = 42",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(result.Hits))
	}
	if result.Query != "john" {
		t.Fatalf("query = %q, want john", result.Query)
	}
}

func TestSearch_DefaultLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body searchRequest
		json.NewDecoder(r.Body).Decode(&body)
		if body.Limit != 20 {
			t.Errorf("limit = %d, want 20 (default)", body.Limit)
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"hits":[],"query":"x","processingTimeMs":0}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")
	idx.Search(context.Background(), "x", SearchOptions{})
}

func TestSearch_HighlightOptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body searchRequest
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.AttributesToHighlight) != 2 {
			t.Errorf("attributesToHighlight len = %d, want 2", len(body.AttributesToHighlight))
		}
		if body.HighlightPreTag != "<em>" {
			t.Errorf("highlightPreTag = %q, want <em>", body.HighlightPreTag)
		}
		if body.HighlightPostTag != "</em>" {
			t.Errorf("highlightPostTag = %q, want </em>", body.HighlightPostTag)
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"hits":[],"query":"x","processingTimeMs":0}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")
	idx.Search(context.Background(), "x", SearchOptions{
		AttributesToHighlight: []string{"name", "bio"},
		HighlightPreTag:       "<em>",
		HighlightPostTag:      "</em>",
	})
}

// --------------------------------------------------------------------------
// MultiSearch
// --------------------------------------------------------------------------

func TestMultiSearch_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/multi-search" {
			t.Errorf("path = %q, want /multi-search", r.URL.Path)
		}

		var body multiSearchRequest
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Queries) != 2 {
			t.Fatalf("queries len = %d, want 2", len(body.Queries))
		}
		if body.Queries[0].IndexUID != "people" {
			t.Errorf("query[0].indexUid = %q, want people", body.Queries[0].IndexUID)
		}
		if body.Queries[1].IndexUID != "companies" {
			t.Errorf("query[1].indexUid = %q, want companies", body.Queries[1].IndexUID)
		}

		w.WriteHeader(200)
		w.Write([]byte(`{
			"results": [
				{"hits":[{"id":"1"}],"query":"x","processingTimeMs":1},
				{"hits":[{"id":"2"},{"id":"3"}],"query":"y","processingTimeMs":2}
			]
		}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	result, err := c.MultiSearch(context.Background(), []SearchQuery{
		{IndexUID: "people", Query: "x"},
		{IndexUID: "companies", Query: "y"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(result.Results))
	}
	if len(result.Results[0].Hits) != 1 {
		t.Errorf("results[0].hits = %d, want 1", len(result.Results[0].Hits))
	}
	if len(result.Results[1].Hits) != 2 {
		t.Errorf("results[1].hits = %d, want 2", len(result.Results[1].Hits))
	}
}

// --------------------------------------------------------------------------
// Configure idempotency
// --------------------------------------------------------------------------

func TestConfigure_IndexAlreadyExists(t *testing.T) {
	step := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/indexes" && r.Method == http.MethodPost:
			step++
			w.WriteHeader(409)
			w.Write([]byte(`{"message":"Index people already exists.","code":"index_already_exists","type":"invalid_request"}`))
		case strings.HasSuffix(r.URL.Path, "/settings") && r.Method == http.MethodPatch:
			step++
			w.WriteHeader(202)
			w.Write([]byte(`{"taskUid":1,"indexUid":"people","status":"enqueued","type":"settingsUpdate"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("people")
	err := idx.Configure(context.Background(), IndexConfig{
		PrimaryKey: "id",
		Searchable: []string{"name"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if step != 2 {
		t.Fatalf("step = %d, want 2 (create + settings)", step)
	}
}

// --------------------------------------------------------------------------
// BulkImport
// --------------------------------------------------------------------------

func TestBulkImport_Progress(t *testing.T) {
	batchCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		batchCount++
		w.WriteHeader(202)
		w.Write([]byte(`{"taskUid":1,"status":"enqueued","type":"documentAdditionOrUpdate"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")

	records := make([]any, 250)
	for i := range records {
		records[i] = map[string]int{"id": i}
	}

	progressCalls := 0
	var lastImported, lastTotal int
	err := idx.BulkImport(context.Background(), records, BulkOptions{
		BatchSize: 100,
		OnProgress: func(imported, total int) {
			progressCalls++
			lastImported = imported
			lastTotal = total
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batchCount != 3 {
		t.Fatalf("batches = %d, want 3 (100+100+50)", batchCount)
	}
	if progressCalls != 3 {
		t.Fatalf("progress calls = %d, want 3", progressCalls)
	}
	if lastImported != 250 {
		t.Fatalf("lastImported = %d, want 250", lastImported)
	}
	if lastTotal != 250 {
		t.Fatalf("lastTotal = %d, want 250", lastTotal)
	}
}

func TestBulkImport_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
		w.Write([]byte(`{"taskUid":1}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	records := make([]any, 10)
	err := idx.BulkImport(ctx, records, BulkOptions{BatchSize: 5})
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestBulkImport_DefaultBatchSize(t *testing.T) {
	var received int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var docs []json.RawMessage
		json.Unmarshal(body, &docs)
		received += len(docs)
		w.WriteHeader(202)
		w.Write([]byte(`{"taskUid":1}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")
	records := make([]any, 50)
	for i := range records {
		records[i] = map[string]int{"id": i}
	}
	idx.BulkImport(context.Background(), records, BulkOptions{})
	if received != 50 {
		t.Fatalf("received = %d, want 50", received)
	}
}

// --------------------------------------------------------------------------
// WaitForTask
// --------------------------------------------------------------------------

func TestWaitForTask_Succeeds(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		status := "processing"
		if calls >= 2 {
			status = "succeeded"
		}
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(Task{UID: 42, Status: status})
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")
	task, err := idx.WaitForTask(context.Background(), 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task.Status != "succeeded" {
		t.Fatalf("status = %q, want succeeded", task.Status)
	}
}

func TestWaitForTask_Failed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"uid":42,"status":"failed","error":{"message":"bad data","code":"invalid_document"}}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")
	task, err := idx.WaitForTask(context.Background(), 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task.Status != "failed" {
		t.Fatalf("status = %q, want failed", task.Status)
	}
	if task.Error == nil || task.Error.Code != "invalid_document" {
		t.Fatalf("expected task error with code invalid_document")
	}
}

func TestWaitForTask_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(Task{UID: 42, Status: "processing"})
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := idx.WaitForTask(ctx, 42)
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

// --------------------------------------------------------------------------
// Error handling
// --------------------------------------------------------------------------

func TestError_400(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"message":"Invalid search query","code":"invalid_search_q","type":"invalid_request","link":"https://docs.meilisearch.com/errors#invalid_search_q"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")
	_, err := idx.Search(context.Background(), "", SearchOptions{})
	if err == nil {
		t.Fatal("expected error for 400")
	}
	me, ok := err.(*MeiliError)
	if !ok {
		t.Fatalf("expected *MeiliError, got %T", err)
	}
	if me.StatusCode != 400 {
		t.Errorf("StatusCode = %d, want 400", me.StatusCode)
	}
	if me.Code != "invalid_search_q" {
		t.Errorf("Code = %q, want invalid_search_q", me.Code)
	}
}

func TestError_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"message":"The Authorization header is missing.","code":"missing_authorization_header","type":"auth"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")
	_, err := idx.Search(context.Background(), "x", SearchOptions{})
	me, ok := err.(*MeiliError)
	if !ok {
		t.Fatalf("expected *MeiliError, got %T", err)
	}
	if me.StatusCode != 401 {
		t.Errorf("StatusCode = %d, want 401", me.StatusCode)
	}
}

func TestError_403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"message":"Forbidden","code":"invalid_api_key","type":"auth"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	err := c.Ping(context.Background())
	me, ok := err.(*MeiliError)
	if !ok {
		t.Fatalf("expected *MeiliError, got %T", err)
	}
	if me.StatusCode != 403 {
		t.Errorf("StatusCode = %d, want 403", me.StatusCode)
	}
}

func TestError_5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"message":"Internal Server Error","code":"internal","type":"internal"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")
	_, err := idx.Search(context.Background(), "x", SearchOptions{})
	me, ok := err.(*MeiliError)
	if !ok {
		t.Fatalf("expected *MeiliError, got %T", err)
	}
	if me.StatusCode != 500 {
		t.Errorf("StatusCode = %d, want 500", me.StatusCode)
	}
}

// --------------------------------------------------------------------------
// AddDocuments / UpdateDocuments / DeleteIndex
// --------------------------------------------------------------------------

func TestAddDocuments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/indexes/test/documents" {
			t.Errorf("path = %q, want /indexes/test/documents", r.URL.Path)
		}
		w.WriteHeader(202)
		w.Write([]byte(`{"taskUid":10,"indexUid":"test","status":"enqueued","type":"documentAdditionOrUpdate"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")
	task, err := idx.AddDocuments(context.Background(), []any{map[string]string{"id": "1"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task.TaskUID != 10 {
		t.Fatalf("taskUID = %d, want 10", task.TaskUID)
	}
}

func TestUpdateDocuments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %q, want PUT", r.Method)
		}
		w.WriteHeader(202)
		w.Write([]byte(`{"taskUid":11,"indexUid":"test","status":"enqueued","type":"documentAdditionOrUpdate"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")
	task, err := idx.UpdateDocuments(context.Background(), []any{map[string]string{"id": "1"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task.TaskUID != 11 {
		t.Fatalf("taskUID = %d, want 11", task.TaskUID)
	}
}

func TestDeleteIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		if r.URL.Path != "/indexes/test" {
			t.Errorf("path = %q, want /indexes/test", r.URL.Path)
		}
		w.WriteHeader(202)
		w.Write([]byte(`{"taskUid":12,"indexUid":"test","status":"enqueued","type":"indexDeletion"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")
	task, err := idx.DeleteIndex(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task.TaskUID != 12 {
		t.Fatalf("taskUID = %d, want 12", task.TaskUID)
	}
}

// --------------------------------------------------------------------------
// GetSettings / UpdateSettings
// --------------------------------------------------------------------------

func TestGetSettings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"searchableAttributes":["name","bio"],"filterableAttributes":["company_id"]}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")
	settings, err := idx.GetSettings(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(settings.SearchableAttributes) != 2 {
		t.Fatalf("searchable = %d, want 2", len(settings.SearchableAttributes))
	}
}

func TestUpdateSettings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("method = %q, want PATCH", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/merge-patch+json" {
			t.Errorf("Content-Type = %q, want application/merge-patch+json", ct)
		}
		w.WriteHeader(202)
		w.Write([]byte(`{"taskUid":13,"status":"enqueued","type":"settingsUpdate"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")
	task, err := idx.UpdateSettings(context.Background(), Settings{
		SearchableAttributes: []string{"name"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task.TaskUID != 13 {
		t.Fatalf("taskUID = %d, want 13", task.TaskUID)
	}
}

// --------------------------------------------------------------------------
// Configure happy path + settings failure
// --------------------------------------------------------------------------

func TestConfigure_HappyPath(t *testing.T) {
	step := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/indexes" && r.Method == http.MethodPost:
			step++
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["uid"] != "people" {
				t.Errorf("uid = %q, want people", body["uid"])
			}
			if body["primaryKey"] != "id" {
				t.Errorf("primaryKey = %q, want id", body["primaryKey"])
			}
			w.WriteHeader(202)
			w.Write([]byte(`{"taskUid":1,"indexUid":"people","status":"enqueued","type":"indexCreation"}`))
		case r.URL.Path == "/tasks/1" && r.Method == http.MethodGet:
			step++
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(Task{UID: 1, Status: "succeeded"})
		case strings.HasSuffix(r.URL.Path, "/settings") && r.Method == http.MethodPatch:
			step++
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			searchable, _ := body["searchableAttributes"].([]any)
			if len(searchable) != 2 {
				t.Errorf("searchableAttributes len = %d, want 2", len(searchable))
			}
			w.WriteHeader(202)
			w.Write([]byte(`{"taskUid":2,"indexUid":"people","status":"enqueued","type":"settingsUpdate"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("people")
	err := idx.Configure(context.Background(), IndexConfig{
		PrimaryKey: "id",
		Searchable: []string{"name", "bio"},
		Filterable: []string{"company_id"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if step != 3 {
		t.Fatalf("step = %d, want 3 (create + wait + settings)", step)
	}
}

func TestConfigure_SettingsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/indexes" && r.Method == http.MethodPost:
			w.WriteHeader(202)
			w.Write([]byte(`{"taskUid":1,"status":"enqueued"}`))
		case r.URL.Path == "/tasks/1" && r.Method == http.MethodGet:
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(Task{UID: 1, Status: "succeeded"})
		case strings.HasSuffix(r.URL.Path, "/settings"):
			w.WriteHeader(400)
			w.Write([]byte(`{"message":"Invalid settings","code":"invalid_settings","type":"invalid_request"}`))
		}
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")
	err := idx.Configure(context.Background(), IndexConfig{PrimaryKey: "id"})
	if err == nil {
		t.Fatal("expected error when settings fail")
	}
	me, ok := err.(*MeiliError)
	if !ok {
		t.Fatalf("expected *MeiliError, got %T", err)
	}
	if me.Code != "invalid_settings" {
		t.Errorf("Code = %q, want invalid_settings", me.Code)
	}
}

// --------------------------------------------------------------------------
// BulkImport HTTP error
// --------------------------------------------------------------------------

func TestBulkImport_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(413)
		w.Write([]byte(`{"message":"Payload too large","code":"payload_too_large","type":"invalid_request"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")
	err := idx.BulkImport(context.Background(), []any{map[string]int{"id": 1}}, BulkOptions{})
	if err == nil {
		t.Fatal("expected error for 413")
	}
	me, ok := err.(*MeiliError)
	if !ok {
		t.Fatalf("expected *MeiliError, got %T: %v", err, err)
	}
	if me.StatusCode != 413 {
		t.Errorf("StatusCode = %d, want 413", me.StatusCode)
	}
}

func TestBulkImport_EmptyRecords(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(202)
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")
	err := idx.BulkImport(context.Background(), nil, BulkOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatal("expected no HTTP calls for empty records")
	}
}

// --------------------------------------------------------------------------
// WaitForTask HTTP error
// --------------------------------------------------------------------------

func TestWaitForTask_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"message":"Task 999 not found","code":"task_not_found","type":"invalid_request"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")
	_, err := idx.WaitForTask(context.Background(), 999)
	if err == nil {
		t.Fatal("expected error for 404 task")
	}
	me, ok := err.(*MeiliError)
	if !ok {
		t.Fatalf("expected *MeiliError, got %T: %v", err, err)
	}
	if me.StatusCode != 404 {
		t.Errorf("StatusCode = %d, want 404", me.StatusCode)
	}
}

// --------------------------------------------------------------------------
// No API key -- no Authorization header
// --------------------------------------------------------------------------

func TestHeaders_NoAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty (no API key configured)", got)
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"available"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	c.Ping(context.Background())
}

// --------------------------------------------------------------------------
// Non-JSON error body
// --------------------------------------------------------------------------

func TestError_NonJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(502)
		w.Write([]byte(`<html><body>Bad Gateway</body></html>`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error for 502")
	}
	// Should NOT be *MeiliError since the body isn't valid JSON
	if _, ok := err.(*MeiliError); ok {
		t.Fatal("expected non-MeiliError for HTML body")
	}
	// Should contain the status code
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error %q should mention status 502", err.Error())
	}
}

// --------------------------------------------------------------------------
// Search facets response
// --------------------------------------------------------------------------

func TestSearch_FacetResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{
			"hits": [],
			"query": "x",
			"processingTimeMs": 2,
			"facetDistribution": {"category": {"books": 10, "movies": 5}},
			"facetStats": {"price": {"min": 1.99, "max": 49.99}}
		}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("products")
	result, err := idx.Search(context.Background(), "x", SearchOptions{
		Facets: []string{"category", "price"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FacetDistribution == nil {
		t.Fatal("expected facetDistribution")
	}
	if result.FacetDistribution["category"]["books"] != 10 {
		t.Errorf("category.books = %d, want 10", result.FacetDistribution["category"]["books"])
	}
	if result.FacetStats == nil {
		t.Fatal("expected facetStats")
	}
	if result.FacetStats["price"].Min != 1.99 {
		t.Errorf("price.min = %f, want 1.99", result.FacetStats["price"].Min)
	}
	if result.FacetStats["price"].Max != 49.99 {
		t.Errorf("price.max = %f, want 49.99", result.FacetStats["price"].Max)
	}
}

// --------------------------------------------------------------------------
// MultiSearch with options
// --------------------------------------------------------------------------

func TestMultiSearch_WithOptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body multiSearchRequest
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Queries) != 1 {
			t.Fatalf("queries len = %d, want 1", len(body.Queries))
		}
		q := body.Queries[0]
		if q.Filter != "active = true" {
			t.Errorf("filter = %q, want active = true", q.Filter)
		}
		if q.Limit != 5 {
			t.Errorf("limit = %d, want 5", q.Limit)
		}
		if len(q.Sort) != 1 || q.Sort[0] != "name:asc" {
			t.Errorf("sort = %v, want [name:asc]", q.Sort)
		}
		if len(q.Facets) != 1 || q.Facets[0] != "category" {
			t.Errorf("facets = %v, want [category]", q.Facets)
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"results":[{"hits":[],"query":"x","processingTimeMs":0}]}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	_, err := c.MultiSearch(context.Background(), []SearchQuery{
		{
			IndexUID: "people",
			Query:    "x",
			SearchOptions: SearchOptions{
				Filter: "active = true",
				Limit:  5,
				Sort:   []string{"name:asc"},
				Facets: []string{"category"},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --------------------------------------------------------------------------
// SanitizeUTF8
// --------------------------------------------------------------------------

func TestSanitizeUTF8_Valid(t *testing.T) {
	in := "hello world"
	if got := SanitizeUTF8(in); got != in {
		t.Fatalf("SanitizeUTF8(%q) = %q, want %q", in, got, in)
	}
}

func TestSanitizeUTF8_Invalid(t *testing.T) {
	in := "hello\x80world"
	got := SanitizeUTF8(in)
	if strings.Contains(got, "\x80") {
		t.Fatalf("SanitizeUTF8 did not replace invalid byte: %q", got)
	}
	if !strings.Contains(got, "hello") || !strings.Contains(got, "world") {
		t.Fatalf("SanitizeUTF8 lost valid content: %q", got)
	}
}

// --------------------------------------------------------------------------
// Defaults
// --------------------------------------------------------------------------

func TestBulkOptions_MaxBatchSizeCapped(t *testing.T) {
	opts := resolveBulkDefaults(BulkOptions{BatchSize: 99999})
	if opts.BatchSize != maxBatchSize {
		t.Fatalf("BatchSize = %d, want %d (capped)", opts.BatchSize, maxBatchSize)
	}
}

func TestBulkOptions_DefaultBatchSize(t *testing.T) {
	opts := resolveBulkDefaults(BulkOptions{})
	if opts.BatchSize != defaultBatchSize {
		t.Fatalf("BatchSize = %d, want %d (default)", opts.BatchSize, defaultBatchSize)
	}
}

// --------------------------------------------------------------------------
// Document ID validation
// --------------------------------------------------------------------------

func TestValidateDocID_Empty(t *testing.T) {
	if err := validateDocID(""); err == nil {
		t.Fatal("expected error for empty doc ID")
	}
}

func TestValidateDocID_Valid(t *testing.T) {
	valid := []string{"1", "abc-123", "my_doc", "DOC42"}
	for _, id := range valid {
		if err := validateDocID(id); err != nil {
			t.Fatalf("unexpected error for doc ID %q: %v", id, err)
		}
	}
}

// --------------------------------------------------------------------------
// GetDocument
// --------------------------------------------------------------------------

func TestGetDocument_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if r.URL.Path != "/indexes/people/documents/doc-1" {
			t.Errorf("path = %q, want /indexes/people/documents/doc-1", r.URL.Path)
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"doc-1","name":"Alice"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("people")
	doc, err := idx.GetDocument(context.Background(), "doc-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc == nil {
		t.Fatal("expected non-nil document")
	}
	var parsed map[string]string
	json.Unmarshal(doc, &parsed)
	if parsed["name"] != "Alice" {
		t.Fatalf("name = %q, want Alice", parsed["name"])
	}
}

func TestGetDocument_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"message":"Document doc-99 not found.","code":"document_not_found","type":"invalid_request"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("people")
	doc, err := idx.GetDocument(context.Background(), "doc-99")
	if err != nil {
		t.Fatalf("expected nil error for 404, got: %v", err)
	}
	if doc != nil {
		t.Fatalf("expected nil doc for 404, got: %s", string(doc))
	}
}

func TestGetDocument_EmptyID(t *testing.T) {
	c, _ := New(Config{BaseURL: "http://localhost:7700"})
	idx, _ := c.Index("test")
	_, err := idx.GetDocument(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty doc ID")
	}
}

func TestGetDocument_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"message":"Internal Server Error","code":"internal","type":"internal"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")
	_, err := idx.GetDocument(context.Background(), "doc-1")
	if err == nil {
		t.Fatal("expected error for 500")
	}
	me, ok := err.(*MeiliError)
	if !ok {
		t.Fatalf("expected *MeiliError, got %T", err)
	}
	if me.StatusCode != 500 {
		t.Errorf("StatusCode = %d, want 500", me.StatusCode)
	}
}

// --------------------------------------------------------------------------
// DeleteDocument
// --------------------------------------------------------------------------

func TestDeleteDocument_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		if r.URL.Path != "/indexes/people/documents/doc-1" {
			t.Errorf("path = %q, want /indexes/people/documents/doc-1", r.URL.Path)
		}
		w.WriteHeader(202)
		w.Write([]byte(`{"taskUid":20,"indexUid":"people","status":"enqueued","type":"documentDeletion"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("people")
	task, err := idx.DeleteDocument(context.Background(), "doc-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task.TaskUID != 20 {
		t.Fatalf("taskUID = %d, want 20", task.TaskUID)
	}
}

func TestDeleteDocument_EmptyID(t *testing.T) {
	c, _ := New(Config{BaseURL: "http://localhost:7700"})
	idx, _ := c.Index("test")
	_, err := idx.DeleteDocument(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty doc ID")
	}
}

func TestDeleteDocument_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"message":"Internal Server Error","code":"internal","type":"internal"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	idx, _ := c.Index("test")
	_, err := idx.DeleteDocument(context.Background(), "doc-1")
	if err == nil {
		t.Fatal("expected error for 500")
	}
	me, ok := err.(*MeiliError)
	if !ok {
		t.Fatalf("expected *MeiliError, got %T", err)
	}
	if me.StatusCode != 500 {
		t.Errorf("StatusCode = %d, want 500", me.StatusCode)
	}
}
