package xyops_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v10"
	"github.com/ai8future/chassis-go/v10/testkit"
	"github.com/ai8future/chassis-go/v10/xyops"
)

func init() { chassis.RequireMajor(10) }

// mux builds a handler that routes by method+path for the mock xyops API.
func mux() http.Handler {
	m := http.NewServeMux()

	m.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	})

	m.HandleFunc("POST /api/events/{eventID}/run", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"job_id":"job-42"}`))
	})

	m.HandleFunc("GET /api/jobs/{jobID}", func(w http.ResponseWriter, r *http.Request) {
		jobID := r.PathValue("jobID")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		resp := map[string]any{
			"id":       jobID,
			"event_id": "evt-1",
			"state":    "running",
			"progress": 50,
		}
		json.NewEncoder(w).Encode(resp)
	})

	m.HandleFunc("POST /api/jobs/{jobID}/cancel", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	m.HandleFunc("GET /api/jobs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": "job-search-1", "event_id": "evt-1", "state": "running", "progress": 90},
		})
	})

	m.HandleFunc("GET /api/events/{eventID}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]string{
			"id":   r.PathValue("eventID"),
			"name": "deploy",
		})
	})

	m.HandleFunc("POST /api/alerts/{alertID}/ack", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	m.HandleFunc("POST /api/webhooks/{hookID}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"received":true}`))
	})

	m.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`[{"id":"evt-1","name":"deploy"}]`))
	})

	m.HandleFunc("GET /api/alerts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`[{"id":"alert-1","message":"CPU high","state":"firing"}]`))
	})

	m.HandleFunc("POST /api/monitoring/push", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	})

	return m
}

func TestPing(t *testing.T) {
	srv := testkit.NewHTTPServer(t, mux())
	client := xyops.New(xyops.Config{
		BaseURL: srv.URL,
		APIKey:  "test-key",
	})
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	reqs := srv.Requests()
	if len(reqs) == 0 {
		t.Fatal("expected at least one request")
	}
	if reqs[0].Path != "/api/ping" {
		t.Fatalf("expected path /api/ping, got %s", reqs[0].Path)
	}
	if auth := reqs[0].Headers.Get("Authorization"); auth != "Bearer test-key" {
		t.Fatalf("expected Bearer test-key, got %q", auth)
	}
}

func TestRunEvent(t *testing.T) {
	srv := testkit.NewHTTPServer(t, mux())
	client := xyops.New(xyops.Config{
		BaseURL: srv.URL,
		APIKey:  "test-key",
	})
	jobID, err := client.RunEvent(context.Background(), "deploy-prod", map[string]string{"version": "1.0"})
	if err != nil {
		t.Fatalf("RunEvent: %v", err)
	}
	if jobID != "job-42" {
		t.Fatalf("expected job-42, got %q", jobID)
	}

	reqs := srv.Requests()
	found := false
	for _, r := range reqs {
		if r.Method == "POST" && r.Path == "/api/events/deploy-prod/run" {
			found = true
			var params map[string]string
			json.Unmarshal(r.Body, &params)
			if params["version"] != "1.0" {
				t.Fatalf("expected version=1.0 in body, got %v", params)
			}
		}
	}
	if !found {
		t.Fatal("expected POST /api/events/deploy-prod/run request")
	}
}

func TestGetJobStatusWithCaching(t *testing.T) {
	srv := testkit.NewHTTPServer(t, mux())
	client := xyops.New(xyops.Config{
		BaseURL: srv.URL,
		APIKey:  "test-key",
	})
	ctx := context.Background()

	// First call hits the server.
	status, err := client.GetJobStatus(ctx, "job-99")
	if err != nil {
		t.Fatalf("GetJobStatus (first): %v", err)
	}
	if status.ID != "job-99" {
		t.Fatalf("expected id job-99, got %q", status.ID)
	}
	if status.State != "running" {
		t.Fatalf("expected state running, got %q", status.State)
	}
	if status.Progress != 50 {
		t.Fatalf("expected progress 50, got %d", status.Progress)
	}

	firstReqCount := len(srv.Requests())

	// Second call should be served from cache — no additional request.
	status2, err := client.GetJobStatus(ctx, "job-99")
	if err != nil {
		t.Fatalf("GetJobStatus (cached): %v", err)
	}
	if status2.ID != "job-99" {
		t.Fatalf("cached: expected id job-99, got %q", status2.ID)
	}

	if len(srv.Requests()) != firstReqCount {
		t.Fatalf("expected cache hit (no new request), but request count went from %d to %d",
			firstReqCount, len(srv.Requests()))
	}
}

func TestFireWebhook(t *testing.T) {
	srv := testkit.NewHTTPServer(t, mux())
	client := xyops.New(xyops.Config{
		BaseURL: srv.URL,
		APIKey:  "test-key",
	})
	id, err := client.FireWebhook(context.Background(), "hook-1", map[string]string{"action": "deploy"})
	if err != nil {
		t.Fatalf("FireWebhook: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty delivery ID")
	}

	reqs := srv.Requests()
	found := false
	for _, r := range reqs {
		if r.Method == "POST" && r.Path == "/api/webhooks/hook-1" {
			found = true
			if sig := r.Headers.Get("X-Webhook-Signature"); sig == "" {
				t.Fatal("expected X-Webhook-Signature header on webhook request")
			}
		}
	}
	if !found {
		t.Fatal("expected POST /api/webhooks/hook-1 request")
	}
}

func TestClientConstruction(t *testing.T) {
	// Verify that New works with various option combos.
	client := xyops.New(xyops.Config{
		BaseURL:         "http://localhost:9999",
		APIKey:          "key",
		ServiceName:     "test-svc",
		MonitorEnabled:  true,
		MonitorInterval: 10,
	},
		xyops.WithMonitoring(15),
		xyops.BridgeMetric("gauge1", &stubGauge{v: 42.0}),
	)
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestMonitoringBridgeDisabled(t *testing.T) {
	// When monitoring is disabled, Run blocks until context is cancelled.
	client := xyops.New(xyops.Config{
		BaseURL: "http://localhost:9999",
		APIKey:  "key",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := client.Run(ctx)
	if err != nil {
		t.Fatalf("Run (disabled monitoring) should return nil, got %v", err)
	}
}

func TestMonitoringBridgeEnabled(t *testing.T) {
	srv := testkit.NewHTTPServer(t, mux())
	client := xyops.New(xyops.Config{
		BaseURL:     srv.URL,
		APIKey:      "key",
		ServiceName: "test-svc",
	},
		xyops.WithMonitoring(1),
		xyops.BridgeMetric("cpu", &stubGauge{v: 0.75}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	err := client.Run(ctx)
	if err != nil {
		t.Fatalf("Run (enabled monitoring) should return nil, got %v", err)
	}

	// Should have made at least one push request (Immediate + at least one tick).
	reqs := srv.Requests()
	pushCount := 0
	for _, r := range reqs {
		if r.Method == "POST" && r.Path == "/api/monitoring/push" {
			pushCount++
		}
	}
	if pushCount < 1 {
		t.Fatalf("expected at least 1 monitoring push, got %d", pushCount)
	}
}

func TestListEvents(t *testing.T) {
	srv := testkit.NewHTTPServer(t, mux())
	client := xyops.New(xyops.Config{
		BaseURL: srv.URL,
		APIKey:  "test-key",
	})
	events, err := client.ListEvents(context.Background())
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ID != "evt-1" {
		t.Fatalf("expected evt-1, got %q", events[0].ID)
	}
}

func TestListActiveAlerts(t *testing.T) {
	srv := testkit.NewHTTPServer(t, mux())
	client := xyops.New(xyops.Config{
		BaseURL: srv.URL,
		APIKey:  "test-key",
	})
	alerts, err := client.ListActiveAlerts(context.Background())
	if err != nil {
		t.Fatalf("ListActiveAlerts: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].State != "firing" {
		t.Fatalf("expected firing, got %q", alerts[0].State)
	}
}

func TestRawEscapeHatch(t *testing.T) {
	srv := testkit.NewHTTPServer(t, mux())
	client := xyops.New(xyops.Config{
		BaseURL: srv.URL,
		APIKey:  "test-key",
	})
	resp, err := client.Raw(context.Background(), "GET", "/api/ping", nil)
	if err != nil {
		t.Fatalf("Raw: %v", err)
	}
	var result map[string]string
	json.Unmarshal(resp, &result)
	if result["status"] != "ok" {
		t.Fatalf("expected status=ok, got %v", result)
	}
}

func TestCancelJobInvalidatesCache(t *testing.T) {
	srv := testkit.NewHTTPServer(t, mux())
	client := xyops.New(xyops.Config{
		BaseURL: srv.URL,
		APIKey:  "test-key",
	})
	ctx := context.Background()

	// Prime the cache.
	if _, err := client.GetJobStatus(ctx, "job-99"); err != nil {
		t.Fatalf("GetJobStatus (prime cache): %v", err)
	}
	if err := client.CancelJob(ctx, "job-99"); err != nil {
		t.Fatalf("CancelJob: %v", err)
	}

	// After cancel the cache should be cleared — next get should hit server.
	firstCount := len(srv.Requests())
	if _, err := client.GetJobStatus(ctx, "job-99"); err != nil {
		t.Fatalf("GetJobStatus (after cancel): %v", err)
	}

	var getCount, cancelCount int
	for _, r := range srv.Requests() {
		switch {
		case r.Method == "GET" && r.Path == "/api/jobs/job-99":
			getCount++
		case r.Method == "POST" && r.Path == "/api/jobs/job-99/cancel":
			cancelCount++
		}
	}
	if getCount != 2 {
		t.Fatalf("expected 2 GET /api/jobs/job-99 requests, got %d (total before=%d after=%d)", getCount, firstCount, len(srv.Requests()))
	}
	if cancelCount != 1 {
		t.Fatalf("expected 1 cancel request, got %d", cancelCount)
	}
}

func TestSearchJobs(t *testing.T) {
	srv := testkit.NewHTTPServer(t, mux())
	client := xyops.New(xyops.Config{
		BaseURL: srv.URL,
		APIKey:  "test-key",
	})

	jobs, err := client.SearchJobs(context.Background(), "status:running")
	if err != nil {
		t.Fatalf("SearchJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].ID != "job-search-1" {
		t.Fatalf("expected job-search-1, got %q", jobs[0].ID)
	}
}

func TestGetEvent(t *testing.T) {
	srv := testkit.NewHTTPServer(t, mux())
	client := xyops.New(xyops.Config{
		BaseURL: srv.URL,
		APIKey:  "test-key",
	})

	event, err := client.GetEvent(context.Background(), "evt-42")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if event.ID != "evt-42" {
		t.Fatalf("expected evt-42, got %q", event.ID)
	}
	if event.Name != "deploy" {
		t.Fatalf("expected deploy, got %q", event.Name)
	}
}

func TestAckAlert(t *testing.T) {
	srv := testkit.NewHTTPServer(t, mux())
	client := xyops.New(xyops.Config{
		BaseURL: srv.URL,
		APIKey:  "test-key",
	})

	if err := client.AckAlert(context.Background(), "alert-1"); err != nil {
		t.Fatalf("AckAlert: %v", err)
	}

	found := false
	for _, r := range srv.Requests() {
		if r.Method == "POST" && r.Path == "/api/alerts/alert-1/ack" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected POST /api/alerts/alert-1/ack request")
	}
}

// stubGauge implements MetricGauge for testing.
type stubGauge struct {
	v float64
}

func (g *stubGauge) Value() float64 { return g.v }
