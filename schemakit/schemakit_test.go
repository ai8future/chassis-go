package schemakit_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ai8future/chassis-go/v9/schemakit"
)

func TestNewRegistry(t *testing.T) {
	r, err := schemakit.NewRegistry("http://localhost:8081")
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil registry")
	}
}

func TestGetSchema_NotFound(t *testing.T) {
	r, err := schemakit.NewRegistry("http://localhost:8081")
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	s := r.GetSchema("nonexistent.subject")
	if s != nil {
		t.Fatalf("expected nil for unknown subject, got %+v", s)
	}
}

func TestLoadSchemas(t *testing.T) {
	r, err := schemakit.NewRegistry("http://localhost:8081")
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	err = r.LoadSchemas("testdata/schemas")
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}

	// The schema subject key should be namespace.name = "ai8.scanner.gdelt.v1.SignalSurge"
	s := r.GetSchema("ai8.scanner.gdelt.v1.SignalSurge")
	if s == nil {
		t.Fatal("expected to find schema by subject ai8.scanner.gdelt.v1.SignalSurge")
	}
	if s.Subject != "ai8.scanner.gdelt.v1.SignalSurge" {
		t.Fatalf("expected subject ai8.scanner.gdelt.v1.SignalSurge, got %s", s.Subject)
	}
	if s.Version != 1 {
		t.Fatalf("expected version 1, got %d", s.Version)
	}
	if s.AvroJSON == "" {
		t.Fatal("expected non-empty AvroJSON")
	}
	if s.SchemaID != 0 {
		t.Fatalf("expected SchemaID 0 before registration, got %d", s.SchemaID)
	}
}

func TestValidate_Valid(t *testing.T) {
	r, err := schemakit.NewRegistry("http://localhost:8081")
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if err := r.LoadSchemas("testdata/schemas"); err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}

	s := r.GetSchema("ai8.scanner.gdelt.v1.SignalSurge")
	if s == nil {
		t.Fatal("schema not found")
	}

	data := map[string]any{
		"entity":         "AAPL",
		"tier":           "flash",
		"kind":           "volume_spike",
		"current":        float32(150.5),
		"baseline":       float32(50.0),
		"multiplier":     float32(3.01),
		"window_minutes": 15,
	}

	err = r.Validate(s, data)
	if err != nil {
		t.Fatalf("Validate returned error for valid data: %v", err)
	}
}

func TestValidate_Invalid(t *testing.T) {
	r, err := schemakit.NewRegistry("http://localhost:8081")
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if err := r.LoadSchemas("testdata/schemas"); err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}

	s := r.GetSchema("ai8.scanner.gdelt.v1.SignalSurge")
	if s == nil {
		t.Fatal("schema not found")
	}

	// Missing required field "entity"
	data := map[string]any{
		"tier":           "flash",
		"kind":           "volume_spike",
		"current":        float32(150.5),
		"baseline":       float32(50.0),
		"multiplier":     float32(3.01),
		"window_minutes": 15,
	}

	err = r.Validate(s, data)
	if err == nil {
		t.Fatal("expected error for invalid data (missing required field)")
	}
}

func TestSerializeDeserialize(t *testing.T) {
	r, err := schemakit.NewRegistry("http://localhost:8081")
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if err := r.LoadSchemas("testdata/schemas"); err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}

	s := r.GetSchema("ai8.scanner.gdelt.v1.SignalSurge")
	if s == nil {
		t.Fatal("schema not found")
	}
	// Set a schema ID so serialize can prepend the header
	s.SchemaID = 42

	data := map[string]any{
		"entity":         "AAPL",
		"tier":           "flash",
		"kind":           "volume_spike",
		"current":        float32(150.5),
		"baseline":       float32(50.0),
		"multiplier":     float32(3.01),
		"window_minutes": 15,
	}

	raw, err := r.Serialize(s, data)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	// Verify wire format: first byte is 0x00, next 4 bytes are schema ID (big-endian)
	if len(raw) < 5 {
		t.Fatalf("expected at least 5 bytes, got %d", len(raw))
	}
	if raw[0] != 0x00 {
		t.Fatalf("expected magic byte 0x00, got 0x%02x", raw[0])
	}
	// Schema ID 42 in big-endian: 0x00 0x00 0x00 0x2A
	if raw[1] != 0x00 || raw[2] != 0x00 || raw[3] != 0x00 || raw[4] != 0x2A {
		t.Fatalf("schema ID header mismatch: got %v", raw[1:5])
	}

	// Deserialize and verify round-trip
	got, err := r.Deserialize(raw)
	if err != nil {
		t.Fatalf("Deserialize: %v", err)
	}

	if got["entity"] != "AAPL" {
		t.Fatalf("expected entity=AAPL, got %v", got["entity"])
	}
	if got["tier"] != "flash" {
		t.Fatalf("expected tier=flash, got %v", got["tier"])
	}
	if got["kind"] != "volume_spike" {
		t.Fatalf("expected kind=volume_spike, got %v", got["kind"])
	}
	if got["window_minutes"] != 15 {
		t.Fatalf("expected window_minutes=15, got %v", got["window_minutes"])
	}
}

func TestRegister(t *testing.T) {
	// Mock Schema Registry: responds with {"id": 99}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", req.Method)
		}

		// Verify content type
		ct := req.Header.Get("Content-Type")
		if ct != "application/vnd.schemaregistry.v1+json" {
			t.Errorf("expected content-type application/vnd.schemaregistry.v1+json, got %s", ct)
		}

		// Read body to verify it contains the schema
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Errorf("reading body: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("unmarshal body: %v", err)
		}
		if _, ok := payload["schema"]; !ok {
			t.Error("expected 'schema' field in request body")
		}
		if payload["schemaType"] != "AVRO" {
			t.Errorf("expected schemaType=AVRO, got %v", payload["schemaType"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"id": 99})
	}))
	defer server.Close()

	r, err := schemakit.NewRegistry(server.URL)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if err := r.LoadSchemas("testdata/schemas"); err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}

	s := r.GetSchema("ai8.scanner.gdelt.v1.SignalSurge")
	if s == nil {
		t.Fatal("schema not found")
	}

	err = r.Register(context.Background(), s)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if s.SchemaID != 99 {
		t.Fatalf("expected SchemaID=99 after registration, got %d", s.SchemaID)
	}
}
