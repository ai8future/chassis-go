package announcekit_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	chassis "github.com/ai8future/chassis-go/v9"
	"github.com/ai8future/chassis-go/v9/announcekit"
)

func init() { chassis.RequireMajor(9) }

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

func TestStartedPublishesCorrectSubject(t *testing.T) {
	pub := &mockPublisher{}
	announcekit.SetServiceName("my-svc")

	err := announcekit.Started(context.Background(), pub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := pub.getMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].subject != "ai8.infra.my-svc.lifecycle.started" {
		t.Errorf("expected subject 'ai8.infra.my-svc.lifecycle.started', got %q", msgs[0].subject)
	}

	payload := msgs[0].data.(map[string]any)
	if payload["service"] != "my-svc" {
		t.Errorf("expected service 'my-svc', got %v", payload["service"])
	}
	if payload["state"] != "started" {
		t.Errorf("expected state 'started', got %v", payload["state"])
	}
}

func TestReadyPublishesCorrectSubject(t *testing.T) {
	pub := &mockPublisher{}
	announcekit.SetServiceName("my-svc")

	err := announcekit.Ready(context.Background(), pub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := pub.getMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].subject != "ai8.infra.my-svc.lifecycle.ready" {
		t.Errorf("expected subject 'ai8.infra.my-svc.lifecycle.ready', got %q", msgs[0].subject)
	}
}

func TestStoppingPublishesCorrectSubject(t *testing.T) {
	pub := &mockPublisher{}
	announcekit.SetServiceName("my-svc")

	err := announcekit.Stopping(context.Background(), pub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := pub.getMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].subject != "ai8.infra.my-svc.lifecycle.stopping" {
		t.Errorf("expected subject 'ai8.infra.my-svc.lifecycle.stopping', got %q", msgs[0].subject)
	}
}

func TestFailedPublishesCorrectSubjectAndIncludesError(t *testing.T) {
	pub := &mockPublisher{}
	announcekit.SetServiceName("my-svc")

	testErr := errors.New("database connection failed")
	err := announcekit.Failed(context.Background(), pub, testErr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := pub.getMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].subject != "ai8.infra.my-svc.lifecycle.failed" {
		t.Errorf("expected subject 'ai8.infra.my-svc.lifecycle.failed', got %q", msgs[0].subject)
	}

	payload := msgs[0].data.(map[string]any)
	if payload["error"] != "database connection failed" {
		t.Errorf("expected error 'database connection failed', got %v", payload["error"])
	}
	if payload["state"] != "failed" {
		t.Errorf("expected state 'failed', got %v", payload["state"])
	}
}

func TestJobStartedPublishesCorrectSubject(t *testing.T) {
	pub := &mockPublisher{}
	announcekit.SetServiceName("my-svc")

	err := announcekit.JobStarted(context.Background(), pub, "data-sync", "job-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := pub.getMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].subject != "ai8.infra.my-svc.job.started" {
		t.Errorf("expected subject 'ai8.infra.my-svc.job.started', got %q", msgs[0].subject)
	}

	payload := msgs[0].data.(map[string]any)
	if payload["job_name"] != "data-sync" {
		t.Errorf("expected job_name 'data-sync', got %v", payload["job_name"])
	}
	if payload["job_id"] != "job-123" {
		t.Errorf("expected job_id 'job-123', got %v", payload["job_id"])
	}
	if payload["state"] != "started" {
		t.Errorf("expected state 'started', got %v", payload["state"])
	}
}

func TestJobCompletePublishesCorrectSubject(t *testing.T) {
	pub := &mockPublisher{}
	announcekit.SetServiceName("my-svc")

	result := map[string]any{"records_processed": 100}
	err := announcekit.JobComplete(context.Background(), pub, "data-sync", "job-123", result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := pub.getMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].subject != "ai8.infra.my-svc.job.complete" {
		t.Errorf("expected subject 'ai8.infra.my-svc.job.complete', got %q", msgs[0].subject)
	}

	payload := msgs[0].data.(map[string]any)
	if payload["state"] != "complete" {
		t.Errorf("expected state 'complete', got %v", payload["state"])
	}
	if payload["result"] == nil {
		t.Error("expected result in payload")
	}
}

func TestJobFailedPublishesCorrectSubject(t *testing.T) {
	pub := &mockPublisher{}
	announcekit.SetServiceName("my-svc")

	testErr := errors.New("timeout exceeded")
	err := announcekit.JobFailed(context.Background(), pub, "data-sync", "job-123", testErr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := pub.getMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].subject != "ai8.infra.my-svc.job.failed" {
		t.Errorf("expected subject 'ai8.infra.my-svc.job.failed', got %q", msgs[0].subject)
	}

	payload := msgs[0].data.(map[string]any)
	if payload["error"] != "timeout exceeded" {
		t.Errorf("expected error 'timeout exceeded', got %v", payload["error"])
	}
	if payload["job_name"] != "data-sync" {
		t.Errorf("expected job_name 'data-sync', got %v", payload["job_name"])
	}
	if payload["job_id"] != "job-123" {
		t.Errorf("expected job_id 'job-123', got %v", payload["job_id"])
	}
}

func TestAllLifecycleSubjectsFollowPattern(t *testing.T) {
	pub := &mockPublisher{}
	announcekit.SetServiceName("test-svc")
	ctx := context.Background()

	_ = announcekit.Started(ctx, pub)
	_ = announcekit.Ready(ctx, pub)
	_ = announcekit.Stopping(ctx, pub)
	_ = announcekit.Failed(ctx, pub, errors.New("err"))

	msgs := pub.getMessages()
	expected := []string{
		"ai8.infra.test-svc.lifecycle.started",
		"ai8.infra.test-svc.lifecycle.ready",
		"ai8.infra.test-svc.lifecycle.stopping",
		"ai8.infra.test-svc.lifecycle.failed",
	}

	if len(msgs) != len(expected) {
		t.Fatalf("expected %d messages, got %d", len(expected), len(msgs))
	}

	for i, msg := range msgs {
		if msg.subject != expected[i] {
			t.Errorf("message %d: expected subject %q, got %q", i, expected[i], msg.subject)
		}
	}
}

func TestAllJobSubjectsFollowPattern(t *testing.T) {
	pub := &mockPublisher{}
	announcekit.SetServiceName("test-svc")
	ctx := context.Background()

	_ = announcekit.JobStarted(ctx, pub, "job", "id")
	_ = announcekit.JobComplete(ctx, pub, "job", "id", nil)
	_ = announcekit.JobFailed(ctx, pub, "job", "id", errors.New("err"))

	msgs := pub.getMessages()
	expected := []string{
		"ai8.infra.test-svc.job.started",
		"ai8.infra.test-svc.job.complete",
		"ai8.infra.test-svc.job.failed",
	}

	if len(msgs) != len(expected) {
		t.Fatalf("expected %d messages, got %d", len(expected), len(msgs))
	}

	for i, msg := range msgs {
		if msg.subject != expected[i] {
			t.Errorf("message %d: expected subject %q, got %q", i, expected[i], msg.subject)
		}
	}
}
