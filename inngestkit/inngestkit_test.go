package inngestkit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	chassis "github.com/ai8future/chassis-go/v11"
	"github.com/inngest/inngestgo"
)

func init() {
	// Satisfy chassis version check for all tests.
	chassis.ResetVersionCheck()
	chassis.RequireMajor(11)
}

// --------------------------------------------------------------------------
// Config validation
// --------------------------------------------------------------------------

func TestValidateConfig_Valid(t *testing.T) {
	cfg := Config{
		BaseURL:    "http://inngest.lan:8288",
		AppID:      "test-app",
		EventKey:   "abc123",
		SigningKey: "ce2293cb10997f4516b045c4d869ed733e78ad9a48dd1f16a3690376a4c10577",
		ServePath:  "/api/inngest",
	}
	if err := validateConfig(cfg); err != nil {
		t.Fatalf("expected valid config, got: %v", err)
	}
}

func TestValidateConfig_MissingBaseURL(t *testing.T) {
	cfg := Config{AppID: "x", EventKey: "x", SigningKey: "aabb"}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("expected error for missing BaseURL")
	}
}

func TestValidateConfig_BadBaseURL(t *testing.T) {
	cfg := Config{BaseURL: "ftp://bad", AppID: "x", EventKey: "x", SigningKey: "aabb"}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("expected error for non-http BaseURL")
	}
}

func TestValidateConfig_MissingAppID(t *testing.T) {
	cfg := Config{BaseURL: "http://x", EventKey: "x", SigningKey: "aabb"}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("expected error for missing AppID")
	}
}

func TestValidateConfig_MissingEventKey(t *testing.T) {
	cfg := Config{BaseURL: "http://x", AppID: "x", SigningKey: "aabb"}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("expected error for missing EventKey")
	}
}

func TestValidateConfig_MissingSigningKey(t *testing.T) {
	cfg := Config{BaseURL: "http://x", AppID: "x", EventKey: "x"}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("expected error for missing SigningKey")
	}
}

func TestValidateConfig_OddLengthSigningKey(t *testing.T) {
	cfg := Config{BaseURL: "http://x", AppID: "x", EventKey: "x", SigningKey: "abc"}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("expected error for odd-length SigningKey")
	}
}

func TestValidateConfig_NonHexSigningKey(t *testing.T) {
	cfg := Config{BaseURL: "http://x", AppID: "x", EventKey: "x", SigningKey: "zzzz"}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("expected error for non-hex SigningKey")
	}
}

func TestValidateConfig_BadServePath(t *testing.T) {
	cfg := Config{
		BaseURL:   "http://x",
		AppID:     "x",
		EventKey:  "x",
		SigningKey: "aabb",
		ServePath: "no-leading-slash",
	}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("expected error for ServePath without leading /")
	}
}

func TestValidateConfig_FallbackValidation(t *testing.T) {
	cfg := Config{
		BaseURL:            "http://x",
		AppID:              "x",
		EventKey:           "x",
		SigningKey:         "aabb",
		SigningKeyFallback: "xyz", // odd length, invalid hex
	}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("expected error for invalid fallback key")
	}
}

// --------------------------------------------------------------------------
// New constructor
// --------------------------------------------------------------------------

func TestNew_ValidConfig(t *testing.T) {
	cfg := Config{
		BaseURL:    "http://inngest.lan:8288",
		AppID:      "test-app",
		EventKey:   "testkey",
		SigningKey: "aabbccdd",
	}
	kit, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if kit.Client() == nil {
		t.Fatal("Client() returned nil")
	}
}

func TestNew_InvalidConfig(t *testing.T) {
	cfg := Config{} // all empty
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for empty config")
	}
}

func TestNew_DefaultServePath(t *testing.T) {
	cfg := Config{
		BaseURL:    "http://inngest.lan:8288",
		AppID:      "test-app",
		EventKey:   "testkey",
		SigningKey: "aabbccdd",
	}
	kit, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if kit.cfg.ServePath != "/api/inngest" {
		t.Fatalf("expected default ServePath /api/inngest, got %q", kit.cfg.ServePath)
	}
}

// --------------------------------------------------------------------------
// Mount
// --------------------------------------------------------------------------

func TestMount_RegistersHandler(t *testing.T) {
	cfg := Config{
		BaseURL:    "http://localhost:8288",
		AppID:      "mount-test",
		EventKey:   "testkey",
		SigningKey: "aabbccdd",
		ServePath:  "/api/inngest",
	}
	kit, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	mux := http.NewServeMux()
	kit.Mount(mux)

	// The handler should respond to GET with an introspection response.
	req := httptest.NewRequest("GET", "/api/inngest", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if _, ok := resp["function_count"]; !ok {
		t.Fatal("expected function_count in introspection response")
	}
}

// --------------------------------------------------------------------------
// Send
// --------------------------------------------------------------------------

func TestSend_EmptyEvents(t *testing.T) {
	cfg := Config{
		BaseURL:    "http://localhost:8288",
		AppID:      "send-test",
		EventKey:   "testkey",
		SigningKey: "aabbccdd",
	}
	kit, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ids, err := kit.Send(context.Background())
	if err != nil {
		t.Fatalf("Send with no events should not error: %v", err)
	}
	if ids != nil {
		t.Fatalf("expected nil ids, got %v", ids)
	}
}

func TestSend_SingleEvent_AgainstMock(t *testing.T) {
	// Mock inngest event API
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ids":    []string{"evt-001"},
			"status": 200,
		})
	}))
	defer srv.Close()

	cfg := Config{
		BaseURL:    srv.URL,
		AppID:      "send-test",
		EventKey:   "testkey",
		SigningKey: "aabbccdd",
	}
	kit, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ids, err := kit.Send(context.Background(), inngestgo.Event{
		Name: "test/event",
		Data: map[string]any{"hello": "world"},
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if len(ids) != 1 || ids[0] != "evt-001" {
		t.Fatalf("expected [evt-001], got %v", ids)
	}
}

func TestSend_BatchEvents_AgainstMock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ids":    []string{"evt-001", "evt-002"},
			"status": 200,
		})
	}))
	defer srv.Close()

	cfg := Config{
		BaseURL:    srv.URL,
		AppID:      "batch-test",
		EventKey:   "testkey",
		SigningKey: "aabbccdd",
	}
	kit, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ids, err := kit.Send(context.Background(),
		inngestgo.Event{Name: "test/a", Data: map[string]any{"n": 1}},
		inngestgo.Event{Name: "test/b", Data: map[string]any{"n": 2}},
	)
	if err != nil {
		t.Fatalf("Send batch failed: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids, got %v", ids)
	}
}
