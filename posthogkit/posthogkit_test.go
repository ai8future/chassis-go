package posthogkit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/ai8future/chassis-go/v11/seal"
)

func init() {
	chassis.RequireMajor(11)
}

// batchRequest is the JSON structure sent to /batch.
type batchRequest struct {
	APIKey string `json:"api_key"`
	Batch  []struct {
		Type       string         `json:"type"`
		Event      string         `json:"event"`
		DistinctID string         `json:"distinct_id"`
		Properties map[string]any `json:"properties"`
		Set        map[string]any `json:"$set"`
		Timestamp  string         `json:"timestamp"`
	} `json:"batch"`
}

// --------------------------------------------------------------------------
// 1. No-op when disabled
// --------------------------------------------------------------------------

func TestCapture_Disabled(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := New(Config{
		APIKey:  "test-key",
		Host:    srv.URL,
		Enabled: false,
	})
	client.Capture("user-1", "page_view", nil)
	client.Identify("user-1", map[string]any{"email": "a@b.com"})
	client.GroupIdentify("company", "co-1", map[string]any{"name": "Acme"})
	client.CaptureWithGroups("user-1", "click", nil, map[string]string{"company": "co-1"})
	client.Close()

	if got := requests.Load(); got != 0 {
		t.Errorf("expected 0 requests when disabled, got %d", got)
	}
}

// --------------------------------------------------------------------------
// 2. Capture flushes correct batch payload on Close
// --------------------------------------------------------------------------

func TestCapture_BatchPayload(t *testing.T) {
	var received batchRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/batch" {
			t.Errorf("expected /batch, got %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type=application/json, got %q", r.Header.Get("Content-Type"))
		}
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := New(Config{
		APIKey:  "phc_test123",
		Host:    srv.URL,
		Enabled: true,
	})
	client.Capture("user-1", "page_view", map[string]any{"url": "/home"})
	client.Close()

	if received.APIKey != "phc_test123" {
		t.Errorf("expected api_key=phc_test123, got %q", received.APIKey)
	}
	if len(received.Batch) != 1 {
		t.Fatalf("expected 1 event in batch, got %d", len(received.Batch))
	}
	ev := received.Batch[0]
	if ev.Type != "capture" {
		t.Errorf("expected type=capture, got %q", ev.Type)
	}
	if ev.Event != "page_view" {
		t.Errorf("expected event=page_view, got %q", ev.Event)
	}
	if ev.DistinctID != "user-1" {
		t.Errorf("expected distinct_id=user-1, got %q", ev.DistinctID)
	}
	if ev.Properties["url"] != "/home" {
		t.Errorf("expected url=/home, got %v", ev.Properties["url"])
	}
	if ev.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}
}

// --------------------------------------------------------------------------
// 3. Multiple events batch together
// --------------------------------------------------------------------------

func TestCapture_MultipleBatch(t *testing.T) {
	var received batchRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := New(Config{
		APIKey:  "test-key",
		Host:    srv.URL,
		Enabled: true,
	})
	client.Capture("u1", "event_a", nil)
	client.Capture("u2", "event_b", map[string]any{"key": "val"})
	client.Capture("u3", "event_c", nil)
	client.Close()

	if len(received.Batch) != 3 {
		t.Fatalf("expected 3 events, got %d", len(received.Batch))
	}
	if received.Batch[0].DistinctID != "u1" {
		t.Errorf("expected first distinct_id=u1, got %q", received.Batch[0].DistinctID)
	}
	if received.Batch[1].DistinctID != "u2" {
		t.Errorf("expected second distinct_id=u2, got %q", received.Batch[1].DistinctID)
	}
	if received.Batch[2].DistinctID != "u3" {
		t.Errorf("expected third distinct_id=u3, got %q", received.Batch[2].DistinctID)
	}
}

// --------------------------------------------------------------------------
// 4. Identify sends $set payload
// --------------------------------------------------------------------------

func TestIdentify_Payload(t *testing.T) {
	var received batchRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := New(Config{
		APIKey:  "test-key",
		Host:    srv.URL,
		Enabled: true,
	})
	client.Identify("user-42", map[string]any{"email": "user@example.com", "plan": "pro"})
	client.Close()

	if len(received.Batch) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received.Batch))
	}
	ev := received.Batch[0]
	if ev.Type != "identify" {
		t.Errorf("expected type=identify, got %q", ev.Type)
	}
	if ev.Event != "$identify" {
		t.Errorf("expected event=$identify, got %q", ev.Event)
	}
	if ev.DistinctID != "user-42" {
		t.Errorf("expected distinct_id=user-42, got %q", ev.DistinctID)
	}
	if ev.Set["email"] != "user@example.com" {
		t.Errorf("expected $set.email=user@example.com, got %v", ev.Set["email"])
	}
	if ev.Set["plan"] != "pro" {
		t.Errorf("expected $set.plan=pro, got %v", ev.Set["plan"])
	}
}

// --------------------------------------------------------------------------
// 5. GroupIdentify sends $group_type, $group_key, $group_set
// --------------------------------------------------------------------------

func TestGroupIdentify_Payload(t *testing.T) {
	var received batchRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := New(Config{
		APIKey:  "test-key",
		Host:    srv.URL,
		Enabled: true,
	})
	client.GroupIdentify("company", "co-42", map[string]any{"name": "Acme Corp"})
	client.Close()

	if len(received.Batch) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received.Batch))
	}
	ev := received.Batch[0]
	if ev.Type != "capture" {
		t.Errorf("expected type=capture, got %q", ev.Type)
	}
	if ev.Event != "$groupidentify" {
		t.Errorf("expected event=$groupidentify, got %q", ev.Event)
	}
	if ev.DistinctID != "company_co-42" {
		t.Errorf("expected distinct_id=company_co-42, got %q", ev.DistinctID)
	}
	if ev.Properties["$group_type"] != "company" {
		t.Errorf("expected $group_type=company, got %v", ev.Properties["$group_type"])
	}
	if ev.Properties["$group_key"] != "co-42" {
		t.Errorf("expected $group_key=co-42, got %v", ev.Properties["$group_key"])
	}
	gs, ok := ev.Properties["$group_set"].(map[string]any)
	if !ok {
		t.Fatalf("expected $group_set to be a map, got %T", ev.Properties["$group_set"])
	}
	if gs["name"] != "Acme Corp" {
		t.Errorf("expected $group_set.name=Acme Corp, got %v", gs["name"])
	}
}

// --------------------------------------------------------------------------
// 6. CaptureWithGroups sends $groups in properties
// --------------------------------------------------------------------------

func TestCaptureWithGroups_Payload(t *testing.T) {
	var received batchRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := New(Config{
		APIKey:  "test-key",
		Host:    srv.URL,
		Enabled: true,
	})
	client.CaptureWithGroups("user-1", "feature_used", map[string]any{"feature": "search"},
		map[string]string{"company": "co-1", "team": "eng"})
	client.Close()

	if len(received.Batch) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received.Batch))
	}
	ev := received.Batch[0]
	if ev.Type != "capture" {
		t.Errorf("expected type=capture, got %q", ev.Type)
	}
	if ev.Event != "feature_used" {
		t.Errorf("expected event=feature_used, got %q", ev.Event)
	}
	if ev.Properties["feature"] != "search" {
		t.Errorf("expected feature=search, got %v", ev.Properties["feature"])
	}
	groups, ok := ev.Properties["$groups"].(map[string]any)
	if !ok {
		t.Fatalf("expected $groups to be a map, got %T", ev.Properties["$groups"])
	}
	if groups["company"] != "co-1" {
		t.Errorf("expected $groups.company=co-1, got %v", groups["company"])
	}
	if groups["team"] != "eng" {
		t.Errorf("expected $groups.team=eng, got %v", groups["team"])
	}
}

// --------------------------------------------------------------------------
// 7. CaptureWithGroups with nil props
// --------------------------------------------------------------------------

func TestCaptureWithGroups_NilProps(t *testing.T) {
	var received batchRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := New(Config{
		APIKey:  "test-key",
		Host:    srv.URL,
		Enabled: true,
	})
	client.CaptureWithGroups("user-1", "login", nil, map[string]string{"company": "co-1"})
	client.Close()

	if len(received.Batch) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received.Batch))
	}
	groups, ok := received.Batch[0].Properties["$groups"].(map[string]any)
	if !ok {
		t.Fatalf("expected $groups map, got %T", received.Batch[0].Properties["$groups"])
	}
	if groups["company"] != "co-1" {
		t.Errorf("expected company=co-1, got %v", groups["company"])
	}
}

// --------------------------------------------------------------------------
// 8. CaptureWithGroups does not mutate caller's props map
// --------------------------------------------------------------------------

func TestCaptureWithGroups_NoMutation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := New(Config{
		APIKey:  "test-key",
		Host:    srv.URL,
		Enabled: true,
	})
	props := map[string]any{"feature": "search"}
	client.CaptureWithGroups("u1", "click", props, map[string]string{"company": "co-1"})
	client.Close()

	if _, ok := props["$groups"]; ok {
		t.Error("CaptureWithGroups must not mutate the caller's props map")
	}
	if len(props) != 1 {
		t.Errorf("expected props to still have 1 key, got %d", len(props))
	}
}

// --------------------------------------------------------------------------
// 9. FlushSize triggers auto-flush
// --------------------------------------------------------------------------

func TestFlushSize_AutoFlush(t *testing.T) {
	flushed := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flushed <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := New(Config{
		APIKey:    "test-key",
		Host:      srv.URL,
		Enabled:   true,
		FlushSize: 2,
	})

	client.Capture("u1", "e1", nil)
	client.Capture("u1", "e2", nil) // should trigger auto-flush

	select {
	case <-flushed:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("expected auto-flush when buffer reached FlushSize")
	}
}

// --------------------------------------------------------------------------
// 10. Flush on empty buffer sends no request
// --------------------------------------------------------------------------

func TestFlush_Empty(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := New(Config{
		APIKey:  "test-key",
		Host:    srv.URL,
		Enabled: true,
	})

	err := client.flush(context.Background())
	if err != nil {
		t.Fatalf("expected nil error for empty flush, got: %v", err)
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("expected 0 requests for empty buffer, got %d", got)
	}
}

// --------------------------------------------------------------------------
// 11. Flush returns error on server error
// --------------------------------------------------------------------------

func TestFlush_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()

	client := New(Config{
		APIKey:  "test-key",
		Host:    srv.URL,
		Enabled: true,
	})
	client.Capture("u1", "event", nil)

	err := client.flush(context.Background())
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
	if got := err.Error(); got != `posthogkit: unexpected status 500: {"error":"rate limited"}` {
		t.Errorf("unexpected error: %q", got)
	}
}

// --------------------------------------------------------------------------
// 12. HashID with HMAC secret
// --------------------------------------------------------------------------

func TestHashID_WithSecret(t *testing.T) {
	client := New(Config{
		APIKey:     "test-key",
		Enabled:    true,
		HMACSecret: "my-secret-key",
	})

	hashed := client.HashID("user-42")
	expected := seal.Sign([]byte("user-42"), "my-secret-key")
	if hashed != expected {
		t.Errorf("expected HashID=%q, got %q", expected, hashed)
	}
	if hashed == "user-42" {
		t.Error("expected hashed ID to differ from raw ID")
	}
}

// --------------------------------------------------------------------------
// 13. HashID without secret returns raw ID
// --------------------------------------------------------------------------

func TestHashID_NoSecret(t *testing.T) {
	client := New(Config{
		APIKey:  "test-key",
		Enabled: true,
	})

	if got := client.HashID("user-42"); got != "user-42" {
		t.Errorf("expected raw ID user-42, got %q", got)
	}
}

// --------------------------------------------------------------------------
// 14. Flusher returns a non-nil tick-compatible function
// --------------------------------------------------------------------------

func TestFlusher_ReturnsFunction(t *testing.T) {
	client := New(Config{
		APIKey:  "test-key",
		Enabled: true,
	})
	fn := client.Flusher()
	if fn == nil {
		t.Fatal("expected non-nil Flusher function")
	}
}

// --------------------------------------------------------------------------
// 15. New applies defaults
// --------------------------------------------------------------------------

func TestNew_Defaults(t *testing.T) {
	client := New(Config{APIKey: "key", Enabled: true})
	if client.cfg.Host != "https://us.i.posthog.com" {
		t.Errorf("expected default host, got %q", client.cfg.Host)
	}
	if client.cfg.FlushInterval != 30*time.Second {
		t.Errorf("expected default flush interval 30s, got %v", client.cfg.FlushInterval)
	}
	if client.cfg.FlushSize != 100 {
		t.Errorf("expected default flush size 100, got %d", client.cfg.FlushSize)
	}
}

// --------------------------------------------------------------------------
// 16. Negative FlushSize/FlushInterval get safe defaults
// --------------------------------------------------------------------------

func TestNew_NegativeConfig(t *testing.T) {
	client := New(Config{
		APIKey:        "key",
		Enabled:       true,
		FlushSize:     -5,
		FlushInterval: -1 * time.Second,
	})
	if client.cfg.FlushSize != 100 {
		t.Errorf("expected FlushSize=100 for negative input, got %d", client.cfg.FlushSize)
	}
	if client.cfg.FlushInterval != 30*time.Second {
		t.Errorf("expected FlushInterval=30s for negative input, got %v", client.cfg.FlushInterval)
	}
}

// --------------------------------------------------------------------------
// 17. Concurrent Capture is safe
// --------------------------------------------------------------------------

func TestCapture_Concurrent(t *testing.T) {
	var batches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		batches.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := New(Config{
		APIKey:    "test-key",
		Host:      srv.URL,
		Enabled:   true,
		FlushSize: 2000, // larger than total events (50*20=1000) to prevent auto-flush
	})

	const goroutines = 50
	done := make(chan struct{})
	for i := range goroutines {
		go func(n int) {
			for j := range 20 {
				client.Capture("user", "event", map[string]any{"i": n, "j": j})
			}
			done <- struct{}{}
		}(i)
	}
	for range goroutines {
		<-done
	}
	client.Close()

	if got := batches.Load(); got != 1 {
		t.Errorf("expected 1 batch flush, got %d", got)
	}
}

// --------------------------------------------------------------------------
// 18. Host with trailing slash produces correct URL
// --------------------------------------------------------------------------

func TestFlush_HostTrailingSlash(t *testing.T) {
	var requestPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := New(Config{
		APIKey:  "test-key",
		Host:    srv.URL + "/",
		Enabled: true,
	})
	client.Capture("u1", "e1", nil)
	client.Close()

	if requestPath != "/batch" {
		t.Errorf("expected /batch, got %q", requestPath)
	}
}

// --------------------------------------------------------------------------
// 19. Double Close is safe
// --------------------------------------------------------------------------

func TestClose_Double(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := New(Config{
		APIKey:  "test-key",
		Host:    srv.URL,
		Enabled: true,
	})
	client.Capture("u1", "e1", nil)
	client.Close()
	client.Close() // must not panic or send empty batch
}

// --------------------------------------------------------------------------
// 20. Flusher tick integration
// --------------------------------------------------------------------------

func TestFlusher_FlushesOnTick(t *testing.T) {
	flushed := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case flushed <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := New(Config{
		APIKey:        "test-key",
		Host:          srv.URL,
		Enabled:       true,
		FlushInterval: 50 * time.Millisecond,
	})
	client.Capture("u1", "e1", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Flusher()(ctx)

	select {
	case <-flushed:
		// tick fired and flushed
	case <-time.After(2 * time.Second):
		t.Fatal("expected Flusher to flush within tick interval")
	}
	cancel()
}

// --------------------------------------------------------------------------
// 21. Flush server error with empty body
// --------------------------------------------------------------------------

func TestFlush_ServerErrorEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	client := New(Config{
		APIKey:  "test-key",
		Host:    srv.URL,
		Enabled: true,
	})
	client.Capture("u1", "event", nil)

	err := client.flush(context.Background())
	if err == nil {
		t.Fatal("expected error for 502, got nil")
	}
	if got := err.Error(); got != "posthogkit: unexpected status 502" {
		t.Errorf("expected 'posthogkit: unexpected status 502', got %q", got)
	}
}

// --------------------------------------------------------------------------
// 22. Health check — healthy when enabled with API key
// --------------------------------------------------------------------------

func TestCheck_Healthy(t *testing.T) {
	client := New(Config{
		APIKey:  "phc_test",
		Enabled: true,
	})
	check := client.Check()
	if err := check(context.Background()); err != nil {
		t.Errorf("expected healthy, got: %v", err)
	}
}

// --------------------------------------------------------------------------
// 23. Health check — healthy when disabled (no-op)
// --------------------------------------------------------------------------

func TestCheck_Disabled(t *testing.T) {
	client := New(Config{
		Enabled: false,
	})
	check := client.Check()
	if err := check(context.Background()); err != nil {
		t.Errorf("expected healthy when disabled, got: %v", err)
	}
}

// --------------------------------------------------------------------------
// 24. Health check — unhealthy when enabled without API key
// --------------------------------------------------------------------------

func TestCheck_MissingAPIKey(t *testing.T) {
	client := New(Config{
		Enabled: true,
	})
	check := client.Check()
	err := check(context.Background())
	if err == nil {
		t.Fatal("expected error for missing API key, got nil")
	}
	if got := err.Error(); got != "posthogkit: enabled but POSTHOG_API_KEY is empty" {
		t.Errorf("unexpected error: %q", got)
	}
}
