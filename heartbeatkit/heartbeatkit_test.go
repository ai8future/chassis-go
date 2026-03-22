package heartbeatkit_test

import (
	"context"
	"sync"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v10"
	"github.com/ai8future/chassis-go/v10/heartbeatkit"
)

func init() { chassis.RequireMajor(10) }

// mockPublisher records all Publish calls for verification.
type mockPublisher struct {
	mu       sync.Mutex
	messages []publishedMessage
}

type publishedMessage struct {
	subject string
	data    any
}

func (m *mockPublisher) Publish(_ context.Context, subject string, data any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, publishedMessage{subject: subject, data: data})
	return nil
}

func (m *mockPublisher) getMessages() []publishedMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]publishedMessage, len(m.messages))
	copy(out, m.messages)
	return out
}

// mockPublisherWithStats implements both publisher and statsProvider.
type mockPublisherWithStats struct {
	mockPublisher
}

func (m *mockPublisherWithStats) Stats() struct {
	EventsPublished1h int64
	Errors1h          int64
	LastEventPublished time.Time
} {
	return struct {
		EventsPublished1h int64
		Errors1h          int64
		LastEventPublished time.Time
	}{
		EventsPublished1h: 42,
		Errors1h:          3,
		LastEventPublished: time.Date(2026, 3, 22, 10, 0, 0, 0, time.UTC),
	}
}

func TestStartPublishesAtInterval(t *testing.T) {
	pub := &mockPublisher{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	heartbeatkit.Start(ctx, pub, heartbeatkit.Config{
		Interval:    50 * time.Millisecond,
		ServiceName: "test-svc",
		Version:     "1.0.0",
	})

	time.Sleep(130 * time.Millisecond)
	heartbeatkit.Stop()

	msgs := pub.getMessages()
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 publishes, got %d", len(msgs))
	}
}

func TestSubjectIsHeartbeat(t *testing.T) {
	pub := &mockPublisher{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	heartbeatkit.Start(ctx, pub, heartbeatkit.Config{
		Interval:    50 * time.Millisecond,
		ServiceName: "test-svc",
		Version:     "1.0.0",
	})

	time.Sleep(70 * time.Millisecond)
	heartbeatkit.Stop()

	msgs := pub.getMessages()
	if len(msgs) == 0 {
		t.Fatal("expected at least 1 publish")
	}
	for _, msg := range msgs {
		if msg.subject != "ai8.infra.heartbeat" {
			t.Fatalf("expected subject 'ai8.infra.heartbeat', got %q", msg.subject)
		}
	}
}

func TestPayloadContainsRequiredFields(t *testing.T) {
	pub := &mockPublisher{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	heartbeatkit.Start(ctx, pub, heartbeatkit.Config{
		Interval:    50 * time.Millisecond,
		ServiceName: "test-svc",
		Version:     "1.0.0",
	})

	time.Sleep(70 * time.Millisecond)
	heartbeatkit.Stop()

	msgs := pub.getMessages()
	if len(msgs) == 0 {
		t.Fatal("expected at least 1 publish")
	}

	payload, ok := msgs[0].data.(map[string]any)
	if !ok {
		t.Fatalf("expected payload to be map[string]any, got %T", msgs[0].data)
	}

	requiredKeys := []string{"service", "host", "pid", "uptime_s", "version", "status"}
	for _, key := range requiredKeys {
		if _, exists := payload[key]; !exists {
			t.Errorf("payload missing required key %q", key)
		}
	}

	if payload["service"] != "test-svc" {
		t.Errorf("expected service 'test-svc', got %v", payload["service"])
	}
	if payload["version"] != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %v", payload["version"])
	}
	if payload["status"] != "healthy" {
		t.Errorf("expected status 'healthy', got %v", payload["status"])
	}
}

func TestStopStopsPublishing(t *testing.T) {
	pub := &mockPublisher{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	heartbeatkit.Start(ctx, pub, heartbeatkit.Config{
		Interval:    50 * time.Millisecond,
		ServiceName: "test-svc",
		Version:     "1.0.0",
	})

	time.Sleep(70 * time.Millisecond)
	heartbeatkit.Stop()

	countAfterStop := len(pub.getMessages())
	time.Sleep(120 * time.Millisecond)
	countLater := len(pub.getMessages())

	if countLater != countAfterStop {
		t.Fatalf("expected no new publishes after Stop, got %d more", countLater-countAfterStop)
	}
}

func TestDefaultInterval(t *testing.T) {
	pub := &mockPublisher{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	heartbeatkit.Start(ctx, pub, heartbeatkit.Config{
		ServiceName: "test-svc",
		Version:     "1.0.0",
	})

	// Default is 30s, so after 50ms there should be 0 publishes
	time.Sleep(50 * time.Millisecond)
	heartbeatkit.Stop()

	msgs := pub.getMessages()
	if len(msgs) != 0 {
		t.Fatalf("expected 0 publishes with 30s default interval after 50ms, got %d", len(msgs))
	}
}

func TestContextCancellationStopsPublishing(t *testing.T) {
	pub := &mockPublisher{}
	ctx, cancel := context.WithCancel(context.Background())

	heartbeatkit.Start(ctx, pub, heartbeatkit.Config{
		Interval:    50 * time.Millisecond,
		ServiceName: "test-svc",
		Version:     "1.0.0",
	})

	time.Sleep(70 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)

	countAfterCancel := len(pub.getMessages())
	time.Sleep(120 * time.Millisecond)
	countLater := len(pub.getMessages())

	// Clean up
	heartbeatkit.Stop()

	if countLater != countAfterCancel {
		t.Fatalf("expected no new publishes after context cancel, got %d more", countLater-countAfterCancel)
	}
}

func TestStatsProviderIncludesStats(t *testing.T) {
	pub := &mockPublisherWithStats{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	heartbeatkit.Start(ctx, pub, heartbeatkit.Config{
		Interval:    50 * time.Millisecond,
		ServiceName: "test-svc",
		Version:     "1.0.0",
	})

	time.Sleep(70 * time.Millisecond)
	heartbeatkit.Stop()

	msgs := pub.getMessages()
	if len(msgs) == 0 {
		t.Fatal("expected at least 1 publish")
	}

	payload, ok := msgs[0].data.(map[string]any)
	if !ok {
		t.Fatalf("expected payload to be map[string]any, got %T", msgs[0].data)
	}

	if payload["events_published_1h"] != int64(42) {
		t.Errorf("expected events_published_1h=42, got %v", payload["events_published_1h"])
	}
	if payload["errors_1h"] != int64(3) {
		t.Errorf("expected errors_1h=3, got %v", payload["errors_1h"])
	}
	if _, exists := payload["last_event_published"]; !exists {
		t.Error("expected last_event_published in payload")
	}
}
