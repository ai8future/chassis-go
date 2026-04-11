package kafkakit

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestConfigEnabled(t *testing.T) {
	cfg := Config{BootstrapServers: "localhost:9092"}
	if !cfg.Enabled() {
		t.Fatal("expected Config with BootstrapServers to be enabled")
	}
}

func TestConfigDisabled(t *testing.T) {
	cfg := Config{}
	if cfg.Enabled() {
		t.Fatal("expected Config without BootstrapServers to be disabled")
	}
}

func TestWrapUnwrapEnvelope(t *testing.T) {
	ctx := context.Background()
	data := []byte(`{"entity":"AAPL","tier":"flash"}`)

	env, err := wrapEnvelope(ctx, "scanner-svc", "ai8.scanner.gdelt.signal.surge", "tenant-abc", []string{"AAPL"}, data)
	if err != nil {
		t.Fatalf("wrapEnvelope error: %v", err)
	}

	// Verify ID prefix
	if !strings.HasPrefix(env.ID, "evt_") {
		t.Fatalf("expected ID to start with evt_, got %s", env.ID)
	}
	// evt_ + 32 hex chars = 36 total
	if len(env.ID) != 36 {
		t.Fatalf("expected ID length 36 (evt_ + 32 hex), got %d: %s", len(env.ID), env.ID)
	}

	// Verify source
	if env.Source != "scanner-svc" {
		t.Fatalf("expected source=scanner-svc, got %s", env.Source)
	}

	// Verify subject
	if env.Subject != "ai8.scanner.gdelt.signal.surge" {
		t.Fatalf("expected subject=ai8.scanner.gdelt.signal.surge, got %s", env.Subject)
	}

	// Verify tenant
	if env.TenantID != "tenant-abc" {
		t.Fatalf("expected tenant_id=tenant-abc, got %s", env.TenantID)
	}

	// Verify timestamp is recent (within last 5 seconds)
	ts := time.Unix(0, env.Timestamp*int64(time.Millisecond))
	if time.Since(ts) > 5*time.Second {
		t.Fatalf("timestamp %v is not recent", ts)
	}

	// Verify entity refs
	if len(env.EntityRefs) != 1 || env.EntityRefs[0] != "AAPL" {
		t.Fatalf("expected entity_refs=[AAPL], got %v", env.EntityRefs)
	}

	// Verify data round-trip
	if string(env.Data) != string(data) {
		t.Fatalf("expected data=%s, got %s", string(data), string(env.Data))
	}
}

func TestWrapEnvelope_UniqueIDs(t *testing.T) {
	ctx := context.Background()
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		env, err := wrapEnvelope(ctx, "svc", "subject", "tenant", nil, nil)
		if err != nil {
			t.Fatalf("wrapEnvelope error: %v", err)
		}
		if ids[env.ID] {
			t.Fatalf("duplicate ID generated: %s", env.ID)
		}
		ids[env.ID] = true
	}
}

func TestTenantFilter_OwnTenant(t *testing.T) {
	f := NewTenantFilter("tenant-abc")
	if !f.ShouldDeliver("tenant-abc") {
		t.Fatal("expected own tenant to be delivered")
	}
}

func TestTenantFilter_SharedTenant(t *testing.T) {
	f := NewTenantFilter("tenant-abc")
	if !f.ShouldDeliver("shared") {
		t.Fatal("expected 'shared' tenant to be delivered")
	}
}

func TestTenantFilter_OtherTenant(t *testing.T) {
	f := NewTenantFilter("tenant-abc")
	if f.ShouldDeliver("tenant-xyz") {
		t.Fatal("expected other tenant to be blocked")
	}
}

func TestTenantFilter_GrantedTenant(t *testing.T) {
	f := NewTenantFilter("tenant-abc")
	f.Grant("tenant-xyz")
	if !f.ShouldDeliver("tenant-xyz") {
		t.Fatal("expected granted tenant to be delivered")
	}
}

func TestTenantFilter_EmptyTenantDelivers(t *testing.T) {
	f := NewTenantFilter("tenant-abc")
	// Events with no tenant ID should be delivered (system events)
	if !f.ShouldDeliver("") {
		t.Fatal("expected empty tenant to be delivered")
	}
}

func TestPublisherStats(t *testing.T) {
	var stats publisherStats

	stats.incPublished()
	stats.incPublished()
	stats.incPublished()
	stats.incErrors()

	s := stats.snapshot()
	if s.EventsPublishedTotal != 3 {
		t.Fatalf("expected 3 published, got %d", s.EventsPublishedTotal)
	}
	if s.ErrorsTotal != 1 {
		t.Fatalf("expected 1 error, got %d", s.ErrorsTotal)
	}
	if s.LastEventPublished.IsZero() {
		t.Fatal("expected non-zero LastEventPublished")
	}
}

func TestSubscribePattern(t *testing.T) {
	tests := []struct {
		pattern string
		subject string
		match   bool
	}{
		// Exact match
		{"ai8.scanner.gdelt.signal.surge", "ai8.scanner.gdelt.signal.surge", true},
		// Wildcard match — > means "match rest"
		{"ai8.scanner.>", "ai8.scanner.gdelt.signal.surge", true},
		// Wildcard at root
		{"ai8.>", "ai8.scanner.gdelt.signal.surge", true},
		// No match
		{"ai8.scanner.gdelt.signal.surge", "ai8.scanner.gdelt.signal.drop", false},
		// Wildcard no match — different prefix
		{"ai8.scanner.>", "ai8.ingest.gdelt.raw", false},
		// Exact vs wildcard
		{"ai8.scanner.gdelt.signal.surge", "ai8.scanner.>", false},
	}

	for _, tt := range tests {
		got := matchPattern(tt.pattern, tt.subject)
		if got != tt.match {
			t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.pattern, tt.subject, got, tt.match)
		}
	}
}

func TestEvent_Ack(t *testing.T) {
	e := Event{ID: "evt_test123456"}
	if err := e.Ack(); err != nil {
		t.Fatalf("Ack() returned error: %v", err)
	}
}

func TestEvent_Reject(t *testing.T) {
	e := Event{ID: "evt_test123456"}
	if err := e.Reject(); err != nil {
		t.Fatalf("Reject() returned error: %v", err)
	}
}

func TestEvent_Header(t *testing.T) {
	e := Event{ID: "evt_test123456"}
	// Header returns empty string for now
	if h := e.Header("some-key"); h != "" {
		t.Fatalf("expected empty header, got %q", h)
	}
}

func TestOutboundEvent(t *testing.T) {
	evt := OutboundEvent{
		Subject:    "ai8.scanner.gdelt.signal.surge",
		Data:       map[string]any{"entity": "AAPL"},
		EntityRefs: []string{"AAPL"},
	}
	if evt.Subject != "ai8.scanner.gdelt.signal.surge" {
		t.Fatalf("unexpected subject: %s", evt.Subject)
	}
}

func TestDLQTopic(t *testing.T) {
	topic := dlqTopic("ai8.scanner.gdelt.signal.surge")
	expected := "ai8._dlq.ai8.scanner.gdelt.signal.surge"
	if topic != expected {
		t.Fatalf("expected DLQ topic %q, got %q", expected, topic)
	}
}

func TestEnvelopeToEvent(t *testing.T) {
	env := &envelope{
		ID:         "evt_abc123def456",
		Timestamp:  time.Now().UnixMilli(),
		Source:     "scanner-svc",
		Subject:    "ai8.scanner.gdelt.signal.surge",
		TraceID:    "trace-123",
		TenantID:   "tenant-abc",
		Version:    "1.0",
		EntityRefs: []string{"AAPL"},
		Data:       []byte(`{"entity":"AAPL"}`),
	}

	evt := envelopeToEvent(env)
	if evt.ID != env.ID {
		t.Fatalf("ID mismatch: %s vs %s", evt.ID, env.ID)
	}
	if evt.Source != env.Source {
		t.Fatalf("Source mismatch: %s vs %s", evt.Source, env.Source)
	}
	if evt.Subject != env.Subject {
		t.Fatalf("Subject mismatch: %s vs %s", evt.Subject, env.Subject)
	}
	if evt.TraceID != env.TraceID {
		t.Fatalf("TraceID mismatch: %s vs %s", evt.TraceID, env.TraceID)
	}
	if evt.TenantID != env.TenantID {
		t.Fatalf("TenantID mismatch: %s vs %s", evt.TenantID, env.TenantID)
	}
	if string(evt.Raw) != string(env.Data) {
		t.Fatalf("Raw mismatch")
	}
	if evt.Data == nil {
		t.Fatal("expected non-nil Data map")
	}
	if evt.Data["entity"] != "AAPL" {
		t.Fatalf("expected entity=AAPL, got %v", evt.Data["entity"])
	}
}
