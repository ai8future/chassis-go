package meilikit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// maxResponseBody caps how much of a success response we read (8 MB).
const maxResponseBody = 8 << 20

// Index is a handle for a Meilisearch index.
type Index struct {
	name   string
	client *Client
}

// Configure creates the index if needed and applies settings. Idempotent --
// if the index already exists, the create is silently skipped.
func (idx *Index) Configure(ctx context.Context, cfg IndexConfig) error {
	// Step 1: create index (swallow "index_already_exists").
	createBody, err := json.Marshal(map[string]string{
		"uid":        idx.name,
		"primaryKey": cfg.PrimaryKey,
	})
	if err != nil {
		return fmt.Errorf("meilikit: marshal create: %w", err)
	}

	createReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		idx.client.baseURL+"/indexes", bytes.NewReader(createBody))
	if err != nil {
		return fmt.Errorf("meilikit: build request: %w", err)
	}
	createReq.Header.Set("Content-Type", "application/json")
	idx.client.setHeaders(ctx, createReq)

	createResp, err := idx.client.http.Do(createReq)
	if err != nil {
		return fmt.Errorf("meilikit: configure %q: %w", idx.name, err)
	}

	if createResp.StatusCode >= 400 {
		createErr := decodeMeiliError(createResp)
		createResp.Body.Close()
		me, ok := createErr.(*MeiliError)
		if !ok || me.Code != "index_already_exists" {
			return createErr
		}
	} else {
		// Wait for the async index creation to complete before applying settings.
		var task TaskInfo
		err = json.NewDecoder(io.LimitReader(createResp.Body, maxResponseBody)).Decode(&task)
		createResp.Body.Close()
		if err != nil {
			return fmt.Errorf("meilikit: decode create task: %w", err)
		}
		if _, err := idx.WaitForTask(ctx, task.TaskUID); err != nil {
			return fmt.Errorf("meilikit: configure %q: wait for create: %w", idx.name, err)
		}
	}

	// Step 2: apply settings.
	settings := buildSettingsRequest(cfg)
	settingsBody, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("meilikit: marshal settings: %w", err)
	}

	settingsReq, err := http.NewRequestWithContext(ctx, http.MethodPatch,
		idx.client.baseURL+"/indexes/"+idx.name+"/settings", bytes.NewReader(settingsBody))
	if err != nil {
		return fmt.Errorf("meilikit: build request: %w", err)
	}
	settingsReq.Header.Set("Content-Type", "application/merge-patch+json")
	idx.client.setHeaders(ctx, settingsReq)

	settingsResp, err := idx.client.http.Do(settingsReq)
	if err != nil {
		return fmt.Errorf("meilikit: configure %q settings: %w", idx.name, err)
	}
	defer settingsResp.Body.Close()

	if settingsResp.StatusCode >= 400 {
		return decodeMeiliError(settingsResp)
	}
	return nil
}

// Search performs a search on this index. POST /indexes/{uid}/search.
func (idx *Index) Search(ctx context.Context, query string, opts SearchOptions) (*SearchResult, error) {
	opts = resolveSearchDefaults(opts)
	body, err := json.Marshal(buildSearchRequest(query, opts))
	if err != nil {
		return nil, fmt.Errorf("meilikit: marshal search: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		idx.client.baseURL+"/indexes/"+idx.name+"/search", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("meilikit: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	idx.client.setHeaders(ctx, req)

	resp, err := idx.client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("meilikit: search %q: %w", idx.name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, decodeMeiliError(resp)
	}

	var result SearchResult
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&result); err != nil {
		return nil, fmt.Errorf("meilikit: decode search: %w", err)
	}
	return &result, nil
}

// BulkImport adds records in batches with optional progress reporting.
func (idx *Index) BulkImport(ctx context.Context, records []any, opts BulkOptions) error {
	opts = resolveBulkDefaults(opts)
	total := len(records)
	imported := 0

	for i := 0; i < total; i += opts.BatchSize {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("meilikit: bulk import %q: %w", idx.name, err)
		}
		end := i + opts.BatchSize
		if end > total {
			end = total
		}
		batch := records[i:end]

		body, err := json.Marshal(batch)
		if err != nil {
			return fmt.Errorf("meilikit: marshal batch: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			idx.client.baseURL+"/indexes/"+idx.name+"/documents", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("meilikit: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		idx.client.setHeaders(ctx, req)

		resp, err := idx.client.http.Do(req)
		if err != nil {
			return fmt.Errorf("meilikit: bulk import %q: %w", idx.name, err)
		}

		if resp.StatusCode >= 400 {
			err := decodeMeiliError(resp)
			resp.Body.Close()
			return err
		}
		resp.Body.Close()

		imported += len(batch)
		if opts.OnProgress != nil {
			opts.OnProgress(imported, total)
		}
	}
	return nil
}

// AddDocuments adds or replaces documents. Returns task info.
func (idx *Index) AddDocuments(ctx context.Context, docs []any) (*TaskInfo, error) {
	return idx.postDocuments(ctx, http.MethodPost, docs)
}

// UpdateDocuments partially updates documents (merge on primary key).
func (idx *Index) UpdateDocuments(ctx context.Context, docs []any) (*TaskInfo, error) {
	return idx.postDocuments(ctx, http.MethodPut, docs)
}

func (idx *Index) postDocuments(ctx context.Context, method string, docs []any) (*TaskInfo, error) {
	body, err := json.Marshal(docs)
	if err != nil {
		return nil, fmt.Errorf("meilikit: marshal documents: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method,
		idx.client.baseURL+"/indexes/"+idx.name+"/documents", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("meilikit: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	idx.client.setHeaders(ctx, req)

	resp, err := idx.client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("meilikit: documents %q: %w", idx.name, err)
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

// DeleteIndex deletes this index.
func (idx *Index) DeleteIndex(ctx context.Context) (*TaskInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		idx.client.baseURL+"/indexes/"+idx.name, nil)
	if err != nil {
		return nil, fmt.Errorf("meilikit: build request: %w", err)
	}
	idx.client.setHeaders(ctx, req)

	resp, err := idx.client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("meilikit: delete index %q: %w", idx.name, err)
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

// GetSettings returns the current settings for this index.
func (idx *Index) GetSettings(ctx context.Context) (*Settings, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		idx.client.baseURL+"/indexes/"+idx.name+"/settings", nil)
	if err != nil {
		return nil, fmt.Errorf("meilikit: build request: %w", err)
	}
	idx.client.setHeaders(ctx, req)

	resp, err := idx.client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("meilikit: get settings %q: %w", idx.name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, decodeMeiliError(resp)
	}

	var settings Settings
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&settings); err != nil {
		return nil, fmt.Errorf("meilikit: decode settings: %w", err)
	}
	return &settings, nil
}

// UpdateSettings applies settings to this index.
func (idx *Index) UpdateSettings(ctx context.Context, settings Settings) (*TaskInfo, error) {
	body, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("meilikit: marshal settings: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch,
		idx.client.baseURL+"/indexes/"+idx.name+"/settings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("meilikit: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/merge-patch+json")
	idx.client.setHeaders(ctx, req)

	resp, err := idx.client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("meilikit: update settings %q: %w", idx.name, err)
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

// WaitForTask polls until a task reaches a terminal status or context is canceled.
// Uses exponential backoff starting at 250ms, capped at 5s.
func (idx *Index) WaitForTask(ctx context.Context, taskUID int64) (*Task, error) {
	url := fmt.Sprintf("%s/tasks/%d", idx.client.baseURL, taskUID)
	delay := 250 * time.Millisecond
	const maxDelay = 5 * time.Second

	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("meilikit: wait for task: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("meilikit: build request: %w", err)
		}
		idx.client.setHeaders(ctx, req)

		resp, err := idx.client.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("meilikit: wait for task %d: %w", taskUID, err)
		}

		if resp.StatusCode >= 400 {
			err := decodeMeiliError(resp)
			resp.Body.Close()
			return nil, err
		}

		var task Task
		err = json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&task)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("meilikit: decode task %d: %w", taskUID, err)
		}

		switch task.Status {
		case "succeeded":
			return &task, nil
		case "failed":
			msg := "unknown error"
			if task.Error != nil {
				msg = task.Error.Message
			}
			return &task, fmt.Errorf("meilikit: task %d failed: %s", taskUID, msg)
		case "canceled":
			return &task, fmt.Errorf("meilikit: task %d was canceled", taskUID)
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("meilikit: wait for task: %w", ctx.Err())
		case <-timer.C:
		}

		// Exponential backoff, capped.
		delay = delay * 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}
