//go:build integration

package inngestkit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/internal/integrationtest"
	"github.com/ai8future/chassis-go/v11/testkit"
	"github.com/inngest/inngestgo"
)

func TestInngestDevServerLiveIntegration(t *testing.T) {
	chassis.RequireMajor(11)
	integrationtest.Run(t, "inngest", func(t *testing.T) {
		image := integrationtest.LoadPinnedImage(t, "inngest")
		svc := startInngestDevServer(t, image)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		kit, err := New(Config{
			BaseURL:    svc.baseURL,
			AppID:      "chassis-inngest-live",
			EventKey:   "dev-dummy-key",
			SigningKey: testKey,
		})
		if err != nil {
			t.Fatalf("New with dummy dev credentials: %v", err)
		}
		ids, err := kit.Send(ctx, inngestgo.Event{
			Name: "chassis/inngest.live",
			Data: map[string]any{"source": "g005", "kind": "dev-server"},
		})
		if err != nil {
			t.Fatalf("Send to live dev server: %v", err)
		}
		if len(ids) != 1 || strings.TrimSpace(ids[0]) == "" {
			t.Fatalf("expected one live dev-server event id, got %v", ids)
		}
		t.Logf("CHASSIS_INNGEST_EVENT_ID:%s", ids[0])
	})
}

type inngestService struct {
	baseURL string
}

func startInngestDevServer(t *testing.T, image string) inngestService {
	t.Helper()
	integrationtest.RequireDocker(t, "inngest")
	port := freePort(t)
	name := "chassis-inngest-" + integrationNameSuffix()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	args := []string{
		"run", "-d", "--name", name, "--pull=missing",
		"-p", fmt.Sprintf("127.0.0.1:%d:8288", port),
		image,
		"inngest", "dev", "--no-discovery", "--host", "0.0.0.0",
	}
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("start inngest dev-server container with pinned image %s: %v\n%s", image, err, out)
	}
	t.Cleanup(func() { integrationtest.CleanupDocker(t, name, "inngest") })
	svc := inngestService{baseURL: fmt.Sprintf("http://127.0.0.1:%d", port)}
	integrationtest.WaitFor(t, 45*time.Second, func() (bool, string) {
		resp, err := http.Get(svc.baseURL + "/")
		if err != nil {
			return false, err.Error()
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Sprintf("status %d: %s", resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "Inngest") {
			return false, string(body)
		}
		return true, "dev server UI ready"
	})
	assertDevEventEndpoint(t, svc.baseURL)
	return svc
}

func assertDevEventEndpoint(t *testing.T, baseURL string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body := strings.NewReader(`[{"name":"chassis/inngest.probe","data":{"kind":"readiness"}}]`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/e/dev-dummy-key", body)
	if err != nil {
		t.Fatalf("build dev event probe: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("dev event endpoint probe: %v", err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dev event endpoint status %d: %s", resp.StatusCode, payload)
	}
	var parsed struct {
		IDs    []string `json:"ids"`
		Status int      `json:"status"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("decode dev event response %q: %v", payload, err)
	}
	if parsed.Status != http.StatusOK || len(parsed.IDs) != 1 || parsed.IDs[0] == "" {
		t.Fatalf("dev event response = %+v", parsed)
	}
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
