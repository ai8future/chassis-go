//go:build integration

package otel_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/internal/integrationtest"
	chassisotel "github.com/ai8future/chassis-go/v11/otel"
	"github.com/ai8future/chassis-go/v11/testkit"
	"go.opentelemetry.io/otel"
)

func TestOTelCollectorContribLiveIntegration(t *testing.T) {
	chassis.RequireMajor(11)
	integrationtest.Run(t, "otel-collector", func(t *testing.T) {
		image := integrationtest.LoadPinnedImage(t, "otel-collector")
		svc := startCollector(t, image)
		shutdown := chassisotel.Init(chassisotel.Config{
			ServiceName:    "chassis-otel-live",
			ServiceVersion: "g005",
			Endpoint:       fmt.Sprintf("127.0.0.1:%d", svc.otlpPort),
			Insecure:       true,
			Sampler:        chassisotel.AlwaysSample(),
		})

		ctx, span := otel.Tracer("chassis-go/otel-integration").Start(context.Background(), "chassis.otel.live.span")
		counter, err := otel.Meter("chassis-go/otel-integration").Int64Counter("chassis_otel_live_metric")
		if err != nil {
			t.Fatalf("create counter: %v", err)
		}
		counter.Add(ctx, 1)
		span.End()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := shutdown(shutdownCtx); err != nil {
			t.Fatalf("shutdown/flush telemetry: %v", err)
		}

		receipt := parseCollectorReceipt(t, svc.receiptsDir)
		if !receipt.TraceSeen || !receipt.MetricSeen || !receipt.TraceServiceSeen || !receipt.MetricServiceSeen {
			t.Fatalf("collector receipt missing expected telemetry: %+v", receipt)
		}
		path := filepath.Join(svc.receiptsDir, "receipt.json")
		data, err := json.MarshalIndent(receipt, "", "  ")
		if err != nil {
			t.Fatalf("marshal receipt: %v", err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("write receipt artifact: %v", err)
		}
		t.Logf("CHASSIS_OTEL_RECEIPT:%s", path)
	})
}

type collectorService struct {
	container   string
	healthURL   string
	otlpPort    int
	receiptsDir string
}

func startCollector(t *testing.T, image string) collectorService {
	t.Helper()
	integrationtest.RequireDocker(t, "otel-collector")
	otlpPort := freePort(t)
	healthPort := freePort(t)
	receiptsDir := prepareCollectorReceiptsDir(t)
	name := "chassis-otel-" + integrationNameSuffix()
	configPath := filepath.Join(repoRoot(t), "otel", "testdata", "collector-config.yaml")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	args := []string{
		"run", "-d", "--name", name, "--pull=missing",
		"-p", fmt.Sprintf("127.0.0.1:%d:4317", otlpPort),
		"-p", fmt.Sprintf("127.0.0.1:%d:13133", healthPort),
		"-v", configPath + ":/etc/otelcol-contrib/config.yaml:ro",
		"-v", receiptsDir + ":/receipts",
		image,
		"--config=/etc/otelcol-contrib/config.yaml",
	}
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("start otel collector container with pinned image %s: %v\n%s", image, err, out)
	}
	t.Cleanup(func() { integrationtest.CleanupDocker(t, name, "otel-collector") })
	svc := collectorService{
		container:   name,
		healthURL:   fmt.Sprintf("http://127.0.0.1:%d/", healthPort),
		otlpPort:    otlpPort,
		receiptsDir: receiptsDir,
	}
	integrationtest.WaitFor(t, 45*time.Second, func() (bool, string) {
		resp, err := http.Get(svc.healthURL)
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

func prepareCollectorReceiptsDir(t *testing.T) string {
	t.Helper()
	dir := strings.TrimSpace(os.Getenv("CHASSIS_OTEL_RECEIPT_DIR"))
	if dir == "" {
		dir = t.TempDir()
	} else {
		if !filepath.IsAbs(dir) {
			t.Fatalf("CHASSIS_OTEL_RECEIPT_DIR must be absolute, got %q", dir)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create collector receipt directory: %v", err)
		}
	}

	// The pinned collector runs as UID/GID 10001. Limit cross-user write access
	// to this dedicated bind-mount directory and restore host-only permissions
	// after the container has been removed.
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("make collector receipt directory writable: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Errorf("restore collector receipt directory permissions: %v", err)
		}
	})
	return dir
}

type collectorReceipt struct {
	TraceSeen         bool `json:"trace_seen"`
	MetricSeen        bool `json:"metric_seen"`
	TraceServiceSeen  bool `json:"trace_service_seen"`
	MetricServiceSeen bool `json:"metric_service_seen"`
}

func parseCollectorReceipt(t *testing.T, dir string) collectorReceipt {
	t.Helper()
	var receipt collectorReceipt
	integrationtest.WaitFor(t, 15*time.Second, func() (bool, string) {
		traceDocs, traceErr := readJSONDocuments(filepath.Join(dir, "traces.json"))
		metricDocs, metricErr := readJSONDocuments(filepath.Join(dir, "metrics.json"))
		if traceErr == nil {
			receipt.TraceSeen = documentsContain(traceDocs, "chassis.otel.live.span")
			receipt.TraceServiceSeen = documentsContain(traceDocs, "service.name") && documentsContain(traceDocs, "chassis-otel-live") && documentsContain(traceDocs, "service.version") && documentsContain(traceDocs, "g005")
		}
		if metricErr == nil {
			receipt.MetricSeen = documentsContain(metricDocs, "chassis_otel_live_metric")
			receipt.MetricServiceSeen = documentsContain(metricDocs, "service.name") && documentsContain(metricDocs, "chassis-otel-live") && documentsContain(metricDocs, "service.version") && documentsContain(metricDocs, "g005")
		}
		if receipt.TraceSeen && receipt.MetricSeen && receipt.TraceServiceSeen && receipt.MetricServiceSeen {
			return true, "receipt complete"
		}
		return false, fmt.Sprintf("traceErr=%v metricErr=%v receipt=%+v", traceErr, metricErr, receipt)
	})
	return receipt
}

func readJSONDocuments(path string) ([]any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	dec := json.NewDecoder(file)
	var docs []any
	for dec.More() {
		var doc any
		if err := dec.Decode(&doc); err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("%s contained no JSON documents", path)
	}
	return docs, nil
}

func documentsContain(docs []any, needle string) bool {
	for _, doc := range docs {
		if valueContains(doc, needle) {
			return true
		}
	}
	return false
}

func valueContains(v any, needle string) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if k == needle || valueContains(v, needle) {
				return true
			}
		}
	case []any:
		for _, v := range x {
			if valueContains(v, needle) {
				return true
			}
		}
	case string:
		return x == needle || strings.Contains(x, needle)
	}
	return false
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
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
