# meilikit: GetDocument & DeleteDocument Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `GetDocument` and `DeleteDocument` single-document methods to meilikit's `Index` type.

**Architecture:** Both methods follow the existing HTTP pattern in `index.go` — build request, set headers, execute, check errors, decode response. `GetDocument` returns `(json.RawMessage, error)` with a special `nil, nil` for 404 (not found). `DeleteDocument` returns `(*TaskInfo, error)` since Meilisearch deletes are async. A shared `validateDocID` helper validates that the document ID is non-empty.

**Tech Stack:** Go stdlib (`net/http`, `encoding/json`), existing meilikit internals (`decodeMeiliError`, `setHeaders`, `maxResponseBody`).

---

### Task 1: Add `validateDocID` helper

**Files:**
- Modify: `meilikit/defaults.go:36` (after `validateIndexName`)
- Test: `meilikit/meilikit_test.go`

- [ ] **Step 1: Write the failing test**

Add to `meilikit/meilikit_test.go` after the index name validation tests (~line 118):

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd meilikit && go test -run TestValidateDocID -v`
Expected: FAIL — `validateDocID` undefined

- [ ] **Step 3: Write minimal implementation**

Add to `meilikit/defaults.go` after `validateIndexName`:

```go
// validateDocID checks that a document ID is non-empty.
func validateDocID(id string) error {
	if id == "" {
		return fmt.Errorf("meilikit: document ID must not be empty")
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd meilikit && go test -run TestValidateDocID -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add meilikit/defaults.go meilikit/meilikit_test.go
git commit -m "feat(meilikit): add validateDocID helper"
```

---

### Task 2: Implement `GetDocument`

**Files:**
- Modify: `meilikit/index.go` (add method after `DeleteIndex`, ~line 241)
- Test: `meilikit/meilikit_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `meilikit/meilikit_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd meilikit && go test -run TestGetDocument -v`
Expected: FAIL — `idx.GetDocument` undefined

- [ ] **Step 3: Write the implementation**

Add to `meilikit/index.go` after `DeleteIndex` (after line 241):

```go
// GetDocument retrieves a single document by ID. Returns nil, nil if not found (404).
// Calls GET /indexes/{uid}/documents/{docID}.
func (idx *Index) GetDocument(ctx context.Context, docID string) (json.RawMessage, error) {
	if err := validateDocID(docID); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		idx.client.baseURL+"/indexes/"+idx.name+"/documents/"+docID, nil)
	if err != nil {
		return nil, fmt.Errorf("meilikit: build request: %w", err)
	}
	idx.client.setHeaders(ctx, req)

	resp, err := idx.client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("meilikit: get document %q/%q: %w", idx.name, docID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 400 {
		return nil, decodeMeiliError(resp)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("meilikit: read document %q/%q: %w", idx.name, docID, err)
	}
	return json.RawMessage(body), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd meilikit && go test -run TestGetDocument -v`
Expected: All 4 tests PASS

- [ ] **Step 5: Commit**

```bash
git add meilikit/index.go meilikit/meilikit_test.go
git commit -m "feat(meilikit): add GetDocument method"
```

---

### Task 3: Implement `DeleteDocument`

**Files:**
- Modify: `meilikit/index.go` (add method after `GetDocument`)
- Test: `meilikit/meilikit_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `meilikit/meilikit_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd meilikit && go test -run TestDeleteDocument -v`
Expected: FAIL — `idx.DeleteDocument` undefined

- [ ] **Step 3: Write the implementation**

Add to `meilikit/index.go` after `GetDocument`:

```go
// DeleteDocument deletes a single document by ID.
// Calls DELETE /indexes/{uid}/documents/{docID}.
func (idx *Index) DeleteDocument(ctx context.Context, docID string) (*TaskInfo, error) {
	if err := validateDocID(docID); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		idx.client.baseURL+"/indexes/"+idx.name+"/documents/"+docID, nil)
	if err != nil {
		return nil, fmt.Errorf("meilikit: build request: %w", err)
	}
	idx.client.setHeaders(ctx, req)

	resp, err := idx.client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("meilikit: delete document %q/%q: %w", idx.name, docID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, decodeMeiliError(resp)
	}

	var task TaskInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&task); err != nil {
		return nil, fmt.Errorf("meilikit: decode task: %w", err)
	}
	return &task, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd meilikit && go test -run TestDeleteDocument -v`
Expected: All 3 tests PASS

- [ ] **Step 5: Commit**

```bash
git add meilikit/index.go meilikit/meilikit_test.go
git commit -m "feat(meilikit): add DeleteDocument method"
```

---

### Task 4: Run full test suite and version bump

**Files:**
- Modify: `VERSION`, `CHANGELOG.md`

- [ ] **Step 1: Run the full meilikit test suite**

Run: `cd meilikit && go test -v -count=1 ./...`
Expected: All tests PASS (existing + new)

- [ ] **Step 2: Run project-wide tests to check for regressions**

Run: `go test ./... 2>&1 | tail -20`
Expected: No failures

- [ ] **Step 3: Read VERSION, increment, and update CHANGELOG**

Read `VERSION`, increment the patch version. Add a CHANGELOG entry:

```
- feat(meilikit): add GetDocument (returns nil,nil on 404) and DeleteDocument single-document methods
```

- [ ] **Step 4: Commit and push**

```bash
git add -A
git commit -m "feat(meilikit): add GetDocument and DeleteDocument methods

Claude:Opus 4.6"
git push
```
