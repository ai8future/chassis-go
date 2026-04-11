package kafkakit

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestWrapEnvelope_NilEntityRefs(t *testing.T) {
	env, err := wrapEnvelope(context.Background(), "svc", "sub", "t", nil, nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if env.EntityRefs == nil {
		t.Error("EntityRefs should not be nil, expect empty slice")
	}
	if len(env.EntityRefs) != 0 {
		t.Errorf("EntityRefs len = %d, want 0", len(env.EntityRefs))
	}
}

func TestUnwrapEnvelope_Roundtrip(t *testing.T) {
	original := &envelope{
		ID:         "evt_aabbccddee01",
		Timestamp:  time.Now().UnixMilli(),
		Source:     "test-svc",
		Subject:    "order.placed",
		TenantID:   "acme",
		Version:    "1.0",
		EntityRefs: []string{"order-123"},
		Data:       []byte(`{"amount":42.50}`),
	}

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	got, err := unwrapEnvelope(raw)
	if err != nil {
		t.Fatalf("unwrapEnvelope error: %v", err)
	}
	if got.ID != original.ID {
		t.Errorf("ID = %q, want %q", got.ID, original.ID)
	}
	if got.Subject != original.Subject {
		t.Errorf("Subject = %q, want %q", got.Subject, original.Subject)
	}
}

func TestUnwrapEnvelope_InvalidJSON(t *testing.T) {
	_, err := unwrapEnvelope([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestEnvelopeToEvent_NonJSONData(t *testing.T) {
	env := &envelope{
		ID:   "evt_aabb",
		Data: []byte("plain text, not JSON"),
	}
	evt := envelopeToEvent(env)
	if evt.Data != nil {
		t.Errorf("Data should be nil for non-JSON data, got %v", evt.Data)
	}
	if string(evt.Raw) != "plain text, not JSON" {
		t.Errorf("Raw = %q, want original bytes", string(evt.Raw))
	}
}

func TestGenerateEventID_Format(t *testing.T) {
	id, err := generateEventID()
	if err != nil {
		t.Fatalf("generateEventID() error: %v", err)
	}
	// "evt_" = 4 chars, 16 bytes = 32 hex chars, total 36
	if len(id) != 36 {
		t.Errorf("ID length = %d, want 36: %q", len(id), id)
	}
}
