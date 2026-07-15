//go:build integration

package meilikit_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/internal/integrationtest"
	"github.com/ai8future/chassis-go/v11/meilikit"
	"github.com/ai8future/chassis-go/v11/testkit"
)

func TestMeiliLiveIntegration(t *testing.T) {
	chassis.RequireMajor(11)
	integrationtest.Run(t, "meilisearch", func(t *testing.T) {
		image := integrationtest.LoadPinnedImage(t, "meilisearch")
		svc := startMeili(t, image)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		client, err := meilikit.New(meilikit.Config{BaseURL: svc.baseURL, Timeout: 5 * time.Second})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if err := client.Ping(ctx); err != nil {
			t.Fatalf("Ping: %v", err)
		}
		indexName := "chassis_meili_" + integrationNameSuffix()
		idx, err := client.Index(indexName)
		if err != nil {
			t.Fatalf("Index: %v", err)
		}
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cleanupCancel()
			if task, err := idx.DeleteIndex(cleanupCtx); err == nil && task != nil {
				_, _ = idx.WaitForTask(cleanupCtx, task.TaskUID)
			}
		})
		if err := idx.Configure(ctx, meilikit.IndexConfig{PrimaryKey: "id", Searchable: []string{"title", "body"}, Filterable: []string{"kind"}}); err != nil {
			t.Fatalf("Configure: %v", err)
		}
		task, err := idx.AddDocuments(ctx, []any{
			map[string]any{"id": "doc-1", "title": "Chassis live adapter", "body": "qdrant meili otel", "kind": "guide"},
			map[string]any{"id": "doc-2", "title": "Other note", "body": "unrelated", "kind": "note"},
		})
		if err != nil {
			t.Fatalf("AddDocuments: %v", err)
		}
		if _, err := idx.WaitForTask(ctx, task.TaskUID); err != nil {
			t.Fatalf("WaitForTask add: %v", err)
		}
		result, err := idx.Search(ctx, "adapter", meilikit.SearchOptions{Limit: 5, Filter: "kind = guide"})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		hits, err := meilikit.SearchHits[map[string]any](result)
		if err != nil {
			t.Fatalf("SearchHits: %v", err)
		}
		if len(hits) != 1 || hits[0]["id"] != "doc-1" {
			t.Fatalf("hits = %#v", hits)
		}
		doc, err := idx.GetDocument(ctx, "doc-1")
		if err != nil {
			t.Fatalf("GetDocument: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(doc, &got); err != nil || got["title"] != "Chassis live adapter" {
			t.Fatalf("document = %#v err=%v", got, err)
		}
		deleteDoc, err := idx.DeleteDocument(ctx, "doc-2")
		if err != nil {
			t.Fatalf("DeleteDocument: %v", err)
		}
		if _, err := idx.WaitForTask(ctx, deleteDoc.TaskUID); err != nil {
			t.Fatalf("WaitForTask delete doc: %v", err)
		}
		if doc, err := idx.GetDocument(ctx, "doc-2"); err != nil || doc != nil {
			t.Fatalf("deleted document = %s err=%v", doc, err)
		}
		if _, err := idx.Search(ctx, "x", meilikit.SearchOptions{Filter: "unknown = nope"}); err == nil || !isMeiliBadRequest(err) {
			t.Fatalf("expected bad request MeiliError, got %T %v", err, err)
		}
		deleteIndex, err := idx.DeleteIndex(ctx)
		if err != nil {
			t.Fatalf("DeleteIndex: %v", err)
		}
		if _, err := idx.WaitForTask(ctx, deleteIndex.TaskUID); err != nil {
			t.Fatalf("WaitForTask delete index: %v", err)
		}
		if _, err := idx.Search(ctx, "adapter", meilikit.SearchOptions{}); err == nil {
			t.Fatal("expected deleted index search error")
		}
	})
}

func isMeiliBadRequest(err error) bool {
	var me *meilikit.MeiliError
	return errors.As(err, &me) && me.StatusCode == http.StatusBadRequest && me.Code != ""
}

type meiliService struct{ container, baseURL string }

func startMeili(t *testing.T, image string) meiliService {
	t.Helper()
	integrationtest.RequireDocker(t, "meilisearch")
	port := freePort(t)
	name := "chassis-meili-" + integrationNameSuffix()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	args := []string{"run", "-d", "--name", name, "--pull=missing", "-e", "MEILI_NO_ANALYTICS=true", "-p", fmt.Sprintf("127.0.0.1:%d:7700", port), image}
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("start meilisearch container with pinned image %s: %v\n%s", image, err, out)
	}
	t.Cleanup(func() { integrationtest.CleanupDocker(t, name, "meilisearch") })
	svc := meiliService{container: name, baseURL: fmt.Sprintf("http://127.0.0.1:%d", port)}
	integrationtest.WaitFor(t, 60*time.Second, func() (bool, string) {
		resp, err := http.Get(svc.baseURL + "/health")
		if err != nil {
			return false, err.Error()
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Sprintf("status %d: %s", resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "available") {
			return false, string(body)
		}
		return true, string(body)
	})
	return svc
}

func freePort(t *testing.T) int {
	t.Helper()
	port, err := testkit.GetFreePort()
	if err != nil {
		t.Fatalf("allocate free port: %v", err)
	}
	return port
}
func integrationNameSuffix() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }
