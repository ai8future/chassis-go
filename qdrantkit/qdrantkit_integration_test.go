//go:build integration

package qdrantkit_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/internal/integrationtest"
	"github.com/ai8future/chassis-go/v11/qdrantkit"
	"github.com/ai8future/chassis-go/v11/testkit"
)

func TestQdrantLiveIntegration(t *testing.T) {
	chassis.RequireMajor(11)
	integrationtest.Run(t, "qdrant", func(t *testing.T) {
		image := integrationtest.LoadPinnedImage(t, "qdrant")
		svc := startQdrant(t, image)
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		client := qdrantkit.New(qdrantkit.Config{BaseURL: svc.baseURL, Timeout: 5 * time.Second})

		collection := "chassis_qdrant_" + integrationNameSuffix()
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cleanupCancel()
			_ = client.DeleteCollection(cleanupCtx, collection)
		})

		if err := client.Ping(ctx); err != nil {
			t.Fatalf("Ping: %v", err)
		}
		if err := client.CreateCollection(ctx, collection, qdrantkit.CollectionConfig{Dimension: 3, Distance: qdrantkit.Cosine}); err != nil {
			t.Fatalf("CreateCollection: %v", err)
		}
		info, err := client.GetCollection(ctx, collection)
		if err != nil {
			t.Fatalf("GetCollection: %v", err)
		}
		if info == nil || info.Status == "" {
			t.Fatalf("collection info not populated: %#v", info)
		}
		if err := client.Upsert(ctx, collection, []qdrantkit.Point{
			{ID: "11111111-1111-4111-8111-111111111111", Vector: []float32{0.9, 0.1, 0.1}, Payload: map[string]any{"kind": "letter", "rank": 1}},
			{ID: "22222222-2222-4222-8222-222222222222", Vector: []float32{0.1, 0.9, 0.1}, Payload: map[string]any{"kind": "letter", "rank": 2}},
		}); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		results, err := client.Search(ctx, collection, []float32{0.88, 0.10, 0.12}, qdrantkit.SearchOptions{Limit: 1, WithPayload: true, WithVector: true})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(results) != 1 || results[0].ID != "11111111-1111-4111-8111-111111111111" || results[0].Payload["kind"] != "letter" || len(results[0].Vector) != 3 {
			t.Fatalf("search results = %#v", results)
		}
		vectors, err := client.GetVectors(ctx, collection, []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"})
		if err != nil {
			t.Fatalf("GetVectors: %v", err)
		}
		if len(vectors["11111111-1111-4111-8111-111111111111"]) != 3 || len(vectors["22222222-2222-4222-8222-222222222222"]) != 3 {
			t.Fatalf("vectors = %#v", vectors)
		}
		if err := client.Delete(ctx, collection, []string{"22222222-2222-4222-8222-222222222222"}); err != nil {
			t.Fatalf("Delete points: %v", err)
		}
		vectors, err = client.GetVectors(ctx, collection, []string{"22222222-2222-4222-8222-222222222222"})
		if err != nil {
			t.Fatalf("GetVectors after delete: %v", err)
		}
		if len(vectors) != 0 {
			t.Fatalf("deleted vector still returned: %#v", vectors)
		}
		if _, err := client.Search(ctx, collection+"_missing", []float32{0, 0, 1}, qdrantkit.SearchOptions{Limit: 1}); err == nil || !strings.Contains(err.Error(), "status") {
			t.Fatalf("expected missing collection search error, got %v", err)
		}
		if err := client.CreateCollection(ctx, collection+"_bad", qdrantkit.CollectionConfig{Dimension: 0}); err == nil || !strings.Contains(err.Error(), "status") {
			t.Fatalf("expected bad collection create error, got %v", err)
		}
		if err := client.DeleteCollection(ctx, collection); err != nil {
			t.Fatalf("DeleteCollection: %v", err)
		}
		info, err = client.GetCollection(ctx, collection)
		if err != nil {
			t.Fatalf("GetCollection after DeleteCollection: %v", err)
		}
		if info != nil {
			t.Fatalf("collection still exists after delete: %#v", info)
		}
	})
}

type qdrantService struct{ container, baseURL string }

func startQdrant(t *testing.T, image string) qdrantService {
	t.Helper()
	integrationtest.RequireDocker(t, "qdrant")
	port := freePort(t)
	name := "chassis-qdrant-" + integrationNameSuffix()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	args := []string{"run", "-d", "--name", name, "--pull=missing", "-p", fmt.Sprintf("127.0.0.1:%d:6333", port), image}
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("start qdrant container with pinned image %s: %v\n%s", image, err, out)
	}
	t.Cleanup(func() { integrationtest.CleanupDocker(t, name, "qdrant") })
	svc := qdrantService{container: name, baseURL: fmt.Sprintf("http://127.0.0.1:%d", port)}
	integrationtest.WaitFor(t, 60*time.Second, func() (bool, string) {
		resp, err := http.Get(svc.baseURL + "/collections")
		if err != nil {
			return false, err.Error()
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Sprintf("status %d: %s", resp.StatusCode, body)
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

func integrationNameSuffix() string {
	return strings.ReplaceAll(fmt.Sprintf("%d", time.Now().UnixNano()), ".", "-")
}
