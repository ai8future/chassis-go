package kafkakit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// envelope is the internal wire format for events on the bus.
type envelope struct {
	ID         string   `json:"id"`
	Timestamp  int64    `json:"timestamp"`
	Source     string   `json:"source"`
	Subject    string   `json:"subject"`
	TraceID    string   `json:"trace_id"`
	TenantID   string   `json:"tenant_id"`
	Version    string   `json:"version"`
	EntityRefs []string `json:"entity_refs"`
	Data       []byte   `json:"data"`
}

// wrapEnvelope creates a new envelope with a unique ID and current timestamp.
// If the context carries an OTel span, the trace ID is extracted.
func wrapEnvelope(ctx context.Context, source, subject, tenantID string, entityRefs []string, data []byte) (*envelope, error) {
	id, err := generateEventID()
	if err != nil {
		return nil, fmt.Errorf("kafkakit: generate event ID: %w", err)
	}

	traceID := ""
	if span := trace.SpanFromContext(ctx); span.SpanContext().HasTraceID() {
		traceID = span.SpanContext().TraceID().String()
	}

	if entityRefs == nil {
		entityRefs = []string{}
	}

	return &envelope{
		ID:         id,
		Timestamp:  time.Now().UnixMilli(),
		Source:     source,
		Subject:    subject,
		TraceID:    traceID,
		TenantID:   tenantID,
		Version:    "1.0",
		EntityRefs: entityRefs,
		Data:       data,
	}, nil
}

// unwrapEnvelope deserializes raw bytes into an envelope.
func unwrapEnvelope(raw []byte) (*envelope, error) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("kafkakit: unwrap envelope: %w", err)
	}
	return &env, nil
}

// envelopeToEvent converts an internal envelope to a public Event.
func envelopeToEvent(env *envelope) Event {
	evt := Event{
		ID:         env.ID,
		Timestamp:  time.Unix(0, env.Timestamp*int64(time.Millisecond)),
		Source:     env.Source,
		Subject:    env.Subject,
		TraceID:    env.TraceID,
		TenantID:   env.TenantID,
		Version:    env.Version,
		EntityRefs: env.EntityRefs,
		Raw:        env.Data,
	}

	// Try to parse Data as JSON map
	if len(env.Data) > 0 {
		var data map[string]any
		if err := json.Unmarshal(env.Data, &data); err == nil {
			evt.Data = data
		}
	}

	return evt
}

// generateEventID creates a unique event ID in the format "evt_" + 32 hex chars.
func generateEventID() (string, error) {
	b := make([]byte, 16) // 16 bytes = 32 hex chars
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "evt_" + hex.EncodeToString(b), nil
}
